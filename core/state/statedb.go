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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	// Key prefixes for LevelDB namespacing
	prefixAccount   = "acc:"
	prefixCode      = "code:"
	prefixStorage   = "store:"
	prefixX402Nonce = "x402nonce:"
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

	// Active snapshots for rollback support (snapshot ID → snapshot data)
	snapshots map[uint64]*StateSnapshot
}

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
		snapshots:  make(map[uint64]*StateSnapshot),
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
		s.leaves[string(iter.Key())] = qcrypto.Hash32(iter.Value())
	}
	return iter.Error()
}

// Close closes the underlying LevelDB.
func (s *StateDB) Close() error {
	return s.db.Close()
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
	s.dirty[acc.Address] = acc.Copy()
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

// GetPublicKey returns the registered Dilithium public key for an address.
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
func (s *StateDB) Transfer(from, to qcrypto.Address, amount *big.Int) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("negative transfer amount")
	}
	if amount.Sign() == 0 {
		return nil
	}

	fromAcc, err := s.GetAccount(from)
	if err != nil {
		return err
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
	s.codeCache[codeHash] = code
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
	s.pendingRaw[k] = cp
	return nil
}

// GetStorageValue retrieves a contract storage slot value (pending overlay first).
func (s *StateDB) GetStorageValue(contract qcrypto.Address, slot [32]byte) ([]byte, error) {
	k := storageKey(contract, slot)

	s.mu.RLock()
	if v, ok := s.pendingRaw[k]; ok {
		cp := make([]byte, len(v))
		copy(cp, v)
		s.mu.RUnlock()
		return cp, nil
	}
	s.mu.RUnlock()

	data, err := s.db.Get([]byte(k), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	return data, err
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
	s.pendingRaw[k] = val
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
// LevelDB in a single atomic batch, folds each into the in-memory leaf index,
// clears the pending layer, and returns the new Merkle state root.
func (s *StateDB) Commit() ([32]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch := new(leveldb.Batch)

	// Dirty accounts.
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		key := prefixAccount + addr.String()
		batch.Put([]byte(key), data)
		s.leaves[key] = qcrypto.Hash32(data)
	}

	// Contract code.
	for hash, code := range s.codeCache {
		key := prefixCode + string(hash[:])
		batch.Put([]byte(key), code)
		s.leaves[key] = qcrypto.Hash32(code)
	}

	// Raw storage + X402 nonce overlay.
	for key, val := range s.pendingRaw {
		batch.Put([]byte(key), val)
		s.leaves[key] = qcrypto.Hash32(val)
	}

	if err := s.db.Write(batch, nil); err != nil {
		return [32]byte{}, fmt.Errorf("state commit failed: %w", err)
	}

	// Clear the pending layer.
	s.dirty = make(map[qcrypto.Address]*types.Account)
	s.codeCache = make(map[[32]byte][]byte)
	s.pendingRaw = make(map[string][]byte)

	return merkleRoot(s.leaves), nil
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
	return merkleRoot(s.leaves), nil
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	eff := make(map[string][32]byte, len(s.leaves)+len(s.dirty)+len(s.codeCache)+len(s.pendingRaw))
	for k, v := range s.leaves {
		eff[k] = v
	}
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		eff[prefixAccount+addr.String()] = qcrypto.Hash32(data)
	}
	for hash, code := range s.codeCache {
		eff[prefixCode+string(hash[:])] = qcrypto.Hash32(code)
	}
	for k, v := range s.pendingRaw {
		eff[k] = qcrypto.Hash32(v)
	}
	return merkleRoot(eff), nil
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
}
