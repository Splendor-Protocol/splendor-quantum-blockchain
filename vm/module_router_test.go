package vm

import (
	"encoding/json"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func newTestState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	return sdb
}

func addr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

func moduleData(t *testing.T, module, action string, params any) []byte {
	t.Helper()
	praw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	data, err := json.Marshal(ModuleCall{Module: module, Action: action, Params: praw})
	if err != nil {
		t.Fatalf("marshal module call: %v", err)
	}
	return data
}

func TestModuleRouterAddressStable(t *testing.T) {
	// Reproducible offline: SHA3-256("splendor:module-router:v1").
	want := qcrypto.Hash32([]byte("splendor:module-router:v1"))
	var wantAddr qcrypto.Address
	copy(wantAddr[:], want[:])
	if ModuleRouterAddress != wantAddr {
		t.Fatalf("router address drifted: %s", ModuleRouterAddress)
	}
}

func TestQRC20DeployMintTransfer(t *testing.T) {
	sdb := newTestState(t)
	owner := addr(0x11)
	alice := addr(0x22)

	// deploy (nonce 7 → deterministic address)
	dep := &types.Receipt{}
	err := DispatchModuleCall(sdb, owner, 7, moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Splendor Test", Symbol: "STEST", Decimals: 18,
		TotalSupply: "1000000000000000000000000", // 1,000,000 * 1e18
	}), dep)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy: receipt.ContractAddress not set")
	}
	token := *dep.ContractAddress

	// address must match the module's own derivation
	if got := deployAddrForNonce(owner, 7); got != token {
		t.Fatalf("deploy addr mismatch: router=%s module=%s", token, got)
	}

	tok := NewQRC20Token(sdb, token)

	// initial supply credited to owner
	if bal, _ := tok.BalanceOf(owner); bal.Cmp(mustBig("1000000000000000000000000")) != 0 {
		t.Fatalf("owner initial balance = %s", bal)
	}
	if sym, _ := tok.Symbol(); sym != "STEST" {
		t.Fatalf("symbol = %q", sym)
	}

	// mint 500 tokens to owner
	mintRcpt := &types.Receipt{}
	if err := DispatchModuleCall(sdb, owner, 8, moduleData(t, "qrc20", "mint", qrc20TokenAction{
		Token: token.String(), To: owner.String(), Amount: "500000000000000000000",
	}), mintRcpt); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(mintRcpt.Logs) == 0 {
		t.Fatal("mint: expected a Mint event log")
	}
	wantAfterMint := mustBig("1000500000000000000000000")
	if bal, _ := tok.BalanceOf(owner); bal.Cmp(wantAfterMint) != 0 {
		t.Fatalf("owner balance after mint = %s want %s", bal, wantAfterMint)
	}

	// transfer 250 tokens owner → alice
	if err := DispatchModuleCall(sdb, owner, 9, moduleData(t, "qrc20", "transfer", qrc20TokenAction{
		Token: token.String(), To: alice.String(), Amount: "250000000000000000000",
	}), &types.Receipt{}); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if bal, _ := tok.BalanceOf(alice); bal.Cmp(mustBig("250000000000000000000")) != 0 {
		t.Fatalf("alice balance = %s", bal)
	}

	// non-owner mint must be rejected (owner-only)
	if err := DispatchModuleCall(sdb, alice, 0, moduleData(t, "qrc20", "mint", qrc20TokenAction{
		Token: token.String(), To: alice.String(), Amount: "1",
	}), &types.Receipt{}); err == nil {
		t.Fatal("expected non-owner mint to be rejected")
	}
}

func TestModuleRouterRejectsBadInput(t *testing.T) {
	sdb := newTestState(t)
	caller := addr(0x33)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"not json", []byte("nonsense")},
		{"unknown module", moduleData(t, "nope", "x", struct{}{})},
		{"unknown action", moduleData(t, "qrc20", "explode", struct{}{})},
	} {
		if err := DispatchModuleCall(sdb, caller, 0, tc.data, &types.Receipt{}); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

// deployAddrForNonce mirrors DeployQRC20Capped's address derivation for the test.
func deployAddrForNonce(deployer qcrypto.Address, nonce uint64) qcrypto.Address {
	nonceBytes := []byte(itoa(nonce))
	in := append(deployer[:], nonceBytes...)
	in = append(in, []byte("qrc20")...)
	h := qcrypto.Hash32(in)
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

func itoa(n uint64) string { return big.NewInt(0).SetUint64(n).String() }

func mustBig(s string) *big.Int {
	v, _ := new(big.Int).SetString(s, 10)
	return v
}
