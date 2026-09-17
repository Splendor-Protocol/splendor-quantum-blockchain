package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

// The SSRF guard must reject metadata/loopback/private/link-local/CGNAT hosts and
// non-http schemes, while allowing public IP literals.
func TestValidatePeerURLSSRF(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://[::ffff:169.254.169.254]/",         // IPv4-mapped IPv6 metadata
		"http://127.0.0.1:8080",
		"http://[::1]:8080",   // IPv6 loopback
		"http://0.0.0.0:8080", // unspecified
		"http://10.1.2.3",
		"http://192.168.0.5:8080",
		"http://172.16.9.9",
		"http://[fd00::1]:8080", // IPv6 ULA (fc00::/7)
		"http://100.64.0.1",     // CGNAT
		"ftp://example.com",
		"http://", // missing host
		"file:///etc/passwd",
	}
	for _, u := range blocked {
		if err := network.IsSafePeerURL(u); err == nil {
			t.Errorf("expected REJECT for %q, got allow", u)
		}
	}

	allowed := []string{
		"http://93.184.216.34:8080",
		"https://8.8.8.8",
		"http://1.1.1.1:9090",
	}
	for _, u := range allowed {
		if err := network.IsSafePeerURL(u); err != nil {
			t.Errorf("expected ALLOW for %q, got %v", u, err)
		}
	}
}

// AllowPrivatePeers opt-in must let loopback/private peers through (for private-
// network / local multi-node clusters) while still rejecting bad schemes/hosts —
// and must stay OFF by default so the public SSRF protection is the baseline.
func TestAllowPrivatePeersToggle(t *testing.T) {
	// Default: loopback/private rejected.
	if err := network.IsSafePeerURL("http://127.0.0.1:8080"); err == nil {
		t.Fatal("default should reject loopback")
	}

	network.AllowPrivatePeers = true
	defer func() { network.AllowPrivatePeers = false }()

	if err := network.IsSafePeerURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback should be allowed with AllowPrivatePeers: %v", err)
	}
	if err := network.IsSafePeerURL("http://10.0.0.5:8080"); err != nil {
		t.Fatalf("private IP should be allowed with AllowPrivatePeers: %v", err)
	}
	// Scheme/host are still validated even in private mode.
	if err := network.IsSafePeerURL("ftp://example.com"); err == nil {
		t.Fatal("non-http scheme must still be rejected in private mode")
	}
	if err := network.IsSafePeerURL("http://"); err == nil {
		t.Fatal("missing host must still be rejected in private mode")
	}
}

func newTestServer(t *testing.T, authToken string) *Server {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	pool := mempool.New(100, sdb)
	return New(Config{
		ListenAddr: ":0",
		AuthToken:  authToken,
	}, sdb, ex, pool, nil, nil)
}

// Settlement must never move balances inside the HTTP handler. It is only ever
// admitted to the mempool, so a payment that is not backed by a payer-signed
// TxX402 must change nothing at all.
func TestX402SettleRequiresSignedTxAndNeverMutatesState(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)

	s := newTestServer(t, "secret")

	// Fund the payer and register the key so x402 verification can succeed.
	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = payerPub.Bytes()
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var nonce [32]byte
	nonce[0] = 0xAA
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	reqs := &x402.PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300,
	}

	post := func(body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/x402/settle", bytes.NewReader(raw))
		s.handleX402Settle(rec, req)
		return rec
	}

	// A fully valid payment with NO settlement transaction must be refused.
	rec := post(map[string]any{"requirements": reqs, "payload": payload})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing settlement_tx: got %d, want 400", rec.Code)
	}
	if bal, _ := s.stateDB.GetBalance(payee); bal.Sign() != 0 {
		t.Fatalf("payee credited without a settlement tx: %s", bal)
	}

	// A settlement tx that does not match the authorised payment must be refused
	// even though the payment payload itself verifies.
	mismatched := x402.NewSettlementTx(payload, big.NewInt(1000), 0)
	mismatched.Amount = big.NewInt(999_000)
	if err := mismatched.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign mismatched: %v", err)
	}
	rec = post(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": mismatched})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched settlement_tx: got %d, want 400", rec.Code)
	}

	// The correct payer-signed settlement tx is accepted into the mempool — and
	// still does not move balances, because that only happens at block execution.
	settle := x402.NewSettlementTx(payload, big.NewInt(1000), 0)
	if err := settle.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign settlement: %v", err)
	}
	rec = post(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": settle})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid settlement: got %d (%s), want 202", rec.Code, rec.Body.String())
	}
	var queued x402.SettleResult
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil {
		t.Fatalf("decode settle result: %v", err)
	}
	if queued.Success || queued.Status != x402.SettleSubmitted {
		t.Fatalf("queued settlement described as confirmed: %+v", queued)
	}
	if s.pool.Size() != 1 {
		t.Fatalf("settlement tx not queued: mempool size %d", s.pool.Size())
	}
	if bal, _ := s.stateDB.GetBalance(payee); bal.Sign() != 0 {
		t.Fatalf("handler moved funds outside consensus: payee balance %s", bal)
	}
	if bal, _ := s.stateDB.GetBalance(payer); bal.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("handler debited payer outside consensus: %s", bal)
	}
}

// A payer that has never sent a transaction has no key in the on-chain
// registry. Attaching the key that derives the payer address to the payment
// payload must let /x402/verify accept it, and the payer-signed settlement tx
// (which carries the same first-use key) must then be admitted by /x402/settle.
func TestX402VerifyAcceptsNeverSeenPayer(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)

	s := newTestServer(t, "secret")

	// Fund the payer but register NO key: a fresh, never-seen account.
	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var nonce [32]byte
	nonce[0] = 0xBB
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	reqs := &x402.PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300,
	}

	verify := func(body any) x402.VerifyResult {
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/x402/verify", bytes.NewReader(raw))
		s.handleX402Verify(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/x402/verify: got %d (%s), want 200", rec.Code, rec.Body.String())
		}
		var res x402.VerifyResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode verify result: %v", err)
		}
		return res
	}

	// Without the key the registry has nothing to verify against.
	if res := verify(map[string]any{"requirements": reqs, "payload": payload}); res.Valid {
		t.Fatal("never-seen payer without public_key must not verify")
	}

	// With the derived key attached the same signed payload verifies.
	payload.PublicKey = payerPub.Bytes()
	if res := verify(map[string]any{"requirements": reqs, "payload": payload}); !res.Valid {
		t.Fatalf("never-seen payer with public_key rejected: %s", res.Reason)
	}

	// A key that does not derive the payer is refused over the wire too.
	otherPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	forged := *payload
	forged.PublicKey = otherPub.Bytes()
	if res := verify(map[string]any{"requirements": reqs, "payload": &forged}); res.Valid {
		t.Fatal("public_key that does not derive the payer must be rejected")
	}

	// The settlement tx carries the same first-use key (Sign with includePubKey)
	// and is admitted to the mempool; nothing moves outside consensus.
	settle := x402.NewSettlementTx(payload, big.NewInt(1000), 0)
	if err := settle.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign settlement: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": settle})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x402/settle", bytes.NewReader(raw))
	s.handleX402Settle(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("never-seen payer settlement: got %d (%s), want 202", rec.Code, rec.Body.String())
	}
	var queued x402.SettleResult
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil {
		t.Fatalf("decode settle result: %v", err)
	}
	if queued.Success || queued.Status != x402.SettleSubmitted {
		t.Fatalf("queued settlement described as confirmed: %+v", queued)
	}
	if s.pool.Size() != 1 {
		t.Fatalf("settlement tx not queued: mempool size %d", s.pool.Size())
	}
	if bal, _ := s.stateDB.GetBalance(payee); bal.Sign() != 0 {
		t.Fatalf("handler moved funds outside consensus: payee balance %s", bal)
	}
}

// The write plane must fail closed with no token and reject a wrong token.
func TestWritePlaneFailsClosed(t *testing.T) {
	noToken := NewWriteAuth("")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/block", strings.NewReader("{}"))
	noToken.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler ran with no token configured")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no token configured: got %d, want 503", rec.Code)
	}

	withToken := NewWriteAuth("secret")
	for _, tc := range []struct {
		name, header string
		want         int
	}{
		{"absent", "", http.StatusUnauthorized},
		{"wrong", "nope", http.StatusUnauthorized},
		{"correct", "secret", http.StatusOK},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/block", strings.NewReader("{}"))
		if tc.header != "" {
			req.Header.Set("X-Node-Auth", tc.header)
		}
		withToken.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s token: got %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

// CORS must not echo an allow-origin header for origins that are not on the
// allowlist, and must default to allowing nothing.
func TestCORSAllowlist(t *testing.T) {
	strict := NewCORSPolicy(nil)
	if got := strict.resolve("https://evil.example"); got != "" {
		t.Errorf("empty allowlist echoed origin %q", got)
	}

	allow := NewCORSPolicy([]string{"https://app.splendor.org"})
	if got := allow.resolve("https://app.splendor.org"); got != "https://app.splendor.org" {
		t.Errorf("allowlisted origin not permitted: %q", got)
	}
	if got := allow.resolve("https://evil.example"); got != "" {
		t.Errorf("non-allowlisted origin permitted: %q", got)
	}

	wild := NewCORSPolicy([]string{"*"})
	if got := wild.resolve("https://anything.example"); got != "*" {
		t.Errorf("explicit wildcard opt-in not honoured: %q", got)
	}
}

// The rate limiter must admit the burst then refuse further requests.
func TestRateLimiterBounds(t *testing.T) {
	rl := NewRateLimiter(1, 3, false)
	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow("1.2.3.4") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed %d requests, want burst of 3", allowed)
	}
	// A different client has its own bucket.
	if !rl.Allow("5.6.7.8") {
		t.Fatal("second client was limited by the first client's bucket")
	}
}

func TestX402SettleReportsConfirmedWhenReceiptExists(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)
	s := newTestServer(t, "secret")

	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = payerPub.Bytes()
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var nonce [32]byte
	nonce[0] = 0xCC
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	reqs := &x402.PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300,
	}
	settle := x402.NewSettlementTx(payload, big.NewInt(1000), 0)
	if err := settle.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign settlement: %v", err)
	}
	hash, err := settle.FullHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	s.lookupReceipt = func(h [32]byte) (*types.Receipt, error) {
		if h != hash {
			return nil, errors.New("unknown")
		}
		return &types.Receipt{Status: types.TxStatusSuccess, TxHash: hash}, nil
	}

	raw, _ := json.Marshal(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": settle})
	rec := httptest.NewRecorder()
	s.handleX402Settle(rec, httptest.NewRequest(http.MethodPost, "/x402/settle", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed settlement: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var out x402.SettleResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success || out.Status != x402.SettleConfirmed {
		t.Fatalf("want confirmed success, got %+v", out)
	}
}

func TestX402SettleIdempotentWhenNonceUsed(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)
	s := newTestServer(t, "secret")

	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = payerPub.Bytes()
	s.stateDB.SetAccount(acc)
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var nonce [32]byte
	nonce[0] = 0xDD
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	if err := s.stateDB.MarkX402NonceUsed(x402.NonceKey(payer, nonce), 3); err != nil {
		t.Fatalf("mark nonce: %v", err)
	}
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit nonce: %v", err)
	}

	reqs := &x402.PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300,
	}
	settle := x402.NewSettlementTx(payload, big.NewInt(1000), 0)
	if err := settle.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign settlement: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": settle})
	rec := httptest.NewRecorder()
	s.handleX402Settle(rec, httptest.NewRequest(http.MethodPost, "/x402/settle", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("consumed payment: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var out x402.SettleResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success || out.Status != x402.SettleConfirmed {
		t.Fatalf("want idempotent confirmed, got %+v", out)
	}
	if s.pool.Size() != 0 {
		t.Fatalf("already-consumed payment was re-queued: pool %d", s.pool.Size())
	}
}
