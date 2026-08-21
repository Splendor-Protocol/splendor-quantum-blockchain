package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func hexOf(b []byte) string { return fmt.Sprintf("%x", b) }

func moduleEnvelope(t *testing.T, module, action string, params interface{}) []byte {
	t.Helper()
	praw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	data, err := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: praw})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}

func TestCallHandlerModuleRead(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	var owner qcrypto.Address
	for i := range owner {
		owner[i] = 0x11
	}

	// Deploy a token directly through the (write) module router.
	dep := &types.Receipt{}
	if err := vm.DispatchModuleCall(sdb, owner, 3, moduleEnvelope(t, "qrc20", "deploy", map[string]interface{}{
		"name": "HTTP Test", "symbol": "HTEST", "decimals": 18,
		"total_supply": "5000000000000000000",
	}), dep); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy: no contract address")
	}
	token := dep.ContractAddress.String()

	// executor is nil: the module-read path must never dereference it.
	h := makeCallHandler(sdb, nil)

	// balance_of(owner) via POST /call
	readEnv := moduleEnvelope(t, "qrc20", "balance_of", map[string]string{
		"token": token, "owner": owner.String(),
	})
	body, _ := json.Marshal(map[string]string{
		"to":       vm.ModuleRouterAddress.String(),
		"data":     hexOf(readEnv),
		"encoding": "hex",
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decoded map[string]interface{} `json:"decoded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if resp.Decoded["balance"] != "5000000000000000000" {
		t.Fatalf("balance = %v (body=%s)", resp.Decoded["balance"], rec.Body.String())
	}

	// A write action through /call must be rejected with 400 (never executed).
	writeEnv := moduleEnvelope(t, "qrc20", "mint", map[string]string{
		"token": token, "to": owner.String(), "amount": "1",
	})
	body, _ = json.Marshal(map[string]string{
		"to": vm.ModuleRouterAddress.String(), "data": hexOf(writeEnv),
	})
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for write action, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestEstimateHandlerTransfer(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	h := makeEstimateHandler(sdb, nil)

	// A plain transfer: gas 0, fee = mempool floor, not reverted.
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x44
	}
	body, _ := json.Marshal(map[string]interface{}{
		"type": 1, "to": to.String(), "value": "100",
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/estimate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp estimateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Gas != 0 {
		t.Fatalf("transfer gas = %d, want 0", resp.Gas)
	}
	if resp.Fee != "1000" {
		t.Fatalf("fee = %q, want the mempool floor 1000", resp.Fee)
	}
	if !resp.FeeIsFloor {
		t.Fatal("fee_is_floor should be true on this chain")
	}
}
