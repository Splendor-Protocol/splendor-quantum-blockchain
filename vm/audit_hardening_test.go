package vm

import (
	"math/big"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// mustCapped asserts a SOFT "above the cap" refusal.
func mustCapped(t *testing.T, err error, field string) {
	t.Helper()
	mustContain(t, err, field+" is ")
	mustContain(t, err, "above the cap of")
	if IsHardModuleError(err) {
		t.Fatalf("a string cap is the action's own complaint, must be SOFT: %v", err)
	}
}

// A qrc1155 mint persists its uri per token and a deploy its name and
// uri_prefix; both paid the flat fee whatever their size. 2048 bytes of uri
// is the cap; one more is refused before anything is written.
func TestQRC1155StringCaps(t *testing.T) {
	f := newMTFixture(t)
	ok := strings.Repeat("u", MaxModuleURIBytes)
	over := ok + "u"

	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "1", URI: ok})); err != nil {
		t.Fatalf("a %d-byte uri must mint: %v", MaxModuleURIBytes, err)
	}
	before, _ := f.sdb.PendingStateRoot()
	_, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "2", Amount: "1", URI: over}))
	mustCapped(t, err, "uri")
	if after, _ := f.sdb.PendingStateRoot(); after != before {
		t.Fatal("a refused mint wrote state")
	}
	if f.balance(t, f.alice, 2).Sign() != 0 {
		t.Fatal("a refused mint minted")
	}
	_, err = f.call(t, f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: []string{"3", "4"}, Amounts: []string{"1", "1"}, URIs: []string{ok, over}}))
	mustCapped(t, err, "uris[1]")

	deploy := func(name, prefix string) error {
		_, err := f.call(t, f.owner, "deploy", qrc1155DeployParams{Name: name, URIPrefix: prefix})
		return err
	}
	if err := deploy(strings.Repeat("n", MaxModuleNameBytes), ok); err != nil {
		t.Fatalf("a deploy at the caps: %v", err)
	}
	mustCapped(t, deploy(strings.Repeat("n", MaxModuleNameBytes+1), ""), "name")
	mustCapped(t, deploy("ok", over), "uri_prefix")
}

// The qrc20 caps are a rule change on an action that already shipped, so
// they are gated under qrc20_v2: a pre-fork replay of a long symbol must
// still apply exactly as it did.
func TestQRC20DeployStringCapsGatedByV2(t *testing.T) {
	sdb := newTestState(t)
	caller := addr(0x11)
	long := qrc20DeployParams{Name: "Long", Symbol: strings.Repeat("S", MaxModuleNameBytes+1), Decimals: 0, TotalSupply: "10"}

	pre := ModuleContext{Caller: caller, Nonce: 1, Height: 1}
	if err := DispatchModuleCall(sdb, pre, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}); err != nil {
		t.Fatalf("pre-fork, a long symbol must keep the legacy behaviour: %v", err)
	}
	early := ModuleContext{Caller: caller, Nonce: 2, Height: 1, Forks: forks.Schedule{forks.QRC20V2: 2}}
	if err := DispatchModuleCall(sdb, early, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}); err != nil {
		t.Fatalf("one block before the fork must keep the legacy behaviour: %v", err)
	}

	post := ModuleContext{Caller: caller, Nonce: 3, Height: 1, Forks: v2Forks()}
	mustCapped(t, DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}), "symbol")
	long.Symbol, long.Name = "OK", strings.Repeat("N", MaxModuleNameBytes+1)
	mustCapped(t, DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}), "name")
	long.Name = strings.Repeat("N", MaxModuleNameBytes)
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), dep); err != nil {
		t.Fatalf("a deploy at the cap under the fork: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
}

// The exchange's position collection carries the same name/uri_prefix caps.
func TestExchangeDeployStringCaps(t *testing.T) {
	f := newExFixture(t)
	ok := strings.Repeat("u", MaxModuleURIBytes)
	if _, err := f.call(t, f.owner, "deploy", map[string]any{"name": strings.Repeat("n", MaxModuleNameBytes), "uri_prefix": ok}); err != nil {
		t.Fatalf("a deploy at the caps: %v", err)
	}
	_, err := f.call(t, f.owner, "deploy", map[string]any{"name": "Positions", "uri_prefix": ok + "u"})
	mustCapped(t, err, "uri_prefix")
	_, err = f.call(t, f.owner, "deploy", map[string]any{"name": strings.Repeat("n", MaxModuleNameBytes+1)})
	mustCapped(t, err, "name")
}

// A StateDB read error must surface as an error, never as the empty-slot
// zero: a node whose disk faults would otherwise compute a different
// successful result (no operator, no condition, nothing filled) than the rest
// of the network. Committing and then closing the store makes every slot read
// fail; each entry point must return that failure, not its own complaint
// about the zero it would have read.
func TestExchangeReadErrorsPropagate(t *testing.T) {
	f := newExFixture(t)
	f.split(t, f.alice, 10)
	taker := f.order(t, f.alice, types.OrderSideSell, 0, 10, 500_000)
	maker := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 500_000)
	if _, err := f.sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := f.sdb.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	const closed = "leveldb: closed"

	if _, err := f.x.Condition(f.cond); err == nil || !strings.Contains(err.Error(), closed) {
		t.Fatalf("Condition: %v, want the read error", err)
	}
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	if err == nil || !strings.Contains(err.Error(), closed) || strings.Contains(err.Error(), "not an operator") {
		t.Fatalf("Match: %v, want the read error", err)
	}
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil); err == nil || !strings.Contains(err.Error(), closed) {
		t.Fatalf("Merge: %v, want the read error", err)
	}
	if _, err := f.x.OrderFilled(f.hash(t, taker)); err == nil || !strings.Contains(err.Error(), closed) {
		t.Fatalf("OrderFilled: %v, want the read error", err)
	}
	if _, err := f.tok.BalanceOf(f.alice.addr); err == nil || !strings.Contains(err.Error(), closed) {
		t.Fatalf("qrc20 BalanceOf: %v, want the read error", err)
	}
	if _, err := f.pos.BalanceOf(f.alice.addr, ExchangePositionID(f.cond, 0)); err == nil || !strings.Contains(err.Error(), closed) {
		t.Fatalf("qrc1155 BalanceOf: %v, want the read error", err)
	}
}
