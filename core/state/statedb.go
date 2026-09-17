// Package state manages the Splendor Quantum Chain account state.
// Accounts, contract code, contract storage and X402 nonces are stored in
// LevelDB. The state root is a binary Merkle commitment over the ENTIRE state
// (every acc:/code:/store:/x402nonce: leaf), not just accounts — so contract
// storage divergence can no longer hide behind an accounts-only root. Leaves are
// length-prefixed and domain-separated before hashing, so distinct (key,value)
// pairs can never collide through concatenation ambiguity.
//
// The root is computed from an in-memory leaf-hash index that is populated once
// at open and kept in sync exclusively through Commit(), so it can never drift
// from what is durably on disk. All writes land on an in-memory pending layer
// (dirty accounts, code cache, raw storage/nonce overlay) and are committed
// atomically; the same layer is captured by Snapshot()/RevertToSnapshot() so a
// failed transaction — including its storage writes — leaves no residue.
package state

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	// Key prefixes for LevelDB namespacing
	prefixAccount   = "acc:"
	prefixCode      = "code:"
	prefixStorage   = "store:"
	prefixX402Nonce = "x402nonce:"

	// prefixMeta namespaces node-local bookkeeping (e.g. the applied-height
	// marker) that is deliberately EXCLUDED from the state root — it is not
	// consensus state and must not affect the root every node agrees on.
	prefixMeta     = "meta:"
	keyStateHeight = "meta:height"
)

// stateEmptyRoot is the state root of a state with no leaves at all.
var stateEmptyRoot = qcrypto.Hash32([]byte("splendor-quantum:empty-state-root:v1"))

// StateDB is the account state database.
// It wraps LevelDB and provides account read/write operations.
// All writes are performed on a pending layer and committed atomically.
// Use Snapshot() / RevertToSnapshot() for sub-call rollback support.
type StateDB struct {
	mu sync.RWMutex

	// Persistent storage
	db *leveldb.DB

	// In-memory dirty accounts (pending writes not yet committed to disk)
	dirty map[qcrypto.Address]*types.Account

	// In-memory contract code store (keyed by code hash)
	codeCache map[[32]byte][]byte

	// In-memory pending raw writes (contract storage slots + X402 nonces),
	// keyed by their full LevelDB key. These used to be written straight to
	// LevelDB mid-execution, which bypassed both the snapshot/revert path and
	// the state root. They now stage here and commit atomically with everything
	// else.
	pendingRaw map[string][]byte

	// leaves is the in-memory committed leaf-hash index: LevelDB key -> SHA3-256
	// of the committed value. It mirrors the durable state and is the sole input
	// to the Merkle state root. Populated at open, mutated only by Commit().
	leaves map[string][32]byte

	// appliedHeight is the block height the committed state reflects (persisted
	// under meta: alongside the state batch). heightKnown is false until the
	// first CommitWithHeight. Used by boot reconciliation to detect and replay a
	// block whose body was persisted before its state commit landed.
	appliedHeight uint64
	heightKnown   bool

	// journal is an undo-log of every pending mutation since the block started.
	// Snapshot() records the journal length (O(1)); RevertToSnapshot() replays
	// the tail undos back to that mark (O(changes-since-mark)). This replaces the
	// old copy-the-whole-dirty-set-per-snapshot scheme, which was O(N^2) across a
	// block (a per-tx snapshot deep-copied every account dirtied so far, each with
	// its ~2 KiB pubkey) and capped block throughput far below the verify bound.
	journal []func()

	// snapshots maps a snapshot ID to its journal-length mark.
	snapshots map[uint64]int

	// nextSnap is the last snapshot ID this StateDB issued. Per-StateDB on
	// purpose: ids are looked up only in this StateDB's own map, and a
	// Simulate() sandbox starts its own count at zero (see Snapshot).
	nextSnap uint64

	// sandbox marks a throwaway overlay produced by Simulate(). Its pending
	// layer is private, so speculative execution cannot be committed and cannot
	// disturb the consensus state db it was derived from.
	sandbox bool

	// nativeSum is the native balance total of the COMMITTED state, kept
	// current by commit, or nil until it is first needed. It lets
	// SumNativeBalances answer from the pending layer alone. The full scan it
	// used to do — every account record read from LevelDB and decoded, twice
	// per block on every node — was the whole apply cost of a one-transaction
	// block once the chain held tens of thousands of accounts.
	nativeSum *big.Int

	// gen counts changes to the pending layer and the leaf index; touchLocked
	// advances it. pendingRoot is the root PendingStateRoot computed at
	// generation pendingRootGen, so a commit that follows with the generation
	// unchanged returns it instead of hashing the whole state a second time.
	gen            uint64
	pendingRoot    [32]byte
	pendingRootGen uint64

	// failNextCommit, when set, is returned by the next commit() and then
	// cleared. Tests use it to inject a state-write failure after the caller
	// has already persisted the block/head (C2).
	failNextCommit error

	// failNextStorage, when set, is returned by the next storage get and then
	// cleared. Tests inject an unreadable registry (C4). Guarded by injectMu
	// so a consume does not race GetStorageValue's read lock.
	injectMu        sync.Mutex
	failNextStorage error
}

// touchLocked records a change to the pending layer or the leaf index, which
// retires the cached pending root. Caller holds s.mu.
func (s *StateDB) touchLocked() {
	s.gen++
}

// cachedRootLocked returns the root PendingStateRoot computed for the
// current generation, if there is one. Caller holds s.mu.
func (s *StateDB) cachedRootLocked() ([32]byte, bool) {
	if s.pendingRootGen != 0 && s.pendingRootGen == s.gen {
		return s.pendingRoot, true
	}
	return [32]byte{}, false
}

// ErrSandboxCommit is returned when a caller tries to persist a simulation.
var ErrSandboxCommit = errors.New("state: cannot commit a simulation sandbox")

// Simulate returns a throwaway StateDB for speculative execution — RPC view
// calls and gas estimation.
//
// It reads through to the same committed LevelDB state and starts from a copy
// of the current pending layer, but owns that layer outright: its own dirty
// map, code cache, raw overlay, journal, snapshot marks and snapshot-id
// counter. Handing the live
// StateDB to the VM instead (which /call and /estimate did) shares one journal
// between an unauthenticated public endpoint and block execution. The engine
// snapshots and reverts freely during a call, and RevertToSnapshot replays
// undos past any earlier mark and drops the snapshot IDs above it — so a remote
// caller could roll back the block being assembled.
//
// The copy is shallow, which is safe because pending entries are only ever
// replaced, never mutated in place: SetAccount stores a copy and GetAccount
// hands one back, and SetStorageValue does the same for raw slots. Writes made
// during the simulation therefore land only in the sandbox's maps.
//
// The sandbox refuses to Commit. Callers simply drop it when the call returns.
func (s *StateDB) Simulate() *StateDB {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sim := &StateDB{
		db:         s.db,
		dirty:      make(map[qcrypto.Address]*types.Account, len(s.dirty)),
		codeCache:  make(map[[32]byte][]byte, len(s.codeCache)),
		pendingRaw: make(map[string][]byte, len(s.pendingRaw)),
		leaves:     nil, // state roots are meaningless for a simulation
		snapshots:  make(map[uint64]int),
		sandbox:    true,
		gen:        1,
	}
	if s.nativeSum != nil {
		sim.nativeSum = new(big.Int).Set(s.nativeSum)
	}
	for k, v := range s.dirty {
		sim.dirty[k] = v
	}
	for k, v := range s.codeCache {
		sim.codeCache[k] = v
	}
	for k, v := range s.pendingRaw {
		sim.pendingRaw[k] = v
	}
	return sim
}

// IsSandbox reports whether this StateDB is a Simulate() overlay.
func (s *StateDB) IsSandbox() bool { return s.sandbox }

// NewStateDB opens (or creates) the state database at the given path.
func NewStateDB(path string) (*StateDB, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open state database at %s: %w", path, err)
	}
	s := &StateDB{
		db:         db,
		dirty:      make(map[qcrypto.Address]*types.Account),
		codeCache:  make(map[[32]byte][]byte),
		pendingRaw: make(map[string][]byte),
		leaves:     make(map[string][32]byte),
		snapshots:  make(map[uint64]int),
		gen:        1,
	}
	if err := s.loadLeaves(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load state leaf index: %w", err)
	}
	return s, nil
}

// loadLeaves scans the committed state once and builds the in-memory leaf-hash
// index the Merkle root is computed from. Every key/value in the state DB is a
// state leaf (acc:/code:/store:/x402nonce:), so a full scan is exactly the leaf
// set.
func (s *StateDB) loadLeaves() error {
	iter := s.db.NewIterator(nil, nil)
	defer iter.Release()
	for iter.Next() {
		k := iter.Key()
		// meta: keys are node-local bookkeeping, not consensus leaves.
		if len(k) >= len(prefixMeta) && string(k[:len(prefixMeta)]) == prefixMeta {
			if string(k) == keyStateHeight {
				var h uint64
				if json.Unmarshal(iter.Value(), &h) == nil {
					s.appliedHeight = h
					s.heightKnown = true
				}
			}
			continue
		}
		s.leaves[string(k)] = qcrypto.Hash32(iter.Value())
	}
	return iter.Error()
}

// SumNativeBalances totals every account's native SPLD balance, overlaying
// uncommitted dirty writes on committed state. This is the circulating native
// supply a block apply must not raise above the genesis-minted cap.
//
// The committed total is scanned once and then carried forward by commit, so
// a call costs one committed-record read per dirty account rather than one
// per account in the chain.
func (s *StateDB) SumNativeBalances() (*big.Int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nativeSum == nil {
		sum, err := s.scanNativeBalances()
		if err != nil {
			return nil, err
		}
		s.nativeSum = sum
	}
	total := new(big.Int).Set(s.nativeSum)
	for addr, acc := range s.dirty {
		was, err := s.committedBalance(addr)
		if err != nil {
			return nil, err
		}
		total.Sub(total, was)
		if acc != nil && acc.Balance != nil {
			total.Add(total, acc.Balance)
		}
	}
	return total, nil
}

// scanNativeBalances totals the committed native balances by reading every
// account record: the slow path, run once per process and again after a
// snapshot import. Caller holds s.mu.
func (s *StateDB) scanNativeBalances() (*big.Int, error) {
	sum := new(big.Int)
	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixAccount)), nil)
	defer iter.Release()
	for iter.Next() {
		var acc types.Account
		if err := json.Unmarshal(iter.Value(), &acc); err != nil {
			return nil, fmt.Errorf("decode account at key %q: %w", iter.Key(), err)
		}
		if acc.Balance != nil {
			sum.Add(sum, acc.Balance)
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return sum, nil
}

// committedBalance reads an account's native balance as committed — zero for
// an account the committed state does not hold. Caller holds s.mu.
func (s *StateDB) committedBalance(addr qcrypto.Address) (*big.Int, error) {
	data, err := s.db.Get([]byte(prefixAccount+addr.String()), nil)
	if err == leveldb.ErrNotFound {
		return new(big.Int), nil
	}
	if err != nil {
		return nil, fmt.Errorf("state read error for %s: %w", addr, err)
	}
	var acc types.Account
	if err := json.Unmarshal(data, &acc); err != nil {
		return nil, fmt.Errorf("failed to decode account %s: %w", addr, err)
	}
	if acc.Balance == nil {
		return new(big.Int), nil
	}
	return acc.Balance, nil
}

// Close closes the underlying LevelDB.
func (s *StateDB) Close() error {
	return s.db.Close()
}

// PutMeta records node-local bookkeeping under the meta: namespace, next to
// the applied-height marker.
//
// It writes straight to LevelDB rather than through the pending layer: meta
// keys are not consensus state (loadLeaves skips them, so they never reach the
// state root), and the callers that use this — the node persisting the fork
// schedule it was started under — need the value durable on its own, before
// the allocations it governs are committed. A simulation sandbox refuses, for
// the same reason it refuses to Commit. "height" is reserved for the marker
// CommitWithHeight maintains.
func (s *StateDB) PutMeta(key string, value []byte) error {
	if s.sandbox {
		return ErrSandboxCommit
	}
	if key == "" {
		return errors.New("state: meta key must not be empty")
	}
	full := prefixMeta + key
	if full == keyStateHeight {
		return errors.New("state: meta key \"height\" is reserved for the applied-height marker")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.Put([]byte(full), value, nil); err != nil {
		return fmt.Errorf("state: write meta %q: %w", key, err)
	}
	return nil
}

// GetMeta reads a value stored by PutMeta. ok is false when nothing is stored
// under the key, which is not an error: a chain older than the key simply has
// no record.
func (s *StateDB) GetMeta(key string) (value []byte, ok bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.db.Get([]byte(prefixMeta+key), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("state: read meta %q: %w", key, err)
	}
	return v, true, nil
}

// ForEachAccount calls fn for every account in COMMITTED state, in key order.
//
// It deliberately ignores the pending layer, so it reports what is durably on
// disk rather than what a running node happens to be holding. Callers that
// export state for a genesis snapshot want exactly that, and should run against
// a stopped node — reading a live one gives a torn view spanning whatever block
// was mid-flight.
//
// Only accounts are visited. Contract code and storage are separate leaves and
// are not reachable through this, which is a limitation for any caller that
// wants to reproduce a chain in full.
func (s *StateDB) ForEachAccount(fn func(*types.Account) error) error {
	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixAccount)), nil)
	defer iter.Release()
	for iter.Next() {
		var acc types.Account
		if err := json.Unmarshal(iter.Value(), &acc); err != nil {
			return fmt.Errorf("decode account at key %q: %w", iter.Key(), err)
		}
		if err := fn(&acc); err != nil {
			return err
		}
	}
	return iter.Error()
}

// GetAccount retrieves an account by address.
// Returns a new zero-balance account if not found (accounts are implicitly created).
func (s *StateDB) GetAccount(addr qcrypto.Address) (*types.Account, error) {
	s.mu.RLock()
	// Check dirty cache first
	if acc, ok := s.dirty[addr]; ok {
		s.mu.RUnlock()
		return acc.Copy(), nil
	}
	s.mu.RUnlock()

	// Load from LevelDB
	key := []byte(prefixAccount + addr.String())
	data, err := s.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return types.NewAccount(addr), nil
	}
	if err != nil {
		return nil, fmt.Errorf("state read error for %s: %w", addr, err)
	}

	var acc types.Account
	if err := json.Unmarshal(data, &acc); err != nil {
		return nil, fmt.Errorf("failed to decode account %s: %w", addr, err)
	}
	if acc.Balance == nil {
		acc.Balance = big.NewInt(0)
	}
	return &acc, nil
}

// SetAccount writes an account to the dirty cache.
// Changes are not persisted until Commit() is called.
func (s *StateDB) SetAccount(acc *types.Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addr := acc.Address
	prev, had := s.dirty[addr]
	s.journal = append(s.journal, func() {
		if had {
			s.dirty[addr] = prev
		} else {
			delete(s.dirty, addr)
		}
	})
	s.dirty[addr] = acc.Copy()
	s.touchLocked()
}

// GetBalance returns the SPLD balance of an account (in smallest unit).
func (s *StateDB) GetBalance(addr qcrypto.Address) (*big.Int, error) {
	acc, err := s.GetAccount(addr)
	if err != nil {
		return big.NewInt(0), err
	}
	if acc.Balance == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(acc.Balance), nil
}

// GetNonce returns the current nonce of an account.
func (s *StateDB) GetNonce(addr qcrypto.Address) (uint64, error) {
	acc, err := s.GetAccount(addr)
	if err != nil {
		return 0, err
	}
	return acc.Nonce, nil
}

// GetPublicKey returns the registered ML-DSA-65 public key for an address.
// Returns nil if the account has no registered key.
func (s *StateDB) GetPublicKey(addr qcrypto.Address) ([]byte, error) {
	acc, err := s.GetAccount(addr)
	if err != nil {
		return nil, err
	}
	if !acc.HasPublicKey() {
		return nil, types.ErrNoPublicKey
	}
	pk := make([]byte, len(acc.PublicKey))
	copy(pk, acc.PublicKey)
	return pk, nil
}

// Transfer moves SPLD from one account to another.
// Both accounts must exist (or be auto-created). Returns error if insufficient funds.
// Changes are staged to dirty cache; call Commit() to persist.
//
// from == to is handled explicitly. GetAccount returns an independent COPY, so
// loading sender and recipient separately for the same address yields two
// objects that both start at balance B; applying -amount to one and +amount to
// the other and writing both back leaves whichever was written last, i.e. B+amount.
// That minted native SPLD on every self-transfer. The VM reaches this through
// the TRANSFER and SELFDESTRUCT opcodes, so it is attacker-controlled.
func (s *StateDB) Transfer(from, to qcrypto.Address, amount *big.Int) error {
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("negative transfer amount")
	}
	if amount.Sign() == 0 {
		return nil
	}

	fromAcc, err := s.GetAccount(from)
	if err != nil {
		return err
	}

	// Self-transfer: the balance cannot change, but the funds must still exist —
	// otherwise a self-transfer would be a free way to skip the solvency check
	// that callers rely on before treating the transfer as settled.
	if from == to {
		if fromAcc.Balance == nil || fromAcc.Balance.Cmp(amount) < 0 {
			return types.ErrInsufficientBalance
		}
		return nil
	}

	toAcc, err := s.GetAccount(to)
	if err != nil {
		return err
	}

	if err := fromAcc.SubBalance(amount); err != nil {
		return err
	}
	toAcc.AddBalance(amount)

	s.SetAccount(fromAcc)
	s.SetAccount(toAcc)
	return nil
}

// SetCode stores contract bytecode under its SHA3-256 hash.
func (s *StateDB) SetCode(codeHash [32]byte, code []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.codeCache[codeHash]
	s.journal = append(s.journal, func() {
		if had {
			s.codeCache[codeHash] = prev
		} else {
			delete(s.codeCache, codeHash)
		}
	})
	s.codeCache[codeHash] = code
	s.touchLocked()
}

// GetCode retrieves contract bytecode by its hash.
func (s *StateDB) GetCode(codeHash [32]byte) ([]byte, error) {
	s.mu.RLock()
	if code, ok := s.codeCache[codeHash]; ok {
		s.mu.RUnlock()
		return code, nil
	}
	s.mu.RUnlock()

	key := append([]byte(prefixCode), codeHash[:]...)
	data, err := s.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrContractNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("code read error: %w", err)
	}
	return data, nil
}

// storageKey builds the full LevelDB key for a contract storage slot.
func storageKey(contract qcrypto.Address, slot [32]byte) string {
	var b []byte
	b = append(b, prefixStorage...)
	b = append(b, contract.String()...)
	b = append(b, ':')
	b = append(b, slot[:]...)
	return string(b)
}

// SetStorageValue stages a contract storage slot value on the pending layer.
// It is NOT written to disk (and NOT folded into the state root) until Commit();
// this keeps it covered by Snapshot()/RevertToSnapshot() so a failed tx's storage
// writes are rolled back instead of leaving residue that silently forks nodes.
func (s *StateDB) SetStorageValue(contract qcrypto.Address, slot [32]byte, value []byte) error {
	k := storageKey(contract, slot)
	cp := make([]byte, len(value))
	copy(cp, value)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journalRaw(k)
	s.pendingRaw[k] = cp
	s.touchLocked()
	return nil
}

// journalRaw records an undo for a pendingRaw key. Caller holds s.mu.
func (s *StateDB) journalRaw(k string) {
	prev, had := s.pendingRaw[k]
	s.journal = append(s.journal, func() {
		if had {
			s.pendingRaw[k] = prev
		} else {
			delete(s.pendingRaw, k)
		}
	})
}

// GetStorageValue retrieves a contract storage slot value (pending overlay first).
func (s *StateDB) GetStorageValue(contract qcrypto.Address, slot [32]byte) ([]byte, error) {
	return s.getStorageValue(contract, slot, true)
}

// GetCommittedStorageValue reads a slot from durable state only, ignoring the
// pending overlay. Consensus set reads use this so a discarded proposal cannot
// change the validator set (C4).
func (s *StateDB) GetCommittedStorageValue(contract qcrypto.Address, slot [32]byte) ([]byte, error) {
	return s.getStorageValue(contract, slot, false)
}

func (s *StateDB) getStorageValue(contract qcrypto.Address, slot [32]byte, includePending bool) ([]byte, error) {
	k := storageKey(contract, slot)

	s.injectMu.Lock()
	if s.failNextStorage != nil {
		err := s.failNextStorage
		s.failNextStorage = nil
		s.injectMu.Unlock()
		return nil, err
	}
	s.injectMu.Unlock()

	s.mu.RLock()
	if includePending {
		if v, ok := s.pendingRaw[k]; ok {
			cp := make([]byte, len(v))
			copy(cp, v)
			s.mu.RUnlock()
			return cp, nil
		}
	}
	s.mu.RUnlock()

	data, err := s.db.Get([]byte(k), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	return data, err
}

// FailNextStorageRead makes the next GetStorageValue / GetCommittedStorageValue
// return err without reading. Consumed once.
func (s *StateDB) FailNextStorageRead(err error) {
	s.injectMu.Lock()
	defer s.injectMu.Unlock()
	s.failNextStorage = err
}

// MarkX402NonceUsed stages an X402 payment nonce as used on the pending layer.
// key = SHA3-256(from_address + nonce_bytes)
func (s *StateDB) MarkX402NonceUsed(nonceKey [32]byte, blockHeight uint64) error {
	k := prefixX402Nonce + string(nonceKey[:])
	val, err := json.Marshal(blockHeight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journalRaw(k)
	s.pendingRaw[k] = val
	s.touchLocked()
	return nil
}

// IsX402NonceUsed returns true if the X402 payment nonce has already been used
// (checking the pending overlay first, then committed state).
func (s *StateDB) IsX402NonceUsed(nonceKey [32]byte) (bool, error) {
	k := prefixX402Nonce + string(nonceKey[:])

	s.mu.RLock()
	if _, ok := s.pendingRaw[k]; ok {
		s.mu.RUnlock()
		return true, nil
	}
	s.mu.RUnlock()

	_, err := s.db.Get([]byte(k), nil)
	if err == leveldb.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Commit writes all pending changes (accounts, code, storage/nonce overlay) to
// LevelDB in a single atomic batch and returns the new Merkle state root. See
// commit() for the durability contract.
func (s *StateDB) Commit() ([32]byte, error) {
	return s.commit(false, 0)
}

// CommitWithHeight behaves like Commit but ALSO records, in the SAME atomic
// batch, the block height this state now reflects (under the meta: namespace,
// which is excluded from the state root). On boot the node compares this marker
// against the chain head and replays any block the head advanced past before the
// state commit landed — so a crash between persisting the block and persisting
// the state can no longer wedge the chain.
func (s *StateDB) CommitWithHeight(height uint64) ([32]byte, error) {
	return s.commit(true, height)
}

// commit stages every pending write into one LevelDB batch and, only AFTER the
// durable write succeeds, advances the in-memory leaf index and clears the
// pending layer. Mutating the index only on success means a failed write can
// never leave the in-memory root ahead of what is on disk.
func (s *StateDB) commit(setHeight bool, height uint64) ([32]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sandbox {
		return [32]byte{}, ErrSandboxCommit
	}
	if s.failNextCommit != nil {
		err := s.failNextCommit
		s.failNextCommit = nil
		return [32]byte{}, err
	}

	batch := new(leveldb.Batch)

	type leafUpd struct {
		key  string
		hash [32]byte
	}
	updates := make([]leafUpd, 0, len(s.dirty)+len(s.codeCache)+len(s.pendingRaw))

	// The root this commit will produce, if PendingStateRoot computed it over
	// exactly this pending layer. Decided before anything moves.
	root, haveRoot := s.cachedRootLocked()

	// What this commit does to the committed native total, taken BEFORE the
	// write while the committed records still hold the old balances. Only
	// tracked once the total is known; before that a scan answers.
	var sumDelta *big.Int
	if s.nativeSum != nil {
		sumDelta = new(big.Int)
	}

	// Dirty accounts.
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		key := prefixAccount + addr.String()
		batch.Put([]byte(key), data)
		updates = append(updates, leafUpd{key, qcrypto.Hash32(data)})
		if sumDelta != nil {
			was, err := s.committedBalance(addr)
			if err != nil {
				return [32]byte{}, err
			}
			sumDelta.Sub(sumDelta, was)
			if acc != nil && acc.Balance != nil {
				sumDelta.Add(sumDelta, acc.Balance)
			}
		}
	}

	// Contract code.
	for hash, code := range s.codeCache {
		key := prefixCode + string(hash[:])
		batch.Put([]byte(key), code)
		updates = append(updates, leafUpd{key, qcrypto.Hash32(code)})
	}

	// Raw storage + X402 nonce overlay.
	for key, val := range s.pendingRaw {
		batch.Put([]byte(key), val)
		updates = append(updates, leafUpd{key, qcrypto.Hash32(val)})
	}

	// Height marker — meta: namespace, NOT a state leaf.
	if setHeight {
		hb, err := json.Marshal(height)
		if err != nil {
			return [32]byte{}, fmt.Errorf("marshal height marker: %w", err)
		}
		batch.Put([]byte(keyStateHeight), hb)
	}

	if err := persist.Write(s.db, batch); err != nil {
		return [32]byte{}, fmt.Errorf("state commit failed: %w", err)
	}

	// Durable — now advance the in-memory index and clear the pending layer.
	for _, u := range updates {
		s.leaves[u.key] = u.hash
	}
	if sumDelta != nil {
		s.nativeSum.Add(s.nativeSum, sumDelta)
	}
	if setHeight {
		s.appliedHeight = height
		s.heightKnown = true
	}
	s.dirty = make(map[qcrypto.Address]*types.Account)
	s.codeCache = make(map[[32]byte][]byte)
	s.pendingRaw = make(map[string][]byte)
	s.journal = nil
	s.snapshots = make(map[uint64]int)
	s.touchLocked()

	if !haveRoot {
		root = merkleRoot(s.leaves)
	}
	return root, nil
}

// AppliedHeight returns the block height the committed state reflects, and
// ok=false if no height marker has been written yet (e.g. a fresh DB or a node
// whose genesis state was applied before height markers existed).
func (s *StateDB) AppliedHeight() (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appliedHeight, s.heightKnown
}

// FailNextCommit makes the next Commit/CommitWithHeight return err without
// writing. The injection is consumed once. Tests use it to reproduce a
// block-stored / state-not-committed split.
func (s *StateDB) FailNextCommit(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextCommit = err
}

// HasPendingWrites reports whether the in-memory overlay still holds uncommitted writes.
func (s *StateDB) HasPendingWrites() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.dirty) > 0 || len(s.codeCache) > 0 || len(s.pendingRaw) > 0
}

// leafDigest hashes one state leaf with a length-prefixed, domain-separated
// preimage so distinct (key,value) pairs can never collide by concatenation
// ambiguity (the previous flat concat of key+value was ambiguous).
func leafDigest(key string, valueHash [32]byte) [32]byte {
	buf := make([]byte, 0, 1+8+len(key)+32)
	buf = append(buf, 0x00) // leaf domain tag
	var lb [8]byte
	binary.BigEndian.PutUint64(lb[:], uint64(len(key)))
	buf = append(buf, lb[:]...)
	buf = append(buf, key...)
	buf = append(buf, valueHash[:]...)
	return qcrypto.Hash32(buf)
}

// internalHash hashes two child digests into their parent, domain-separated from
// leaves so an internal node can never be forged to equal a leaf.
func internalHash(l, r [32]byte) [32]byte {
	var buf [1 + 32 + 32]byte
	buf[0] = 0x01 // internal domain tag
	copy(buf[1:], l[:])
	copy(buf[33:], r[:])
	return qcrypto.Hash32(buf[:])
}

// merkleRoot builds a deterministic binary Merkle root over the leaf set (keyed
// by state key, valued by the value hash). Keys are sorted; an odd node at any
// level is carried up unchanged (never duplicated, which would be malleable).
func merkleRoot(leaves map[string][32]byte) [32]byte {
	if len(leaves) == 0 {
		return stateEmptyRoot
	}
	keys := make([]string, 0, len(leaves))
	for k := range leaves {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	level := make([][32]byte, len(keys))
	for i, k := range keys {
		level[i] = leafDigest(k, leaves[k])
	}
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i]) // carry odd node up unchanged
				break
			}
			next = append(next, internalHash(level[i], level[i+1]))
		}
		level = next
	}
	return level[0]
}

// StateRoot returns the current COMMITTED state root (pending writes excluded).
func (s *StateDB) StateRoot() ([32]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sandbox {
		return [32]byte{}, ErrSandboxCommit
	}
	return merkleRoot(s.leaves), nil
}

// MerkleProof returns the Merkle inclusion proof for an account's leaf in the
// committed state tree. The proof is the list of sibling hashes from the leaf
// level up to (but not including) the root.
//
// A verifier reconstructs the root:
//  1. Compute leafHash = leafDigest("acc:SQ:...", SHA3-256(account_bytes))
//  2. For each sibling in order: node = internalHash(node, sibling) if node was
//     the left child, or internalHash(sibling, node) if it was the right child.
//     The caller can determine left/right from the original leaf index parity.
//  3. The final node must equal the committed state root.
//
// Returns (leafHash, siblings, nil) when found, or (zero, nil, error) when the
// account is not in committed state. Odd-count levels carry the unpaired node
// up without a sibling (len(siblings) may be < tree depth).
func (s *StateDB) MerkleProof(addr qcrypto.Address) ([32]byte, [][32]byte, error) {
	return s.MerkleProofContext(context.Background(), addr)
}

// MerkleProofContext is MerkleProof that stops when ctx is cancelled, so a
// guarded /state/proof cannot hold an execution slot for a full tree walk
// after the caller has gone away.
func (s *StateDB) MerkleProofContext(ctx context.Context, addr qcrypto.Address) ([32]byte, [][32]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return [32]byte{}, nil, err
	}

	targetKey := prefixAccount + addr.String()
	if _, ok := s.leaves[targetKey]; !ok {
		return [32]byte{}, nil, fmt.Errorf("account %s not in committed state", addr)
	}

	// Build sorted key list — same iteration order as merkleRoot.
	keys := make([]string, 0, len(s.leaves))
	for k := range s.leaves {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Compute leaf digests and find the index of the target.
	level := make([][32]byte, len(keys))
	targetIdx := -1
	for i, k := range keys {
		if i&63 == 0 {
			if err := ctx.Err(); err != nil {
				return [32]byte{}, nil, err
			}
		}
		level[i] = leafDigest(k, s.leaves[k])
		if k == targetKey {
			targetIdx = i
		}
	}
	if targetIdx < 0 {
		return [32]byte{}, nil, fmt.Errorf("account %s vanished from leaves during proof generation", addr)
	}

	leafHash := level[targetIdx]
	var siblings [][32]byte
	idx := targetIdx

	// Walk up the binary tree, collecting one sibling per level.
	// Odd nodes at any level are carried up unchanged and have no sibling.
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				// Odd node: carried up unchanged — matches merkleRoot behaviour.
				next = append(next, level[i])
				break
			}
			left, right := level[i], level[i+1]
			next = append(next, internalHash(left, right))
			// Collect the sibling for our proof path.
			if idx == i {
				siblings = append(siblings, right)
			} else if idx == i+1 {
				siblings = append(siblings, left)
			}
		}
		idx /= 2
		level = next
	}

	return leafHash, siblings, nil
}

// PendingStateRoot computes the state root that Commit() WOULD produce, without
// persisting anything. It overlays the pending layer (dirty accounts, code cache,
// raw storage/nonce writes) onto a copy of the committed leaf index and hashes it
// with the identical Merkle construction Commit uses — so the two roots are equal
// by construction whenever the same writes are pending.
//
// This lets a caller verify a block's declared state root BEFORE durably
// committing: on a mismatch it can DiscardPending() and leave nothing on disk.
func (s *StateDB) PendingStateRoot() ([32]byte, error) {
	root, gen, err := s.pendingStateRoot()
	if err != nil {
		return [32]byte{}, err
	}
	// Remember it for the commit that follows, unless something changed
	// while the lock was released.
	s.mu.Lock()
	if s.gen == gen {
		s.pendingRoot, s.pendingRootGen = root, gen
	}
	s.mu.Unlock()
	return root, nil
}

// pendingStateRoot computes the pending root under the read lock and reports
// the generation it was computed at.
func (s *StateDB) pendingStateRoot() ([32]byte, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sandbox {
		return [32]byte{}, 0, ErrSandboxCommit
	}
	gen := s.gen

	eff := make(map[string][32]byte, len(s.leaves)+len(s.dirty)+len(s.codeCache)+len(s.pendingRaw))
	for k, v := range s.leaves {
		eff[k] = v
	}
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, 0, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		eff[prefixAccount+addr.String()] = qcrypto.Hash32(data)
	}
	for hash, code := range s.codeCache {
		eff[prefixCode+string(hash[:])] = qcrypto.Hash32(code)
	}
	for k, v := range s.pendingRaw {
		eff[k] = qcrypto.Hash32(v)
	}
	return merkleRoot(eff), gen, nil
}

// DiscardPending drops all uncommitted writes (accounts, code, storage/nonce)
// without persisting them. Used to abandon a block whose computed state root does
// not match its header, so a rejected block leaves committed state untouched. The
// committed leaf index is not touched (it only advances on Commit).
func (s *StateDB) DiscardPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = make(map[qcrypto.Address]*types.Account)
	s.codeCache = make(map[[32]byte][]byte)
	s.pendingRaw = make(map[string][]byte)
	s.journal = nil
	s.snapshots = make(map[uint64]int)
	s.touchLocked()
}
