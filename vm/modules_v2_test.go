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

// modules_v2 closes the three fund-handling gaps from the September 2026
// review. These tests pin the router surface: dex remove_liquidity exists,
// is gated, and returns both assets; validators.register refuses an Exiting
// record; and both keep the legacy rule below the fork.

func v2ForksWithModules(height uint64) forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.ModulesV2: height}
}

func ctxAt(caller qcrypto.Address, nonce, height uint64, sched forks.Schedule) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: height, Time: 1, Forks: sched}
}

// dexFixture is a pool with one LP holding all the shares.
type dexFixture struct {
	sdb        *state.StateDB
	lp         qcrypto.Address
	pool       qcrypto.Address
	t0, t1     qcrypto.Address
	shares     *big.Int
	dep0, dep1 *big.Int
}

func newDexFixture(t *testing.T) *dexFixture {
	t.Helper()
	sdb := newTestState(t)
	lp := addr(0x50)
	t0, err := DeployQRC20(sdb, lp, "Alpha", "ALP", 18, big.NewInt(1_000_000), 0)
	if err != nil {
		t.Fatalf("deploy token0: %v", err)
	}
	t1, err := DeployQRC20(sdb, lp, "Beta", "BET", 18, big.NewInt(1_000_000), 1)
	if err != nil {
		t.Fatalf("deploy token1: %v", err)
	}
	pool, err := DeployPool(sdb, lp, t0, t1, 2)
	if err != nil {
		t.Fatalf("deploy pool: %v", err)
	}
	dep0, dep1 := big.NewInt(400_000), big.NewInt(100_000)
	_, _, shares, err := NewLiquidityPool(sdb, pool).AddLiquidity(lp, dep0, dep1, big.NewInt(1), big.NewInt(1))
	if err != nil {
		t.Fatalf("add liquidity: %v", err)
	}
	return &dexFixture{sdb: sdb, lp: lp, pool: pool, t0: t0, t1: t1, shares: shares, dep0: dep0, dep1: dep1}
}

func (f *dexFixture) balances(t *testing.T, who qcrypto.Address) (*big.Int, *big.Int) {
	t.Helper()
	b0, err := NewQRC20Token(f.sdb, f.t0).BalanceOf(who)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := NewQRC20Token(f.sdb, f.t1).BalanceOf(who)
	if err != nil {
		t.Fatal(err)
	}
	return b0, b1
}

func (f *dexFixture) remove(t *testing.T, height uint64, p dexRemoveLiquidityParams) error {
	t.Helper()
	sched := v2ForksWithModules(10)
	return DispatchModuleCall(f.sdb, ctxAt(f.lp, 5, height, sched), moduleData(t, "dex", "remove_liquidity", p), &types.Receipt{})
}

func TestRemoveLiquidityIsHardBeforeModulesV2AndWritesNothing(t *testing.T) {
	f := newDexFixture(t)
	lpBefore, _ := NewLiquidityPool(f.sdb, f.pool).LPBalance(f.lp)
	err := f.remove(t, 9, dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: f.shares.String()})
	if err == nil || !IsHardModuleError(err) {
		t.Fatalf("remove_liquidity below the fork must be a HARD refusal, got %v", err)
	}
	lpAfter, _ := NewLiquidityPool(f.sdb, f.pool).LPBalance(f.lp)
	if lpAfter.Cmp(lpBefore) != 0 {
		t.Fatal("a refused remove_liquidity changed the LP balance")
	}
}

func TestRemoveLiquidityReturnsBothAssetsProRata(t *testing.T) {
	f := newDexFixture(t)
	pool := NewLiquidityPool(f.sdb, f.pool)
	b0Before, b1Before := f.balances(t, f.lp)
	// The first deposit locks MinLiquidity shares at the zero address for
	// ever (the usual constant-product guard), so the LP owns total minus
	// MinLiquidity and every exit is pro rata against the TOTAL.
	totalBefore, _ := pool.TotalLPShares()
	if totalBefore.Cmp(new(big.Int).Add(f.shares, big.NewInt(MinLiquidity))) != 0 {
		t.Fatalf("precondition: total %s != lp %s + MinLiquidity %d", totalBefore, f.shares, MinLiquidity)
	}
	proRata := func(shares, total, reserve *big.Int) *big.Int {
		return new(big.Int).Div(new(big.Int).Mul(shares, reserve), total)
	}

	// Partial exit: half the LP's shares returns that share of each reserve.
	half := new(big.Int).Div(f.shares, big.NewInt(2))
	want0 := proRata(half, totalBefore, f.dep0)
	want1 := proRata(half, totalBefore, f.dep1)
	if err := f.remove(t, 10, dexRemoveLiquidityParams{
		Pool: f.pool.String(), LPShares: half.String(),
		Amount0Min: want0.String(), Amount1Min: want1.String(),
	}); err != nil {
		t.Fatalf("partial exit: %v", err)
	}
	b0, b1 := f.balances(t, f.lp)
	got0 := new(big.Int).Sub(b0, b0Before)
	got1 := new(big.Int).Sub(b1, b1Before)
	if got0.Cmp(want0) != 0 || got1.Cmp(want1) != 0 {
		t.Fatalf("partial exit returned (%s, %s), want (%s, %s)", got0, got1, want0, want1)
	}
	rem, _ := pool.LPBalance(f.lp)
	if rem.Cmp(new(big.Int).Sub(f.shares, half)) != 0 {
		t.Fatalf("LP balance after partial exit = %s", rem)
	}
	r0, r1, _ := pool.Reserves()
	if r0.Cmp(new(big.Int).Sub(f.dep0, want0)) != 0 || r1.Cmp(new(big.Int).Sub(f.dep1, want1)) != 0 {
		t.Fatalf("reserves after partial exit = (%s, %s)", r0, r1)
	}
	total, _ := pool.TotalLPShares()
	if total.Cmp(new(big.Int).Sub(totalBefore, half)) != 0 {
		t.Fatalf("total shares after partial exit = %s", total)
	}

	// Full exit of the LP's remaining shares: the LP has received its whole
	// pro-rata stake; only MinLiquidity's share stays behind, owned by the
	// zero address.
	want0 = proRata(rem, total, r0)
	want1 = proRata(rem, total, r1)
	if err := f.remove(t, 11, dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: rem.String()}); err != nil {
		t.Fatalf("full exit: %v", err)
	}
	b0, b1 = f.balances(t, f.lp)
	if new(big.Int).Sub(b0, b0Before).Cmp(new(big.Int).Add(got0, want0)) != 0 ||
		new(big.Int).Sub(b1, b1Before).Cmp(new(big.Int).Add(got1, want1)) != 0 {
		t.Fatalf("full exit did not return the LP's whole stake")
	}
	if lpBal, _ := pool.LPBalance(f.lp); lpBal.Sign() != 0 {
		t.Fatalf("LP still holds %s shares after a full exit", lpBal)
	}
	total, _ = pool.TotalLPShares()
	if total.Cmp(big.NewInt(MinLiquidity)) != 0 {
		t.Fatalf("total shares after full exit = %s, want only the locked MinLiquidity %d", total, MinLiquidity)
	}
	// What remains in the pool is exactly MinLiquidity's pro-rata share, and
	// the token balances match the reserves: nothing else is stranded.
	r0, r1, _ = pool.Reserves()
	p0, p1 := f.balances(t, f.pool)
	if p0.Cmp(r0) != 0 || p1.Cmp(r1) != 0 {
		t.Fatalf("pool token balances (%s, %s) differ from reserves (%s, %s)", p0, p1, r0, r1)
	}
	lpTotalReturned0 := new(big.Int).Sub(b0, b0Before)
	if new(big.Int).Add(lpTotalReturned0, r0).Cmp(f.dep0) != 0 {
		t.Fatalf("token0 not conserved: returned %s + reserve %s != deposit %s", lpTotalReturned0, r0, f.dep0)
	}
	lpTotalReturned1 := new(big.Int).Sub(b1, b1Before)
	if new(big.Int).Add(lpTotalReturned1, r1).Cmp(f.dep1) != 0 {
		t.Fatalf("token1 not conserved: returned %s + reserve %s != deposit %s", lpTotalReturned1, r1, f.dep1)
	}
}

func TestRemoveLiquidityRefusals(t *testing.T) {
	f := newDexFixture(t)
	pool := NewLiquidityPool(f.sdb, f.pool)
	lpBefore, _ := pool.LPBalance(f.lp)
	b0Before, b1Before := f.balances(t, f.lp)

	cases := []struct {
		name string
		p    dexRemoveLiquidityParams
		want string
	}{
		{"more shares than held", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: new(big.Int).Add(f.shares, big.NewInt(1)).String()}, "insufficient LP balance"},
		{"zero shares", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: "0"}, "positive"},
		{"token0 minimum not met", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: f.shares.String(), Amount0Min: "400001"}, "token0 below minimum"},
		{"token1 minimum not met", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: f.shares.String(), Amount1Min: "100001"}, "token1 below minimum"},
		{"deadline passed", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: f.shares.String(), Deadline: 12}, "deadline"},
		{"not a pool", dexRemoveLiquidityParams{Pool: addr(0x99).String(), LPShares: "1"}, "not a pool"},
	}
	for _, tc := range cases {
		err := f.remove(t, 20, tc.p)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
		if IsHardModuleError(err) {
			t.Fatalf("%s: a runtime refusal must be SOFT (failed receipt), got HARD", tc.name)
		}
	}
	lpAfter, _ := pool.LPBalance(f.lp)
	b0, b1 := f.balances(t, f.lp)
	if lpAfter.Cmp(lpBefore) != 0 || b0.Cmp(b0Before) != 0 || b1.Cmp(b1Before) != 0 {
		t.Fatal("a refused remove_liquidity moved shares or tokens")
	}
	// A stranger with no shares gets nothing.
	err := DispatchModuleCall(f.sdb, ctxAt(addr(0x51), 1, 20, v2ForksWithModules(10)),
		moduleData(t, "dex", "remove_liquidity", dexRemoveLiquidityParams{Pool: f.pool.String(), LPShares: "1"}), &types.Receipt{})
	if err == nil || !strings.Contains(err.Error(), "insufficient LP balance") {
		t.Fatalf("a stranger removed liquidity it never added: %v", err)
	}
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
