// Package state — live state snapshots for joining a chain whose history
// cannot be replayed.
//
// A file copy of an open LevelDB is not a snapshot: compaction rewrites and
// deletes SSTs underneath it and the journal may be half-written, so the copy
// may not open, or opens on a root that matches no header. goleveldb's
// DB.GetSnapshot() gives exactly the consistent point-in-time view a copy
// cannot, and CommitWithHeight writes every leaf together with the height
// marker in one batch, so a snapshot always sees a whole block's state with
// the height it belongs to.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

var (
	// ErrSnapshotUnavailable means the state carries no applied-height marker,
	// so nothing ties it to a block and it cannot be served.
	ErrSnapshotUnavailable = errors.New("state: no applied-height marker, cannot serve a snapshot")
	// ErrSnapshotTooLarge means the state exceeds the caller's byte cap.
	ErrSnapshotTooLarge = errors.New("state: snapshot exceeds the size cap")
	// ErrStateNotFresh means ImportSnapshot was asked to replace a state that
	// already holds leaves, a height marker, or pending writes.
	ErrStateNotFresh = errors.New("state: snapshot import needs an empty, unmarked state with nothing pending")
	// ErrSnapshotRootMismatch means the leaves do not hash to the root the
	// caller demanded; nothing was written.
	ErrSnapshotRootMismatch = errors.New("state: snapshot leaves do not hash to the expected root")
)

// ExportSnapshot returns every committed state leaf and the height the state
// reflects, read from one consistent LevelDB snapshot. meta: keys are
// node-local bookkeeping and are not exported; the importer takes the fork
// schedule and minted supply from its own pinned genesis document. Past
// maxBytes of keys plus values it stops with ErrSnapshotTooLarge.
func (s *StateDB) ExportSnapshot(maxBytes int) (uint64, []types.StateLeaf, error) {
	if s.sandbox {
		return 0, nil, ErrSandboxCommit
	}
	snap, err := s.db.GetSnapshot()
	if err != nil {
		return 0, nil, fmt.Errorf("state: snapshot: %w", err)
	}
	defer snap.Release()

	raw, err := snap.Get([]byte(keyStateHeight), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return 0, nil, ErrSnapshotUnavailable
	}
	if err != nil {
		return 0, nil, fmt.Errorf("state: snapshot height: %w", err)
	}
	var height uint64
	if err := json.Unmarshal(raw, &height); err != nil {
		return 0, nil, fmt.Errorf("state: snapshot height marker is corrupt: %w", err)
	}

	iter := snap.NewIterator(nil, nil)
	defer iter.Release()
	var leaves []types.StateLeaf
	size := 0
	for iter.Next() {
		k := iter.Key()
		if bytes.HasPrefix(k, []byte(prefixMeta)) {
			continue
		}
		v := iter.Value()
		size += len(k) + len(v)
		if size > maxBytes {
			return 0, nil, fmt.Errorf("%w: more than %d bytes at height %d", ErrSnapshotTooLarge, maxBytes, height)
		}
		leaves = append(leaves, types.StateLeaf{
			Key:   append([]byte(nil), k...),
			Value: append([]byte(nil), v...),
		})
	}
	if err := iter.Error(); err != nil {
		return 0, nil, fmt.Errorf("state: snapshot scan: %w", err)
	}
	return height, leaves, nil
}

// SnapshotRoot is the state root a set of leaves commits to, built with the
// same construction Commit uses, so it equals the header state_root of the
// block the leaves reflect.
func SnapshotRoot(leaves []types.StateLeaf) [32]byte {
	index := make(map[string][32]byte, len(leaves))
	for _, l := range leaves {
		index[string(l.Key)] = qcrypto.Hash32(l.Value)
	}
	return merkleRoot(index)
}

// ImportSnapshot replaces an empty state with the given leaves at height, in
// one batch together with the height marker, but only if the leaves hash to
// wantRoot — the state_root of an already-authenticated block. On any refusal
// nothing is written.
func (s *StateDB) ImportSnapshot(leaves []types.StateLeaf, height uint64, wantRoot [32]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sandbox {
		return ErrSandboxCommit
	}
	if len(s.leaves) != 0 || s.heightKnown || len(s.dirty) != 0 || len(s.codeCache) != 0 || len(s.pendingRaw) != 0 {
		return ErrStateNotFresh
	}

	index := make(map[string][32]byte, len(leaves))
	batch := new(leveldb.Batch)
	for _, l := range leaves {
		if len(l.Key) == 0 {
			return errors.New("state: snapshot carries an empty key")
		}
		// meta: keys are this node's own bookkeeping (minted supply, fork
		// schedule, the height marker) and must never arrive from the wire.
		if bytes.HasPrefix(l.Key, []byte(prefixMeta)) {
			return fmt.Errorf("state: snapshot carries node-local key %q", l.Key)
		}
		if _, dup := index[string(l.Key)]; dup {
			return fmt.Errorf("state: snapshot carries key %q twice", l.Key)
		}
		index[string(l.Key)] = qcrypto.Hash32(l.Value)
		batch.Put(l.Key, l.Value)
	}
	if got := merkleRoot(index); got != wantRoot {
		return fmt.Errorf("%w: leaves hash to %x, header says %x", ErrSnapshotRootMismatch, got[:8], wantRoot[:8])
	}
	hb, err := json.Marshal(height)
	if err != nil {
		return fmt.Errorf("marshal height marker: %w", err)
	}
	batch.Put([]byte(keyStateHeight), hb)
	if err := s.db.Write(batch, nil); err != nil {
		return fmt.Errorf("state: snapshot import failed: %w", err)
	}

	s.leaves = index
	s.appliedHeight = height
	s.heightKnown = true
	return nil
}
