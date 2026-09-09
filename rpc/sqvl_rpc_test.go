package rpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestVerifyCheckDualSig(t *testing.T) {
	s := newTestServer(t, "")
	mldsaPub, mldsaPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_ = mldsaPub
	slhPub, slhPriv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_ = slhPub
	var id [32]byte
	id[0] = 0x42
	ins := types.VerifyInstruction{
		Version:          types.VerifyInstructionVersion,
		InstructionID:    id,
		Purpose:          "pay",
		Origin:           "eip155:1",
		SettlementHint:   "splendor-qc",
		SettlementAction: "finalize_only",
	}
	if err := types.SignVerifyInstruction(types.ChainID, &ins, mldsaPriv, slhPriv); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(&ins)
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
	ins.SLHSig[0] ^= 0xff
	bad, _ := json.Marshal(&ins)
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/check", bytes.NewReader(bad)))
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["valid"] != false {
		t.Fatalf("tampered sig should fail, got %v", out)
	}
}
