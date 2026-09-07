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
)

// /tx/submit must refuse a nonce below the committed nonce with 400, not report
// it "accepted": such a tx can never be included, so a 202 lets a buggy or
// malicious client fill its own per-address window with never-includable txs
// (each costing the node a signature verification) and then wedge itself.
func TestSubmitRejectsStaleNonceWith400(t *testing.T) {
	s := newTestServer(t, "")
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.Nonce = 207
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx := types.NewTransfer(from, qcrypto.Address{0x55}, big.NewInt(1), big.NewInt(mempool.MinFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, _ := json.Marshal(tx)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(raw))
	s.handleSubmitTx(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stale nonce: got %d (%s), want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nonce too low") {
		t.Fatalf("body should name the cause: %s", rec.Body.String())
	}
	if s.pool.Size() != 0 {
		t.Fatalf("stale tx entered the pool: size=%d", s.pool.Size())
	}
}
