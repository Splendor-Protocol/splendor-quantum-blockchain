package network

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestFetchBlockDefersUnknownProducer(t *testing.T) {
	peer := newChainPeer(t, 2)
	other, _, _ := qcrypto.GenerateKeypair()
	cfg := DefaultSyncConfig(other.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return nil }
	f := NewBlockFetcher(cfg)
	b, err := f.FetchBlock(peer.srv.URL, 1)
	if err != nil {
		t.Fatalf("unknown producer must be deferred to apply: %v", err)
	}
	if b == nil || b.Header.Height != 1 {
		t.Fatal("fetch dropped the block")
	}
}

func TestVerifyBlockProducerUnknownIsDistinct(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	b := sealedBy(t, 4, otherPub, otherPriv)
	err := VerifyBlockProducer(b, genesisPub.Bytes(), func(qcrypto.Address) []byte { return nil })
	if !errors.Is(err, ErrProducerNotYetKnown) {
		t.Fatalf("unknown rotating producer = %v, want ErrProducerNotYetKnown", err)
	}
	if err := VerifyBlockProducer(b, genesisPub.Bytes(), onlyKeyOf(otherPub)); err != nil {
		t.Fatalf("known rotating producer refused: %v", err)
	}
	ok := sealedBy(t, 4, genesisPub, genesisPriv)
	if err := VerifyBlockProducer(ok, genesisPub.Bytes(), nil); err != nil {
		t.Fatalf("genesis producer refused: %v", err)
	}
}

func TestSyncOnceContextCancelStopsRetry(t *testing.T) {
	peer := newChainPeer(t, 3)
	orig := peer.srv.Config.Handler
	peer.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	_ = orig

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 2
	cfg.RetryDelay = 30 * time.Second

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, 3)

	s := NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.SyncOnceContext(ctx)
	if err == nil {
		t.Fatal("expected cancel to surface")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancel did not interrupt retry sleep: %s", time.Since(start))
	}
}
