package consensus

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The in-memory lock stops a running engine from voting twice in one round.
// The journal is what stops the NEXT process from doing it: a validator that
// crashed after voting for A at (h, r) and restarted before h committed must
// not sign B at (h, r).

func TestVoteJournalRefusesConflictsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := j.Last(); ok {
		t.Fatal("fresh journal reports a last vote")
	}
	a, b := [32]byte{1}, [32]byte{2}
	if err := j.Record(7, 0, a); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	// Same decision again is fine; a different block in the same round is not.
	if err := j.Record(7, 0, a); err != nil {
		t.Fatalf("identical re-record refused: %v", err)
	}
	if err := j.Record(7, 0, b); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("conflicting vote in the same round: got %v, want ErrDoubleSign", err)
	}

	// "Restart": reopen from disk. The refusal must survive.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	h, r, hash, ok := j2.Last()
	if !ok || h != 7 || r != 0 || hash != a {
		t.Fatalf("reopened journal = (%d, %d, %x, %v), want (7, 0, %x, true)", h, r, hash[:2], ok, a[:2])
	}
	if err := j2.Record(7, 0, b); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("after restart, conflicting vote accepted: %v", err)
	}
	// Going backwards is refused too: a lower height, or a lower round.
	if err := j2.Record(6, 5, b); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("vote at a lower height accepted: %v", err)
	}
	if err := j2.Record(7, 1, b); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("different block at the next round accepted: %v", err)
	}
	if err := j2.Record(7, 1, a); err != nil {
		t.Fatalf("same block at the next round refused: %v", err)
	}
	if err := j2.Record(7, 0, a); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("vote at a lower round accepted: %v", err)
	}
	if err := j2.Record(8, 0, a); err != nil {
		t.Fatalf("next height refused: %v", err)
	}
	// The file on disk is the latest entry and nothing else.
	raw, _ := os.ReadFile(path)
	if len(raw) == 0 {
		t.Fatal("journal file empty after records")
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp file left behind after an atomic write")
	}
}

func TestVoteJournalAndProposalPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	set, keys := testSet(t, 1)
	block := proposalAt(t, set, keys, 4, 0, 3).Block
	if err := j.Record(4, 0, block.Hash); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveProposal(block); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal temp left behind")
	}
	if _, err := os.Stat(j.proposalPath() + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("proposal temp left behind")
	}

	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	h, r, hash, ok := j2.Last()
	if !ok || h != 4 || r != 0 || hash != block.Hash {
		t.Fatalf("reopened journal = (%d, %d, %x, %v)", h, r, hash[:2], ok)
	}
	got, ok := j2.LoadProposal()
	if !ok || got == nil || got.Hash != block.Hash {
		t.Fatalf("reopened proposal missing: ok=%v got=%v", ok, got)
	}
}

func TestVoteJournalRefusesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	if err := os.WriteFile(path, []byte(`{"height": "seven"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenVoteJournal(path); err == nil {
		t.Fatal("a corrupt journal was opened as if empty")
	}
}

// Through the engine: with the journal wired, a second engine on the same
// journal (the restarted process) gets no vote for a conflicting block, and
// therefore casts none.
func TestEngineDoesNotDoubleSignAcrossRestart(t *testing.T) {
	set, keys := testSet(t, 2)
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	first := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j1})
	blockA := proposalAt(t, set, keys, 5, 0, 1).Block
	if _, err := first.castVote(blockA, 0); err != nil {
		t.Fatalf("first vote: %v", err)
	}

	// The process dies and comes back with the same data dir.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	second := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j2})
	blockB := proposalAt(t, set, keys, 5, 0, 2).Block // different timestamp, different hash
	if blockB.Hash == blockA.Hash {
		t.Fatal("test blocks collide")
	}
	if _, err := second.castVote(blockB, 0); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("restarted engine signed a conflicting vote: %v", err)
	}
	// Re-signing the SAME block is allowed (the retry of one decision).
	if _, err := second.castVote(blockA, 0); err != nil {
		t.Fatalf("restarted engine refused to re-sign its own earlier vote: %v", err)
	}
	if _, err := second.castVote(blockB, 1); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("restarted engine signed a different block at the next round: %v", err)
	}
	if _, err := second.castVote(blockA, 1); err != nil {
		t.Fatalf("restarted engine refused to re-sign A at the next round: %v", err)
	}
}

// The durable prevote record closes the "restart before precommit" gap: a
// validator that prevoted for A at (h, r) but crashed before precommitting must
// not, after restart, prevote for a DIFFERENT block at the same (h, r). The
// value lock (journal.Record) would not stop it — a prevote never reached the
// precommit journal — so the prevote record is what preserves the per-view
// commitment.
func TestPrevotedBeforeRestartCannotPrevoteDifferentlyAtSameView(t *testing.T) {
	set, keys := testSet(t, 2)
	path := filepath.Join(t.TempDir(), "vote-journal.json")

	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	first := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j1})
	first.SetHeight(5)
	blockA := proposalAt(t, set, keys, 5, 0, 11).Block
	// Cast a prevote directly: this is what handleProposalTwoPhase does before
	// any precommit exists, and it is exactly the point at which a crash loses
	// the vote if nothing is durable.
	if _, err := first.castPrevote(blockA, 0); err != nil {
		t.Fatalf("first prevote: %v", err)
	}
	if _, _, _, ok := j1.Last(); ok {
		t.Fatal("a prevote alone must not create a value lock")
	}
	if h, r, hash, ok := j1.LastPrevote(); !ok || h != 5 || r != 0 || hash != blockA.Hash {
		t.Fatalf("prevote record = (%d,%d,%x,%v), want (5,0,%x,true)", h, r, hash[:2], ok, blockA.Hash[:2])
	}

	// Restart with the same journal.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	second := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j2})
	second.SetHeight(5)
	blockB := proposalAt(t, set, keys, 5, 0, 22).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("test blocks collide")
	}
	// The restarted engine must refuse a different prevote at the same view.
	if _, err := second.castPrevote(blockB, 0); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("restarted engine prevoted differently at the same view: %v", err)
	}
	// Re-prevoting the SAME block is an idempotent re-send.
	if _, err := second.castPrevote(blockA, 0); err != nil {
		t.Fatalf("restarted engine refused to re-prevote its own earlier vote: %v", err)
	}
}

func TestVoteJournalPrevotePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	a := [32]byte{3}
	if err := j.RecordPrevote(9, 1, a); err != nil {
		t.Fatal(err)
	}
	// Identical re-prevote is fine; a different hash at the same view is not.
	if err := j.RecordPrevote(9, 1, a); err != nil {
		t.Fatalf("identical re-prevote refused: %v", err)
	}
	if err := j.RecordPrevote(9, 1, [32]byte{4}); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("conflicting prevote in the same view: %v", err)
	}
	// A later round admits a different hash (a new view), unlike the value lock.
	if err := j.RecordPrevote(9, 2, [32]byte{4}); err != nil {
		t.Fatalf("new view prevote refused: %v", err)
	}

	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	h, r, hash, ok := j2.LastPrevote()
	if !ok || h != 9 || r != 2 || hash != [32]byte{4} {
		t.Fatalf("reopened prevote = (%d,%d,%x,%v)", h, r, hash[:2], ok)
	}
	// And the value lock itself is still empty (precommits write it, not prevotes).
	if _, _, _, ok := j2.Last(); ok {
		t.Fatal("prevote record leaked into the value lock")
	}
}

func TestVoteJournalClearAndClearIfHeight(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	a := [32]byte{9}
	if err := j.Record(54314, 1, a); err != nil {
		t.Fatal(err)
	}

	// Wrong height must refuse and leave the lock in place.
	if _, err := j.ClearIfHeight(999); err == nil {
		t.Fatal("ClearIfHeight at the wrong height succeeded")
	}
	if h, _, _, ok := j.Last(); !ok || h != 54314 {
		t.Fatalf("lock cleared on refused ClearIfHeight: ok=%v h=%d", ok, h)
	}
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if h, _, _, ok := j2.Last(); !ok || h != 54314 {
		t.Fatal("disk lock lost after refused ClearIfHeight")
	}

	backup, err := j2.ClearIfHeight(54314)
	if err != nil {
		t.Fatalf("ClearIfHeight: %v", err)
	}
	if backup == "" {
		t.Fatal("expected backup path")
	}
	if _, _, _, ok := j2.Last(); ok {
		t.Fatal("in-memory lock survived ClearIfHeight")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal file still present after clear")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	// After clear, a different hash at the same height is allowed (ops recovery).
	j3, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := [32]byte{8}
	if err := j3.Record(54314, 0, b); err != nil {
		t.Fatalf("post-clear vote refused: %v", err)
	}

	backup2, err := j3.Clear()
	if err != nil || backup2 == "" {
		t.Fatalf("Clear: backup=%q err=%v", backup2, err)
	}
	if _, err := j3.Clear(); err != nil {
		t.Fatalf("Clear on empty journal: %v", err)
	}
}
