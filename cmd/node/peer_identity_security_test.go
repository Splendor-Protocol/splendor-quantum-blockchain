package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

func TestRejectedGenesisPeerStopsReceivingSealedWrites(t *testing.T) {
	var ours, theirs [32]byte
	ours[0], theirs[0] = 0xaa, 0xbb
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	id, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan network.SealedRequest, 4)
	var foreign atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		g := ours
		if foreign.Load() {
			g = theirs
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"height": 99, "genesis_hash": hex.EncodeToString(g[:])})
	})
	mux.HandleFunc(network.PathIdentity, func(w http.ResponseWriter, _ *http.Request) {
		doc, err := id.Document()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc(network.PathSealed, func(w http.ResponseWriter, r *http.Request) {
		var env network.Envelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		raw, err := id.Open(&env)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var req network.SealedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		received <- req
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	pool := network.NewPeerPool("http://self.invalid", 8)
	pool.Add(&network.PeerInfo{Addr: srv.URL})
	broadcaster := network.NewBroadcaster(nil, "review-only-dummy-token", false)
	broadcaster.Identities().RequirePins(true)
	if err := broadcaster.Identities().Pin(srv.URL, pub.Bytes()); err != nil {
		t.Fatal(err)
	}
	known := func() ([32]byte, bool) { return ours, true }
	peer := &network.PeerInfo{Addr: srv.URL}
	if err := admitAnnouncedPeer(pool, broadcaster, known, peer); err != nil {
		t.Fatal(err)
	}
	broadcaster.Broadcast(&types.Block{})
	select {
	case req := <-received:
		if req.Kind != network.KindBlock || req.Token != "review-only-dummy-token" {
			t.Fatal("healthy encrypted push did not authenticate")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("healthy peer never received the control broadcast")
	}
	foreign.Store(true)
	refreshPeerHeights(pool, broadcaster, known)
	if pool.Count() != 0 || broadcaster.PeerCount() != 0 {
		t.Fatal("foreign peer remains a sync or broadcast target")
	}
	// Re-announcing or being returned by peer exchange must not restore writes.
	if err := admitAnnouncedPeer(pool, broadcaster, known, peer); !errors.Is(err, network.ErrPeerOnAnotherChain) {
		t.Fatalf("foreign announcement: %v", err)
	}
	pool.Add(peer)
	refreshPeerHeights(pool, broadcaster, known)
	if pool.Count() != 0 || broadcaster.PeerCount() != 0 {
		t.Fatal("foreign peer was readmitted")
	}
	broadcaster.Broadcast(&types.Block{})
	broadcaster.BroadcastSealed(network.KindVote, []byte(`{}`))
	select {
	case <-received:
		t.Fatal("rejected peer still decrypted authenticated writes")
	case <-time.After(100 * time.Millisecond):
	}
	// A peer that rejoins the correct chain can recover without a process reset.
	foreign.Store(false)
	if err := admitAnnouncedPeer(pool, broadcaster, known, peer); err != nil {
		t.Fatal(err)
	}
	if pool.Count() != 1 || broadcaster.PeerCount() != 1 {
		t.Fatal("recovered peer was not admitted")
	}
}
