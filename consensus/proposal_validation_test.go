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
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(5)

	first := peerProposalAt5(t, set, keys, 1)
	// Manually cast a vote for this block (simulating we voted in round 0)
	_, err := eng.castVote(first.Block, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eng.hasVoted {
		t.Fatal("vote not recorded")
	}

	// Same height, different block, different round: MUST be refused
	eng.handleTimeout()
	if eng.Round() != 1 {
		t.Fatalf("round = %d after timeout, want 1", eng.Round())
	}
	conflict := proposalAt(t, set, keys, 5, 1, 99)
	eng.handleProposal(&pendingProposal{block: conflict.Block, local: true})
	if eng.voteSet != nil {
		t.Fatal("a conflicting block at height 5 round 1 was accepted after voting for a different block at height 5 round 0")
	}

	// Re-signing the SAME block in a later round at the same height is allowed
	// Use the exact same block from round 0
	eng.handleProposal(&pendingProposal{block: first.Block, local: true})
	if eng.voteSet == nil {
		t.Fatal("re-voting for the same block hash in a later round was refused")
	}
}
