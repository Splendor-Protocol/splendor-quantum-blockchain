package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func residualPolicy(t *testing.T, trusted ...string) ClientIPPolicy {
	t.Helper()
	p, err := NewClientIPPolicy(true, trusted, "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	return p
}

func reqFrom(remote string, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// A /64 is what a single site is handed — it is the standard residential and
// cloud allocation — so keying a budget per /128 gives one tenant 18 quintillion
// identities and every per-source limit becomes decorative.
func TestIPv6BudgetsAggregateToA64(t *testing.T) {
	p := residualPolicy(t, "192.0.2.10")

	a := p.clientKey(reqFrom("192.0.2.10:443", "2001:db8:1:1::1"))
	b := p.clientKey(reqFrom("192.0.2.10:443", "2001:db8:1:1::ffff"))
	if a != b {
		t.Fatalf("two addresses in one /64 got different budgets: %q vs %q", a, b)
	}

	// A different /64 is a different site and keeps its own budget.
	c := p.clientKey(reqFrom("192.0.2.10:443", "2001:db8:1:2::1"))
	if c == a {
		t.Fatalf("addresses in different /64s share a budget: both %q", a)
	}

	// The same rule on the socket plane, or the hole just moves to whichever
	// plane the operator is not proxying.
	d := p.clientKey(reqFrom("[2001:db8:9:9::5]:5000", ""))
	e := p.clientKey(reqFrom("[2001:db8:9:9::6]:5000", ""))
	if d != e {
		t.Fatalf("socket-keyed addresses in one /64 got different budgets: %q vs %q", d, e)
	}
	if !strings.HasPrefix(d, "ip:") {
		t.Fatalf("socket key %q lost its namespace", d)
	}

	// IPv4 is still keyed exactly: there a single address really is the unit.
	f := p.clientKey(reqFrom("192.0.2.10:443", "203.0.113.9"))
	g := p.clientKey(reqFrom("192.0.2.10:443", "203.0.113.10"))
	if f == g {
		t.Fatalf("two IPv4 addresses were aggregated: both %q", f)
	}
}

// A chain nobody can attribute falls back to the socket key, which is safe for
// honest traffic but hands the caller a choice: send something unreadable and be
// keyed as the proxy — the bucket every unattributable request shares — instead
// of as yourself. Refusing removes the choice.
func TestUnattributableForwardedChainIsRefused(t *testing.T) {
	p := residualPolicy(t, "192.0.2.10")

	long := strings.TrimSuffix(strings.Repeat("203.0.113.9,", maxForwardedHops+2), ",")
	if !p.forwardedChainIsAbusive(reqFrom("192.0.2.10:443", long)) {
		t.Fatalf("a chain of %d hops was accepted", maxForwardedHops+2)
	}

	huge := strings.TrimSuffix(strings.Repeat("203.0.113.9,", maxForwardedBytes/12+8), ",")
	if len(huge) <= maxForwardedBytes {
		t.Fatalf("test premise: %d bytes should exceed the %d-byte cap", len(huge), maxForwardedBytes)
	}
	if !p.forwardedChainIsAbusive(reqFrom("192.0.2.10:443", huge)) {
		t.Fatal("an oversized chain was accepted")
	}

	// An ordinary chain is not abusive.
	if p.forwardedChainIsAbusive(reqFrom("192.0.2.10:443", "6.6.6.6, 203.0.113.9")) {
		t.Fatal("an ordinary two-hop chain was refused")
	}
	// Nor is a merely malformed one: a proxy can emit that, and the socket
	// fallback is the right answer there rather than a refusal.
	if p.forwardedChainIsAbusive(reqFrom("192.0.2.10:443", "<script>")) {
		t.Fatal("a malformed chain was refused rather than falling back")
	}
	// And a caller that did not come through a trusted proxy is not judged on a
	// header that was never going to be read.
	if p.forwardedChainIsAbusive(reqFrom("198.51.100.7:443", long)) {
		t.Fatal("an untrusted peer's chain was refused rather than ignored")
	}
}

// With trust off, the header is not read at all, so nothing about it can be
// abusive and nothing about it can change a key.
func TestForwardedHeaderIsInertWithoutProxyTrust(t *testing.T) {
	p, err := NewClientIPPolicy(false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.TrimSuffix(strings.Repeat("203.0.113.9,", maxForwardedHops+2), ",")
	if p.forwardedChainIsAbusive(reqFrom("192.0.2.10:443", long)) {
		t.Fatal("a chain was refused on a node that does not read the header")
	}
	if got := p.clientKey(reqFrom("192.0.2.10:443", "6.6.6.6")); got != "ip:192.0.2.10" {
		t.Fatalf("key %q, want the socket address", got)
	}
}

// Trust must follow the entries that parsed, not the length of the slice.
//
// Reading the raw length meant one blank entry — a trailing comma in a list, an
// unset variable expanded into the flag — turned trust on while leaving the
// prefix list empty, and an empty prefix list means "every peer is my proxy". So
// --trusted-proxy "" honoured the forwarded header from anyone, silently: the
// bare-flag warning could not fire either, because trustProxy was false.
func TestBlankTrustedProxyEntriesDoNotEnableTrust(t *testing.T) {
	for _, entries := range [][]string{{""}, {"   "}, {"", ""}, {"\t"}} {
		p, err := NewClientIPPolicy(false, entries, "")
		if err != nil {
			t.Fatalf("NewClientIPPolicy(%q): %v", entries, err)
		}
		// A caller reaching the port directly, claiming to be somebody else.
		got := p.clientKey(reqFrom("198.51.100.7:443", "203.0.113.9"))
		if got != "ip:198.51.100.7" {
			t.Fatalf("with trusted proxies %q the forwarded header decided the key (%q); "+
				"a blank entry must not turn trust on", entries, got)
		}
		if p.forwardedChainIsAbusive(reqFrom("198.51.100.7:443", "203.0.113.9")) {
			t.Fatalf("with trusted proxies %q the header is read at all", entries)
		}
	}

	// A blank entry alongside a real one is inert, and the real one still counts.
	p, err := NewClientIPPolicy(false, []string{"192.0.2.10", ""}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.clientKey(reqFrom("192.0.2.10:443", "203.0.113.9")); got != "fwd:203.0.113.9" {
		t.Fatalf("a real trusted proxy stopped working beside a blank entry: %q", got)
	}
	if got := p.clientKey(reqFrom("198.51.100.7:443", "203.0.113.9")); got != "ip:198.51.100.7" {
		t.Fatalf("a peer that is not the named proxy was trusted: %q", got)
	}

	// And a client-ip header still needs trust from somewhere, which blanks do
	// not supply.
	if _, err := NewClientIPPolicy(false, []string{""}, "CF-Connecting-IP"); err == nil {
		t.Fatal("a client-ip header was accepted with no trusted proxy: the header is client-supplied")
	}
}
