package rpc

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// A coded block batch travels the same sealed object path as a snapshot: the
// peer builds the batch object per range, serves its manifest from
// get_block_batch_v1 and its blocks as symbol proofs, and the client decodes
// and validates every block (heights, structure, producer seal) before
// returning them.
func TestSealedCodedBlockBatchRoundTrip(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	const batch = 8
	chain := make([]*types.Block, 0, batch)
	for h := uint64(1); h <= batch; h++ {
		chain = append(chain, signedHead(t, h, genesisPub, genesisPriv, [32]byte{byte(h)}))
	}

	nodePub, nodePriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := network.NewNodeIdentity(nodePub, nodePriv)
	if err != nil {
		t.Fatal(err)
	}
	identity.SetCapabilities(network.IdentityCaps{CodedObjects: true, CodedBlocks: true})
	batchCache := objects.NewCache(8)

	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		ServeBlockBatch: func(from uint64, count int) ([]byte, error) {
			start := int(from - 1)
			raw, err := json.Marshal(chain[start : start+count])
			if err != nil {
				return nil, err
			}
			last := chain[start+count-1]
			desc := objects.ObjectDescriptor{
				Anchor: objects.Anchor{
					Kind:   "block",
					ID:     last.Hash,
					Height: last.Header.Height,
				},
				Codec:      "rlnc-gf256-v1",
				SymbolSize: 64,
			}
			send, err := objects.NewObjectSender(raw, desc, coding.NewRLNCCodec(), 2, nodePriv, objects.DefaultLimits())
			if err != nil {
				return nil, err
			}
			batchCache.Put(send)
			return objects.EncodeObjectManifest(send.Manifest())
		},
		ServeObjectSymbol: func(id []byte, generation uint32, index uint32) ([]byte, error) {
			send, err := batchCache.Get(id)
			if err != nil {
				return nil, err
			}
			proof, err := send.Proof(int(generation), int(index))
			if err != nil {
				return nil, err
			}
			return objects.EncodeSymbolProof(proof)
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	cfg := network.DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = network.NewIdentityCache()
	got, err := network.NewBlockFetcher(cfg).FetchBlockBatch(srv.URL, 1, batch)
	if err != nil {
		t.Fatalf("coded block batch fetch failed: %v", err)
	}
	if len(got) != batch {
		t.Fatalf("got %d blocks, want %d", len(got), batch)
	}
	for i, b := range got {
		if b.Header.Height != uint64(1+i) {
			t.Fatalf("block %d at height %d, want %d", i, b.Header.Height, 1+i)
		}
		if b.Hash != chain[i].Hash {
			t.Fatalf("block %d hash mismatch", i)
		}
	}
}

// A manifest that names a different range than the one requested is refused
// before any symbol work: the peer's signed anchor must match the request.
func TestFetchBlockBatchRefusesWrongRange(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	chain := make([]*types.Block, 0, 2)
	for h := uint64(1); h <= 2; h++ {
		chain = append(chain, signedHead(t, h, genesisPub, genesisPriv, [32]byte{byte(h)}))
	}

	nodePub, nodePriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := network.NewNodeIdentity(nodePub, nodePriv)
	if err != nil {
		t.Fatal(err)
	}
	identity.SetCapabilities(network.IdentityCaps{CodedBlocks: true})
	batchCache := objects.NewCache(8)

	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		ServeBlockBatch: func(from uint64, count int) ([]byte, error) {
			raw, err := json.Marshal(chain[:count])
			if err != nil {
				return nil, err
			}
			last := chain[count-1]
			// Lie about the range: anchor the NEXT height.
			desc := objects.ObjectDescriptor{
				Anchor: objects.Anchor{
					Kind:   "block",
					ID:     last.Hash,
					Height: last.Header.Height + 1,
				},
				Codec:      "rlnc-gf256-v1",
				SymbolSize: 64,
			}
			send, err := objects.NewObjectSender(raw, desc, coding.NewRLNCCodec(), 1, nodePriv, objects.DefaultLimits())
			if err != nil {
				return nil, err
			}
			batchCache.Put(send)
			return objects.EncodeObjectManifest(send.Manifest())
		},
		ServeObjectSymbol: func(id []byte, generation uint32, index uint32) ([]byte, error) {
			send, err := batchCache.Get(id)
			if err != nil {
				return nil, err
			}
			proof, err := send.Proof(int(generation), int(index))
			if err != nil {
				return nil, err
			}
			return objects.EncodeSymbolProof(proof)
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	cfg := network.DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = network.NewIdentityCache()
	if _, err := network.NewBlockFetcher(cfg).FetchBlockBatch(srv.URL, 1, 2); err == nil {
		t.Fatal("a batch manifest anchoring the wrong range was accepted")
	}
}
