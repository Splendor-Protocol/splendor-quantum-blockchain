package rpc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// These began as an adversarial audit of the first --trust-proxy fix and found
// it open: that version took the LEFT-MOST X-Forwarded-For entry, which is the
// one entry a caller writes. Every real appending proxy (nginx's
// $proxy_add_x_forwarded_for, Cloudflare, cloudflared) preserves what the
// client sent and appends its own view to the RIGHT, so left-most meant
// attacker-controlled. They are kept as regression tests for the policy that
// replaced it. Each one fails against the left-most parse.

func policyOrFail(t *testing.T, trust bool, trusted []string, header string) ClientIPPolicy {
	t.Helper()
	p, err := NewClientIPPolicy(trust, trusted, header)
	if err != nil {
		t.Fatalf("NewClientIPPolicy(%v, %v, %q): %v", trust, trusted, header, err)
	}
	return p
}

// The sealed per-source key and the rate limiter's key must agree on every
// input, not just the happy path: two different parses would let a caller be
// one identity to the limiter and another to the sealed cap. They are one
// implementation now, and this holds them to it.
func TestQASealedKeyMatchesRateLimiterKeyExactly(t *testing.T) {
	cases := []struct{ name, remote, xff string }{
		{"absent", "192.0.2.10:1234", ""},
		{"single", "192.0.2.10:1234", "198.51.100.7"},
		{"multi_hop", "192.0.2.10:1234", "198.51.100.7, 10.0.0.1, 10.0.0.2"},
		{"blank", "192.0.2.10:1234", "   "},
		{"leading_comma", "192.0.2.10:1234", ", 198.51.100.7"},
		{"only_comma", "192.0.2.10:1234", ","},
		{"garbage", "192.0.2.10:1234", "not-an-ip-at-all"},
		{"ipv6", "[2001:db8::1]:443", "2001:db8::99"},
		{"no_port_remote", "192.0.2.10", "198.51.100.7"},
		{"tab_padded", "192.0.2.10:1234", "\t198.51.100.7\t"},
	}
	for _, trust := range []bool{false, true} {
		policy := policyOrFail(t, trust, nil, "")
		rl := NewRateLimiter(1, 1, policy)
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			want := rl.clientKey(req)
			got := sealedClientKey(req, policy)
			if got != want {
				t.Errorf("trust=%v %s: sealed key %q != limiter key %q", trust, tc.name, got, want)
			}
		}
	}
}

// With trust OFF a spoofed header must never change the key: otherwise one
// client mints a fresh quota per request.
func TestQASpoofedHeaderCannotMintQuotaWhenTrustOff(t *testing.T) {
	policy := policyOrFail(t, false, nil, "")
	slots := newSealedSourceSlots(2)
	for i, spoof := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"} {
		req := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("X-Forwarded-For", spoof)
		ok := slots.acquire(sealedClientKey(req, policy))
		if i < 2 && !ok {
			t.Fatalf("request %d wrongly shed", i)
		}
		if i >= 2 && ok {
			t.Fatalf("spoofed X-Forwarded-For %q minted a fresh source budget with trust off", spoof)
		}
	}
}

// The original finding. Behind an appending proxy the caller owns the
// left-most entry, so a left-most parse gives one client an unlimited supply
// of identities. The right-most-untrusted walk must collapse all 50 of them
// onto the single entry the proxy itself wrote.
func TestQARotatingForwardedEntriesCannotEscapeTheSourceCap(t *testing.T) {
	policy := policyOrFail(t, true, nil, "")
	const cap = 2
	slots := newSealedSourceSlots(cap)
	admitted := 0
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
		// The tunnel's socket address; every hostile client arrives here.
		req.RemoteAddr = "10.0.0.1:9999"
		// The client sent "X-Forwarded-For: <forged>" and the proxy appended
		// its own view of the real peer on the right.
		req.Header.Set("X-Forwarded-For", forge(i)+", 203.0.113.55")
		if slots.acquire(sealedClientKey(req, policy)) {
			admitted++
		}
	}
	if admitted > cap {
		t.Fatalf("one client held %d concurrent sealed slots against a cap of %d by rotating the left-most forwarded entry", admitted, cap)
	}
}

// The same parse decides whether a caller can claim SOMEONE ELSE's address.
// Under a left-most parse an attacker exhausts a victim's budget by naming it;
// under the right-most walk the attacker only ever spends its own.
func TestQAForwardedEntryCannotPinAnotherPeersBudget(t *testing.T) {
	policy := policyOrFail(t, true, nil, "")
	victim := newSealedSourceSlots(2)

	// Attacker, arriving through the proxy, claims to be the victim.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
		req.RemoteAddr = "10.0.0.1:9999"
		req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.55")
		victim.acquire(sealedClientKey(req, policy))
	}

	// The real 198.51.100.7, arriving through the same proxy, must still be
	// served: the attacker's traffic was charged to 203.0.113.55, not here.
	real := httptest.NewRequest(http.MethodPost, network.PathSealed, nil)
	real.RemoteAddr = "10.0.0.1:9999"
	real.Header.Set("X-Forwarded-For", "198.51.100.7")
	if !victim.acquire(sealedClientKey(real, policy)) {
		t.Fatal("an attacker pinned another peer's source budget by claiming its address in a forwarded entry")
	}
}

func forge(i int) string {
	return fmt.Sprintf("198.51.100.%d", i%254+1)
}
