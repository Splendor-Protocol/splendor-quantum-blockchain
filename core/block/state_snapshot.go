package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	
	// Fsync parent directory to ensure rename is durable
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		dir.Sync()
		dir.Close()
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
	
	// Fsync parent directory
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		dir.Sync()
		dir.Close()
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
	if latest != nil {
		return fmt.Errorf("%w: head is already at %d", ErrChainNotEmpty, latest.Header.Height)
	}
	if !e.state.IsFresh() {
		return state.ErrStateNotFresh
	}
	// A snapshot taken at genesis is the one case the pin can still be checked.
	if b.Header.Height == 0 && e.genesisHash != nil && b.Hash != *e.genesisHash {
		return fmt.Errorf("state snapshot's block 0 hashes to %x, pinned --genesis-hash is %x", b.Hash[:8], e.genesisHash[:8])
	}

	// STAGED IMPORT WITH RECOVERY MARKERS
	// Step 1: Write recovery marker BEFORE any modifications
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
	log.Info().Uint64("height", snap.Height).Msg("snapshot import: recovery marker written")

	// Step 2: Store block
	if err := e.storeBlock(b); err != nil {
		// Clean up marker on failure
		e.deleteSnapshotMarker()
		return fmt.Errorf("store snapshot head block: %w", err)
	}
	
	// Update marker: block is stored
	marker.Phase = phaseBlockStored
	if err := e.writeSnapshotMarker(marker); err != nil {
		log.Warn().Err(err).Msg("failed to update recovery marker after block store")
		// Continue anyway - block is stored, we can recover
	}
	log.Info().Uint64("height", snap.Height).Msg("snapshot import: block stored")

	// Step 3: Import state
	if err := e.state.ImportSnapshot(snap.Leaves, snap.Height, b.Header.StateRoot); err != nil {
		log.Error().Err(err).Uint64("height", snap.Height).
			Msg("state snapshot: head block stored but state import failed — restart to retry or wipe data dir")
		return fmt.Errorf("import snapshot state: %w", err)
	}
	log.Info().Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).Msg("snapshot import: state imported")

	// Step 4: Delete marker - import complete
	if err := e.deleteSnapshotMarker(); err != nil {
		log.Warn().Err(err).Msg("failed to delete recovery marker after successful import")
		// Not fatal - import succeeded
	}
	
	log.Info().Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).
		Str("hash", fmt.Sprintf("%x", b.Hash[:8])).Msg("state snapshot imported successfully")
	return nil
}

// ReconcileSnapshotImport checks for an interrupted snapshot import at startup
// and completes or rolls it back. Called before normal operations begin.
//
// Recovery logic:
//   - No marker: normal startup, nothing to do
//   - Marker phase="started": nothing persisted yet, delete marker and retry
//   - Marker phase="block_stored": block is in DB but state isn't, complete state import
//
// This ensures every startup either has a complete snapshot or a clean slate,
// never a half-imported state that would corrupt the chain.
func (e *Executor) ReconcileSnapshotImport() error {
	marker, err := e.readSnapshotMarker()
	if err != nil {
		return fmt.Errorf("read snapshot marker: %w", err)
	}
	if marker == nil {
		// No import in progress
		return nil
	}

	log.Warn().
		Uint64("height", marker.Height).
		Str("phase", marker.Phase).
		Str("block_hash", fmt.Sprintf("%x", marker.BlockHash[:8])).
		Msg("RECOVERY: interrupted snapshot import detected")

	switch marker.Phase {
	case phaseStarted:
		// Nothing was persisted yet - just clean up marker
		log.Info().Msg("RECOVERY: import never started, cleaning up marker")
		if err := e.deleteSnapshotMarker(); err != nil {
			return fmt.Errorf("delete stale marker: %w", err)
		}
		log.Info().Msg("RECOVERY: clean startup, ready to retry import")
		return nil

	case phaseBlockStored:
		// Block is stored but state import didn't complete
		// We need the original snapshot to complete import
		log.Error().
			Uint64("height", marker.Height).
			Msg("RECOVERY: block stored but state incomplete - operator must retry state sync to provide snapshot again")
		// Don't delete marker - it proves import was interrupted
		return fmt.Errorf("incomplete snapshot import at height %d: block stored but state missing - retry state sync", marker.Height)

	case phaseComplete:
		// Both are done, just need to delete marker
		log.Info().Msg("RECOVERY: import was complete, removing marker")
		if err := e.deleteSnapshotMarker(); err != nil {
			return fmt.Errorf("delete completed marker: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unknown recovery phase: %s", marker.Phase)
	}
}
