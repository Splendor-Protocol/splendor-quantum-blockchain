package block

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// MaxStateSnapshotBytes caps a served snapshot (keys plus values). It is
// single-shot: a state past this is a follow-up (paging), not a bigger cap —
// the sealed reply carries it base64-encoded twice over and must stay under
// the sync body limit.
const MaxStateSnapshotBytes = 64 << 20

// ErrChainNotEmpty is returned by ImportStateSnapshot on a node that already
// has blocks: a snapshot is a starting point, never a replacement.
var ErrChainNotEmpty = errors.New("state snapshot import needs an empty chain")

// ErrSnapshotImportIncomplete means recovery could not finish and left
// marker/staging in place so a subsequent --state-sync can resume. Startup
// must not treat this as fatal when state-sync retry is configured.
var ErrSnapshotImportIncomplete = errors.New("snapshot import incomplete; retry state sync")

// snapClass is how a store lines up with the recovery marker.
type snapClass int

const (
	snapAbsent snapClass = iota
	snapMatch
	snapConflict
	snapMismatch
)

// snapshotImportMarker tracks an in-progress snapshot import across restarts.
// Written before any import operations, deleted only after both block and state
// are committed. A marker present at startup means import was interrupted and
// must be completed or rolled back.
type snapshotImportMarker struct {
	Height    uint64   `json:"height"`
	BlockHash [32]byte `json:"block_hash"`
	StateRoot [32]byte `json:"state_root"`
	ChainID   string   `json:"chain_id"`
	// Phase tracks how far import progressed before interruption
	// "started" = marker written, nothing stored yet
	// "block_stored" = block is in blockstore, state import pending
	// "complete" = both stored (marker should be deleted)
	Phase string `json:"phase"`
}

const (
	phaseStarted     = "started"
	phaseBlockStored = "block_stored"
	phaseComplete    = "complete"
)

// syncDir fsyncs a directory so a rename or unlink inside it is durable.
// Marker and staging writes/deletes call this because a crash between the
// rename/unlink and the directory flush would otherwise resurrect or lose the
// very file whose whole job is to survive a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// snapshotMarkerPath returns the path to the recovery marker file
func (e *Executor) snapshotMarkerPath() string {
	// Store in the data directory alongside chaindata
	if e.dataDir == "" {
		return ""
	}
	return filepath.Join(e.dataDir, "snapshot_import.marker")
}

// writeSnapshotMarker writes a recovery marker to disk with fsync
func (e *Executor) writeSnapshotMarker(marker *snapshotImportMarker) error {
	path := e.snapshotMarkerPath()
	if path == "" {
		return errors.New("no data directory configured for snapshot marker")
	}

	data, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal snapshot marker: %w", err)
	}

	// Write to temp file first, then atomic rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write snapshot marker: %w", err)
	}

	// Fsync the file before rename
	f, err := os.OpenFile(tmpPath, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open marker for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync marker: %w", err)
	}
	f.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename marker: %w", err)
	}

	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync marker directory: %w", err)
	}

	return nil
}

// readSnapshotMarker reads the recovery marker if it exists
func (e *Executor) readSnapshotMarker() (*snapshotImportMarker, error) {
	path := e.snapshotMarkerPath()
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil // No marker = no import in progress
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot marker: %w", err)
	}

	var marker snapshotImportMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot marker: %w", err)
	}

	return &marker, nil
}

// deleteSnapshotMarker removes the recovery marker after successful import
func (e *Executor) deleteSnapshotMarker() error {
	path := e.snapshotMarkerPath()
	if path == "" {
		return nil
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete snapshot marker: %w", err)
	}

	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync marker directory: %w", err)
	}

	return nil
}

// snapshotStagingPath returns the path to the retained snapshot staging file.
// The verified snapshot (block + leaves) is written here before the block is
// stored, so a crash after the block lands can complete the state import from
// this file instead of re-asking the peer.
func (e *Executor) snapshotStagingPath() string {
	if e.dataDir == "" {
		return ""
	}
	return filepath.Join(e.dataDir, "snapshot_import.staged")
}

// writeSnapshotStaging durably retains the verified snapshot. Same discipline
// as the marker: write a temp file, fsync it, atomic rename, fsync the dir.
func (e *Executor) writeSnapshotStaging(snap *types.StateSnapshot) error {
	path := e.snapshotStagingPath()
	if path == "" {
		return errors.New("no data directory configured for snapshot staging")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot staging: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write snapshot staging: %w", err)
	}
	f, err := os.OpenFile(tmpPath, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open snapshot staging for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync snapshot staging: %w", err)
	}
	f.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename snapshot staging: %w", err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync staging directory: %w", err)
	}
	return nil
}

// readSnapshotStaging loads the retained snapshot, or returns nil when there
// is no staging file (either never written or already cleaned up).
func (e *Executor) readSnapshotStaging() (*types.StateSnapshot, error) {
	path := e.snapshotStagingPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot staging: %w", err)
	}
	var snap types.StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot staging: %w", err)
	}
	return &snap, nil
}

// deleteSnapshotStaging removes the retained snapshot and fsyncs its
// directory. An absent file is not an error.
func (e *Executor) deleteSnapshotStaging() error {
	path := e.snapshotStagingPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete snapshot staging: %w", err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync staging directory: %w", err)
	}
	return nil
}

// ExportStateSnapshot returns this node's committed state at its applied
// height together with the block at that height.
//
// The block is always present: storeBlock advances the head before the state
// commit lands, so a state marked H always has block H stored. No lock is
// taken — the state comes from one LevelDB snapshot, which is what makes the
// leaves consistent, and the export must not stall block production for the
// length of a full state scan.
func (e *Executor) ExportStateSnapshot() (*types.StateSnapshot, error) {
	height, leaves, err := e.state.ExportSnapshot(MaxStateSnapshotBytes)
	if err != nil {
		return nil, err
	}
	b, err := e.GetBlockByHeight(height)
	if err != nil {
		return nil, fmt.Errorf("state snapshot at height %d has no stored block: %w", height, err)
	}
	return &types.StateSnapshot{Height: height, Block: b, Leaves: leaves}, nil
}

// MaxSnapshotChunkLeaves bounds one chunk of a chunked snapshot transfer.
// Chosen so a chunk of full-size leaves stays a small fraction of
// maxSyncBodyBytes-class limits even before base64 and envelope overhead —
// the whole point of chunking is that neither side ever needs a buffer
// anywhere near MaxStateSnapshotBytes.
const MaxSnapshotChunkLeaves = state.MaxChunkLeaves

// ExportStateSnapshotChunk returns one page of this node's committed state,
// the chunked counterpart of ExportStateSnapshot. The head block is attached
// only to the first chunk (afterKey == nil) — it is what the receiver
// authenticates the whole transfer against, and re-sending it on every page
// would be pure overhead. maxLeaves <= 0 requests a default page, and any
// value above MaxSnapshotChunkLeaves is clamped down — no caller can widen a
// single export beyond the budget the state layer enforces.
func (e *Executor) ExportStateSnapshotChunk(afterKey []byte, maxLeaves int) (*types.StateSnapshotChunkResult, error) {
	if maxLeaves <= 0 || maxLeaves > MaxSnapshotChunkLeaves {
		maxLeaves = MaxSnapshotChunkLeaves
	}
	height, leaves, nextKey, done, err := e.state.ExportSnapshotChunk(afterKey, maxLeaves)
	if err != nil {
		return nil, err
	}
	result := &types.StateSnapshotChunkResult{
		Height:  height,
		Leaves:  leaves,
		NextKey: nextKey,
		Done:    done,
	}
	if len(afterKey) == 0 {
		b, err := e.GetBlockByHeight(height)
		if err != nil {
			return nil, fmt.Errorf("state snapshot chunk at height %d has no stored block: %w", height, err)
		}
		result.Block = b
	}
	return result, nil
}

// snapshotSessionTTL bounds how long an immutable chunk session survives before
// its pinned LevelDB snapshot is released and its id stops resolving.
const snapshotSessionTTL = 10 * time.Minute

const (
	maxSnapshotChunkSessions          = 32
	maxSnapshotChunkSessionsPerSource = 4
)

func normalizeSnapshotChunkLeaves(n int) int {
	if n <= 0 || n > MaxSnapshotChunkLeaves {
		return MaxSnapshotChunkLeaves
	}
	return n
}

// snapshotChunkSession is one in-flight immutable chunk transfer: a pinned
// state snapshot plus the head block it was begun at.
type snapshotChunkSession struct {
	mu      sync.Mutex
	session *state.SnapshotSession
	block   *types.Block
	expires time.Time
	source  string
	final   *types.StateSnapshotChunkResult
}

// StartSnapshotChunkSession pins the state at its current height and returns an
// opaque session id plus the FIRST page (which carries the head block). Every
// later page uses the same id so the whole transfer reads one immutable
// snapshot, immune to blocks committed in between. maxLeaves <= 0 requests the
// default page size; larger values are clamped to MaxSnapshotChunkLeaves.
func (e *Executor) StartSnapshotChunkSession(maxLeaves int) (string, *types.StateSnapshotChunkResult, error) {
	return e.StartSnapshotChunkSessionFrom("", maxLeaves)
}

// StartSnapshotChunkSessionFrom is StartSnapshotChunkSession keyed by source
// for the per-peer session quota. Empty source still counts toward the global cap.
func (e *Executor) StartSnapshotChunkSessionFrom(source string, maxLeaves int) (string, *types.StateSnapshotChunkResult, error) {
	return e.StartSnapshotChunkSessionFromAt(source, nil, maxLeaves)
}

// StartSnapshotChunkSessionFromAt starts a session and returns the page whose
// keys sort after afterKey. A resume/failover client that already has a prefix
// of the snapshot uses this so the first page of a new session still carries
// the head block for re-authentication.
func (e *Executor) StartSnapshotChunkSessionFromAt(source string, afterKey []byte, maxLeaves int) (string, *types.StateSnapshotChunkResult, error) {
	maxLeaves = normalizeSnapshotChunkLeaves(maxLeaves)
	sess, err := e.state.BeginSnapshotChunkSession()
	if err != nil {
		return "", nil, err
	}
	height := sess.Height()
	block, err := e.GetBlockByHeight(height)
	if err != nil {
		sess.Release()
		return "", nil, fmt.Errorf("state snapshot chunk at height %d has no stored block: %w", height, err)
	}
	leaves, nextKey, done, err := sess.Page(afterKey, maxLeaves)
	if err != nil {
		sess.Release()
		return "", nil, err
	}
	id, err := randomSessionID()
	if err != nil {
		sess.Release()
		return "", nil, err
	}

	result := &types.StateSnapshotChunkResult{
		SessionID: id,
		Height:    height,
		Block:     block,
		Leaves:    leaves,
		NextKey:   nextKey,
		Done:      done,
	}
	// A first page that is already the whole snapshot does not need a
	// retained session — the client retries by starting again.
	if done {
		sess.Release()
		return id, result, nil
	}

	e.snapshotChunkMu.Lock()
	if e.snapshotChunkSessions == nil {
		e.snapshotChunkSessions = make(map[string]*snapshotChunkSession)
	}
	e.sweepSnapshotChunkSessionsLocked()
	if err := e.reserveSnapshotChunkSessionLocked(source); err != nil {
		e.snapshotChunkMu.Unlock()
		sess.Release()
		return "", nil, err
	}
	e.snapshotChunkSessions[id] = &snapshotChunkSession{
		session: sess, block: block, expires: time.Now().Add(snapshotSessionTTL), source: source,
	}
	e.snapshotChunkMu.Unlock()

	return id, result, nil
}

// PageSnapshotChunkSession serves one page from an existing immutable session.
// The head block is only attached to the first page; the final page releases
// the pinned snapshot.
func (e *Executor) PageSnapshotChunkSession(sessionID string, afterKey []byte, maxLeaves int) (*types.StateSnapshotChunkResult, error) {
	maxLeaves = normalizeSnapshotChunkLeaves(maxLeaves)
	e.snapshotChunkMu.Lock()
	e.sweepSnapshotChunkSessionsLocked()
	sc := e.snapshotChunkSessions[sessionID]
	if sc == nil {
		e.snapshotChunkMu.Unlock()
		return nil, fmt.Errorf("unknown or expired snapshot session %q; start a new transfer", sessionID)
	}
	if time.Now().After(sc.expires) {
		e.releaseSnapshotChunkSessionLocked(sessionID, sc)
		e.snapshotChunkMu.Unlock()
		return nil, fmt.Errorf("unknown or expired snapshot session %q; start a new transfer", sessionID)
	}
	e.snapshotChunkMu.Unlock()

	sc.mu.Lock()
	if sc.session == nil {
		sc.mu.Unlock()
		return nil, fmt.Errorf("unknown or expired snapshot session %q; start a new transfer", sessionID)
	}
	if sc.final != nil {
		cp := *sc.final
		e.unpinSnapshotChunkSession(sc)
		sc.mu.Unlock()
		e.forgetSnapshotChunkSession(sessionID, sc)
		return &cp, nil
	}

	leaves, nextKey, done, err := sc.session.Page(afterKey, maxLeaves)
	if err != nil {
		sc.mu.Unlock()
		return nil, err
	}
	result := &types.StateSnapshotChunkResult{
		SessionID: sessionID,
		Height:    sc.session.Height(),
		Leaves:    leaves,
		NextKey:   nextKey,
		Done:      done,
	}
	if done {
		cp := *result
		sc.final = &cp
	}
	sc.mu.Unlock()
	return result, nil
}

// sweepSnapshotChunkSessionsLocked releases expired sessions. Caller holds
// snapshotChunkMu.
func (e *Executor) sweepSnapshotChunkSessionsLocked() {
	now := time.Now()
	for id, sc := range e.snapshotChunkSessions {
		if now.After(sc.expires) {
			e.releaseSnapshotChunkSessionLocked(id, sc)
		}
	}
}

func (e *Executor) releaseSnapshotChunkSessionLocked(id string, sc *snapshotChunkSession) {
	if sc != nil {
		sc.mu.Lock()
		e.unpinSnapshotChunkSession(sc)
		sc.mu.Unlock()
	}
	delete(e.snapshotChunkSessions, id)
}

func (e *Executor) unpinSnapshotChunkSession(sc *snapshotChunkSession) {
	if sc.session != nil {
		sc.session.Release()
		sc.session = nil
	}
}

func (e *Executor) forgetSnapshotChunkSession(id string, sc *snapshotChunkSession) {
	e.snapshotChunkMu.Lock()
	defer e.snapshotChunkMu.Unlock()
	if cur, ok := e.snapshotChunkSessions[id]; ok && cur == sc {
		delete(e.snapshotChunkSessions, id)
	}
}

func (e *Executor) reserveSnapshotChunkSessionLocked(source string) error {
	if len(e.snapshotChunkSessions) >= maxSnapshotChunkSessions {
		return fmt.Errorf("too many snapshot chunk sessions in flight (max %d)", maxSnapshotChunkSessions)
	}
	if source == "" {
		return nil
	}
	n := 0
	for _, sc := range e.snapshotChunkSessions {
		if sc.source == source {
			n++
		}
	}
	if n >= maxSnapshotChunkSessionsPerSource {
		return fmt.Errorf("too many snapshot chunk sessions from this source (max %d)", maxSnapshotChunkSessionsPerSource)
	}
	return nil
}

func (e *Executor) releaseAllSnapshotChunkSessions() {
	e.snapshotChunkMu.Lock()
	defer e.snapshotChunkMu.Unlock()
	for id, sc := range e.snapshotChunkSessions {
		e.releaseSnapshotChunkSessionLocked(id, sc)
	}
}

func randomSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("snapshot session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ImportStateSnapshot adopts a peer's state at its head block on a node with
// no blocks and no state.
//
// Trust: the caller has verified the head block's signature against the
// pinned genesis validator key. Here the block is checked structurally and
// for this chain, and the leaves must hash to its header's state_root — so
// the state is exactly what that authenticated block committed to.
//
// RECOVERY: Uses staged import with explicit boundaries. A crash at any point
// can be recovered deterministically on restart:
//   - Before commit: staged data discarded, import retried
//   - After commit: partial state completed automatically
//
// No manual wipe-and-retry needed. See ReconcileSnapshotImport().
//
// The certificate is deliberately NOT re-verified against the imported
// registry: the set after block H is not the set that certified H (a register
// or unregister inside H changes it), so that check would refuse honest
// snapshots at exactly the heights a join is most likely to happen.
func (e *Executor) ImportStateSnapshot(snap *types.StateSnapshot) error {
	if snap == nil || snap.Block == nil {
		return errors.New("state snapshot carries no head block")
	}
	b := snap.Block
	if b.Header.Height != snap.Height {
		return fmt.Errorf("state snapshot says height %d but its block is at %d", snap.Height, b.Header.Height)
	}
	if len(snap.Leaves) == 0 {
		return errors.New("state snapshot carries no leaves")
	}
	if err := b.Validate(); err != nil {
		return fmt.Errorf("state snapshot head block: %w", err)
	}
	if b.Header.ChainID != types.ChainID {
		return fmt.Errorf("state snapshot is for chain %q, this node is on %q", b.Header.ChainID, types.ChainID)
	}
	if got := state.SnapshotRoot(snap.Leaves); got != b.Header.StateRoot {
		return fmt.Errorf("%w: leaves hash to %x, head block %d commits to %x",
			state.ErrSnapshotRootMismatch, got[:8], b.Header.Height, b.Header.StateRoot[:8])
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	latest, err := e.LatestBlock()
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}
	if latest != nil && e.snapshotAlreadyAppliedLocked(latest, snap.Height, b) {
		return nil
	}
	resume := false
	if latest != nil {
		if latest.Hash == b.Hash && latest.Header.Height == snap.Height && e.state.IsFresh() {
			resume = true
		} else {
			return fmt.Errorf("%w: head is already at %d", ErrChainNotEmpty, latest.Header.Height)
		}
	}
	if !e.state.IsFresh() {
		return state.ErrStateNotFresh
	}
	// A snapshot taken at genesis is the one case the pin can still be checked.
	if b.Header.Height == 0 && e.genesisHash != nil && b.Hash != *e.genesisHash {
		return fmt.Errorf("state snapshot's block 0 hashes to %x, pinned --genesis-hash is %x", b.Hash[:8], e.genesisHash[:8])
	}

	// STAGED IMPORT WITH RECOVERY MARKERS.
	//
	// Order matters: the verified snapshot is retained to disk BEFORE the block
	// is stored, so a crash at any later point can complete the state import
	// from the retained bytes without re-asking the peer. The marker says "an
	// import is in progress, reconcile it"; the actual blockstore and committed
	// state decide HOW to reconcile — never the marker phase alone, which can be
	// stale if the write that would have advanced it was itself the crash point.
	// See ReconcileSnapshotImport.
	marker := &snapshotImportMarker{
		Height:    snap.Height,
		BlockHash: b.Hash,
		StateRoot: b.Header.StateRoot,
		ChainID:   b.Header.ChainID,
		Phase:     phaseStarted,
	}
	if err := e.writeSnapshotMarker(marker); err != nil {
		return fmt.Errorf("write recovery marker: %w", err)
	}

	// Retain the verified snapshot durably: the data recovery completes with.
	if err := e.writeSnapshotStaging(snap); err != nil {
		e.deleteSnapshotMarker()
		return fmt.Errorf("write snapshot staging: %w", err)
	}
	log.Info().Uint64("height", snap.Height).Msg("snapshot import: marker and staging written")

	// Store block unless this is a resume after the head already landed.
	if !resume {
		if err := e.storeBlock(b); err != nil {
			e.deleteSnapshotMarker()
			e.deleteSnapshotStaging()
			return fmt.Errorf("store snapshot head block: %w", err)
		}
	}

	// Advance the marker. A failure here is survivable: reconciliation judges
	// progress from the blockstore/state, not from this phase, so a stale
	// "started" marker still recovers correctly.
	marker.Phase = phaseBlockStored
	if err := e.writeSnapshotMarker(marker); err != nil {
		log.Warn().Err(err).Msg("failed to update recovery marker after block store; recovery will reconcile from the stores")
	}
	log.Info().Uint64("height", snap.Height).Msg("snapshot import: block stored")

	// Import state.
	if err := e.state.ImportSnapshot(snap.Leaves, snap.Height, b.Header.StateRoot); err != nil {
		log.Error().Err(err).Uint64("height", snap.Height).
			Msg("state snapshot: head block stored but state import failed; recovery will complete it on restart")
		return fmt.Errorf("import snapshot state: %w", err)
	}
	log.Info().Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).Msg("snapshot import: state imported")

	// Import complete: drop the marker first (recovery that finds no marker
	// treats the import as done), then the retained staging and download dir.
	if err := e.deleteSnapshotEvidence(); err != nil {
		log.Warn().Err(err).Msg("failed to delete snapshot evidence after successful import")
	}

	log.Info().Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).
		Str("hash", fmt.Sprintf("%x", b.Hash[:8])).Msg("state snapshot imported successfully")
	return nil
}

// ReconcileSnapshotImport checks for an interrupted snapshot import at startup
// and completes it, recognises it as done, or rolls back only what that import
// left behind. Called before normal operations begin.
//
// Evidence (marker, legacy staging, download dir) is deleted only when the
// stored block hash and the applied height/root all match the marker. A
// missing block plus mismatched or partial state never drops evidence. A
// legacy interrupted import with no staging returns ErrSnapshotImportIncomplete
// so --state-sync can resume instead of failing startup.
func (e *Executor) ReconcileSnapshotImport() error {
	marker, err := e.readSnapshotMarker()
	if err != nil {
		return fmt.Errorf("read snapshot marker: %w", err)
	}
	if marker == nil {
		return nil
	}

	switch marker.Phase {
	case phaseStarted, phaseBlockStored, phaseComplete:
	default:
		return fmt.Errorf("unknown recovery phase: %s", marker.Phase)
	}

	log.Warn().
		Uint64("height", marker.Height).
		Str("phase", marker.Phase).
		Str("block_hash", fmt.Sprintf("%x", marker.BlockHash[:8])).
		Str("state_root", fmt.Sprintf("%x", marker.StateRoot[:8])).
		Msg("RECOVERY: interrupted snapshot import detected")

	blockClass, _, err := e.classifySnapshotBlock(marker)
	if err != nil {
		return err
	}
	stateClass := e.classifySnapshotState(marker)

	switch {
	case blockClass == snapConflict:
		return fmt.Errorf("recovery: block at height %d has a different hash than the marker — refusing to reconcile mismatched stores",
			marker.Height)

	case blockClass == snapMatch && stateClass == snapMatch:
		log.Info().Uint64("height", marker.Height).Msg("RECOVERY: block and applied state match; dropping evidence")
		return e.deleteSnapshotEvidence()

	case blockClass == snapMatch && stateClass == snapAbsent:
		if err := e.completeSnapshotStateFromEvidence(marker); err != nil {
			return err
		}
		log.Info().Uint64("height", marker.Height).Msg("RECOVERY: state import completed from retained snapshot")
		return e.deleteSnapshotEvidence()

	case blockClass == snapAbsent && stateClass == snapMatch:
		if err := e.restoreSnapshotBlockFromEvidence(marker); err != nil {
			return err
		}
		log.Info().Uint64("height", marker.Height).Msg("RECOVERY: missing snapshot head restored from staging")
		return e.deleteSnapshotEvidence()

	case blockClass == snapAbsent && stateClass == snapAbsent:
		log.Info().Uint64("height", marker.Height).Msg("RECOVERY: nothing persisted yet; discarding marker and retained snapshot")
		return e.deleteSnapshotEvidence()

	case stateClass == snapMismatch:
		return fmt.Errorf("recovery: state at marker height %d is present but does not match the recorded root — evidence kept",
			marker.Height)

	default:
		return fmt.Errorf("%w: stores are not yet consistent at height %d; evidence kept",
			ErrSnapshotImportIncomplete, marker.Height)
	}
}

func (e *Executor) classifySnapshotBlock(marker *snapshotImportMarker) (snapClass, *types.Block, error) {
	block, err := e.GetBlockByHeight(marker.Height)
	if errors.Is(err, types.ErrBlockNotFound) {
		return snapAbsent, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("recovery: read block at height %d: %w", marker.Height, err)
	}
	if block.Hash != marker.BlockHash {
		return snapConflict, block, nil
	}
	return snapMatch, block, nil
}

func (e *Executor) classifySnapshotState(marker *snapshotImportMarker) snapClass {
	h, ok := e.state.AppliedHeight()
	if ok && h == marker.Height {
		if root, err := e.state.StateRoot(); err == nil && root == marker.StateRoot {
			return snapMatch
		}
	}
	if e.state.IsFresh() && !ok {
		return snapAbsent
	}
	return snapMismatch
}

func (e *Executor) completeSnapshotStateFromEvidence(marker *snapshotImportMarker) error {
	if !e.state.IsFresh() {
		return fmt.Errorf("recovery: block %d stored but state is neither imported nor fresh — refusing to overwrite", marker.Height)
	}
	if d, err := e.openSnapshotDownload(); err != nil {
		return fmt.Errorf("recovery: download staging unreadable: %w", err)
	} else if d != nil {
		if err := d.VerifyRoot(); err != nil {
			return fmt.Errorf("recovery: download staging failed integrity: %w", err)
		}
		if got := d.StateRoot(); got != marker.StateRoot {
			return fmt.Errorf("recovery: download staging root %x != marker %x", got[:8], marker.StateRoot[:8])
		}
		if err := e.state.ImportSnapshotJSONL(filepath.Join(d.Dir(), "leaves.jsonl"), marker.Height, marker.StateRoot); err != nil {
			return fmt.Errorf("recovery: complete state import at height %d: %w", marker.Height, err)
		}
		return nil
	}
	staged, err := e.readSnapshotStaging()
	if err != nil {
		return fmt.Errorf("recovery: block %d stored but staging unreadable: %w", marker.Height, err)
	}
	if staged == nil {
		return fmt.Errorf("%w: block %d stored but no retained snapshot to complete the import",
			ErrSnapshotImportIncomplete, marker.Height)
	}
	if got := state.SnapshotRoot(staged.Leaves); got != marker.StateRoot {
		return fmt.Errorf("recovery: retained snapshot leaves hash to %x, marker commits to %x — staging corrupted",
			got[:8], marker.StateRoot[:8])
	}
	if err := e.state.ImportSnapshot(staged.Leaves, marker.Height, marker.StateRoot); err != nil {
		return fmt.Errorf("recovery: complete state import at height %d: %w", marker.Height, err)
	}
	return nil
}

func (e *Executor) restoreSnapshotBlockFromEvidence(marker *snapshotImportMarker) error {
	if d, err := e.openSnapshotDownload(); err != nil {
		return fmt.Errorf("recovery: download staging unreadable: %w", err)
	} else if d != nil && d.Head() != nil && d.Head().Hash == marker.BlockHash {
		if err := e.storeBlock(d.Head()); err != nil {
			return fmt.Errorf("recovery: restore snapshot head: %w", err)
		}
		return nil
	}
	staged, err := e.readSnapshotStaging()
	if err != nil {
		return fmt.Errorf("recovery: staging unreadable: %w", err)
	}
	if staged == nil || staged.Block == nil {
		return fmt.Errorf("%w: applied state matches height %d but the head block is missing and no staging has it",
			ErrSnapshotImportIncomplete, marker.Height)
	}
	if staged.Block.Hash != marker.BlockHash {
		return fmt.Errorf("recovery: staged block hash %x != marker %x — evidence kept",
			staged.Block.Hash[:8], marker.BlockHash[:8])
	}
	if err := e.storeBlock(staged.Block); err != nil {
		return fmt.Errorf("recovery: restore snapshot head: %w", err)
	}
	return nil
}

func (e *Executor) snapshotAlreadyAppliedLocked(latest *types.Block, height uint64, b *types.Block) bool {
	if latest == nil || b == nil || latest.Hash != b.Hash || latest.Header.Height != height {
		return false
	}
	h, ok := e.state.AppliedHeight()
	if !ok || h != height {
		return false
	}
	root, err := e.state.StateRoot()
	return err == nil && root == b.Header.StateRoot
}

// HasIncompleteSnapshotImport reports a recovery marker still on disk.
func (e *Executor) HasIncompleteSnapshotImport() bool {
	m, err := e.readSnapshotMarker()
	return err == nil && m != nil
}

func (e *Executor) snapshotDownloadPath() string {
	if e.dataDir == "" {
		return ""
	}
	return filepath.Join(e.dataDir, "snapshot_download")
}

func (e *Executor) openSnapshotDownload() (*state.SnapshotDownload, error) {
	path := e.snapshotDownloadPath()
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(path, "progress.json")); os.IsNotExist(err) {
		return nil, nil
	}
	return state.OpenSnapshotDownload(path, 0)
}

func (e *Executor) deleteSnapshotDownload() error {
	path := e.snapshotDownloadPath()
	if path == "" {
		return nil
	}
	return state.RemoveSnapshotDownload(path)
}

func (e *Executor) deleteSnapshotEvidence() error {
	if err := e.deleteSnapshotMarker(); err != nil {
		return fmt.Errorf("recovery: delete marker: %w", err)
	}
	if err := e.deleteSnapshotStaging(); err != nil {
		return fmt.Errorf("recovery: delete staging: %w", err)
	}
	if err := e.deleteSnapshotDownload(); err != nil {
		return fmt.Errorf("recovery: delete download staging: %w", err)
	}
	return nil
}

// ImportSnapshotDownload adopts a completed on-disk chunked download. Used by
// StateSync so a large snapshot is never assembled into a second RAM copy.
func (e *Executor) ImportSnapshotDownload(dir string) error {
	d, err := state.OpenSnapshotDownload(dir, 0)
	if err != nil {
		return err
	}
	if !d.Done() || d.Head() == nil {
		return state.ErrSnapshotDownloadIncomplete
	}
	if err := d.VerifyRoot(); err != nil {
		return err
	}
	b := d.Head()
	if b.Header.Height != d.Height() {
		return fmt.Errorf("state snapshot says height %d but its block is at %d", d.Height(), b.Header.Height)
	}
	if err := b.Validate(); err != nil {
		return fmt.Errorf("state snapshot head block: %w", err)
	}
	if b.Header.ChainID != types.ChainID {
		return fmt.Errorf("state snapshot is for chain %q, this node is on %q", b.Header.ChainID, types.ChainID)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	latest, err := e.LatestBlock()
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}
	if latest != nil && e.snapshotAlreadyAppliedLocked(latest, d.Height(), b) {
		_ = d.Remove()
		_ = e.deleteSnapshotMarker()
		_ = e.deleteSnapshotStaging()
		return nil
	}
	resume := false
	if latest != nil {
		if latest.Hash == b.Hash && latest.Header.Height == d.Height() && e.state.IsFresh() {
			resume = true
		} else {
			return fmt.Errorf("%w: head is already at %d", ErrChainNotEmpty, latest.Header.Height)
		}
	}
	if !e.state.IsFresh() {
		return state.ErrStateNotFresh
	}
	if b.Header.Height == 0 && e.genesisHash != nil && b.Hash != *e.genesisHash {
		return fmt.Errorf("state snapshot's block 0 hashes to %x, pinned --genesis-hash is %x", b.Hash[:8], e.genesisHash[:8])
	}

	marker := &snapshotImportMarker{
		Height:    d.Height(),
		BlockHash: b.Hash,
		StateRoot: b.Header.StateRoot,
		ChainID:   b.Header.ChainID,
		Phase:     phaseStarted,
	}
	if err := e.writeSnapshotMarker(marker); err != nil {
		return fmt.Errorf("write recovery marker: %w", err)
	}
	if !resume {
		if err := e.storeBlock(b); err != nil {
			e.deleteSnapshotMarker()
			return fmt.Errorf("store snapshot head block: %w", err)
		}
		marker.Phase = phaseBlockStored
		if err := e.writeSnapshotMarker(marker); err != nil {
			log.Warn().Err(err).Msg("failed to update recovery marker after block store; recovery will reconcile from the stores")
		}
	}
	if err := e.state.ImportSnapshotJSONL(filepath.Join(d.Dir(), "leaves.jsonl"), d.Height(), b.Header.StateRoot); err != nil {
		log.Error().Err(err).Uint64("height", d.Height()).
			Msg("state snapshot: head block stored but state import failed; recovery will complete it on restart")
		return fmt.Errorf("import snapshot state: %w", err)
	}
	if err := e.deleteSnapshotEvidence(); err != nil {
		log.Warn().Err(err).Msg("failed to delete snapshot evidence after successful import")
	}
	log.Info().Uint64("height", d.Height()).
		Str("hash", fmt.Sprintf("%x", b.Hash[:8])).Msg("state snapshot imported from disk staging")
	return nil
}
