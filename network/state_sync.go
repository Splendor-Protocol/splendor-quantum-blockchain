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
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// ErrSnapshotNotAnchored means the snapshot's head block was sealed by a
// validator other than the pinned genesis one. With empty local state there
// is no registry to resolve that validator against, so the snapshot cannot be
// used — but it is not evidence of a hostile peer: under leader rotation the
// next head may well be the genesis validator's, so callers retry rather than
// give up.
// ErrSnapshotCheckpointUnavailable means the staged transfer is pinned to a
// checkpoint the peer has moved past: it can no longer serve pages under that
// head, and pages from two different heads must never be spliced together.
// The transfer is not resumable — it has to be abandoned and restarted at a
// checkpoint the network still holds.
var ErrSnapshotCheckpointUnavailable = errors.New("state snapshot: the staged checkpoint is no longer servable")

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
	if err := validateFetchedSnapshot(peerAddr, trusted, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// FetchStateSnapshotCoded pulls the peer's state snapshot as a coded object
// over the sealed channel, reconstructs it, and runs the SAME head
// authentication as the plain single-shot path: the transfer is authenticated
// by the object manifest (pinned to the peer's node identity) and the content
// by the genesis validator seal on the head block. A peer that does not serve
// coded objects, or a transfer failure of any kind, returns an error so the
// caller falls back to the plain paths.
func (f *BlockFetcher) FetchStateSnapshotCoded(peerAddr string) (*types.StateSnapshot, error) {
	trusted := f.TrustedPubKey()
	if len(trusted) == 0 {
		return nil, fmt.Errorf("state sync from %s: no trusted validator pubkey yet "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", peerAddr)
	}
	raw, err := f.FetchCodedObject(peerAddr, objects.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("coded state snapshot from %s: %w", peerAddr, err)
	}
	var snap types.StateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("decode coded state snapshot from %s: %w", peerAddr, err)
	}
	if err := validateFetchedSnapshot(peerAddr, trusted, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// validateFetchedSnapshot binds the served hash to the header and checks the
// head's seal against the trusted genesis validator key. Shared by every
// snapshot fetch path (single-shot, coded) so none of them can skip a check --
// which is what keeps the coded path a transport accelerator rather than a
// second, weaker trust path.
func validateFetchedSnapshot(peerAddr string, trusted []byte, snap *types.StateSnapshot) error {
	if snap.Block == nil {
		return fmt.Errorf("state snapshot from %s carries no head block", peerAddr)
	}
	if snap.Block.Header.Height != snap.Height {
		return fmt.Errorf("state snapshot from %s says height %d but its block is at %d",
			peerAddr, snap.Height, snap.Block.Header.Height)
	}
	// Bind the served Hash to the header before any signature is believed
	// over it (the same closure FetchGenesisValidatorPubKey needs).
	if err := snap.Block.Validate(); err != nil {
		return fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(trusted)
	if err != nil {
		return fmt.Errorf("trusted validator pubkey: %w", err)
	}
	if snap.Block.Header.ValidatorAddr != qcrypto.DeriveAddress(pub) {
		return fmt.Errorf("%w: head %d from %s was sealed by %s",
			ErrSnapshotNotAnchored, snap.Height, peerAddr, snap.Block.Header.ValidatorAddr)
	}
	if err := snap.Block.VerifySignature(trusted); err != nil {
		return fmt.Errorf("state snapshot head from %s: %w", peerAddr, err)
	}
	return nil
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
				// The pages this peer can serve belong to a different root, so
				// they are refused either way: pages from two roots must never
				// be spliced. What matters is which KIND of refusal this is,
				// because one of them can retire a staged transfer.
				//
				// Only a peer that has moved PAST the staged checkpoint is
				// evidence that it is gone. A peer serving a LOWER height has
				// not moved past anything — it is behind, and reporting that as
				// "the checkpoint is gone" would let a lagging peer retire a
				// transfer the rest of the set can still serve.
				//
				// A resumed transfer asks from a cursor, so the reply carries no
				// head to check: the height is all there is. It is therefore
				// treated as a CLAIM, not as proof, and acting on it is gated on
				// no peer having advanced the transfer this round (see
				// StateSync). When a head does come with it, it has to verify —
				// a peer that sends an unauthenticated one is refused outright
				// rather than believed about the checkpoint.
				if chunk.Height > store.Height() {
					if chunk.Block != nil {
						if err := authenticateSnapshotHead(peerAddr, trusted, chunk.Block, chunk.Height); err != nil {
							return err
						}
					}
					return fmt.Errorf("%w: staged head is %d, %s has moved on to %d",
						ErrSnapshotCheckpointUnavailable, store.Height(), peerAddr, chunk.Height)
				}
				return fmt.Errorf("state snapshot from %s: serves height %d, behind the staged transfer at %d",
					peerAddr, chunk.Height, store.Height())
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
//
// RESTART POLICY. Resuming is only possible while some peer still holds the
// checkpoint the transfer was pinned to. Once a session has expired, or the
// peer that served it is gone and the rest have advanced, that head is dead:
// the height guard refuses the mismatched pages (correctly — pages from two
// roots must never be spliced), and without a policy the client reopens the
// same dead checkpoint on every retry and never syncs. So a round in which
// at least one peer says the staged checkpoint is gone and NO peer could
// continue it abandons the staged transfer wholesale — head, cursor and
// leaves together, never a subset — and runs one more round from scratch at
// whatever checkpoint the network currently serves.
//
// A COMPLETED download is never abandoned: it is self-contained, root-checked
// evidence that the executor's recovery can still import, and the peers being
// past it does not make it any less valid.
func (s *Syncer) StateSync(importer func(*types.StateSnapshot) error) error {
	peers := s.pool.Addrs()
	if len(peers) == 0 {
		return errors.New("state sync: no peers to take a snapshot from")
	}

	dir := s.fetcher.cfg.SnapshotStagingDir
	store, err := s.openStateSyncStaging(dir)
	if err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		before := stagedProgress(store)
		imported, staleCheckpoint, refusals := s.stateSyncRound(peers, store, importer)
		if imported {
			return nil
		}
		if attempt == 0 && staleCheckpoint && store != nil && store.HasHead() && !store.Done() {
			// Somebody said the checkpoint is gone. Before acting on it: did
			// anybody get further this round? A transfer simply longer than one
			// round looks identical from here, and throwing away a download an
			// honest peer is still feeding is the worse mistake of the two — the
			// retry loop above reopens every couple of seconds, so it would not
			// be one wasted transfer but an unbounded series of them.
			if after := stagedProgress(store); after != before {
				log.Warn().Uint64("staged_height", store.Height()).
					Int64("leaf_bytes", after.leafBytes).
					Msg("state sync: a peer reports the staged checkpoint is gone, but the transfer advanced this round; keeping it")
				return fmt.Errorf("state sync: no peer served a usable snapshot: %s", strings.Join(refusals, "; "))
			}
			abandons := store.Abandons()
			if abandons >= maxSnapshotAbandons {
				return fmt.Errorf("state sync: the staged checkpoint at height %d has been abandoned %d times "+
					"without completing a transfer; refusing to discard it again (last refusals: %s)",
					store.Height(), abandons, strings.Join(refusals, "; "))
			}
			log.Warn().Uint64("staged_height", store.Height()).Str("dir", dir).Int("abandons", abandons+1).
				Msg("state sync: no peer can serve the staged checkpoint and none advanced it; abandoning and restarting")
			if err := store.Remove(); err != nil {
				return fmt.Errorf("state sync: abandon staged checkpoint: %w", err)
			}
			store, err = s.openStateSyncStaging(dir)
			if err != nil {
				return err
			}
			if store != nil && store.HasHead() {
				return errors.New("state sync: abandoned staging still carries a head; refusing to mix checkpoints")
			}
			// Carried across the wipe and persisted, so the count survives this
			// process. Without that, a restart resets it and the ceiling above
			// is no ceiling at all.
			if store != nil {
				if err := store.SetAbandons(abandons + 1); err != nil {
					return fmt.Errorf("state sync: record the abandoned checkpoint: %w", err)
				}
			}
			continue
		}
		return fmt.Errorf("state sync: no peer served a usable snapshot: %s", strings.Join(refusals, "; "))
	}
}

// maxSnapshotAbandons bounds how many times a staged checkpoint may be thrown
// away before the node stops trying and says so. Abandoning is recovery, not
// routine: past this the problem is not the checkpoint.
const maxSnapshotAbandons = 3

// stagedTransferProgress is the part of a staged download that moving forward
// changes. Comparing it across a round distinguishes "nobody can serve this
// checkpoint" from "this transfer is simply taking longer than one round".
type stagedTransferProgress struct {
	leafBytes int64
	afterKey  string
	done      bool
}

func stagedProgress(store *state.SnapshotDownload) stagedTransferProgress {
	if store == nil {
		return stagedTransferProgress{}
	}
	return stagedTransferProgress{
		leafBytes: store.LeafBytes(),
		afterKey:  string(store.AfterKey()),
		done:      store.Done(),
	}
}

// openStateSyncStaging opens the configured staging directory, discarding and
// recreating it if it cannot be read. nil when no directory is configured.
func (s *Syncer) openStateSyncStaging(dir string) (*state.SnapshotDownload, error) {
	if dir == "" {
		return nil, nil
	}
	opened, err := state.OpenSnapshotDownload(dir, s.fetcher.snapshotDiskLimit())
	if err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("state sync: discarding unreadable download staging")
		_ = state.RemoveSnapshotDownload(dir)
		opened, err = state.OpenSnapshotDownload(dir, s.fetcher.snapshotDiskLimit())
		if err != nil {
			return nil, fmt.Errorf("state sync: open download staging: %w", err)
		}
	}
	return opened, nil
}

// stateSyncRound tries every peer once. staleCheckpoint reports that a peer
// explicitly refused to continue the staged checkpoint because it has moved
// past it, which — with no peer having succeeded — is what "no peer can serve
// this checkpoint any more" looks like from here.
func (s *Syncer) stateSyncRound(peers []string, store *state.SnapshotDownload,
	importer func(*types.StateSnapshot) error) (imported, staleCheckpoint bool, refusals []string) {
	for _, peer := range peers {
		snap, err := s.fetchSnapshotFromPeer(peer, store)
		if err != nil {
			if errors.Is(err, ErrSnapshotCheckpointUnavailable) {
				staleCheckpoint = true
			}
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
			return true, staleCheckpoint, refusals
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
		return true, staleCheckpoint, refusals
	}
	return false, staleCheckpoint, refusals
}

func (s *Syncer) fetchSnapshotFromPeer(peer string, store *state.SnapshotDownload) (*types.StateSnapshot, error) {
	if store != nil && store.HasHead() {
		// A staged transfer is already pinned to a checkpoint. Continue it
		// rather than starting a coded fetch at whatever head this peer now
		// serves: switching would discard the pages already on disk, and the
		// restart policy above exists precisely so that discarding is a
		// decision taken deliberately and counted.
		if err := s.fetcher.fetchChunkedInto(peer, 0, store); err != nil {
			return nil, err
		}
		return nil, nil
	}
	// Negotiation: prefer the coded-object path when enabled — it is the
	// resilient transfer — and fall back to the plain paths on ANY failure
	// (peer does not serve coded objects, transfer error, unanchored head).
	// The coded path authenticates the transfer; the content is authenticated
	// identically by validateFetchedSnapshot, so the fallback never relaxes
	// trust, only transport resilience.
	if s.fetcher.cfg.CodedObjects {
		if snap, err := s.fetcher.FetchStateSnapshotCoded(peer); err == nil {
			log.Info().Str("peer", peer).Uint64("height", snap.Height).
				Msg("state sync: took the peer's coded snapshot")
			return snap, nil
		} else {
			log.Debug().Err(err).Str("peer", peer).
				Msg("state sync: coded snapshot unavailable, falling back to plain transfer")
		}
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
