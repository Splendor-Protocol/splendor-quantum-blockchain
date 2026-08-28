package vm

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestModuleReadQRC20(t *testing.T) {
	sdb := newTestState(t)
	owner := addr(0x11)
	alice := addr(0x22)

	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, ModuleContext{Caller: owner, Nonce: 7}, moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Read Test", Symbol: "RTEST", Decimals: 18,
		TotalSupply: "1000000000000000000000000",
	}), dep); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy: no contract address")
	}
	token := *dep.ContractAddress

	// balance_of(owner) == total supply
	res, err := DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", "balance_of", map[string]string{
		"token": token.String(), "owner": owner.String(),
	}))
	if err != nil {
		t.Fatalf("balance_of: %v", err)
	}
	m := res.(map[string]interface{})
	if m["balance"] != "1000000000000000000000000" {
		t.Fatalf("balance_of(owner) = %v", m["balance"])
	}
	if m["exists"] != true {
		t.Fatalf("exists = %v (deployed token should be a contract)", m["exists"])
	}

	// balance_of(alice) == 0 (no holdings, no error)
	res, err = DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", "balance_of", map[string]string{
		"token": token.String(), "owner": alice.String(),
	}))
	if err != nil {
		t.Fatalf("balance_of(alice): %v", err)
	}
	if res.(map[string]interface{})["balance"] != "0" {
		t.Fatalf("alice balance = %v, want 0", res.(map[string]interface{})["balance"])
	}

	// total_supply carries metadata
	res, err = DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", "total_supply", map[string]string{
		"token": token.String(),
	}))
	if err != nil {
		t.Fatalf("total_supply: %v", err)
	}
	m = res.(map[string]interface{})
	if m["symbol"] != "RTEST" || m["total_supply"] != "1000000000000000000000000" {
		t.Fatalf("total_supply meta = %v", m)
	}

	// allowance defaults to 0
	res, err = DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", "allowance", map[string]string{
		"token": token.String(), "owner": owner.String(), "spender": alice.String(),
	}))
	if err != nil {
		t.Fatalf("allowance: %v", err)
	}
	if res.(map[string]interface{})["allowance"] != "0" {
		t.Fatalf("allowance = %v, want 0", res.(map[string]interface{})["allowance"])
	}

	// WRITE actions must be rejected on the read path (never mutate state).
	for _, act := range []string{"mint", "transfer", "approve", "burn", "deploy"} {
		if _, err := DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", act, map[string]string{
			"token": token.String(),
		})); err == nil {
			t.Errorf("expected write action %q to be rejected on the read path", act)
		}
	}

	// unknown module / action
	if _, err := DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "nope", "x", map[string]string{})); err == nil {
		t.Error("expected unknown module to be rejected")
	}
	if _, err := DispatchModuleReadCall(sdb, ReadContext{}, moduleData(t, "qrc20", "explode", map[string]string{
		"token": token.String(),
	})); err == nil {
		t.Error("expected unknown read action to be rejected")
	}
}

func TestPreflightModuleCall(t *testing.T) {
	// valid envelopes (read + write actions both parse in preflight)
	for _, tc := range []struct{ module, action string }{
		{"qrc20", "balance_of"}, {"qrc20", "transfer"}, {"dex", "reserves"}, {"dex", "swap"},
	} {
		if _, err := PreflightModuleCall(moduleData(t, tc.module, tc.action, map[string]string{})); err != nil {
			t.Errorf("preflight %s:%s rejected: %v", tc.module, tc.action, err)
		}
	}
	// invalid: unknown action, unknown module, bad JSON
	if _, err := PreflightModuleCall(moduleData(t, "qrc20", "explode", map[string]string{})); err == nil {
		t.Error("expected unknown action rejection")
	}
	if _, err := PreflightModuleCall(moduleData(t, "nope", "x", map[string]string{})); err == nil {
		t.Error("expected unknown module rejection")
	}
	if _, err := PreflightModuleCall([]byte("nonsense")); err == nil {
		t.Error("expected bad-JSON rejection")
	}
}
