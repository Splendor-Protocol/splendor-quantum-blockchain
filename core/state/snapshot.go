// Package state — StateDB snapshot and revert support.
//
// A snapshot captures the set of dirty (uncommitted) accounts at a point in time.
// If execution needs to be rolled back (e.g. a sub-call reverts), the snapshot
// is restored. Only dirty state is tracked — committed state in LevelDB is not
// affected until Commit() is called.
//
// Usage:
//   snap := stateDB.Snapshot()
//   // ... make changes ...
//   if err != nil {
//       stateDB.RevertToSnapshot(snap)  // undo all changes since snap
//   } else {
//       stateDB.CommitSnapshot(snap)    // keep changes, discard snap
//   }
package state

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// StateSnapshot captures the dirty (pending) state at a moment in time.
// It stores deep copies of all pending writes so revert is cheap and correct.
type StateSnapshot struct {
	// id is a monotonically increasing snapshot counter.
	id uint64

	// dirtyAccounts is a copy of stateDB.dirty at snapshot time.
	dirtyAccounts map[qcrypto.Address]*types.Account

	// dirtyCode is a copy of stateDB.codeCache at snapshot time.
	dirtyCode map[[32]byte][]byte

	// pendingRaw is a copy of stateDB.pendingRaw (contract storage + X402 nonce
	// overlay) at snapshot time. Capturing it is what lets a failed tx's storage
	// writes be rolled back — previously they went straight to LevelDB and
	// survived a revert, silently forking a follower on contract storage.
	pendingRaw map[string][]byte
}

// snapCounter is a process-global snapshot ID counter.
var snapCounter uint64

// Snapshot creates a new snapshot of the current dirty state.
// Returns a snapshot ID that can be used with RevertToSnapshot or CommitSnapshot.
func (s *StateDB) Snapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapCounter++
	id := snapCounter

	// Deep-copy dirty accounts
	accounts := make(map[qcrypto.Address]*types.Account, len(s.dirty))
	for addr, acc := range s.dirty {
		accounts[addr] = acc.Copy()
	}

	// Deep-copy code cache
	code := make(map[[32]byte][]byte, len(s.codeCache))
	for h, c := range s.codeCache {
		cp := make([]byte, len(c))
		copy(cp, c)
		code[h] = cp
	}

	// Deep-copy the raw storage/nonce overlay
	raw := make(map[string][]byte, len(s.pendingRaw))
	for k, v := range s.pendingRaw {
		cp := make([]byte, len(v))
		copy(cp, v)
		raw[k] = cp
	}

	// Store snapshot in the registry
	s.snapshots[id] = &StateSnapshot{
		id:            id,
		dirtyAccounts: accounts,
		dirtyCode:     code,
		pendingRaw:    raw,
	}

	return id
}

// RevertToSnapshot rolls back all dirty state changes made after snapshot `id`.
// The snapshot is consumed — you cannot revert to the same snapshot twice.
func (s *StateDB) RevertToSnapshot(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, ok := s.snapshots[id]
	if !ok {
		return // snapshot not found, nothing to revert
	}

	// Restore dirty state from snapshot
	s.dirty = snap.dirtyAccounts
	s.codeCache = snap.dirtyCode
	s.pendingRaw = snap.pendingRaw

	// Clean up this snapshot and all newer ones
	for snapID := range s.snapshots {
		if snapID >= id {
			delete(s.snapshots, snapID)
		}
	}
}

// CommitSnapshot discards a snapshot (keeps changes, removes the checkpoint).
// Call this when the operation that took the snapshot succeeded.
func (s *StateDB) CommitSnapshot(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
}

// SnapshotCount returns the number of active snapshots.
func (s *StateDB) SnapshotCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.snapshots)
}

// copyBigInt safely copies a *big.Int, returning zero for nil.
func copyBigInt(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(v)
}
