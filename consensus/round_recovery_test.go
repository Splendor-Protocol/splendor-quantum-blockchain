package consensus

import (
	"path/filepath"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// timesOut models the loop's round timer: a deadline exists only for a view
// this engine holds a proposal in, is waiting on another member for, or has
// been refused a proposal at. It is syncRoundTimer's own rule, restated here
// because the stall these tests are about is precisely a view where nothing is
// armed — an engine that cannot time out sends no round-change, and three of
// four validators is a quorum only while all three are talking.
func timesOut(e *BFTEngine) bool {
	e.stateMu.Lock()
	h, r := e.currentHeight, e.currentRound
	open := e.voteSet != nil && e.proposal != nil
	refused := e.hasRefused && e.refusedHeight == h && e.refusedRound == r
	e.stateMu.Unlock()
	return open || refused || e.awaitingRemoteLeader(h, r)
}

// roundChangeRecordOf reads what the engine retained for one sender at one
// round: whether the message was counted, and what of its justification was
// kept.
func roundChangeRecordOf(e *BFTEngine, round uint32, addr qcrypto.Address) *roundChangeRecord {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.rcByRound[round][addr]
}

// TestRestartAtAPrevotedRoundDoesNotStrandTheSet is the liveness half of the
// prevote sidecar.
//
// A validator that prevoted at round 1 and died before precommitting has
// nothing in the PRECOMMIT journal, so SetHeight resumed it at round 0 — below
// the round its own sidecar refuses to prevote under. It then led round 0,
// could not vote for the body it built, took no view change (the refusal is not
// at the sidecar's round), and armed no deadline (a view it leads itself waits
// on nobody). It therefore sent no round-change at all, and with one member of
// four silent the two round-changes left are one short of the quorum that would
// have rotated the leader: the height stops with two honest nodes waiting on a
// third that is running normally and saying nothing.
//
// Resuming at the sidecar's round puts it back in the view it was already in,
// where the identical value may be re-prevoted and a fresh body of its own is
// refused by a branch that does take the view change.
func TestRestartAtAPrevotedRoundDoesNotStrandTheSet(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(5) // (5+0)%4 == 1: V1 leads round 0, V2 round 1
	journalPath := filepath.Join(t.TempDir(), "v1-journal.json")

	var lastProp *ProposalMsg
	var votes []*Vote
	var rcs []*RoundChange
	commits := make([]*types.Block, 3)
	engs := make([]*BFTEngine, 3)
	openV1 := func() *BFTEngine {
		t.Helper()
		j, err := OpenVoteJournal(journalPath)
		if err != nil {
			t.Fatal(err)
		}
		return twoPhaseFor(t, set, keys, 1, BFTConfig{
			VoteJournal:          j,
			OnCommit:             func(b *types.Block) error { commits[1] = b; return nil },
			Broadcast:            func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal:    func(p *ProposalMsg) { lastProp = p },
			BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; rcs = append(rcs, &cp) },
		})
	}
	for _, i := range []int{0, 2} {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit:             func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast:            func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal:    func(p *ProposalMsg) { lastProp = p },
			BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; rcs = append(rcs, &cp) },
		})
	}
	engs[1] = openV1()
	for _, e := range engs {
		e.SetHeight(height)
	}

	// Round 0. V1 leads and proposes A, and the partition loses it: V1 holds
	// its own value open and the other two wait for a proposal that never
	// arrives, so all three time out. The fourth member is silent throughout
	// and sends nothing at all.
	blockA := proposalAt(t, set, keys, height, 0, 11).Block
	if !engs[1].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("V1 did not adopt its own round-0 proposal")
	}
	votes, lastProp = nil, nil
	for i, e := range engs {
		if !timesOut(e) {
			t.Fatalf("engine %d has no deadline at round 0", i)
		}
		e.handleTimeout()
	}
	deliverRoundChanges(engs, &rcs)
	if !engs[2].CanPropose() {
		t.Fatal("three round-changes out of four is a quorum; V2 may propose at round 1")
	}

	// Round 1. V2 proposes B; V0 and V1 prevote it. No prevote reaches anyone,
	// so nobody locks — and V1's prevote is on disk regardless.
	blockB := proposalAt(t, set, keys, height, 1, 22).Block
	lastProp = nil
	if !engs[2].handleProposal(&pendingProposal{block: blockB, local: true}) || lastProp == nil {
		t.Fatal("V2 did not adopt B at round 1")
	}
	propB := lastProp
	for _, i := range []int{0, 1} {
		if !engs[i].handleProposal(pendingProposalOf(propB)) {
			t.Fatalf("V%d did not adopt B at round 1", i)
		}
	}
	votes = nil
	if engs[1].VoteLockedAt(height) {
		t.Fatal("a prevote with no quorum behind it must not lock the height")
	}
	if ph, pr, _, ok := engs[1].journal.LastPrevote(); !ok || ph != height || pr != 1 {
		t.Fatalf("V1's durable prevote record = (%d,%d,%v), want height %d round 1", ph, pr, ok, height)
	}

	// V1 dies here: prevoted at round 1, precommitted nothing.
	engs[1] = openV1()
	engs[1].SetHeight(height)
	if r := engs[1].Round(); r != 1 {
		t.Fatalf("the restarted validator resumed at round %d; its own durable prevote record "+
			"refuses every prevote below round 1, so it can neither vote nor time out there", r)
	}

	// The three honest members are now alone with one silent member. The height
	// must still commit, and all three must agree.
	for step := 0; step < 40; step++ {
		for _, e := range engs {
			if timesOut(e) {
				e.handleTimeout()
			}
		}
		deliverRoundChanges(engs, &rcs)
		for i, le := range engs {
			round := le.Round()
			if !le.IsLeader(height, round) || !le.CanPropose() {
				continue
			}
			body := leaderBody(le)
			if body == nil {
				body = proposalAt(t, set, keys, height, round, int64(1000+step)).Block
			}
			lastProp = nil
			if !le.handleProposal(&pendingProposal{block: body, local: true}) || lastProp == nil {
				continue
			}
			for j, other := range engs {
				if j != i {
					other.handleProposal(pendingProposalOf(lastProp))
				}
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
			t.Fatalf("engine %d never committed: the set stalled after one member restarted "+
				"at a round it had already prevoted in", i)
		}
		if c.Hash != commits[0].Hash {
			t.Fatalf("engine %d committed %x, engine 0 committed %x", i, c.Hash[:4], commits[0].Hash[:4])
		}
	}
}

// TestRefusedOwnProposalGetsARoundDeadline covers the other half of the same
// stall, for every refusal that is not the restart shape.
//
// The journal here belongs to a later height — a data dir restored under a
// surviving journal — so this node cannot vote at this height at all. The round
// is deliberately left alone, as before: bumping it on a refusal this process
// caused would abandon a round its peers are still voting in. But a view this
// node leads and holds no proposal in arms no deadline, so "left to the timer"
// meant left to a timer that was never set, and the node went silent for good.
func TestRefusedOwnProposalGetsARoundDeadline(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(5) // (5+0)%4 == 1: this node leads round 0
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(9, 0, proposalAt(t, set, keys, 9, 0, 1).Block.Hash); err != nil {
		t.Fatal(err)
	}
	eng := twoPhaseFor(t, set, keys, 1, BFTConfig{VoteJournal: j})
	eng.SetHeight(height)
	if !eng.IsLeader(height, 0) {
		t.Fatal("this test needs the local node leading round 0")
	}

	rt := newRoundTimer()
	defer rt.disarm()
	eng.syncRoundTimer(rt, false)
	if rt.armed {
		t.Fatal("a view this node leads with no proposal yet has nothing to wait for")
	}

	fresh := proposalAt(t, set, keys, height, 0, 7).Block
	if eng.handleProposal(&pendingProposal{block: fresh, local: true}) {
		t.Fatal("the journal must refuse a vote at a height it has already passed")
	}
	if r := eng.Round(); r != 0 {
		t.Fatalf("a refusal this process caused bumped the round to %d; that is the timer's decision", r)
	}

	eng.syncRoundTimer(rt, false)
	if !rt.armed || rt.height != height || rt.round != 0 {
		t.Fatalf("no deadline for the view this node leads and cannot propose into "+
			"(armed=%v height=%d round=%d): nothing will move it off, and it emits no round-change",
			rt.armed, rt.height, rt.round)
	}
}

// TestRoundChangeBodyMustMatchTheCertifiedValue is the body-binding defect.
//
// ComputeVotedHash marshals the HEADER alone — deliberately, so a certificate
// can be attached to a block after it is signed — which leaves the transaction
// list bound by nothing but the header's Merkle root. roundChangeJustified
// checked only that hash, so a faulty member that had watched a genuine prevote
// quorum form could forward the genuine certificate with a garbage transaction
// list under it. The selection accepted it, the leader prevoted and broadcast
// it, and every peer rejected it on the transaction root: not a safety break,
// since the header hash is genuine, but the round the fix exists to resolve was
// spent on a value nobody could apply.
func TestRoundChangeBodyMustMatchTheCertifiedValue(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	var lastProp *ProposalMsg
	eng := twoPhaseFor(t, set, keys, 2, BFTConfig{
		BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
		// The judgement the executor makes, in the shape the engine gets it.
		ValidateProposal: func(b *types.Block) error { return b.Validate() },
	})
	eng.SetHeight(height)
	eng.handleTimeout() // round 1
	eng.handleTimeout() // round 2, which this node leads
	if eng.Round() != 2 || !eng.IsLeader(height, 2) {
		t.Fatalf("expected V2 leading round 2, got round %d", eng.Round())
	}

	genuine := proposalAt(t, set, keys, height, 1, 77).Block
	tampered := *genuine
	tampered.Transactions = []*types.Transaction{{Version: 1, Nonce: 1, ChainID: types.ChainID}}
	if tampered.Hash != genuine.Hash {
		t.Fatal("the tampered body must keep the hash the certificate proves")
	}

	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 1, height, 2, 1, &tampered, []int{1, 2, 3}))
	eng.handleRoundChange(roundChangeFrom(t, set, keys, 3, height, 2))
	if !eng.CanPropose() {
		t.Fatal("three round-changes out of four is a quorum, justified or not")
	}
	if got := eng.SafeProposal(); got != nil {
		t.Fatalf("a genuine certificate justified a body carrying a different transaction list: "+
			"the leader was handed %x to propose", got.Hash[:4])
	}

	lastProp = nil
	if eng.handleProposal(&pendingProposal{block: &tampered, local: true}) || lastProp != nil {
		t.Fatal("the leader proposed a body whose transactions do not match its own header")
	}

	// The control: the same header with its real body is proposable, so the
	// refusal above is about the mismatch and not about the value's origin.
	lastProp = nil
	if !eng.handleProposal(&pendingProposal{block: genuine, local: true}) || lastProp == nil {
		t.Fatal("the leader refused a body that does match its header")
	}
}

// TestUnjustifiedRoundChangeCountsWithoutRetainingItsBody pins both halves of
// the retention bound.
//
// Counting an unjustified round-change is deliberate and must not change:
// dropping one because its sender lost a certificate stalls n=4, f=1. What must
// not be kept is the attachment — on an unproven claim, or on a round the
// engine is nowhere near. rcByRound lives for a whole height and admits rounds
// up to 32 ahead, so one member could park a body per admissible round on every
// peer, each up to the 32 MiB a block may be.
func TestUnjustifiedRoundChangeCountsWithoutRetainingItsBody(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	eng := twoPhaseFor(t, set, keys, 2, BFTConfig{})
	eng.SetHeight(height)
	eng.handleTimeout()
	eng.handleTimeout()
	if eng.Round() != 2 {
		t.Fatalf("expected round 2, got %d", eng.Round())
	}
	body := proposalAt(t, set, keys, height, 1, 77).Block

	// Two prevotes out of four is not a quorum, so this claim is unproven.
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 1, height, 2, 1, body, []int{1, 2}))
	rec := roundChangeRecordOf(eng, 2, set[1].Address)
	if rec == nil {
		t.Fatal("an unjustified round-change must still be counted: dropping it stalls n=4, f=1")
	}
	if rec.justified {
		t.Fatal("a claim two prevotes short of a quorum was recorded as proven")
	}
	if rec.rc.LockedBlock != nil || rec.rc.LockedCert != nil {
		t.Fatal("an unjustified round-change retained its body; nothing will ever read it")
	}

	// It counts: with the silent member's plain round-change the quorum is there.
	eng.handleRoundChange(roundChangeFrom(t, set, keys, 3, height, 2))
	if !eng.CanPropose() {
		t.Fatal("the unjustified round-change did not count toward the quorum")
	}

	// A proven claim for a round the engine is nowhere near is counted and
	// stripped: only the leader of the CURRENT round ever reads an attachment.
	far := uint32(2 + roundChangeBodyWindow + 3)
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 1, height, far, 1, body, []int{1, 2, 3}))
	rec = roundChangeRecordOf(eng, far, set[1].Address)
	if rec == nil {
		t.Fatalf("a round-change for round %d is admissible and must be counted", far)
	}
	if rec.rc.LockedBlock != nil || rec.rc.LockedCert != nil {
		t.Fatalf("round %d is %d rounds ahead of the engine and kept its body", far, far-2)
	}

	// And the window is not a blanket refusal: a proven claim at the round the
	// engine is actually in keeps everything the selection needs.
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 0, height, 2, 1, body, []int{0, 1, 2}))
	rec = roundChangeRecordOf(eng, 2, set[0].Address)
	if rec == nil || !rec.justified || rec.rc.LockedBlock == nil || len(rec.rc.LockedCert) == 0 {
		t.Fatal("a proven claim at the current round must keep its proof and its body")
	}
	if got := eng.SafeProposal(); got == nil || got.Hash != body.Hash {
		t.Fatal("the leader must still be handed the value its quorum proves")
	}
}

// TestJustifiedValueSelectionIsDeterministic pins the tie-break.
//
// Two round-changes reporting the same proven value differ only in who sent it
// and which quorum they carry. The selection fell through to map iteration
// order there, so two leaders at one view — or one leader on a retry — could
// forward different certificates for the same value, and which body the
// proposal carried was whatever the map happened to yield first.
func TestJustifiedValueSelectionIsDeterministic(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	eng := twoPhaseFor(t, set, keys, 2, BFTConfig{})
	eng.SetHeight(height)
	eng.handleTimeout()
	eng.handleTimeout()
	body := proposalAt(t, set, keys, height, 1, 77).Block

	// Both are proven, both at the same round, both for the same value.
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 0, height, 2, 1, body, []int{0, 1, 2}))
	eng.handleRoundChange(lockedRoundChangeFrom(t, set, keys, 1, height, 2, 1, body, []int{1, 2, 3}))
	eng.handleRoundChange(roundChangeFrom(t, set, keys, 3, height, 2))

	want, ok := eng.safeValueLocked()
	if !ok {
		t.Fatal("two proven claims for one value and no selection")
	}
	lower := set[0].Address
	if string(set[1].Address[:]) < string(lower[:]) {
		lower = set[1].Address
	}
	if want.ValidatorAddr != lower {
		t.Fatalf("selected the claim from %s, want the lower reporter %s", want.ValidatorAddr, lower)
	}
	// Map iteration order is randomised per range, so repeating the selection
	// is what catches an order-dependent choice.
	for i := 0; i < 64; i++ {
		got, ok := eng.safeValueLocked()
		if !ok || got.ValidatorAddr != want.ValidatorAddr {
			t.Fatalf("selection %d chose %s, the first chose %s: the choice depends on map order",
				i, got.ValidatorAddr, want.ValidatorAddr)
		}
	}
}

// TestRoundChangeQuorumForALaterRoundCatchesUp pins the catch-up.
//
// A quorum of signed round-changes for a later round is proof that the set has
// left the rounds in between: at most f of those signatures are faulty, so at
// least one correct node is already there and will answer nothing earlier. The
// node used to stay where it was and pay a full round timeout per intermediate
// round, with nothing to wait for in any of them.
func TestRoundChangeQuorumForALaterRoundCatchesUp(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	var mine []*RoundChange
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{
		BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; mine = append(mine, &cp) },
	})
	eng.SetHeight(height)
	if eng.Round() != 0 {
		t.Fatalf("expected round 0, got %d", eng.Round())
	}

	for _, i := range []int{1, 2, 3} {
		eng.handleRoundChange(roundChangeFrom(t, set, keys, i, height, 5))
	}
	if r := eng.Round(); r != 5 {
		t.Fatalf("a quorum of round-changes for round 5 left this node at round %d: it will now "+
			"walk there one round timeout at a time", r)
	}
	found := false
	for _, rc := range mine {
		if rc.Round == 5 {
			found = true
		}
	}
	if !found {
		t.Fatal("catching up must announce this node's own round-change at the round it joined")
	}
	if eng.View().Round != 5 {
		t.Fatalf("the published view still reports round %d", eng.View().Round)
	}
}

// A node that comes back at a round above zero has to SAY so.
//
// SetHeight assigns currentRound directly. Nothing else does: a view change goes
// through enterRoundLocked, which signs a round-change and broadcasts it. So a
// recovered node sat in a view no peer knew about, and — because it holds no
// proposal and may lead that view itself — armed no deadline either, so it never
// reached handleTimeout and never announced anything.
//
// With four validators and one silent, that is a halt rather than a delay. The
// two peers still walking up through their timeouts reach the recovered round
// and find two round-changes there, one short of the quorum of three: the third
// is the recovered node's, which it never sent. The height stops with every
// honest member waiting.
//
// Both recovery paths assign the round, so both need the announcement: the
// precommit journal's round + 1, and the durable prevote's own round.
func TestARecoveredRoundIsAnnounced(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(5)

	for _, tc := range []struct {
		name  string
		write func(t *testing.T, j *VoteJournal, hash [32]byte)
		want  uint32
	}{
		{
			name: "resumed from a durable prevote",
			write: func(t *testing.T, j *VoteJournal, hash [32]byte) {
				t.Helper()
				if err := j.RecordPrevote(height, 3, hash, -1); err != nil {
					t.Fatalf("record prevote: %v", err)
				}
			},
			want: 3, // the prevoted round itself: a lower one cannot be prevoted at
		},
		{
			name: "resumed from a precommit",
			write: func(t *testing.T, j *VoteJournal, hash [32]byte) {
				t.Helper()
				if err := j.RecordWithPOL(height, 2, hash, 0); err != nil {
					t.Fatalf("record precommit: %v", err)
				}
			},
			want: 3, // the round after the one already spent
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			var hash [32]byte
			hash[0] = 0xAB

			j, err := OpenVoteJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.write(t, j, hash)

			// The process comes back with that journal.
			reopened, err := OpenVoteJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			var sent []*RoundChange
			eng := twoPhaseFor(t, set, keys, 1, BFTConfig{
				VoteJournal:          reopened,
				BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; sent = append(sent, &cp) },
			})
			eng.SetHeight(height)

			eng.stateMu.Lock()
			round := eng.currentRound
			eng.stateMu.Unlock()
			if round != tc.want {
				t.Fatalf("resumed at round %d, want %d", round, tc.want)
			}

			if len(sent) == 0 {
				t.Fatalf("recovered at round %d and told nobody: no round-change was broadcast, so the "+
					"quorum at that round is one member short for ever and the height cannot move", round)
			}
			for _, rc := range sent {
				if rc.Height != height || rc.Round != tc.want {
					t.Fatalf("announced (h=%d,r=%d), want (h=%d,r=%d)", rc.Height, rc.Round, height, tc.want)
				}
				if rc.ValidatorAddr != eng.localAddr {
					t.Fatalf("announced a round-change from %s, not from this node", rc.ValidatorAddr)
				}
				if len(rc.Signature) == 0 {
					t.Fatal("the announced round-change carries no signature, so no peer can count it")
				}
			}
		})
	}
}

// Round 0 is not a recovery: a node starting a fresh height must not announce a
// view change nobody asked for, or every restart would push the set forward.
func TestAFreshHeightAnnouncesNothing(t *testing.T) {
	set, keys := testSet(t, 4)
	var sent []*RoundChange
	eng := twoPhaseFor(t, set, keys, 1, BFTConfig{
		BroadcastRoundChange: func(rc *RoundChange) { cp := *rc; sent = append(sent, &cp) },
	})
	eng.SetHeight(9)
	if len(sent) != 0 {
		t.Fatalf("a fresh height broadcast %d round-changes, want none", len(sent))
	}
}
