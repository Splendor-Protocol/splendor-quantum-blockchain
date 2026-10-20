package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"

	"github.com/gorilla/mux"
)

func TestContractCodeHandler(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	// A raw-bytecode contract: store code, point an account at its hash.
	code := []byte{0x52, 0x00, 0x52, 0x2a, 0x71, 0x52, 0x00, 0x52, 0x01, 0xf0}
	codeHash := qcrypto.Hash32(code)
	sdb.SetCode(codeHash, code)

	var addr qcrypto.Address
	for i := range addr {
		addr[i] = 0x77
	}
	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = codeHash
	sdb.SetAccount(acc)

	h := makeContractCodeHandler(sdb)
	call := func(a string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/contract/"+a+"/code", nil)
		req = mux.SetURLVars(req, map[string]string{"address": a})
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}

	// bytecode contract → returns code hex + disassembly
	rec := call(addr.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		NativeModule bool     `json:"native_module"`
		Code         string   `json:"code"`
		CodeSize     int      `json:"code_size"`
		Disassembly  []string `json:"disassembly"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NativeModule {
		t.Error("bytecode contract wrongly reported as native module")
	}
	if resp.Code != "5200522a7152005201f0" {
		t.Errorf("code hex = %q", resp.Code)
	}
	if resp.CodeSize != len(code) {
		t.Errorf("code_size = %d want %d", resp.CodeSize, len(code))
	}
	if len(resp.Disassembly) == 0 {
		t.Error("expected a disassembly listing")
	}

	// native-module contract (no code_hash) → native_module=true, empty code
	var mod qcrypto.Address
	for i := range mod {
		mod[i] = 0x33
	}
	macc := types.NewAccount(mod)
	macc.IsContract = true // no CodeHash set → zero
	sdb.SetAccount(macc)

	rec = call(mod.String())
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode module: %v", err)
	}
	if !resp.NativeModule || resp.Code != "" {
		t.Errorf("native module case wrong: native=%v code=%q", resp.NativeModule, resp.Code)
	}
}
