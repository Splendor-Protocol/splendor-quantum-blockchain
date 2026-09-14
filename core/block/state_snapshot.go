package block

import (
	"errors"
	"fmt"

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

	if err := e.storeBlock(b); err != nil {
		return fmt.Errorf("store snapshot head block: %w", err)
	}
	if err := e.state.ImportSnapshot(snap.Leaves, snap.Height, b.Header.StateRoot); err != nil {
		log.Error().Err(err).Uint64("height", snap.Height).
			Msg("state snapshot: head block stored but state import failed — wipe the data dir and run state sync again")
		return fmt.Errorf("import snapshot state: %w", err)
	}
	log.Info().Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).
		Str("hash", fmt.Sprintf("%x", b.Hash[:8])).Msg("state snapshot imported")
	return nil
}
