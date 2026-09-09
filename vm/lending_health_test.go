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

// The lending module's withdrawal path had a comment where the collateral
// health check should have been, and liquidation was never routed. These
// tests pin: a withdrawal that would leave debt above the borrow capacity of
// the remaining collateral is refused; one that keeps it covered passes; a
// depositor with no debt is never priced; liquidation is refused while the
// whole position is healthy and seizes the right amount once it is not.

type lendFixture struct {
	sdb             *state.StateDB
	owner, alice    qcrypto.Address
	bob, carol      qcrypto.Address
	col, debt       qcrypto.Address
	lending, oracle qcrypto.Address
	feeder          qcrypto.Address
	sched           forks.Schedule
	nonce           uint64
}

const lendHeight = uint64(10)

func newLendFixture(t *testing.T) *lendFixture {
	t.Helper()
	f := &lendFixture{
		sdb: newTestState(t), owner: addr(0x80), alice: addr(0x81), bob: addr(0x82), carol: addr(0x83), feeder: addr(0x84),
		sched: forks.Schedule{forks.ModuleRevertV1: 1, forks.LendingV1: 1, forks.OracleV1: 1, forks.QRC20V2: 1},
	}
	var err error
	// alice holds the collateral token, bob and carol the debt token.
	if f.col, err = DeployQRC20(f.sdb, f.alice, "Collateral", "COL", 18, big.NewInt(1_000_000), 0); err != nil {
		t.Fatal(err)
	}
	if f.debt, err = DeployQRC20(f.sdb, f.bob, "Debt", "DBT", 18, big.NewInt(1_000_000), 0); err != nil {
		t.Fatal(err)
	}
	if err := NewQRC20Token(f.sdb, f.debt).Transfer(f.bob, f.carol, big.NewInt(100_000)); err != nil {
		t.Fatal(err)
	}
	f.lending = DeployLending(f.sdb, f.owner, 1)
	f.oracle = DeployOracle(f.sdb, f.owner)
	lp := NewLendingProtocol(f.sdb, f.lending)
	if err := lp.SetOracle(f.owner, f.oracle); err != nil {
		t.Fatal(err)
	}
	for _, a := range []qcrypto.Address{f.col, f.debt} {
		if err := lp.EnableAsset(f.owner, a, CollateralFactorBPS, LiquidationThresholdBPS); err != nil {
			t.Fatal(err)
		}
	}
	o := NewPriceOracle(f.sdb, f.oracle)
	if err := o.RegisterFeeder(f.owner, f.feeder, make([]byte, qcrypto.PublicKeySize), "f"); err != nil {
		t.Fatal(err)
	}
	f.price(t, f.col, 100)
	f.price(t, f.debt, 100)
	return f
}

func (f *lendFixture) price(t *testing.T, asset qcrypto.Address, p int64) {
	t.Helper()
	if err := NewPriceOracle(f.sdb, f.oracle).setPrice(asset, f.feeder, &PriceFeed{Price: big.NewInt(p), BlockHeight: 1}); err != nil {
		t.Fatal(err)
	}
}

func (f *lendFixture) call(t *testing.T, caller qcrypto.Address, action string, params map[string]any) error {
	t.Helper()
	f.nonce++
	params["lending"] = f.lending.String()
	ctx := ModuleContext{Caller: caller, Nonce: f.nonce, Height: lendHeight, Time: 1, Forks: f.sched}
	return DispatchModuleCall(f.sdb, ctx, moduleData(t, "lending", action, params), &types.Receipt{})
}

func (f *lendFixture) bal(t *testing.T, token, who qcrypto.Address) *big.Int {
	t.Helper()
	b, err := NewQRC20Token(f.sdb, token).BalanceOf(who)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *lendFixture) health(t *testing.T, who qcrypto.Address) map[string]any {
	t.Helper()
	out, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "lending", "health",
		map[string]any{"lending": f.lending.String(), "owner": who.String(), "height": lendHeight}))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	return out.(map[string]any)
}

func (f *lendFixture) supplyAndBorrow(t *testing.T) {
	t.Helper()
	// bob supplies 1000 DBT; alice deposits 1000 COL and borrows 700 DBT
	// (cap at CF 75% = 750).
	if err := f.call(t, f.bob, "deposit", map[string]any{"asset": f.debt.String(), "amount": "1000"}); err != nil {
		t.Fatalf("bob deposit: %v", err)
	}
	if err := f.call(t, f.alice, "deposit", map[string]any{"asset": f.col.String(), "amount": "1000"}); err != nil {
		t.Fatalf("alice deposit: %v", err)
	}
	if err := f.call(t, f.alice, "borrow", map[string]any{"asset": f.debt.String(), "collateral": f.col.String(), "amount": "700"}); err != nil {
		t.Fatalf("alice borrow: %v", err)
	}
}

func TestLendingWithdrawRefusesToUndercollateralise(t *testing.T) {
	f := newLendFixture(t)
	f.supplyAndBorrow(t)
	colBefore := f.bal(t, f.col, f.alice)

	// Borrowing past the cap is refused (700 + 60 > 750).
	if err := f.call(t, f.alice, "borrow", map[string]any{"asset": f.debt.String(), "collateral": f.col.String(), "amount": "60"}); err == nil || !strings.Contains(err.Error(), "collateral factor") {
		t.Fatalf("over-borrow accepted: %v", err)
	}
	// Withdrawing 100 COL leaves 900 -> cap 675 < debt 700: refused, nothing moves.
	if err := f.call(t, f.alice, "withdraw", map[string]any{"asset": f.col.String(), "amount": "100"}); err == nil || !strings.Contains(err.Error(), "borrow capacity") {
		t.Fatalf("undercollateralising withdrawal accepted: %v", err)
	}
	if f.bal(t, f.col, f.alice).Cmp(colBefore) != 0 {
		t.Fatal("a refused withdrawal moved collateral")
	}
	// Withdrawing 60 leaves 940 -> cap 705 >= 700: allowed.
	if err := f.call(t, f.alice, "withdraw", map[string]any{"asset": f.col.String(), "amount": "60"}); err != nil {
		t.Fatalf("safe withdrawal refused: %v", err)
	}
	if got := f.bal(t, f.col, f.alice); got.Cmp(new(big.Int).Add(colBefore, big.NewInt(60))) != 0 {
		t.Fatalf("collateral after safe withdrawal = %s", got)
	}
	h := f.health(t, f.alice)
	if h["has_debt"] != true || h["liquidatable"] != false || h["borrow_capacity_value"] != "70500" || h["debt_value"] != "70000" {
		t.Fatalf("health = %v", h)
	}

	// Repaying everything frees the collateral entirely.
	if err := f.call(t, f.alice, "repay", map[string]any{"asset": f.debt.String(), "amount": "700"}); err != nil {
		t.Fatalf("repay: %v", err)
	}
	if err := f.call(t, f.alice, "withdraw", map[string]any{"asset": f.col.String(), "amount": "940"}); err != nil {
		t.Fatalf("withdraw after repay refused: %v", err)
	}
	if got := f.bal(t, f.col, f.alice); got.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("alice did not get all her collateral back: %s", got)
	}
}

// A depositor with no debt is never priced: with the oracle removed from the
// picture (no price for the asset) they can still withdraw.
func TestLendingWithdrawWithoutDebtNeedsNoOracle(t *testing.T) {
	f := newLendFixture(t)
	// A third asset with no oracle price at all.
	extra, err := DeployQRC20(f.sdb, f.bob, "Extra", "EXT", 18, big.NewInt(1000), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewLendingProtocol(f.sdb, f.lending).EnableAsset(f.owner, extra, CollateralFactorBPS, LiquidationThresholdBPS); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.bob, "deposit", map[string]any{"asset": extra.String(), "amount": "500"}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.bob, "withdraw", map[string]any{"asset": extra.String(), "amount": "500"}); err != nil {
		t.Fatalf("debt-free withdrawal needed the oracle: %v", err)
	}
	if got := f.bal(t, extra, f.bob); got.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("bob balance = %s", got)
	}
}

func TestLendingLiquidationOnlyWhenTheWholePositionIsUnderwater(t *testing.T) {
	f := newLendFixture(t)
	f.supplyAndBorrow(t)

	// Healthy: debt 70000 <= LT value 1000*100*0.8 = 80000. Refused.
	err := f.call(t, f.carol, "liquidate", map[string]any{"borrower": f.alice.String(), "asset": f.debt.String(), "collateral": f.col.String(), "amount": "350"})
	if err == nil || !strings.Contains(err.Error(), "healthy") {
		t.Fatalf("healthy position liquidated: %v", err)
	}
	// Self-liquidation is refused whatever the state.
	if err := f.call(t, f.alice, "liquidate", map[string]any{"borrower": f.alice.String(), "asset": f.debt.String(), "collateral": f.col.String(), "amount": "1"}); err == nil || !strings.Contains(err.Error(), "yourself") {
		t.Fatalf("self-liquidation: %v", err)
	}

	// Collateral falls to 80: LT value 1000*80*0.8 = 64000 < 70000. Liquidatable.
	f.price(t, f.col, 80)
	if h := f.health(t, f.alice); h["liquidatable"] != true {
		t.Fatalf("health after the price drop = %v", h)
	}
	carolDbt := f.bal(t, f.debt, f.carol)
	carolCol := f.bal(t, f.col, f.carol)
	// Repay 350 (exactly the 50% close factor): seizes 350*100/80 = 437 + 5% = 458 COL.
	if err := f.call(t, f.carol, "liquidate", map[string]any{"borrower": f.alice.String(), "asset": f.debt.String(), "collateral": f.col.String(), "amount": "500"}); err != nil {
		t.Fatalf("liquidate: %v", err)
	}
	if got := new(big.Int).Sub(carolDbt, f.bal(t, f.debt, f.carol)); got.Cmp(big.NewInt(350)) != 0 {
		t.Fatalf("liquidator repaid %s, want 350 (close factor)", got)
	}
	if got := new(big.Int).Sub(f.bal(t, f.col, f.carol), carolCol); got.Cmp(big.NewInt(458)) != 0 {
		t.Fatalf("liquidator seized %s COL, want 458", got)
	}
	lp := NewLendingProtocol(f.sdb, f.lending)
	if d, _ := lp.GetBorrowBalance(f.alice, f.debt); d.Cmp(big.NewInt(350)) != 0 {
		t.Fatalf("alice debt after liquidation = %s, want 350", d)
	}
	if c, _ := lp.GetDepositBalance(f.alice, f.col); c.Cmp(big.NewInt(542)) != 0 {
		t.Fatalf("alice collateral after liquidation = %s, want 542", c)
	}
	// Now healthy again: 542*80*0.8 = 34688 >= 35000? No: 34688 < 35000, still
	// slightly under; a second liquidation is allowed and repays at most 175.
	if err := f.call(t, f.carol, "liquidate", map[string]any{"borrower": f.alice.String(), "asset": f.debt.String(), "collateral": f.col.String(), "amount": "1000"}); err != nil {
		t.Fatalf("second liquidation: %v", err)
	}
	if d, _ := lp.GetBorrowBalance(f.alice, f.debt); d.Cmp(big.NewInt(175)) != 0 {
		t.Fatalf("alice debt after second liquidation = %s, want 175", d)
	}
	// 175 debt = 17500 value; collateral 542-228=314 -> LT value 314*80*0.8 = 20096 >= 17500: healthy.
	if err := f.call(t, f.carol, "liquidate", map[string]any{"borrower": f.alice.String(), "asset": f.debt.String(), "collateral": f.col.String(), "amount": "1"}); err == nil || !strings.Contains(err.Error(), "healthy") {
		t.Fatalf("a healthy position was liquidated a third time: %v", err)
	}
}

func TestLendingIsHardBelowTheFork(t *testing.T) {
	f := newLendFixture(t)
	f.sched = forks.Schedule{forks.ModuleRevertV1: 1, forks.LendingV1: 1000}
	for _, action := range []string{"deposit", "withdraw", "borrow", "repay", "liquidate"} {
		err := f.call(t, f.alice, action, map[string]any{"asset": f.col.String(), "amount": "1", "collateral": f.col.String(), "borrower": f.bob.String()})
		if err == nil || !IsHardModuleError(err) {
			t.Fatalf("%s below the fork: got %v, want HARD", action, err)
		}
	}
}
