package network

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// FetchBlockByHash must return the block a peer serves whose hash matches the
// requested one, and fail closed when no peer serves it.
func TestSyncerFetchBlockByHash(t *testing.T) {
	peer := newChainPeer(t, 3)
	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	s := NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })

	// Learn block 2's real hash.
	b2, err := s.fetcher.FetchBlock(peer.srv.URL, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.FetchBlockByHash(2, b2.Hash)
	if err != nil {
		t.Fatalf("FetchBlockByHash: %v", err)
	}
	if got.Hash != b2.Hash {
		t.Fatalf("FetchBlockByHash returned %x, want %x", got.Hash[:8], b2.Hash[:8])
	}

	// A hash no peer serves must fail closed.
	if _, err := s.FetchBlockByHash(2, [32]byte{0xFF}); err == nil {
		t.Fatal("FetchBlockByHash must fail when no peer serves the requested hash")
	}
}
