package vm

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// exForks is the tier convention: every settlement name from block 1.
func exForks() forks.Schedule {
	return forks.Schedule{forks.ExchangeV1: 1, forks.QRC1155V1: 1, forks.QRC20V2: 1, forks.ModuleRevertV1: 1}
}

// rctx is the fixture context with exchange_v1 scheduled, for router calls.
func (f *exFixture) rctx(caller qcrypto.Address) ModuleContext {
	c := f.ctx(caller)
	c.Forks = exForks()
	return c
}

// call runs an exchange write through DispatchModuleCall.
func (f *exFixture) call(t *testing.T, caller qcrypto.Address, action string, params any) (*types.Receipt, error) {
	t.Helper()
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(f.sdb, f.rctx(caller), moduleData(t, "exchange", action, params), rc)
}

// read runs an exchange read through DispatchModuleReadCall at the fixture's
// block time.
func (f *exFixture) read(t *testing.T, action string, params any) (map[string]interface{}, error) {
	t.Helper()
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{Height: 1, Time: f.time}, moduleData(t, "exchange", action, params))
	if err != nil {
		return nil, err
	}
	return v.(map[string]interface{}), nil
}

func (f *exFixture) mustRead(t *testing.T, action string, params any) map[string]interface{} {
	t.Helper()
	out, err := f.read(t, action, params)
	if err != nil {
		t.Fatalf("read %s: %v", action, err)
	}
	return out
}

// exParams is the params object for the simple exchange writes.
func (f *exFixture) exParams(extra map[string]any) map[string]any {
	p := map[string]any{"exchange": f.x.addr.String()}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// Every exchange write is HARD fork-inactive with no schedule and one block
// before its height, writing nothing, and reachable (SOFT or success) from the
// scheduled height on.
func TestExchangeRouterForkMatrix(t *testing.T) {
	f := newExFixture(t)
	acts := KnownModuleActions["exchange"]
	if len(acts.Writes) != 16 || len(acts.Reads) != 10 {
		t.Fatalf("exchange lists %d writes and %d reads, want 16 and 10", len(acts.Writes), len(acts.Reads))
	}
	for _, action := range sortedKeys(acts.Writes) {
		if acts.Writes[action] != forks.ExchangeV1 {
			t.Errorf("%s is gated by %q, want exchange_v1", action, acts.Writes[action])
		}
		data := moduleData(t, "exchange", action, f.exParams(nil))
		before, _ := f.sdb.PendingStateRoot()
		for name, sched := range map[string]forks.Schedule{
			"nil":       nil,
			"one early": {forks.ExchangeV1: 2},
		} {
			ctx := ModuleContext{Caller: f.owner, Nonce: 5, Height: 1, Time: f.time, Forks: sched}
			err := DispatchModuleCall(f.sdb, ctx, data, &types.Receipt{})
			if err == nil || !IsHardModuleError(err) || !errors.Is(err, ErrForkInactive) {
				t.Errorf("%s with %s schedule: want HARD ErrForkInactive, got %v", action, name, err)
			}
		}
		if after, _ := f.sdb.PendingStateRoot(); after != before {
			t.Fatalf("%s wrote state while gated", action)
		}
		ctx := ModuleContext{Caller: f.owner, Nonce: 5, Height: 1, Time: f.time, Forks: forks.Schedule{forks.ExchangeV1: 1}}
		if err := DispatchModuleCall(f.sdb, ctx, data, &types.Receipt{}); err != nil {
			if IsHardModuleError(err) {
				t.Errorf("%s at the scheduled height: want SOFT or success, got HARD %v", action, err)
			}
			if !strings.HasPrefix(err.Error(), "exchange "+action+": ") {
				t.Errorf("%s: error lacks the stable prefix: %v", action, err)
			}
		}
	}
	// A read inside a write call is HARD, like every unknown action.
	err := DispatchModuleCall(f.sdb, f.rctx(f.owner), moduleData(t, "exchange", "exchange_info", f.exParams(nil)), &types.Receipt{})
	if !IsHardModuleError(err) {
		t.Fatalf("read action in a TxCall must be HARD, got %v", err)
	}
	// A write through the read dispatch is refused before any module code.
	if _, err := f.read(t, "split", f.exParams(map[string]any{"condition": f.cond, "amount": "1"})); err == nil || !strings.Contains(err.Error(), "state-changing") {
		t.Fatalf("write via /call: want refusal, got %v", err)
	}
}

// Deploy through the router: the receipt carries the height-bound address,
// the account is a contract with a module-managed collection, and the
// instance is then usable through the router.
func TestExchangeRouterDeploy(t *testing.T) {
	f := newExFixture(t)
	rc := &types.Receipt{}
	ctx := ModuleContext{Caller: f.owner, Nonce: 7, Height: 3, Time: f.time, Forks: exForks()}
	if err := DispatchModuleCall(f.sdb, ctx, moduleData(t, "exchange", "deploy", map[string]any{
		"name": "Router Positions", "redeem_fee_bps": 100, "fee_recipient": f.carol.addr.String(),
	}), rc); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if rc.ContractAddress == nil || *rc.ContractAddress != ExchangeAddress(f.owner, 7, 3) {
		t.Fatalf("deploy address = %v, want %s", rc.ContractAddress, ExchangeAddress(f.owner, 7, 3))
	}
	addr := *rc.ContractAddress
	if acc, _ := f.sdb.GetAccount(addr); !acc.IsContract {
		t.Fatal("deployed exchange is not marked is_contract")
	}
	if managed, _ := NewQRC1155Token(f.sdb, addr).IsModuleManaged(); !managed {
		t.Fatal("position collection is not module-managed")
	}
	info := f.mustRead(t, "exchange_info", map[string]any{"exchange": addr.String()})
	if info["owner"] != f.owner.String() || info["fee_recipient"] != f.carol.addr.String() || info["redeem_fee_bps"] != uint64(100) || info["exists"] != true {
		t.Fatalf("exchange_info = %v", info)
	}

	// Bad params are SOFT and prefixed; the address stays free.
	bad := ModuleContext{Caller: f.owner, Nonce: 8, Height: 3, Time: f.time, Forks: exForks()}
	err := DispatchModuleCall(f.sdb, bad, moduleData(t, "exchange", "deploy", map[string]any{"redeem_fee_bps": MaxExchangeRedeemFeeBps + 1}), &types.Receipt{})
	mustSoft(t, err, "exceeds the cap")
	err = DispatchModuleCall(f.sdb, bad, []byte(`{"module":"exchange","action":"deploy","params":"[]"}`), &types.Receipt{})
	mustSoft(t, err, "bad params")
	if owner, _ := NewExchange(f.sdb, ExchangeAddress(f.owner, 8, 3)).Owner(); !owner.IsZero() {
		t.Fatal("a refused deploy left an owner behind")
	}

	// The new instance is driven through the router like any other.
	x := NewExchange(f.sdb, addr)
	rctx := func(c qcrypto.Address) ModuleContext {
		return ModuleContext{Caller: c, Height: 3, Time: f.time, Forks: exForks()}
	}
	if err := DispatchModuleCall(f.sdb, rctx(f.owner), moduleData(t, "exchange", "set_operator", map[string]any{
		"exchange": addr.String(), "operator": f.operator.String(),
	}), &types.Receipt{}); err == nil || !strings.Contains(err.Error(), "active is required") {
		t.Fatalf("set_operator without active must be refused, got %v", err)
	}
	if ok, _ := x.IsOperator(f.operator); ok {
		t.Fatal("a refused set_operator granted")
	}
	rc = &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, rctx(f.owner), moduleData(t, "exchange", "set_operator", map[string]any{
		"exchange": addr.String(), "operator": f.operator.String(), "active": true,
	}), rc); err != nil {
		t.Fatalf("set_operator: %v", err)
	}
	wantExTopics(t, rc, "OperatorSet")
	rc = &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, rctx(f.operator), moduleData(t, "exchange", "create_condition", map[string]any{
		"exchange": addr.String(), "collateral": f.token.String(), "resolver": f.resolver.String(),
		"end_time": exEndTime, "outcome_count": 2, "question_hash": strings.Repeat("cd", 32),
	}), rc); err != nil {
		t.Fatalf("create_condition: %v", err)
	}
	wantExTopics(t, rc, "ConditionCreated")
	ci := f.mustRead(t, "condition_info", map[string]any{"exchange": addr.String(), "condition": 1})
	if ci["collateral"] != f.token.String() || ci["question_hash"] != strings.Repeat("cd", 32) || ci["resolved"] != false || ci["locked"] != "0" {
		t.Fatalf("condition_info = %v", ci)
	}
	if ids, _ := ci["position_ids"].([]string); len(ids) != 2 || ids[0] != "256" || ids[1] != "257" {
		t.Fatalf("position_ids = %v", ci["position_ids"])
	}
}

// The whole lifecycle through the router envelopes: split, a MINT match,
// cancel and nonce by their makers, merge, pause, the owner controls,
// resolve, redeem and redeem_for — each with the receipt it should carry.
func TestExchangeRouterLifecycle(t *testing.T) {
	f := newExFixture(t)

	rc, err := f.call(t, f.alice.addr, "split", f.exParams(map[string]any{"condition": f.cond, "amount": "100"}))
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	wantExTopics(t, rc, "Transfer", "TransferSingle", "TransferSingle", "PositionSplit")
	_, err = f.call(t, f.alice.addr, "split", f.exParams(map[string]any{"condition": f.cond, "amount": "010"}))
	mustSoft(t, err, "leading zero")
	pos := f.mustRead(t, "position", f.exParams(map[string]any{"condition": f.cond, "holder": f.alice.addr.String()}))
	if bals, _ := pos["balances"].([]string); len(bals) != 2 || bals[0] != "100" || bals[1] != "100" {
		t.Fatalf("position = %v", pos)
	}

	// MINT match by the operator; a stranger is refused SOFT before any verify.
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 100, 500_000)
	matchParams := f.exParams(map[string]any{"taker_order": taker, "maker_orders": []exchangeOrderWire{maker}, "fill_shares": []string{"100"}})
	_, err = f.call(t, f.carol.addr, "match", matchParams)
	mustSoft(t, err, "is not an operator")
	rc, err = f.call(t, f.operator, "match", matchParams)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	wantExTopics(t, rc, "Transfer", "Transfer", "TransferSingle", "TransferSingle", "MintMatched", "OrderFilled", "OrderFilled")
	st := f.mustRead(t, "order_status", f.exParams(map[string]any{"hash": hex.EncodeToString(f.hashOf(t, taker))}))
	if st["filled"] != "100" || st["cancelled"] != false {
		t.Fatalf("order_status = %v", st)
	}
	rem := f.mustRead(t, "order_remaining", f.exParams(map[string]any{"order": maker}))
	if rem["remaining"] != "0" || rem["filled"] != "100" || rem["share_amount"] != "100" {
		t.Fatalf("order_remaining = %v", rem)
	}

	// cancel_order by the maker, increment_nonce by the maker.
	open := f.order(t, f.carol, types.OrderSideSell, 0, 10, 700_000)
	rc, err = f.call(t, f.carol.addr, "cancel_order", f.exParams(map[string]any{"order": open}))
	if err != nil {
		t.Fatalf("cancel_order: %v", err)
	}
	wantExTopics(t, rc, "OrderCancelled")
	rem = f.mustRead(t, "order_remaining", f.exParams(map[string]any{"order": open}))
	if rem["cancelled"] != true || rem["remaining"] != "0" {
		t.Fatalf("order_remaining after cancel = %v", rem)
	}
	rc, err = f.call(t, f.carol.addr, "increment_nonce", f.exParams(map[string]any{"maker": f.carol.addr.String(), "expected_nonce": 0}))
	if err != nil {
		t.Fatalf("increment_nonce: %v", err)
	}
	wantExTopics(t, rc, "NonceIncremented")
	if n := f.mustRead(t, "nonce", f.exParams(map[string]any{"maker": f.carol.addr.String()})); n["nonce"] != uint64(1) {
		t.Fatalf("nonce = %v", n)
	}

	// merge by alice: 50 of her 100 NO + 100 YES (split) — she holds YES 200.
	rc, err = f.call(t, f.alice.addr, "merge", f.exParams(map[string]any{"condition": f.cond, "amount": "50"}))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	wantExTopics(t, rc, "TransferSingle", "TransferSingle", "Transfer", "PositionsMerged")
	if f.position(t, f.alice.addr, 0) != 150 || f.position(t, f.alice.addr, 1) != 50 {
		t.Fatalf("alice positions = %d / %d", f.position(t, f.alice.addr, 0), f.position(t, f.alice.addr, 1))
	}

	// pause blocks match (SOFT) and nothing else; unpause lifts it.
	if _, err := f.call(t, f.owner, "pause", f.exParams(nil)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if info := f.mustRead(t, "exchange_info", f.exParams(nil)); info["paused"] != true {
		t.Fatalf("exchange_info = %v", info)
	}
	_, err = f.call(t, f.operator, "match", f.exParams(map[string]any{
		"taker_order": f.order(t, f.alice, types.OrderSideBuy, 0, 10, 600_000), "maker_orders": []exchangeOrderWire{f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)}, "fill_shares": []string{"10"},
	}))
	mustSoft(t, err, "paused")
	if _, err := f.call(t, f.owner, "unpause", f.exParams(nil)); err != nil {
		t.Fatalf("unpause: %v", err)
	}

	// Owner controls through the router.
	if _, err := f.call(t, f.owner, "set_redeem_fee_bps", f.exParams(map[string]any{"redeem_fee_bps": 250})); err != nil {
		t.Fatalf("set_redeem_fee_bps: %v", err)
	}
	if _, err := f.call(t, f.owner, "set_fee_recipient", f.exParams(map[string]any{"fee_recipient": f.carol.addr.String()})); err != nil {
		t.Fatalf("set_fee_recipient: %v", err)
	}
	_, err = f.call(t, f.owner, "set_fee_recipient", f.exParams(map[string]any{"fee_recipient": "nope"}))
	mustSoft(t, err, "fee_recipient")
	_, err = f.call(t, f.carol.addr, "set_redeem_fee_bps", f.exParams(map[string]any{"redeem_fee_bps": 1}))
	mustSoft(t, err, "not the owner")

	// resolve (resolver only), then redeem / redeem_for.
	_, err = f.call(t, f.owner, "resolve", f.exParams(map[string]any{"condition": f.cond, "outcome": 0}))
	mustSoft(t, err, "not the resolver")
	rc, err = f.call(t, f.resolver, "resolve", f.exParams(map[string]any{"condition": f.cond, "outcome": 0}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantExTopics(t, rc, "Resolved")
	carolBefore := f.collateral(t, f.carol.addr)
	aliceBefore := f.collateral(t, f.alice.addr)
	rc, err = f.call(t, f.alice.addr, "redeem", f.exParams(map[string]any{"condition": f.cond}))
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	// 150 winning shares @ 250 bps: fee floor(150*250/10000) = 3, payout 147.
	wantExTopics(t, rc, "TransferSingle", "Transfer", "Transfer", "RedeemFeeCharged", "Redeemed")
	if got := f.collateral(t, f.alice.addr) - aliceBefore; got != 147 {
		t.Fatalf("alice payout = %d, want 147", got)
	}
	if got := f.collateral(t, f.carol.addr) - carolBefore; got != 3 {
		t.Fatalf("fee to carol = %d, want 3", got)
	}
	_, err = f.call(t, f.owner, "redeem_for", f.exParams(map[string]any{"condition": f.cond, "holder": f.bob.addr.String()}))
	mustSoft(t, err, "holds no shares of the winning outcome")
	_, err = f.call(t, f.owner, "redeem_for", f.exParams(map[string]any{"condition": f.cond, "holder": ""}))
	mustSoft(t, err, "holder")

	// transfer_ownership: the old owner is out.
	rc, err = f.call(t, f.owner, "transfer_ownership", f.exParams(map[string]any{"new_owner": f.carol.addr.String()}))
	if err != nil {
		t.Fatalf("transfer_ownership: %v", err)
	}
	wantExTopics(t, rc, "OwnershipTransferred")
	_, err = f.call(t, f.owner, "pause", f.exParams(nil))
	mustSoft(t, err, "not the owner")
	if _, err := f.call(t, f.carol.addr, "pause", f.exParams(nil)); err != nil {
		t.Fatalf("new owner pause: %v", err)
	}

	// Every non-deploy action on an address nothing is deployed at is SOFT.
	ghost := map[string]any{"exchange": addr(0x99).String(), "condition": 1, "amount": "1"}
	_, err = f.call(t, f.owner, "split", ghost)
	mustSoft(t, err, "no exchange is deployed")
	_, err = f.call(t, f.owner, "pause", map[string]any{"exchange": "SQ:nope"})
	mustSoft(t, err, "exchange: ")
}

func (f *exFixture) hashOf(t *testing.T, w exchangeOrderWire) []byte {
	t.Helper()
	h := f.hash(t, w)
	return h[:]
}

// The reads: shapes, the pure ones, and the failure texts.
func TestExchangeRouterReads(t *testing.T) {
	f := newExFixture(t)

	info := f.mustRead(t, "exchange_info", f.exParams(nil))
	if info["owner"] != f.owner.String() || info["condition_count"] != uint64(1) || info["exists"] != true ||
		info["paused"] != false || info["fee_recipient"] != f.owner.String() {
		t.Fatalf("exchange_info = %v", info)
	}
	if ops, _ := info["operators"].([]string); len(ops) != 1 || ops[0] != f.operator.String() {
		t.Fatalf("operators = %v", info["operators"])
	}
	// An address nothing is deployed at reads as empty, exists false.
	ghost := f.mustRead(t, "exchange_info", map[string]any{"exchange": addr(0x99).String()})
	if ghost["exists"] != false || ghost["owner"] != (qcrypto.Address{}).String() || ghost["condition_count"] != uint64(0) {
		t.Fatalf("ghost exchange_info = %v", ghost)
	}

	ci := f.mustRead(t, "condition_info", f.exParams(map[string]any{"condition": f.cond}))
	if ci["resolver"] != f.resolver.String() || ci["end_time"] != exEndTime || ci["outcome_count"] != uint8(2) || ci["winning_outcome"] != uint8(0) {
		t.Fatalf("condition_info = %v", ci)
	}
	if _, err := f.read(t, "condition_info", f.exParams(map[string]any{"condition": 2})); err == nil || !strings.HasPrefix(err.Error(), "exchange condition_info: condition 2 does not exist") {
		t.Fatalf("missing condition: %v", err)
	}

	if op := f.mustRead(t, "is_operator", f.exParams(map[string]any{"address": f.operator.String()})); op["is_operator"] != true {
		t.Fatalf("is_operator = %v", op)
	}
	if op := f.mustRead(t, "is_operator", f.exParams(map[string]any{"address": f.carol.addr.String()})); op["is_operator"] != false {
		t.Fatalf("is_operator = %v", op)
	}
	if n := f.mustRead(t, "nonce", f.exParams(map[string]any{"maker": f.alice.addr.String()})); n["nonce"] != uint64(0) || n["maker"] != f.alice.addr.String() {
		t.Fatalf("nonce = %v", n)
	}

	// position_id is pure and needs no exchange; the golden id is pinned.
	pid := f.mustRead(t, "position_id", map[string]any{"condition": 42, "outcome": 1})
	if pid["id"] != "10753" || pid["condition"] != uint64(42) || pid["outcome"] != uint8(1) {
		t.Fatalf("position_id = %v", pid)
	}
	if _, ok := pid["exchange"]; ok {
		t.Fatal("position_id must not report an instance")
	}

	// order_hash is the instance-bound digest, whatever the wire says.
	o := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	oh := f.mustRead(t, "order_hash", f.exParams(map[string]any{"order": o}))
	if oh["hash"] != hex.EncodeToString(f.hashOf(t, o)) {
		t.Fatalf("order_hash = %v", oh)
	}
	foreign := o
	foreign.Exchange = addr(0x99).String()
	if _, err := f.read(t, "order_hash", f.exParams(map[string]any{"order": foreign})); err == nil || !strings.Contains(err.Error(), "is for exchange") {
		t.Fatalf("order for another exchange: %v", err)
	}
	if _, err := f.read(t, "order_hash", f.exParams(nil)); err == nil || !strings.Contains(err.Error(), "order is required") {
		t.Fatalf("order_hash without order: %v", err)
	}
	// Unfilled: status zero, remaining is the whole size.
	st := f.mustRead(t, "order_status", f.exParams(map[string]any{"hash": oh["hash"]}))
	if st["filled"] != "0" || st["cancelled"] != false || st["hash"] != oh["hash"] {
		t.Fatalf("order_status = %v", st)
	}
	rem := f.mustRead(t, "order_remaining", f.exParams(map[string]any{"order": o}))
	if rem["remaining"] != "100" || rem["hash"] != oh["hash"] {
		t.Fatalf("order_remaining = %v", rem)
	}
	for _, bad := range []string{"", "0x" + strings.Repeat("ab", 31), strings.Repeat("AB", 32), strings.Repeat("ab", 33)} {
		if _, err := f.read(t, "order_status", f.exParams(map[string]any{"hash": bad})); err == nil || !strings.HasPrefix(err.Error(), "exchange order_status: hash:") {
			t.Fatalf("hash %q: %v", bad, err)
		}
	}

	// Positions for a holder, and the failure texts of the address fields.
	f.split(t, f.bob, 7)
	pos := f.mustRead(t, "position", f.exParams(map[string]any{"condition": f.cond, "holder": f.bob.addr.String()}))
	if bals, _ := pos["balances"].([]string); len(bals) != 2 || bals[0] != "7" || bals[1] != "7" {
		t.Fatalf("position = %v", pos)
	}
	if ids, _ := pos["position_ids"].([]string); len(ids) != 2 || ids[0] != ExchangePositionID(f.cond, 0).String() {
		t.Fatalf("position ids = %v", pos["position_ids"])
	}
	for action, params := range map[string]map[string]any{
		"position":      f.exParams(map[string]any{"condition": f.cond, "holder": "x"}),
		"nonce":         f.exParams(map[string]any{"maker": "x"}),
		"is_operator":   f.exParams(map[string]any{"address": "x"}),
		"exchange_info": {"exchange": "x"},
	} {
		if _, err := f.read(t, action, params); err == nil || !strings.HasPrefix(err.Error(), "exchange "+action+": ") {
			t.Fatalf("%s with a bad address: %v", action, err)
		}
	}
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, []byte(`{"module":"exchange","action":"nonce","params":"[]"}`)); err == nil || !strings.Contains(err.Error(), "bad params") {
		t.Fatalf("bad params: %v", err)
	}
}

// simulate_match is a dry run at the head: it reports success or the exact
// error a real match would return, and leaves no residue — on the fill
// counters, the balances, or the snapshot stack — even with a zero head.
func TestExchangeSimulateMatch(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 100, 500_000)
	params := func(caller qcrypto.Address) map[string]any {
		return f.exParams(map[string]any{
			"caller": caller.String(), "taker_order": taker, "maker_orders": []exchangeOrderWire{maker}, "fill_shares": []string{"100"},
		})
	}
	before, _ := f.sdb.PendingStateRoot()
	snaps := f.sdb.SnapshotCount()

	sim := f.mustRead(t, "simulate_match", params(f.operator))
	if sim["ok"] != true || sim["error"] != "" || sim["logs"] != 7 || sim["head_time"] != f.time || sim["head_height"] != uint64(1) {
		t.Fatalf("simulate_match = %v", sim)
	}
	if f.filled(t, taker) != 0 || f.filled(t, maker) != 0 || f.collateral(t, f.alice.addr) != exFunding || f.locked(t) != 0 {
		t.Fatal("simulate_match left residue")
	}
	if after, _ := f.sdb.PendingStateRoot(); after != before {
		t.Fatal("simulate_match staged a write")
	}
	if f.sdb.SnapshotCount() != snaps {
		t.Fatalf("simulate_match leaked a snapshot: %d -> %d", snaps, f.sdb.SnapshotCount())
	}

	// A stranger's dry run fails with the very string the real match returns.
	sim = f.mustRead(t, "simulate_match", params(f.carol.addr))
	_, real := f.call(t, f.carol.addr, "match", params(f.carol.addr))
	if sim["ok"] != false || real == nil || sim["error"] != real.Error() {
		t.Fatalf("simulate error %q, real %v", sim["error"], real)
	}

	// Zero head (nil executor): still a valid answer.
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "exchange", "simulate_match", params(f.operator)))
	if err != nil {
		t.Fatalf("zero head: %v", err)
	}
	if out := v.(map[string]interface{}); out["ok"] != true || out["head_time"] != int64(0) {
		t.Fatalf("zero head simulate_match = %v", out)
	}
	// The head time is what the dry run is judged against.
	v, err = DispatchModuleReadCall(f.sdb, ReadContext{Height: 9, Time: exEndTime}, moduleData(t, "exchange", "simulate_match", params(f.operator)))
	if err != nil {
		t.Fatalf("late head: %v", err)
	}
	if out := v.(map[string]interface{}); out["ok"] != false || !strings.Contains(out["error"].(string), "has ended") {
		t.Fatalf("late head simulate_match = %v", out)
	}
	if _, err := f.read(t, "simulate_match", f.exParams(map[string]any{"taker_order": taker})); err == nil || !strings.HasPrefix(err.Error(), "exchange simulate_match: caller:") {
		t.Fatalf("caller is required: %v", err)
	}

	// The real match still works afterwards: nothing was consumed.
	if _, err := f.call(t, f.operator, "match", params(f.operator)); err != nil {
		t.Fatalf("match after simulate: %v", err)
	}
	if f.filled(t, taker) != 100 {
		t.Fatal("real match did not fill")
	}
}

// The admission preflight refuses a match from anyone but an active operator
// of the named instance and ignores every other envelope.
func TestPreflightExchangeMatch(t *testing.T) {
	f := newExFixture(t)
	mc := func(module, action string, params any) ModuleCall {
		var m ModuleCall
		data := moduleData(t, module, action, params)
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if err := PreflightExchangeMatch(f.sdb, mc("exchange", "match", f.exParams(nil)), f.operator); err != nil {
		t.Fatalf("operator: %v", err)
	}
	if err := PreflightExchangeMatch(f.sdb, mc("exchange", "split", f.exParams(nil)), f.carol.addr); err != nil {
		t.Fatalf("non-match action must pass: %v", err)
	}
	if err := PreflightExchangeMatch(f.sdb, mc("qrc20", "match", f.exParams(nil)), f.carol.addr); err != nil {
		t.Fatalf("other module must pass: %v", err)
	}
	mustContain(t, PreflightExchangeMatch(f.sdb, mc("exchange", "match", f.exParams(nil)), f.carol.addr), "is not an operator")
	mustContain(t, PreflightExchangeMatch(f.sdb, mc("exchange", "match", map[string]any{"exchange": addr(0x99).String()}), f.operator), "no exchange is deployed")
	mustContain(t, PreflightExchangeMatch(f.sdb, mc("exchange", "match", map[string]any{"exchange": "x"}), f.operator), "exchange match: exchange:")
	mustContain(t, PreflightExchangeMatch(f.sdb, ModuleCall{Module: "exchange", Action: "match", Params: []byte(`"[]"`)}, f.operator), "bad params")
	// Revoked operators are refused too.
	if err := f.x.SetOperator(f.owner, f.operator, false, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, PreflightExchangeMatch(f.sdb, mc("exchange", "match", f.exParams(nil)), f.operator), "is not an operator")
}
