package rpc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The client key decides two budgets: the per-source sealed slot cap and the
// per-IP token bucket. Both used to take the LEFT-most X-Forwarded-For entry
// when --trust-proxy was on, which is the one entry an attacker writes: every
// appending proxy (nginx's $proxy_add_x_forwarded_for, Cloudflare, cloudflared,
// most load balancers) puts its own view of the peer at the RIGHT and passes
// whatever the client sent through unchanged to its left.
//
// The correct selection is the right-most entry that is not a configured
// trusted proxy — see RFC 7239 §7.1 and the "rightmost-ish" algorithm in
// https://adam-p.ca/blog/2022/03/x-forwarded-for/. These tests pin that.

// bothClientKeys returns the key each of the two call sites derives for the
// same request. They must always agree: one budget being spoofable while the
// other is not is the same bug twice.
func bothClientKeys(r *http.Request, trustProxy bool) (sealed, limited string) {
	p := mustPolicy(trustProxy, nil, "")
	return sealedClientKey(r, p), NewRateLimiter(1, 1, p).clientKey(r)
}

func mustPolicy(trust bool, trusted []string, header string) ClientIPPolicy {
	p, err := NewClientIPPolicy(trust, trusted, header)
	if err != nil {
		panic(err)
	}
	return p
}

func keyFor(t *testing.T, r *http.Request, trustProxy bool) string {
	t.Helper()
	sealed, limited := bothClientKeys(r, trustProxy)
	if sealed != limited {
		t.Fatalf("sealed key %q and rate-limiter key %q disagree for the same request", sealed, limited)
	}
	return sealed
}

func reqWith(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/tx", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

// The table QA drove: every shape of the header a hostile client can send.
func TestClientKeyRightmostUntrustedParse(t *testing.T) {
	const peer = "192.0.2.10:1234"
	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"header absent", peer, nil, "ip:192.0.2.10"},
		{"multi hop", peer, []string{"198.51.100.7, 10.0.0.1"}, "fwd:10.0.0.1"},
		{"blank", peer, []string{"   "}, "ip:192.0.2.10"},
		{"leading comma", peer, []string{", 203.0.113.9"}, "fwd:203.0.113.9"},
		{"garbage", peer, []string{"not-an-ip"}, "ip:192.0.2.10"},
		{"garbage left of a genuine hop", peer, []string{"<script>, 203.0.113.9"}, "fwd:203.0.113.9"},
		{"spoofed leftmost with a genuine appended hop", peer,
			[]string{"6.6.6.6, 203.0.113.9"}, "fwd:203.0.113.9"},
		{"ipv6 bare keys per /64", peer, []string{"2001:db8::1"}, "fwd:2001:db8::/64"},
		{"ipv6 bracketed keys per /64", peer, []string{"[2001:db8::1]"}, "fwd:2001:db8::/64"},
		{"ipv6 bracketed with port keys per /64", peer, []string{"[2001:db8::1]:443"}, "fwd:2001:db8::/64"},
		{"ipv4 with port", peer, []string{"203.0.113.9:51234"}, "fwd:203.0.113.9"},
		{"tab padding", peer, []string{"6.6.6.6,\t203.0.113.9\t"}, "fwd:203.0.113.9"},
		{"trailing empty entry breaks the chain", peer, []string{"203.0.113.9, "}, "ip:192.0.2.10"},
		{"two header instances join in order", peer,
			[]string{"6.6.6.6", "203.0.113.9"}, "fwd:203.0.113.9"},
		{"port-less remote address", "192.0.2.77", nil, "ip:192.0.2.77"},
		{"ipv6 remote address keys per /64", "[2001:db8::2]:5000", nil, "ip:2001:db8::/64"},
		{"ipv4-mapped ipv6 normalises", peer, []string{"::ffff:203.0.113.9"}, "fwd:203.0.113.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keyFor(t, reqWith(tc.remote, tc.xff...), true); got != tc.want {
				t.Fatalf("key %q, want %q", got, tc.want)
			}
		})
	}
}

// With the operator opt-in off, the socket address stays authoritative no
// matter what the client claims.
func TestClientKeyIgnoresHeaderWithoutTrust(t *testing.T) {
	r := reqWith("192.0.2.10:1234", "198.51.100.7, 10.0.0.1")
	if got := keyFor(t, r, false); got != "ip:192.0.2.10" {
		t.Fatalf("trust off keyed on %q, want the socket address", got)
	}
}

// The live exploit: 50 requests through one tunnel, each rotating the
// left-most entry, must not mint 50 budgets. The tunnel's own appended hop is
// the only entry that is not attacker-written, so all 50 collapse to one key.
func TestRotatingForwardedHeaderCannotMintBudgets(t *testing.T) {
	keys := map[string]bool{}
	for i := 0; i < 50; i++ {
		r := reqWith("127.0.0.1:8443", fmt.Sprintf("10.%d.%d.%d, 203.0.113.9", i, i, i))
		keys[keyFor(t, r, true)] = true
	}
	if len(keys) != 1 {
		t.Fatalf("%d distinct keys from 50 rotated headers behind one tunnel, want 1", len(keys))
	}
	if !keys["fwd:203.0.113.9"] {
		t.Fatalf("keys %v, want the tunnel-appended hop fwd:203.0.113.9", keys)
	}
}

// A client must never be able to name another client's identity. A direct
// caller from 192.0.2.99 and a forwarded claim of 192.0.2.99 live in separate
// namespaces, so claiming it cannot pin the victim's budget.
func TestForwardedClaimCannotImpersonateASocketPeer(t *testing.T) {
	victim := keyFor(t, reqWith("192.0.2.99:9000"), true)
	attacker := keyFor(t, reqWith("192.0.2.10:1234", "192.0.2.99"), true)
	if victim == attacker {
		t.Fatalf("a forwarded claim of %q selected the victim's own key %q", "192.0.2.99", victim)
	}
}

// Multi-hop ingress: cloudflared in front of nginx. Both hops are ours, so
// both are skipped and the client is the entry in front of them — the same
// idea Envoy spells as xff_num_trusted_hops, keyed on who rather than on how
// many so it stays right when the hop count changes.
func TestTrustedProxyCIDRsAreSkipped(t *testing.T) {
	p := mustPolicy(false, []string{"10.0.0.0/8", "172.16.0.5"}, "")
	r := reqWith("10.0.0.9:443", "6.6.6.6, 203.0.113.9, 172.16.0.5, 10.0.0.2")
	if got := p.clientKey(r); got != "fwd:203.0.113.9" {
		t.Fatalf("key %q, want fwd:203.0.113.9", got)
	}
	if got := sealedClientKey(r, p); got != p.clientKey(r) {
		t.Fatalf("sealed key %q disagrees with the limiter key %q", got, p.clientKey(r))
	}
}

// With a trusted-proxy list, a connection that did NOT come through one of
// them is identified by its socket address whatever it claims. This is the
// case the bare boolean cannot express, and the one that closes header
// spoofing completely.
func TestDirectConnectionIgnoresTheHeaderWhenProxiesAreNamed(t *testing.T) {
	p := mustPolicy(false, []string{"10.0.0.0/8"}, "")
	keys := map[string]bool{}
	for i := 0; i < 50; i++ {
		r := reqWith("198.51.100.4:33000", fmt.Sprintf("203.0.113.%d", i))
		keys[p.clientKey(r)] = true
	}
	if len(keys) != 1 || !keys["ip:198.51.100.4"] {
		t.Fatalf("a direct caller minted %d keys %v, want one socket key", len(keys), keys)
	}
}

// A chain made only of our own proxies names no client. Falling back to the
// socket address is coarse but never selects something a caller wrote.
func TestAllTrustedChainFallsBackToTheSocket(t *testing.T) {
	p := mustPolicy(false, []string{"10.0.0.0/8"}, "")
	r := reqWith("10.0.0.9:443", "10.0.0.1, 10.0.0.2")
	if got := p.clientKey(r); got != "ip:10.0.0.9" {
		t.Fatalf("key %q, want the socket address", got)
	}
}

// A single-valued header the ingress overwrites is preferred when named, and
// is honoured only from a trusted peer: unscoped, it is free-form text the
// first time somebody reaches the origin directly.
func TestSingleValuedIngressHeader(t *testing.T) {
	p := mustPolicy(false, []string{"10.0.0.0/8"}, "CF-Connecting-IP")

	through := reqWith("10.0.0.9:443", "6.6.6.6, 203.0.113.9")
	through.Header.Set("CF-Connecting-IP", "198.51.100.23")
	if got := p.clientKey(through); got != "fwd:198.51.100.23" {
		t.Fatalf("key %q, want fwd:198.51.100.23 from the ingress header", got)
	}

	direct := reqWith("198.51.100.4:33000")
	direct.Header.Set("CF-Connecting-IP", "203.0.113.1")
	if got := p.clientKey(direct); got != "ip:198.51.100.4" {
		t.Fatalf("a direct caller set CF-Connecting-IP and got %q", got)
	}

	// Named but not set by the ingress: fall back, do not reach for the chain.
	missing := reqWith("10.0.0.9:443", "6.6.6.6, 203.0.113.9")
	if got := p.clientKey(missing); got != "ip:10.0.0.9" {
		t.Fatalf("key %q, want the socket address when the named header is absent", got)
	}
}

// Naming a header without a proxy to scope it is a configuration error, not a
// quietly-accepted setting.
func TestClientIPHeaderNeedsATrustedProxy(t *testing.T) {
	if _, err := NewClientIPPolicy(false, nil, "CF-Connecting-IP"); err == nil {
		t.Fatal("an unscoped client-ip header was accepted")
	}
	if _, err := NewClientIPPolicy(false, []string{"not-a-cidr"}, ""); err == nil {
		t.Fatal("a malformed trusted proxy was accepted")
	}
}

// An absurd chain must not turn into work. Past the hop or byte bound the
// header is unusable and the socket address stands.
func TestOversizeForwardedChainIsNotHonoured(t *testing.T) {
	p := mustPolicy(true, nil, "")
	hops := make([]string, 0, 4000)
	for i := 0; i < 4000; i++ {
		hops = append(hops, "10.0.0.1")
	}
	r := reqWith("192.0.2.10:1234", strings.Join(hops, ","))
	if got := p.clientKey(r); got != "ip:192.0.2.10" {
		t.Fatalf("key %q, want the socket address for an oversize chain", got)
	}
}
