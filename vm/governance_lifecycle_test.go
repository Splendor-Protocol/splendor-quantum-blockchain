package vm

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Governance is advisory in this build and now says so: only text proposals
// are accepted, voting power is the active validator bond (nothing ever set
// the old power slots, so nobody could propose), tally is routed and judged
// against the active set's total bond, and Execute refuses instead of marking
// a proposal "executed" while enacting nothing.

type govFixture struct {
	sdb    *state.StateDB
	v      []qcrypto.Address // registered validators
	nobody qcrypto.Address
	sched  forks.Schedule
	nonce  uint64
}

func newGovFixture(t *testing.T, validators int) *govFixture {
	t.Helper()
	f := &govFixture{sdb: newTestState(t), nobody: addr(0x71),
		sched: forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.GovernanceV1: 1}}
	reg := NewStakingContract(f.sdb, DeployStaking(nil))
	for i := 0; i < validators; i++ {
		a, pk := fundedValidator(t, f.sdb)
		if err := reg.Register(a, pk, 1); err != nil {
			t.Fatal(err)
		}
		f.v = append(f.v, a)
	}
	return f
}

func (f *govFixture) call(t *testing.T, caller qcrypto.Address, height uint64, action string, params any) error {
	t.Helper()
	f.nonce++
	ctx := ModuleContext{Caller: caller, Nonce: f.nonce, Height: height, Time: 1, Forks: f.sched}
	return DispatchModuleCall(f.sdb, ctx, moduleData(t, "governance", action, params), &types.Receipt{})
}

func (f *govFixture) proposal(t *testing.T, id uint64) *Proposal {
	t.Helper()
	p, err := NewGovernanceContract(f.sdb, DeployGovernance(nil)).GetProposal(id)
	if err != nil || p == nil {
		t.Fatalf("proposal %d: %v", id, err)
	}
	return p
}

func TestGovernanceAdvisoryLifecycle(t *testing.T) {
	f := newGovFixture(t, 3)
	const submitAt = uint64(100)
	voteAt := submitAt + DiscussionBlocks + 1
	tallyAt := submitAt + DiscussionBlocks + VotingBlocks + 1

	// Only an active validator can propose.
	if err := f.call(t, f.nobody, submitAt, "submit", map[string]string{"title": "x", "description": "y"}); err == nil || !strings.Contains(err.Error(), "voting power") {
		t.Fatalf("a non-validator proposed: %v", err)
	}
	if err := f.call(t, f.v[0], submitAt, "submit", map[string]string{"title": "Raise the fee floor", "description": "signal"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	p := f.proposal(t, 1)
	if p.Type != ProposalTypeText || p.Status != ProposalPending {
		t.Fatalf("proposal = %+v", p)
	}

	// Too early to vote; a non-validator cannot vote; bad option refused.
	if err := f.call(t, f.v[1], submitAt+1, "vote", map[string]any{"id": 1, "option": 1}); err == nil || !strings.Contains(err.Error(), "not yet") {
		t.Fatalf("early vote accepted: %v", err)
	}
	if err := f.call(t, f.nobody, voteAt, "vote", map[string]any{"id": 1, "option": 1}); err == nil || !strings.Contains(err.Error(), "voting power") {
		t.Fatalf("non-validator vote accepted: %v", err)
	}
	if err := f.call(t, f.v[1], voteAt, "vote", map[string]any{"id": 1, "option": 9}); err == nil || !strings.Contains(err.Error(), "invalid vote option") {
		t.Fatalf("bad option accepted: %v", err)
	}

	// Two of three vote yes; weights are one bond each.
	bond := ValidatorBond()
	if err := f.call(t, f.v[0], voteAt, "vote", map[string]any{"id": 1, "option": 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.v[1], voteAt+1, "vote", map[string]any{"id": 1, "option": 2}); err != nil {
		t.Fatal(err)
	}
	// v[1] changes its mind: the old weight is removed, not double counted.
	if err := f.call(t, f.v[1], voteAt+2, "vote", map[string]any{"id": 1, "option": 1}); err != nil {
		t.Fatal(err)
	}
	p = f.proposal(t, 1)
	if p.YesVotes.Cmp(new(big.Int).Mul(bond, big.NewInt(2))) != 0 || p.NoVotes.Sign() != 0 || p.TotalVotes.Cmp(new(big.Int).Mul(bond, big.NewInt(2))) != 0 {
		t.Fatalf("tally after re-vote: yes=%s no=%s total=%s", p.YesVotes, p.NoVotes, p.TotalVotes)
	}

	// Tally too early, then at the right height; anyone may close it.
	if err := f.call(t, f.nobody, voteAt+3, "tally", map[string]any{"id": 1}); err == nil || !strings.Contains(err.Error(), "not ended") {
		t.Fatalf("early tally accepted: %v", err)
	}
	if err := f.call(t, f.nobody, tallyAt, "tally", map[string]any{"id": 1}); err != nil {
		t.Fatalf("tally: %v", err)
	}
	if p = f.proposal(t, 1); p.Status != ProposalPassed {
		t.Fatalf("status after tally = %s, want passed", ProposalStatusName(p.Status))
	}
	// A second tally is refused; a late vote is refused.
	if err := f.call(t, f.nobody, tallyAt+1, "tally", map[string]any{"id": 1}); err == nil || !strings.Contains(err.Error(), "already tallied") {
		t.Fatalf("double tally: %v", err)
	}
	if err := f.call(t, f.v[2], tallyAt+1, "vote", map[string]any{"id": 1, "option": 1}); err == nil {
		t.Fatal("a vote after the voting period was accepted")
	}

	// Passed is terminal: nothing "executes". The status never reads executed.
	g := NewGovernanceContract(f.sdb, DeployGovernance(nil))
	if err := g.Execute(1, tallyAt+ExecutionDelayBlocks+1); err == nil || !strings.Contains(err.Error(), "advisory") {
		t.Fatalf("Execute did not refuse: %v", err)
	}
	if p = f.proposal(t, 1); p.Status != ProposalPassed {
		t.Fatalf("Execute changed the status to %s", ProposalStatusName(p.Status))
	}
	// The read carries the label.
	out, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "governance", "proposal", map[string]any{"id": 1}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["advisory"] != true || m["status_name"] != "passed" {
		t.Fatalf("proposal read = %v", m)
	}
	// execute is not a routed action at all.
	if err := f.call(t, f.v[0], tallyAt+1, "execute", map[string]any{"id": 1}); err == nil || !IsHardModuleError(err) {
		t.Fatalf("execute is routed: %v", err)
	}
}

func TestGovernanceVetoAndCancel(t *testing.T) {
	f := newGovFixture(t, 3)
	const submitAt = uint64(100)
	voteAt := submitAt + DiscussionBlocks + 1
	tallyAt := submitAt + DiscussionBlocks + VotingBlocks + 1

	if err := f.call(t, f.v[0], submitAt, "submit", map[string]string{"title": "veto me", "description": ""}); err != nil {
		t.Fatal(err)
	}
	// Two yes, one no-with-veto: veto share is 1/3 exactly, NOT above it, so
	// the supermajority test decides: yes/(yes+no+veto) = 2/3 = 66.66% < 66.67% -> rejected.
	for i, opt := range []int{1, 1, 4} {
		if err := f.call(t, f.v[i], voteAt+uint64(i), "vote", map[string]any{"id": 1, "option": opt}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.call(t, f.nobody, tallyAt, "tally", map[string]any{"id": 1}); err != nil {
		t.Fatal(err)
	}
	if p := f.proposal(t, 1); p.Status != ProposalRejected {
		t.Fatalf("status = %s, want rejected", ProposalStatusName(p.Status))
	}

	// Cancel: proposer only, and only before voting starts.
	if err := f.call(t, f.v[1], submitAt+1, "submit", map[string]string{"title": "cancel me", "description": ""}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.v[0], submitAt+2, "cancel", map[string]any{"id": 2}); err == nil || !strings.Contains(err.Error(), "only the proposer") {
		t.Fatalf("non-proposer cancelled: %v", err)
	}
	if err := f.call(t, f.v[1], submitAt+2, "cancel", map[string]any{"id": 2}); err != nil {
		t.Fatalf("proposer cancel: %v", err)
	}
	if err := f.call(t, f.v[0], voteAt, "vote", map[string]any{"id": 2, "option": 1}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("vote on a cancelled proposal accepted: %v", err)
	}
	if err := f.call(t, f.v[1], submitAt+3, "submit", map[string]string{"title": "no cancel after start", "description": ""}); err != nil {
		t.Fatal(err)
	}
	vote3At := submitAt + 3 + DiscussionBlocks + 1
	if err := f.call(t, f.v[0], vote3At, "vote", map[string]any{"id": 3, "option": 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.v[1], vote3At+1, "cancel", map[string]any{"id": 3}); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("cancel after voting started accepted: %v", err)
	}
}

func TestGovernanceIsHardBelowTheFork(t *testing.T) {
	f := newGovFixture(t, 1)
	f.sched = forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.GovernanceV1: 1000}
	for _, action := range []string{"submit", "vote", "tally", "cancel"} {
		err := f.call(t, f.v[0], 5, action, map[string]any{"id": 1, "title": "x"})
		if err == nil || !IsHardModuleError(err) {
			t.Fatalf("%s below the fork: got %v, want HARD", action, err)
		}
	}
}
