package consensus

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The previous process voted at (h, 0) and died before h committed. The
// journal refuses any other block at (h, 0), and a fresh proposal always has
// a fresh hash, so the restarted engine must resume at round 1 — not sit at
// round 0 proposing blocks it can never vote for.
func TestRestartResumesAtTheRoundAfterTheJournaledVote(t *testing.T) {
	set, keys := testSet(t, 1)
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

	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	second := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j2})
	second.SetHeight(5)
	if r := second.Round(); r != 1 {
		t.Fatalf("restarted engine at height 5 is on round %d, want 1 (round 0 is spent in the journal)", r)
	}
	blockB := proposalAt(t, set, keys, 5, 1, 2).Block
	if _, err := second.castVote(blockB, second.Round()); err != nil {
		t.Fatalf("vote at the resumed round refused: %v", err)
	}
	// A later height starts at round 0 as before.
	second.SetHeight(6)
	if r := second.Round(); r != 0 {
		t.Fatalf("height 6 started at round %d, want 0", r)
	}
}

// End to end through the engine loop: a sole validator that restarts with a
// journaled vote at the height it resumes must still seal that height.
func TestSoleValidatorSealsAfterRestartMidHeight(t *testing.T) {
	set, keys := testSet(t, 1)
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j1.Record(1, 0, proposalAt(t, set, keys, 1, 0, 1).Block.Hash); err != nil {
		t.Fatal(err)
	}

	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan *types.Block, 1)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		VoteJournal: j2,
		OnCommit: func(b *types.Block) error {
			committed <- b
			return nil
		},
	})
	eng.SetHeight(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	// The node's own fresh proposal for the height it resumed at.
	b := proposalAt(t, set, keys, 1, eng.Round(), 2).Block
	eng.ProposeBlock(b)
	select {
	case got := <-committed:
		if got.Hash != b.Hash {
			t.Fatalf("committed %x, want %x", got.Hash[:4], b.Hash[:4])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the restarted sole validator never sealed the height it resumed at")
	}
}

// Defence in depth: if the engine somehow proposes at a round the journal has
// already spent, the refusal must advance the round and trigger the view
// change, not leave the engine re-proposing at the same round for ever.
func TestRefusedOwnVoteAdvancesTheRound(t *testing.T) {
	set, keys := testSet(t, 1)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	viewChanged := make(chan [2]uint64, 1)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		VoteJournal:  j,
		OnViewChange: func(h uint64, r uint32) { viewChanged <- [2]uint64{h, uint64(r)} },
	})
	eng.SetHeight(5) // journal empty here, so round 0
	// Now the journal learns of a vote at (5, 0) behind the engine's back.
	if err := j.Record(5, 0, proposalAt(t, set, keys, 5, 0, 1).Block.Hash); err != nil {
		t.Fatal(err)
	}
	blockB := proposalAt(t, set, keys, 5, 0, 2).Block
	eng.handleProposal(&pendingProposal{block: blockB, local: true})
	if r := eng.Round(); r != 1 {
		t.Fatalf("after a refused own vote the engine is on round %d, want 1", r)
	}
	select {
	case hr := <-viewChanged:
		if hr != [2]uint64{5, 1} {
			t.Fatalf("view change for %v, want height 5 round 1", hr)
		}
	case <-time.After(time.Second):
		t.Fatal("no view change after the refused own vote")
	}
}
