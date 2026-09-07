// Package block handles block production, execution, and chain storage.
package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
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

	// MaxBlockWireBytes caps the serialised size of a block on the wire.
	//
	// MaxTxPerBlock alone does not bound block size: each tx may carry
	// MaxTxDataBytes (128 KiB) of calldata, so 10k txs is theoretically >1 GiB.
	// That is both a propagation problem and a memory-exhaustion vector on every
	// receiving node. 32 MiB fits ~4,300 signature-bearing transactions, which is
	// far above what a 2-second interval can verify and execute, so this bounds
	// memory without bounding real throughput.
	//
	// ProduceBlock stops packing at this budget; the P2P/RPC ingress caps the
	// request body at the same value, so an oversized block is refused before it
	// is ever buffered.
	MaxBlockWireBytes = 32 << 20

	// MaxFutureBlockDrift is how far ahead of local wall-clock a block timestamp
	// may be. Without a bound, a validator (or anyone replaying its blocks) can
	// stamp a block far in the future and poison every contract that reads
	// OpTIMESTAMP — time-locks, vesting, auction deadlines, and oracle staleness
	// checks all key off block time.
	MaxFutureBlockDrift = 15 * time.Second
)

// txWireSizeEstimate returns an UPPER BOUND on a transaction's JSON wire size.
//
// It must never under-estimate: ProduceBlock uses it as a packing budget while
// the receiving side enforces the true byte count, so an under-estimate would
// let a producer build blocks its own peers reject. Go marshals []byte fields as
// base64 strings (4 bytes out per 3 in); the fixed slack covers the header
// fields plus the two 32-number address arrays.
func txWireSizeEstimate(tx *types.Transaction) int {
	b64 := func(n int) int { return ((n + 2) / 3) * 4 }
	return 1024 + b64(len(tx.Data)) + b64(len(tx.PublicKey)) + b64(len(tx.Signature))
}

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
	hooks    *HookRegistry // optional; nil = no hooks
	txIndex  receiptStorer // optional; nil = don't store receipts

	// genesisHash pins the hash of block 0 when the operator supplied one.
	// nil = unpinned, which is the pre-existing behaviour.
	genesisHash *[32]byte

	// forks is the chain's rule-change schedule. It travels into every module
	// call (vm.ModuleContext.Forks) so a gate is judged against the height of
	// the block being applied — the one code path producer and follower share.
	// nil = no rule changes, which is what every chain scheduled before the
	// executor learned to read one.
	//
	// Held behind an atomic pointer, NOT under mu: Forks() feeds /status, the
	// /tx/submit preflight and /estimate on public RPC goroutines, and taking
	// the block lock there stalled every one of them for the whole of a
	// ProduceBlock/ExecuteBlock/ReconcileState. The map behind the pointer is
	// never mutated after install (installForks stores a private copy), so a
	// reader only ever needs the pointer. SetForks still serializes on mu, so
	// the pointer cannot change while a block is in flight: every in-block
	// read (execCall) sees the schedule installed when the block began.
	forks atomic.Pointer[forks.Schedule]
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

// WithGenesisHash pins the hash of block 0.
//
// It supplies the anchor missing at the bottom of the chain: linkage checks
// every block against its stored parent, but block 1 arrives on an empty chain
// with no parent to check, so an unpinned node bootstrapping from scratch will
// build on whatever genesis its first peer claims. With a pin, block 1 must
// name that genesis as its parent, and a self-built block 0 must be the pinned
// one — so a node cannot be walked onto a fabricated chain, nor silently start
// a divergent one after its data dir is wiped.
func (e *Executor) WithGenesisHash(h [32]byte) *Executor {
	e.genesisHash = &h
	return e
}

// WithForks installs the rule-change schedule at construction time, before any
// block is applied. Builder-style like the others above; use SetForks once the
// executor is live.
func (e *Executor) WithForks(s forks.Schedule) *Executor {
	e.installForks(s)
	return e
}

// SetForks installs (or replaces) the schedule on a running executor.
//
// It takes the block lock, so the change lands between two block transitions
// and never in the middle of one: a schedule that arrives with a peer-served
// genesis is visible to the very next block the syncer applies, and no block
// is applied half under one schedule and half under another. Readers (Forks,
// execCall) do not take the lock — see the forks field.
func (e *Executor) SetForks(s forks.Schedule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.installForks(s)
}

// installForks publishes a private copy of s, so a caller that keeps mutating
// its own map after handing it over can never reach what consensus reads.
// nil stays nil.
func (e *Executor) installForks(s forks.Schedule) {
	if s == nil {
		e.forks.Store(nil)
		return
	}
	own := cloneSchedule(s)
	e.forks.Store(&own)
}

// schedule is the executor's own lock-free read of the installed schedule
// (nil when none is). The map it returns is immutable; never hand it out.
func (e *Executor) schedule() forks.Schedule {
	if p := e.forks.Load(); p != nil {
		return *p
	}
	return nil
}

func cloneSchedule(s forks.Schedule) forks.Schedule {
	out := make(forks.Schedule, len(s))
	for name, at := range s {
		out[name] = at
	}
	return out
}

// Forks returns a copy of the installed schedule (nil when none is).
//
// A copy, so a caller reading it for /status or an admission check can never
// mutate what consensus is about to apply. Lock-free: it must never wait on a
// block transition, or every RPC that consults the schedule stalls behind
// ProduceBlock/ExecuteBlock/ReconcileState.
func (e *Executor) Forks() forks.Schedule {
	s := e.schedule()
	if s == nil {
		return nil
	}
	return cloneSchedule(s)
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
	if err := e.verifyProducerAndQC(b); err != nil {
		return fmt.Errorf("block certificate rejected: %w", err)
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
			// The tx could not be applied at all: bad signature, wrong nonce,
			// unaffordable, unknown type. ProduceBlock drops such txs, so a block
			// that still carries one was not built honestly. It must be rejected
			// outright — the previous behaviour reverted the tx (leaving the
			// sender undebited) yet still credited its fee to the validator,
			// which minted the fee out of nothing on every invalid tx packed.
			e.state.DiscardPending()
			return fmt.Errorf("block contains an unappliable tx at index %d: %w", i, err)
		}
		receipts = append(receipts, receipt)

		// Reject a block that exceeds the consensus gas budget (defence against a
		// producer packing far more compute than the interval can process).
		blockGas += receipt.GasUsed
		if blockGas > MaxBlockGas {
			e.state.DiscardPending()
			return fmt.Errorf("block exceeds gas budget: %d > %d", blockGas, MaxBlockGas)
		}

		// Accumulate fees for validator credit (mirrors ProduceBlock). Only txs
		// that actually applied reach here, and every exec path debits tx.Fee
		// from the sender before doing its work — including the reverted-call
		// case, where the fee is kept as gas. So the credit always has a
		// matching debit and total supply is conserved.
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

	if err := genesis.CheckNativeSupply(e.state); err != nil {
		e.state.DiscardPending()
		return err
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

	// Root matches — persist. ORDER MATTERS: store the block + advance the chain
	// head FIRST (atomic within the chain DB), THEN commit state with its height
	// marker. A crash between the two leaves the head ahead of the state by one
	// block, which boot reconciliation replays from the already-stored block. The
	// reverse order (state ahead of a head with no stored block) is unrecoverable.
	if err := e.storeBlock(b); err != nil {
		e.state.DiscardPending()
		return fmt.Errorf("block store failed: %w", err)
	}
	if _, err := e.state.CommitWithHeight(b.Header.Height); err != nil {
		return fmt.Errorf("state commit failed: %w", err)
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

// ReconcileState catches committed state up to the chain head after a crash.
//
// storeBlock advances the chain head (block body + head pointer, one atomic
// batch) BEFORE the state is committed, so an ill-timed crash can leave the head
// ahead of the state by one or more blocks. Those blocks are already durably
// stored, so they are replayed deterministically to rebuild the missing state.
// The reverse (state ahead of head) is prevented by that write order and treated
// as corruption. Must be called once at startup before the node serves or seals.
func (e *Executor) ReconcileState() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	stateH, known := e.state.AppliedHeight()
	if !known {
		// No height marker: a fresh DB (chain head also absent), or legacy state
		// written before markers existed. Either way there is nothing to
		// reconcile against — trust the durable chain DB and return.
		return nil
	}

	chainH, err := e.LatestHeight()
	if err != nil {
		return fmt.Errorf("reconcile: read chain head: %w", err)
	}
	if stateH > chainH {
		return fmt.Errorf("reconcile: state height %d is ahead of chain head %d — corrupt state", stateH, chainH)
	}
	for h := stateH + 1; h <= chainH; h++ {
		b, err := e.GetBlockByHeight(h)
		if err != nil {
			return fmt.Errorf("reconcile: load stored block %d: %w", h, err)
		}
		if err := e.reapplyBlock(b); err != nil {
			return fmt.Errorf("reconcile: replay block %d: %w", h, err)
		}
		log.Warn().Uint64("height", h).Msg("reconcile: replayed stored block to catch state up to chain head after crash")
	}
	return nil
}

// reapplyBlock re-executes an already-stored block's transactions against the
// current state and commits (advancing the state height marker). Used ONLY by
// ReconcileState: it skips linkage (the block is already canonical) and block
// storage (already stored), but still verifies the recomputed state root matches
// the block header — a mismatch is corruption, not a normal reject.
func (e *Executor) reapplyBlock(b *types.Block) error {
	var receipts []*types.Receipt
	totalFees := new(bigInt)
	var blockGas uint64

	for i, tx := range b.Transactions {
		receipt, err := e.executeTxWithReceipt(tx, uint32(i), b.Header.Height, b.Header.Timestamp, b.Hash)
		if err != nil {
			e.state.DiscardPending()
			return fmt.Errorf("reapply tx %d: %w", i, err)
		}
		receipts = append(receipts, receipt)
		blockGas += receipt.GasUsed
		if blockGas > MaxBlockGas {
			e.state.DiscardPending()
			return fmt.Errorf("reapply: block exceeds gas budget: %d > %d", blockGas, MaxBlockGas)
		}
		totalFees.Add(totalFees, tx.Fee)
	}

	if totalFees.Sign() > 0 {
		va, err := e.state.GetAccount(b.Header.ValidatorAddr)
		if err != nil {
			e.state.DiscardPending()
			return fmt.Errorf("reapply fee credit: %w", err)
		}
		va.AddBalance(totalFees)
		e.state.SetAccount(va)
	}

	if err := genesis.CheckNativeSupply(e.state); err != nil {
		e.state.DiscardPending()
		return err
	}

	root, err := e.state.PendingStateRoot()
	if err != nil {
		e.state.DiscardPending()
		return fmt.Errorf("reapply state root: %w", err)
	}
	if root != b.Header.StateRoot {
		e.state.DiscardPending()
		return fmt.Errorf("reapply state root mismatch at height %d: computed %x, header %x",
			b.Header.Height, root[:8], b.Header.StateRoot[:8])
	}
	if _, err := e.state.CommitWithHeight(b.Header.Height); err != nil {
		return fmt.Errorf("reapply commit: %w", err)
	}

	if e.txIndex != nil {
		_ = e.txIndex.IndexBlock(b)
		_ = e.txIndex.StoreBlockReceipts(receipts)
	}
	return nil
}

// executeTxWithReceipt applies a single transaction from a block being ADOPTED
// (inbound sync or crash replay) and returns its receipt.
//
// A non-nil error means the tx could not be applied at all and the enclosing
// block must be rejected. That is different from a tx that applied but whose
// contract call reverted: a revert still charges the fee and consumes the nonce,
// so it is a legitimate block member and comes back with a failed receipt and a
// nil error. Only the unappliable cases — bad signature, wrong nonce, unfunded,
// unknown type, rejected by a pre-tx hook — produce an error, because
// ProduceBlock drops exactly those and never puts them in a block.
func (e *Executor) executeTxWithReceipt(tx *types.Transaction, txIndex uint32, blockHeight uint64, blockTime int64, blockHash [32]byte) (*types.Receipt, error) {
	receipt, err := types.NewReceipt(tx, txIndex, blockHeight, blockHash)
	if err != nil {
		return nil, fmt.Errorf("receipt init failed: %w", err)
	}

	if e.hooks != nil {
		if err := e.hooks.RunPreTx(tx, blockHeight); err != nil {
			return nil, fmt.Errorf("pre-tx hook rejected tx: %w", err)
		}
	}

	// Execute inside a snapshot so a FAILED tx leaves NO state residue.
	// execCall/execDeploy/execX402 deduct the fee and bump the nonce BEFORE the
	// operation; without this revert, a failed op keeps that dirty mutation, so
	// the block's account state (and thus the state root) reflects a tx that the
	// producer excluded — producer and followers then compute divergent roots.
	//
	// A failed revert or commit of that per-tx checkpoint is fatal for the
	// block, not just the tx: the pending layer then holds residue nobody can
	// account for, and the state root computed over it is one no other node
	// would reproduce. The error propagates and the caller discards the
	// pending layer with the block.
	snap := e.state.Snapshot()
	if err := e.executeTx(tx, blockHeight, blockTime, receipt); err != nil {
		if rerr := e.state.RevertToSnapshot(snap); rerr != nil {
			return nil, fmt.Errorf("revert of unappliable tx: %w (tx error: %v)", rerr, err)
		}
		log.Warn().Err(err).
			Str("from", tx.From.String()).
			Uint64("nonce", tx.Nonce).
			Msg("transaction in received block could not be applied")
		return nil, err
	}
	if err := e.state.CommitSnapshot(snap); err != nil {
		return nil, fmt.Errorf("commit of tx snapshot: %w", err)
	}

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

	// An account's public key is the only thing that can ever authorise spending
	// from it, so it is recorded only once it has actually verified a signature
	// — never merely because a transaction presented it.
	//
	// This is also what makes the algorithm of a stored key a fact rather than a
	// claim. Validate() above has already rejected any tx whose alg_id is not
	// ML-DSA-65, and VerifySignature below checks the signature as ML-DSA-65, so
	// a key reaches account state only after producing a valid FIPS-204
	// signature over this transaction. An account key that is not ML-DSA-65
	// cannot be written, which is a stronger guarantee than a stored label: the
	// label would be a self-reported string, and it would still have to be
	// believed.
	var pubKeyBytes []byte
	registerKey := false
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
		registerKey = true
	} else {
		return types.ErrNoPublicKey
	}

	if err := tx.VerifySignature(pubKeyBytes); err != nil {
		return types.ErrInvalidSignature
	}

	if registerKey {
		fromAcc.PublicKey = tx.PublicKey
		e.state.SetAccount(fromAcc)
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

// dispatchModuleCall is the module entry point execCall uses. A variable so a
// test can stand in a module that panics or writes-then-fails and prove the
// executor's handling of it, independent of any real module's failure modes.
var dispatchModuleCall = vm.DispatchModuleCall

// errModulePanic is the SOFT error a recovered module panic becomes under
// module_revert_v1. The panic value is deliberately not in the text: it can
// carry addresses or allocation sizes that differ between nodes.
var errModulePanic = errors.New("module panic")

// runModuleCall dispatches under a recover so a panicking module becomes
// errModulePanic rather than a dropped tx (legacy) or a halted validator.
func runModuleCall(stateDB *state.StateDB, ctx vm.ModuleContext, data []byte, receipt *types.Receipt) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errModulePanic
		}
	}()
	return dispatchModuleCall(stateDB, ctx, data, receipt)
}

func (e *Executor) execCall(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64, blockTime int64) error {
	if tx.To == vm.ModuleRouterAddress {
		if err := fromAcc.SubBalance(tx.Fee); err != nil {
			return err
		}
		fromAcc.IncrNonce()
		e.state.SetAccount(fromAcc)
		// The block height and header time travel with the call so modules
		// judge deadlines against the block, never the wall clock: the
		// producer stamped blockTime once and every follower reads the same
		// value back from the header.
		// One read per call; SetForks cannot land while this block holds
		// e.mu, so every tx in the block is judged under the same map.
		schedule := e.schedule()
		mctx := vm.ModuleContext{
			Caller: tx.From,
			Nonce:  tx.Nonce,
			Height: blockHeight,
			Time:   blockTime,
			Forks:  schedule,
		}

		// Legacy rules: any module error makes the tx unappliable — the
		// producer drops it and the enclosing per-tx snapshot reverts the fee
		// debit and nonce bump. A sender whose call fails for good is stuck at
		// that nonce, which is what module_revert_v1 fixes.
		if !schedule.Active(forks.ModuleRevertV1, blockHeight) {
			return dispatchModuleCall(e.state, mctx, tx.Data, receipt)
		}

		// module_revert_v1: the module runs inside its own snapshot, nested in
		// the per-tx one, so its writes can be undone while the fee debit and
		// nonce bump above are kept.
		// A snapshot-layer error here makes the tx unappliable; the per-tx
		// checkpoint around this call then decides whether the block survives.
		inner := e.state.Snapshot()
		err := runModuleCall(e.state, mctx, tx.Data, receipt)
		if err == nil {
			return e.state.CommitSnapshot(inner)
		}
		if rerr := e.state.RevertToSnapshot(inner); rerr != nil {
			return fmt.Errorf("revert of failed module call: %w (module error: %v)", rerr, err)
		}
		if vm.IsHardModuleError(err) {
			// Unparseable envelope, unknown module or action, fork not active:
			// statically detectable at admission, so an honest producer never
			// packs one. Still unappliable — a follower lacking the fork
			// rejects the block outright instead of diverging on state root.
			return err
		}
		// SOFT: included with a failed receipt, fee kept, nonce consumed —
		// mirroring the reverted-bytecode branch below. Logs and a deploy
		// address the module may have set before failing go with its writes.
		receipt.Logs = []*types.Log{}
		receipt.ContractAddress = nil
		receipt.SetFailed(err.Error())
		return nil
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

	// vm_revert_v1: the frame runs inside its own snapshot, nested in the
	// per-tx one, so a top-level REVERT can undo the frame's writes while the
	// fee debit and nonce bump above are kept. The engine snapshots only
	// sub-calls; the entry frame runs directly on state, so without this a
	// reverting call is unappliable (the producer drops it and the sender's
	// call can never land — the jam module_revert_v1 fixed for modules).
	vmRevert := e.schedule().Active(forks.VMRevertV1, blockHeight)
	var frame uint64
	if vmRevert {
		frame = e.state.Snapshot()
	}

	result, err := e.vmEngine.CallWithContext(
		tx.From, tx.From, tx.To, tx.Data, tx.Amount,
		blockHeight, blockTime, false,
	)

	reverted := result != nil && result.Reverted
	if vmRevert && reverted {
		// Included with a failed receipt: fee kept, nonce consumed, gas
		// recorded — and every write and log of the frame rolled back, the
		// deal a reverted EVM transaction gets. A snapshot-layer error makes
		// the tx unappliable; the per-tx checkpoint then decides whether the
		// block survives.
		if rerr := e.state.RevertToSnapshot(frame); rerr != nil {
			return fmt.Errorf("revert of reverted call frame: %w", rerr)
		}
		receipt.GasUsed = result.GasUsed
		receipt.Logs = []*types.Log{}
		msg := "execution reverted"
		if result.Err != nil {
			msg = result.Err.Error()
		}
		receipt.SetFailed(msg)
		return nil
	}
	if err != nil {
		// Hard error (or any revert under legacy rules): unappliable, exactly
		// as before the fork. The open frame checkpoint, if any, is consumed
		// by the per-tx revert above this call.
		return fmt.Errorf("call failed: %w", err)
	}
	if vmRevert {
		if cerr := e.state.CommitSnapshot(frame); cerr != nil {
			return fmt.Errorf("commit of call frame snapshot: %w", cerr)
		}
	}

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
	return e.assembleBlock(txs, validatorAddr, validatorPubKey, validatorPrivKey, prevHash, height, true)
}

// BuildProposal assembles and signs a block without committing it. The leader
// proposes this; followers re-execute via ExecuteBlock after a quorum.
func (e *Executor) BuildProposal(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, error) {
	return e.assembleBlock(txs, validatorAddr, validatorPubKey, validatorPrivKey, prevHash, height, false)
}

func (e *Executor) assembleBlock(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
	persist bool,
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
		blockSize int
	)

	for _, tx := range txs {
		// Snapshot so a skipped (failed) tx leaves NO residue: executeTx deducts
		// fee + bumps nonce before the op, so without reverting, a failed tx that
		// is excluded from `executed` would still dirty the state → the block's
		// root reflects a tx not in Transactions and followers reject it.
		// Stop packing before executing if this tx would push the block past the
		// wire-size budget. Checked first so we never pay for execution we are
		// about to discard.
		txSize := txWireSizeEstimate(tx)
		if blockSize+txSize > MaxBlockWireBytes {
			break
		}

		// Run the SAME pre-tx hooks a follower will run when it replays this
		// block. Producing without them means the producer can pack a transaction
		// every follower then rejects — the block is unappliable everywhere but
		// here, which is a fork, not a dropped transaction. Skipping it during
		// production is the only safe direction.
		if e.hooks != nil {
			if err := e.hooks.RunPreTx(tx, height); err != nil {
				log.Warn().Err(err).Msg("skipping tx a follower would reject")
				continue
			}
		}

		// A failed revert or commit of the per-tx checkpoint abandons the block:
		// the pending layer would carry residue the header's state root could
		// not honestly cover, so it is discarded rather than produced from.
		snap := e.state.Snapshot()
		// The receipt's index is the position in the BLOCK, which is
		// len(executed) — not the candidate index i. Candidates are skipped
		// above (bad nonce, unfunded, hook rejection), so after any skip i runs
		// ahead of the real position, and every follower path indexes
		// b.Transactions. Using i made a producing node disagree with its own
		// transaction index, and with every other node, about the same block.
		receipt, _ := types.NewReceipt(tx, uint32(len(executed)), height, [32]byte{})
		if err := e.executeTx(tx, height, blockTime, receipt); err != nil {
			if rerr := e.state.RevertToSnapshot(snap); rerr != nil {
				e.state.DiscardPending()
				return nil, fmt.Errorf("revert of skipped tx: %w (tx error: %v)", rerr, err)
			}
			log.Warn().Err(err).Msg("skipping invalid tx")
			continue
		}
		// Stop packing once the block gas budget is reached (txs are fee-sorted,
		// so we keep the most valuable ones). Revert this tx's state before break.
		if blockGas+receipt.GasUsed > MaxBlockGas {
			if err := e.state.RevertToSnapshot(snap); err != nil {
				e.state.DiscardPending()
				return nil, fmt.Errorf("revert of over-budget tx: %w", err)
			}
			break
		}
		if err := e.state.CommitSnapshot(snap); err != nil {
			e.state.DiscardPending()
			return nil, fmt.Errorf("commit of tx snapshot: %w", err)
		}
		blockGas += receipt.GasUsed
		blockSize += txSize
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

	if err := genesis.CheckNativeSupply(e.state); err != nil {
		e.state.DiscardPending()
		return nil, err
	}

	// Compute the root the pending writes WOULD commit to (for the header), but do
	// not persist state yet — the block body + head must be stored first so a
	// crash leaves the head ahead of state (replayable), never the reverse.
	stateRoot, err := e.state.PendingStateRoot()
	if err != nil {
		return nil, fmt.Errorf("pending state root: %w", err)
	}

	txMerkleRoot, err := types.ComputeTxMerkleRoot(executed)
	if err != nil {
		return nil, fmt.Errorf("merkle root: %w", err)
	}

	header := types.BlockHeader{
		Height:        height,
		Timestamp:     blockTime,
		ChainID:       types.ChainID,
		AlgID:         qcrypto.AlgMLDSA65,
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
	if len(validatorPubKey) > 0 {
		b.ValidatorPubKey = validatorPubKey
	}

	if !persist {
		e.state.DiscardPending()
		return b, nil
	}

	if err := e.storeBlock(b); err != nil {
		e.state.DiscardPending()
		return nil, fmt.Errorf("store block: %w", err)
	}
	if _, err := e.state.CommitWithHeight(height); err != nil {
		return nil, fmt.Errorf("state commit: %w", err)
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
	// Advance the chain head in the SAME atomic batch as the block body, and only
	// ever forward. Folding the head write into the batch means a block body can
	// never be persisted without its head (or vice-versa) — the two move together.
	if curH, ok := e.latestHeightRaw(); !ok || b.Header.Height > curH {
		latestData, _ := json.Marshal(map[string]interface{}{
			"height": b.Header.Height,
			"hash":   hashHex,
		})
		batch.Put([]byte(keyLatest), latestData)
	}
	return e.chainDB.Write(batch, nil)
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

func (e *Executor) verifyProducerAndQC(b *types.Block) error {
	if b.Header.Height == 0 {
		return nil
	}
	sched := e.schedule()
	if !sched.Active(forks.MultiValidator, b.Header.Height) {
		return nil
	}
	if err := b.Validate(); err != nil {
		return err
	}
	reg := vm.NewStakingContract(e.state, vm.DeployStaking(nil))
	active, err := reg.ListActive()
	if err != nil {
		return fmt.Errorf("validator set: %w", err)
	}
	byAddr := make(map[qcrypto.Address][]byte, len(active))
	for _, rec := range active {
		byAddr[rec.Address] = rec.PublicKey
	}
	pub := byAddr[b.Header.ValidatorAddr]
	if len(pub) == 0 {
		pub = b.ValidatorPubKey
	}
	if len(pub) == 0 {
		return fmt.Errorf("producer %s is not in the active validator set", b.Header.ValidatorAddr)
	}
	if err := b.VerifySignature(pub); err != nil {
		return err
	}
	if len(active) < 2 {
		return nil
	}
	if len(b.Header.QCRoot) == 0 || len(b.Votes) == 0 {
		return fmt.Errorf("multi_validator requires a quorum certificate at height %d", b.Header.Height)
	}
	need := (len(active)*2)/3 + 1
	if len(b.Votes) < need {
		return fmt.Errorf("qc has %d votes, need %d", len(b.Votes), need)
	}
	seen := make(map[qcrypto.Address]struct{})
	for _, v := range b.Votes {
		pk, ok := byAddr[v.ValidatorAddr]
		if !ok {
			return fmt.Errorf("vote from unknown validator %s", v.ValidatorAddr)
		}
		if _, dup := seen[v.ValidatorAddr]; dup {
			return fmt.Errorf("duplicate vote from %s", v.ValidatorAddr)
		}
		seen[v.ValidatorAddr] = struct{}{}
		if err := v.Verify(pk); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) verifyBlockLinkage(b *types.Block) error {
	if b.Header.ChainID != types.ChainID {
		return fmt.Errorf("wrong chain id: block %q, node %q", b.Header.ChainID, types.ChainID)
	}
	if b.Header.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported block alg_id %q: this node only accepts %q",
			b.Header.AlgID, qcrypto.AlgMLDSA65)
	}

	// Reject a block stamped too far in the future. Contracts read this value as
	// OpTIMESTAMP, so an unbounded timestamp is a state-corruption primitive, not
	// just a cosmetic problem.
	if drift := time.Until(time.Unix(b.Header.Timestamp, 0)); drift > MaxFutureBlockDrift {
		return fmt.Errorf("block timestamp %d is %s ahead of local clock (max %s)",
			b.Header.Timestamp, drift.Truncate(time.Second), MaxFutureBlockDrift)
	}

	latest, err := e.LatestBlock()
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}

	if b.Header.Height == 0 {
		if latest != nil {
			return fmt.Errorf("genesis rejected: chain already has a head at height %d", latest.Header.Height)
		}
		if e.genesisHash != nil {
			computed, herr := b.ComputeHash()
			if herr != nil {
				return fmt.Errorf("hash genesis block: %w", herr)
			}
			if computed != *e.genesisHash {
				return fmt.Errorf("genesis rejected: block 0 hashes to %x, pinned --genesis-hash is %x "+
					"(a reset chain mints a NEW genesis — update or drop the pin)", computed[:8], e.genesisHash[:8])
			}
		}
		return nil
	}

	if latest == nil {
		if b.Header.Height != 1 {
			return fmt.Errorf("cannot apply block %d to an empty chain (expected block 1)", b.Header.Height)
		}
		// Block 1 has no stored parent to link against, so on an empty chain the
		// pin is the only thing tying it to the intended genesis.
		if e.genesisHash != nil && b.Header.PrevHash != *e.genesisHash {
			return fmt.Errorf("block 1 names parent %x, pinned --genesis-hash is %x — wrong chain or hostile peer",
				b.Header.PrevHash[:8], e.genesisHash[:8])
		}
		return nil
	}

	if b.Header.Height != latest.Header.Height+1 {
		return fmt.Errorf("non-contiguous height: got %d, expected %d", b.Header.Height, latest.Header.Height+1)
	}
	if b.Header.PrevHash != latest.Hash {
		return fmt.Errorf("prev-hash mismatch at height %d", b.Header.Height)
	}
	// Block time must not run backwards. A block that predates its parent would
	// let a producer rewind OpTIMESTAMP for contracts while still building a
	// structurally valid chain.
	if b.Header.Timestamp < latest.Header.Timestamp {
		return fmt.Errorf("non-monotonic timestamp at height %d: block %d predates parent %d",
			b.Header.Height, b.Header.Timestamp, latest.Header.Timestamp)
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
