package consensus

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The deadlock this file pins down, as observed on devnet at heights 5584 and
// 5611 with bft_rounds_v1 active.
//
// MultiValidator.propose runs on every block-interval tick. It rebroadcasts a
// body only once this node is vote-locked to it (LockedProposal != nil, which
// needs a prevote QC first). At a contended height that QC does not form
// inside one interval, so the next tick mints a FRESH block — new timestamp,
// new tx set, new hash — and hands it to the engine at the SAME (height,
// round).
//
// handleProposalTwoPhase used to adopt it: it replaced e.voteSet and
// e.precommits and signed a second prevote. The leader has now authored two
// different prevotes at one (height, round). Every peer's seenVotes key is
// validator|height|round|kind — exactly that granularity — so the second one
// is logged "equivocation — conflicting votes dropped" and the key stays
// poisoned: every later prevote from that validator in that round is dropped
// too. Peers shown the second body did the same thing to themselves. A few
// ticks in, every member has equivocated at that view and no quorum can form
// there again.
//
// The round timer did not rescue it: syncRoundTimer renews the deadline on any
// accepted proposal at the same view, and a 2s tick renewed the 3s deadline
// before it could fire — hence "no round timeout or view change at all" in the
// four-validator capture.
//
// The rule asserted here: a view owns one value. Once this node has adopted a
// proposal at (height, round), a different body at that view is refused, and
// an identical one is idempotent — no new signature, no vote set discarded,
// and no renewal of the round deadline.

// distinctPrevoteHashes reports the distinct block hashes addr signed as
// prevotes at (height, round) across a captured broadcast stream.
func distinctPrevoteHashes(votes []*Vote, addr qcrypto.Address, height uint64, round uint32) [][32]byte {
	seen := map[[32]byte]bool{}
	var out [][32]byte
	for _, v := range votes {
		if v.Kind != types.VoteKindPrevote || v.Height != height || v.Round != round {
			continue
		}
		if v.ValidatorAddr != addr || seen[v.BlockHash] {
			continue
		}
		seen[v.BlockHash] = true
		out = append(out, v.BlockHash)
	}
	return out
}

// TestTwoPhaseLeaderRemintAtTheSameRoundDoesNotDeadlock is the regression for
// the devnet freeze. Four honest validators, quorum 3, every message
// delivered, nobody down: the only disturbance is the leader's second tick
// firing before the prevotes it is waiting for come back.
func TestTwoPhaseLeaderRemintAtTheSameRoundDoesNotDeadlock(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes, allVotes []*Vote
	commits := make([]*types.Block, 4)
	engs := make([]*BFTEngine, 4)
	for i := 0; i < 4; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit: func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast: func(v *Vote) {
				cp := *v
				votes = append(votes, &cp)
				allVotes = append(allVotes, &cp)
			},
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
		})
		engs[i].SetHeight(4) // (4+0)%4 == 0 → set[0] leads round 0
	}
	if !engs[0].IsLeader(4, 0) {
		t.Fatal("set[0] must lead height 4 round 0")
	}

	// Tick 1: the leader mints A and everyone prevotes it.
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader refused its own first proposal")
	}
	propA := lastProp
	if propA == nil || propA.Envelope == nil {
		t.Fatal("leader broadcast no envelope for A")
	}
	for i, e := range engs[1:] {
		if !e.handleProposal(pendingProposalOf(propA)) {
			t.Fatalf("follower %d refused the round-0 proposal", i+1)
		}
	}

	// Tick 2, before those prevotes are counted. No prevote QC has formed, so
	// MultiValidator.propose finds no locked body and mints a fresh one.
	if engs[0].LockedProposal() != nil {
		t.Fatal("precondition: the leader must not be vote-locked yet, " +
			"or propose() would rebroadcast instead of minting")
	}
	blockB := proposalAt(t, set, keys, 4, 0, 22).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("A and B collide")
	}
	adoptedB := engs[0].handleProposal(&pendingProposal{block: blockB, local: true})

	// Whatever the leader did with B is what its peers are shown next.
	next := propA
	if adoptedB {
		next = lastProp
	}
	for _, e := range engs[1:] {
		e.handleProposal(pendingProposalOf(next))
	}

	// Everything on the wire now reaches everyone, in the order it was sent.
	for i := 0; i < 2; i++ {
		deliverVotes(t, engs, &votes, types.VoteKindPrevote)
		deliverVotes(t, engs, &votes, types.VoteKindPrecommit)
	}

	// Liveness: four honest validators with every message delivered commit.
	var committed [32]byte
	for i := range engs {
		if commits[i] == nil {
			t.Fatalf("engine %d never committed: a same-round re-mint deadlocked "+
				"four honest validators with nothing lost and nobody down", i)
		}
		if i == 0 {
			committed = commits[i].Hash
		} else if commits[i].Hash != committed {
			t.Fatalf("conflicting finality: %x vs %x", committed[:4], commits[i].Hash[:4])
		}
	}

	// Safety: an honest node must never author two different prevotes at one
	// view. That self-equivocation is what poisons seenVotes at every peer and
	// takes the voter out of the quorum for the rest of the round.
	for i := range engs {
		if h := distinctPrevoteHashes(allVotes, set[i].Address, 4, 0); len(h) > 1 {
			t.Fatalf("validator %d signed %d different prevotes at height 4 round 0 "+
				"(%x, %x): every peer drops it as an equivocator from here on",
				i, len(h), h[0][:4], h[1][:4])
		}
	}
}

// TestTwoPhaseIdenticalRebroadcastIsIdempotent covers the other half of the
// live capture: a leader that IS vote-locked rebroadcasts the same body every
// tick ("rebroadcasting vote-locked proposal hash=3ca914a7 height=5611
// round=1"). A receiver must not treat that as a new proposal — no fresh
// signature, no vote set thrown away, and no renewal of the round deadline, or
// the view change that would break the tie never fires.
func TestTwoPhaseIdenticalRebroadcastIsIdempotent(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes []*Vote
	engs := make([]*BFTEngine, 4)
	for i := 0; i < 4; i++ {
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			Broadcast:         func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
		})
		engs[i].SetHeight(4)
	}
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader refused its own proposal")
	}
	propA := lastProp
	follower := engs[1]
	if !follower.handleProposal(pendingProposalOf(propA)) {
		t.Fatal("follower refused the proposal")
	}
	first := follower.voteSet
	before := first.VoteCount()
	if before < 2 {
		t.Fatalf("vote set holds %d prevotes, want the leader's and its own", before)
	}
	ownPrevote := first.Votes()
	if len(ownPrevote) != before {
		t.Fatal("vote set snapshot disagrees with its own count")
	}

	rt := newRoundTimer()
	defer rt.disarm()
	follower.syncRoundTimer(rt, true)
	if !rt.armed {
		t.Fatal("an open proposal left the round timer disarmed")
	}
	deadline := rt.deadline

	// The leader's periodic rebroadcast of the very same body.
	renew := follower.handleProposal(pendingProposalOf(propA))
	if renew {
		t.Fatal("an identical rebroadcast was reported as a new proposal, so it renews " +
			"the round deadline and the view change can never fire")
	}
	if follower.voteSet != first {
		t.Fatal("an identical rebroadcast replaced the vote set, discarding the prevotes already collected")
	}
	if got := follower.voteSet.VoteCount(); got != before {
		t.Fatalf("vote set went from %d prevotes to %d on a duplicate proposal", before, got)
	}
	follower.syncRoundTimer(rt, renew)
	if rt.deadline != deadline {
		t.Fatal("an identical rebroadcast moved the round deadline")
	}

	// A duplicate must still be answered, or a peer that lost the first vote
	// never hears it again — but with the SAME signed vote, not a new one.
	mine := distinctPrevoteHashes(votes, set[1].Address, 4, 0)
	if len(mine) != 1 || mine[0] != blockA.Hash {
		t.Fatalf("follower prevoted %d distinct hashes at height 4 round 0, want exactly A", len(mine))
	}
}
