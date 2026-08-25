package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
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

func newTestServer(t *testing.T, authToken string, x402 bool) *Server {
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
		ListenAddr:        ":0",
		AuthToken:         authToken,
		X402SettleEnabled: x402,
	}, sdb, ex, pool, nil, nil)
}

// /x402/settle must be disabled (503) unless explicitly enabled, and require the
// admin token (401) when enabled.
func TestX402SettleGating(t *testing.T) {
	// Disabled by default → 503, regardless of token.
	s := newTestServer(t, "secret", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x402/settle", strings.NewReader("{}"))
	req.Header.Set("X-Node-Auth", "secret")
	s.handleX402Settle(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: got %d, want 503", rec.Code)
	}

	// Enabled but no/incorrect token → 401.
	s = newTestServer(t, "secret", true)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/x402/settle", strings.NewReader("{}"))
	s.handleX402Settle(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/x402/settle", strings.NewReader("{}"))
	req.Header.Set("X-Node-Auth", "wrong")
	s.handleX402Settle(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", rec.Code)
	}

	// Enabled + correct token → passes the gate (body is empty/invalid → not 503/401).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/x402/settle", strings.NewReader("{}"))
	req.Header.Set("X-Node-Auth", "secret")
	s.handleX402Settle(rec, req)
	if rec.Code == http.StatusServiceUnavailable || rec.Code == http.StatusUnauthorized {
		t.Fatalf("authorized request blocked by gate: %d", rec.Code)
	}
}
