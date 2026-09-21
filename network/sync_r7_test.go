package network

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestSyncOnceContextCancelStopsInFlightFetch(t *testing.T) {
	peer := newChainPeer(t, 3)
	orig := peer.srv.Config.Handler
	peer.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	_ = orig

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 1
	cfg.RequestTimeout = 15 * time.Second
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
		t.Fatalf("cancel did not interrupt in-flight fetch: %s", time.Since(start))
	}
}

func TestPipelineRefusesUnknownProducerAtApply(t *testing.T) {
	peer := newChainPeer(t, 2)
	other, _, _ := qcrypto.GenerateKeypair()
	cfg := DefaultSyncConfig(other.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return nil }
	cfg.MaxParallelFetch = 1
	cfg.RetryDelay = time.Millisecond

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, 2)

	_, err := NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil }).SyncOnce()
	if !errors.Is(err, ErrProducerNotYetKnown) {
		t.Fatalf("apply of deferred unknown producer = %v, want ErrProducerNotYetKnown", err)
	}

	cfg.LookupProducerKey = onlyKeyOf(peer.pub)
	n, err := NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil }).SyncOnce()
	if err != nil || n != 2 {
		t.Fatalf("known producer apply = %d, %v", n, err)
	}
}

func TestPeerForSelectsByRequestedHeight(t *testing.T) {
	tall := newChainPeer(t, 5)
	var shortHits atomic.Int32
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/block/") {
			shortHits.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(short.Close)

	cfg := DefaultSyncConfig(tall.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 1
	cfg.RetryDelay = time.Millisecond

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: short.URL})
	pool.Add(&PeerInfo{Addr: tall.srv.URL})
	pool.UpdatePeerHeight(short.URL, 1)
	pool.UpdatePeerHeight(tall.srv.URL, 5)

	n, err := NewSyncer(cfg, pool, func() uint64 { return 1 }, func(*types.Block) error { return nil }).SyncOnce()
	if err != nil || n != 4 {
		t.Fatalf("sync = %d, %v", n, err)
	}
	if shortHits.Load() != 0 {
		t.Fatalf("short peer was asked for %d blocks above its height", shortHits.Load())
	}
}

func TestByteWindowBoundsInFlightFetches(t *testing.T) {
	const target = uint64(8)
	peer := newChainPeer(t, target)
	var inFlight, maxInFlight int32
	orig := peer.srv.Config.Handler
	peer.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/block/") {
			n := atomic.AddInt32(&inFlight, 1)
			defer atomic.AddInt32(&inFlight, -1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if n <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, n) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		orig.ServeHTTP(w, r)
	})

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 8
	cfg.MaxFetchLookahead = 8
	cfg.MaxFetchBufferBytes = 2000
	cfg.MaxBlocksPerBatch = 1000

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { return localH },
		func(b *types.Block) error {
			time.Sleep(20 * time.Millisecond)
			localH = b.Header.Height
			return nil
		},
	)
	if _, err := s.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	// The window is 2000 bytes and a block here is about a kilobyte, so two
	// fit. The budget admits ONE more: the height the applier is waiting for
	// is let through over the allowance, because making it wait is a
	// deadlock rather than backpressure (network/sync.go, the documented
	// exception; pinned exactly by TestOversizedBlockSyncsUnderTheDocumented
	// Exception). So the bound this test protects is window/body + 1, not a
	// bare 2 -- and the number that would mean the window is not working is
	// MaxParallelFetch, which is 8.
	const fit = 2
	if got := atomic.LoadInt32(&maxInFlight); got > fit+1 {
		t.Fatalf("max in-flight fetches = %d, want <= %d (window/body + the one awaited block) under a 2000-byte window",
			got, fit+1)
	}
}
