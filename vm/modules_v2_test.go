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

// modules_v2 closed three fund-handling gaps from the September 2026 review.
// With the dex module gone, these tests pin what is left of that surface:
// validators.register refuses an Exiting record, and keeps the legacy rule
// below the fork.

func v2ForksWithModules(height uint64) forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.ModulesV2: height}
}

func ctxAt(caller qcrypto.Address, nonce, height uint64, sched forks.Schedule) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: height, Time: 1, Forks: sched}
}

// ── validators lifecycle ─────────────────────────────────────────────────────

func fundedValidator(t *testing.T, sdb *state.StateDB) (qcrypto.Address, []byte) {
	t.Helper()
	pub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	a := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(a)
	acc.AddBalance(new(big.Int).Mul(ValidatorBond(), big.NewInt(3)))
	sdb.SetAccount(acc)
	return a, pub.Bytes()
}

func balanceOf(t *testing.T, sdb *state.StateDB, a qcrypto.Address) *big.Int {
	t.Helper()
	acc, err := sdb.GetAccount(a)
	if err != nil {
		t.Fatal(err)
	}
	return new(big.Int).Set(acc.Balance)
}

// register -> unregister -> register (refused under modules_v2) ->
// finalize_exit exactly once -> register again. The bond is returned exactly
// once and the caller ends with one bond locked, not two.
func TestValidatorLifecycleUnderModulesV2(t *testing.T) {
	sdb := newTestState(t)
	v, pk := fundedValidator(t, sdb)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	sched := v2ForksWithModules(1)
	start := balanceOf(t, sdb, v)
	bond := ValidatorBond()

	if err := reg.RegisterV2(v, pk, 10); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if got := balanceOf(t, sdb, v); got.Cmp(new(big.Int).Sub(start, bond)) != 0 {
		t.Fatalf("balance after register = %s, want start - bond", got)
	}
	if err := reg.Unregister(v, 20); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	// Through the router, under the fork: refused while exiting, and nothing
	// moves.
	before := balanceOf(t, sdb, v)
	err := DispatchModuleCall(sdb, ctxAt(v, 3, 25, sched), moduleData(t, "validators", "register", map[string]string{"pubkey": hexOf(pk)}), &types.Receipt{})
	if err == nil || !strings.Contains(err.Error(), "exiting") {
		t.Fatalf("re-register while exiting was accepted: %v", err)
	}
	if IsHardModuleError(err) {
		t.Fatal("an exiting re-register must be a SOFT refusal")
	}
	if got := balanceOf(t, sdb, v); got.Cmp(before) != 0 {
		t.Fatalf("a refused register moved the balance: %s -> %s", before, got)
	}
	rec, _ := reg.GetValidator(v)
	if rec.Status != ValidatorExiting || rec.Bond.Cmp(bond) != 0 {
		t.Fatalf("record was altered by the refused register: %+v", rec)
	}

	// Too early to finalize; then finalize once; a second finalize is refused.
	if err := reg.FinalizeExit(v, 30); err == nil {
		t.Fatal("finalize before the unbonding delay was accepted")
	}
	if err := reg.FinalizeExit(v, 20+UnbondingBlocks); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if got := balanceOf(t, sdb, v); got.Cmp(start) != 0 {
		t.Fatalf("balance after finalize = %s, want the full start balance %s", got, start)
	}
	if err := reg.FinalizeExit(v, 20+UnbondingBlocks+1); err == nil {
		t.Fatal("a second finalize returned the bond twice")
	}

	// Registered again from a clean record.
	if err := DispatchModuleCall(sdb, ctxAt(v, 4, 60, sched), moduleData(t, "validators", "register", map[string]string{"pubkey": hexOf(pk)}), &types.Receipt{}); err != nil {
		t.Fatalf("register after finalize: %v", err)
	}
	if got := balanceOf(t, sdb, v); got.Cmp(new(big.Int).Sub(start, bond)) != 0 {
		t.Fatalf("balance after re-register = %s, want start - one bond", got)
	}
	active, _ := reg.ListActive()
	if len(active) != 1 || active[0].Address != v {
		t.Fatalf("active set after lifecycle = %+v", active)
	}
	pool := balanceOf(t, sdb, DeployStaking(nil))
	if pool.Cmp(bond) != 0 {
		t.Fatalf("bond pool holds %s, want exactly one bond %s", pool, bond)
	}
}

// Below the fork the legacy rule applies unchanged, so already-applied blocks
// replay the same way: an Exiting record does not refuse a new registration.
func TestValidatorRegisterKeepsLegacyRuleBelowModulesV2(t *testing.T) {
	sdb := newTestState(t)
	v, pk := fundedValidator(t, sdb)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	sched := v2ForksWithModules(1000)
	if err := reg.Register(v, pk, 10); err != nil {
		t.Fatal(err)
	}
	if err := reg.Unregister(v, 20); err != nil {
		t.Fatal(err)
	}
	if err := DispatchModuleCall(sdb, ctxAt(v, 3, 25, sched), moduleData(t, "validators", "register", map[string]string{"pubkey": hexOf(pk)}), &types.Receipt{}); err != nil {
		t.Fatalf("legacy rule changed below the fork: %v", err)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2*len(b))
	for i, c := range b {
		out[2*i] = digits[c>>4]
		out[2*i+1] = digits[c&0x0f]
	}
	return string(out)
}
