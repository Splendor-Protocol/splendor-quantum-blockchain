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
	"errors"
	"fmt"
	"math/big"
)

// ErrUnknownSnapshot is returned by RevertToSnapshot and CommitSnapshot for an
// id this StateDB is not holding: never issued, already consumed, or dropped
// by a revert to an older checkpoint. Every caller keeps strict LIFO
// discipline, so an unknown id is a bug — and one that must not pass
// silently. A no-op revert would leave a failed transaction's fee debit,
// nonce bump and module writes in the pending layer, and the block's state
// root would then cover a transaction the block does not contain.
var ErrUnknownSnapshot = errors.New("state: unknown snapshot id")

// Snapshot records the current journal position and returns a snapshot ID.
//
// The id comes from a counter owned by this StateDB, not a package global. Ids
// are only ever looked up in the issuing StateDB's own map, so there is no
// cross-StateDB uniqueness to maintain — and a shared counter was a hazard:
// Simulate() sandboxes have their own mutex, so a /call or /estimate running
// concurrently with block execution could increment the global between the
// producer's outer and inner Snapshot() calls, and the ordering that
// RevertToSnapshot relies on (inner id > outer id) no longer held.
func (s *StateDB) Snapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextSnap++
	id := s.nextSnap
	s.snapshots[id] = len(s.journal)
	return id
}

// RevertToSnapshot rolls back every pending mutation made after snapshot `id` by
// replaying the journal's undo functions from the end back to the recorded mark.
// The snapshot is consumed; snapshots taken after it are invalidated too. An
// unknown id is ErrUnknownSnapshot and nothing is changed.
func (s *StateDB) RevertToSnapshot(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mark, ok := s.snapshots[id]
	if !ok {
		return fmt.Errorf("%w: revert to %d", ErrUnknownSnapshot, id)
	}
	// Undo newest-first down to the mark. LIFO order is essential: a key mutated
	// several times must be restored through each prior value in reverse.
	for i := len(s.journal) - 1; i >= mark; i-- {
		s.journal[i]()
	}
	s.journal = s.journal[:mark]
	s.touchLocked()

	// Drop this checkpoint and any newer ones (their marks are now past the tail).
	for snapID, m := range s.snapshots {
		if snapID >= id || m > mark {
			delete(s.snapshots, snapID)
		}
	}
	return nil
}

// CommitSnapshot discards a snapshot checkpoint, keeping its changes. The journal
// is retained so an OUTER snapshot can still revert past this point. An unknown
// id is ErrUnknownSnapshot.
func (s *StateDB) CommitSnapshot(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[id]; !ok {
		return fmt.Errorf("%w: commit of %d", ErrUnknownSnapshot, id)
	}
	delete(s.snapshots, id)
	return nil
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
