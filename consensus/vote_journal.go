package consensus

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ErrDoubleSign is returned when a vote would contradict one this validator
// already signed: a different block at the same height (any round), or any
// vote for a height or round this validator has already moved past.
var ErrDoubleSign = errors.New("refusing to sign a vote that contradicts one already signed")

// VoteJournal is the durable record of the last vote this validator signed.
//
// The engine's in-memory lock protects a running process from voting twice in
// one round. It does not survive a restart: a validator that crashed after
// voting for block A at (h, r) and came back before the height committed
// would happily vote for block B at the same (h, r), and two conflicting
// certificates for one height is the thing BFT exists to prevent. The journal
// is written and fsynced (file and parent directory) BEFORE the vote is
// signed, so whatever the process finds on disk after a restart is at least
// as far along as anything it ever put on the wire.
type VoteJournal struct {
	mu   sync.Mutex
	path string
	last *journalEntry // nil until the first vote
	// lastPrevote records the most recent PREVOTE this validator signed,
	// separately from the value lock (last). A prevote at a view must not
	// survive that view's round advancing; a value lock survives the height.
	// Keeping them apart lets a restart resume at the round after a prevote
	// that never reached a precommit, without turning a prevote into the
	// permanent per-height value commitment.
	lastPrevote *prevoteEntry
	// cert is the persisted POL (proof-of-lock) certificate: the prevote
	// quorum this node witnessed that justifies its lock. It is a separate
	// file because a POL is evidence a future leader verifies vote-by-vote,
	// not a value commitment this node itself signed.
	cert *certEntry
}

type journalEntry struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	BlockHash string `json:"block_hash"`
	POLRound  uint32 `json:"pol_round,omitempty"`
}

type prevoteEntry struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	BlockHash string `json:"block_hash"`
}

// certEntry is the on-disk form of a POL certificate: the prevote quorum that
// justifies a locked value, plus the height/round/hash it locks. Votes are
// ML-DSA-65 prevotes, each re-verified against the live validator set on load.
type certEntry struct {
	Height    uint64  `json:"height"`
	Round     uint32  `json:"round"`
	BlockHash string  `json:"block_hash"`
	Votes     []*Vote `json:"votes"`
}

// maxPOLVotes bounds the number of prevotes a persisted certificate may hold,
// and maxPOLFileBytes the on-disk size read back. A certificate cannot exceed
// a 2/3+1 quorum of the validator set, so either bound being exceeded is a
// corrupt file, not a certificate — and both are read from disk, so both need
// a ceiling.
const (
	maxPOLVotes     = 1024
	maxPOLFileBytes = 8 << 20 // 8 MiB: generous for 1024 ML-DSA prevotes (~3.4 MB)
)

// OpenVoteJournal loads the journal at path, creating the parent directory.
// A missing file is an empty journal; a corrupt one is an error, because
// guessing is exactly what this file exists to avoid.
func OpenVoteJournal(path string) (*VoteJournal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("vote journal: %w", err)
	}
	j := &VoteJournal{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := j.loadSidecars(); err != nil {
			return nil, err
		}
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vote journal: %w", err)
	}
	if len(b) == 0 {
		if err := j.loadSidecars(); err != nil {
			return nil, err
		}
		return j, nil
	}
	var e journalEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("vote journal %s is corrupt: %w", path, err)
	}
	if _, err := hex.DecodeString(e.BlockHash); err != nil || len(e.BlockHash) != 64 {
		return nil, fmt.Errorf("vote journal %s is corrupt: bad block hash", path)
	}
	j.last = &e
	if err := j.loadSidecars(); err != nil {
		return nil, err
	}
	return j, nil
}

// Last returns the last recorded vote, or ok=false when none.
func (j *VoteJournal) Last() (height uint64, round uint32, hash [32]byte, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == nil {
		return 0, 0, [32]byte{}, false
	}
	b, _ := hex.DecodeString(j.last.BlockHash)
	copy(hash[:], b)
	return j.last.Height, j.last.Round, hash, true
}

// LastPrevote returns the most recently recorded PREVOTE, or ok=false.
// A prevote is a per-view (height, round) commitment: it is what must survive
// a restart only long enough to resume past the view it already voted in.
func (j *VoteJournal) LastPrevote() (height uint64, round uint32, hash [32]byte, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lastPrevote == nil {
		return 0, 0, [32]byte{}, false
	}
	b, _ := hex.DecodeString(j.lastPrevote.BlockHash)
	copy(hash[:], b)
	return j.lastPrevote.Height, j.lastPrevote.Round, hash, true
}

// RecordPrevote admits and persists a prevote for (height, round, blockHash).
// It guards per-view equivocation: a different hash at the SAME (height, round)
// is refused. A different hash at a later round is a round change's new view
// and is allowed — the value lock (Record/RecordWithPOL) decides whether the
// height can change value, not the prevote.
func (j *VoteJournal) RecordPrevote(height uint64, round uint32, blockHash [32]byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	want := hex.EncodeToString(blockHash[:])
	if p := j.lastPrevote; p != nil {
		switch {
		case height < p.Height:
			return fmt.Errorf("%w: already prevoted at height %d, asked to prevote at %d", ErrDoubleSign, p.Height, height)
		case height == p.Height && round < p.Round:
			return fmt.Errorf("%w: already prevoted at height %d round %d, asked to prevote at round %d", ErrDoubleSign, p.Height, p.Round, round)
		case height == p.Height && round == p.Round && p.BlockHash != want:
			return fmt.Errorf("%w: already prevoted %s at height %d round %d, asked to prevote %s", ErrDoubleSign, p.BlockHash[:8], height, round, want[:8])
		case height == p.Height && round == p.Round:
			return nil // identical prevote, nothing new to record
		}
	}
	e := &prevoteEntry{Height: height, Round: round, BlockHash: want}
	if err := j.writePrevote(e); err != nil {
		return fmt.Errorf("vote journal prevote: %w", err)
	}
	j.lastPrevote = e
	return nil
}

// Record admits a vote for (height, round, blockHash) and persists it, or
// returns ErrDoubleSign. Re-recording the identical vote is allowed (a retry
// of the same decision is not a second decision).
func (j *VoteJournal) Record(height uint64, round uint32, blockHash [32]byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	want := hex.EncodeToString(blockHash[:])
	if l := j.last; l != nil {
		switch {
		case height < l.Height:
			return fmt.Errorf("%w: already voted at height %d, asked to vote at %d", ErrDoubleSign, l.Height, height)
		case height == l.Height && l.BlockHash != want:
			return fmt.Errorf("%w: already voted for %s at height %d, asked to vote for %s at round %d",
				ErrDoubleSign, l.BlockHash[:8], height, want[:8], round)
		case height == l.Height && round < l.Round:
			return fmt.Errorf("%w: already voted at height %d round %d, asked to vote at round %d", ErrDoubleSign, l.Height, l.Round, round)
		case height == l.Height && round == l.Round:
			return nil // identical vote, nothing new to record
		}
	}
	e := &journalEntry{Height: height, Round: round, BlockHash: want}
	if err := j.write(e); err != nil {
		return fmt.Errorf("vote journal: %w", err)
	}
	j.last = e
	return nil
}

// RecordWithPOL records a precommit. A different hash at the same height is
// admitted only when polRound is strictly later than the last locked round
// (an authenticated prevote QC the caller already verified).
func (j *VoteJournal) RecordWithPOL(height uint64, round uint32, blockHash [32]byte, polRound uint32) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	want := hex.EncodeToString(blockHash[:])
	if l := j.last; l != nil {
		switch {
		case height < l.Height:
			return fmt.Errorf("%w: already voted at height %d, asked to vote at %d", ErrDoubleSign, l.Height, height)
		case height == l.Height && l.BlockHash != want && polRound <= l.Round:
			return fmt.Errorf("%w: conflicting precommit without a later POL", ErrDoubleSign)
		case height == l.Height && l.BlockHash == want && round < l.Round:
			return fmt.Errorf("%w: already precommitted at height %d round %d, asked to vote at round %d", ErrDoubleSign, l.Height, l.Round, round)
		case height == l.Height && l.BlockHash == want && round == l.Round:
			return nil
		}
	}
	e := &journalEntry{Height: height, Round: round, BlockHash: want, POLRound: polRound}
	if err := j.write(e); err != nil {
		return fmt.Errorf("vote journal: %w", err)
	}
	j.last = e
	return nil
}

// write replaces the journal atomically: temp file, fsync, rename, then
// fsync the parent directory so the new directory entry survives power loss.
func (j *VoteJournal) write(e *journalEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return persist.WriteFileAtomic(j.path, b, 0o600)
}

func (j *VoteJournal) prevotePath() string { return j.path + ".prevote" }

func (j *VoteJournal) polPath() string { return j.path + ".pol" }

// writePrevote persists the per-view prevote alongside the journal. It is a
// second file, not the journal entry: a prevote is not the value lock, and
// backing it into journalEntry would let a crash-recovery path confuse a
// view-level prevote for a height-level precommit lock.
func (j *VoteJournal) writePrevote(e *prevoteEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return persist.WriteFileAtomic(j.prevotePath(), b, 0o600)
}

// loadSidecars reads the per-view prevote and the POL certificate back. It is
// called wherever the journal is opened, so a restart recovers both the
// per-view commitment and the prevote quorum that justifies a lock.
func (j *VoteJournal) loadSidecars() error {
	if err := j.loadPrevote(); err != nil {
		return err
	}
	return j.loadPOL()
}

// SavePOL persists the prevote quorum (POL) that justifies a lock, so a
// restart can re-attach it to a round-change instead of losing the lock — and
// the ability to unlock peers — with the in-memory copy. The votes are an
// already-verified quorum; the caller guarantees they cover (height, round,
// hash). It is a third file, not a journalEntry field, for the same reason the
// prevote is: a POL is evidence a future leader verifies vote-by-vote, not a
// value commitment this node itself signed.
func (j *VoteJournal) SavePOL(height uint64, round uint32, hash [32]byte, votes []*Vote) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(votes) == 0 {
		return fmt.Errorf("vote journal: refusing to persist an empty POL")
	}
	e := &certEntry{Height: height, Round: round, BlockHash: hex.EncodeToString(hash[:]), Votes: votes}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := persist.WriteFileAtomic(j.polPath(), b, 0o600); err != nil {
		return err
	}
	j.cert = e
	return nil
}

// LoadPOL returns the persisted POL certificate, or ok=false. The votes are
// returned exactly as persisted; the caller must re-verify them against the
// live validator set, because the journal holds no public keys.
func (j *VoteJournal) LoadPOL() (height uint64, round uint32, hash [32]byte, votes []*Vote, ok bool) {
	if j == nil {
		return 0, 0, [32]byte{}, nil, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cert == nil {
		return 0, 0, [32]byte{}, nil, false
	}
	b, _ := hex.DecodeString(j.cert.BlockHash)
	var h [32]byte
	copy(h[:], b)
	return j.cert.Height, j.cert.Round, h, j.cert.Votes, true
}

// loadPOL reads the POL certificate back. A missing or empty file is a no-op;
// a corrupt one is an error. It is structural validation only (byte-limit,
// hash format, vote count, and that every vote is a prevote for this
// certificate's height/round/hash): signature verification happens in the
// engine, which holds the validator keys.
func (j *VoteJournal) loadPOL() error {
	b, err := os.ReadFile(j.polPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vote journal pol: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	if len(b) > maxPOLFileBytes {
		return fmt.Errorf("vote journal pol %s is corrupt: %d bytes", j.polPath(), len(b))
	}
	var e certEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return fmt.Errorf("vote journal pol %s is corrupt: %w", j.polPath(), err)
	}
	if _, err := hex.DecodeString(e.BlockHash); err != nil || len(e.BlockHash) != 64 {
		return fmt.Errorf("vote journal pol %s is corrupt: bad block hash", j.polPath())
	}
	if len(e.Votes) == 0 || len(e.Votes) > maxPOLVotes {
		return fmt.Errorf("vote journal pol %s is corrupt: %d votes", j.polPath(), len(e.Votes))
	}
	raw, _ := hex.DecodeString(e.BlockHash)
	var h [32]byte
	copy(h[:], raw)
	for _, v := range e.Votes {
		if v == nil || v.Kind != types.VoteKindPrevote ||
			v.Height != e.Height || v.Round != e.Round || v.BlockHash != h {
			return fmt.Errorf("vote journal pol %s is corrupt: a vote does not match the certificate", j.polPath())
		}
	}
	j.cert = &e
	return nil
}

// loadPrevote reads the prevote record back. A missing or empty file is a
// no-op; a corrupt one is an error, for the same reason the journal itself is.
func (j *VoteJournal) loadPrevote() error {
	b, err := os.ReadFile(j.prevotePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vote journal prevote: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	var e prevoteEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return fmt.Errorf("vote journal prevote %s is corrupt: %w", j.prevotePath(), err)
	}
	if _, err := hex.DecodeString(e.BlockHash); err != nil || len(e.BlockHash) != 64 {
		return fmt.Errorf("vote journal prevote %s is corrupt: bad block hash", j.prevotePath())
	}
	j.lastPrevote = &e
	return nil
}

// Clear removes the durable vote lock, backing up the file first.
//
// Operators use this after a split vote-lock stall once the peer mesh is
// healthy — never as a substitute for fixing connectivity. Clearing while a
// prior vote may still form a QC with other nodes can produce conflicting
// certificates; coordinate the clear across every conflicting validator and
// bounce them together.
//
// backupPath is the renamed journal (empty when there was nothing on disk).
func (j *VoteJournal) Clear() (backupPath string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.clearLocked(0, false)
}

// ClearIfHeight clears only when the journal's last vote is at height h.
// If the journal is empty, it is a no-op. If it holds a different height,
// it returns an error and leaves the file untouched — so an operator who
// mistypes the stalled height cannot wipe a live lock by accident.
func (j *VoteJournal) ClearIfHeight(h uint64) (backupPath string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.clearLocked(h, true)
}

func (j *VoteJournal) clearLocked(h uint64, requireHeight bool) (string, error) {
	if j.last == nil {
		// Still remove a stray empty/corrupt-looking file if present, plus the
		// sidecars (prevote, POL) so a clear leaves no lock state behind.
		if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("vote journal clear: %w", err)
		}
		j.clearPrevoteLocked()
		j.clearPOLLocked()
		return "", nil
	}
	if requireHeight && j.last.Height != h {
		return "", fmt.Errorf("vote journal clear: journal is at height %d, not %d — refusing to clear", j.last.Height, h)
	}
	backup := fmt.Sprintf("%s.bak-%d", j.path, time.Now().Unix())
	if err := os.Rename(j.path, backup); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			j.last = nil
			j.removeProposalFile()
			return "", nil
		}
		return "", fmt.Errorf("vote journal clear: backup: %w", err)
	}
	if err := persist.SyncDir(filepath.Dir(j.path)); err != nil {
		return "", fmt.Errorf("vote journal clear: sync dir: %w", err)
	}
	j.last = nil
	j.removeProposalFile()
	return backup, nil
}

func (j *VoteJournal) proposalPath() string { return j.path + ".proposal" }

// SaveProposal persists the block body this validator locked onto, so a
// restart (or a round timeout that dropped the in-memory proposal) can
// rebroadcast the SAME hash instead of minting a conflicting one.
func (j *VoteJournal) SaveProposal(b *types.Block) error {
	if j == nil || b == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == nil || j.last.BlockHash != hex.EncodeToString(b.Hash[:]) {
		return fmt.Errorf("vote journal: refusing to save proposal that does not match the locked hash")
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return persist.WriteFileAtomic(j.proposalPath(), raw, 0o600)
}

// LoadProposal returns the persisted locked block when it matches Last().
func (j *VoteJournal) LoadProposal() (*types.Block, bool) {
	if j == nil {
		return nil, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == nil {
		return nil, false
	}
	raw, err := os.ReadFile(j.proposalPath())
	if err != nil {
		return nil, false
	}
	var b types.Block
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, false
	}
	if hex.EncodeToString(b.Hash[:]) != j.last.BlockHash {
		return nil, false
	}
	return &b, true
}

func (j *VoteJournal) removeProposalFile() {
	_ = os.Remove(j.proposalPath())
	_ = os.Remove(j.proposalPath() + ".tmp")
	j.clearPrevoteLocked()
	j.clearPOLLocked()
}

// clearPrevoteLocked drops the in-memory and on-disk prevote record. Caller
// holds j.mu.
func (j *VoteJournal) clearPrevoteLocked() {
	_ = os.Remove(j.prevotePath())
	_ = os.Remove(j.prevotePath() + ".tmp")
	j.lastPrevote = nil
}

// clearPOLLocked drops the in-memory and on-disk POL certificate. Caller holds
// j.mu.
func (j *VoteJournal) clearPOLLocked() {
	_ = os.Remove(j.polPath())
	_ = os.Remove(j.polPath() + ".tmp")
	j.cert = nil
}
