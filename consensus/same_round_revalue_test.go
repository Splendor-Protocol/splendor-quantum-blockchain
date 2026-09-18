package consensus

import (
	"errors"
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

// TestTwoPhaseByzantineLeaderCannotReplaceItsValueInOneRound is the safety
// side of the same rule. A leader that sends two different bodies at the round
// it leads is equivocating, not retrying; the second must be refused, and the
// receiver must keep the prevote it already signed rather than sign another.
func TestTwoPhaseByzantineLeaderCannotReplaceItsValueInOneRound(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes []*Vote
	commits := make([]*types.Block, 4)
	engs := make([]*BFTEngine, 4)
	for i := 0; i < 4; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit:          func(b *types.Block) error { commits[i] = b; return nil },
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

	// The same leader signs a second envelope, at the same round, over a
	// different body. Followers adopt A first, then are shown B.
	blockB := proposalAt(t, set, keys, 4, 0, 22).Block
	envB := &ProposalEnvelope{
		Height: 4, Round: 0, BlockHash: blockB.Hash,
		LeaderAddr: set[0].Address, POLRound: -1, Timestamp: 99,
	}
	if err := envB.Sign(keys[0]); err != nil {
		t.Fatal(err)
	}
	propB := &ProposalMsg{Block: blockB, Envelope: envB}

	for i, e := range engs[1:] {
		if !e.handleProposal(pendingProposalOf(propA)) {
			t.Fatalf("follower %d refused A", i+1)
		}
		if e.handleProposal(pendingProposalOf(propB)) {
			t.Fatalf("follower %d adopted a second value at the round it already voted in", i+1)
		}
		if e.proposal == nil || e.proposal.Hash != blockA.Hash {
			t.Fatalf("follower %d abandoned A for B", i+1)
		}
		if h := distinctPrevoteHashes(votes, set[i+1].Address, 4, 0); len(h) != 1 || h[0] != blockA.Hash {
			t.Fatalf("follower %d signed %d prevotes at height 4 round 0, want one for A", i+1, len(h))
		}
	}

	// The honest majority still finishes the height on A.
	for i := 0; i < 2; i++ {
		deliverVotes(t, engs, &votes, types.VoteKindPrevote)
		deliverVotes(t, engs, &votes, types.VoteKindPrecommit)
	}
	for i := range engs {
		if commits[i] == nil || commits[i].Hash != blockA.Hash {
			t.Fatalf("engine %d did not commit A despite an equivocating leader", i)
		}
	}
}

// TestTwoPhaseFailedApplyDoesNotEraseTheVoteAlreadyCast is the regression for
// the residual seen on the fixed build: four equivocation lines at two
// heights, from one node, rarely rather than continuously.
//
// commitBlock's failure branch used to clear the whole view — proposal, vote
// sets, POL, vote commitment, round-change QC — reasoning that nothing was
// committed so there was nothing to be locked on. But the prevote this node
// had already broadcast at (height, round) was still in every peer's
// seenVotes. Having forgotten it, the node would adopt a different value at
// the very same view and prevote again, and every peer dropped it as an
// equivocator for the rest of the round. A node that had only prevoted — never
// precommitted — had nothing in its journal to catch it, and a node reaches a
// precommit quorum on its peers' precommits alone.
func TestTwoPhaseFailedApplyDoesNotEraseTheVoteAlreadyCast(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes []*Vote
	applyFails := true
	engs := make([]*BFTEngine, 4)
	for i := 0; i < 4; i++ {
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit: func(b *types.Block) error {
				if applyFails {
					return errors.New("injected apply failure")
				}
				return nil
			},
			Broadcast:         func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
		})
		engs[i].SetHeight(4) // set[0] leads round 0
	}
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader refused its own proposal")
	}
	victim := engs[1]
	if !victim.handleProposal(pendingProposalOf(lastProp)) {
		t.Fatal("follower refused A")
	}
	// It prevoted A. It never precommits, so its journal stays empty — the
	// prevote is the only record that it voted at all.
	if victim.VoteLockedAt(4) {
		t.Fatal("precondition: the victim must not be journal-locked")
	}

	// Three peers precommit A. That is a quorum, so the victim commits — and
	// the apply fails.
	for _, i := range []int{0, 2, 3} {
		pc := &Vote{
			Height: 4, Round: 0, BlockHash: blockA.Hash, ValidatorAddr: set[i].Address,
			Timestamp: int64(50 + i), Kind: types.VoteKindPrecommit,
		}
		if err := pc.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		victim.handleVote(pc)
	}
	if victim.AdoptedProposal() == nil {
		t.Fatal("a failed apply erased the value this node had already prevoted for")
	}

	// A second body arrives at the very same view. It must be refused.
	blockB := proposalAt(t, set, keys, 4, 0, 22).Block
	envB := &ProposalEnvelope{
		Height: 4, Round: 0, BlockHash: blockB.Hash,
		LeaderAddr: set[0].Address, POLRound: -1, Timestamp: 77,
	}
	if err := envB.Sign(keys[0]); err != nil {
		t.Fatal(err)
	}
	if victim.handleProposal(pendingProposalOf(&ProposalMsg{Block: blockB, Envelope: envB})) {
		t.Fatal("after a failed apply the node adopted a second value at the round it had already voted in")
	}
	if h := distinctPrevoteHashes(votes, set[1].Address, 4, 0); len(h) != 1 || h[0] != blockA.Hash {
		t.Fatalf("victim signed %d distinct prevotes at height 4 round 0, want exactly one for A", len(h))
	}

	// Recovery is the round timer, not amnesia: the view change frees it.
	applyFails = false
	victim.handleTimeout()
	if victim.Round() != 1 {
		t.Fatalf("round = %d after the timeout, want 1", victim.Round())
	}
	if victim.AdoptedProposal() != nil {
		t.Fatal("a view change must release the value so the next round can start clean")
	}
}
