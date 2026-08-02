// Package block handles block production, execution, and chain storage.
package block

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

const (
	// Maximum transactions per block
	MaxTxPerBlock = 10_000

	// Block key prefix in chain database
	prefixBlock  = "block:"
	prefixHeight = "height:"
	keyLatest    = "latest"
)

// Executor executes blocks and manages the chain database.
type Executor struct {
	state    *state.StateDB
	chainDB  *leveldb.DB
	vmEngine *vm.Engine
}

// NewExecutor creates a new block executor.
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

// Close closes the chain database.
func (e *Executor) Close() error {
	return e.chainDB.Close()
}

// ExecuteBlock applies all transactions in a block to the state, validates
// signatures, nonces, and balances, and commits the resulting state.
// Returns the new state root.
func (e *Executor) ExecuteBlock(b *types.Block) error {
	log.Info().
		Uint64("height", b.Header.Height).
		Int("txs", len(b.Transactions)).
		Msg("executing block")

	for i, tx := range b.Transactions {
		if err := e.executeTx(tx, b.Header.Height); err != nil {
			return fmt.Errorf("tx %d failed: %w", i, err)
		}
	}

	// Commit all state changes and get new state root
	stateRoot, err := e.state.Commit()
	if err != nil {
		return fmt.Errorf("state commit failed: %w", err)
	}

	// Verify the state root matches the block header's declared state root
	if stateRoot != b.Header.StateRoot {
		return fmt.Errorf("state root mismatch: computed %x, header %x",
			stateRoot, b.Header.StateRoot)
	}

	// Persist the block to chain database
	return e.storeBlock(b)
}

// executeTx processes a single transaction.
func (e *Executor) executeTx(tx *types.Transaction, blockHeight uint64) error {
	// 1. Basic structural validation
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// 2. Get sender account
	fromAcc, err := e.state.GetAccount(tx.From)
	if err != nil {
		return err
	}

	// 3. Handle public key registration (first transaction from this address)
	var pubKeyBytes []byte
	if fromAcc.HasPublicKey() {
		pubKeyBytes = fromAcc.PublicKey
	} else if len(tx.PublicKey) == qcrypto.PublicKeySize {
		// First transaction: public key must be included and must hash to the From address
		pub, err := qcrypto.PublicKeyFromBytes(tx.PublicKey)
		if err != nil {
			return fmt.Errorf("invalid public key in transaction: %w", err)
		}
		derivedAddr := qcrypto.DeriveAddress(pub)
		if derivedAddr != tx.From {
			return fmt.Errorf("public key does not match from address")
		}
		pubKeyBytes = tx.PublicKey
		// Register the public key
		fromAcc.PublicKey = tx.PublicKey
		e.state.SetAccount(fromAcc)
	} else {
		return types.ErrNoPublicKey
	}

	// 4. Verify Dilithium signature
	if err := tx.VerifySignature(pubKeyBytes); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// 5. Check nonce
	if tx.Nonce != fromAcc.Nonce {
		return fmt.Errorf("%w: expected %d, got %d",
			types.ErrInvalidNonce, fromAcc.Nonce, tx.Nonce)
	}

	// 6. Check balance (amount + fee)
	total := new(types.BigInt).Add(tx.Amount, tx.Fee)
	if fromAcc.Balance.Cmp(total) < 0 {
		return types.ErrInsufficientBalance
	}

	// 7. Execute by type
	switch tx.Type {
	case types.TxTransfer:
		if err := e.execTransfer(tx, fromAcc); err != nil {
			return err
		}
	case types.TxDeploy:
		if err := e.execDeploy(tx, fromAcc); err != nil {
			return err
		}
	case types.TxCall:
		if err := e.execCall(tx, fromAcc); err != nil {
			return err
		}
	case types.TxX402:
		if err := e.execX402(tx, fromAcc, blockHeight); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown transaction type: %x", tx.Type)
	}

	return nil
}

func (e *Executor) execTransfer(tx *types.Transaction, fromAcc *types.Account) error {
	// Deduct amount + fee from sender
	total := new(bigInt).Add(tx.Amount, tx.Fee)
	if err := fromAcc.SubBalance(total); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	// Credit amount to recipient
	toAcc, err := e.state.GetAccount(tx.To)
	if err != nil {
		return err
	}
	toAcc.AddBalance(tx.Amount)
	e.state.SetAccount(toAcc)

	// Fee goes to validator (handled at block level — credited in ProduceBlock)
	log.Debug().
		Str("from", tx.From.String()).
		Str("to", tx.To.String()).
		Str("amount", tx.Amount.String()).
		Msg("transfer executed")
	return nil
}

func (e *Executor) execDeploy(tx *types.Transaction, fromAcc *types.Account) error {
	// Deduct fee
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	// Deploy contract via VM
	contractAddr, err := e.vmEngine.Deploy(tx.From, tx.Data, tx.Nonce)
	if err != nil {
		return fmt.Errorf("contract deploy failed: %w", err)
	}

	log.Debug().
		Str("deployer", tx.From.String()).
		Str("contract", contractAddr.String()).
		Msg("contract deployed")
	return nil
}

func (e *Executor) execCall(tx *types.Transaction, fromAcc *types.Account) error {
	// Deduct amount + fee
	total := addBig(tx.Amount, tx.Fee)
	if err := fromAcc.SubBalance(total); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	// Execute contract call via VM
	if _, err := e.vmEngine.Call(tx.From, tx.To, tx.Data, tx.Amount); err != nil {
		return fmt.Errorf("contract call failed: %w", err)
	}

	log.Debug().
		Str("caller", tx.From.String()).
		Str("contract", tx.To.String()).
		Msg("contract called")
	return nil
}

func (e *Executor) execX402(tx *types.Transaction, fromAcc *types.Account, blockHeight uint64) error {
	// X402 settlement: deduct fee from sender (amount already validated by x402 RPC layer)
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	// Transfer payment amount
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

	// Mark X402 nonce as used
	nonceKey := qcrypto.Hash32(append(tx.From[:], tx.Data...))
	if err := e.state.MarkX402NonceUsed(nonceKey, blockHeight); err != nil {
		return fmt.Errorf("failed to mark X402 nonce: %w", err)
	}

	log.Debug().
		Str("payer", tx.From.String()).
		Str("receiver", tx.To.String()).
		Str("amount", tx.Amount.String()).
		Msg("X402 payment settled")
	return nil
}

// ProduceBlock creates a new block from the given transactions, executes them,
// and signs the block with the validator's Dilithium key.
func (e *Executor) ProduceBlock(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, error) {

	// Cap transactions
	if len(txs) > MaxTxPerBlock {
		txs = txs[:MaxTxPerBlock]
	}

	// Execute all transactions and collect fees
	totalFees := new(bigInt)
	var executed []*types.Transaction
	for _, tx := range txs {
		fromAcc, err := e.state.GetAccount(tx.From)
		if err != nil {
			log.Warn().Err(err).Str("tx_from", tx.From.String()).Msg("skipping tx: account error")
			continue
		}
		_ = fromAcc
		if err := e.executeTx(tx, height); err != nil {
			log.Warn().Err(err).Msg("skipping invalid tx during block production")
			continue
		}
		totalFees = totalFees.Add(totalFees, tx.Fee)
		executed = append(executed, tx)
	}

	// Credit total fees to validator
	if totalFees.Sign() > 0 {
		validatorAcc, err := e.state.GetAccount(validatorAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to load validator account: %w", err)
		}
		validatorAcc.AddBalance(totalFees)
		e.state.SetAccount(validatorAcc)
	}

	// Commit state
	stateRoot, err := e.state.Commit()
	if err != nil {
		return nil, fmt.Errorf("state commit failed: %w", err)
	}

	// Build tx merkle root
	txMerkleRoot, err := types.ComputeTxMerkleRoot(executed)
	if err != nil {
		return nil, fmt.Errorf("merkle root failed: %w", err)
	}

	// Build block header
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

	b := &types.Block{
		Header:       header,
		Transactions: executed,
	}

	// Compute and set block hash
	hash, err := b.ComputeHash()
	if err != nil {
		return nil, fmt.Errorf("block hash failed: %w", err)
	}
	b.Hash = hash

	// Sign block with validator's Dilithium key
	if err := b.SignBlock(validatorPrivKey); err != nil {
		return nil, fmt.Errorf("block signing failed: %w", err)
	}

	// Persist block
	if err := e.storeBlock(b); err != nil {
		return nil, fmt.Errorf("block store failed: %w", err)
	}

	log.Info().
		Uint64("height", height).
		Int("txs", len(executed)).
		Str("hash", fmt.Sprintf("%x", hash[:8])).
		Msg("block produced and signed")

	return b, nil
}

// storeBlock persists a block to the chain LevelDB.
func (e *Executor) storeBlock(b *types.Block) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	hashHex := fmt.Sprintf("%x", b.Hash[:])
	heightKey := fmt.Sprintf("%s%d", prefixHeight, b.Header.Height)

	batch := new(leveldb.Batch)
	batch.Put([]byte(prefixBlock+hashHex), data)
	batch.Put([]byte(heightKey), []byte(hashHex))
	if err := e.chainDB.Write(batch, nil); err != nil {
		return fmt.Errorf("failed to write block to chain DB: %w", err)
	}

	// Update latest block pointer
	latestData, _ := json.Marshal(map[string]interface{}{
		"height": b.Header.Height,
		"hash":   hashHex,
	})
	return e.chainDB.Put([]byte(keyLatest), latestData, nil)
}

// GetBlock retrieves a block by its SHA3-256 hash (hex string).
func (e *Executor) GetBlock(hashHex string) (*types.Block, error) {
	data, err := e.chainDB.Get([]byte(prefixBlock+hashHex), nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrBlockNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chain DB read error: %w", err)
	}
	var b types.Block
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}
	return &b, nil
}

// GetBlockByHeight retrieves a block by height.
func (e *Executor) GetBlockByHeight(height uint64) (*types.Block, error) {
	heightKey := fmt.Sprintf("%s%d", prefixHeight, height)
	hashHex, err := e.chainDB.Get([]byte(heightKey), nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrBlockNotFound
	}
	if err != nil {
		return nil, err
	}
	return e.GetBlock(string(hashHex))
}

// LatestHeight returns the height of the most recently committed block.
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
	if err := json.Unmarshal(data, &info); err != nil {
		return 0, err
	}
	return info.Height, nil
}

// LatestBlock returns the most recently committed block.
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

// helper: add two *big.Int safely
func addBig(a, b *bigInt) *bigInt {
	return new(bigInt).Add(a, b)
}

// bigInt is an alias for math/big.Int for clarity in this file
type bigInt = types.BigInt
