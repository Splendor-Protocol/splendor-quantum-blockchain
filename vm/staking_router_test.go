package vm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// stakingRouterFixture drives the module through the router, the way a signed
// TxCall reaches it, rather than through the Go API directly.
type stakingRouterFixture struct {
	sdb     *state.StateDB
	sched   forks.Schedule
	pub     []byte
	addr    qcrypto.Address
	genesis qcrypto.Address
}

func newStakingRouterFixture(t *testing.T) *stakingRouterFixture {
	t.Helper()
	sdb := newTestState(t)
	pub, a := validatorKey(t, 0x11)
	_, genesis := validatorKey(t, 0xAA)
	fund(t, sdb, a, spld(1_000_000))
	fund(t, sdb, genesis, spld(1_000_000))
	return &stakingRouterFixture{
		sdb:     sdb,
		sched:   forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1},
		pub:     pub,
		addr:    a,
		genesis: genesis,
	}
}

func (f *stakingRouterFixture) ctx(caller qcrypto.Address, height uint64) ModuleContext {
	return ModuleContext{
		Caller:           caller,
		Height:           height,
		Forks:            f.sched,
		GenesisValidator: f.genesis,
	}
}

func (f *stakingRouterFixture) call(t *testing.T, caller qcrypto.Address, height uint64, action string, params any) error {
	t.Helper()
	return DispatchModuleCall(f.sdb, f.ctx(caller, height), moduleData(t, "staking", action, params), &types.Receipt{})
}

func (f *stakingRouterFixture) read(t *testing.T, action string, params any) map[string]interface{} {
	t.Helper()
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{Height: 10, GenesisValidator: f.genesis},
		moduleData(t, "staking", action, params))
	if err != nil {
		t.Fatalf("staking read %s: %v", action, err)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("staking read %s returned %T", action, v)
	}
	return m
}

// The whole point of the change: a bond can be placed and read back through
// the router, by an ordinary signed transaction, with no new tx type.
func TestStakingRoutesRegisterAndStake(t *testing.T) {
	f := newStakingRouterFixture(t)

	if err := f.call(t, f.addr, 5, "register", stakingActionParams{
		PubKey: f.pub, Amount: spld(5_000).String(),
	}); err != nil {
		t.Fatalf("register through the router: %v", err)
	}
	if err := f.call(t, f.addr, 6, "add_stake", stakingActionParams{Amount: spld(1_000).String()}); err != nil {
		t.Fatalf("add_stake: %v", err)
	}

	got := f.read(t, "validator", stakingReadParams{Address: f.addr.String()})
	if got["registered"] != true {
		t.Fatalf("validator read = %v", got)
	}
	if got["stake"] != spld(6_000).String() {
		t.Errorf("stake = %v, want %s", got["stake"], spld(6_000))
	}
	if got["status"] != "active" {
		t.Errorf("status = %v", got["status"])
	}

	// Unstake, then read the pending unbonding back.
	if err := f.call(t, f.addr, 7, "unstake", stakingActionParams{Amount: spld(6_000).String()}); err != nil {
		t.Fatalf("unstake: %v", err)
	}
	got = f.read(t, "validator", stakingReadParams{Address: f.addr.String()})
	if got["unbonding"] != spld(6_000).String() {
		t.Errorf("unbonding = %v", got["unbonding"])
	}
	if got["status"] != "exiting" {
		t.Errorf("status = %v, want exiting", got["status"])
	}

	// Withdraw takes no parameters at all.
	if err := f.call(t, f.addr, 7+UnbondingBlocks, "withdraw", struct{}{}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	got = f.read(t, "validator", stakingReadParams{Address: f.addr.String()})
	if got["unbonding"] != "0" {
		t.Errorf("unbonding after withdraw = %v", got["unbonding"])
	}
}

// A key can be admitted and revoked by the admin, over the wire, and the pass
// carries the full public key it was granted to.
func TestStakingRoutesTheAdmissionRegistry(t *testing.T) {
	f := newStakingRouterFixture(t)
	yes, no := true, false

	if err := f.call(t, f.addr, 2, "set_pass", stakingActionParams{PubKey: f.pub, Passed: &yes}); err == nil {
		t.Fatal("a non-admin must not be able to grant a pass")
	}
	if err := f.call(t, f.genesis, 2, "set_pass", stakingActionParams{PubKey: f.pub, Passed: &yes}); err != nil {
		t.Fatalf("set_pass by the admin: %v", err)
	}

	got := f.read(t, "pass", stakingReadParams{PubKey: f.pub})
	if got["passed"] != true || got["source"] != "admin" || got["listed"] != true {
		t.Fatalf("pass read = %v", got)
	}
	if got["height"].(uint64) != 2 {
		t.Errorf("height = %v, want the block the grant landed in", got["height"])
	}

	// Not bonded yet, so still not eligible: the two gates are independent.
	if e := f.read(t, "can_produce", stakingReadParams{PubKey: f.pub}); e["eligible"] != false {
		t.Fatalf("an unbonded key must not be eligible: %v", e)
	}
	if err := f.call(t, f.addr, 3, "register", stakingActionParams{
		PubKey: f.pub, Amount: spld(5_000).String(),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if e := f.read(t, "can_produce", stakingReadParams{PubKey: f.pub}); e["eligible"] != true {
		t.Fatalf("passed and bonded must be eligible: %v", e)
	}

	// Revoking takes the eligibility away without touching the bond.
	if err := f.call(t, f.genesis, 4, "set_pass", stakingActionParams{PubKey: f.pub, Passed: &no}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	e := f.read(t, "can_produce", stakingReadParams{PubKey: f.pub})
	if e["eligible"] != false {
		t.Fatalf("a revoked key must not be eligible: %v", e)
	}
	if !strings.Contains(e["reason"].(string), "revoked") {
		t.Errorf("reason = %v, it should say the pass was revoked", e["reason"])
	}
	if v := f.read(t, "validator", stakingReadParams{Address: f.addr.String()}); v["stake"] != spld(5_000).String() {
		t.Errorf("revoking a pass must not touch the bond: %v", v["stake"])
	}
}

// A missing `passed` must not read as a revocation: the flag is a pointer for
// exactly this reason.
func TestStakingSetPassRequiresTheFlag(t *testing.T) {
	f := newStakingRouterFixture(t)
	err := f.call(t, f.genesis, 2, "set_pass", stakingActionParams{PubKey: f.pub})
	if err == nil {
		t.Fatal("set_pass without `passed` must be refused")
	}
	if IsHardModuleError(err) {
		t.Errorf("a missing field is the action's own complaint, so SOFT: %v", err)
	}
}

// Before the fork every staking action is HARD: statically detectable at
// admission, so an honest producer never packs one, and a follower without the
// rule rejects the block rather than applying it differently.
func TestStakingIsHardBeforeTheFork(t *testing.T) {
	f := newStakingRouterFixture(t)
	f.sched = forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 100}

	for _, action := range []string{"register", "add_stake", "unstake", "withdraw", "unjail", "slash", "set_pass", "set_admin"} {
		err := f.call(t, f.addr, 99, action, stakingActionParams{PubKey: f.pub, Amount: "1"})
		if err == nil {
			t.Fatalf("%s one block early must be refused", action)
		}
		if !errors.Is(err, ErrForkInactive) || !IsHardModuleError(err) {
			t.Errorf("%s: want HARD ErrForkInactive, got %v", action, err)
		}
	}
	// And an unknown action is HARD too, at any height.
	err := f.call(t, f.addr, 100, "explode", struct{}{})
	if err == nil || !IsHardModuleError(err) {
		t.Errorf("an unknown staking action must be HARD, got %v", err)
	}
}

// Everything an action itself refuses is SOFT: a failed receipt, the fee kept
// and the nonce consumed, never a jammed account.
func TestStakingActionFailuresAreSoft(t *testing.T) {
	f := newStakingRouterFixture(t)
	unfunded := addr(0x7E)

	for name, err := range map[string]error{
		"below the minimum": f.call(t, f.addr, 1, "register", stakingActionParams{PubKey: f.pub, Amount: "1"}),
		"unfunded staker":   f.call(t, unfunded, 1, "register", stakingActionParams{PubKey: f.pub, Amount: spld(5_000).String()}),
		"never registered":  f.call(t, f.addr, 1, "add_stake", stakingActionParams{Amount: "1"}),
		"nothing unbonding": f.call(t, f.addr, 1, "withdraw", struct{}{}),
		"not the admin":     f.call(t, f.addr, 1, "set_admin", stakingActionParams{Admin: addr(0x5A).String()}),
		"bad amount":        f.call(t, f.addr, 1, "unstake", stakingActionParams{Amount: "not a number"}),
	} {
		if err == nil {
			t.Errorf("%s: expected a refusal", name)
			continue
		}
		if IsHardModuleError(err) {
			t.Errorf("%s: an action's own error must be SOFT, got HARD: %v", name, err)
		}
	}
}

// /call serves views only. A staking write there would run an admin action for
// an unauthenticated caller and report success on state that never lands.
func TestStakingWritesAreRefusedOnTheReadPath(t *testing.T) {
	f := newStakingRouterFixture(t)
	for _, action := range []string{"register", "set_pass", "slash", "set_admin"} {
		_, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "staking", action, struct{}{}))
		if err == nil || !strings.Contains(err.Error(), "state-changing") {
			t.Errorf("%s on /call = %v, want a state-changing refusal", action, err)
		}
	}
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "staking", "nonsense", struct{}{})); err == nil {
		t.Error("an unknown read action must be refused")
	}
}

// staking_info is the operator's one-shot answer to "what are the rules here".
func TestStakingInfoReportsTheRules(t *testing.T) {
	f := newStakingRouterFixture(t)
	got := f.read(t, "staking_info", struct{}{})
	if got["min_stake"] != MinValidatorStake.String() {
		t.Errorf("min_stake = %v", got["min_stake"])
	}
	if got["unbonding_blocks"] != UnbondingBlocks {
		t.Errorf("unbonding_blocks = %v", got["unbonding_blocks"])
	}
	if got["admin"] != f.genesis.String() {
		t.Errorf("admin = %v, want the genesis validator before anyone sets one", got["admin"])
	}
	if got["initialised"] != false {
		t.Errorf("initialised = %v on an untouched registry", got["initialised"])
	}
	if got["contract"] != StakingAddress.String() {
		t.Errorf("contract = %v", got["contract"])
	}
}

// The set is never empty on a running chain: the genesis validator is in it
// before anyone writes an entry, because it is the only key that can produce.
func TestValidatorSetIncludesTheGenesisValidator(t *testing.T) {
	f := newStakingRouterFixture(t)
	got := f.read(t, "validator_set", struct{}{})
	rows, ok := got["validators"].([]map[string]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("validator_set = %v", got)
	}
	if rows[0]["address"] != f.genesis.String() || rows[0]["source"] != "genesis" || rows[0]["eligible"] != true {
		t.Fatalf("genesis row = %v", rows[0])
	}

	yes := true
	if err := f.call(t, f.genesis, 2, "set_pass", stakingActionParams{PubKey: f.pub, Passed: &yes}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	got = f.read(t, "validator_set", struct{}{})
	rows = got["validators"].([]map[string]interface{})
	if len(rows) != 2 {
		t.Fatalf("want the genesis row plus the granted one, got %v", rows)
	}
	if rows[1]["address"] != f.addr.String() || rows[1]["eligible"] != false {
		t.Fatalf("granted row = %v (it holds no bond yet)", rows[1])
	}
}

// Reads must not answer with a confidence they do not have: without the
// block-0 anchor the genesis key's implicit admission is unknowable, and the
// answer says so rather than reporting a refusal.
func TestStakingReadsFlagAMissingGenesisAnchor(t *testing.T) {
	f := newStakingRouterFixture(t)
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "staking", "can_produce",
		stakingReadParams{Address: f.genesis.String()}))
	if err != nil {
		t.Fatalf("can_produce: %v", err)
	}
	got := v.(map[string]interface{})
	if got["genesis_anchor_known"] != false {
		t.Errorf("genesis_anchor_known = %v, want false", got["genesis_anchor_known"])
	}
	if got["eligible"] != false {
		t.Errorf("eligible = %v", got["eligible"])
	}
}

// A public key arrives as standard base64, like a transaction's public_key,
// and naming an address that contradicts it is an error rather than a silent
// preference for one of the two.
func TestStakingReadResolvesKeyAndAddress(t *testing.T) {
	f := newStakingRouterFixture(t)
	raw := map[string]string{
		"pubkey":  base64.StdEncoding.EncodeToString(f.pub),
		"address": addr(0x01).String(),
	}
	body, err := json.Marshal(ModuleCall{Module: "staking", Action: "pass", Params: mustJSON(t, raw)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, body); err == nil ||
		!strings.Contains(err.Error(), "not to the address given") {
		t.Fatalf("a pubkey and address that disagree must be refused, got %v", err)
	}

	raw["address"] = f.addr.String()
	body, _ = json.Marshal(ModuleCall{Module: "staking", Action: "pass", Params: mustJSON(t, raw)})
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, body); err != nil {
		t.Fatalf("agreeing pubkey and address: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// Admission (/tx/submit preflight) must accept every staking write at the
// activation height and refuse it before, or a producer would pack a tx its
// own followers reject.
func TestStakingPreflight(t *testing.T) {
	sched := forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 50}
	data, err := json.Marshal(ModuleCall{Module: "staking", Action: "register", Params: mustJSON(t, stakingActionParams{
		Amount: spld(5_000).String(),
	})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := PreflightModuleCall(data, sched, 49); !errors.Is(err, ErrForkInactive) {
		t.Fatalf("one block early = %v, want ErrForkInactive", err)
	}
	if _, err := PreflightModuleCall(data, sched, 50); err != nil {
		t.Fatalf("at the activation height: %v", err)
	}
	// A read action inside a signed TxCall must be refused at admission: the
	// write dispatch would treat it as HARD and jam the sender's nonce.
	readData, _ := json.Marshal(ModuleCall{Module: "staking", Action: "can_produce", Params: mustJSON(t, struct{}{})})
	if _, err := PreflightModuleCall(readData, sched, 50); err == nil {
		t.Fatal("a read action must not pass write admission")
	}
}

// Slashing is admin-only through the router too, and the coins it takes stay
// where nobody can reach them.
func TestStakingRoutesSlash(t *testing.T) {
	f := newStakingRouterFixture(t)
	if err := f.call(t, f.addr, 1, "register", stakingActionParams{
		PubKey: f.pub, Amount: spld(5_000).String(),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	slash := stakingActionParams{Validator: f.addr.String(), Amount: spld(1_000).String()}
	if err := f.call(t, f.addr, 2, "slash", slash); err == nil {
		t.Fatal("a non-admin must not be able to slash")
	}
	if err := f.call(t, f.genesis, 2, "slash", slash); err != nil {
		t.Fatalf("slash by the admin: %v", err)
	}
	got := f.read(t, "validator", stakingReadParams{Address: f.addr.String()})
	if got["status"] != "jailed" || got["stake"] != spld(4_000).String() {
		t.Fatalf("after slash: %v", got)
	}
	info := f.read(t, "staking_info", struct{}{})
	if info["slashed_total"] != spld(1_000).String() {
		t.Errorf("slashed_total = %v", info["slashed_total"])
	}
	if info["escrow_balance"] != spld(5_000).String() {
		t.Errorf("escrow = %v, the slashed coins must still be held", info["escrow_balance"])
	}

	// Unjail comes back through the router, with the optional top-up omitted.
	if err := f.call(t, f.addr, 3, "unjail", stakingActionParams{}); err != nil {
		t.Fatalf("unjail: %v", err)
	}
	if got = f.read(t, "validator", stakingReadParams{Address: f.addr.String()}); got["status"] != "active" {
		t.Errorf("status after unjail = %v", got["status"])
	}
}

// Handing the authority on works over the wire, and the module refuses a
// holder no key could ever act as.
func TestStakingRoutesSetAdmin(t *testing.T) {
	f := newStakingRouterFixture(t)
	if err := f.call(t, f.genesis, 1, "set_admin", stakingActionParams{Admin: ModuleRouterAddress.String()}); err == nil {
		t.Fatal("the module router must be refused as admin")
	}
	next := addr(0x5A)
	if err := f.call(t, f.genesis, 1, "set_admin", stakingActionParams{Admin: next.String()}); err != nil {
		t.Fatalf("set_admin: %v", err)
	}
	if got := f.read(t, "staking_info", struct{}{}); got["admin"] != next.String() {
		t.Errorf("admin = %v, want %s", got["admin"], next)
	}
	yes := true
	if err := f.call(t, f.genesis, 2, "set_pass", stakingActionParams{PubKey: f.pub, Passed: &yes}); err == nil {
		t.Fatal("the old admin must have lost the authority")
	}
}

// The module must never see a mutated tier constant: they are package-level
// *big.Int, so one arithmetic slip would move the floor for the whole process.
func TestStakingLeavesTheTierConstantsAlone(t *testing.T) {
	f := newStakingRouterFixture(t)
	before := new(big.Int).Set(StakeBronze)
	if err := f.call(t, f.addr, 1, "register", stakingActionParams{
		PubKey: f.pub, Amount: spld(900_000).String(),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.call(t, f.genesis, 2, "slash", stakingActionParams{
		Validator: f.addr.String(), Amount: spld(9_000_000).String(),
	}); err != nil {
		t.Fatalf("slash: %v", err)
	}
	if StakeBronze.Cmp(before) != 0 {
		t.Fatalf("StakeBronze is now %s, was %s", StakeBronze, before)
	}
}
