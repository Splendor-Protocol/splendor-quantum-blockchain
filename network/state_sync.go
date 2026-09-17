// Package network — state sync: joining a chain whose history cannot be
// replayed by taking a peer's live state at its head block.
//
// Trust model (R5): genesis-checkpoint only. The pinned genesis validator
// key authenticates the head block, and the header's state_root authenticates
// the leaves (checked by the importer). A current head sealed by a later
// rotating leader is refused (ErrSnapshotNotAnchored) until a genesis-sealed
// checkpoint is available — the join has no authenticated validator history
// on empty state. The snapshot's own registry is never used as a trust
// source. Nothing a peer serves is believed on its own say-so.
package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
var ErrSnapshotNotAnchored = errors.New("state snapshot head was not sealed by the trusted genesis validator (genesis-checkpoint trust: retry until a genesis-sealed head is available; the snapshot registry is not a trust source)")

const (
	// snapshotFetchTimeout bounds one snapshot query (single-shot or one
	// chunked page). The reply is tens of MiB at the cap, not a block.
	snapshotFetchTimeout = 3 * time.Minute

	// snapshotTransferTimeout bounds a whole chunked download, including
	// every page. Each page uses only the remaining time, so the overall
	// deadline cannot be exceeded by starting another full per-request wait.
	snapshotTransferTimeout = 15 * time.Minute

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

func (f *BlockFetcher) snapshotDiskLimit() int64 {
	if f.cfg.MaxSnapshotDiskBytes > 0 {
		return f.cfg.MaxSnapshotDiskBytes
	}
	return state.DefaultMaxSnapshotDiskBytes
}

// remainingRequestTimeout is the time a single page (or single-shot) query
// may use: the per-request cap, or whatever is left of the overall deadline,
// whichever is smaller. Zero means the overall budget is already gone.
func remainingRequestTimeout(deadline time.Time, perRequest time.Duration, now time.Time) time.Duration {
	left := deadline.Sub(now)
	if left <= 0 {
		return 0
	}
	if left < perRequest {
		return left
	}
	return perRequest
}

func sealedChunkNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "peer returned 404")
}

// FetchStateSnapshotChunked pulls a peer's state page by page into disk
// staging (cfg.SnapshotStagingDir, or a temporary directory). The head is
// authenticated before further pages. Pages are not accumulated in RAM; the
// returned snapshot is assembled only after the transfer and root check.
// chunkSize <= 0 lets the peer choose its own default page size.
func (f *BlockFetcher) FetchStateSnapshotChunked(peerAddr string, chunkSize int) (*types.StateSnapshot, error) {
	store, cleanup, err := f.openChunkStaging()
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if err := f.fetchChunkedInto(peerAddr, chunkSize, store); err != nil {
		return nil, err
	}
	return store.Assemble()
}

func (f *BlockFetcher) openChunkStaging() (*state.SnapshotDownload, func(), error) {
	dir := f.cfg.SnapshotStagingDir
	cleanup := func() {}
	if dir == "" {
		tmp, err := os.MkdirTemp("", "sqc-snap-dl-*")
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot download staging: %w", err)
		}
		dir = tmp
		cleanup = func() { _ = os.RemoveAll(tmp) }
	}
	store, err := state.OpenSnapshotDownload(dir, f.snapshotDiskLimit())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return store, cleanup, nil
}

// fetchChunkedInto streams pages from peer into store, resuming from the
// persisted cursor when the store already holds an authenticated head.
func (f *BlockFetcher) fetchChunkedInto(peerAddr string, chunkSize int, store *state.SnapshotDownload) error {
	trusted := f.TrustedPubKey()
	if len(trusted) == 0 {
		return fmt.Errorf("state sync from %s: no trusted validator pubkey yet "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", peerAddr)
	}
	if f.cfg.Identities == nil {
		return fmt.Errorf("state sync from %s: no sealed transport configured, and snapshots are only served sealed", peerAddr)
	}
	if store.Done() {
		return store.VerifyRoot()
	}

	var (
		sessionID = store.SessionID()
		afterKey  = store.AfterKey()
	)
	const maxPages = 1 << 16
	deadline := time.Now().Add(snapshotTransferTimeout)
	retriedSession := false
	for page := 0; ; page++ {
		if page >= maxPages {
			return fmt.Errorf("state snapshot from %s: exceeded %d pages", peerAddr, maxPages)
		}
		reqTimeout := remainingRequestTimeout(deadline, snapshotFetchTimeout, time.Now())
		if reqTimeout <= 0 {
			return fmt.Errorf("state snapshot from %s: transfer exceeded %s", peerAddr, snapshotTransferTimeout)
		}

		reqBody, err := json.Marshal(GetStateSnapshotChunkRequest{SessionID: sessionID, AfterKey: afterKey, MaxLeaves: chunkSize})
		if err != nil {
			return fmt.Errorf("marshal chunk request: %w", err)
		}
		raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetStateSnapshotChunk, reqBody,
			maxSnapshotChunkReplyBytes, reqTimeout)
		if err != nil {
			if sessionID != "" && !retriedSession && sealedChunkNotFound(err) {
				// Session expired on this or a previous peer: start a new
				// session at the saved cursor under the same head.
				sessionID = ""
				retriedSession = true
				page--
				continue
			}
			return fmt.Errorf("fetch state snapshot chunk from %s: %w", peerAddr, err)
		}
		var chunk StateSnapshotChunk
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return fmt.Errorf("decode state snapshot chunk from %s: %w", peerAddr, err)
		}

		if !store.HasHead() {
			if chunk.Block == nil {
				return fmt.Errorf("state snapshot chunk from %s carries no head block on its first page", peerAddr)
			}
			if err := authenticateSnapshotHead(peerAddr, trusted, chunk.Block, chunk.Height); err != nil {
				return err
			}
			if err := store.AcceptHead(chunk.Block, chunk.Height); err != nil {
				return err
			}
		} else {
			if chunk.Height != store.Height() {
				return fmt.Errorf("state snapshot from %s changed height mid-transfer (%d -> %d); the peer's "+
					"state moved during the transfer, retry from scratch", peerAddr, store.Height(), chunk.Height)
			}
			if chunk.Block != nil {
				if err := authenticateSnapshotHead(peerAddr, trusted, chunk.Block, chunk.Height); err != nil {
					return err
				}
				if err := store.AcceptHead(chunk.Block, chunk.Height); err != nil {
					return err
				}
			}
		}
		if chunk.SessionID != "" {
			sessionID = chunk.SessionID
		}

		if !chunk.Done {
			if len(chunk.NextKey) == 0 {
				return fmt.Errorf("state snapshot chunk from %s: not done but carries no next key", peerAddr)
			}
			if len(afterKey) > 0 && bytes.Compare(chunk.NextKey, afterKey) <= 0 {
				return fmt.Errorf("state snapshot chunk from %s: cursor did not advance", peerAddr)
			}
		}

		if err := store.AppendPage(chunk.Leaves, chunk.NextKey, chunk.Done, sessionID); err != nil {
			return fmt.Errorf("stage snapshot page from %s: %w", peerAddr, err)
		}
		if chunk.Done {
			break
		}
		afterKey = chunk.NextKey
		retriedSession = false
	}
	return store.VerifyRoot()
}

// StateSync tries every known peer until one snapshot is fetched and
// imported. The importer is the executor's ImportStateSnapshot; a failure
// there moves on to the next peer, and the returned error names every
// refusal so an operator can see which peer said what.
//
// A configured SnapshotStagingDir lets a failed peer (or a restart) resume
// the same authenticated snapshot from the persisted cursor instead of
// starting over in RAM.
func (s *Syncer) StateSync(importer func(*types.StateSnapshot) error) error {
	peers := s.pool.Addrs()
	if len(peers) == 0 {
		return errors.New("state sync: no peers to take a snapshot from")
	}

	var store *state.SnapshotDownload
	if dir := s.fetcher.cfg.SnapshotStagingDir; dir != "" {
		opened, err := state.OpenSnapshotDownload(dir, s.fetcher.snapshotDiskLimit())
		if err != nil {
			log.Warn().Err(err).Str("dir", dir).Msg("state sync: discarding unreadable download staging")
			_ = state.RemoveSnapshotDownload(dir)
			opened, err = state.OpenSnapshotDownload(dir, s.fetcher.snapshotDiskLimit())
			if err != nil {
				return fmt.Errorf("state sync: open download staging: %w", err)
			}
		}
		store = opened
	}

	var refusals []string
	for _, peer := range peers {
		snap, err := s.fetchSnapshotFromPeer(peer, store)
		if err != nil {
			log.Warn().Err(err).Str("peer", peer).Msg("state sync: snapshot not usable")
			refusals = append(refusals, fmt.Sprintf("%s: %v", peer, err))
			continue
		}
		if store != nil && store.Done() && s.fetcher.cfg.AdoptSnapshotDownload != nil {
			if err := s.fetcher.cfg.AdoptSnapshotDownload(store.Dir()); err != nil {
				log.Warn().Err(err).Str("peer", peer).Msg("state sync: download import failed")
				refusals = append(refusals, fmt.Sprintf("%s: import: %v", peer, err))
				continue
			}
			log.Info().Str("peer", peer).Uint64("height", store.Height()).
				Msg("state sync: snapshot imported from disk staging, block sync resumes from the next height")
			return nil
		}
		if snap == nil && store != nil {
			snap, err = store.Assemble()
			if err != nil {
				refusals = append(refusals, fmt.Sprintf("%s: assemble: %v", peer, err))
				continue
			}
		}
		if snap == nil {
			refusals = append(refusals, fmt.Sprintf("%s: no snapshot assembled", peer))
			continue
		}
		if err := importer(snap); err != nil {
			log.Warn().Err(err).Str("peer", peer).Uint64("height", snap.Height).Msg("state sync: import failed")
			refusals = append(refusals, fmt.Sprintf("%s: import: %v", peer, err))
			continue
		}
		if store != nil {
			_ = store.Remove()
		}
		log.Info().Str("peer", peer).Uint64("height", snap.Height).Int("leaves", len(snap.Leaves)).
			Msg("state sync: snapshot imported, block sync resumes from the next height")
		return nil
	}
	return fmt.Errorf("state sync: no peer served a usable snapshot: %s", strings.Join(refusals, "; "))
}

func (s *Syncer) fetchSnapshotFromPeer(peer string, store *state.SnapshotDownload) (*types.StateSnapshot, error) {
	if store != nil && store.HasHead() {
		if err := s.fetcher.fetchChunkedInto(peer, 0, store); err != nil {
			return nil, err
		}
		return nil, nil
	}
	snap, err := s.fetcher.FetchStateSnapshot(peer)
	if err != nil {
		log.Debug().Err(err).Str("peer", peer).Msg("state sync: single-shot snapshot unavailable, trying chunked")
		if store == nil {
			return s.fetcher.FetchStateSnapshotChunked(peer, 0)
		}
		if err := s.fetcher.fetchChunkedInto(peer, 0, store); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return snap, nil
}
