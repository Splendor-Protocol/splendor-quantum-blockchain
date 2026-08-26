package network

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	cache.Pin(srv.URL, nodePubKeyOf(t, peer))

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
	cache.Pin(srv.URL, impostorKey) // pins required is deliberately left OFF

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
