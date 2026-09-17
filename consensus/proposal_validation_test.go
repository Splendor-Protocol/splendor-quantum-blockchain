package consensus

import (
	"errors"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A peer's proposal is judged BEFORE this node votes on it. These tests pin
// the refusals and the acceptance: a block whose signature does not verify
// and a block the validator says can never be applied each get no vote and
// leave the engine exactly where it was; a block that passes everything is
// judged once and voted on; a locally built block is not re-judged.
//
// Set of two, this node is set[0]. Height 5 round 0 selects set[1] as leader
// ((5+0)%2 == 1), so every proposal below is a peer's.

func peerProposalAt5(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, salt int64) *ProposalMsg {
	t.Helper()
	msg := proposalAt(t, set, keys, 5, 0, salt)
	if msg.Block.Header.ValidatorAddr != set[1].Address {
		t.Fatalf("leader schedule changed: expected set[1] to lead 5/0")
	}
	return msg
}

// Tampering with the block signature after the leader's own vote was made
// leaves the vote valid and the block forged. proposalRound alone accepted
// that: it checks the vote, not the block.
// C2: a halted apply must not be able to cast a new vote or adopt a proposal.
func TestHaltedApplyGetsNoVote(t *testing.T) {
	set, keys := testSet(t, 2)
	broadcast := make(chan *Vote, 4)
	progress := false
	eng := engineFor(t, set, keys, 0, BFTConfig{
		Broadcast:   func(v *Vote) { broadcast <- v },
		CanProgress: func() bool { return progress },
	})
	eng.SetHeight(5)

	msg := peerProposalAt5(t, set, keys, 1)
	eng.handleProposal(pendingProposalOf(msg))
	if eng.voteSet != nil || eng.proposal != nil || eng.hasVoted {
		t.Fatal("a proposal was adopted while CanProgress was false")
	}
	select {
	case <-broadcast:
		t.Fatal("a vote was broadcast while apply was halted")
	default:
	}

	progress = true
	eng.handleProposal(pendingProposalOf(msg))
	select {
	case <-broadcast:
	default:
		t.Fatal("the same proposal was not voted once progress resumed")
	}
}

func TestProposalWithInvalidBlockSignatureGetsNoVote(t *testing.T) {
	set, keys := testSet(t, 2)
	broadcast := make(chan *Vote, 4)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		Broadcast: func(v *Vote) { broadcast <- v },
	})
	eng.SetHeight(5)

	msg := peerProposalAt5(t, set, keys, 1)
	msg.Block.ValidatorSignature[0] ^= 0xff // the proposer's vote still verifies
	eng.handleProposal(pendingProposalOf(msg))

	if eng.voteSet != nil || eng.proposal != nil {
		t.Fatal("a proposal with an invalid block signature was accepted")
	}
	if eng.Height() != 5 || eng.Round() != 0 {
		t.Fatalf("engine moved to %d/%d on a rejected proposal", eng.Height(), eng.Round())
	}
	select {
	case v := <-broadcast:
		t.Fatalf("a vote was broadcast for a forged block: %+v", v)
	default:
	}
}

// The validator's verdict is final: a proposal it rejects gets no vote and
// changes nothing, however legitimate the leader and the signature are.
func TestProposalRejectedByTheValidatorGetsNoVote(t *testing.T) {
	set, keys := testSet(t, 2)
	broadcast := make(chan *Vote, 4)
	var judged []*types.Block
	eng := engineFor(t, set, keys, 0, BFTConfig{
		Broadcast: func(v *Vote) { broadcast <- v },
		ValidateProposal: func(b *types.Block) error {
			judged = append(judged, b)
			return errors.New("state root mismatch: computed 00, header ff")
		},
	})
	eng.SetHeight(5)

	msg := peerProposalAt5(t, set, keys, 1)
	eng.handleProposal(pendingProposalOf(msg))

	if len(judged) != 1 || judged[0].Hash != msg.Block.Hash {
		t.Fatalf("validator was consulted %d times, want once with the proposed block", len(judged))
	}
	if eng.voteSet != nil || eng.proposal != nil {
		t.Fatal("a proposal the validator rejected was accepted")
	}
	if eng.hasVoted {
		t.Fatal("a rejected proposal caused a vote")
	}
	if eng.Height() != 5 || eng.Round() != 0 {
		t.Fatalf("engine moved to %d/%d on a rejected proposal", eng.Height(), eng.Round())
	}
	select {
	case v := <-broadcast:
		t.Fatalf("a vote was broadcast for a block that would not apply: %+v", v)
	default:
	}
}

// And the acceptance: a proposal that verifies and that the validator passes
// is voted on, exactly once, after being judged.
func TestValidProposalIsJudgedOnceThenVoted(t *testing.T) {
	set, keys := testSet(t, 2)
	broadcast := make(chan *Vote, 4)
	judged := 0
	eng := engineFor(t, set, keys, 0, BFTConfig{
		Broadcast:        func(v *Vote) { broadcast <- v },
		ValidateProposal: func(*types.Block) error { judged++; return nil },
	})
	eng.SetHeight(5)

	msg := peerProposalAt5(t, set, keys, 1)
	eng.handleProposal(pendingProposalOf(msg))

	if judged != 1 {
		t.Fatalf("validator consulted %d times, want 1", judged)
	}
	// With two validators the proposer's bundled vote plus this node's vote
	// are the whole quorum, so acceptance shows as a commit: the engine has
	// moved on to height 6.
	if eng.Height() != 6 {
		t.Fatalf("engine at height %d after a valid proposal, want 6 (committed)", eng.Height())
	}
	select {
	case v := <-broadcast:
		if v.ValidatorAddr != set[0].Address || v.BlockHash != msg.Block.Hash {
			t.Fatalf("broadcast vote is not this node's vote for the proposal: %+v", v)
		}
	default:
		t.Fatal("no vote was broadcast for a valid proposal")
	}
}

// The local proposer built the block itself, under the same executor the
// validator would use; judging it again would only run the block twice.
func TestLocalProposalIsNotReJudged(t *testing.T) {
	set, keys := testSet(t, 2)
	judged := 0
	eng := engineFor(t, set, keys, 0, BFTConfig{
		ValidateProposal: func(*types.Block) error { judged++; return errors.New("must not be called") },
	})
	eng.SetHeight(8) // (8+0)%2 == 0: set[0], this node, leads
	msg := proposalAt(t, set, keys, 8, 0, 1)
	if msg.Block.Header.ValidatorAddr != set[0].Address {
		t.Fatal("leader schedule changed: expected set[0] to lead 8/0")
	}
	eng.handleProposal(&pendingProposal{block: msg.Block, local: true})
	if judged != 0 {
		t.Fatalf("the local proposal was re-judged %d times", judged)
	}
	if eng.voteSet == nil {
		t.Fatal("the local proposal was not accepted")
	}
}

// Vote commitment is bound to one HEIGHT across ALL rounds. After voting for
// a block at height H, a later round at the same height may only re-sign that
// same hash, never a different one. This prevents two-QC forks.
func TestVoteCommitmentPreventsVotingForDifferentBlockAtSameHeight(t *testing.T) {
	// Use 3 validators so we need 3 votes for quorum
	// This way, one validator's vote won't immediately commit
	set, keys := testSet(t, 3)
	broadcast := make(chan *Vote, 10)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		Broadcast: func(v *Vote) { broadcast <- v },
	})
	eng.SetHeight(5)

	// Build a proposal where set[1] is leader: (5+0)%3 = 2, so use height=4 round=0 where (4+0)%3=1
	msg := proposalAt(t, set, keys, 4, 0, 1)
	if msg.Block.Header.ValidatorAddr != set[1].Address {
		t.Skipf("leader schedule changed: expected set[1] at 4/0")
	}

	// Accept this proposal - we'll vote but won't reach quorum (need 3, have proposer's + ours = 2)
	eng.SetHeight(4)
	eng.handleProposal(pendingProposalOf(msg))

	// Verify we voted
	select {
	case v := <-broadcast:
		if v.BlockHash != msg.Block.Hash {
			t.Fatalf("voted for wrong block: got %x, want %x", v.BlockHash, msg.Block.Hash)
		}
	default:
		t.Fatal("no vote was broadcast")
	}

	if !eng.hasVoted {
		t.Fatal("vote not recorded")
	}

	// Should still be at height 4 (no quorum yet)
	if eng.Height() != 4 {
		t.Fatalf("height = %d, want 4 (no quorum yet)", eng.Height())
	}

	// Advance to round 1
	eng.handleTimeout()
	if eng.Round() != 1 {
		t.Fatalf("round = %d after timeout, want 1", eng.Round())
	}

	// Try to propose a DIFFERENT block at height 4 round 1: MUST be refused
	// At round 1: (4+1)%3 = 2, so set[2] is leader
	conflict := proposalAt(t, set, keys, 4, 1, 99)
	eng.handleProposal(pendingProposalOf(conflict))

	// Should NOT accept the conflicting block
	if eng.voteSet != nil && eng.proposal != nil && eng.proposal.Hash != msg.Block.Hash {
		t.Fatal("a conflicting block at height 4 round 1 was accepted after voting for a different block at height 4 round 0")
	}

	// Verify no new vote was broadcast for the conflict
	select {
	case v := <-broadcast:
		if v.BlockHash != msg.Block.Hash {
			t.Fatalf("a vote was broadcast for a conflicting block: %+v", v)
		}
	default:
		// Expected: no vote for conflicting block
	}
}
