// Package block handles block production, execution, and chain storage.
package block

import (
	"bytes"
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
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

const (
	MaxTxPerBlock       = 10_000
	prefixBlock         = "block:"
	prefixHeight        = "height:"
	prefixChainReceipts = "rcpts:"
	keyLatest           = "latest"

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

	// txIndexHole is set once a live reconcile finds a historical height whose
	// receipts exist nowhere on this node (state.ErrTxIndexIncomplete). The
	// watermark can never pass it, so recordTxIndex stops re-walking to it on
	// every block. Cleared only by a restart, which re-checks once.
	txIndexHole atomic.Bool
	// txIndexWalking is true while StartTxIndexReconcile's walk runs, so the
	// per-block hole reconcile does not start a second one beside it.
	txIndexWalking atomic.Bool

	// dataDir is the node's data directory, used to place the snapshot-import
	// recovery marker (see state_snapshot.go) alongside chaindata. Derived
	// from chainDBPath in NewExecutor; empty only in tests that skip it, in
	// which case snapshot-import recovery marker writes are skipped.
	dataDir string

	// genesisHash pins the hash of block 0 when the operator supplied one.
	// nil = unpinned, which is the pre-existing behaviour.
	genesisHash *[32]byte

	// genesisValidatorKey supplies block 0's sealing key when the chain DB
	// does not hold block 0; see WithGenesisValidatorKey.
	genesisValidatorKey func() []byte
	// warnedCarriedKey makes the "no genesis key known, trusting the carried
	// key" warning fire once rather than on every block.
	warnedCarriedKey atomic.Bool

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

	// x402SettleRules is the x402_settle_v1 predicate. It is installed in
	// NewExecutor exactly the way the VM's gas and call rules are, so the gate
	// on a consensus-visible state leaf is one named place rather than a
	// schedule lookup buried inside execX402. nil means pre-fork: the used-nonce
	// marker is written alone, as it was before the settlement record existed.
	x402SettleRules func(uint64) bool

	// snapshotChunkSessions holds in-flight immutable chunked-snapshot serving
	// sessions, keyed by an opaque session id handed to the client on the first
	// page (see StartSnapshotChunkSession / PageSnapshotChunkSession).
	snapshotChunkMu       sync.Mutex
	snapshotChunkSessions map[string]*snapshotChunkSession

	// snapshotFinalMu guards the bounded cache of completed-transfer final
	// responses, kept separately from the pinned LevelDB snapshots so a lost
	// final page is retryable without holding the whole immutable view open.
	snapshotFinalMu   sync.Mutex
	snapshotFinals    map[string]*snapshotFinalCache
	snapshotSweepStop chan struct{}
	snapshotSweepOnce sync.Once

	// snapshotSessionTTL and snapshotSweepInterval override the package
	// defaults for this executor only. Zero means the default. They exist so a
	// test can drive the session lifecycle in milliseconds without assigning
	// to a package var that another executor's sweeper goroutine is reading.
	snapshotSessionTTL    time.Duration
	snapshotSweepInterval time.Duration

	// applyHalt is set when the chain head was stored but the matching state
	// commit failed. New execution and voting must wait until RecoverPartialApply
	// brings applied height/root in line with the head (review C2).
	applyHalt atomic.Bool

	// proposalWorkLimit is the producer-local cap on cumulative receipt gas
	// while assembling a block (included failed receipts count). Zero means
	// MaxBlockGas. Followers still enforce MaxBlockGas on apply; this only
	// stops the builder from spending more wall time than the work budget.
	proposalWorkLimit uint64

	// onTxFailed, when set, is told how many transactions ended in failure
	// each time this executor settles a block: receipts committed with
	// TxStatusFailed, plus candidates the producer executed and dropped. It
	// runs under mu, so it must be cheap and must not call back into the
	// executor. cmd/node points it at sqc_txs_failed_total, which was exported
	// long before anything drove it.
	onTxFailed func(n int)
}

// ErrApplyHalted is returned when the executor refuses new work because a
// previous persist left the chain head ahead of committed state.
var ErrApplyHalted = errors.New("executor: apply halted until state is reconciled with the stored head")

// ErrQuorumRequired is returned by ProduceBlock when multi_validator is active
// at the height being produced and the registry lists two or more active
// validators. From that height on every block needs a quorum certificate, which
// the single-path producer never attaches: each block it sealed would be
// canonical here and unappliable everywhere else. Refusing is the mainnet-5786
// failure made visible instead of a fork.
//
// It is a backstop, not the node's steering: consensus.Producer leaves the
// single path for BFT at the first multi_validator block, whether that block
// is due at boot or arrives while the node runs, so its own producer never
// reaches this. Any caller that does is driving ProduceBlock on a chain that
// has moved past it.
var ErrQuorumRequired = errors.New("multi_validator: quorum certificate required")

// receiptStorer is satisfied by *state.TxIndexStore.
// Using an interface keeps executor.go from importing the state package's txindex.
type receiptStorer interface {
	StoreReceipt(r *types.Receipt) error
	StoreBlockReceipts(receipts []*types.Receipt) error
	IndexBlock(b *types.Block) error
}

// txIndexProgress is the durable height/backfill surface on TxIndexStore.
type txIndexProgress interface {
	IndexedHeight() (uint64, bool)
	CoverageFrom() (uint64, bool)
	MarkSnapshotCoverage(height uint64) error
	RecordBlock(b *types.Block, receipts []*types.Receipt) error
	Backfill(from, to uint64, load func(uint64) (*types.Block, []*types.Receipt, error)) error
	IndexComplete(b *types.Block) bool
	ClearCoverageFrom() error
}

// NewExecutor creates a new block executor with default settings.
func NewExecutor(stateDB *state.StateDB, chainDBPath string) (*Executor, error) {
	db, err := leveldb.OpenFile(chainDBPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open chain database: %w", err)
	}
	ex := &Executor{
		state:    stateDB,
		chainDB:  db,
		vmEngine: vm.NewEngine(stateDB),
		// The chain DB itself is the most durable place to root the recovery
		// marker: it is the store this executor is opened against, so a
		// marker written here always shares the same data directory as the
		// blocks and state it is guarding an import into.
		dataDir: chainDBPath,
	}
	ex.vmEngine.SetGasRules(func(h uint64) bool {
		return ex.schedule().Active(forks.VMGasV1, h)
	})
	ex.vmEngine.SetCallRules(func(h uint64) bool {
		return ex.schedule().Active(forks.VMCallV1, h)
	})
	ex.SetX402SettleRules(func(h uint64) bool {
		return ex.schedule().Active(forks.X402SettleV1, h)
	})
	return ex, nil
}

// SetX402SettleRules installs the x402_settle_v1 predicate. nil keeps the
// pre-fork registry: the used-nonce marker only, and no settlement-record leaf.
func (e *Executor) SetX402SettleRules(fn func(uint64) bool) {
	e.x402SettleRules = fn
}

// x402SettleV1 reports whether the settlement record is written at height h.
func (e *Executor) x402SettleV1(height uint64) bool {
	return e.x402SettleRules != nil && e.x402SettleRules(height)
}

// WithHooks attaches a hook registry to the executor.
func (e *Executor) WithHooks(h *HookRegistry) *Executor {
	e.hooks = h
	return e
}

// WithTxFailedHook registers fn to be told, per settled block, how many
// transactions failed; see the onTxFailed field. nil clears it.
func (e *Executor) WithTxFailedHook(fn func(n int)) *Executor {
	e.onTxFailed = fn
	return e
}

// noteTxsFailed reports n failed transactions to the hook, when there is one
// and there are any.
func (e *Executor) noteTxsFailed(n int) {
	if n > 0 && e.onTxFailed != nil {
		e.onTxFailed(n)
	}
}

// countFailedReceipts is how many of receipts carry TxStatusFailed.
func countFailedReceipts(receipts []*types.Receipt) int {
	n := 0
	for _, r := range receipts {
		if r != nil && r.Status == types.TxStatusFailed {
			n++
		}
	}
	return n
}

// WithTxIndex attaches a receipt/tx index store.
func (e *Executor) WithTxIndex(idx receiptStorer) *Executor {
	e.txIndex = idx
	return e
}

func (e *Executor) recordTxIndex(b *types.Block, receipts []*types.Receipt) {
	if e.txIndex == nil || b == nil {
		return
	}
	if prog, ok := e.txIndex.(txIndexProgress); ok {
		if err := prog.RecordBlock(b, receipts); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).Msg("tx index: record failed; retrying before reconcile")
			if err := prog.RecordBlock(b, receipts); err != nil {
				log.Warn().Err(err).Uint64("height", b.Header.Height).Msg("tx index: retry failed; reconciling now")
				if e.txIndexWalking.Load() {
					// The boot walk is a second walker over the same store;
					// the first reconcile after it ends repairs this height.
					return
				}
				if rerr := e.ReconcileTxIndex(); rerr != nil {
					log.Warn().Err(rerr).Msg("tx index: live reconcile failed")
				}
				return
			}
		}
		if applied, ok := e.state.AppliedHeight(); ok {
			if ih, iok := prog.IndexedHeight(); !iok || ih < applied {
				if e.txIndexWalking.Load() || e.txIndexHole.Load() {
					// The boot-time walk is still running and will reach this
					// height, or a historical height nothing here can complete
					// keeps the watermark down for good; walking up to it again
					// on every block buys nothing.
					return
				}
				if rerr := e.ReconcileTxIndex(); rerr != nil {
					if errors.Is(rerr, state.ErrTxIndexIncomplete) {
						log.Warn().Err(rerr).Uint64("indexed", ih).Uint64("applied", applied).
							Msg("tx index: a historical height has receipts nowhere on this node; the watermark stays below it, /readyz reports the index behind, and this is not retried per block")
						return
					}
					log.Warn().Err(rerr).Msg("tx index: hole reconcile failed")
				}
			}
		}
		return
	}
	if err := e.txIndex.IndexBlock(b); err != nil {
		log.Warn().Err(err).Msg("tx index: failed to index block")
	}
	if err := e.txIndex.StoreBlockReceipts(receipts); err != nil {
		log.Warn().Err(err).Msg("tx index: failed to store receipts")
	}
}

// HasTxIndex reports whether a receipt/tx index is attached.
func (e *Executor) HasTxIndex() bool { return e.txIndex != nil }

// TxIndexStatus is the durable index progress used by /readyz.
func (e *Executor) TxIndexStatus() (height uint64, heightOK bool, coverageFrom uint64, coverageOK bool) {
	prog, ok := e.txIndex.(txIndexProgress)
	if !ok || e.txIndex == nil {
		return 0, false, 0, false
	}
	height, heightOK = prog.IndexedHeight()
	coverageFrom, coverageOK = prog.CoverageFrom()
	return
}

// ReconcileTxIndex backfills a durable index that lagged a successful state
// commit (or a snapshot join with no pre-checkpoint receipts).
func (e *Executor) ReconcileTxIndex() error {
	if e.txIndex == nil {
		return nil
	}
	prog, ok := e.txIndex.(txIndexProgress)
	if !ok {
		return nil
	}
	applied, appliedOK := e.state.AppliedHeight()
	if !appliedOK {
		return nil
	}
	start := uint64(0)
	if cov, cok := prog.CoverageFrom(); cok {
		start = cov
	} else if first, ok := e.snapshotIndexGapStart(); ok {
		if err := prog.MarkSnapshotCoverage(first); err != nil {
			return err
		}
		start = first
	}
	if head, err := e.LatestBlock(); err == nil && head != nil && head.Header.Height > 0 {
		if _, berr := e.GetBlockByHeight(head.Header.Height - 1); errors.Is(berr, types.ErrBlockNotFound) {
			if err := prog.MarkSnapshotCoverage(head.Header.Height); err != nil {
				return err
			}
			if start == 0 || start > head.Header.Height {
				start = head.Header.Height
			}
		}
	}
	if start > applied {
		return nil
	}
	err := prog.Backfill(start, applied, func(h uint64) (*types.Block, []*types.Receipt, error) {
		b, err := e.GetBlockByHeight(h)
		if errors.Is(err, types.ErrBlockNotFound) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		receipts, rerr := e.loadChainReceipts(h)
		if rerr != nil {
			return nil, nil, rerr
		}
		return b, receipts, nil
	})
	if errors.Is(err, state.ErrTxIndexIncomplete) {
		e.txIndexHole.Store(true)
	}
	return err
}

// StartTxIndexReconcile runs ReconcileTxIndex on its own goroutine and
// reports the result to done. A node upgraded from a build with no index
// watermark walks its whole chain once; doing that before the listener is up
// and before sealing left a tier silent for the length of its history, so
// the walk runs beside the node instead. While it runs, recordTxIndex does
// not start a second walk; /readyz keeps reporting the index behind until
// the watermark catches up.
func (e *Executor) StartTxIndexReconcile(done func(error)) {
	if e.txIndex == nil {
		if done != nil {
			done(nil)
		}
		return
	}
	e.txIndexWalking.Store(true)
	go func() {
		err := e.ReconcileTxIndex()
		e.txIndexWalking.Store(false)
		if done != nil {
			done(err)
		}
	}()
}

// snapshotIndexGapStart reports the first stored height when block 1 is
// missing — the snapshot-join shape — so a lost CoverageFrom mark can be
// rewritten after later blocks have already been applied.
func (e *Executor) snapshotIndexGapStart() (uint64, bool) {
	head, err := e.LatestBlock()
	if err != nil || head == nil || head.Header.Height <= 1 {
		return 0, false
	}
	if _, err := e.GetBlockByHeight(1); !errors.Is(err, types.ErrBlockNotFound) {
		return 0, false
	}
	for h := uint64(2); h <= head.Header.Height; h++ {
		if b, err := e.GetBlockByHeight(h); err == nil && b != nil {
			return h, true
		}
	}
	return head.Header.Height, true
}

func (e *Executor) markSnapshotIndexCoverage(height uint64) {
	if e.txIndex == nil {
		return
	}
	if prog, ok := e.txIndex.(txIndexProgress); ok {
		if err := prog.MarkSnapshotCoverage(height); err != nil {
			log.Warn().Err(err).Uint64("height", height).Msg("tx index: failed to mark snapshot coverage")
		}
	}
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

// WithGenesisValidatorKey names the key that sealed block 0, for the one place
// the registry is not yet the authority on who produces: below two active
// validators a block from an address the registry does not list is accepted,
// and before this the executor accepted it on the key the block CARRIED —
// which every block passes. A chain's genesis validator is typically not
// registered, so the fallback is needed, but it must be pinned to that
// validator, not to whoever signs.
//
// The key is read at call time because a node that pins only --genesis-hash
// learns it from block 0 after start-up. Block 0 in the chain DB, when stored,
// is consulted first; the function is the fallback for a node that never
// stored it (pull-sync and state-sync joiners). With neither, the executor
// keeps the old carried-key rule and says so once in the log.
func (e *Executor) WithGenesisValidatorKey(fn func() []byte) *Executor {
	e.genesisValidatorKey = fn
	return e
}

// WithForks installs the rule-change schedule at construction time, before any
// block is applied. Builder-style like the others above; use SetForks once the
// executor is live.
func (e *Executor) WithForks(s forks.Schedule) *Executor {
	e.installForks(s)
	return e
}

// WithProposalWorkLimit caps cumulative receipt gas the producer will pack
// into one block. Zero (the default) uses MaxBlockGas.
func (e *Executor) WithProposalWorkLimit(n uint64) *Executor {
	e.proposalWorkLimit = n
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

// Close closes the chain database and releases pinned snapshot sessions.
func (e *Executor) Close() error {
	e.stopSnapshotSweeper()
	e.releaseAllSnapshotChunkSessions()
	return e.chainDB.Close()
}

func (e *Executor) notePartialPersist() {
	e.applyHalt.Store(true)
	log.Error().Msg("executor: state commit failed after the head was stored; apply halted until reconciliation")
}

// CanProgress reports whether the node may produce, vote, or apply a new block.
// False after a partial persist until RecoverPartialApply succeeds.
func (e *Executor) CanProgress() bool {
	return !e.applyHalt.Load()
}

// ApplyHalted reports the C2 halt flag. Prefer CanProgress at call sites.
func (e *Executor) ApplyHalted() bool {
	return e.applyHalt.Load()
}

// BlockFullyApplied is the only condition under which a failed ExecuteBlock
// may still be acknowledged as a successful commit: the stored head is this
// block, the committed applied height is this height, and the committed state
// root matches the header. Head-hash alone is not enough — storeBlock writes
// that hash before CommitWithHeight.
func (e *Executor) BlockFullyApplied(b *types.Block) bool {
	if b == nil {
		return false
	}
	head, err := e.LatestBlock()
	if err != nil || head == nil || head.Hash != b.Hash {
		return false
	}
	applied, ok := e.state.AppliedHeight()
	if !ok || applied != b.Header.Height {
		return false
	}
	root, err := e.state.StateRoot()
	if err != nil || root != b.Header.StateRoot {
		return false
	}
	return true
}

func (e *Executor) storesAligned() bool {
	head, err := e.LatestBlock()
	if err != nil {
		return false
	}
	applied, known := e.state.AppliedHeight()
	if head == nil {
		return !known || applied == 0
	}
	if !known || applied != head.Header.Height {
		return false
	}
	root, err := e.state.StateRoot()
	if err != nil {
		return false
	}
	return root == head.Header.StateRoot
}

// StoresAligned reports whether the stored head and the committed state agree:
// the applied height equals the head block's height and the committed state root
// matches the header. Readiness uses it so a node that stored a head but has not
// committed its state is never reported ready.
func (e *Executor) StoresAligned() bool {
	return e.storesAligned()
}

// RecoverPartialApply replays stored blocks onto stale state (ReconcileState)
// and clears the apply halt only when head height/root and applied state match.
func (e *Executor) RecoverPartialApply() error {
	if err := e.ReconcileState(); err != nil {
		return err
	}
	if e.storesAligned() {
		e.applyHalt.Store(false)
	}
	return nil
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
	if e.applyHalt.Load() {
		return ErrApplyHalted
	}

	log.Info().
		Uint64("height", b.Header.Height).
		Int("txs", len(b.Transactions)).
		Msg("executing block")

	receipts, stateRoot, err := e.applyBlockPending(b, true)
	if err != nil {
		return err
	}

	// Root matches — persist. ORDER MATTERS: store the block + advance the chain
	// head FIRST (atomic within the chain DB), THEN commit state with its height
	// marker. A crash between the two leaves the head ahead of the state by one
	// block, which boot reconciliation replays from the already-stored block. The
	// reverse order (state ahead of a head with no stored block) is unrecoverable.
	if err := e.storeBlockAndReceipts(b, receipts); err != nil {
		e.state.DiscardPending()
		return fmt.Errorf("block store failed: %w", err)
	}
	if _, err := e.state.CommitWithHeight(b.Header.Height); err != nil {
		e.state.DiscardPending()
		e.notePartialPersist()
		return fmt.Errorf("state commit failed: %w", err)
	}

	// The block is settled: its failed receipts are now a fact of the chain.
	e.noteTxsFailed(countFailedReceipts(receipts))

	// Index block transactions and store receipts. Failures are not consensus
	// fatal; IndexedHeight stays behind so ReconcileTxIndex can backfill.
	e.recordTxIndex(b, receipts)

	// Run post-block hooks (non-fatal)
	if e.hooks != nil {
		e.hooks.RunPostBlock(b, stateRoot)
	}

	return nil
}

// ValidateProposal judges a BFT proposal exactly as ExecuteBlock would judge
// the committed block — linkage to this node's head, producer authorization
// and signature, every transaction applied, the gas budget, the supply cap and
// the declared state root — and then discards the result. Nothing is
// persisted and the pending layer is left empty.
//
// This is what stands between a vote and a proposal that could never commit.
// Before it, a validator voted on any block from the scheduled leader and
// found out at commit time that the parent was wrong, a transaction was
// unappliable or the state root did not match; a quorum could form on a block
// that no honest node could apply. The certificate is not required here,
// because it does not exist until the votes are in; everything else is.
//
// Callers hold no lock; mu is taken here, so a proposal is never judged while
// a block is being applied.
func (e *Executor) ValidateProposal(b *types.Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.applyHalt.Load() {
		return ErrApplyHalted
	}
	_, _, err := e.applyBlockPending(b, false)
	// Success leaves the block's writes in the pending layer; they must not
	// survive into the next transition.
	e.state.DiscardPending()
	return err
}

// applyBlockPending runs every check and every state transition of a block
// into the pending layer and verifies the declared state root, without
// persisting anything. On error the pending layer is already discarded. On
// success it is left populated for the caller to commit or discard.
// requireQC decides whether a quorum certificate is demanded of the block
// (committed blocks) or not (proposals, which have none yet).
func (e *Executor) applyBlockPending(b *types.Block, requireQC bool) ([]*types.Receipt, [32]byte, error) {
	var zero [32]byte

	// Chain-continuity checks before any state mutation.
	if err := e.verifyBlockLinkage(b); err != nil {
		return nil, zero, fmt.Errorf("block linkage rejected: %w", err)
	}
	if err := e.verifyProducerAndQC(b, requireQC); err != nil {
		return nil, zero, fmt.Errorf("block certificate rejected: %w", err)
	}

	// Run pre-block hooks
	if e.hooks != nil {
		if err := e.hooks.RunPreBlock(b); err != nil {
			return nil, zero, fmt.Errorf("pre-block hook rejected block: %w", err)
		}
	}

	var receipts []*types.Receipt
	totalFees := new(bigInt)
	var blockGas uint64

	for i, tx := range b.Transactions {
		receipt, err := e.executeTxWithReceipt(tx, uint32(i), b.Header.Height, b.Header.Timestamp, b.Hash, b.Header.ValidatorAddr)
		if err != nil {
			// The tx could not be applied at all: bad signature, wrong nonce,
			// unaffordable, unknown type. ProduceBlock drops such txs, so a block
			// that still carries one was not built honestly. It must be rejected
			// outright — the previous behaviour reverted the tx (leaving the
			// sender undebited) yet still credited its fee to the validator,
			// which minted the fee out of nothing on every invalid tx packed.
			e.state.DiscardPending()
			return nil, zero, fmt.Errorf("block contains an unappliable tx at index %d: %w", i, err)
		}
		receipts = append(receipts, receipt)

		// Reject a block that exceeds the consensus gas budget (defence against a
		// producer packing far more compute than the interval can process).
		blockGas += receipt.GasUsed
		if blockGas > MaxBlockGas {
			e.state.DiscardPending()
			return nil, zero, fmt.Errorf("block exceeds gas budget: %d > %d", blockGas, MaxBlockGas)
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
			return nil, zero, fmt.Errorf("fee credit: get validator account: %w", err)
		}
		va.AddBalance(totalFees)
		e.state.SetAccount(va)
	}

	if err := genesis.CheckNativeSupply(e.state); err != nil {
		e.state.DiscardPending()
		return nil, zero, err
	}

	// Verify the declared state root BEFORE persisting.
	stateRoot, err := e.state.PendingStateRoot()
	if err != nil {
		e.state.DiscardPending()
		return nil, zero, fmt.Errorf("state root computation failed: %w", err)
	}
	if stateRoot != b.Header.StateRoot {
		e.state.DiscardPending()
		return nil, zero, fmt.Errorf("state root mismatch: computed %x, header %x",
			stateRoot[:8], b.Header.StateRoot[:8])
	}
	return receipts, stateRoot, nil
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

	// A snapshot import in progress stored the head without the ancestor
	// chain. Replaying from height 1 would fail (those blocks were never
	// stored) and would also be the wrong recovery: the applied state must
	// come from the snapshot, not from re-executing an incomplete history.
	if e.HasIncompleteSnapshotImport() {
		log.Warn().Msg("reconcile: snapshot import still incomplete; skipping replay of the snapshot head")
		return nil
	}

	chainH, err := e.LatestHeight()
	if err != nil {
		return fmt.Errorf("reconcile: read chain head: %w", err)
	}
	stateH, known := e.state.AppliedHeight()
	if !known {
		// No height marker. An empty chain is fine. A stored head with no
		// marker is the C2 split on the first persist (or a legacy DB):
		// replay every stored block so applied height/root catch the head.
		if chainH == 0 {
			if b0, gerr := e.GetBlockByHeight(0); gerr == nil && b0 != nil {
				if err := e.reapplyBlock(b0); err != nil {
					return fmt.Errorf("reconcile: replay block 0: %w", err)
				}
			}
			if e.storesAligned() {
				e.applyHalt.Store(false)
			}
			return nil
		}
		stateH = 0
	} else if stateH > chainH {
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
	if e.storesAligned() {
		e.applyHalt.Store(false)
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
		receipt, err := e.executeTxWithReceipt(tx, uint32(i), b.Header.Height, b.Header.Timestamp, b.Hash, b.Header.ValidatorAddr)
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

	e.recordTxIndex(b, receipts)
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
func (e *Executor) executeTxWithReceipt(tx *types.Transaction, txIndex uint32, blockHeight uint64, blockTime int64, blockHash [32]byte, producer qcrypto.Address) (*types.Receipt, error) {
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
	if err := e.executeTx(tx, blockHeight, blockTime, producer, receipt); err != nil {
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
func (e *Executor) executeTx(tx *types.Transaction, blockHeight uint64, blockTime int64, producer qcrypto.Address, receipt *types.Receipt) (err error) {
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

	// The nonce and the balance are judged BEFORE the signature. A transaction
	// applies only if every check passes, so the order changes no outcome —
	// only which refusal is reported when several apply — and these two are
	// cheap reads where a signature is an ML-DSA-65 verification. It matters
	// because the producer is handed every pool entry on every block: each
	// one it could not pack yet (a nonce ahead of the sender's next, funds
	// spent by an earlier transaction in the same block) paid for a
	// verification here only to be refused on the nonce, on every block,
	// for as long as it stayed in the pool.
	if tx.Nonce != fromAcc.Nonce {
		return fmt.Errorf("%w: want %d got %d", types.ErrInvalidNonce, fromAcc.Nonce, tx.Nonce)
	}
	total := new(types.BigInt).Add(tx.Amount, tx.Fee)
	if fromAcc.Balance.Cmp(total) < 0 {
		return types.ErrInsufficientBalance
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

	switch tx.Type {
	case types.TxTransfer:
		return e.execTransfer(tx, fromAcc, receipt)
	case types.TxDeploy:
		return e.execDeploy(tx, fromAcc, receipt, blockHeight)
	case types.TxCall:
		return e.execCall(tx, fromAcc, receipt, blockHeight, blockTime, producer)
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

func (e *Executor) execDeploy(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64) error {
	if err := fromAcc.SubBalance(tx.Fee); err != nil {
		return err
	}
	fromAcc.IncrNonce()
	e.state.SetAccount(fromAcc)

	contractAddr, err := e.vmEngine.DeployAt(tx.From, tx.Data, tx.Nonce, blockHeight)
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

// Module gas under modules_v2. A native-module call used to record zero gas,
// so the block gas budget — the one limit that bounds how much work a block
// asks of every node — did not see module calls at all. The charge is
// deterministic and depends only on the call's bytes, so producer and
// follower always agree: a flat base for dispatch plus a per-byte charge on
// the envelope. It is charged whether the call succeeds or fails SOFT (the
// work was done either way); a HARD failure leaves no receipt to charge.
//
// These are accounting figures against MaxBlockGas, not a price: the fee a
// module call pays is unchanged.
const (
	ModuleGasBase    uint64 = 50_000
	ModuleGasPerByte uint64 = 16
)

// moduleGas is the gas a module call with the given envelope records.
func moduleGas(data []byte) uint64 {
	return ModuleGasBase + ModuleGasPerByte*uint64(len(data))
}

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

func (e *Executor) execCall(tx *types.Transaction, fromAcc *types.Account, receipt *types.Receipt, blockHeight uint64, blockTime int64, producer qcrypto.Address) error {
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
			Caller:   tx.From,
			Nonce:    tx.Nonce,
			Height:   blockHeight,
			Time:     blockTime,
			Forks:    schedule,
			Producer: producer,
		}

		// modules_v2: the call records gas that counts against the block
		// budget. Set before dispatch so success and SOFT failure both carry
		// it; a HARD failure returns an error and the receipt is dropped.
		if schedule.Active(forks.ModulesV2, blockHeight) {
			receipt.GasUsed = moduleGas(tx.Data)
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
	// vm_gas_v1 uses the same frame so an OOG / hard VM error keeps the fee
	// and nonce while rolling back speculative writes.
	sched := e.schedule()
	vmRevert := sched.Active(forks.VMRevertV1, blockHeight)
	vmGas := sched.Active(forks.VMGasV1, blockHeight)
	vmCall := sched.Active(forks.VMCallV1, blockHeight)
	var frame uint64
	if vmRevert || vmGas || vmCall {
		frame = e.state.Snapshot()
	}

	// vm_call_v1: move the attached value inside the frame BEFORE the
	// contract runs, matching nested CALL. The contract can spend it during
	// this execution; a revert rolls the transfer back with the frame.
	if vmCall && tx.Amount != nil && tx.Amount.Sign() > 0 {
		if err := e.state.Transfer(tx.From, tx.To, tx.Amount); err != nil {
			if frame != 0 {
				_ = e.state.RevertToSnapshot(frame)
			}
			return fmt.Errorf("call value transfer: %w", err)
		}
	}

	result, err := e.vmEngine.CallWithContext(
		tx.From, tx.From, tx.To, tx.Data, tx.Amount,
		blockHeight, blockTime, false,
	)

	reverted := result != nil && result.Reverted
	// Only a result that actually ran (OOG, unknown opcode, stack error)
	// becomes a failed receipt. A nil result is pre-execution (EOA, I/O)
	// and stays dropped so statically invalid calls do not change root.
	if vmGas && result != nil && err != nil && !reverted {
		if rerr := e.state.RevertToSnapshot(frame); rerr != nil {
			return fmt.Errorf("revert of failed call frame: %w", rerr)
		}
		receipt.GasUsed = result.GasUsed
		receipt.Logs = []*types.Log{}
		receipt.ContractAddress = nil
		receipt.SetFailed(err.Error())
		return nil
	}
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
	if vmRevert || vmGas || vmCall {
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

	// Pre-vm_call_v1: credit the call value after a successful run. The
	// contract could not spend newly attached funds during that execution.
	if !vmCall && !reverted && tx.Amount != nil && tx.Amount.Sign() > 0 {
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

	// The settlement record is a state leaf, so writing it changes the state
	// root of every block that carries an X402 payment. Before x402_settle_v1
	// only the used-nonce marker is written — byte for byte the pre-record
	// registry — so an upgraded node and a node still on the old build compute
	// the SAME root and a rolling upgrade does not part the network at the first
	// X402 transaction. At the fork height every node starts writing the record
	// in the same block.
	if e.x402SettleV1(blockHeight) {
		if err := e.state.MarkX402NonceSettled(nonceKey, &types.X402Settlement{
			From:    tx.From,
			To:      tx.To,
			Amount:  new(bigInt).Set(tx.Amount),
			ChainID: tx.ChainID,
			TxHash:  receipt.TxHash,
			Height:  blockHeight,
		}); err != nil {
			return fmt.Errorf("X402 nonce: %w", err)
		}
	} else if err := e.state.MarkX402NonceUsed(nonceKey, blockHeight); err != nil {
		return fmt.Errorf("X402 nonce: %w", err)
	}

	log.Debug().
		Str("payer", tx.From.String()).
		Str("receiver", tx.To.String()).
		Str("amount", tx.Amount.String()).
		Msg("X402 settled")
	return nil
}

// SkippedTx is a candidate the producer left out of the block it built, and
// why, so the pool can drop what will never apply and keep what may.
type SkippedTx struct {
	Tx  *types.Transaction
	Err error
}

// ErrNonceGap marks a candidate whose nonce is ahead of the sender's next.
// Nothing is wrong with it; it cannot apply until the nonces before it have,
// so the producer skips it without executing it and the pool keeps it.
var ErrNonceGap = errors.New("nonce ahead of the sender's next nonce")

// Unappliable reports whether the skip means the transaction can never apply
// from the pool as it stands: a stale nonce, a signature that does not verify,
// funds it no longer has, a hook that refuses it. ErrNonceGap is the one
// reason that clears by itself.
func (s SkippedTx) Unappliable() bool { return !errors.Is(s.Err, ErrNonceGap) }

// ProduceBlock creates, executes, and signs a new block.
func (e *Executor) ProduceBlock(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, error) {
	b, _, err := e.assembleBlock(txs, validatorAddr, validatorPubKey, validatorPrivKey, prevHash, height, true)
	return b, err
}

// ProduceBlockReport is ProduceBlock that also returns the candidates it left
// out of the block, with the reason for each.
func (e *Executor) ProduceBlockReport(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, []SkippedTx, error) {
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
	b, _, err := e.assembleBlock(txs, validatorAddr, validatorPubKey, validatorPrivKey, prevHash, height, false)
	return b, err
}

// BuildProposalReport is BuildProposal that also returns the candidates it
// left out of the block, with the reason for each.
func (e *Executor) BuildProposalReport(
	txs []*types.Transaction,
	validatorAddr qcrypto.Address,
	validatorPubKey []byte,
	validatorPrivKey *qcrypto.PrivateKey,
	prevHash [32]byte,
	height uint64,
) (*types.Block, []SkippedTx, error) {
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
) (b *types.Block, skipped []SkippedTx, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.applyHalt.Load() {
		return nil, nil, ErrApplyHalted
	}
	defer func() {
		if err != nil {
			e.state.DiscardPending()
		}
	}()

	// The single path (persist) has no certificate rule of its own, so it is
	// judged here, before any state mutation, against the same registry a
	// follower reads at the top of ExecuteBlock: the parent's committed state.
	if persist {
		if err := e.refuseSinglePathIfQuorumRequired(height); err != nil {
			return nil, nil, err
		}
	}

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
		dropped   int // candidates that executed, failed, and were left out
	)
	skip := func(tx *types.Transaction, err error) {
		skipped = append(skipped, SkippedTx{Tx: tx, Err: err})
	}

	// The nonce each sender must present next, read once per sender from the
	// state and advanced as its transactions are packed. A candidate that
	// does not present it is left out on this alone — no hook, no signature,
	// no execution — because it could not apply anyway: the executor demands
	// exactly this nonce. The pool hands a sender's entries out in nonce
	// order, so once one of them is left out, every later one of that
	// sender's is a gap and costs a map lookup. Every such entry used to be
	// executed to the point of a signature verification on every block.
	nextNonce := make(map[qcrypto.Address]uint64)

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

		next, known := nextNonce[tx.From]
		if !known {
			acc, err := e.state.GetAccount(tx.From)
			if err != nil {
				log.Warn().Err(err).Str("from", tx.From.String()).Msg("skipping tx: sender account unreadable")
				skip(tx, err)
				continue
			}
			next = acc.Nonce
			nextNonce[tx.From] = next
		}
		switch {
		case tx.Nonce > next:
			skip(tx, ErrNonceGap)
			continue
		case tx.Nonce < next:
			log.Debug().Str("from", tx.From.String()).Uint64("nonce", tx.Nonce).Uint64("want", next).
				Msg("skipping tx with a stale nonce")
			skip(tx, fmt.Errorf("%w: want %d got %d", types.ErrInvalidNonce, next, tx.Nonce))
			continue
		}

		// Run the SAME pre-tx hooks a follower will run when it replays this
		// block. Producing without them means the producer can pack a transaction
		// every follower then rejects — the block is unappliable everywhere but
		// here, which is a fork, not a dropped transaction. Skipping it during
		// production is the only safe direction.
		if e.hooks != nil {
			if err := e.hooks.RunPreTx(tx, height); err != nil {
				log.Warn().Err(err).Msg("skipping tx a follower would reject")
				skip(tx, err)
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
		if err := e.executeTx(tx, height, blockTime, validatorAddr, receipt); err != nil {
			if rerr := e.state.RevertToSnapshot(snap); rerr != nil {
				e.state.DiscardPending()
				return nil, nil, fmt.Errorf("revert of skipped tx: %w (tx error: %v)", rerr, err)
			}
			log.Warn().Err(err).Msg("skipping invalid tx")
			dropped++
			skip(tx, err)
			continue
		}
		// Stop packing once the block gas budget is reached (txs are fee-sorted,
		// so we keep the most valuable ones). Failed receipts under vm_gas_v1
		// count here too — repeated OOG cannot produce an empty-gas block.
		// Revert this tx's state before break.
		workLimit := e.proposalWorkLimit
		if workLimit == 0 {
			workLimit = MaxBlockGas
		}
		if blockGas+receipt.GasUsed > workLimit {
			if err := e.state.RevertToSnapshot(snap); err != nil {
				e.state.DiscardPending()
				return nil, nil, fmt.Errorf("revert of over-budget tx: %w", err)
			}
			break
		}
		if err := e.state.CommitSnapshot(snap); err != nil {
			e.state.DiscardPending()
			return nil, nil, fmt.Errorf("commit of tx snapshot: %w", err)
		}
		// The nonce the sender must present next is read back rather than
		// assumed: every applying path bumps it exactly once today, and the
		// state is the authority if that ever changes.
		if acc, err := e.state.GetAccount(tx.From); err == nil {
			nextNonce[tx.From] = acc.Nonce
		} else {
			nextNonce[tx.From] = tx.Nonce + 1
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
			return nil, nil, fmt.Errorf("validator account: %w", err)
		}
		va.AddBalance(totalFees)
		e.state.SetAccount(va)
	}

	if err := genesis.CheckNativeSupply(e.state); err != nil {
		e.state.DiscardPending()
		return nil, nil, err
	}

	// Compute the root the pending writes WOULD commit to (for the header), but do
	// not persist state yet — the block body + head must be stored first so a
	// crash leaves the head ahead of state (replayable), never the reverse.
	stateRoot, err := e.state.PendingStateRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("pending state root: %w", err)
	}

	txMerkleRoot, err := types.ComputeTxMerkleRoot(executed)
	if err != nil {
		return nil, nil, fmt.Errorf("merkle root: %w", err)
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

	b = &types.Block{Header: header, Transactions: executed}

	hash, err := b.ComputeHash()
	if err != nil {
		return nil, nil, fmt.Errorf("block hash: %w", err)
	}
	b.Hash = hash
	for _, r := range receipts {
		r.BlockHash = hash
	}

	if err := b.SignBlock(validatorPrivKey); err != nil {
		return nil, nil, fmt.Errorf("block sign: %w", err)
	}
	if len(validatorPubKey) > 0 {
		b.ValidatorPubKey = validatorPubKey
	}

	if !persist {
		e.state.DiscardPending()
		// The proposal's failed receipts are reported by ExecuteBlock once a
		// quorum commits it; what was dropped is final now.
		e.noteTxsFailed(dropped)
		return b, skipped, nil
	}

	if err := e.storeBlockAndReceipts(b, receipts); err != nil {
		return nil, nil, fmt.Errorf("store block: %w", err)
	}
	if _, err := e.state.CommitWithHeight(height); err != nil {
		e.notePartialPersist()
		return nil, nil, fmt.Errorf("state commit: %w", err)
	}

	e.recordTxIndex(b, receipts)
	e.noteTxsFailed(dropped + countFailedReceipts(receipts))

	log.Info().
		Uint64("height", height).
		Int("txs", len(executed)).
		Str("hash", fmt.Sprintf("%x", hash[:8])).
		Msg("block produced and signed")

	return b, skipped, nil
}

// ── Chain DB helpers ──────────────────────────────────────────────────────────

func (e *Executor) storeBlock(b *types.Block) error {
	return e.storeBlockAndReceipts(b, nil)
}

func (e *Executor) storeBlockAndReceipts(b *types.Block, receipts []*types.Receipt) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	hashHex := fmt.Sprintf("%x", b.Hash[:])
	heightKey := fmt.Sprintf("%s%d", prefixHeight, b.Header.Height)

	batch := new(leveldb.Batch)
	batch.Put([]byte(prefixBlock+hashHex), data)
	batch.Put([]byte(heightKey), []byte(hashHex))
	if len(receipts) > 0 {
		rcpt, err := json.Marshal(receipts)
		if err != nil {
			return err
		}
		batch.Put([]byte(fmt.Sprintf("%s%d", prefixChainReceipts, b.Header.Height)), rcpt)
	}
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
	// Durable before the matching state commit (C3). Sync=false can lose the
	// head across a machine failure while state survives — or the reverse.
	return persist.Write(e.chainDB, batch)
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

// refuseSinglePathIfQuorumRequired is the producer half of the rule
// verifyProducerAndQC enforces on followers. Caller holds e.mu.
func (e *Executor) refuseSinglePathIfQuorumRequired(height uint64) error {
	if !e.schedule().Active(forks.MultiValidator, height) {
		return nil
	}
	active, err := e.committedActiveValidatorsLocked()
	if err != nil {
		return fmt.Errorf("validator set: %w", err)
	}
	if len(active) < 2 {
		return nil
	}
	return fmt.Errorf("%w: %d active validators at height %d, this block must be sealed through "+
		"BFT (multi_validator is active for it) — the single path cannot attach a certificate",
		ErrQuorumRequired, len(active), height)
}

// ActiveValidatorKey returns the ML-DSA-65 public key of addr when the
// validator registry lists it as Active, and nil otherwise.
//
// It is the resolver the network layer uses to authenticate a block sealed by
// a validator other than the pinned genesis key, before ExecuteBlock runs. It
// reads committed state, which for the next block to apply is the parent
// state — the same set verifyProducerAndQC judges the block against.
// ExecuteBlock remains the authority: it still verifies the certificate.
func (e *Executor) ActiveValidatorKey(addr qcrypto.Address) []byte {
	rec, err := e.CommittedValidator(addr)
	if err != nil || rec == nil || rec.Status != vm.ValidatorActive || len(rec.PublicKey) == 0 {
		return nil
	}
	return append([]byte(nil), rec.PublicKey...)
}

// CommittedActiveValidators is the consensus validator set: committed parent
// state only, taken under the block lock so a discarded proposal cannot change it.
func (e *Executor) CommittedActiveValidators() ([]vm.ValidatorRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.committedActiveValidatorsLocked()
}

// CommittedValidator reads one committed registry record under the block lock.
func (e *Executor) CommittedValidator(addr qcrypto.Address) (*vm.ValidatorRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return vm.NewStakingContract(e.state, vm.DeployStaking(nil)).CommittedView().GetValidator(addr)
}

func (e *Executor) committedActiveValidatorsLocked() ([]vm.ValidatorRecord, error) {
	return vm.NewStakingContract(e.state, vm.DeployStaking(nil)).CommittedView().ListActive()
}

// genesisProducerKey returns the key that sealed block 0, or nil when this
// node cannot know it: block 0 from the chain DB first, then the configured
// resolver (a joiner that never stored block 0).
func (e *Executor) genesisProducerKey() []byte {
	if g, err := e.GetBlockByHeight(0); err == nil && g != nil && len(g.ValidatorPubKey) > 0 {
		return g.ValidatorPubKey
	}
	if e.genesisValidatorKey != nil {
		return e.genesisValidatorKey()
	}
	return nil
}

// verifyProducerAndQC authorizes the block's producer and, once a quorum is
// required of the chain, its certificate. requireQC is false for a proposal,
// which is judged before any vote exists; everything else applies to both.
func (e *Executor) verifyProducerAndQC(b *types.Block, requireQC bool) error {
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
	active, err := e.committedActiveValidatorsLocked()
	if err != nil {
		// Fail closed. An unreadable registry is not an empty registry: the
		// latter has a defined meaning (the genesis validator runs the chain),
		// the former means this node cannot tell who is allowed to produce.
		return fmt.Errorf("validator set unreadable, refusing to judge block %d: %w", b.Header.Height, err)
	}
	byAddr := make(map[qcrypto.Address][]byte, len(active))
	for _, rec := range active {
		byAddr[rec.Address] = rec.PublicKey
	}
	pub := byAddr[b.Header.ValidatorAddr]
	// validators_admission_v1: below two members the genesis validator is the
	// only producer — the BFT engine elects nobody else there (RefreshSet) —
	// so a lone registrant that is not the genesis validator cannot have
	// sealed a canonical block, listed or not. Judge such a block on the
	// genesis key alone. When no genesis key is known the rule cannot be
	// applied and the pre-fork behaviour (a listed producer is accepted)
	// stands, which the carried-key warning below already covers.
	if len(active) < 2 && len(pub) > 0 && sched.Active(forks.ValidatorsAdmissionV1, b.Header.Height) {
		if gk := e.genesisProducerKey(); len(gk) > 0 {
			pub = nil
		}
	}
	if len(pub) == 0 {
		// Once a quorum is required the leader is always drawn from the active
		// set, so a producer outside it cannot have sealed a canonical block.
		if len(active) >= 2 {
			return fmt.Errorf("multi_validator: producer %s is not in the active validator set (%d members) at height %d",
				b.Header.ValidatorAddr, len(active), b.Header.Height)
		}
		// Below two the registry is not yet the authority on who produces: a
		// chain's genesis validator is typically not registered, and the block
		// in which the first validator registers is sealed by a producer the
		// parent state does not list. That producer is the genesis validator
		// and nobody else, so the fallback is pinned to block 0's key. The key
		// the block CARRIES is trusted only when this node has no way to know
		// the genesis key at all — and it says so.
		if gk := e.genesisProducerKey(); len(gk) > 0 {
			gpub, perr := qcrypto.PublicKeyFromBytes(gk)
			if perr != nil {
				return fmt.Errorf("genesis validator key unreadable: %w", perr)
			}
			if qcrypto.DeriveAddress(gpub) != b.Header.ValidatorAddr {
				return fmt.Errorf("producer %s is neither in the active validator set (%d members) nor the genesis validator %s at height %d",
					b.Header.ValidatorAddr, len(active), qcrypto.DeriveAddress(gpub), b.Header.Height)
			}
			pub = gk
		} else {
			if e.warnedCarriedKey.CompareAndSwap(false, true) {
				log.Warn().Uint64("height", b.Header.Height).
					Msg("no genesis validator key known (no block 0 stored, no --validator-pubkey-hex): " +
						"authenticating an unregistered producer on the key its block carries")
			}
			pub = b.ValidatorPubKey
		}
	}
	if len(pub) == 0 {
		return fmt.Errorf("producer %s is not in the active validator set", b.Header.ValidatorAddr)
	}
	if err := b.VerifySignature(pub); err != nil {
		return err
	}
	// Whoever may certify: the active set, plus — below two members — the
	// producer just authorized above, since a sole BFT validator (or the
	// unregistered genesis validator) self-certifies with its own vote.
	if _, ok := byAddr[b.Header.ValidatorAddr]; !ok && len(active) < 2 {
		byAddr[b.Header.ValidatorAddr] = pub
	}

	hasCert := len(b.Header.QCRoot) > 0 || len(b.Votes) > 0
	if !requireQC && !hasCert {
		return nil // a proposal: the certificate does not exist yet
	}
	if len(active) < 2 && !hasCert {
		return nil // history sealed on the single path before any quorum was demanded
	}
	if len(b.Header.QCRoot) == 0 || len(b.Votes) == 0 {
		return fmt.Errorf("multi_validator requires a quorum certificate at height %d", b.Header.Height)
	}
	// From here a certificate is present and is judged in full whatever the
	// set size. A malformed certificate on a one-validator chain used to pass
	// unread, so "qc_root present" meant nothing until a second validator
	// registered.
	if root := types.ComputeQCRoot(b.Votes); !bytes.Equal(root[:], b.Header.QCRoot) {
		return fmt.Errorf("qc_root does not cover the votes at height %d", b.Header.Height)
	}
	n := len(active)
	if n == 0 {
		n = 1
	}
	need := (n*2)/3 + 1
	if len(b.Votes) < need {
		return fmt.Errorf("qc has %d votes, need %d", len(b.Votes), need)
	}
	seen := make(map[qcrypto.Address]struct{})
	for _, v := range b.Votes {
		pk, ok := byAddr[v.ValidatorAddr]
		if !ok || len(pk) == 0 {
			return fmt.Errorf("vote from unknown validator %s", v.ValidatorAddr)
		}
		if _, dup := seen[v.ValidatorAddr]; dup {
			return fmt.Errorf("duplicate vote from %s", v.ValidatorAddr)
		}
		seen[v.ValidatorAddr] = struct{}{}
		// Validate() has already bound every vote to this block's hash. The
		// height inside the signed body must agree with it too, so a
		// certificate cannot be assembled from votes their signers cast about
		// a different height.
		if v.Height != b.Header.Height {
			return fmt.Errorf("vote from %s is for height %d, block is %d",
				v.ValidatorAddr, v.Height, b.Header.Height)
		}
		if sched.Active(forks.BFTRoundsV1, b.Header.Height) && v.Kind != types.VoteKindPrecommit {
			return fmt.Errorf("bft_rounds_v1: vote from %s is %q, want precommit", v.ValidatorAddr, v.Kind)
		}
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

// HeadConsensus is what /status reports about the certificate on the head
// block. It exists because the fork bit alone ("multi_validator": true) says
// only that a rule is scheduled — not that the head carries a certificate,
// nor that the certificate verifies. Presence and validity are reported
// separately: a block can carry a qc_root and votes that do not verify.
type HeadConsensus struct {
	// Height is the head the rest of the fields describe.
	Height uint64 `json:"height"`
	// MultiValidatorActive is whether the multi_validator rule applied to
	// the head block (schedule.Active at Height).
	MultiValidatorActive bool `json:"multi_validator_active"`
	// QCPresent is whether the head carries a qc_root and at least one vote.
	QCPresent bool `json:"qc_present"`
	// QCVotes is how many votes the head carries.
	QCVotes int `json:"qc_votes"`
	// QuorumRequired is floor(2n/3)+1 over the validator set below, with n
	// treated as 1 when the registry is empty (the genesis validator is
	// typically not registered and self-certifies with its own vote).
	QuorumRequired int `json:"quorum_required"`
	// QCValid is true only when a certificate is present, every vote is from
	// the validator set (or from the producer itself while the set has fewer
	// than two members), no voter is duplicated, every vote names this
	// block's hash and height, every signature verifies, and the vote count
	// meets QuorumRequired.
	QCValid bool `json:"qc_valid"`
	// Reason names the first check that failed when QCValid is false; empty
	// when it is true.
	Reason string `json:"reason,omitempty"`
	// ValidatorSetSize is the number of Active registry entries in committed
	// state, and ValidatorSetHeight the height that state is committed at
	// (the head). This is the set judged against here; the executor judged
	// the head against the PARENT state when it applied it, and the two
	// differ exactly when the head itself registered or exited a validator.
	ValidatorSetSize   int    `json:"validator_set_size"`
	ValidatorSetHeight uint64 `json:"validator_set_height"`
}

// HeadConsensus inspects the head block's certificate against the validator
// set in committed state. It never fails the node: an unreadable head or
// registry is reported in Reason with QCValid false.
func (e *Executor) HeadConsensus() HeadConsensus {
	head, err := e.LatestBlock()
	if err != nil || head == nil {
		return HeadConsensus{Reason: "no head block"}
	}
	hc := HeadConsensus{
		Height:               head.Header.Height,
		MultiValidatorActive: e.schedule().Active(forks.MultiValidator, head.Header.Height),
		QCPresent:            len(head.Header.QCRoot) > 0 && len(head.Votes) > 0,
		QCVotes:              len(head.Votes),
		ValidatorSetHeight:   head.Header.Height,
	}
	active, err := e.CommittedActiveValidators()
	if err != nil {
		hc.Reason = "validator set unreadable: " + err.Error()
		return hc
	}
	hc.ValidatorSetSize = len(active)
	n := len(active)
	if n == 0 {
		n = 1
	}
	hc.QuorumRequired = (n*2)/3 + 1

	if !hc.QCPresent {
		hc.Reason = "no certificate on head"
		return hc
	}
	if got := types.ComputeQCRoot(head.Votes); !bytes.Equal(got[:], head.Header.QCRoot) {
		hc.Reason = "qc_root does not match the votes"
		return hc
	}
	byAddr := make(map[qcrypto.Address][]byte, len(active))
	for _, rec := range active {
		byAddr[rec.Address] = rec.PublicKey
	}
	if _, ok := byAddr[head.Header.ValidatorAddr]; !ok && len(active) < 2 {
		// Same bootstrap rule as verifyProducerAndQC: below two members the
		// producer may be the unregistered genesis validator.
		byAddr[head.Header.ValidatorAddr] = head.ValidatorPubKey
	}
	seen := make(map[qcrypto.Address]struct{}, len(head.Votes))
	for _, v := range head.Votes {
		pk, ok := byAddr[v.ValidatorAddr]
		if !ok || len(pk) == 0 {
			hc.Reason = fmt.Sprintf("vote from %s, not in the validator set", v.ValidatorAddr)
			return hc
		}
		if _, dup := seen[v.ValidatorAddr]; dup {
			hc.Reason = fmt.Sprintf("duplicate vote from %s", v.ValidatorAddr)
			return hc
		}
		seen[v.ValidatorAddr] = struct{}{}
		if v.Height != head.Header.Height {
			hc.Reason = fmt.Sprintf("vote from %s is for height %d", v.ValidatorAddr, v.Height)
			return hc
		}
		if v.BlockHash != head.Hash {
			hc.Reason = fmt.Sprintf("vote from %s is for another block", v.ValidatorAddr)
			return hc
		}
		if err := v.Verify(pk); err != nil {
			hc.Reason = err.Error()
			return hc
		}
	}
	if len(head.Votes) < hc.QuorumRequired {
		hc.Reason = fmt.Sprintf("qc has %d votes, need %d", len(head.Votes), hc.QuorumRequired)
		return hc
	}
	hc.QCValid = true
	return hc
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

func (e *Executor) loadChainReceipts(height uint64) ([]*types.Receipt, error) {
	raw, err := e.chainDB.Get([]byte(fmt.Sprintf("%s%d", prefixChainReceipts, height)), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipts []*types.Receipt
	if err := json.Unmarshal(raw, &receipts); err != nil {
		return nil, err
	}
	return receipts, nil
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
