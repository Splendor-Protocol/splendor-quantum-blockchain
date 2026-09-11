package consensus

import (
	"path/filepath"
	"testing"
)

// After the leader vote-locks height h, a round timeout must not cause it to
// mint a different hash: rebroadcast the locked proposal (same hash, new round).
func TestLeaderRebroadcastsVoteLockedProposalAfterTimeout(t *testing.T) {
	set, keys := testSet(t, 3) // quorum 3 — own vote alone does not commit
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var broadcast []*ProposalMsg
	eng := engineFor(t, set, keys, 0, BFTConfig{
		VoteJournal: j,
		BroadcastProposal: func(p *ProposalMsg) {
			broadcast = append(broadcast, p)
		},
	})
	// Height 3 round 0: (3+0)%3 == 0 → set[0] leads.
	eng.SetHeight(3)
	first := proposalAt(t, set, keys, 3, 0, 11).Block
	eng.handleProposal(&pendingProposal{block: first, local: true})
	if len(broadcast) != 1 || broadcast[0].Block.Hash != first.Hash {
		t.Fatalf("first broadcast missing locked hash")
	}
	if eng.LockedProposal() == nil || eng.LockedProposal().Hash != first.Hash {
		t.Fatal("locked proposal not retained after vote")
	}

	eng.handleTimeout()
	if eng.Round() != 1 {
		t.Fatalf("round = %d, want 1", eng.Round())
	}
	if eng.LockedProposal() == nil {
		t.Fatal("timeout cleared locked proposal body")
	}

	// Round 1 leader is set[1]; bump to round 3 so set[0] leads again: (3+3)%3=0.
	eng.handleTimeout() // round 2
	eng.handleTimeout() // round 3
	if !eng.IsLeader(3, eng.Round()) {
		t.Fatalf("set[0] should lead at round %d", eng.Round())
	}

	n := len(broadcast)
	locked := eng.LockedProposal()
	eng.handleProposal(&pendingProposal{block: locked, local: true})
	if len(broadcast) != n+1 {
		t.Fatalf("rebroadcast count = %d, want %d", len(broadcast), n+1)
	}
	if broadcast[n].Block.Hash != first.Hash {
		t.Fatalf("rebroadcast minted %x, want locked %x", broadcast[n].Block.Hash[:4], first.Hash[:4])
	}
	_, jr, jhash, ok := j.Last()
	if !ok || jhash != first.Hash {
		t.Fatal("journal hash drifted")
	}
	if jr != eng.Round() {
		t.Fatalf("journal round = %d, want engine round %d", jr, eng.Round())
	}
}

func TestProposeHoldsWhenVoteLockedWithoutBody(t *testing.T) {
	set, keys := testSet(t, 1)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	first := proposalAt(t, set, keys, 5, 0, 1).Block
	if err := j.Record(5, 0, first.Hash); err != nil {
		t.Fatal(err)
	}
	eng := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j})
	eng.SetHeight(5)
	if eng.LockedProposal() != nil {
		t.Fatal("expected no locked body")
	}
	if !eng.VoteLockedAt(5) {
		t.Fatal("expected journal lock")
	}
}

func TestLockedProposalSurvivesJournalReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	set, keys := testSet(t, 1)
	block := proposalAt(t, set, keys, 9, 0, 7).Block
	if err := j1.Record(9, 0, block.Hash); err != nil {
		t.Fatal(err)
	}
	if err := j1.SaveProposal(block); err != nil {
		t.Fatal(err)
	}
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	eng := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j2})
	eng.SetHeight(9)
	got := eng.LockedProposal()
	if got == nil || got.Hash != block.Hash {
		t.Fatalf("restored locked proposal missing")
	}
}
