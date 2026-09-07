package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func signedTestInstruction(t *testing.T) (*types.VerifyInstruction, qcrypto.Address) {
	t.Helper()
	mldsaPub, mldsaPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, slhPriv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var id types.InstructionID
	id[0] = 0x42
	ins := &types.VerifyInstruction{
		Version:          1,
		InstructionID:    id,
		Purpose:          "pay",
		Origin:           "eip155:1",
		SettlementHint:   "splendor-qc",
		SettlementAction: types.SettleFinalizeOnly,
	}
	if err := types.SignVerifyInstruction(types.ChainID, ins, mldsaPriv, slhPriv); err != nil {
		t.Fatal(err)
	}
	return ins, qcrypto.DeriveAddress(mldsaPub)
}

func TestVerifyCheckDualSig(t *testing.T) {
	s := newTestServer(t, "")
	ins, signer := signedTestInstruction(t)
	body, _ := json.Marshal(ins)
	if !bytes.Contains(body, []byte(`"instruction_id":"42000000`)) {
		t.Fatalf("instruction id is not hex on the wire: %s", body[:120])
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/check", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["valid"] != true {
		t.Fatalf("want valid, got %v", out)
	}
	if out["signer"] != signer.String() {
		t.Fatalf("signer = %v, want %s", out["signer"], signer)
	}
	if out["instruction_id"] != ins.InstructionID.Hex() {
		t.Fatalf("instruction_id = %v, want %s", out["instruction_id"], ins.InstructionID.Hex())
	}
	if out["settlement_action"] != types.SettleFinalizeOnly {
		t.Fatalf("settlement_action = %v", out["settlement_action"])
	}

	ins.SLHSig[0] ^= 0xff
	bad, _ := json.Marshal(ins)
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/check", bytes.NewReader(bad)))
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["valid"] != false {
		t.Fatalf("tampered sig should fail, got %v", out)
	}
	if !strings.Contains(out["reason"].(string), "SLH-DSA") {
		t.Fatalf("reason should name the failing scheme, got %v", out["reason"])
	}
}

// The array form of the id — what an unannotated [32]byte marshals to — is
// refused with a 400, not silently accepted.
func TestVerifyCheckRefusesArrayInstructionID(t *testing.T) {
	s := newTestServer(t, "")
	body := []byte(`{"version":1,"instruction_id":[66,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"purpose":"pay","origin":"x","settlement_hint":"splendor-qc"}`)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/check", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyProofValidatesItsInputs(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verify/proof/notahexid?instance=SQ:"+strings.Repeat("11", 32), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verify/proof/"+strings.Repeat("42", 32), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing instance: status %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verify/proof/"+strings.Repeat("42", 32)+"?instance=SQ:"+strings.Repeat("11", 32), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance: status %d: %s", rec.Code, rec.Body.String())
	}
}

// The doors only admit the action they are named for, and only verify-module
// calls; everything else is a 400 before any signature work.
func TestVerifyDoorsCheckTheActionBeforeAdmitting(t *testing.T) {
	s := newTestServer(t, "")
	mk := func(module, action string) []byte {
		data, _ := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: json.RawMessage(`{}`)})
		tx := types.Transaction{Type: types.TxCall, To: vm.ModuleRouterAddress, Data: data, ChainID: types.ChainID}
		body, _ := json.Marshal(map[string]any{"tx": tx})
		return body
	}
	cases := []struct {
		path, module, action string
		want                 int
	}{
		{"/verify/attest", "verify", "spend", http.StatusBadRequest},
		{"/verify/attest", "bridge", "attest", http.StatusBadRequest},
		{"/verify/settle", "verify", "attest", http.StatusBadRequest},
		// The right action reaches admission, which then refuses the unsigned
		// tx — but with the admission wording, not the door's.
		{"/verify/settle", "verify", "spend", http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, c.path, bytes.NewReader(mk(c.module, c.action))))
		if rec.Code != c.want {
			t.Fatalf("%s %s.%s: status %d: %s", c.path, c.module, c.action, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/settle", bytes.NewReader(mk("verify", "spend"))))
	if !strings.Contains(rec.Body.String(), "rejected") {
		t.Fatalf("a well-formed but unsigned spend should reach admission, got: %s", rec.Body.String())
	}
}

// Signed verify.attest / verify.spend TxCalls must clear the product-door
// action check and reach admitTx (202 into the mempool). Full on-chain
// check→attest→settle→proof is covered by `featsmoke verify` against a live node.
func TestVerifyDoorsAdmitSignedAttestAndSettle(t *testing.T) {
	s := newTestServer(t, "")
	s.executor.SetForks(forks.Schedule{forks.VerifyV1: 1, forks.ModuleRevertV1: 1})
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = pub.Bytes()
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	inst := "SQ:" + strings.Repeat("11", 32)
	id := strings.Repeat("42", 32)
	params, _ := json.Marshal(map[string]string{"verify": inst, "instruction_id": id})
	postDoor := func(path, action string, nonce uint64) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(vm.ModuleCall{Module: "verify", Action: action, Params: params})
		tx := types.NewCall(from, vm.ModuleRouterAddress, data, big.NewInt(0), big.NewInt(mempool.MinFee), nonce, now)
		if err := tx.Sign(priv, true); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"tx": tx})
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
		return rec
	}
	before := s.pool.Size()
	rec := postDoor("/verify/attest", "attest", 0)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("attest door: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != before+1 {
		t.Fatalf("attest not in mempool: size %d", s.pool.Size())
	}
	rec = postDoor("/verify/settle", "spend", 1)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("settle door: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != before+2 {
		t.Fatalf("settle not in mempool: size %d", s.pool.Size())
	}
	rec = postDoor("/verify/settle", "finalize", 2)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("settle finalize door: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
}

// End-to-end wire shape for /verify/check → doors: a dual-signed instruction
// validates, and the doors refuse a bare (non-tx) body before admission.
func TestVerifyCheckThenDoorsRefuseBareBody(t *testing.T) {
	s := newTestServer(t, "")
	ins, _ := signedTestInstruction(t)
	body, _ := json.Marshal(ins)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/check", bytes.NewReader(body)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("check: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/verify/attest", "/verify/settle"} {
		rec = httptest.NewRecorder()
		s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s bare instruction body: status %d %s, want 400", path, rec.Code, rec.Body.String())
		}
	}
}
