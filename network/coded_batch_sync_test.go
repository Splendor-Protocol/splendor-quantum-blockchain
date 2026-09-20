package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// sealedBatchPeer is a minimal sealed-channel peer: it serves a signed
// identity document and answers get_block_batch_v1 / get_object_symbol_v1 with
// a coded batch of a genesis-sealed chain. It serves NOTHING else — so a
// SyncOnce that succeeds against it must have taken the coded batch path.
type sealedBatchPeer struct {
	srv      *httptest.Server
	identity *NodeIdentity
	blocks   map[uint64]*types.Block
	cache    *objects.Cache
	mu       sync.Mutex
}

func newSealedBatchPeer(t *testing.T, height uint64, genesisPub *qcrypto.PublicKey, genesisPriv *qcrypto.PrivateKey) *sealedBatchPeer {
	t.Helper()
	nodePub, nodePriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewNodeIdentity(nodePub, nodePriv)
	if err != nil {
		t.Fatal(err)
	}
	identity.SetCapabilities(IdentityCaps{CodedObjects: true, CodedBlocks: true})

	blocks := make(map[uint64]*types.Block, height)
	var prev [32]byte
	for h := uint64(1); h <= height; h++ {
		b := &types.Block{Header: types.BlockHeader{
			Height: h, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: qcrypto.Hash32([]byte{}),
			ValidatorAddr: qcrypto.DeriveAddress(genesisPub), PrevHash: prev,
		}}
		hash, err := b.ComputeHash()
		if err != nil {
			t.Fatal(err)
		}
		b.Hash = hash
		if err := b.SignBlock(genesisPriv); err != nil {
			t.Fatal(err)
		}
		blocks[h] = b
		prev = hash
	}

	peer := &sealedBatchPeer{identity: identity, blocks: blocks, cache: objects.NewCache(8)}
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
			peer.serveSealed(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(peer.srv.Close)
	return peer
}

func (p *sealedBatchPeer) serveSealed(w http.ResponseWriter, r *http.Request) {
	var env Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	plain, err := p.identity.Open(&env)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req SealedRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload []byte
	switch req.Kind {
	case KindGetBlockBatch:
		var q GetBlockBatchRequest
		if err := json.Unmarshal(req.Body, &q); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payload, err = p.buildBatch(q.FromHeight, q.Count)
	case KindGetObjectSymbol:
		var q GetObjectSymbolRequest
		if err := json.Unmarshal(req.Body, &q); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payload, err = p.serveSymbol(q.DescriptorID, q.Generation, q.Index)
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	reply, err := SealToKEM(req.ReplyKEM, payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(reply)
}

func (p *sealedBatchPeer) buildBatch(from uint64, count int) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	blocks := make([]*types.Block, 0, count)
	for h := from; h < from+uint64(count); h++ {
		b, ok := p.blocks[h]
		if !ok {
			return nil, fmt.Errorf("block %d unavailable", h)
		}
		blocks = append(blocks, b)
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	last := blocks[len(blocks)-1]
	_, priv := p.identity.SigningKeys()
	send, err := objects.NewObjectSender(raw, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: last.Hash, Height: last.Header.Height},
		Codec:      "uncoded-v1",
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, objects.DefaultLimits())
	if err != nil {
		return nil, err
	}
	p.cache.Put(send)
	return objects.EncodeObjectManifest(send.Manifest())
}

func (p *sealedBatchPeer) serveSymbol(descID []byte, generation, index uint32) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	send, err := p.cache.Get(descID)
	if err != nil {
		return nil, err
	}
	proof, err := send.Proof(int(generation), int(index))
	if err != nil {
		return nil, err
	}
	return objects.EncodeSymbolProof(proof)
}

// SyncOnce with a gap above the coded-batch threshold must fill the whole gap
// from the coded batch when the peer serves one: the peer answers ONLY the
// coded kinds (and its identity), so success here proves the pre-fill path
// applied every block in order without touching per-block fetching.
func TestSyncOncePrefillsCodedBlockBatch(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	const target = uint64(24) // ≥ MinCodedBatchBlocks (16), ≤ CodedBatchSize (32)
	peer := newSealedBatchPeer(t, target, genesisPub, genesisPriv)

	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = NewIdentityCache()
	cfg.MaxParallelFetch = 1
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
		})

	n, err := s.SyncOnce()
	if err != nil {
		t.Fatalf("coded batch sync failed: %v", err)
	}
	if n != int(target) {
		t.Fatalf("applied %d blocks, want %d", n, target)
	}
	if len(applied) != int(target) {
		t.Fatalf("applied %d blocks in order, want %d", len(applied), target)
	}
	for i, h := range applied {
		if h != uint64(i+1) {
			t.Fatalf("block %d at height %d, want %d", i, h, i+1)
		}
	}
}

// Degenerate batch ranges are refused before any network work: an out-of-cap
// count and a fromHeight whose range end would wrap must never reach the wire
// or the anchor arithmetic.
func TestFetchBlockBatchRejectsDegenerateRanges(t *testing.T) {
	f := NewBlockFetcher(DefaultSyncConfig(nil)) // no Identities needed: guards fire first
	if _, err := f.FetchBlockBatch("http://peer", 10, MaxCodedBlockBatchBlocks+1); err == nil {
		t.Fatal("over-cap count accepted")
	}
	if _, err := f.FetchBlockBatch("http://peer", 10, 0); err == nil {
		t.Fatal("zero count accepted")
	}
	if _, err := f.FetchBlockBatch("http://peer", ^uint64(0), 2); err == nil {
		t.Fatal("wrapping range accepted")
	}
	// The guard is strict about the serve-side range [fromHeight,
	// fromHeight+count): a range whose END wraps is refused even though the
	// anchored block height itself would not wrap.
	cfg := DefaultSyncConfig(nil)
	cfg.Identities = NewIdentityCache()
	f2 := NewBlockFetcher(cfg)
	if _, err := f2.FetchBlockBatch("http://127.0.0.1:1", ^uint64(0)-1, 2); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("wrapping range end was not refused: %v", err)
	}
}

// parseBlockBatch is the batch analogue of the per-block checks in
// FetchBlockContext: exact count, exact heights, per-block wire cap,
// structural validity and producer authentication. A batch that fails any of
// these makes the caller fall back to per-block fetching.
func TestParseBlockBatchRefusals(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(height uint64, prev [32]byte) *types.Block {
		b := &types.Block{Header: types.BlockHeader{
			Height: height, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: qcrypto.Hash32([]byte{}),
			ValidatorAddr: qcrypto.DeriveAddress(pub), PrevHash: prev,
		}}
		h, err := b.ComputeHash()
		if err != nil {
			t.Fatal(err)
		}
		b.Hash = h
		if err := b.SignBlock(priv); err != nil {
			t.Fatal(err)
		}
		return b
	}
	var prev [32]byte
	b1 := mk(21, prev)
	b2 := mk(22, b1.Hash)
	b3 := mk(23, b2.Hash)
	raw, err := json.Marshal([]*types.Block{b1, b2, b3})
	if err != nil {
		t.Fatal(err)
	}
	ok := func(*types.Block) error { return nil }

	got, err := parseBlockBatch("peer:1", 21, 3, raw, block.MaxBlockWireBytes, ok)
	if err != nil || len(got) != 3 {
		t.Fatalf("valid batch = %d blocks, %v; want 3, nil", len(got), err)
	}
	if _, err := parseBlockBatch("peer:1", 21, 2, raw, block.MaxBlockWireBytes, ok); err == nil {
		t.Fatal("count mismatch accepted")
	}
	if _, err := parseBlockBatch("peer:1", 21, 3, raw, block.MaxBlockWireBytes, func(b *types.Block) error {
		return fmt.Errorf("producer refused")
	}); err == nil || !strings.Contains(err.Error(), "producer refused") {
		t.Fatalf("producer refusal lost: %v", err)
	}

	// A block at the wrong height in the first slot must be refused even
	// though it is validly signed.
	shifted, err := json.Marshal([]*types.Block{b2, b3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseBlockBatch("peer:1", 21, 2, shifted, block.MaxBlockWireBytes, ok); err == nil {
		t.Fatal("wrong-height batch accepted")
	}

	// Null elements are refused.
	if _, err := parseBlockBatch("peer:1", 21, 3, []byte(`[null,null,null]`), block.MaxBlockWireBytes, ok); err == nil {
		t.Fatal("null-element batch accepted")
	}

	// Every block inside the batch must respect the wire cap. The signed
	// blocks above are far larger than 128 bytes on the wire, so a small cap
	// must refuse them before any parsing.
	if _, err := parseBlockBatch("peer:1", 21, 3, raw, 128, ok); err == nil || !strings.Contains(err.Error(), "wire cap") {
		t.Fatalf("wire-cap refusal missing: %v", err)
	}
}
