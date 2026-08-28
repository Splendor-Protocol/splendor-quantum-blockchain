// Package state — StateDB snapshot and revert support (journaled undo-log).
//
// A snapshot records a MARK: the current length of the pending-mutation journal.
// Reverting to a snapshot replays the journal's tail undos (newest first) back to
// that mark, restoring the exact pre-snapshot pending state, then truncates the
// journal. This makes Snapshot() O(1) and RevertToSnapshot() O(mutations-since),
// versus the previous scheme that deep-copied the entire dirty account set on
// every snapshot — which was O(N^2) across a block and throttled throughput far
// below the signature-verification ceiling.
//
// Only the in-memory pending layer (dirty accounts, code cache, raw storage/nonce
// overlay) is journaled; committed LevelDB state is untouched until Commit().
//
// Usage:
//
//	snap := stateDB.Snapshot()
//	// ... make changes ...
//	if err != nil {
//	    stateDB.RevertToSnapshot(snap)  // undo all changes since snap
//	} else {
//	    stateDB.CommitSnapshot(snap)    // keep changes, discard the checkpoint
//	}
package state

import (
	"math/big"
)

// snapCounter is a process-global snapshot ID counter.
var snapCounter uint64

// Snapshot records the current journal position and returns a snapshot ID.
func (s *StateDB) Snapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapCounter++
	id := snapCounter
	s.snapshots[id] = len(s.journal)
	return id
}

// RevertToSnapshot rolls back every pending mutation made after snapshot `id` by
// replaying the journal's undo functions from the end back to the recorded mark.
// The snapshot is consumed; snapshots taken after it are invalidated too.
func (s *StateDB) RevertToSnapshot(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mark, ok := s.snapshots[id]
	if !ok {
		return // unknown snapshot, nothing to revert
	}
	// Undo newest-first down to the mark. LIFO order is essential: a key mutated
	// several times must be restored through each prior value in reverse.
	for i := len(s.journal) - 1; i >= mark; i-- {
		s.journal[i]()
	}
	s.journal = s.journal[:mark]

	// Drop this checkpoint and any newer ones (their marks are now past the tail).
	for snapID, m := range s.snapshots {
		if snapID >= id || m > mark {
			delete(s.snapshots, snapID)
		}
	}
}

// CommitSnapshot discards a snapshot checkpoint, keeping its changes. The journal
// is retained so an OUTER snapshot can still revert past this point.
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
