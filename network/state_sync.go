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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
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

// maxSnapshotChunkReplyBytes bounds one chunked page's sealed reply. Far
// smaller than maxSnapshotReplyBytes because a chunk is capped at
// block.MaxSnapshotChunkLeaves leaves, not the whole state.
const maxSnapshotChunkReplyBytes = 16 << 20

// maxChunkedLeafBytes bounds the total raw key+value bytes a chunked transfer
// may accumulate on this node: the same 64 MiB the single-shot snapshot caps
// its leaves at (block.MaxStateSnapshotBytes). It keeps this node's memory
// bounded to one snapshot's worth of leaves no matter how many pages a peer
// serves.
const maxChunkedLeafBytes = 64 << 20

// authenticateSnapshotHead binds the served hash to the header and checks the
// head's seal against the trusted validator key. Call it as soon as the head is
// known, before any further (potentially expensive) transfer work.
func authenticateSnapshotHead(peerAddr string, trusted []byte, head *types.Block, height uint64) error {
	if head.Header.Height != height {
		return fmt.Errorf("state snapshot from %s says height %d but its block is at %d",
			peerAddr, height, head.Header.Height)
	}
	if err := head.Validate(); err != nil {
		return fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(trusted)
	if err != nil {
		return fmt.Errorf("trusted validator pubkey: %w", err)
	}
	if head.Header.ValidatorAddr != qcrypto.DeriveAddress(pub) {
		return fmt.Errorf("%w: head %d from %s was sealed by %s",
			ErrSnapshotNotAnchored, height, peerAddr, head.Header.ValidatorAddr)
	}
	if err := head.VerifySignature(trusted); err != nil {
		return fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	return nil
}

// FetchStateSnapshotChunked pulls a peer's state page by page. The head block
// (attached to the first chunk) is authenticated against the pinned genesis
// validator key BEFORE any further page is requested, and the fully assembled
// leaves are checked against that block's declared state_root before anything
// is returned — chunking changes how the bytes move over the wire, never what
// is trusted. A height change mid-transfer aborts, as does a cursor that fails
// to advance, a page-count/deadline breach, or raw leaves past the byte budget,
// so a hostile or buggy peer cannot keep this node accumulating.
//
// chunkSize <= 0 lets the peer choose its own default page size.
func (f *BlockFetcher) FetchStateSnapshotChunked(peerAddr string, chunkSize int) (*types.StateSnapshot, error) {
	trusted := f.TrustedPubKey()
	if len(trusted) == 0 {
		return nil, fmt.Errorf("state sync from %s: no trusted validator pubkey yet "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", peerAddr)
	}
	if f.cfg.Identities == nil {
		return nil, fmt.Errorf("state sync from %s: no sealed transport configured, and snapshots are only served sealed", peerAddr)
	}

	var (
		head       *types.Block
		height     uint64
		haveHeight bool
		leaves     []types.StateLeaf
		afterKey   []byte
		sessionID  string
		totalBytes int
	)
	const maxPages = 1 << 16 // far above any real state; a hard stop against a loop bug
	deadline := time.Now().Add(snapshotFetchTimeout)
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("state snapshot from %s: exceeded %d pages", peerAddr, maxPages)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("state snapshot from %s: transfer exceeded %s", peerAddr, snapshotFetchTimeout)
		}

		reqBody, err := json.Marshal(GetStateSnapshotChunkRequest{SessionID: sessionID, AfterKey: afterKey, MaxLeaves: chunkSize})
		if err != nil {
			return nil, fmt.Errorf("marshal chunk request: %w", err)
		}
		raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetStateSnapshotChunk, reqBody,
			maxSnapshotChunkReplyBytes, snapshotFetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("fetch state snapshot chunk from %s: %w", peerAddr, err)
		}
		var chunk StateSnapshotChunk
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return nil, fmt.Errorf("decode state snapshot chunk from %s: %w", peerAddr, err)
		}

		if !haveHeight {
			height = chunk.Height
			haveHeight = true
			if chunk.Block == nil {
				return nil, fmt.Errorf("state snapshot chunk from %s carries no head block on its first page", peerAddr)
			}
			head = chunk.Block
			// Carry the server's session across pages so the whole transfer
			// reads one immutable snapshot (empty if the peer is pre-session).
			sessionID = chunk.SessionID
			// Authenticate the head NOW, before requesting any further page:
			// do not keep downloading a snapshot whose head we reject anyway.
			if err := authenticateSnapshotHead(peerAddr, trusted, head, height); err != nil {
				return nil, err
			}
		} else if chunk.Height != height {
			return nil, fmt.Errorf("state snapshot from %s changed height mid-transfer (%d -> %d); the peer's "+
				"state moved during the transfer, retry from scratch", peerAddr, height, chunk.Height)
		}

		// Every non-final page must carry a cursor strictly past the one that
		// produced it, so a hostile or buggy peer cannot loop us on one page.
		if !chunk.Done {
			if len(chunk.NextKey) == 0 {
				return nil, fmt.Errorf("state snapshot chunk from %s: not done but carries no next key", peerAddr)
			}
			if len(afterKey) > 0 && bytes.Compare(chunk.NextKey, afterKey) <= 0 {
				return nil, fmt.Errorf("state snapshot chunk from %s: cursor did not advance", peerAddr)
			}
			afterKey = chunk.NextKey
		}

		for _, l := range chunk.Leaves {
			totalBytes += len(l.Key) + len(l.Value)
		}
		if totalBytes > maxChunkedLeafBytes {
			return nil, fmt.Errorf("state snapshot from %s: raw leaves exceed the %d-byte budget", peerAddr, maxChunkedLeafBytes)
		}
		leaves = append(leaves, chunk.Leaves...)
		if chunk.Done {
			break
		}
	}

	// Final integrity: the assembled leaves must hash to the head's committed
	// root, so a peer that serves pages from a different state is caught here
	// before anything is handed to the importer.
	if got := state.SnapshotRoot(leaves); got != head.Header.StateRoot {
		return nil, fmt.Errorf("state snapshot from %s: assembled leaves hash to %x, head %d commits to %x",
			peerAddr, got[:8], height, head.Header.StateRoot[:8])
	}
	return &types.StateSnapshot{Height: height, Block: head, Leaves: leaves}, nil
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
			// Bounded legacy fallback: the chunked path covers states too large
			// to serve in one reply and carries the same trust rules.
			log.Debug().Err(err).Str("peer", peer).Msg("state sync: single-shot snapshot unavailable, trying chunked")
			snap, err = s.fetcher.FetchStateSnapshotChunked(peer, 0)
		}
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
