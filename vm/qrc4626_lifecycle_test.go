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

// The vault module routed deploy and deposit only, so every deposit was a
// one-way door. These tests drive deposit -> partial withdraw -> redeem the
// rest through the router, with slippage bounds, the allowance path, and the
// solvency refusals, and pin that everything is HARD below qrc4626_v1.

type vaultFixture struct {
	sdb   *state.StateDB
	lp    qcrypto.Address
	other qcrypto.Address
	asset qcrypto.Address
	vault qcrypto.Address
	sched forks.Schedule
	nonce uint64
}

func (f *vaultFixture) ctx(caller qcrypto.Address) ModuleContext {
	f.nonce++
	return ModuleContext{Caller: caller, Nonce: f.nonce, Height: 5, Time: 1, Forks: f.sched}
}

func (f *vaultFixture) call(t *testing.T, caller qcrypto.Address, action string, params any) error {
	t.Helper()
	return DispatchModuleCall(f.sdb, f.ctx(caller), moduleData(t, "qrc4626", action, params), &types.Receipt{})
}

func (f *vaultFixture) read(t *testing.T, action string, params any) any {
	t.Helper()
	out, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc4626", action, params))
	if err != nil {
		t.Fatalf("read %s: %v", action, err)
	}
	return out
}

func (f *vaultFixture) assetBal(t *testing.T, who qcrypto.Address) *big.Int {
	t.Helper()
	b, err := NewQRC20Token(f.sdb, f.asset).BalanceOf(who)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *vaultFixture) shares(t *testing.T, who qcrypto.Address) *big.Int {
	t.Helper()
	s, err := NewQRC4626Vault(f.sdb, f.vault).ShareBalance(who)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newVaultFixture(t *testing.T) *vaultFixture {
	t.Helper()
	f := &vaultFixture{
		sdb: newTestState(t), lp: addr(0x61), other: addr(0x62),
		sched: forks.Schedule{forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC4626V1: 1},
	}
	asset, err := DeployQRC20(f.sdb, f.lp, "Under", "UND", 18, big.NewInt(1_000_000), 0)
	if err != nil {
		t.Fatalf("deploy asset: %v", err)
	}
	f.asset = asset
	// Give `other` some of the asset too.
	if err := NewQRC20Token(f.sdb, asset).Transfer(f.lp, f.other, big.NewInt(100_000)); err != nil {
		t.Fatal(err)
	}
	rc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, f.ctx(f.lp), moduleData(t, "qrc4626", "deploy", map[string]string{
		"name": "Vault", "symbol": "vUND", "asset": asset.String(),
	}), rc); err != nil {
		t.Fatalf("deploy vault: %v", err)
	}
	f.vault = *rc.ContractAddress
	return f
}

func TestVaultDepositPartialWithdrawThenRedeemAllThroughTheRouter(t *testing.T) {
	f := newVaultFixture(t)
	start := f.assetBal(t, f.lp)

	// Deposit 500_000 with a slippage floor that holds (1:1 on an empty vault).
	if err := f.call(t, f.lp, "deposit", map[string]any{"vault": f.vault.String(), "assets": "500000", "min_shares": "500000"}); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	if got := f.shares(t, f.lp); got.Cmp(big.NewInt(500_000)) != 0 {
		t.Fatalf("shares after deposit = %s, want 500000", got)
	}
	if got := f.assetBal(t, f.lp); got.Cmp(new(big.Int).Sub(start, big.NewInt(500_000))) != 0 {
		t.Fatalf("asset balance after deposit = %s", got)
	}
	if ta := f.read(t, "total_assets", map[string]string{"vault": f.vault.String()}).(*big.Int); ta.Cmp(big.NewInt(500_000)) != 0 {
		t.Fatalf("total_assets = %s", ta)
	}

	// Partial withdraw of 200_000 assets: burns 200_000 shares at 1:1.
	if err := f.call(t, f.lp, "withdraw", map[string]any{"vault": f.vault.String(), "assets": "200000", "max_shares": "200000"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := f.shares(t, f.lp); got.Cmp(big.NewInt(300_000)) != 0 {
		t.Fatalf("shares after withdraw = %s, want 300000", got)
	}
	if got := f.assetBal(t, f.lp); got.Cmp(new(big.Int).Sub(start, big.NewInt(300_000))) != 0 {
		t.Fatalf("asset balance after withdraw = %s", got)
	}

	// Redeem the rest to a different receiver.
	otherBefore := f.assetBal(t, f.other)
	if err := f.call(t, f.lp, "redeem", map[string]any{"vault": f.vault.String(), "shares": "300000", "to": f.other.String(), "min_assets": "300000"}); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got := f.shares(t, f.lp); got.Sign() != 0 {
		t.Fatalf("shares after full redeem = %s, want 0", got)
	}
	if got := f.assetBal(t, f.other); got.Cmp(new(big.Int).Add(otherBefore, big.NewInt(300_000))) != 0 {
		t.Fatalf("receiver did not get the redeemed assets: %s", got)
	}
	// Vault is empty and its books agree with its token balance.
	ta := f.read(t, "total_assets", map[string]string{"vault": f.vault.String()}).(*big.Int)
	ts := f.read(t, "total_shares", map[string]string{"vault": f.vault.String()}).(*big.Int)
	if ta.Sign() != 0 || ts.Sign() != 0 || f.assetBal(t, f.vault).Sign() != 0 {
		t.Fatalf("vault not empty after full exit: assets=%s shares=%s balance=%s", ta, ts, f.assetBal(t, f.vault))
	}
}

func TestVaultSlippageBoundsRefuseBeforeAnyTransfer(t *testing.T) {
	f := newVaultFixture(t)
	if err := f.call(t, f.lp, "deposit", map[string]any{"vault": f.vault.String(), "assets": "1000"}); err != nil {
		t.Fatal(err)
	}
	before := f.assetBal(t, f.lp)
	sharesBefore := f.shares(t, f.lp)
	cases := []struct {
		action string
		params map[string]any
		want   string
	}{
		{"deposit", map[string]any{"vault": f.vault.String(), "assets": "100", "min_shares": "101"}, "below min_shares"},
		{"mint", map[string]any{"vault": f.vault.String(), "shares": "100", "max_assets": "99"}, "above max_assets"},
		{"withdraw", map[string]any{"vault": f.vault.String(), "assets": "100", "max_shares": "99"}, "above max_shares"},
		{"redeem", map[string]any{"vault": f.vault.String(), "shares": "100", "min_assets": "101"}, "below min_assets"},
	}
	for _, tc := range cases {
		err := f.call(t, f.lp, tc.action, tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", tc.action, err, tc.want)
		}
		if IsHardModuleError(err) {
			t.Fatalf("%s: a slippage refusal must be SOFT", tc.action)
		}
	}
	if f.assetBal(t, f.lp).Cmp(before) != 0 || f.shares(t, f.lp).Cmp(sharesBefore) != 0 {
		t.Fatal("a refused action moved assets or shares")
	}
}

func TestVaultSolvencyAndAuthorizationRefusals(t *testing.T) {
	f := newVaultFixture(t)
	if err := f.call(t, f.lp, "deposit", map[string]any{"vault": f.vault.String(), "assets": "1000"}); err != nil {
		t.Fatal(err)
	}
	// More than the vault holds.
	if err := f.call(t, f.lp, "withdraw", map[string]any{"vault": f.vault.String(), "assets": "1001"}); err == nil || !strings.Contains(err.Error(), "vault holds") {
		t.Fatalf("over-withdraw accepted: %v", err)
	}
	// More shares than owned (also more than the vault holds at 1:1; either
	// refusal is correct, and nothing moves).
	if err := f.call(t, f.lp, "redeem", map[string]any{"vault": f.vault.String(), "shares": "1001"}); err == nil ||
		!(strings.Contains(err.Error(), "insufficient shares") || strings.Contains(err.Error(), "vault holds")) {
		t.Fatalf("over-redeem accepted: %v", err)
	}
	// Give `other` 1 share so an over-redeem by shares alone (not solvency) is
	// exercised too: 2 shares owned by nobody's stake.
	if err := f.call(t, f.other, "deposit", map[string]any{"vault": f.vault.String(), "assets": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, f.other, "redeem", map[string]any{"vault": f.vault.String(), "shares": "2"}); err == nil || !strings.Contains(err.Error(), "insufficient shares") {
		t.Fatalf("redeem of more shares than owned accepted: %v", err)
	}
	// A stranger cannot spend the LP's shares without allowance.
	if err := f.call(t, f.other, "redeem", map[string]any{"vault": f.vault.String(), "shares": "10", "owner": f.lp.String()}); err == nil || !strings.Contains(err.Error(), "insufficient allowance") {
		t.Fatalf("redeem without allowance accepted: %v", err)
	}
	// With allowance, exactly the approved amount, to the stranger.
	if err := f.call(t, f.lp, "approve", map[string]any{"vault": f.vault.String(), "spender": f.other.String(), "shares": "10"}); err != nil {
		t.Fatal(err)
	}
	otherBefore := f.assetBal(t, f.other)
	if err := f.call(t, f.other, "redeem", map[string]any{"vault": f.vault.String(), "shares": "10", "owner": f.lp.String()}); err != nil {
		t.Fatalf("redeem with allowance: %v", err)
	}
	if got := f.assetBal(t, f.other); got.Cmp(new(big.Int).Add(otherBefore, big.NewInt(10))) != 0 {
		t.Fatalf("spender did not receive the assets: %s", got)
	}
	if al := f.read(t, "allowance", map[string]string{"vault": f.vault.String(), "owner": f.lp.String(), "spender": f.other.String()}).(*big.Int); al.Sign() != 0 {
		t.Fatalf("allowance not consumed: %s", al)
	}
	if err := f.call(t, f.other, "redeem", map[string]any{"vault": f.vault.String(), "shares": "1", "owner": f.lp.String()}); err == nil {
		t.Fatal("allowance was spendable twice")
	}
	// Zero receiver and a non-vault address.
	if err := f.call(t, f.lp, "withdraw", map[string]any{"vault": f.vault.String(), "assets": "1", "to": (qcrypto.Address{}).String()}); err == nil || !strings.Contains(err.Error(), "zero address") {
		t.Fatalf("withdraw to zero address accepted: %v", err)
	}
	if err := f.call(t, f.lp, "deposit", map[string]any{"vault": addr(0x99).String(), "assets": "1"}); err == nil || !strings.Contains(err.Error(), "not a vault") {
		t.Fatalf("deposit into a non-vault accepted: %v", err)
	}
}

// Share price moves with yield and exits stay pro rata: after the vault's
// books record extra assets, redeeming burns fewer shares per asset and the
// previews agree with what the actions do.
func TestVaultPreviewsMatchActionsAfterYield(t *testing.T) {
	f := newVaultFixture(t)
	v := NewQRC4626Vault(f.sdb, f.vault)
	if err := f.call(t, f.lp, "deposit", map[string]any{"vault": f.vault.String(), "assets": "1000"}); err != nil {
		t.Fatal(err)
	}
	// Yield arrives: the asset lands in the vault and the books are told.
	if err := NewQRC20Token(f.sdb, f.asset).Transfer(f.lp, f.vault, big.NewInt(500)); err != nil {
		t.Fatal(err)
	}
	if err := v.ReportYield(big.NewInt(500)); err != nil {
		t.Fatal(err)
	}
	// 1000 shares now back 1500 assets. Redeeming 400 shares returns 600.
	preview := f.read(t, "preview_redeem", map[string]string{"vault": f.vault.String(), "shares": "400"}).(*big.Int)
	if preview.Cmp(big.NewInt(600)) != 0 {
		t.Fatalf("preview_redeem(400) = %s, want 600", preview)
	}
	before := f.assetBal(t, f.lp)
	if err := f.call(t, f.lp, "redeem", map[string]any{"vault": f.vault.String(), "shares": "400", "min_assets": "600"}); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got := new(big.Int).Sub(f.assetBal(t, f.lp), before); got.Cmp(preview) != 0 {
		t.Fatalf("redeem returned %s, preview said %s", got, preview)
	}
	// Withdrawing 300 assets now costs ceil(300*600/900) = 200 shares.
	pw := f.read(t, "preview_withdraw", map[string]string{"vault": f.vault.String(), "assets": "300"}).(*big.Int)
	if pw.Cmp(big.NewInt(200)) != 0 {
		t.Fatalf("preview_withdraw(300) = %s, want 200", pw)
	}
	sharesBefore := f.shares(t, f.lp)
	if err := f.call(t, f.lp, "withdraw", map[string]any{"vault": f.vault.String(), "assets": "300", "max_shares": "200"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if burned := new(big.Int).Sub(sharesBefore, f.shares(t, f.lp)); burned.Cmp(pw) != 0 {
		t.Fatalf("withdraw burned %s shares, preview said %s", burned, pw)
	}
	rate := f.read(t, "exchange_rate", map[string]string{"vault": f.vault.String()}).(map[string]string)
	if rate["assets_per_share"] != "1500000000000000000" {
		t.Fatalf("exchange_rate = %v, want 1.5 assets per share", rate)
	}
}

func TestVaultIsHardBelowTheFork(t *testing.T) {
	f := newVaultFixture(t)
	f.sched = forks.Schedule{forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC4626V1: 1000}
	for _, action := range []string{"deploy", "deposit", "mint", "withdraw", "redeem", "approve"} {
		err := f.call(t, f.lp, action, map[string]any{"vault": f.vault.String(), "assets": "1", "shares": "1"})
		if err == nil || !IsHardModuleError(err) {
			t.Fatalf("%s below the fork: got %v, want HARD", action, err)
		}
	}
}
