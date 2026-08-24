// Package state manages the Splendor Quantum Chain account state.
// Accounts are stored in LevelDB. The state root is a SHA3-256 hash of
// the sorted set of all account states — a simple but correct Merkle commitment.
// A full Sparse Merkle Tree is planned for Phase 2.
package state

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	// Key prefixes for LevelDB namespacing
	prefixAccount  = "acc:"
	prefixCode     = "code:"
	prefixStorage  = "store:"
	prefixX402Nonce = "x402nonce:"
)

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

	// Active snapshots for rollback support (snapshot ID → snapshot data)
	snapshots map[uint64]*StateSnapshot
}

// NewStateDB opens (or creates) the state database at the given path.
func NewStateDB(path string) (*StateDB, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open state database at %s: %w", path, err)
	}
	return &StateDB{
		db:        db,
		dirty:     make(map[qcrypto.Address]*types.Account),
		codeCache: make(map[[32]byte][]byte),
		snapshots: make(map[uint64]*StateSnapshot),
	}, nil
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

// SetStorageValue stores a contract storage slot value.
// key: SHA3-256(contract_address + slot_key)
func (s *StateDB) SetStorageValue(contract qcrypto.Address, slot [32]byte, value []byte) error {
	k := append([]byte(prefixStorage+contract.String()+":"), slot[:]...)
	return s.db.Put(k, value, nil)
}

// GetStorageValue retrieves a contract storage slot value.
func (s *StateDB) GetStorageValue(contract qcrypto.Address, slot [32]byte) ([]byte, error) {
	k := append([]byte(prefixStorage+contract.String()+":"), slot[:]...)
	data, err := s.db.Get(k, nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	return data, err
}

// MarkX402NonceUsed marks an X402 payment nonce as used.
// key = SHA3-256(from_address + nonce_bytes)
func (s *StateDB) MarkX402NonceUsed(nonceKey [32]byte, blockHeight uint64) error {
	k := append([]byte(prefixX402Nonce), nonceKey[:]...)
	val, err := json.Marshal(blockHeight)
	if err != nil {
		return err
	}
	return s.db.Put(k, val, nil)
}

// IsX402NonceUsed returns true if the X402 payment nonce has already been used.
func (s *StateDB) IsX402NonceUsed(nonceKey [32]byte) (bool, error) {
	k := append([]byte(prefixX402Nonce), nonceKey[:]...)
	_, err := s.db.Get(k, nil)
	if err == leveldb.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Commit writes all pending (dirty) changes to LevelDB atomically.
// It also stores contract code from the cache.
// Returns the new state root after the commit.
func (s *StateDB) Commit() ([32]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch := new(leveldb.Batch)

	// Write dirty accounts
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		key := []byte(prefixAccount + addr.String())
		batch.Put(key, data)
	}

	// Write code cache
	for hash, code := range s.codeCache {
		key := append([]byte(prefixCode), hash[:]...)
		batch.Put(key, code)
	}

	if err := s.db.Write(batch, nil); err != nil {
		return [32]byte{}, fmt.Errorf("state commit failed: %w", err)
	}

	// Clear dirty caches
	s.dirty = make(map[qcrypto.Address]*types.Account)
	s.codeCache = make(map[[32]byte][]byte)

	// Compute and return state root
	return s.computeStateRoot()
}

// computeStateRoot computes a SHA3-256 commitment over all account states.
// It reads all accounts from LevelDB, sorts by address, and hashes them.
func (s *StateDB) computeStateRoot() ([32]byte, error) {
	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixAccount)), nil)
	defer iter.Release()

	type entry struct {
		key string
		val []byte
	}
	var entries []entry

	for iter.Next() {
		k := string(iter.Key())
		v := make([]byte, len(iter.Value()))
		copy(v, iter.Value())
		entries = append(entries, entry{key: k, val: v})
	}
	if err := iter.Error(); err != nil {
		return [32]byte{}, fmt.Errorf("state root iteration error: %w", err)
	}

	// Sort by key for determinism
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})

	// Hash all entries together: SHA3-256(key1+val1+key2+val2+...)
	var combined []byte
	for _, e := range entries {
		combined = append(combined, []byte(e.key)...)
		combined = append(combined, e.val...)
	}
	return qcrypto.Hash32(combined), nil
}

// StateRoot returns the current state root without committing.
func (s *StateDB) StateRoot() ([32]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.computeStateRoot()
}

// PendingStateRoot computes the state root that Commit() WOULD produce, without
// persisting anything to disk. It overlays the in-memory dirty accounts on top of
// the committed account set and hashes them exactly as computeStateRoot does
// (same key ordering, same per-account JSON encoding Commit writes).
//
// This lets a caller verify a block's declared state root BEFORE durably
// committing: on a mismatch it can DiscardPending() and leave no residue on disk.
// The previous flow committed first and compared after, so a rejected block's
// (corrupt) account writes were already persisted and irreversible.
func (s *StateDB) PendingStateRoot() ([32]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make(map[string][]byte)

	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixAccount)), nil)
	for iter.Next() {
		k := string(iter.Key())
		v := make([]byte, len(iter.Value()))
		copy(v, iter.Value())
		entries[k] = v
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return [32]byte{}, fmt.Errorf("pending state root iteration error: %w", err)
	}

	// Overlay dirty accounts using the identical marshaling Commit() applies, so
	// the resulting bytes match what would land in LevelDB.
	for addr, acc := range s.dirty {
		data, err := json.Marshal(acc)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to marshal account %s: %w", addr, err)
		}
		entries[prefixAccount+addr.String()] = data
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var combined []byte
	for _, k := range keys {
		combined = append(combined, []byte(k)...)
		combined = append(combined, entries[k]...)
	}
	return qcrypto.Hash32(combined), nil
}

// DiscardPending drops all uncommitted (dirty) account and code writes without
// persisting them. Used to abandon a block whose computed state root does not
// match its header, so a rejected block leaves the committed state untouched.
//
// NOTE: contract storage slots (SetStorageValue) and X402 nonces are written
// straight to LevelDB during execution and are NOT covered by this rollback.
// Callers therefore run the block-linkage and validator-signature checks BEFORE
// executing transactions, so a block that would fail the state-root comparison
// only ever originates from the trusted validator itself.
func (s *StateDB) DiscardPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = make(map[qcrypto.Address]*types.Account)
	s.codeCache = make(map[[32]byte][]byte)
}
