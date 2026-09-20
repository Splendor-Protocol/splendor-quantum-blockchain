package rpc

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

func sealedIdentity(t *testing.T) *network.NodeIdentity {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	id, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// One source must not occupy every decrypt slot. A second request from the
// same IP is shed; a different IP still reaches the decrypt path.
func TestSealedPerSourceCapShedsSameIP(t *testing.T) {
	identity := sealedIdentity(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity:     identity,
		Token:        "write-secret",
		MaxInFlight:  8,
		MaxPerSource: 1,
		HandleBlock: func([]byte) error {
			entered <- struct{}{}
			<-release
			return nil
		},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	cache := network.NewIdentityCache()
	first := make(chan error, 1)
	go func() {
		first <- cache.SendSealed(srv.URL, "write-secret", network.KindBlock, []byte(`{}`))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first sealed request never reached handler")
	}

	err := cache.SendSealed(srv.URL, "write-secret", network.KindBlock, []byte(`{}`))
	if err == nil {
		t.Fatal("second request from the same source was accepted")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected 503 shed, got %v", err)
	}

	// A different RemoteAddr must not be blamed on the per-source cap.
	other := httptest.NewRequest(http.MethodPost, network.PathSealed, bytes.NewReader([]byte("nope")))
	other.RemoteAddr = "203.0.113.99:9"
	other.ContentLength = 4
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, other)
	if rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "this source") {
		t.Fatalf("other source was shed on per-source cap: %s", rec.Body.String())
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("held request failed: %v", err)
	}
}

func TestSealedOversizeContentLengthRejectedBeforeSlot(t *testing.T) {
	identity := sealedIdentity(t)
	router := mux.NewRouter()
	var handled int
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "t",
		HandleBlock: func([]byte) error {
			handled++
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, network.PathSealed, bytes.NewReader(nil))
	req.ContentLength = maxSealedBody + 1
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
	if handled != 0 {
		t.Fatal("oversize body reached the handler")
	}
}

// The per-source key must honour X-Forwarded-For only behind a trusted proxy,
// matching the tx/x402 rate limiter, and it must take the entry the PROXY
// wrote rather than the one the caller did.
//
// This test used to assert the left-most entry. That is the wrong end: every
// appending proxy -- nginx's $proxy_add_x_forwarded_for, Cloudflare,
// cloudflared -- preserves what the client sent and appends its own view on
// the right, so the left-most entry is attacker-controlled and one client
// could mint an identity per request. Both planes now resolve the caller
// through one ClientIPPolicy; see TestQARotatingForwardedEntriesCannotEscape
// TheSourceCap for the attack this ordering refuses.
func TestSealedClientKeyHonorsTrustProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
	req.RemoteAddr = "192.0.2.10:1234"
	// The caller claimed 198.51.100.7; the proxy appended what it actually saw.
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")

	off, err := NewClientIPPolicy(false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sealedClientKey(req, off); got != socketKeyPrefix+"192.0.2.10" {
		t.Fatalf("untrusted keyed on %q, want the socket address 192.0.2.10", got)
	}

	on, err := NewClientIPPolicy(true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sealedClientKey(req, on); got != forwardedKeyPrefix+"10.0.0.1" {
		t.Fatalf("trusted keyed on %q, want the entry the proxy appended, 10.0.0.1", got)
	}
}
