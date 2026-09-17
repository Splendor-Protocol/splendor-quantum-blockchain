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
	"github.com/syndtr/goleveldb/leveldb/util"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// MaxChunkLeaves and MaxChunkBytes are hard bounds on a single chunk of a
// chunked snapshot export. They are enforced here, at the scan boundary, on
// every caller: a caller-supplied maxLeaves above MaxChunkLeaves is clamped,
// and the scan stops once either the leaf count or the raw byte total is
// reached. This is what keeps a hostile or buggy "small" page request from
// making the serving node collect, allocate and serialize an unbounded
// fraction of its state in one call.
const (
	// MaxChunkLeaves caps one chunk at this many leaves. The executor's
	// exported MaxSnapshotChunkLeaves mirrors it; the scan itself is the
	// authoritative enforcement point because it is what actually allocates.
	MaxChunkLeaves = 5000

	// MaxChunkBytes caps the raw key+value bytes a single chunk may carry.
	// Chosen well under the chunked reply cap (16 MiB) so base64 and
	// envelope overhead cannot push a chunk over the wire limit. It also
	// bounds the pathological case where fewer than MaxChunkLeaves leaves
	// have individually huge values.
	MaxChunkBytes = 4 << 20
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

// ExportSnapshotChunk returns up to maxLeaves leaves whose keys sort strictly
// after afterKey (nil fetches the first chunk), read from one consistent
// LevelDB snapshot, together with the height that snapshot reflects and the
// key to resume from next.
//
// Unlike ExportSnapshot this never holds the whole state in memory or on the
// wire at once — a chunk is bounded by maxLeaves regardless of total state
// size, so serving (and later, importing) a multi-GB state does not require a
// single multi-GB buffer on either side.
//
// Each call takes its OWN LevelDB snapshot rather than pinning one across the
// whole multi-chunk transfer, so a chunk can never block a live validator's
// writes for the transfer's duration. The tradeoff is explicit and safe: if
// the state actually changes between two chunk calls (a validator commits a
// new block mid-transfer), the assembled leaf set will not hash to the head
// block's declared state_root, and the importer's existing root check refuses
// it outright — the caller sees a clear failure and retries, never a silently
// inconsistent import. height is returned on every chunk so a caller can also
// abort early the moment it changes, rather than only failing at the root
// check after the whole transfer completes.
func (s *StateDB) ExportSnapshotChunk(afterKey []byte, maxLeaves int) (height uint64, leaves []types.StateLeaf, nextKey []byte, done bool, err error) {
	if s.sandbox {
		return 0, nil, nil, false, ErrSandboxCommit
	}
	if maxLeaves <= 0 {
		return 0, nil, nil, false, errors.New("state: chunk size must be positive")
	}
	// A caller asking for more than one chunk's worth gets one chunk's worth.
	// The cap is enforced here — not just by the executor's public constant —
	// because this is the code that actually allocates; trusting a
	// caller-supplied value would let one small request scan and serialize an
	// unbounded fraction of state.
	if maxLeaves > MaxChunkLeaves {
		maxLeaves = MaxChunkLeaves
	}
	snap, err := s.db.GetSnapshot()
	if err != nil {
		return 0, nil, nil, false, fmt.Errorf("state: snapshot: %w", err)
	}
	defer snap.Release()

	raw, err := snap.Get([]byte(keyStateHeight), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return 0, nil, nil, false, ErrSnapshotUnavailable
	}
	if err != nil {
		return 0, nil, nil, false, fmt.Errorf("state: snapshot height: %w", err)
	}
	if err := json.Unmarshal(raw, &height); err != nil {
		return 0, nil, nil, false, fmt.Errorf("state: snapshot height marker is corrupt: %w", err)
	}

	var iterRange *util.Range
	if len(afterKey) > 0 {
		iterRange = &util.Range{Start: append(append([]byte(nil), afterKey...), 0x00)}
	}
	iter := snap.NewIterator(iterRange, nil)
	defer iter.Release()

	var (
		size    int
		lastKey []byte
	)
	for iter.Next() {
		k := iter.Key()
		if bytes.HasPrefix(k, []byte(prefixMeta)) {
			continue
		}
		v := iter.Value()
		leafBytes := len(k) + len(v)

		// A single leaf larger than the whole budget cannot be paginated; there
		// is no fragmentation protocol, so refuse explicitly rather than hand
		// back a page that exceeds the cap the caller was promised.
		if len(leaves) == 0 && leafBytes > MaxChunkBytes {
			return 0, nil, nil, false, fmt.Errorf(
				"state: snapshot leaf of %d bytes exceeds the %d-byte chunk budget; leaf fragmentation is not implemented",
				leafBytes, MaxChunkBytes)
		}
		// The byte budget is a hard maximum, not a stopping threshold: once the
		// page has content, a leaf that would push it over is NOT copied into
		// this page. The resume cursor is the last key actually included, so the
		// over-budget leaf is fetched again on the next page with no loss.
		if len(leaves) > 0 && size+leafBytes > MaxChunkBytes {
			nextKey = append([]byte(nil), lastKey...)
			return height, leaves, nextKey, false, nil
		}

		leaves = append(leaves, types.StateLeaf{
			Key:   append([]byte(nil), k...),
			Value: append([]byte(nil), v...),
		})
		size += leafBytes
		lastKey = append([]byte(nil), k...)

		if len(leaves) >= maxLeaves {
			// Peek: is there anything left after this key?
			nextKey = append([]byte(nil), k...)
			if !iter.Next() {
				if err := iter.Error(); err != nil {
					return 0, nil, nil, false, fmt.Errorf("state: snapshot chunk scan: %w", err)
				}
				return height, leaves, nil, true, nil
			}
			return height, leaves, nextKey, false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return 0, nil, nil, false, fmt.Errorf("state: snapshot chunk scan: %w", err)
	}
	return height, leaves, nil, true, nil
}

// IsFresh reports whether the state holds no committed leaves, no applied-
// height marker and no pending writes — the only state ImportSnapshot may
// replace. The executor asks before it stores the snapshot's head block, so a
// refusal never leaves a head with no state behind it.
func (s *StateDB) IsFresh() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.freshLocked()
}

func (s *StateDB) freshLocked() bool {
	return len(s.leaves) == 0 && !s.heightKnown &&
		len(s.dirty) == 0 && len(s.codeCache) == 0 && len(s.pendingRaw) == 0
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
	if !s.freshLocked() {
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
	s.nativeSum = nil // the committed total is whatever the snapshot holds; scan it when asked
	s.touchLocked()
	s.appliedHeight = height
	s.heightKnown = true
	return nil
}
