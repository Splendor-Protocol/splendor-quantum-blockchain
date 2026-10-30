package main

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// A peer on another genesis is dropped from the pool by the same pass that
// records peer heights, so the syncer never picks it; a peer on this chain
// keeps its place and its height is recorded as before.
func TestRefreshPeerHeightsDropsPeersOnAnotherGenesis(t *testing.T) {
	var ours, theirs [32]byte
	ours[0], theirs[0] = 0xaa, 0xbb
	peer := func(genesis [32]byte, height uint64) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"height":%d,"genesis_hash":%q}`, height, hex.EncodeToString(genesis[:]))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	same, other := peer(ours, 40), peer(theirs, 900)

	pool := network.NewPeerPool("http://self.invalid", 8)
	pool.Add(&network.PeerInfo{Addr: same})
	pool.Add(&network.PeerInfo{Addr: other})

	refreshPeerHeights(pool, func() ([32]byte, bool) { return ours, true })

	addrs := pool.Addrs()
	if len(addrs) != 1 || addrs[0] != same {
		t.Fatalf("pool after refresh = %v, want only %s", addrs, same)
	}
	if best := pool.BestPeerHeight(); best != 40 {
		t.Fatalf("best peer height = %d, want 40 (the other chain's height must not count)", best)
	}
}
