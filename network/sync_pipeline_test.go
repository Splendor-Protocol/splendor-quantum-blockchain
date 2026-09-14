package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// chainPeer serves a chain of `height` sequentially-linked, validator-signed
// blocks (1..height) over plaintext GET /block/{h}, and counts how many
// distinct heights were actually requested — so a test can tell whether
// fetches really overlapped instead of happening one at a time.
type chainPeer struct {
	srv       *httptest.Server
	requested sync.Map // height -> count
	pub       *qcrypto.PublicKey
}

func newChainPeer(t *testing.T, height uint64) *chainPeer {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	blocks := make(map[uint64]*types.Block, height)
	var prev [32]byte
	for h := uint64(1); h <= height; h++ {
		b := &types.Block{Header: types.BlockHeader{
			Height: h, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: qcrypto.Hash32([]byte{}),
			ValidatorAddr: qcrypto.DeriveAddress(pub), PrevHash: prev,
		}}
		hash, err := b.ComputeHash()
		if err != nil {
			t.Fatal(err)
		}
		b.Hash = hash
		if err := b.SignBlock(priv); err != nil {
			t.Fatal(err)
		}
		blocks[h] = b
		prev = hash
	}

	cp := &chainPeer{pub: pub}
	cp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/p2p/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var h uint64
		if _, err := fmt.Sscanf(r.URL.Path, "/block/%d", &h); err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, ok := blocks[h]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		v, _ := cp.requested.LoadOrStore(h, new(int32))
		atomic.AddInt32(v.(*int32), 1)
		body, _ := json.Marshal(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(cp.srv.Close)
	return cp
}

// TestSyncOnceAppliesBlocksInOrderWithParallelFetch proves the pipeline still
// applies every block in strict ascending height order even when many
// fetches are allowed to race, which is the correctness property that
// matters — ExecuteBlock at h depends on the state h-1 left behind.
func TestSyncOnceAppliesBlocksInOrderWithParallelFetch(t *testing.T) {
	const target = uint64(40)
	peer := newChainPeer(t, target)

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 8
	cfg.MaxBlocksPerBatch = 1000

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var applied []uint64
	var mu sync.Mutex
	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { mu.Lock(); defer mu.Unlock(); return localH },
		func(b *types.Block) error {
			mu.Lock()
			defer mu.Unlock()
			if b.Header.Height != localH+1 {
				return fmt.Errorf("applied height %d out of order after %d", b.Header.Height, localH)
			}
			applied = append(applied, b.Header.Height)
			localH = b.Header.Height
			return nil
		},
	)

	n, err := s.SyncOnce()
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if n != int(target) {
		t.Fatalf("applied %d blocks, want %d", n, target)
	}
	for i, h := range applied {
		if h != uint64(i+1) {
			t.Fatalf("applied[%d] = %d, want %d — blocks applied out of order", i, h, i+1)
		}
	}
}

// TestSyncOnceParallelFetchOverlapsRequests confirms fetching actually
// overlaps: with MaxParallelFetch > 1 the peer must see more than one height
// requested before any block is applied. A slow handler on the peer holds
// each response just long enough for concurrent requests to pile up.
func TestSyncOnceParallelFetchOverlapsRequests(t *testing.T) {
	const target = uint64(20)
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
			time.Sleep(20 * time.Millisecond)
		}
		orig.ServeHTTP(w, r)
	})

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 8
	cfg.MaxBlocksPerBatch = 1000

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var mu sync.Mutex
	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { mu.Lock(); defer mu.Unlock(); return localH },
		func(b *types.Block) error {
			mu.Lock()
			defer mu.Unlock()
			localH = b.Header.Height
			return nil
		},
	)

	if _, err := s.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
		t.Fatalf("max concurrent in-flight fetches = %d, want >1 (fetches did not overlap)", got)
	}
}

// TestSyncOnceResumesAfterFetchFailure simulates a sync interrupted partway
// through — a fetch that permanently fails after a handful of blocks already
// applied — and confirms a second SyncOnce call resumes exactly where the
// first left off, applying the rest with no manual bookkeeping. This is the
// "resume capability" the improvement plan asks for: SyncOnce always starts
// from localHeight()+1, and localHeight only advances on a successful apply,
// so the interrupted call's partial progress is never lost or replayed.
func TestSyncOnceResumesAfterFetchFailure(t *testing.T) {
	const target = uint64(15)
	const failAt = uint64(7) // this height 404s until unblocked
	peer := newChainPeer(t, target)

	var blocked atomic.Bool
	blocked.Store(true)
	origHandler := peer.srv.Config.Handler
	peer.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blocked.Load() && r.URL.Path == fmt.Sprintf("/block/%d", failAt) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 4
	cfg.MaxBlocksPerBatch = 1000
	cfg.RetryDelay = 1 * time.Millisecond // keep the test fast

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var mu sync.Mutex
	var localH uint64
	applyCount := 0
	s := NewSyncer(cfg, pool,
		func() uint64 { mu.Lock(); defer mu.Unlock(); return localH },
		func(b *types.Block) error {
			mu.Lock()
			defer mu.Unlock()
			if b.Header.Height != localH+1 {
				return fmt.Errorf("out-of-order apply: got %d, expected %d", b.Header.Height, localH+1)
			}
			localH = b.Header.Height
			applyCount++
			return nil
		},
	)

	// First pass: must fail once it reaches the blocked height, but everything
	// strictly below that height must already be durably applied.
	if _, err := s.SyncOnce(); err == nil {
		t.Fatal("expected SyncOnce to fail while height 7 is blocked")
	}
	mu.Lock()
	firstPassHeight := localH
	mu.Unlock()
	if firstPassHeight == 0 {
		t.Fatal("first pass applied nothing before hitting the blocked height")
	}
	if firstPassHeight >= failAt {
		t.Fatalf("local height advanced to %d, past the blocked height %d", firstPassHeight, failAt)
	}

	// Unblock the peer and resume: SyncOnce must pick up from localHeight()+1,
	// not restart from 1 and not skip anything.
	blocked.Store(false)
	n, err := s.SyncOnce()
	if err != nil {
		t.Fatalf("resumed SyncOnce: %v", err)
	}
	mu.Lock()
	finalHeight := localH
	finalApplyCount := applyCount
	mu.Unlock()
	if finalHeight != target {
		t.Fatalf("final height = %d, want %d", finalHeight, target)
	}
	if uint64(n) != target-firstPassHeight {
		t.Fatalf("resumed pass applied %d blocks, want %d (target - already-applied)", n, target-firstPassHeight)
	}
	if uint64(finalApplyCount) != target {
		t.Fatalf("total applies across both passes = %d, want exactly %d (no re-apply, no gap)", finalApplyCount, target)
	}
}
