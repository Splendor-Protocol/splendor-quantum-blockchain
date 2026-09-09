// Package network — state sync: joining a chain whose history cannot be
// replayed by taking a peer's live state at its head block.
//
// Trust model, unchanged in kind from block sync: the pinned genesis validator
// key authenticates the head block, and the header's state_root authenticates
// the leaves (checked by the importer). Nothing a peer serves is believed on
// its own say-so; a node with no pinned key cannot state-sync, exactly as it
// cannot block-sync.
package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ErrSnapshotNotAnchored means the snapshot's head block was sealed by a
// validator other than the pinned genesis one. With empty local state there
// is no registry to resolve that validator against, so the snapshot cannot be
// used — but it is not evidence of a hostile peer: under leader rotation the
// next head may well be the genesis validator's, so callers retry rather than
// give up.
var ErrSnapshotNotAnchored = errors.New("state snapshot head was not sealed by the trusted genesis validator")

const (
	// snapshotFetchTimeout bounds one snapshot query; the reply is tens of
	// MiB at the cap, not a block.
	snapshotFetchTimeout = 3 * time.Minute

	// maxSnapshotReplyBytes bounds the sealed reply. The server caps a
	// snapshot at 64 MiB of raw leaves (block.MaxStateSnapshotBytes); those
	// are base64 in the JSON and base64 once more inside the envelope, so
	// the wire form is under 2x that.
	maxSnapshotReplyBytes = 192 << 20
)

// FetchStateSnapshot pulls a peer's state at its head over the sealed channel
// and authenticates the head block against the pinned genesis validator key.
// There is no plaintext form: the snapshot is only ever served sealed.
func (f *BlockFetcher) FetchStateSnapshot(peerAddr string) (*types.StateSnapshot, error) {
	trusted := f.TrustedPubKey()
	if len(trusted) == 0 {
		return nil, fmt.Errorf("state sync from %s: no trusted validator pubkey yet "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", peerAddr)
	}
	if f.cfg.Identities == nil {
		return nil, fmt.Errorf("state sync from %s: no sealed transport configured, and snapshots are only served sealed", peerAddr)
	}
	raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetStateSnapshot, []byte(`{}`),
		maxSnapshotReplyBytes, snapshotFetchTimeout)
	if err != nil {
		return nil, fmt.Errorf("fetch state snapshot from %s: %w", peerAddr, err)
	}

	var snap types.StateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("decode state snapshot from %s: %w", peerAddr, err)
	}
	if snap.Block == nil {
		return nil, fmt.Errorf("state snapshot from %s carries no head block", peerAddr)
	}
	if snap.Block.Header.Height != snap.Height {
		return nil, fmt.Errorf("state snapshot from %s says height %d but its block is at %d",
			peerAddr, snap.Height, snap.Block.Header.Height)
	}
	// Bind the served Hash to the header before any signature is believed
	// over it (the same closure FetchGenesisValidatorPubKey needs).
	if err := snap.Block.Validate(); err != nil {
		return nil, fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(trusted)
	if err != nil {
		return nil, fmt.Errorf("trusted validator pubkey: %w", err)
	}
	if snap.Block.Header.ValidatorAddr != qcrypto.DeriveAddress(pub) {
		return nil, fmt.Errorf("%w: head %d from %s was sealed by %s",
			ErrSnapshotNotAnchored, snap.Height, peerAddr, snap.Block.Header.ValidatorAddr)
	}
	if err := snap.Block.VerifySignature(trusted); err != nil {
		return nil, fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	return &snap, nil
}

// StateSync tries every known peer until one snapshot is fetched and
// imported. The importer is the executor's ImportStateSnapshot; a failure
// there moves on to the next peer, and the returned error names every
// refusal so an operator can see which peer said what.
func (s *Syncer) StateSync(importer func(*types.StateSnapshot) error) error {
	peers := s.pool.Addrs()
	if len(peers) == 0 {
		return errors.New("state sync: no peers to take a snapshot from")
	}
	var refusals []string
	for _, peer := range peers {
		snap, err := s.fetcher.FetchStateSnapshot(peer)
		if err != nil {
			log.Warn().Err(err).Str("peer", peer).Msg("state sync: snapshot not usable")
			refusals = append(refusals, fmt.Sprintf("%s: %v", peer, err))
			continue
		}
		if err := importer(snap); err != nil {
			log.Warn().Err(err).Str("peer", peer).Uint64("height", snap.Height).Msg("state sync: import failed")
			refusals = append(refusals, fmt.Sprintf("%s: import: %v", peer, err))
			continue
		}
		log.Info().Str("peer", peer).Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).
			Msg("state sync: snapshot imported, block sync resumes from the next height")
		return nil
	}
	return fmt.Errorf("state sync: no peer served a usable snapshot: %s", strings.Join(refusals, "; "))
}
