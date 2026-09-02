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
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

// Amounts are integers above 2^53 in practice (1 SPLD = 1e18), which a
// JavaScript Number rounds. The RPC ingress therefore accepts "amount" and
// "fee" as strict decimal strings and turns them into the bare numbers the
// core types decode, so the canonical signed body — and the signer WASM built
// from core/types — never changes.

func TestNormalizeWireAmountsQuotesToNumbers(t *testing.T) {
	in := []byte(`{"amount":"12345678901234567891","fee":"1000","nonce":7,"data":"YW1vdW50","memo":"fee: 12",
		"inner":{"amount":"5","list":[{"fee":"6"},{"amount":7}]},"from":[1,2,3],"big":98765432109876543210,"nil":null}`)
	out, err := normalizeWireAmounts(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"amount": `12345678901234567891`, "fee": `1000`, "nonce": `7`, "data": `"YW1vdW50"`,
		"memo": `"fee: 12"`, "from": `[1,2,3]`, "big": `98765432109876543210`, "nil": `null`,
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s: got %s, want %s", k, got[k], v)
		}
	}
	var inner struct {
		Amount json.RawMessage   `json:"amount"`
		List   []json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(got["inner"], &inner); err != nil {
		t.Fatal(err)
	}
	if string(inner.Amount) != `5` || string(inner.List[0]) != `{"fee":6}` || string(inner.List[1]) != `{"amount":7}` {
		t.Errorf("nested: %s %s %s", inner.Amount, inner.List[0], inner.List[1])
	}
}

func TestNormalizeWireAmountsRejectsMalformedStrings(t *testing.T) {
	for _, bad := range []string{`"1.5"`, `"-1"`, `"0x10"`, `"1e3"`, `""`, `" 12"`, `"12 "`, `"12_000"`, `"+1"`, `"abc"`} {
		_, err := normalizeWireAmounts([]byte(`{"nonce":1,"amount":` + bad + `}`))
		if err == nil {
			t.Errorf("amount %s accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "amount") {
			t.Errorf("error should name the field: %v", err)
		}
	}
	// Numbers and null are left exactly as encoding/json will judge them.
	for _, ok := range []string{`1`, `null`, `-1`, `1.5`} {
		if _, err := normalizeWireAmounts([]byte(`{"amount":` + ok + `}`)); err != nil {
			t.Errorf("numeric %s should pass through: %v", ok, err)
		}
	}
	if _, err := normalizeWireAmounts([]byte(`{"amount":`)); err == nil {
		t.Error("truncated JSON accepted")
	}
	if out, err := normalizeWireAmounts([]byte(`[1,"2"]`)); err != nil || string(out) != `[1,"2"]` {
		t.Errorf("non-object body should pass through: %s %v", out, err)
	}
}

func quoteTxAmounts(t *testing.T, raw []byte, amount, fee string) []byte {
	t.Helper()
	out := bytes.Replace(raw, []byte(`"amount":`+amount+`,`), []byte(`"amount":"`+amount+`",`), 1)
	out = bytes.Replace(out, []byte(`"fee":`+fee+`,`), []byte(`"fee":"`+fee+`",`), 1)
	if bytes.Equal(out, raw) {
		t.Fatalf("test setup: amounts not numeric in %s", raw)
	}
	return out
}

func TestSubmitAcceptsStringAmountsAndVerifiesSignature(t *testing.T) {
	s := newTestServer(t, "")
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000_000))
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatal(err)
	}

	tx := types.NewTransfer(from, qcrypto.Address{0x55}, big.NewInt(123456789), big.NewInt(mempool.MinFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tx)
	body := quoteTxAmounts(t, raw, "123456789", "1000")

	rec := httptest.NewRecorder()
	s.handleSubmitTx(rec, httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("string amounts: got %d (%s), want 202", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != 1 {
		t.Fatalf("pool size %d, want 1", s.pool.Size())
	}
	pending := s.pool.Pending(1)
	if pending[0].Amount.Cmp(big.NewInt(123456789)) != 0 || pending[0].Fee.Cmp(big.NewInt(mempool.MinFee)) != 0 {
		t.Fatalf("pooled tx amounts: %v %v", pending[0].Amount, pending[0].Fee)
	}
	// Same tx id as the numeric form: the encoding was presentation only.
	want, _ := tx.FullHash()
	got, _ := pending[0].FullHash()
	if want != got {
		t.Fatalf("tx id changed by string amounts")
	}
}

func TestSubmitRejectsMalformedAmountStringWith400(t *testing.T) {
	s := newTestServer(t, "")
	body := []byte(`{"version":1,"chain_id":"` + types.ChainID + `","type":0,"alg_id":"ML-DSA-65","amount":"1.5","fee":"1000","nonce":0}`)
	rec := httptest.NewRecorder()
	s.handleSubmitTx(rec, httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "amount") {
		t.Fatalf("got %d (%s), want 400 naming amount", rec.Code, rec.Body.String())
	}
}

func TestX402VerifyAcceptsStringAmounts(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)
	s := newTestServer(t, "")
	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = payerPub.Bytes()
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatal(err)
	}

	var nonce [32]byte
	nonce[0] = 0xAB
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID, From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatal(err)
	}
	reqs := &x402.PaymentRequirements{PayTo: payee, Amount: big.NewInt(500), ValidAfter: now - 30, ValidBefore: now + 300}
	raw, _ := json.Marshal(map[string]any{"requirements": reqs, "payload": payload})
	quoted := bytes.ReplaceAll(raw, []byte(`"amount":500,`), []byte(`"amount":"500",`))
	if bytes.Equal(raw, quoted) {
		t.Fatalf("test setup: %s", raw)
	}

	rec := httptest.NewRecorder()
	s.handleX402Verify(rec, httptest.NewRequest(http.MethodPost, "/x402/verify", bytes.NewReader(quoted)))
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}
	var res x402.VerifyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("string-amount payment not valid: %s", rec.Body.String())
	}
}
