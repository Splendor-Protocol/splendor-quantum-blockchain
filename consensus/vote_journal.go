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
}

type journalEntry struct {
	Height    uint64 `json:"height"`
	Round     uint32 `json:"round"`
	BlockHash string `json:"block_hash"`
}

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
}
