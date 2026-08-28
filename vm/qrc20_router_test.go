package vm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// v2Forks schedules qrc20_v2 from block 1, the tier convention.
func v2Forks() forks.Schedule { return forks.Schedule{forks.QRC20V2: 1} }

// v2Ctx is a block-1 context with qrc20_v2 active and an explicit block time.
func v2Ctx(caller qcrypto.Address, nonce uint64, blockTime int64) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: 1, Time: blockTime, Forks: v2Forks()}
}

// routerFixture is a permit fixture whose token was deployed THROUGH the
// router, so the account carries the IsContract marker the v2 guard checks.
type routerFixture struct {
	*permitFixture
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	holder := qcrypto.DeriveAddress(pub)
	sdb := newTestState(t)
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, v2Ctx(holder, 1, 1), moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Router Permit", Symbol: "RPM", Decimals: 18, TotalSupply: "1000000",
	}), dep); err != nil {
		t.Fatalf("deploy via router: %v", err)
	}
	token := *dep.ContractAddress
	return &routerFixture{&permitFixture{
		sdb: sdb, tok: NewQRC20Token(sdb, token), token: token,
		holder: holder, pub: pub, priv: priv, spender: addr(0x33),
	}}
}

func (f *routerFixture) permitParams(t *testing.T, amount *big.Int, deadline int64, withKey bool) qrc20PermitParams {
	t.Helper()
	p := qrc20PermitParams{
		Token: f.token.String(), Owner: f.holder.String(), Spender: f.spender.String(),
		Amount: amount.String(), Deadline: deadline,
		Signature: f.sign(t, f.priv, f.token, f.holder, f.spender, amount, f.permitNonce(t), deadline),
	}
	if withKey {
		p.OwnerPubKey = f.pub.Bytes()
	}
	return p
}

func (f *routerFixture) call(t *testing.T, ctx ModuleContext, action string, params any) (*types.Receipt, error) {
	t.Helper()
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(f.sdb, ctx, moduleData(t, "qrc20", action, params), rc)
}

// topics lists topic[0] of every log, for readable assertions.
func topics(rc *types.Receipt) []string {
	names := map[[32]byte]string{
		TopicTransfer: "Transfer", TopicApproval: "Approval", TopicMint: "Mint", TopicBurn: "Burn",
		TopicPaused: "Paused", TopicUnpaused: "Unpaused", TopicOwnershipTransferred: "OwnershipTransferred",
	}
	out := make([]string, 0, len(rc.Logs))
	for _, l := range rc.Logs {
		n, ok := names[l.Topics[0]]
		if !ok {
			n = fmt.Sprintf("%x", l.Topics[0][:4])
		}
		out = append(out, n)
	}
	return out
}

func wantTopics(t *testing.T, rc *types.Receipt, want ...string) {
	t.Helper()
	if got := strings.Join(topics(rc), ","); got != strings.Join(want, ",") {
		t.Fatalf("logs = [%s], want [%s]", got, strings.Join(want, ","))
	}
}

// Every v2 write action is HARD ErrForkInactive before its height — with no
// schedule, and with the fork one block ahead — and leaves no trace: no
// allowance, no permit nonce, no logs, no owner or pause change.
func TestQRC20V2ActionsGatedOffAreHardAndWriteNothing(t *testing.T) {
	f := newRouterFixture(t)
	amount := mustBig("100")
	// Signed under the real nonce so the only reason to refuse is the gate.
	permit := f.permitParams(t, amount, permitTestDeadline, true)
	spend := qrc20TokenAction{Token: f.token.String(), From: f.holder.String(), To: addr(0x66).String(), Amount: "1"}
	allow := qrc20TokenAction{Token: f.token.String(), Spender: f.spender.String(), Amount: "1"}
	own := qrc20TokenAction{Token: f.token.String(), NewOwner: addr(0x77).String()}
	only := qrc20TokenAction{Token: f.token.String()}

	actions := []struct {
		name   string
		params any
	}{
		{"permit", permit}, {"transfer_from", spend}, {"increase_allowance", allow},
		{"decrease_allowance", allow}, {"burn_from", spend}, {"transfer_ownership", own},
		{"pause", only}, {"unpause", only},
	}
	for _, sched := range []struct {
		name string
		ctx  ModuleContext
	}{
		{"no schedule", ModuleContext{Caller: f.holder, Height: 1, Time: 1}},
		{"fork one block ahead", ModuleContext{Caller: f.holder, Height: 1, Time: 1, Forks: forks.Schedule{forks.QRC20V2: 2}}},
	} {
		for _, a := range actions {
			rc, err := f.call(t, sched.ctx, a.name, a.params)
			if err == nil {
				t.Fatalf("%s/%s: accepted before the fork", sched.name, a.name)
			}
			if !IsHardModuleError(err) || !errors.Is(err, ErrForkInactive) {
				t.Fatalf("%s/%s: want HARD ErrForkInactive, got %v", sched.name, a.name, err)
			}
			if len(rc.Logs) != 0 {
				t.Fatalf("%s/%s: gated-off action emitted logs", sched.name, a.name)
			}
		}
	}
	if f.allowance(t).Sign() != 0 || f.permitNonce(t) != 0 {
		t.Fatal("gated-off actions changed allowance or permit nonce")
	}
	if owner, _ := f.tok.Owner(); owner != f.holder {
		t.Fatal("gated-off transfer_ownership changed the owner")
	}
	if paused, _ := f.tok.IsPaused(); paused {
		t.Fatal("gated-off pause paused the token")
	}
	// Admission mirrors the gate: PreflightModuleCall says the same thing.
	if _, err := PreflightModuleCall(moduleData(t, "qrc20", "permit", permit), nil, 0); err != nil {
		t.Fatalf("preflight without a schedule must not gate: %v", err)
	}
	if _, err := PreflightModuleCall(moduleData(t, "qrc20", "permit", permit), forks.Schedule{forks.QRC20V2: 2}, 1); !errors.Is(err, ErrForkInactive) {
		t.Fatalf("preflight one block early: want ErrForkInactive, got %v", err)
	}
}

// The relayer flow end to end: the owner signs a permit with a real ML-DSA-65
// key and never sends a transaction; a keyless spender submits `permit` (the
// owner has no registered key, so owner_pubkey rides along), then spends the
// allowance with `transfer_from`. The Approval from the permit, then the
// Transfer plus the allowance-decrement Approval, all land in receipts.
func TestPermitViaRouterEndToEnd(t *testing.T) {
	f := newRouterFixture(t)
	amount := mustBig("12345")
	relayer := addr(0x99)
	to := addr(0x66)

	rc, err := f.call(t, v2Ctx(relayer, 0, permitTestDeadline-100), "permit", f.permitParams(t, amount, permitTestDeadline, true))
	if err != nil {
		t.Fatalf("permit via router: %v", err)
	}
	wantTopics(t, rc, "Approval")
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}
	if f.permitNonce(t) != 1 {
		t.Fatalf("permit nonce = %d, want 1", f.permitNonce(t))
	}
	if _, err := f.sdb.GetPublicKey(f.holder); err == nil {
		t.Fatal("permit via router registered the owner's key; it must not")
	}

	// The same signature again is a SOFT replay failure (nonce moved on).
	_, err = f.call(t, v2Ctx(relayer, 1, permitTestDeadline-100), "permit", qrc20PermitParams{
		Token: f.token.String(), Owner: f.holder.String(), Spender: f.spender.String(),
		Amount: amount.String(), Deadline: permitTestDeadline,
		Signature: f.sign(t, f.priv, f.token, f.holder, f.spender, amount, 0, permitTestDeadline), OwnerPubKey: f.pub.Bytes(),
	})
	mustContain(t, err, "invalid signature")
	if IsHardModuleError(err) {
		t.Fatalf("a replayed permit is the action's own failure, must be SOFT: %v", err)
	}

	// Spender spends part of the allowance.
	rc, err = f.call(t, v2Ctx(f.spender, 0, 1), "transfer_from", qrc20TokenAction{
		Token: f.token.String(), From: f.holder.String(), To: to.String(), Amount: "12000",
	})
	if err != nil {
		t.Fatalf("transfer_from: %v", err)
	}
	wantTopics(t, rc, "Transfer", "Approval")
	if bal, _ := f.tok.BalanceOf(to); bal.Cmp(mustBig("12000")) != 0 {
		t.Fatalf("recipient balance = %s", bal)
	}
	if got := f.allowance(t); got.Cmp(mustBig("345")) != 0 {
		t.Fatalf("allowance after spend = %s, want 345", got)
	}
	// Over the remaining allowance: SOFT, nothing moves.
	_, err = f.call(t, v2Ctx(f.spender, 1, 1), "transfer_from", qrc20TokenAction{
		Token: f.token.String(), From: f.holder.String(), To: to.String(), Amount: "346",
	})
	mustContain(t, err, "allowance insufficient")
	if bal, _ := f.tok.BalanceOf(to); bal.Cmp(mustBig("12000")) != 0 {
		t.Fatalf("failed transfer_from moved tokens: %s", bal)
	}
}

// A registered owner needs no owner_pubkey on the wire; a forged key on the
// wire is ignored in favour of the registry.
func TestPermitViaRouterRegistryPath(t *testing.T) {
	f := newRouterFixture(t)
	f.registerHolderKey(t)
	amount := mustBig("77")
	if _, err := f.call(t, v2Ctx(addr(0x99), 0, 1), "permit", f.permitParams(t, amount, permitTestDeadline, false)); err != nil {
		t.Fatalf("permit without owner_pubkey for a registered owner: %v", err)
	}
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}
}

// The wire rule for the byte fields is standard base64 (Go []byte JSON): a
// value that is not base64 fails to decode and is the action's own SOFT
// "bad params", never a HARD envelope error and never a silent empty key.
func TestPermitViaRouterBadBase64(t *testing.T) {
	f := newRouterFixture(t)
	for _, field := range []string{"signature", "owner_pubkey"} {
		raw := fmt.Sprintf(`{"module":"qrc20","action":"permit","params":{"token":%q,"owner":%q,"spender":%q,"amount":"1","deadline":%d,%q:"not*base64!"}}`,
			f.token.String(), f.holder.String(), f.spender.String(), permitTestDeadline, field)
		rc := &types.Receipt{}
		err := DispatchModuleCall(f.sdb, v2Ctx(addr(0x99), 0, 1), []byte(raw), rc)
		mustContain(t, err, "bad params")
		if IsHardModuleError(err) {
			t.Fatalf("%s: bad base64 must be SOFT, got HARD %v", field, err)
		}
	}
	if f.allowance(t).Sign() != 0 || f.permitNonce(t) != 0 {
		t.Fatal("a rejected permit changed state")
	}

	// And the happy path really is base64 on the wire: hand-encode it.
	amount := mustBig("5")
	sig := f.sign(t, f.priv, f.token, f.holder, f.spender, amount, 0, permitTestDeadline)
	raw := fmt.Sprintf(`{"module":"qrc20","action":"permit","params":{"token":%q,"owner":%q,"spender":%q,"amount":"5","deadline":%d,"signature":%q,"owner_pubkey":%q}}`,
		f.token.String(), f.holder.String(), f.spender.String(), permitTestDeadline,
		base64.StdEncoding.EncodeToString(sig), base64.StdEncoding.EncodeToString(f.pub.Bytes()))
	if err := DispatchModuleCall(f.sdb, v2Ctx(addr(0x99), 0, 1), []byte(raw), &types.Receipt{}); err != nil {
		t.Fatalf("hand-encoded base64 permit: %v", err)
	}
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}
}

// The permit deadline is judged against ctx.Time, inclusive, through the
// router; deadline 0 is refused; the amount uses the strict decoder.
func TestPermitViaRouterDeadlineAndAmountRules(t *testing.T) {
	f := newRouterFixture(t)
	amount := mustBig("9")

	_, err := f.call(t, v2Ctx(addr(0x99), 0, permitTestDeadline+1), "permit", f.permitParams(t, amount, permitTestDeadline, true))
	mustContain(t, err, "expired")
	if f.permitNonce(t) != 0 {
		t.Fatal("an expired permit consumed the nonce")
	}
	_, err = f.call(t, v2Ctx(addr(0x99), 0, 1), "permit", f.permitParams(t, amount, 0, true))
	mustContain(t, err, "deadline must be positive")

	p := f.permitParams(t, amount, permitTestDeadline, true)
	p.Amount = "09"
	_, err = f.call(t, v2Ctx(addr(0x99), 0, 1), "permit", p)
	mustContain(t, err, "leading zero")

	if _, err := f.call(t, v2Ctx(addr(0x99), 0, permitTestDeadline), "permit", f.permitParams(t, amount, permitTestDeadline, true)); err != nil {
		t.Fatalf("block time == deadline must be accepted: %v", err)
	}
}

// The IsContract guard on the pre-existing actions is itself fork-gated:
// before qrc20_v2 an approve against an address nobody deployed still
// "succeeds" (legacy behaviour, unchanged); at the fork it is refused, SOFT.
func TestApproveOnBogusTokenGatedByFork(t *testing.T) {
	sdb := newTestState(t)
	caller := addr(0x11)
	bogus := addr(0xbb)
	params := qrc20TokenAction{Token: bogus.String(), Spender: addr(0x22).String(), To: addr(0x22).String(), Amount: "5"}

	pre := ModuleContext{Caller: caller, Height: 1}
	if err := DispatchModuleCall(sdb, pre, moduleData(t, "qrc20", "approve", params), &types.Receipt{}); err != nil {
		t.Fatalf("pre-fork approve on a bogus token must keep the legacy behaviour: %v", err)
	}
	early := ModuleContext{Caller: caller, Height: 1, Forks: forks.Schedule{forks.QRC20V2: 2}}
	if err := DispatchModuleCall(sdb, early, moduleData(t, "qrc20", "approve", params), &types.Receipt{}); err != nil {
		t.Fatalf("one block before the fork must keep the legacy behaviour: %v", err)
	}

	post := ModuleContext{Caller: caller, Height: 1, Forks: v2Forks()}
	for _, action := range []string{"approve", "transfer", "mint", "burn", "increase_allowance", "pause"} {
		err := DispatchModuleCall(sdb, post, moduleData(t, "qrc20", action, params), &types.Receipt{})
		mustContain(t, err, "not a deployed contract")
		if IsHardModuleError(err) {
			t.Fatalf("%s: the guard is the action's own complaint, must be SOFT: %v", action, err)
		}
	}

	// A token deployed through the router passes the guard.
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, ModuleContext{Caller: caller, Nonce: 3, Height: 1, Forks: v2Forks()}, moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Real", Symbol: "REAL", Decimals: 0, TotalSupply: "10",
	}), dep); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	params.Token = dep.ContractAddress.String()
	if err := DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "approve", params), &types.Receipt{}); err != nil {
		t.Fatalf("approve on a deployed token under the fork: %v", err)
	}
}

// increase/decrease_allowance, burn_from, transfer_ownership, pause/unpause
// through the router, with their events and their own failure modes.
func TestQRC20V2OwnerAndAllowanceActionsViaRouter(t *testing.T) {
	f := newRouterFixture(t)
	owner := f.holder
	spender := f.spender
	next := uint64(2)
	ctx := func(caller qcrypto.Address) ModuleContext {
		next++
		return v2Ctx(caller, next, 1)
	}
	allow := func(amount string) qrc20TokenAction {
		return qrc20TokenAction{Token: f.token.String(), Spender: spender.String(), Amount: amount}
	}

	rc, err := f.call(t, ctx(owner), "increase_allowance", allow("300"))
	if err != nil {
		t.Fatalf("increase_allowance: %v", err)
	}
	wantTopics(t, rc, "Approval")
	rc, err = f.call(t, ctx(owner), "decrease_allowance", allow("100"))
	if err != nil {
		t.Fatalf("decrease_allowance: %v", err)
	}
	wantTopics(t, rc, "Approval")
	if got := f.allowance(t); got.Cmp(mustBig("200")) != 0 {
		t.Fatalf("allowance = %s, want 200", got)
	}
	_, err = f.call(t, ctx(owner), "decrease_allowance", allow("201"))
	mustContain(t, err, "below zero")
	maxU256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	_, err = f.call(t, ctx(owner), "increase_allowance", allow(maxU256.String()))
	mustContain(t, err, "overflow")
	if got := f.allowance(t); got.Cmp(mustBig("200")) != 0 {
		t.Fatalf("failed allowance changes must not move it: %s", got)
	}

	// burn_from: spender burns 50 of the owner's tokens against the allowance.
	supplyBefore, _ := f.tok.TotalSupply()
	rc, err = f.call(t, ctx(spender), "burn_from", qrc20TokenAction{
		Token: f.token.String(), From: owner.String(), Amount: "50",
	})
	if err != nil {
		t.Fatalf("burn_from: %v", err)
	}
	wantTopics(t, rc, "Burn", "Approval")
	if got := f.allowance(t); got.Cmp(mustBig("150")) != 0 {
		t.Fatalf("allowance after burn_from = %s, want 150", got)
	}
	if supply, _ := f.tok.TotalSupply(); new(big.Int).Sub(supplyBefore, supply).Cmp(mustBig("50")) != 0 {
		t.Fatalf("supply not reduced by 50: %s -> %s", supplyBefore, supply)
	}
	_, err = f.call(t, ctx(addr(0x42)), "burn_from", qrc20TokenAction{
		Token: f.token.String(), From: owner.String(), Amount: "1",
	})
	mustContain(t, err, "allowance insufficient")

	// pause / unpause: owner-only, with events; a transfer is blocked between.
	_, err = f.call(t, ctx(spender), "pause", qrc20TokenAction{Token: f.token.String()})
	mustContain(t, err, "not owner")
	rc, err = f.call(t, ctx(owner), "pause", qrc20TokenAction{Token: f.token.String()})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	wantTopics(t, rc, "Paused")
	_, err = f.call(t, ctx(owner), "transfer", qrc20TokenAction{Token: f.token.String(), To: addr(0x66).String(), Amount: "1"})
	mustContain(t, err, "paused")
	rc, err = f.call(t, ctx(owner), "unpause", qrc20TokenAction{Token: f.token.String()})
	if err != nil {
		t.Fatalf("unpause: %v", err)
	}
	wantTopics(t, rc, "Unpaused")
	if _, err := f.call(t, ctx(owner), "transfer", qrc20TokenAction{Token: f.token.String(), To: addr(0x66).String(), Amount: "1"}); err != nil {
		t.Fatalf("transfer after unpause: %v", err)
	}

	// transfer_ownership: zero refused, non-owner refused, then the old owner
	// loses its powers.
	newOwner := addr(0x77)
	_, err = f.call(t, ctx(owner), "transfer_ownership", qrc20TokenAction{Token: f.token.String(), NewOwner: qcrypto.Address{}.String()})
	mustContain(t, err, "zero address")
	_, err = f.call(t, ctx(spender), "transfer_ownership", qrc20TokenAction{Token: f.token.String(), NewOwner: newOwner.String()})
	mustContain(t, err, "not owner")
	rc, err = f.call(t, ctx(owner), "transfer_ownership", qrc20TokenAction{Token: f.token.String(), NewOwner: newOwner.String()})
	if err != nil {
		t.Fatalf("transfer_ownership: %v", err)
	}
	wantTopics(t, rc, "OwnershipTransferred")
	if got, _ := f.tok.Owner(); got != newOwner {
		t.Fatalf("owner = %s, want %s", got, newOwner)
	}
	_, err = f.call(t, ctx(owner), "pause", qrc20TokenAction{Token: f.token.String()})
	mustContain(t, err, "not owner")
	if _, err := f.call(t, ctx(newOwner), "pause", qrc20TokenAction{Token: f.token.String()}); err != nil {
		t.Fatalf("new owner pause: %v", err)
	}
}

// The three v2 reads on /call: permit_nonce (what to sign next), paused, owner.
func TestQRC20V2ReadsViaRouter(t *testing.T) {
	f := newRouterFixture(t)
	read := func(action string, params any) map[string]interface{} {
		t.Helper()
		out, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc20", action, params))
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		return out.(map[string]interface{})
	}
	if got := read("permit_nonce", qrc20ReadParams{Token: f.token.String(), Owner: f.holder.String()}); got["permit_nonce"] != "0" || got["exists"] != true {
		t.Fatalf("permit_nonce = %v", got)
	}
	if got := read("paused", qrc20ReadParams{Token: f.token.String()}); got["paused"] != false {
		t.Fatalf("paused = %v", got)
	}
	if got := read("owner", qrc20ReadParams{Token: f.token.String()}); got["owner"] != f.holder.String() {
		t.Fatalf("owner = %v", got)
	}

	if _, err := f.call(t, v2Ctx(addr(0x99), 0, 1), "permit", f.permitParams(t, mustBig("1"), permitTestDeadline, true)); err != nil {
		t.Fatalf("permit: %v", err)
	}
	if _, err := f.call(t, v2Ctx(f.holder, 5, 1), "pause", qrc20TokenAction{Token: f.token.String()}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := read("permit_nonce", qrc20ReadParams{Token: f.token.String(), Owner: f.holder.String()}); got["permit_nonce"] != "1" {
		t.Fatalf("permit_nonce after permit = %v", got)
	}
	if got := read("paused", qrc20ReadParams{Token: f.token.String()}); got["paused"] != true {
		t.Fatalf("paused after pause = %v", got)
	}

	// A write action is still refused on /call, and a read on the write path.
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc20", "permit", struct{}{})); err == nil || !strings.Contains(err.Error(), "state-changing") {
		t.Fatalf("permit on /call: want a state-changing refusal, got %v", err)
	}
	err := DispatchModuleCall(f.sdb, v2Ctx(f.holder, 6, 1), moduleData(t, "qrc20", "paused", qrc20ReadParams{Token: f.token.String()}), &types.Receipt{})
	if !IsHardModuleError(err) {
		t.Fatalf("paused inside a TxCall must be HARD, got %v", err)
	}
	if _, err := PreflightModuleCall(moduleData(t, "qrc20", "paused", struct{}{}), nil, 0); err == nil || !strings.Contains(err.Error(), "read action") {
		t.Fatalf("preflight must refuse the read action: %v", err)
	}
}
