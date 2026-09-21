package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// newTestIdentity creates a test node identity
func newTestIdentity(t *testing.T) *NodeIdentity {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	id, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("create node identity: %v", err)
	}
	return id
}

// sealedPeer stands up a peer that serves a signed identity document and
// accepts sealed envelopes, so a send that gets past the pin gate really
// completes rather than merely failing later for transport reasons.
func sealedPeer(t *testing.T, id *NodeIdentity) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(PathIdentity, func(w http.ResponseWriter, _ *http.Request) {
		doc, err := id.Document()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Errorf("encode identity document: %v", err)
		}
	})
	mux.HandleFunc(PathSealed, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// nodePubKeyOf returns the identity key a peer will present.
func nodePubKeyOf(t *testing.T, id *NodeIdentity) []byte {
	t.Helper()
	doc, err := id.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	return doc.NodePubKey
}

// mustPin fails the test if the operator's pin is not accepted.
func mustPin(t *testing.T, c *IdentityCache, peerURL string, key []byte) {
	t.Helper()
	if err := c.Pin(peerURL, key); err != nil {
		t.Fatalf("pin %s: %v", peerURL, err)
	}
}

// respellings of a peer URL that all reach the same box. Each is something a
// peer can put in an announce body or a /peers list.
func respellings(base string) []string {
	return []string{
		base,
		base + "/",
		base + "//",
		"HTTP" + strings.TrimPrefix(base, "http"),
		"HTTP" + strings.TrimPrefix(base, "http") + "/",
	}
}

// With pins required, a peer nobody vouched for must be refused outright —
// not pinned to whatever identity it happens to serve first.
func TestRequirePinsRefusesUnpinnedPeer(t *testing.T) {
	peer := newTestIdentity(t)
	srv := sealedPeer(t, peer)

	cache := NewIdentityCache()
	cache.RequirePins(true)

	_, err := cache.Get(srv.URL)
	if err == nil {
		t.Fatal("unpinned peer was accepted while pins are required")
	}
	if !errors.Is(err, ErrPeerNotPinned) {
		t.Fatalf("refusal should be ErrPeerNotPinned, got: %v", err)
	}
	// The operator has to be able to act on the message without reading source.
	if !strings.Contains(err.Error(), "--peer-pin") {
		t.Fatalf("refusal should name the flag that fixes it, got: %v", err)
	}

	// Refusing must not leave the peer pinned as a side effect: a later run
	// with the flag off would otherwise inherit a trust decision nobody made.
	cache.mu.RLock()
	_, pinned := cache.pinned[srv.URL]
	cache.mu.RUnlock()
	if pinned {
		t.Fatal("a refused peer was recorded as pinned")
	}
}

// Every sealed path — block push, announce, catch-up pull — must hit the gate,
// since each of them is a separate way to start talking to a peer.
func TestRequirePinsCoversEverySealedPath(t *testing.T) {
	peer := newTestIdentity(t)
	srv := sealedPeer(t, peer)

	cache := NewIdentityCache()
	cache.RequirePins(true)

	for _, tc := range []struct {
		path string
		err  error
	}{
		{"block push", cache.SendSealed(srv.URL, "token", KindBlock, []byte(`{}`))},
		{"announce", cache.SendSealed(srv.URL, "token", KindAnnounce, []byte(`{}`))},
	} {
		if !errors.Is(tc.err, ErrPeerNotPinned) {
			t.Fatalf("%s to an unpinned peer should be refused, got: %v", tc.path, tc.err)
		}
	}

	if _, err := cache.QuerySealed(srv.URL, KindGetLatest, nil, 1<<20); !errors.Is(err, ErrPeerNotPinned) {
		t.Fatalf("sealed pull from an unpinned peer should be refused, got: %v", err)
	}
}

// The point of requiring pins is that pinned peers keep working.
func TestRequirePinsAcceptsCorrectlyPinnedPeer(t *testing.T) {
	peer := newTestIdentity(t)
	srv := sealedPeer(t, peer)

	cache := NewIdentityCache()
	cache.RequirePins(true)
	mustPin(t, cache, srv.URL, nodePubKeyOf(t, peer))

	doc, err := cache.Get(srv.URL)
	if err != nil {
		t.Fatalf("correctly pinned peer was refused: %v", err)
	}
	if doc == nil {
		t.Fatal("no identity document returned for a pinned peer")
	}
	if err := cache.SendSealed(srv.URL, "token", KindBlock, []byte(`{}`)); err != nil {
		t.Fatalf("sealed push to a correctly pinned peer failed: %v", err)
	}
}

// A wrong pin must always lose, flag or no flag: the pin is the operator's
// statement about who the peer is, and a peer that contradicts it is not the
// peer regardless of how permissive the policy is elsewhere.
func TestWrongPinIsRefusedWithoutTheFlag(t *testing.T) {
	peer := newTestIdentity(t)
	impostorKey := nodePubKeyOf(t, newTestIdentity(t))
	srv := sealedPeer(t, peer)

	cache := NewIdentityCache()
	mustPin(t, cache, srv.URL, impostorKey) // pins required is deliberately left OFF

	_, err := cache.Get(srv.URL)
	if err == nil {
		t.Fatal("peer presenting a key other than its pin was accepted")
	}
	if !strings.Contains(err.Error(), "pinned key") {
		t.Fatalf("error should name the pin mismatch, got: %v", err)
	}
	if errors.Is(err, ErrPeerNotPinned) {
		t.Fatal("a mismatch is not a missing pin — the two must stay distinguishable")
	}
}

// The pin binds a PEER, not a string. Respelling the URL — the one thing an
// announce body or a /peers list can freely do — must not produce a second,
// unpinned identity for the same box, or the pin is bypassed by punctuation.
func TestPinHoldsAcrossURLRespellings(t *testing.T) {
	impostor := newTestIdentity(t)
	srv := sealedPeer(t, impostor) // the box we actually reach is hostile
	honestKey := nodePubKeyOf(t, newTestIdentity(t))

	for _, spelling := range respellings(srv.URL) {
		cache := NewIdentityCache()
		mustPin(t, cache, srv.URL, honestKey) // pinned under the plain spelling

		_, err := cache.Get(spelling)
		if err == nil {
			t.Fatalf("%s: impostor accepted — the pin was dodged by respelling the URL", spelling)
		}
		if errors.Is(err, ErrPeerNotPinned) {
			t.Fatalf("%s: respelling turned a pinned peer into an unpinned one", spelling)
		}
		if !strings.Contains(err.Error(), "pinned key") {
			t.Fatalf("%s: expected a pin mismatch, got: %v", spelling, err)
		}

		// And nothing may be recorded under the respelling, or the next call
		// would find a "pin" the operator never set.
		cache.mu.RLock()
		extra := len(cache.pinned)
		cache.mu.RUnlock()
		if extra != 1 {
			t.Fatalf("%s: respelling created %d pin entries, want 1", spelling, extra)
		}
	}
}

// The same normalisation must not lock a legitimate peer out: an operator who
// pins one spelling and configures another still has one working peer, and
// under --require-peer-pins that peer is not refused.
func TestPinnedPeerIsReachableUnderAnySpelling(t *testing.T) {
	peer := newTestIdentity(t)
	srv := sealedPeer(t, peer)

	for _, spelling := range respellings(srv.URL) {
		cache := NewIdentityCache()
		cache.RequirePins(true)
		mustPin(t, cache, spelling, nodePubKeyOf(t, peer))

		if _, err := cache.Get(srv.URL); err != nil {
			t.Fatalf("pinned as %s, dialled as %s: %v", spelling, srv.URL, err)
		}
		if err := cache.SendSealed(spelling, "token", KindBlock, []byte(`{}`)); err != nil {
			t.Fatalf("sealed push to %s failed: %v", spelling, err)
		}
	}
}

func TestCanonicalPeerURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://peer.example.com", "http://peer.example.com"},
		{"HTTP://Peer.Example.COM/", "http://peer.example.com"},
		{"http://peer.example.com:80", "http://peer.example.com"},
		{"https://peer.example.com:443//", "https://peer.example.com"},
		{"https://peer.example.com:8443/", "https://peer.example.com:8443"},
		{"http://peer.example.com/base/", "http://peer.example.com/base"},
		{"http://[2001:DB8::1]:80", "http://[2001:db8::1]"},
		{"  http://peer.example.com/  ", "http://peer.example.com"},
		// Not a URL: keep the caller's spelling rather than invent one. It will
		// match no pin, which is the safe way to be wrong.
		{"not a url", "not a url"},
	} {
		if got := canonicalPeerURL(tc.in); got != tc.want {
			t.Errorf("canonicalPeerURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An empty pin is the worst possible entry: Verify reads a zero-length
// expectation as "no expectation" and accepts any identity, while the peer
// looks pinned and so is never pinned on first contact either.
func TestPinRejectsEmptyKey(t *testing.T) {
	impostor := newTestIdentity(t)
	srv := sealedPeer(t, impostor)

	cache := NewIdentityCache()
	if err := cache.Pin(srv.URL, nil); err == nil {
		t.Fatal("an empty pin was accepted")
	}

	// Nothing was stored, so the peer is simply unpinned: refused when pins are
	// required, and pinned to what it serves when they are not — never
	// permanently unauthenticated behind a pin that does not exist.
	cache.RequirePins(true)
	if _, err := cache.Get(srv.URL); !errors.Is(err, ErrPeerNotPinned) {
		t.Fatalf("peer with a rejected pin should be unpinned, got: %v", err)
	}

	cache.RequirePins(false)
	if _, err := cache.Get(srv.URL); err != nil {
		t.Fatalf("peer with a rejected pin should fall back to first-use trust: %v", err)
	}
	cache.mu.RLock()
	stored := cache.pinned[srv.URL]
	cache.mu.RUnlock()
	if !bytesEqualConstantTime(stored, nodePubKeyOf(t, impostor)) {
		t.Fatal("first contact did not pin the identity it served")
	}
}

// Default off: an unpinned peer is still trusted on first contact and then held
// to that identity, exactly as before this flag existed.
func TestDefaultStillTrustsOnFirstUse(t *testing.T) {
	peer := newTestIdentity(t)
	srv := sealedPeer(t, peer)

	cache := NewIdentityCache()
	if cache.PinsRequired() {
		t.Fatal("pins must not be required by default")
	}

	if _, err := cache.Get(srv.URL); err != nil {
		t.Fatalf("first contact should be trusted by default: %v", err)
	}

	cache.mu.RLock()
	pin := cache.pinned[srv.URL]
	cache.mu.RUnlock()
	if !bytesEqualConstantTime(pin, nodePubKeyOf(t, peer)) {
		t.Fatal("first contact did not pin the identity it served")
	}

	// ...and the peer is then held to it: a substituted identity is rejected.
	cache.Forget(srv.URL)
	cache.mu.Lock()
	cache.pinned[srv.URL] = nodePubKeyOf(t, newTestIdentity(t))
	cache.mu.Unlock()
	if _, err := cache.Get(srv.URL); err == nil {
		t.Fatal("a substituted identity was accepted after first-use pinning")
	}
}

// A refused peer must not be reached by the cleartext fallback. That path
// carries no identity at all and puts the write-plane token in a header, so
// retrying there would deliver the block, and the credential, to exactly the
// peer the operator declined to trust.
func TestPinRefusalIsNeverRetriedInPlaintext(t *testing.T) {
	hits := make(chan string, 4)
	mux := http.NewServeMux()
	for _, path := range []string{"/block", "/peers/announce", "/block/latest"} {
		p := path
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) {
			hits <- p
			w.WriteHeader(http.StatusOK)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The combination the CLI refuses to start with, asserted at the library
	// level so the guarantee does not rest on flag validation alone.
	b := NewBroadcaster(nil, "write-plane-token", true)
	b.Identities().RequirePins(true)
	b.AddPeer(srv.URL)

	if err := b.sendBlock(srv.URL, []byte(`{}`)); !errors.Is(err, ErrPeerNotPinned) {
		t.Fatalf("block push to an unpinned peer should be refused, got: %v", err)
	}

	b.AnnounceSelf("http://self.example.com", 1)

	cfg := DefaultSyncConfig(nil)
	cfg.Identities = b.Identities()
	cfg.AllowPlaintext = true
	if _, err := NewBlockFetcher(cfg).fetchBlockJSON(context.Background(), srv.URL, KindGetLatest, []byte(`{}`), "/block/latest", maxSyncBodyBytes); !errors.Is(err, ErrPeerNotPinned) {
		t.Fatalf("catch-up pull from an unpinned peer should be refused, got: %v", err)
	}

	select {
	case path := <-hits:
		t.Fatalf("a pin-refused peer was retried in PLAINTEXT at %s", path)
	case <-time.After(250 * time.Millisecond): // AnnounceSelf sends asynchronously
	}
}
