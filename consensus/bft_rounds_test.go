package consensus

import (
	"errors"
	"path/filepath"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func alwaysRounds(uint64) bool { return true }

func twoPhaseFor(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, me int, cfg BFTConfig) *BFTEngine {
	t.Helper()
	cfg.RoundsV1 = alwaysRounds
	if cfg.VoteJournal == nil {
		j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg.VoteJournal = j
	}
	return engineFor(t, set, keys, me, cfg)
}

func TestLegacySingleVotePathUntilFork(t *testing.T) {
	set, keys := testSet(t, 1)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got *types.Block
	eng := engineFor(t, set, keys, 0, BFTConfig{
		VoteJournal: j,
		OnCommit:    func(b *types.Block) error { got = b; return nil },
	})
	eng.SetHeight(1)
	msg := proposalAt(t, set, keys, 1, 0, 1)
	if !eng.handleProposal(&pendingProposal{block: msg.Block, local: true}) {
		t.Fatal("legacy proposal refused")
	}
	if got == nil {
		t.Fatal("legacy solo path must still commit on the first vote")
	}
	if len(got.Votes) != 1 || got.Votes[0].Kind != "" {
		t.Fatalf("legacy QC kind = %q, want empty", got.Votes[0].Kind)
	}
	_, _, hash, ok := j.Last()
	if !ok || hash != msg.Block.Hash {
		t.Fatal("legacy path must journal-lock on the first vote")
	}
}

func TestTwoPhaseSoloCommitsInOneLoop(t *testing.T) {
	set, keys := testSet(t, 1)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "solo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got *types.Block
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{
		VoteJournal: j,
		OnCommit:    func(b *types.Block) error { got = b; return nil },
	})
	eng.SetHeight(1)
	msg := proposalAt(t, set, keys, 1, 0, 1)
	if !eng.handleProposal(&pendingProposal{block: msg.Block, local: true}) {
		t.Fatal("two-phase solo proposal refused")
	}
	if got == nil || got.Hash != msg.Block.Hash {
		t.Fatal("n=1 must prevote+precommit+commit in the same loop")
	}
	if len(got.Votes) != 1 || got.Votes[0].Kind != types.VoteKindPrecommit {
		t.Fatalf("solo QC kind = %q, want precommit", got.Votes[0].Kind)
	}
	_, _, hash, ok := j.Last()
	if !ok || hash != msg.Block.Hash {
		t.Fatal("journal must lock on the precommit, not the prevote")
	}
}

// A leader holding a prevote quorum for A must attach that certificate when it
// re-broadcasts A — not merely name the round in the envelope. A peer locked to
// a different value needs the actual prevotes to verify the POL and unlock.
func TestReProposalAttachesTheRetainedPrevoteQuorum(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{
		BroadcastProposal: func(p *ProposalMsg) {
			cp := *p
			lastProp = &cp
		},
	})
	eng.SetHeight(4) // (4+0)%4 == 0: set[0] leads round 0

	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !eng.handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader did not adopt A")
	}
	// The first broadcast has no POL yet: nothing has reached quorum.
	if lastProp == nil || len(lastProp.POL) != 0 {
		t.Fatalf("initial proposal POL = %d votes, want 0", len(lastProp.POL))
	}
	// Two peer prevotes plus the leader's own form the quorum 3, which the
	// engine retains as a POL for A.
	for _, i := range []int{1, 2} {
		v := &Vote{Height: 4, Round: 0, BlockHash: blockA.Hash, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.handleVote(v)
	}
	if !eng.VoteLockedAt(4) {
		t.Fatal("leader did not POL-lock on A")
	}
	if len(eng.polVotes) < 3 {
		t.Fatalf("retained POL votes = %d, want >= 3", len(eng.polVotes))
	}

	// Re-proposing the SAME locked body takes the duplicate path (returns false
	// because it is not newly adopted), but it must still re-broadcast — and the
	// re-broadcast must now attach the retained prevote quorum.
	lastProp = nil
	eng.handleProposal(&pendingProposal{block: blockA, local: true})
	if lastProp == nil || len(lastProp.POL) < 3 {
		t.Fatalf("re-proposal POL has %d votes, want the retained quorum of 3", len(lastProp.POL))
	}
	if !eng.verifyPOL(4, 0, blockA.Hash, lastProp.POL) {
		t.Fatal("attached POL does not verify for the locked value")
	}
}

// A round-change quorum must select the value with the HIGHEST locked round and
// retain its certificate, so the next leader re-proposes the safe value (B@1)
// instead of a lower lock (A@0) or a fresh block.
func TestRoundChangeSelectsHighestJustifiedValue(t *testing.T) {
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(5)

	hashA := [32]byte{0xAA}
	hashB := [32]byte{0xBB}

	// Validator 0: no lock.
	rc0 := &RoundChange{Height: 5, Round: 2, LockedRound: -1, ValidatorAddr: set[0].Address, Timestamp: 1}
	if err := rc0.Sign(keys[0]); err != nil {
		t.Fatal(err)
	}
	// Validator 1: bare vote lock at A@round0 (no witnessed quorum).
	rc1 := &RoundChange{Height: 5, Round: 2, LockedHash: hashA, LockedRound: 0, ValidatorAddr: set[1].Address, Timestamp: 2}
	if err := rc1.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	// Validator 2: lock at B@round1 carrying its prevote quorum certificate.
	polB := make([]*Vote, 3)
	for i := 0; i < 3; i++ {
		polB[i] = &Vote{Height: 5, Round: 1, BlockHash: hashB, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := polB[i].Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
	}
	rc2 := &RoundChange{Height: 5, Round: 2, LockedHash: hashB, LockedRound: 1, ValidatorAddr: set[2].Address, Timestamp: 3, POLVotes: polB}
	if err := rc2.Sign(keys[2]); err != nil {
		t.Fatal(err)
	}

	// n=4 → quorum 3. The round-change QC forms on the third message.
	eng.addRoundChangeLocked(rc0)
	eng.addRoundChangeLocked(rc1)
	eng.addRoundChangeLocked(rc2)

	if eng.rcQCRound != 2 {
		t.Fatalf("rcQCRound = %d, want 2", eng.rcQCRound)
	}
	hash, round, votes, ok := eng.JustifiedValue()
	if !ok {
		t.Fatal("no justified value selected")
	}
	if hash != hashB || round != 1 {
		t.Fatalf("JustifiedValue = (%x, %d), want (%x, 1)", hash[:8], round, hashB[:8])
	}
	if len(votes) != 3 {
		t.Fatalf("selected certificate has %d votes, want 3", len(votes))
	}
	if !eng.verifyPOL(5, 1, hashB, votes) {
		t.Fatal("selected certificate does not verify")
	}
}

func TestTwoPhaseLockedValueReproposedByNewEnvelopeLeader(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes []*Vote
	var rcs []*RoundChange
	commits := make([]*types.Block, 3)
	engs := make([]*BFTEngine, 3)
	for i := 0; i < 3; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit: func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast: func(v *Vote) {
				cp := *v
				votes = append(votes, &cp)
			},
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
			BroadcastRoundChange: func(rc *RoundChange) {
				cp := *rc
				rcs = append(rcs, &cp)
			},
		})
		engs[i].SetHeight(4)
	}

	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader 0 did not adopt A")
	}
	propA := lastProp
	if propA == nil || propA.Envelope == nil {
		t.Fatal("leader 0 did not broadcast an envelope")
	}
	engs[1].handleProposal(pendingProposalOf(propA))
	engs[2].handleProposal(pendingProposalOf(propA))
	deliverVotes(t, engs, &votes, types.VoteKindPrevote)
	// POL formed; precommits were cast locally. Drop them so we lock without commit.
	votes = nil
	for i, c := range commits {
		if c != nil {
			t.Fatalf("engine %d committed before the re-proposal", i)
		}
	}
	if !engs[0].VoteLockedAt(4) || engs[0].LockedProposal() == nil {
		t.Fatal("expected a POL lock on A")
	}

	rcs = nil
	for _, e := range engs {
		e.handleTimeout()
	}
	deliverRoundChanges(engs, &rcs)
	if !engs[1].IsLeader(4, 1) || !engs[1].CanPropose() {
		t.Fatal("round-1 leader must have a round-change QC")
	}

	locked := engs[1].LockedProposal()
	if locked == nil || locked.Hash != blockA.Hash {
		t.Fatal("new leader lost the locked body")
	}
	lastProp = nil
	if !engs[1].handleProposal(&pendingProposal{block: locked, local: true}) {
		t.Fatal("new envelope leader refused the locked body")
	}
	reprop := lastProp
	if reprop == nil || reprop.Envelope == nil {
		t.Fatal("missing re-proposal envelope")
	}
	if reprop.Envelope.LeaderAddr != set[1].Address {
		t.Fatalf("envelope leader = %s, want %s", reprop.Envelope.LeaderAddr, set[1].Address)
	}
	if reprop.Block.Header.ValidatorAddr != set[0].Address {
		t.Fatalf("value producer = %s, want original %s", reprop.Block.Header.ValidatorAddr, set[0].Address)
	}
	if reprop.Block.Hash != blockA.Hash {
		t.Fatal("re-proposal changed the locked value")
	}

	engs[0].handleProposal(pendingProposalOf(reprop))
	engs[2].handleProposal(pendingProposalOf(reprop))
	deliverVotes(t, engs, &votes, types.VoteKindPrevote)
	deliverVotes(t, engs, &votes, types.VoteKindPrecommit)
	for i := 0; i < 3; i++ {
		if commits[i] == nil || commits[i].Hash != blockA.Hash {
			t.Fatalf("engine %d did not commit locked A", i)
		}
	}
}

func TestTwoPhaseConflictingFinalityImpossible(t *testing.T) {
	set, keys := testSet(t, 3)
	commits := make([]*types.Block, 3)
	var lastProp *ProposalMsg
	var votes []*Vote
	engs := make([]*BFTEngine, 3)
	for i := 0; i < 3; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit:          func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast:         func(v *Vote) { cp := *v; votes = append(votes, &cp) },
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
		})
		engs[i].SetHeight(3)
	}
	blockA := proposalAt(t, set, keys, 3, 0, 1).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("propose A")
	}
	engs[1].handleProposal(pendingProposalOf(lastProp))
	engs[2].handleProposal(pendingProposalOf(lastProp))
	deliverVotes(t, engs, &votes, types.VoteKindPrevote)
	deliverVotes(t, engs, &votes, types.VoteKindPrecommit)
	for i := 0; i < 3; i++ {
		if commits[i] == nil || commits[i].Hash != blockA.Hash {
			t.Fatalf("engine %d missing commit A", i)
		}
	}

	blockB := proposalAt(t, set, keys, 3, 1, 2).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("A and B collide")
	}
	for i, eng := range engs {
		if j := eng.journal; j != nil {
			if err := j.RecordWithPOL(3, 1, blockB.Hash, 0); !errors.Is(err, ErrDoubleSign) {
				t.Fatalf("v%d journal accepted B after precommitting A: %v", i, err)
			}
		}
		if _, err := eng.castPrecommit(blockB, 1, 0); !errors.Is(err, ErrDoubleSign) {
			t.Fatalf("v%d precommitted B after A without a later POL: %v", i, err)
		}
	}
	if err := certificateAccepted(certify(blockB, nil), set); err == nil {
		t.Fatal("empty certificate for B accepted")
	}
	if err := certificateAccepted(commits[0], set); err != nil {
		t.Fatalf("certificate for A: %v", err)
	}
}

func TestTwoPhaseFourValidatorsOneDownABBEventuallyCommits(t *testing.T) {
	set, keys := testSet(t, 4)
	var lastProp *ProposalMsg
	var votes []*Vote
	var rcs []*RoundChange
	commits := make([]*types.Block, 3)
	engs := make([]*BFTEngine, 3)
	for i := 0; i < 3; i++ {
		i := i
		engs[i] = twoPhaseFor(t, set, keys, i, BFTConfig{
			OnCommit: func(b *types.Block) error { commits[i] = b; return nil },
			Broadcast: func(v *Vote) {
				cp := *v
				votes = append(votes, &cp)
			},
			BroadcastProposal: func(p *ProposalMsg) { lastProp = p },
			BroadcastRoundChange: func(rc *RoundChange) {
				cp := *rc
				rcs = append(rcs, &cp)
			},
		})
		engs[i].SetHeight(4)
	}

	// Round 0: A seen only by V0 (the old A-B-B lock shape starts here).
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !engs[0].handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("A")
	}
	votes = nil
	rcs = nil
	for _, e := range engs {
		e.handleTimeout()
	}
	deliverRoundChanges(engs, &rcs)

	// Round 1: B seen only by V1 and V2.
	blockB := proposalAt(t, set, keys, 4, 1, 22).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("A and B collide")
	}
	if !engs[1].CanPropose() {
		t.Fatal("V1 needs a round-change QC to propose B")
	}
	if !engs[1].handleProposal(&pendingProposal{block: blockB, local: true}) {
		t.Fatal("B")
	}
	propB := lastProp
	engs[2].handleProposal(pendingProposalOf(propB))
	deliverVotesTo(t, []*BFTEngine{engs[1], engs[2]}, &votes, types.VoteKindPrevote)
	// V0 never saw A as a POL or B. No one has a prevote QC (need 3).
	if engs[0].VoteLockedAt(4) || engs[1].VoteLockedAt(4) || engs[2].VoteLockedAt(4) {
		t.Fatal("disrupted prevotes must not journal-lock")
	}

	votes = nil
	rcs = nil
	for _, e := range engs {
		e.handleTimeout()
	}
	deliverRoundChanges(engs, &rcs)
	if !engs[2].IsLeader(4, 2) || !engs[2].CanPropose() {
		t.Fatal("V2 should lead round 2 with an RC QC")
	}

	blockC := proposalAt(t, set, keys, 4, 2, 33).Block
	if !engs[2].handleProposal(&pendingProposal{block: blockC, local: true}) {
		t.Fatal("C")
	}
	propC := lastProp
	engs[0].handleProposal(pendingProposalOf(propC))
	engs[1].handleProposal(pendingProposalOf(propC))
	deliverVotes(t, engs, &votes, types.VoteKindPrevote)
	deliverVotes(t, engs, &votes, types.VoteKindPrecommit)

	var committed [32]byte
	for i := 0; i < 3; i++ {
		if commits[i] == nil {
			t.Fatalf("engine %d did not commit after the A-B-B disruption", i)
		}
		if i == 0 {
			committed = commits[i].Hash
		} else if commits[i].Hash != committed {
			t.Fatalf("conflicting finality: %x vs %x", committed[:4], commits[i].Hash[:4])
		}
	}
}

func TestJournalConflictingPrecommitRequiresLaterPOL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pol.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := [32]byte{1}, [32]byte{2}
	if err := j.RecordWithPOL(9, 0, a, 0); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordWithPOL(9, 1, b, 0); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("same-round POL unlocked a different hash: %v", err)
	}
	if err := j.RecordWithPOL(9, 2, b, 2); err != nil {
		t.Fatalf("later POL refused: %v", err)
	}
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	h, r, hash, ok := j2.Last()
	if !ok || h != 9 || r != 2 || hash != b {
		t.Fatalf("reopened journal = (%d,%d,%x)", h, r, hash[:2])
	}
}

func deliverVotes(t *testing.T, engs []*BFTEngine, votes *[]*Vote, kind string) {
	t.Helper()
	deliverVotesTo(t, engs, votes, kind)
}

func deliverVotesTo(t *testing.T, engs []*BFTEngine, votes *[]*Vote, kind string) {
	t.Helper()
	batch := *votes
	*votes = nil
	for _, v := range batch {
		if v.Kind != kind {
			*votes = append(*votes, v)
			continue
		}
		for _, e := range engs {
			cp := *v
			e.handleVote(&cp)
		}
	}
}

func deliverRoundChanges(engs []*BFTEngine, rcs *[]*RoundChange) {
	batch := *rcs
	*rcs = nil
	for _, rc := range batch {
		for _, e := range engs {
			if e.localAddr == rc.ValidatorAddr {
				continue
			}
			cp := *rc
			e.handleRoundChange(&cp)
		}
	}
}

func TestVoteDigestKindSplitsTheHash(t *testing.T) {
	var addr qcrypto.Address
	legacy, err := types.VoteDigest(qcrypto.AlgMLDSA65, 1, 0, [32]byte{1}, addr, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	pre, err := types.VoteDigest(qcrypto.AlgMLDSA65, 1, 0, [32]byte{1}, addr, 1, types.VoteKindPrecommit)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == pre {
		t.Fatal("precommit digest must not collide with a legacy vote")
	}
	v := Vote{AlgID: qcrypto.AlgMLDSA65, Height: 1, BlockHash: [32]byte{1}, Timestamp: 1}
	got, err := v.Hash()
	if err != nil || got != legacy {
		t.Fatalf("Vote.Hash empty kind drifted from VoteDigest: %v", err)
	}
}
