package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// probePeer serves a signed identity document (optionally declaring the
// capability block) and answers every sealed request with 404, counting how
// many sealed POSTs it received.
type probePeer struct {
	srv      *httptest.Server
	identity *NodeIdentity
	probes   atomic.Int64
}

func newProbePeer(t *testing.T, caps *IdentityCaps) *probePeer {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	if caps != nil {
		identity.SetCapabilities(*caps)
	}
	peer := &probePeer{identity: identity}
	peer.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathIdentity:
			doc, err := identity.Document()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(doc)
		case PathSealed:
			peer.probes.Add(1)
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(peer.srv.Close)
	return peer
}

// A legacy peer (no capability declaration) that refuses the coded probe is
// remembered: the next attempt skips the wasted sealed request, and the entry
// clears when a transfer succeeds or expires.
func TestCodedProbeRefusalIsRememberedForUndeclaredPeers(t *testing.T) {
	peer := newProbePeer(t, nil)
	cfg := DefaultSyncConfig(nil)
	cfg.Identities = NewIdentityCache()
	f := NewBlockFetcher(cfg)

	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a refusing peer")
	}
	if peer.probes.Load() != 1 {
		t.Fatalf("first fetch probed %d times, want 1", peer.probes.Load())
	}

	// Second attempt must not probe again: the refusal is remembered.
	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a refusing peer")
	}
	if peer.probes.Load() != 1 {
		t.Fatalf("refused peer probed again: %d sealed requests", peer.probes.Load())
	}

	// Entries expire after the TTL: shrink it, note a fresh refusal under the
	// short TTL, wait it out, and the next attempt must probe again.
	oldTTL := codedProbeRefusalTTL
	codedProbeRefusalTTL = 20 * time.Millisecond
	t.Cleanup(func() { codedProbeRefusalTTL = oldTTL })
	f.cfg.Identities.ForgetCodedRefusal(peer.srv.URL, CodedSnapshots)
	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a refusing peer")
	}
	if peer.probes.Load() != 2 {
		t.Fatalf("cleared refusal did not re-enable probing: %d sealed requests", peer.probes.Load())
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a refusing peer")
	}
	if peer.probes.Load() != 3 {
		t.Fatalf("expired refusal still suppressed probing: %d sealed requests", peer.probes.Load())
	}
}

// A peer that DECLARES coded service but fails transiently is never poisoned:
// its refusals are not remembered, because the signed declaration says the
// failure is worth retrying.
func TestCodedProbeRefusalIsNotRememberedForDeclaredPeers(t *testing.T) {
	caps := IdentityCaps{CodedObjects: true}
	peer := newProbePeer(t, &caps)
	cfg := DefaultSyncConfig(nil)
	cfg.Identities = NewIdentityCache()
	f := NewBlockFetcher(cfg)

	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a failing peer")
	}
	if _, err := f.FetchCodedObject(peer.srv.URL, objects.DefaultLimits()); err == nil {
		t.Fatal("coded fetch succeeded against a failing peer")
	}
	if peer.probes.Load() != 2 {
		t.Fatalf("declared peer's transient failure was remembered: %d sealed requests, want 2", peer.probes.Load())
	}
}

// A peer may serve one coded capability and not the other: a node on the
// previous release started WITH --object-codec answers get_object_manifest_v1
// and has never heard of get_block_batch_v1. Keyed together, one batch refusal
// suppressed that peer's WORKING snapshot path for the whole TTL — the
// fallback cost paid on a capability the peer actually had.
func TestCodedRefusalsAreKeptPerCapability(t *testing.T) {
	ic := NewIdentityCache()
	const peer = "http://198.51.100.9:8080"

	ic.NoteCodedRefusal(peer, CodedBatches)

	if !ic.CodedRecentlyRefused(peer, CodedBatches) {
		t.Fatal("a batch refusal was not remembered")
	}
	if ic.CodedRecentlyRefused(peer, CodedSnapshots) {
		t.Fatal("a refusal of the block-batch service suppressed the snapshot service, " +
			"which this peer serves")
	}

	// And clearing one does not clear the other.
	ic.NoteCodedRefusal(peer, CodedSnapshots)
	ic.ForgetCodedRefusal(peer, CodedSnapshots)
	if ic.CodedRecentlyRefused(peer, CodedSnapshots) {
		t.Fatal("forgetting the snapshot refusal did not clear it")
	}
	if !ic.CodedRecentlyRefused(peer, CodedBatches) {
		t.Fatal("forgetting the snapshot refusal also cleared the batch one")
	}
}
