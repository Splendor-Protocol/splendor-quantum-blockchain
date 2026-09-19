package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
