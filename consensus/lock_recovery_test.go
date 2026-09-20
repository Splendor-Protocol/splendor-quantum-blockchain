package consensus

import (
	"errors"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// prevoteFrom signs a prevote as validator idx. Used for the member whose
// engine this test does not run: a faulty validator's messages are ordinary
// signed messages until it stops sending them.
func prevoteFrom(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, idx int,
	height uint64, round uint32, hash [32]byte) *Vote {
	t.Helper()
	v := &Vote{
		Height:        height,
		Round:         round,
		BlockHash:     hash,
		ValidatorAddr: set[idx].Address,
		Timestamp:     int64(height)*1000 + int64(round),
		Kind:          types.VoteKindPrevote,
	}
	if err := v.Sign(keys[idx]); err != nil {
		t.Fatal(err)
	}
	return v
}

// roundChangeFrom signs an unlocked round-change as validator idx.
func roundChangeFrom(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, idx int,
	height uint64, round uint32) *RoundChange {
	t.Helper()
	rc := &RoundChange{
		Height:        height,
		Round:         round,
		LockedRound:   -1,
		ValidatorAddr: set[idx].Address,
		Timestamp:     int64(height)*1000 + int64(round),
	}
	if err := rc.Sign(keys[idx]); err != nil {
		t.Fatal(err)
	}
	return rc
}

// envelopeFrom signs a leader's proposal envelope as validator idx.
func envelopeFrom(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, idx int,
	height uint64, round uint32, hash [32]byte) *ProposalEnvelope {
	t.Helper()
	env := &ProposalEnvelope{
		Height:     height,
		Round:      round,
		BlockHash:  hash,
		LeaderAddr: set[idx].Address,
		POLRound:   -1,
		Timestamp:  int64(height)*1000 + int64(round),
	}
	if err := env.Sign(keys[idx]); err != nil {
		t.Fatal(err)
	}
	return env
}

// leaderBody is the block a leader offers the engine at its current view.
// MultiValidator.propose asks the engine the same question.
func leaderBody(e *BFTEngine) *types.Block { return e.SafeProposal() }

// TestTwoPhaseABBLocksConvergeWithFaultySilent is the A@0/B@1/B@1 shape.
//
// Four validators, quorum three. One honest member sees the round-0 prevote
// quorum for A while the other two honest members do not; those two plus the
// faulty member later form a round-1 prevote quorum for B. The faulty member
// then goes silent, leaving honest locks A@0, B@1, B@1 — and two votes for B
// are one short of a quorum.
//
// B is the only value a correct node may still commit: it holds the later
// proof of lock. The node locked on A must therefore be handed that proof.
// Naming the round in the envelope is not enough — an honest node will not
// abandon a lock on an unproven claim — so the certificate itself has to
// travel with the re-proposal.
func TestTwoPhaseABBLocksConvergeWithFaultySilent(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	var lastProp *ProposalMsg
	var votes []*Vote
	var rcs []*RoundChange
	commits := make([]*types.Block, 3)
	engs := make([]*BFTEngine, 3)
	for i := 0; i < 3; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit:             func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast:            func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal:    func(p *ProposalMsg) { lastProp = p },
			BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; rcs = append(rcs, &cp) },
		})
		engs[i].SetHeight(height)
	}

	// Round 0. V0 leads and proposes A; all three prevote it, but only V0 is
	// shown the third prevote, so only V0 reaches the quorum and locks.
	blockA := proposalAt(t, set, keys, height, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("V0 did not adopt its own round-0 proposal")
	}
	propA := lastProp
	if propA == nil || propA.Envelope == nil {
		t.Fatal("V0 did not broadcast a round-0 envelope")
	}
	engs[1].handleProposal(pendingProposalOf(propA))
	engs[2].handleProposal(pendingProposalOf(propA))
	deliverVotesTo(t, []*BFTEngine{engs[0]}, &votes, types.VoteKindPrevote)
	votes = nil // V0's precommit for A reaches nobody.

	if got := engs[0].LockedProposal(); got == nil || got.Hash != blockA.Hash {
		t.Fatal("V0 must hold a round-0 lock on A")
	}
	if engs[1].VoteLockedAt(height) || engs[2].VoteLockedAt(height) {
		t.Fatal("V1 and V2 saw no round-0 quorum and must not be locked")
	}

	// Round 1. The same partition keeps V0's round-change from V1 and V2; the
	// faulty member's round-change completes their quorum instead.
	rcs = nil
	for _, e := range engs {
		e.handleTimeout()
	}
	for _, rc := range rcs {
		for _, e := range engs {
			if e.localAddr == rc.ValidatorAddr {
				continue
			}
			if rc.ValidatorAddr == set[0].Address {
				continue // V0 is still on the far side of the partition
			}
			cp := *rc
			e.handleRoundChange(&cp)
		}
	}
	rcs = nil
	for _, e := range engs {
		e.handleRoundChange(roundChangeFrom(t, set, keys, 3, height, 1))
	}
	if !engs[1].IsLeader(height, 1) {
		t.Fatal("V1 must lead round 1")
	}
	if !engs[1].CanPropose() {
		t.Fatal("V1 needs a round-change QC before proposing at round 1")
	}

	blockB := proposalAt(t, set, keys, height, 1, 22).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("A and B collide")
	}
	lastProp = nil
	if !engs[1].handleProposal(&pendingProposal{block: blockB, local: true}) {
		t.Fatal("V1 did not adopt B at round 1")
	}
	propB := lastProp
	if propB == nil {
		t.Fatal("V1 did not broadcast B")
	}
	engs[2].handleProposal(pendingProposalOf(propB))
	if engs[0].handleProposal(pendingProposalOf(propB)) {
		t.Fatal("V0 accepted B while locked on A with no proof of a later lock")
	}
	deliverVotesTo(t, []*BFTEngine{engs[1], engs[2]}, &votes, types.VoteKindPrevote)
	engs[1].handleVote(prevoteFrom(t, set, keys, 3, height, 1, blockB.Hash))
	engs[2].handleVote(prevoteFrom(t, set, keys, 3, height, 1, blockB.Hash))
	votes = nil // and now the faulty member goes silent for good.

	if got := engs[1].LockedProposal(); got == nil || got.Hash != blockB.Hash {
		t.Fatal("V1 must hold a round-1 lock on B")
	}
	if got := engs[2].LockedProposal(); got == nil || got.Hash != blockB.Hash {
		t.Fatal("V2 must hold a round-1 lock on B")
	}
	if got := engs[0].LockedProposal(); got == nil || got.Hash != blockA.Hash {
		t.Fatal("V0 must still hold its round-0 lock on A")
	}
	for i, c := range commits {
		if c != nil {
			t.Fatalf("engine %d committed %x before any quorum of three", i, c.Hash[:4])
		}
	}

	// From here the three honest members are alone. The chain must still
	// converge, on B.
	for round := uint32(2); round <= 9; round++ {
		votes, rcs = nil, nil
		for _, e := range engs {
			e.handleTimeout()
		}
		deliverRoundChanges(engs, &rcs)

		leader := int(height+uint64(round)) % len(set)
		if leader > 2 {
			continue // the faulty member's slot; nothing is proposed
		}
		le := engs[leader]
		if !le.CanPropose() {
			continue
		}
		body := leaderBody(le)
		if body == nil {
			body = proposalAt(t, set, keys, height, round, int64(100+round)).Block
		}
		lastProp = nil
		if !le.handleProposal(&pendingProposal{block: body, local: true}) || lastProp == nil {
			continue
		}
		for i, e := range engs {
			if i != leader {
				e.handleProposal(pendingProposalOf(lastProp))
			}
		}
		deliverVotes(t, engs, &votes, types.VoteKindPrevote)
		deliverVotes(t, engs, &votes, types.VoteKindPrecommit)
		if commits[0] != nil && commits[1] != nil && commits[2] != nil {
			break
		}
	}

	for i, c := range commits {
		if c == nil {
			t.Fatalf("engine %d never committed: the A@0/B@1/B@1 locks did not converge", i)
		}
		if c.Hash != blockB.Hash {
			t.Fatalf("engine %d committed %x, want the later-locked value B %x", i, c.Hash[:4], blockB.Hash[:4])
		}
	}
}

// TestPrevoteSurvivesRestartWithinTheSameView is the durability half of C1.
//
// A validator prevotes A at (h, r) and restarts before it precommits. The
// precommit journal is empty — nothing was locked — so nothing on disk stops
// the fresh process from prevoting a different value in the very same view,
// which is the equivocation every peer answers by dropping that validator's
// prevotes for the rest of the round.
//
// CometBFT's FilePV persists (height, round, step) before signing and refuses
// any regression; the prevote step is exactly the one Splendor does not yet
// record. Five Persistence validators were tombstoned in 2022 restarting nodes
// without their signing state, which is the same file doing the same job.
func TestPrevoteSurvivesRestartWithinTheSameView(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)
	journalPath := t.TempDir() + "/vote-journal.json"

	open := func() *BFTEngine {
		t.Helper()
		j, err := OpenVoteJournal(journalPath)
		if err != nil {
			t.Fatal(err)
		}
		e := twoPhaseFor(t, set, keys, 1, BFTConfig{VoteJournal: j})
		e.SetHeight(height)
		return e
	}

	blockA := proposalAt(t, set, keys, height, 0, 11).Block
	blockB := proposalAt(t, set, keys, height, 0, 22).Block
	if blockA.Hash == blockB.Hash {
		t.Fatal("A and B collide")
	}
	propA := &ProposalMsg{Block: blockA, Envelope: envelopeFrom(t, set, keys, 0, height, 0, blockA.Hash)}
	propB := &ProposalMsg{Block: blockB, Envelope: envelopeFrom(t, set, keys, 0, height, 0, blockB.Hash)}

	var before []*Vote
	eng := open()
	eng.broadcast = func(v *Vote) { cp := *v; before = append(before, &cp) }
	if !eng.handleProposal(pendingProposalOf(propA)) {
		t.Fatal("V1 did not adopt A at round 0")
	}
	if len(before) != 1 || before[0].Kind != types.VoteKindPrevote || before[0].BlockHash != blockA.Hash {
		t.Fatalf("expected exactly one prevote for A, got %d votes", len(before))
	}
	if eng.VoteLockedAt(height) {
		t.Fatal("a lone prevote must not lock the height")
	}

	// The process dies here: prevoted, not precommitted.
	var after []*Vote
	restarted := open()
	restarted.broadcast = func(v *Vote) { cp := *v; after = append(after, &cp) }

	restarted.handleProposal(pendingProposalOf(propB))
	for _, v := range after {
		if v.Kind == types.VoteKindPrevote && v.BlockHash == blockB.Hash {
			t.Fatal("restarted validator prevoted a second value at a view it had already prevoted in")
		}
	}

	// The same value at the same view is not a second decision: a restarted
	// validator must still be able to say again what it already said.
	after = nil
	if !restarted.handleProposal(pendingProposalOf(propA)) {
		t.Fatal("restarted validator refused to re-prevote the value it had already prevoted")
	}
	found := false
	for _, v := range after {
		if v.Kind == types.VoteKindPrevote && v.BlockHash == blockA.Hash {
			found = true
		}
	}
	if !found {
		t.Fatal("restarted validator did not re-send its prevote for A")
	}
}

// lockedRoundChangeFrom signs a round-change reporting a proven lock: the
// prevote quorum from certIdx and the body it locked.
func lockedRoundChangeFrom(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, idx int,
	height uint64, round, lockedRound uint32, body *types.Block, certIdx []int) *RoundChange {
	t.Helper()
	rc := &RoundChange{
		Height:        height,
		Round:         round,
		LockedHash:    body.Hash,
		LockedRound:   int32(lockedRound),
		LockedBlock:   body,
		ValidatorAddr: set[idx].Address,
		Timestamp:     int64(height)*1000 + int64(round),
	}
	for _, i := range certIdx {
		rc.LockedCert = append(rc.LockedCert, prevoteFrom(t, set, keys, i, height, lockedRound, body.Hash))
	}
	if err := rc.Sign(keys[idx]); err != nil {
		t.Fatal(err)
	}
	return rc
}

// TestRoundChangeLeaderProposesHighestJustifiedValue covers the selection
// itself, with a leader that holds no lock of its own.
//
// Counting round-changes says a leader may propose. It does not say what. The
// value belongs to the highest lock the quorum can prove — and a claim with no
// certificate behind it still counts toward the quorum without winning it.
func TestRoundChangeLeaderProposesHighestJustifiedValue(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	var lastProp *ProposalMsg
	eng := twoPhaseFor(t, set, keys, 2, BFTConfig{
		BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
	})
	eng.SetHeight(height)
	eng.handleTimeout() // round 1
	eng.handleTimeout() // round 2
	if eng.Round() != 2 || !eng.IsLeader(height, 2) {
		t.Fatalf("expected V2 leading round 2, got round %d", eng.Round())
	}
	if eng.VoteLockedAt(height) {
		t.Fatal("this leader is meant to hold no lock of its own")
	}

	locked := proposalAt(t, set, keys, height, 1, 77).Block
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 1, height, 2, 1, locked, []int{1, 2, 3}))
	eng.handleRoundChange(roundChangeFrom(t, set, keys, 3, height, 2))
	if !eng.CanPropose() {
		t.Fatal("three round-changes is a quorum; the leader may propose")
	}
	if got := eng.SafeProposal(); got == nil || got.Hash != locked.Hash {
		t.Fatal("the leader must take the value its round-change quorum proves, not mint its own")
	}

	fresh := proposalAt(t, set, keys, height, 2, 88).Block
	if eng.handleProposal(&pendingProposal{block: fresh, local: true}) {
		t.Fatal("the leader minted a value the round-change quorum does not justify")
	}

	lastProp = nil
	if !eng.handleProposal(&pendingProposal{block: locked, local: true}) {
		t.Fatal("the leader refused the value its own quorum justifies")
	}
	if lastProp == nil || lastProp.Envelope == nil {
		t.Fatal("no envelope broadcast")
	}
	if lastProp.Envelope.POLRound != 1 || lastProp.Envelope.POLHash != locked.Hash {
		t.Fatalf("envelope POL = round %d hash %x, want round 1 hash %x",
			lastProp.Envelope.POLRound, lastProp.Envelope.POLHash[:4], locked.Hash[:4])
	}
	if len(lastProp.POL) < 3 {
		t.Fatalf("re-proposal carried %d prevotes, want the quorum that proves the lock", len(lastProp.POL))
	}
	if lastProp.Block.Header.ValidatorAddr != set[1].Address {
		t.Fatal("re-proposing must not change who produced the value")
	}

	// A lock claimed with no certificate counts toward the quorum and loses
	// the selection: it is signed, so it cannot be forged, and unproven, so it
	// cannot displace a value somebody can actually prove.
	bogus := proposalAt(t, set, keys, height, 1, 99).Block
	unproven := &RoundChange{
		Height:        height,
		Round:         2,
		LockedHash:    bogus.Hash,
		LockedRound:   1,
		ValidatorAddr: set[0].Address,
		Timestamp:     1,
	}
	if err := unproven.Sign(keys[0]); err != nil {
		t.Fatal(err)
	}
	eng.handleRoundChange(unproven)
	if got := eng.SafeProposal(); got == nil || got.Hash != locked.Hash {
		t.Fatal("an unproven lock claim displaced a proven one")
	}
}

// TestJournalPrevoteRules pins the durable prevote record's decisions.
func TestJournalPrevoteRules(t *testing.T) {
	path := t.TempDir() + "/pv.json"
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := [32]byte{1}, [32]byte{2}

	if err := j.RecordPrevote(9, 3, a, -1); err != nil {
		t.Fatalf("first prevote refused: %v", err)
	}
	if err := j.RecordPrevote(9, 3, a, -1); err != nil {
		t.Fatalf("re-sending the same prevote is not a second decision: %v", err)
	}
	if err := j.RecordPrevote(9, 3, b, -1); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("a second value at the same view was admitted: %v", err)
	}
	if err := j.RecordPrevote(9, 2, b, -1); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("a prevote at a round already left was admitted: %v", err)
	}
	if err := j.RecordPrevote(9, 4, b, -1); err != nil {
		t.Fatalf("an unlocked node may prevote another value at a later round: %v", err)
	}

	// Reopening must find the record; that is the whole point of writing it.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	h, r, hash, ok := j2.LastPrevote()
	if !ok || h != 9 || r != 4 || hash != b {
		t.Fatalf("reopened prevote record = (%d,%d,%x)", h, r, hash[:2])
	}

	// Once locked, leaving the lock needs a strictly later proof of lock.
	if err := j2.RecordWithPOL(9, 4, b, 4); err != nil {
		t.Fatal(err)
	}
	if err := j2.RecordPrevote(9, 5, a, -1); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("prevoted away from the lock with no POL: %v", err)
	}
	if err := j2.RecordPrevote(9, 5, a, 4); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("a POL at the locked round is not later: %v", err)
	}
	if err := j2.RecordPrevote(9, 6, a, 5); err != nil {
		t.Fatalf("a strictly later POL must unlock: %v", err)
	}
}
