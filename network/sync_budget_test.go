package network

import (
	"encoding/json"
	"fmt"
	"math/big"
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

// paddedChainPeer serves a chain of signed blocks whose bodies are padded to
// roughly padBytes by a transaction carrying that much data, and stalls one
// chosen height so the blocks behind it pile up in the applier's out-of-order
// buffer. That is the shape R7 is about: the earliest block is late while
// later, large blocks keep arriving.
type paddedChainPeer struct {
	srv       *httptest.Server
	pub       *qcrypto.PublicKey
	maxWire   int64
	requests  sync.Map // height -> *int32
	totalReqs int32
}

func (cp *paddedChainPeer) requestsFor(h uint64) int32 {
	v, ok := cp.requests.Load(h)
	if !ok {
		return 0
	}
	return atomic.LoadInt32(v.(*int32))
}

func newPaddedChainPeer(t *testing.T, height uint64, padBytes int, stallHeight uint64, stall time.Duration) *paddedChainPeer {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	bodies := make(map[uint64][]byte, height)
	cp := &paddedChainPeer{pub: pub}
	var prev [32]byte
	for h := uint64(1); h <= height; h++ {
		data := make([]byte, padBytes)
		for i := range data {
			data[i] = byte(h)
		}
		tx := &types.Transaction{
			Version: 1, ChainID: types.ChainID, Type: types.TxTransfer,
			AlgID: qcrypto.AlgMLDSA65, Amount: big.NewInt(0), Fee: big.NewInt(0),
			Nonce: h, Timestamp: time.Now().Unix(), Data: data,
		}
		root, err := types.ComputeTxMerkleRoot([]*types.Transaction{tx})
		if err != nil {
			t.Fatal(err)
		}
		b := &types.Block{
			Header: types.BlockHeader{
				Height: h, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
				AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: root, TxCount: 1,
				ValidatorAddr: qcrypto.DeriveAddress(pub), PrevHash: prev,
			},
			Transactions: []*types.Transaction{tx},
		}
		hash, err := b.ComputeHash()
		if err != nil {
			t.Fatal(err)
		}
		b.Hash = hash
		if err := b.SignBlock(priv); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(body)) > cp.maxWire {
			cp.maxWire = int64(len(body))
		}
		bodies[h] = body
		prev = hash
	}

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
		body, ok := bodies[h]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		v, _ := cp.requests.LoadOrStore(h, new(int32))
		atomic.AddInt32(v.(*int32), 1)
		atomic.AddInt32(&cp.totalReqs, 1)
		if h == stallHeight && stall > 0 {
			time.Sleep(stall)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(cp.srv.Close)
	return cp
}

// TestHeldOutOfOrderBlocksStayInsideTheByteBudget is R7. Each fetch used to
// reserve a flat estimate and have its real size charged only once the result
// reached the ordered applier, so blocks buffered ahead of a late earliest
// block held full decoded bodies while accounted at the estimate. The budget
// has to cover what is held, not what was guessed.
func TestHeldOutOfOrderBlocksStayInsideTheByteBudget(t *testing.T) {
	const target = uint64(8)
	peer := newPaddedChainPeer(t, target, 3<<10, 1, 400*time.Millisecond)

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 8
	cfg.MaxFetchLookahead = 8
	cfg.MaxBlocksPerBatch = 1000
	cfg.MaxFetchBufferBytes = 8 << 10
	cfg.FetchReserveBytes = 1 << 10

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { return localH },
		func(b *types.Block) error { localH = b.Header.Height; return nil },
	)
	applied, err := s.SyncOnce()
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if applied != int(target) {
		t.Fatalf("applied %d blocks, want %d", applied, target)
	}

	// The documented exception is ONE block: the height the applier is
	// waiting for is admitted even when the budget is full, because nothing
	// can drain the budget until it is applied.
	allowed := cfg.MaxFetchBufferBytes + peer.maxWire
	if got := s.PeakFetchResidentBytes(); got > allowed {
		t.Fatalf("pipeline held %d bytes of fetched blocks at once; the budget is %d and the documented "+
			"single-block exception allows %d", got, cfg.MaxFetchBufferBytes, allowed)
	}
}

// TestOversizedBlockSyncsUnderTheDocumentedException pins the other half of
// the bound: a block bigger than the whole budget must still sync. Only the
// height the applier is waiting for may be admitted over the allowance, so
// the pipeline serialises around it instead of deadlocking on a reservation
// that can never be met, and the overrun stays one block.
func TestOversizedBlockSyncsUnderTheDocumentedException(t *testing.T) {
	const target = uint64(4)
	peer := newPaddedChainPeer(t, target, 3<<10, 0, 0)

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 4
	cfg.MaxFetchLookahead = 4
	cfg.MaxBlocksPerBatch = 1000
	cfg.MaxFetchBufferBytes = 4 << 10 // smaller than a single block
	cfg.FetchReserveBytes = 1 << 10

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { return localH },
		func(b *types.Block) error { localH = b.Header.Height; return nil },
	)
	done := make(chan struct{})
	var applied int
	var err error
	go func() {
		defer close(done)
		applied, err = s.SyncOnce()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("pipeline deadlocked on a block larger than the whole byte budget")
	}
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if applied != int(target) {
		t.Fatalf("applied %d blocks, want %d", applied, target)
	}
	if peer.maxWire <= cfg.MaxFetchBufferBytes {
		t.Fatalf("test needs blocks larger than the budget: %d <= %d", peer.maxWire, cfg.MaxFetchBufferBytes)
	}
	allowed := cfg.MaxFetchBufferBytes + peer.maxWire
	if got := s.PeakFetchResidentBytes(); got > allowed {
		t.Fatalf("held %d bytes; the exception allows one oversized block over the %d-byte budget (%d)",
			got, cfg.MaxFetchBufferBytes, allowed)
	}
}

// TestFetchReservationConvergesOnRealBlockSizes guards the cost of enforcing
// the budget: the working reservation rises to the sizes actually seen, so a
// peer serving ordinary blocks is asked once per height after the opening
// estimate has caught up. Without that convergence every fetch would pay a
// refused read and a retry.
func TestFetchReservationConvergesOnRealBlockSizes(t *testing.T) {
	const target = uint64(8)
	peer := newPaddedChainPeer(t, target, 3<<10, 0, 0)

	cfg := DefaultSyncConfig(peer.pub.Bytes())
	cfg.AllowPlaintext = true
	cfg.MaxParallelFetch = 4
	cfg.MaxFetchLookahead = 4
	cfg.MaxBlocksPerBatch = 1000
	cfg.MaxFetchBufferBytes = 64 << 10
	cfg.FetchReserveBytes = 1 << 10 // deliberately far below a real block

	pool := NewPeerPool("http://self", 10)
	pool.Add(&PeerInfo{Addr: peer.srv.URL})
	pool.UpdatePeerHeight(peer.srv.URL, target)

	var localH uint64
	s := NewSyncer(cfg, pool,
		func() uint64 { return localH },
		func(b *types.Block) error { localH = b.Header.Height; return nil },
	)
	if _, err := s.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	// The first wave of concurrent fetches all start from the same too-small
	// opening estimate and widen it; every height issued after that wave must
	// cost exactly one request, or the reservation is not converging.
	for h := uint64(cfg.MaxParallelFetch) + 1; h <= target; h++ {
		if got := peer.requestsFor(h); got != 1 {
			t.Fatalf("height %d was fetched %d times, want 1: the reservation is not converging on real sizes", h, got)
		}
	}
	if got := atomic.LoadInt32(&peer.totalReqs); got > int32(target)+int32(cfg.MaxParallelFetch)*2 {
		t.Fatalf("%d requests for %d blocks is more than one widening wave", got, target)
	}
}
