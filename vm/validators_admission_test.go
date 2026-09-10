package vm

import (
	"encoding/json"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func admissionForks(at uint64) forks.Schedule {
	return forks.Schedule{
		forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.ModulesV2: 1,
		forks.ValidatorsAdmissionV1: at,
	}
}

func ctxProduced(caller, producer qcrypto.Address, nonce, height uint64, sched forks.Schedule) ModuleContext {
	c := ctxAt(caller, nonce, height, sched)
	c.Producer = producer
	return c
}

func register(t *testing.T, sdb *state.StateDB, ctx ModuleContext, pk []byte) error {
	t.Helper()
	return DispatchModuleCall(sdb, ctx, moduleData(t, "validators", "register", map[string]string{"pubkey": hexOf(pk)}), &types.Receipt{})
}

func newAdmissionState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}

// Before the fork the door is open exactly as validators_v1 left it, so
// already-applied history replays unchanged.
func TestRegisterStaysOpenBelowAdmissionFork(t *testing.T) {
	sdb := newAdmissionState(t)
	v, pk := fundedValidator(t, sdb)
	if err := register(t, sdb, ctxProduced(v, addr(0x99), 1, 50, admissionForks(100)), pk); err != nil {
		t.Fatalf("below the fork an unapproved stranger must still register: %v", err)
	}
}

// The producer of the block enrols itself with nobody to approve it; that is
// how a chain's genesis validator gets into the registry.
func TestProducerMayRegisterItself(t *testing.T) {
	sdb := newAdmissionState(t)
	v, pk := fundedValidator(t, sdb)
	if err := register(t, sdb, ctxProduced(v, v, 1, 100, admissionForks(100)), pk); err != nil {
		t.Fatalf("the block producer must be able to register itself: %v", err)
	}
	ok, _ := NewStakingContract(sdb, DeployStaking(nil)).IsActive(v)
	if !ok {
		t.Fatal("producer registered but is not active")
	}
}

// A stranger with the bond is refused SOFT, keeps its SPLD, and is not
// registered. This is the C5 halt: without it that stranger would become the
// leader set.
func TestStrangerIsRefusedAndKeepsTheBond(t *testing.T) {
	sdb := newAdmissionState(t)
	v, pk := fundedValidator(t, sdb)
	before := balanceOf(t, sdb, v)
	err := register(t, sdb, ctxProduced(v, addr(0x99), 1, 100, admissionForks(100)), pk)
	if err == nil {
		t.Fatal("an unapproved stranger registered under validators_admission_v1")
	}
	if IsHardModuleError(err) {
		t.Fatalf("refusal must be SOFT (a failed receipt), got HARD: %v", err)
	}
	if !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("refusal should say why: %v", err)
	}
	if balanceOf(t, sdb, v).Cmp(before) != 0 {
		t.Fatal("a refused register moved the bond")
	}
	if ok, _ := NewStakingContract(sdb, DeployStaking(nil)).IsActive(v); ok {
		t.Fatal("refused caller is in the registry")
	}
}

// Only an active validator approves; a stranger cannot approve itself or a
// friend, and a simulation (zero producer) has no producer clause to lean on.
func TestOnlyActiveValidatorsApprove(t *testing.T) {
	sdb := newAdmissionState(t)
	sched := admissionForks(100)
	stranger, _ := fundedValidator(t, sdb)
	friend, friendPK := fundedValidator(t, sdb)
	approve := func(by, who qcrypto.Address, nonce uint64) error {
		return DispatchModuleCall(sdb, ctxProduced(by, addr(0x99), nonce, 100, sched),
			moduleData(t, "validators", "approve", map[string]string{"address": who.String()}), &types.Receipt{})
	}
	if err := approve(stranger, friend, 1); err == nil {
		t.Fatal("a non-validator approved someone")
	}
	if err := approve(stranger, stranger, 2); err == nil {
		t.Fatal("a non-validator approved itself")
	}
	// A simulation carries no producer: nobody is the producer, so the
	// producer clause must not be satisfiable by a zero-address caller.
	if err := register(t, sdb, ctxProduced(friend, qcrypto.Address{}, 1, 100, sched), friendPK); err == nil {
		t.Fatal("register with a zero producer must not pass on the producer clause")
	}
}

// approve -> register works; revoke -> register is refused again; and an
// approval granted before the fork activates counts once it is in force.
func TestApproveRegisterRevokeCycle(t *testing.T) {
	sdb := newAdmissionState(t)
	sched := admissionForks(100)
	genesis, gpk := fundedValidator(t, sdb)
	joiner, jpk := fundedValidator(t, sdb)
	if err := register(t, sdb, ctxProduced(genesis, genesis, 1, 100, sched), gpk); err != nil {
		t.Fatalf("genesis producer register: %v", err)
	}
	call := func(by qcrypto.Address, action string, who qcrypto.Address, nonce uint64) error {
		return DispatchModuleCall(sdb, ctxProduced(by, genesis, nonce, 101, sched),
			moduleData(t, "validators", action, map[string]string{"address": who.String()}), &types.Receipt{})
	}
	if err := call(genesis, "approve", joiner, 2); err != nil {
		t.Fatalf("active validator approve: %v", err)
	}
	got, err := dispatchValidatorsRead(sdb, "approved", json.RawMessage(`{"address":"`+joiner.String()+`"}`))
	if err != nil || got.(map[string]bool)["approved"] != true {
		t.Fatalf("approved read: got %v, %v", got, err)
	}
	if err := register(t, sdb, ctxProduced(joiner, genesis, 1, 101, sched), jpk); err != nil {
		t.Fatalf("approved joiner register: %v", err)
	}
	// A second joiner: approved, then revoked before it registers.
	late, lpk := fundedValidator(t, sdb)
	if err := call(genesis, "approve", late, 3); err != nil {
		t.Fatalf("approve late: %v", err)
	}
	if err := call(genesis, "revoke", late, 4); err != nil {
		t.Fatalf("revoke late: %v", err)
	}
	if err := register(t, sdb, ctxProduced(late, genesis, 1, 102, sched), lpk); err == nil {
		t.Fatal("a revoked address registered")
	}
	// approve/revoke themselves are gated by the admission fork.
	if err := DispatchModuleCall(sdb, ctxProduced(genesis, genesis, 5, 99, sched),
		moduleData(t, "validators", "approve", map[string]string{"address": late.String()}), &types.Receipt{}); err == nil || !IsHardModuleError(err) {
		t.Fatalf("approve below the fork must be a HARD refusal, got %v", err)
	}
}
