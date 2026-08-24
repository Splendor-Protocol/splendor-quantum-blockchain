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
	hooks    *HookRegistry  // optional; nil = no hooks
	txIndex  receiptStorer  // optional; nil = don't store receipts
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
// If called, all registered hooks will run before/after each transaction and block.
func (e *Executor) WithHooks(h *HookRegistry) *Executor {
	e.hooks = h
	return e
}

// WithTxIndex attaches a receipt/tx index store.
// If set, receipts are stored after every executed transaction.
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
func (e *Executor) ExecuteBlock(b *types.Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Info().
		Uint64("height", b.Header.Height).
		Int("txs", len(b.Transactions)).
		Msg("executing block")

	// Chain-continuity / anti-rewind checks BEFORE any state mutation. Combined
	// with validator-signature verification on the inbound path, this rejects
	// forged, replayed, out-of-order, duplicate, and cross-chain blocks before
	// they can touch state or move the head.
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

	for i, tx := range b.Transactions {
		receipt, err := e.executeTxWithReceipt(tx, uint32(i), b.Header.Height, b.Hash)
		if err != nil {
			return fmt.Errorf("tx %d failed: %w", i, err)
		}
		receipts = append(receipts, receipt)

		// Run post-tx hooks (non-fatal)
		if e.hooks != nil {
			e.hooks.RunPostTx(tx, receipt)
		}
	}

	// Verify the declared state root BEFORE persisting. PendingStateRoot computes
	// the exact root Commit would produce; on a mismatch we discard the pending
	// writes so a rejected block leaves the committed state untouched. (The old
	// flow committed first and checked after, durably persisting rejected state.)
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
func (e *Executor) executeTxWithReceipt(tx *types.Transaction, txIndex uint32, blockHeight uint64, blockHash [32]byte) (*types.Receipt, error) {
	// Build receipt shell
	receipt, err := types.NewReceipt(tx, txIndex, blockHeight, blockHash)
	if err != nil {
		return nil, fmt.Errorf("receipt init failed: %w", err)
	}

	// Run pre-tx hook
	if e.hooks != nil {
		if err := e.hooks.RunPreTx(tx, blockHeight); err != nil {
			receipt.SetFailed(fmt.Sprintf("pre-tx hook: %v", err))
			return receipt, nil // hook rejection: skip but don't abort block
		}
	}

	if err := e.executeTx(tx, blockHeight, receipt); err != nil {
		receipt.SetFailed(err.Error())
		log.Warn().Err(err).
			Str("from", tx.From.String()).
			Uint64("nonce", tx.Nonce).
			Msg("transaction failed")
		// Return receipt even on failure (records the failure)
		return receipt, nil
	}

	return receipt, nil
}

// executeTx processes a single transaction, updating receipt with logs and gas.
func (e *Executor) executeTx(tx *types.Transaction, blockHeight uint64, receipt *types.Receipt) error {
	// 1. Basic validation
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// 2. Get sender account
	fromAcc, err := e.state.GetAccount(tx.From)
	if err != nil {
		return err
	}

	// 3. Public key registration / lookup
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

	// 4. Signature verification
	if err := tx.VerifySignature(pubKeyBytes); err != nil {
		return types.ErrInvalidSignature
	}

	// 5. Nonce check
	if tx.Nonce != fromAcc.Nonce {
		return fmt.Errorf("%w: want %d got %d", types.ErrInvalidNonce, fromAcc.Nonce, tx.Nonce)
	}

	// 6. Balance check
	total := new(types.BigInt).Add(tx.Amount, tx.Fee)
	if fromAcc.Balance.Cmp(total) < 0 {
		return types.ErrInsufficientBalance
	}

	// 7. Type-specific execution
	switch tx.Type {
	case types.TxTransfer:
		return e.execTransfer(tx, fromAcc, receipt)
	case types.TxDeploy:
		return e.execDeploy(tx, fromAcc, receipt)
	case types.TxCall:
		return e.execCall(tx, fromAcc, receipt, blockHeight)
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

	// Record contract address in receipt
	receipt.ContractAddress = &contractAddr

	log.Debug().
		Str("deployer", tx.From.String()).
		Str("contract", contractAddr.String()).
		Msg("contract deployed")
	return nil
}

func (e *Executor) execCall(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64) error {
	// Native module router: a TxCall to the reserved module-router address
	// invokes a built-in module (qrc20/dex/…) which writes state directly,
	// rather than executing VM bytecode. Fee-only charge, like execDeploy;
	// the module carries no SPLD value.
	if tx.To == vm.ModuleRouterAddress {
		if err := fromAcc.SubBalance(tx.Fee); err != nil {
			return err
		}
		fromAcc.IncrNonce()
		e.state.SetAccount(fromAcc)
		return vm.DispatchModuleCall(e.state, tx.From, tx.Nonce, tx.Data, receipt)
	}

	total := addBig(tx.Amount, tx.Fee)
	if err := fromAcc.SubBalance(total); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	result, err := e.vmEngine.CallWithContext(
		tx.From, tx.From, tx.To, tx.Data, tx.Amount,
		blockHeight, time.Now().Unix(), false,
	)
	if err != nil {
		return fmt.Errorf("call failed: %w", err)
	}

	// Record VM execution results in receipt
	if result != nil {
		receipt.GasUsed = result.GasUsed
		for _, l := range result.Logs {
			receipt.AddLog(l)
		}
		if result.Reverted {
			receipt.SetFailed("execution reverted")
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
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()

	toAcc, err := e.state.GetAccount(tx.To)
	if err != nil {
		return err
	}
	if err := fromAcc.SubBalance(tx.Amount); err != nil {
		return err
	}
	toAcc.AddBalance(tx.Amount)
	e.state.SetAccount(fromAcc)
	e.state.SetAccount(toAcc)

	nonceKey := qcrypto.Hash32(append(tx.From[:], tx.Data...))
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

	var (
		totalFees  = new(bigInt)
		executed   []*types.Transaction
		receipts   []*types.Receipt
	)

	for i, tx := range txs {
		receipt, _ := types.NewReceipt(tx, uint32(i), height, [32]byte{}) // hash filled below
		if err := e.executeTx(tx, height, receipt); err != nil {
			log.Warn().Err(err).Msg("skipping invalid tx")
			continue
		}
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
		Timestamp:     time.Now().Unix(),
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

	// Back-fill block hash in receipts and store them
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

// ── Chain DB helpers ───────────────────────────────────────────────────────────

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

	// Advance the head pointer forward only. verifyBlockLinkage already enforces
	// contiguity for the apply path; this is defence-in-depth so no caller can
	// move the head backward or sideways with a lower/duplicate-height block.
	if curH, ok := e.latestHeightRaw(); ok && b.Header.Height <= curH {
		return nil
	}
	latestData, _ := json.Marshal(map[string]interface{}{
		"height": b.Header.Height,
		"hash":   hashHex,
	})
	return e.chainDB.Put([]byte(keyLatest), latestData, nil)
}

// latestHeightRaw returns the stored head height and whether a head exists.
// Unlike LatestHeight (which returns 0 for an empty chain, indistinguishable
// from genesis height 0), the bool disambiguates "no head yet" from "head at 0".
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

// verifyBlockLinkage enforces chain-continuity invariants on a block BEFORE it is
// executed: matching chain ID, height == head+1, prev-hash links to the stored
// head, monotonic and not-too-future timestamp, and duplicate/backward rejection.
// Genesis (height 0) is exempt from predecessor checks but may only be applied to
// an empty chain. On an empty chain only block 1 is accepted for bootstrap; its
// authenticity rests on validator-signature verification upstream.
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
		return nil // genesis on an empty chain
	}

	if latest == nil {
		if b.Header.Height != 1 {
			return fmt.Errorf("cannot apply block %d to an empty chain (expected block 1)", b.Header.Height)
		}
		return nil // bootstrapping first block; signature verification is the gate
	}

	if b.Header.Height != latest.Header.Height+1 {
		return fmt.Errorf("non-contiguous height: got %d, expected %d", b.Header.Height, latest.Header.Height+1)
	}
	if b.Header.PrevHash != latest.Hash {
		return fmt.Errorf("prev-hash mismatch at height %d", b.Header.Height)
	}
	// NOTE: deliberately NO timestamp monotonicity / future-drift check here.
	// The producer (ProduceBlock) stamps time.Now().Unix() unclamped and does not
	// self-verify, so a validator wall-clock step-back (NTP correction, VM
	// migration) can emit a validly-signed non-monotonic block. A consumer-side
	// timestamp gate would then permanently wedge every sync/RPC node on that
	// block (and on any such block already in historical LevelDB). Anti-rewind /
	// anti-replay is fully covered by chain-id + height==head+1 + prev-hash link +
	// mandatory validator-signature verification, none of which depend on the clock.
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

// ── Helpers ────────────────────────────────────────────────────────────────────

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
