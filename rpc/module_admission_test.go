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

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// postSubmit signs tx and POSTs it to /tx/submit, returning the recorder.
func postSubmit(t *testing.T, s *Server, tx *types.Transaction, priv *qcrypto.PrivateKey) *httptest.ResponseRecorder {
	t.Helper()
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal tx: %v", err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(body)))
	return rec
}

// A signed TxCall carrying a module READ action is refused at /tx/submit and
// never enters the mempool: the write dispatch would treat it as an unknown
// action (HARD), the producer would drop it, and the sender would sit at that
// nonce forever. The same envelope is served by /call.
func TestSubmitRefusesReadActionThatCallServes(t *testing.T) {
	s := newTestServer(t, "")

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000))
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	readEnv := moduleEnvelope(t, "qrc20", "balance_of", map[string]string{
		"token": qcrypto.Address{0x44}.String(), "owner": from.String(),
	})
	now := time.Now().Unix()

	rec := postSubmit(t, s, types.NewCall(from, vm.ModuleRouterAddress, readEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), priv)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read action in a TxCall: status %d body %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "is a read action") {
		t.Fatalf("want a read-action refusal, got %s", rec.Body.String())
	}
	if s.pool.Size() != 0 {
		t.Fatalf("refused tx entered the mempool (size %d)", s.pool.Size())
	}

	// The identical envelope is what /call is for.
	callBody, _ := json.Marshal(map[string]string{
		"to": vm.ModuleRouterAddress.String(), "data": hexOf(readEnv),
	})
	callRec := httptest.NewRecorder()
	makeCallHandler(s.stateDB, s.executor)(callRec, httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(callBody)))
	if callRec.Code != http.StatusOK {
		t.Fatalf("/call status %d body %s, want 200", callRec.Code, callRec.Body.String())
	}

	// The other HARD classes are refused at the same door.
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"unparseable envelope", []byte("nonsense"), "invalid JSON envelope"},
		{"unknown module", moduleEnvelope(t, "nope", "x", map[string]string{}), "unknown module"},
		{"unknown action", moduleEnvelope(t, "qrc20", "explode", map[string]string{}), "unknown action"},
	} {
		rec := postSubmit(t, s, types.NewCall(from, vm.ModuleRouterAddress, tc.data, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), priv)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: status %d body %s, want 400 containing %q", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	if s.pool.Size() != 0 {
		t.Fatalf("a refused tx entered the mempool (size %d)", s.pool.Size())
	}

	// A router call carrying value is refused (the module branch never moves it).
	writeEnv := moduleEnvelope(t, "qrc20", "transfer", map[string]string{
		"token": qcrypto.Address{0x44}.String(), "to": from.String(), "amount": "1",
	})
	rec = postSubmit(t, s, types.NewCall(from, vm.ModuleRouterAddress, writeEnv, big.NewInt(5), big.NewInt(mempool.MinFee), 0, now), priv)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "amount 0") {
		t.Fatalf("valued router call: status %d body %s, want 400 amount-0 refusal", rec.Code, rec.Body.String())
	}

	// A well-formed write action is admitted: preflight only refuses what the
	// executor could never apply.
	rec = postSubmit(t, s, types.NewCall(from, vm.ModuleRouterAddress, writeEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), priv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("write action: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != 1 {
		t.Fatalf("admitted tx missing from the mempool (size %d)", s.pool.Size())
	}
}
