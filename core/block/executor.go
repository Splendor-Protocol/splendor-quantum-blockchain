// Package block handles block production, execution, and chain storage.
package block

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

const (
	MaxTxPerBlock = 10_000
	prefixBlock   = "block:"
	prefixHeight  = "height:"
	keyLatest     = "latest"

	// MaxBlockGas caps the total VM gas a block may consume (mirrors
	// config.MaxBlockGas). Without enforcement, 10k txs * MaxGasPerCall (10M) =
	// 100B gas of compute per block, letting a funded attacker force blocks that
	// take far longer than the target interval. ProduceBlock stops including txs
	// past this budget; ExecuteBlock rejects a block that exceeds it.
	MaxBlockGas uint64 = 500_000_000_000
)

// Executor executes blocks and manages the chain database.
// It supports pluggable middleware hooks and generates transaction receipts.
type Executor struct {
	// mu serializes whole block transitions. ExecuteBlock (inbound/sync apply)
	// and ProduceBlock (validator) both read-modify-Commit the shared StateDB;
	// without this lock two concurrent transitions interleave on the shared
	// dirty map and compute a state root over each other's half-written accounts.
	mu sync.Mutex

	state    *state.StateDB
	chainDB  *leveldb.DB
	vmEngine *vm.Engine
	hooks   *HookRegistry // optional; nil = no hooks
	txIndex receiptStorer // optional; nil = don't store receipts
}

// receiptStorer is satisfied by *state.TxIndexStore.
// Using an interface keeps executor.go from importing the state package's txindex.
type receiptStorer interface {
	StoreReceipt(r *types.Receipt) error
	StoreBlockReceipts(receipts []*types.Receipt) error
	IndexBlock(b *types.Block) error
}

// NewExecutor creates a new block executor with default settings.
func NewExecutor(stateDB *state.StateDB, chainDBPath string) (*Executor, error) {
	db, err := leveldb.OpenFile(chainDBPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open chain database: %w", err)
	}
	return &Executor{
		state:    stateDB,
		chainDB:  db,
		vmEngine: vm.NewEngine(stateDB),
	}, nil
}

// WithHooks attaches a hook registry to the executor.
func (e *Executor) WithHooks(h *HookRegistry) *Executor {
	e.hooks = h
	return e
}

// WithTxIndex attaches a receipt/tx index store.
func (e *Executor) WithTxIndex(idx receiptStorer) *Executor {
	e.txIndex = idx
	return e
}

// Close closes the chain database.
func (e *Executor) Close() error {
	return e.chainDB.Close()
}

// ExecuteBlock applies all transactions in a block to the state.
// It verifies signatures, nonces, balances, runs hooks, generates receipts,
// and stores the block.
//
// IMPORTANT: ExecuteBlock MUST mirror ProduceBlock's state mutations exactly,
// including crediting transaction fees to b.Header.ValidatorAddr. Omitting the
// fee credit causes state root mismatch on every block with transactions.
func (e *Executor) ExecuteBlock(b *types.Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Info().
		Uint64("height", b.Header.Height).
		Int("txs", len(b.Transactions)).
		Msg("executing block")

	// Chain-continuity checks before any state mutation.
	if err := e.verifyBlockLinkage(b); err != nil {
		return fmt.Errorf("block linkage rejected: %w", err)
	}

	// Run pre-block hooks
	if e.hooks != nil {
		if err := e.hooks.RunPreBlock(b); err != nil {
			return fmt.Errorf("pre-block hook rejected block: %w", err)
		}
	}

	var receipts []*types.Receipt
	totalFees := new(bigInt)
	var blockGas uint64

	for i, tx := range b.Transactions {
		receipt, err := e.executeTxWithReceipt(tx, uint32(i), b.Header.Height, b.Header.Timestamp, b.Hash)
		if err != nil {
			return fmt.Errorf("tx %d failed: %w", i, err)
		}
		receipts = append(receipts, receipt)

		// Reject a block that exceeds the consensus gas budget (defence against a
		// producer packing far more compute than the interval can process).
		blockGas += receipt.GasUsed
		if blockGas > MaxBlockGas {
			e.state.DiscardPending()
			return fmt.Errorf("block exceeds gas budget: %d > %d", blockGas, MaxBlockGas)
		}

		// Accumulate fees for validator credit (mirrors ProduceBlock).
		// Blocks from the validator only contain txs that executed successfully,
		// so all fees in a synced block should be credited to the validator.
		totalFees.Add(totalFees, tx.Fee)

		// Run post-tx hooks (non-fatal)
		if e.hooks != nil {
			e.hooks.RunPostTx(tx, receipt)
		}
	}

	// Credit accumulated fees to the block's validator address.
	// This MUST match what ProduceBlock does or state roots will diverge on
	// any block that contains transactions.
	if totalFees.Sign() > 0 {
		va, err := e.state.GetAccount(b.Header.ValidatorAddr)
		if err != nil {
			e.state.DiscardPending()
			return fmt.Errorf("fee credit: get validator account: %w", err)
		}
		va.AddBalance(totalFees)
		e.state.SetAccount(va)
	}

	// Verify the declared state root BEFORE persisting.
	stateRoot, err := e.state.PendingStateRoot()
	if err != nil {
		e.state.DiscardPending()
		return fmt.Errorf("state root computation failed: %w", err)
	}
	if stateRoot != b.Header.StateRoot {
		e.state.DiscardPending()
		return fmt.Errorf("state root mismatch: computed %x, header %x",
			stateRoot[:8], b.Header.StateRoot[:8])
	}

	// Root matches — durably commit.
	if _, err := e.state.Commit(); err != nil {
		return fmt.Errorf("state commit failed: %w", err)
	}

	// Store block
	if err := e.storeBlock(b); err != nil {
		return fmt.Errorf("block store failed: %w", err)
	}

	// Index block transactions and store receipts
	if e.txIndex != nil {
		if err := e.txIndex.IndexBlock(b); err != nil {
			log.Warn().Err(err).Msg("tx index: failed to index block")
		}
		if err := e.txIndex.StoreBlockReceipts(receipts); err != nil {
			log.Warn().Err(err).Msg("tx index: failed to store receipts")
		}
	}

	// Run post-block hooks (non-fatal)
	if e.hooks != nil {
		e.hooks.RunPostBlock(b, stateRoot)
	}

	return nil
}

// executeTxWithReceipt processes a single transaction and returns a receipt.
func (e *Executor) executeTxWithReceipt(tx *types.Transaction, txIndex uint32, blockHeight uint64, blockTime int64, blockHash [32]byte) (*types.Receipt, error) {
	receipt, err := types.NewReceipt(tx, txIndex, blockHeight, blockHash)
	if err != nil {
		return nil, fmt.Errorf("receipt init failed: %w", err)
	}

	if e.hooks != nil {
		if err := e.hooks.RunPreTx(tx, blockHeight); err != nil {
			receipt.SetFailed(fmt.Sprintf("pre-tx hook: %v", err))
			return receipt, nil
		}
	}

	// Execute inside a snapshot so a FAILED tx leaves NO state residue.
	// execCall/execDeploy/execX402 deduct the fee and bump the nonce BEFORE the
	// operation; without this revert, a failed op keeps that dirty mutation, so
	// the block's account state (and thus the state root) reflects a tx that the
	// producer excluded — producer and followers then compute divergent roots.
	snap := e.state.Snapshot()
	if err := e.executeTx(tx, blockHeight, blockTime, receipt); err != nil {
		e.state.RevertToSnapshot(snap)
		receipt.SetFailed(err.Error())
		log.Warn().Err(err).
			Str("from", tx.From.String()).
			Uint64("nonce", tx.Nonce).
			Msg("transaction failed")
		return receipt, nil
	}
	e.state.CommitSnapshot(snap)

	return receipt, nil
}

// executeTx processes a single transaction, updating receipt with logs and gas.
//
// It recovers from panics in the VM / native-module execution: crafted bytecode
// (e.g. a negative make() size from a 256-bit length, or an unbounded EXP) could
// otherwise panic and CRASH the validator + every follower — halting the chain.
// A recovered panic becomes an error, which the caller's snapshot reverts, so the
// tx is recorded as failed and the block continues.
func (e *Executor) executeTx(tx *types.Transaction, blockHeight uint64, blockTime int64, receipt *types.Receipt) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tx execution panic: %v", r)
		}
	}()
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	fromAcc, err := e.state.GetAccount(tx.From)
	if err != nil {
		return err
	}

	var pubKeyBytes []byte
	if fromAcc.HasPublicKey() {
		pubKeyBytes = fromAcc.PublicKey
	} else if len(tx.PublicKey) == qcrypto.PublicKeySize {
		pub, err := qcrypto.PublicKeyFromBytes(tx.PublicKey)
		if err != nil {
			return fmt.Errorf("invalid public key: %w", err)
		}
		if qcrypto.DeriveAddress(pub) != tx.From {
			return fmt.Errorf("public key does not match from address")
		}
		pubKeyBytes = tx.PublicKey
		fromAcc.PublicKey = tx.PublicKey
		e.state.SetAccount(fromAcc)
	} else {
		return types.ErrNoPublicKey
	}

	if err := tx.VerifySignature(pubKeyBytes); err != nil {
		return types.ErrInvalidSignature
	}

	if tx.Nonce != fromAcc.Nonce {
		return fmt.Errorf("%w: want %d got %d", types.ErrInvalidNonce, fromAcc.Nonce, tx.Nonce)
	}

	total := new(types.BigInt).Add(tx.Amount, tx.Fee)
	if fromAcc.Balance.Cmp(total) < 0 {
		return types.ErrInsufficientBalance
	}

	switch tx.Type {
	case types.TxTransfer:
		return e.execTransfer(tx, fromAcc, receipt)
	case types.TxDeploy:
		return e.execDeploy(tx, fromAcc, receipt)
	case types.TxCall:
		return e.execCall(tx, fromAcc, receipt, blockHeight, blockTime)
	case types.TxX402:
		return e.execX402(tx, fromAcc, receipt, blockHeight)
	default:
		return fmt.Errorf("unknown tx type 0x%02x", tx.Type)
	}
}

func (e *Executor) execTransfer(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt) error {
	total := addBig(tx.Amount, tx.Fee)
	if err := fromAcc.SubBalance(total); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	toAcc, err := e.state.GetAccount(tx.To)
	if err != nil {
		return err
	}
	toAcc.AddBalance(tx.Amount)
	e.state.SetAccount(toAcc)

	log.Debug().
		Str("from", tx.From.String()).
		Str("to", tx.To.String()).
		Str("amount", tx.Amount.String()).
		Msg("transfer executed")
	return nil
}

func (e *Executor) execDeploy(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt) error {
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	contractAddr, err := e.vmEngine.Deploy(tx.From, tx.Data, tx.Nonce)
	if err != nil {
		return fmt.Errorf("deploy failed: %w", err)
	}

	receipt.ContractAddress = &contractAddr

	log.Debug().
		Str("deployer", tx.From.String()).
		Str("contract", contractAddr.String()).
		Msg("contract deployed")
	return nil
}

func (e *Executor) execCall(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64, blockTime int64) error {
	if tx.To == vm.ModuleRouterAddress {
		if err := fromAcc.SubBalance(tx.Fee); err != nil {
			return err
		}
		fromAcc.IncrNonce()
		e.state.SetAccount(fromAcc)
		return vm.DispatchModuleCall(e.state, tx.From, tx.Nonce, tx.Data, receipt)
	}

	// The fee is charged up-front as gas (kept even if the call reverts). The
	// call value is NOT debited here — it moves sender->contract only if the
	// call succeeds (see below). Previously tx.Amount was debited from the
	// sender but never credited to tx.To, so the coins were burned out of total
	// supply on every value-bearing contract call.
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	result, err := e.vmEngine.CallWithContext(
		tx.From, tx.From, tx.To, tx.Data, tx.Amount,
		blockHeight, blockTime, false,
	)
	if err != nil {
		return fmt.Errorf("call failed: %w", err)
	}

	reverted := result != nil && result.Reverted
	if result != nil {
		receipt.GasUsed = result.GasUsed
		for _, l := range result.Logs {
			receipt.AddLog(l)
		}
		if reverted {
			receipt.SetFailed("execution reverted")
		}
	}

	// Credit the call value to the contract on success. On revert the value
	// stays with the sender; on a hard error the enclosing per-tx snapshot
	// reverts the fee debit and nonce bump too. Transfer re-checks the sender's
	// balance (which still covers tx.Amount after the fee debit, since the
	// pre-flight check required Amount+Fee) and handles the from==to case.
	if !reverted && tx.Amount != nil && tx.Amount.Sign() > 0 {
		if err := e.state.Transfer(tx.From, tx.To, tx.Amount); err != nil {
			return fmt.Errorf("call value transfer: %w", err)
		}
	}

	log.Debug().
		Str("caller", tx.From.String()).
		Str("contract", tx.To.String()).
		Uint64("gas_used", receipt.GasUsed).
		Msg("contract called")
	return nil
}

func (e *Executor) execX402(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64) error {
	// Replay guard: reject a reused X402 payment nonce.
	nonceKey := qcrypto.Hash32(append(tx.From[:], tx.Data...))
	if used, err := e.state.IsX402NonceUsed(nonceKey); err != nil {
		return fmt.Errorf("X402 nonce check: %w", err)
	} else if used {
		return fmt.Errorf("X402 nonce already used")
	}

	if err := fromAcc.SubBalance(addBig(tx.Amount, tx.Fee)); err != nil {
		return err
	}
	fromAcc.IncrNonce()

	if tx.To == tx.From {
		// Self-payment: mutate ONE account object. Previously GetAccount(tx.To)
		// returned a stale copy taken before the from-side writes, and writing it
		// last silently discarded the fee/nonce updates.
		fromAcc.AddBalance(tx.Amount)
		e.state.SetAccount(fromAcc)
	} else {
		e.state.SetAccount(fromAcc)
		toAcc, err := e.state.GetAccount(tx.To)
		if err != nil {
			return err
		}
		toAcc.AddBalance(tx.Amount)
		e.state.SetAccount(toAcc)
	}

	if err := e.state.MarkX402NonceUsed(nonceKey, blockHeight); err != nil {
		return fmt.Errorf("X402 nonce: %w", err)
	}

	log.Debug().
		Str("payer", tx.From.String()).
		Str("receiver", tx.To.String()).
		Str("amount", tx.Amount.String()).
		Msg("X402 settled")
	return nil
}

// ProduceBlock creates, executes, and signs a new block.
func (e *Executor) ProduceBlock(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(txs) > MaxTxPerBlock {
		txs = txs[:MaxTxPerBlock]
	}

	// Stamp the block time ONCE and use it for BOTH execution and the header, so a
	// contract reading OpTIMESTAMP sees the same value the producer committed to.
	// (Previously execution used time.Now() at each executeTx, so a follower
	// re-executing at a different wall-clock computed a divergent state root.)
	blockTime := time.Now().Unix()

	var (
		totalFees = new(bigInt)
		executed  []*types.Transaction
		receipts  []*types.Receipt
		blockGas  uint64
	)

	for i, tx := range txs {
		// Snapshot so a skipped (failed) tx leaves NO residue: executeTx deducts
		// fee + bumps nonce before the op, so without reverting, a failed tx that
		// is excluded from `executed` would still dirty the state → the block's
		// root reflects a tx not in Transactions and followers reject it.
		snap := e.state.Snapshot()
		receipt, _ := types.NewReceipt(tx, uint32(i), height, [32]byte{})
		if err := e.executeTx(tx, height, blockTime, receipt); err != nil {
			e.state.RevertToSnapshot(snap)
			log.Warn().Err(err).Msg("skipping invalid tx")
			continue
		}
		// Stop packing once the block gas budget is reached (txs are fee-sorted,
		// so we keep the most valuable ones). Revert this tx's state before break.
		if blockGas+receipt.GasUsed > MaxBlockGas {
			e.state.RevertToSnapshot(snap)
			break
		}
		e.state.CommitSnapshot(snap)
		blockGas += receipt.GasUsed
		totalFees.Add(totalFees, tx.Fee)
		executed = append(executed, tx)
		receipts = append(receipts, receipt)
	}

	// Credit fees to validator
	if totalFees.Sign() > 0 {
		va, err := e.state.GetAccount(validatorAddr)
		if err != nil {
			return nil, fmt.Errorf("validator account: %w", err)
		}
		va.AddBalance(totalFees)
		e.state.SetAccount(va)
	}

	stateRoot, err := e.state.Commit()
	if err != nil {
		return nil, fmt.Errorf("state commit: %w", err)
	}

	txMerkleRoot, err := types.ComputeTxMerkleRoot(executed)
	if err != nil {
		return nil, fmt.Errorf("merkle root: %w", err)
	}

	header := types.BlockHeader{
		Height:        height,
		Timestamp:     blockTime,
		ChainID:       types.ChainID,
		PrevHash:      prevHash,
		TxMerkleRoot:  txMerkleRoot,
		StateRoot:     stateRoot,
		ValidatorAddr: validatorAddr,
		TxCount:       uint32(len(executed)),
	}

	b := &types.Block{Header: header, Transactions: executed}

	hash, err := b.ComputeHash()
	if err != nil {
		return nil, fmt.Errorf("block hash: %w", err)
	}
	b.Hash = hash

	if err := b.SignBlock(validatorPrivKey); err != nil {
		return nil, fmt.Errorf("block sign: %w", err)
	}

	if err := e.storeBlock(b); err != nil {
		return nil, fmt.Errorf("store block: %w", err)
	}

	if e.txIndex != nil {
		for _, r := range receipts {
			r.BlockHash = hash
		}
		_ = e.txIndex.IndexBlock(b)
		_ = e.txIndex.StoreBlockReceipts(receipts)
	}

	log.Info().
		Uint64("height", height).
		Int("txs", len(executed)).
		Str("hash", fmt.Sprintf("%x", hash[:8])).
		Msg("block produced and signed")

	return b, nil
}

// ── Chain DB helpers ──────────────────────────────────────────────────────────

func (e *Executor) storeBlock(b *types.Block) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	hashHex := fmt.Sprintf("%x", b.Hash[:])
	heightKey := fmt.Sprintf("%s%d", prefixHeight, b.Header.Height)

	batch := new(leveldb.Batch)
	batch.Put([]byte(prefixBlock+hashHex), data)
	batch.Put([]byte(heightKey), []byte(hashHex))
	if err := e.chainDB.Write(batch, nil); err != nil {
		return err
	}

	if curH, ok := e.latestHeightRaw(); ok && b.Header.Height <= curH {
		return nil
	}
	latestData, _ := json.Marshal(map[string]interface{}{
		"height": b.Header.Height,
		"hash":   hashHex,
	})
	return e.chainDB.Put([]byte(keyLatest), latestData, nil)
}

func (e *Executor) latestHeightRaw() (uint64, bool) {
	data, err := e.chainDB.Get([]byte(keyLatest), nil)
	if err != nil {
		return 0, false
	}
	var info struct {
		Height uint64 `json:"height"`
	}
	if json.Unmarshal(data, &info) != nil {
		return 0, false
	}
	return info.Height, true
}

func (e *Executor) verifyBlockLinkage(b *types.Block) error {
	if b.Header.ChainID != types.ChainID {
		return fmt.Errorf("wrong chain id: block %q, node %q", b.Header.ChainID, types.ChainID)
	}

	latest, err := e.LatestBlock()
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}

	if b.Header.Height == 0 {
		if latest != nil {
			return fmt.Errorf("genesis rejected: chain already has a head at height %d", latest.Header.Height)
		}
		return nil
	}

	if latest == nil {
		if b.Header.Height != 1 {
			return fmt.Errorf("cannot apply block %d to an empty chain (expected block 1)", b.Header.Height)
		}
		return nil
	}

	if b.Header.Height != latest.Header.Height+1 {
		return fmt.Errorf("non-contiguous height: got %d, expected %d", b.Header.Height, latest.Header.Height+1)
	}
	if b.Header.PrevHash != latest.Hash {
		return fmt.Errorf("prev-hash mismatch at height %d", b.Header.Height)
	}
	return nil
}

func (e *Executor) GetBlock(hashHex string) (*types.Block, error) {
	data, err := e.chainDB.Get([]byte(prefixBlock+hashHex), nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrBlockNotFound
	}
	if err != nil {
		return nil, err
	}
	var b types.Block
	return &b, json.Unmarshal(data, &b)
}

func (e *Executor) GetBlockByHeight(height uint64) (*types.Block, error) {
	hashHex, err := e.chainDB.Get([]byte(fmt.Sprintf("%s%d", prefixHeight, height)), nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrBlockNotFound
	}
	if err != nil {
		return nil, err
	}
	return e.GetBlock(string(hashHex))
}

func (e *Executor) LatestHeight() (uint64, error) {
	data, err := e.chainDB.Get([]byte(keyLatest), nil)
	if err == leveldb.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var info struct {
		Height uint64 `json:"height"`
	}
	return info.Height, json.Unmarshal(data, &info)
}

func (e *Executor) LatestBlock() (*types.Block, error) {
	data, err := e.chainDB.Get([]byte(keyLatest), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var info struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return e.GetBlock(info.Hash)
}

// StoreBlockReceipts exposes batch receipt storage for external callers.
func (e *Executor) StoreBlockReceipts(receipts []*types.Receipt) error {
	if e.txIndex == nil {
		return nil
	}
	return e.txIndex.StoreBlockReceipts(receipts)
}

func addBig(a, b *bigInt) *bigInt {
	return new(bigInt).Add(a, b)
}

type bigInt = types.BigInt
