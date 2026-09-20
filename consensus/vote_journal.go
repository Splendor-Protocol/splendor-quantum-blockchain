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
	last *journalEntry // nil until the first precommit
	// prevote is the last PREVOTE this validator signed, kept in a sidecar
	// file of its own. A prevote is not a lock — it binds nothing beyond its
	// own view — so it must not be mistaken for one by Last(), lockAt or the
	// crash-recovery round in SetHeight, all of which read `last`.
	prevote *prevoteEntry
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

// certEntry is the prevote quorum that justified a lock, kept so a restart —
// or a later leader asking this node what it is locked to — can still produce
// the proof rather than an unbacked claim.
type certEntry struct {
	Height    uint64  `json:"height"`
	Round     uint32  `json:"round"`
	BlockHash string  `json:"block_hash"`
	Votes     []*Vote `json:"votes"`
}

// OpenVoteJournal loads the journal at path, creating the parent directory.
// A missing file is an empty journal; a corrupt one is an error, because
// guessing is exactly what this file exists to avoid.
func OpenVoteJournal(path string) (*VoteJournal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("vote journal: %w", err)
	}
	j := &VoteJournal{path: path}
	if err := j.loadPrevote(); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vote journal: %w", err)
	}
	if len(b) == 0 {
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
	return j, nil
}

// loadPrevote reads the prevote sidecar. A missing file is an empty record; a
// corrupt one is an error, for the same reason the journal itself is: this
// file exists so the process does not have to guess what it already signed.
func (j *VoteJournal) loadPrevote() error {
	b, err := os.ReadFile(j.prevotePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vote journal prevote record: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	var e prevoteEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return fmt.Errorf("vote journal prevote record %s is corrupt: %w", j.prevotePath(), err)
	}
	if _, err := hex.DecodeString(e.BlockHash); err != nil || len(e.BlockHash) != 64 {
		return fmt.Errorf("vote journal prevote record %s is corrupt: bad block hash", j.prevotePath())
	}
	j.prevote = &e
	return nil
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

// LastPrevote returns the last recorded prevote, or ok=false when none.
func (j *VoteJournal) LastPrevote() (height uint64, round uint32, hash [32]byte, ok bool) {
	if j == nil {
		return 0, 0, [32]byte{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.prevote == nil {
		return 0, 0, [32]byte{}, false
	}
	b, _ := hex.DecodeString(j.prevote.BlockHash)
	copy(hash[:], b)
	return j.prevote.Height, j.prevote.Round, hash, true
}

// RecordPrevote admits a prevote for (height, round, blockHash) and persists
// it before the caller signs, or returns ErrDoubleSign.
//
// polRound is the round of the proof of lock the caller verified to justify
// leaving a lock, or -1 when it has none. The rules are the two Tendermint
// states a prevote can contradict:
//
//   - The durable LOCK (a precommit). A prevote for another value at that
//     height needs a proof of lock strictly later than the locked round —
//     "a correct process unlocks a value once it sees +2/3 prevotes for a
//     different value from the more recent round". The caller verifies the
//     certificate; this only re-checks the round relation, so a restart
//     cannot lose the constraint the in-memory lock was enforcing.
//   - The last prevote. One view owns one value: a second, different prevote
//     at a (height, round) already prevoted in is equivocation, and it is
//     exactly what a restart between prevote and precommit used to allow.
//     Re-signing the SAME value there is not a second decision.
//
// This is CometBFT's FilePV rule with Splendor's steps: persist the signed
// (height, round, step) before signing, and refuse any regression.
func (j *VoteJournal) RecordPrevote(height uint64, round uint32, blockHash [32]byte, polRound int32) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	want := hex.EncodeToString(blockHash[:])
	if l := j.last; l != nil {
		switch {
		case height < l.Height:
			return fmt.Errorf("%w: already precommitted at height %d, asked to prevote at %d",
				ErrDoubleSign, l.Height, height)
		case height == l.Height && l.BlockHash != want && !(polRound >= 0 && uint32(polRound) > l.Round):
			return fmt.Errorf("%w: prevote for %s contradicts the lock on %s at round %d with no later proof of lock",
				ErrDoubleSign, want[:8], l.BlockHash[:8], l.Round)
		}
	}
	if pv := j.prevote; pv != nil {
		switch {
		case height < pv.Height:
			return fmt.Errorf("%w: already prevoted at height %d, asked to prevote at %d",
				ErrDoubleSign, pv.Height, height)
		case height == pv.Height && round < pv.Round:
			return fmt.Errorf("%w: already prevoted at height %d round %d, asked to prevote at round %d",
				ErrDoubleSign, pv.Height, pv.Round, round)
		case height == pv.Height && round == pv.Round && pv.BlockHash != want:
			return fmt.Errorf("%w: already prevoted for %s at height %d round %d, asked to prevote for %s in the same view",
				ErrDoubleSign, pv.BlockHash[:8], height, round, want[:8])
		case height == pv.Height && round == pv.Round:
			return nil // the identical prevote, re-sent
		}
	}
	e := &prevoteEntry{Height: height, Round: round, BlockHash: want}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := persist.WriteFileAtomic(j.prevotePath(), raw, 0o600); err != nil {
		return fmt.Errorf("vote journal prevote record: %w", err)
	}
	j.prevote = e
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
		// Still remove a stray empty/corrupt-looking file if present.
		if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("vote journal clear: %w", err)
		}
		return "", nil
	}
	if requireHeight && j.last.Height != h {
		return "", fmt.Errorf("vote journal clear: journal is at height %d, not %d — refusing to clear", j.last.Height, h)
	}
	backup := fmt.Sprintf("%s.bak-%d", j.path, time.Now().Unix())
	if err := os.Rename(j.path, backup); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			j.last = nil
			j.prevote = nil
			j.removeSidecars()
			return "", nil
		}
		return "", fmt.Errorf("vote journal clear: backup: %w", err)
	}
	if err := persist.SyncDir(filepath.Dir(j.path)); err != nil {
		return "", fmt.Errorf("vote journal clear: sync dir: %w", err)
	}
	j.last = nil
	j.prevote = nil
	j.removeSidecars()
	return backup, nil
}

func (j *VoteJournal) proposalPath() string { return j.path + ".proposal" }
func (j *VoteJournal) prevotePath() string  { return j.path + ".prevote" }
func (j *VoteJournal) certPath() string     { return j.path + ".pol" }

// SaveCertificate persists the prevote quorum that justified a lock.
//
// The body alone is not enough to re-propose a locked value to a node locked
// on something else: that node needs the proof, and after a restart the votes
// that formed the quorum are gone from memory. Without this, a validator that
// legitimately holds the highest proof of lock cannot hand it to anyone, and
// the round-change selection has to skip it.
func (j *VoteJournal) SaveCertificate(height uint64, round uint32, blockHash [32]byte, votes []*Vote) error {
	if j == nil || len(votes) == 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	raw, err := json.Marshal(&certEntry{
		Height:    height,
		Round:     round,
		BlockHash: hex.EncodeToString(blockHash[:]),
		Votes:     votes,
	})
	if err != nil {
		return err
	}
	return persist.WriteFileAtomic(j.certPath(), raw, 0o600)
}

// LoadCertificate returns the persisted prevote quorum. The caller matches it
// against the lock it is trying to justify; nothing here assumes it is still
// the right one.
func (j *VoteJournal) LoadCertificate() (height uint64, round uint32, hash [32]byte, votes []*Vote, ok bool) {
	if j == nil {
		return 0, 0, [32]byte{}, nil, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	raw, err := os.ReadFile(j.certPath())
	if err != nil {
		return 0, 0, [32]byte{}, nil, false
	}
	var e certEntry
	if err := json.Unmarshal(raw, &e); err != nil || len(e.Votes) == 0 {
		return 0, 0, [32]byte{}, nil, false
	}
	b, err := hex.DecodeString(e.BlockHash)
	if err != nil || len(b) != 32 {
		return 0, 0, [32]byte{}, nil, false
	}
	copy(hash[:], b)
	return e.Height, e.Round, hash, e.Votes, true
}

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

// removeSidecars drops everything the journal keeps beside itself: the locked
// body, the certificate that justified the lock, and the prevote record. An
// operator clearing a stalled vote-lock is clearing the whole signing state,
// and leaving a prevote record behind would keep refusing votes the cleared
// lock no longer justifies refusing.
func (j *VoteJournal) removeSidecars() {
	for _, p := range []string{j.proposalPath(), j.certPath(), j.prevotePath()} {
		_ = os.Remove(p)
		_ = os.Remove(p + ".tmp")
	}
}
