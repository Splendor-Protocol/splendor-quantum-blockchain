package rpc

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// sealedSyncPeer starts a node exposing only the sealed P2P transport, so a
// test can prove catch-up works without any cleartext block route.
func sealedSyncPeer(t *testing.T, served *types.Block) (*httptest.Server, *network.IdentityCache) {
	t.Helper()

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("node identity: %v", err)
	}

	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		ServeBlock: func(height uint64) ([]byte, error) {
			if height != served.Header.Height {
				return nil, errors.New("no such block")
			}
			return json.Marshal(served)
		},
		ServeLatestBlock: func() ([]byte, error) { return json.Marshal(served) },
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, network.NewIdentityCache()
}

// testBlock builds a signed block the fetcher will accept.
func testBlock(t *testing.T, height uint64) (*types.Block, []byte) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	b := &types.Block{Header: types.BlockHeader{
		Height:       height,
		ChainID:      types.ChainID,
		AlgID:        qcrypto.AlgMLDSA65,
		Timestamp:    1_700_000_000,
		TxMerkleRoot: qcrypto.Hash32([]byte{}),
	}}
	h, err := b.ComputeHash()
	if err != nil {
		t.Fatalf("hash block: %v", err)
	}
	b.Hash = h
	if err := b.SignBlock(priv); err != nil {
		t.Fatalf("sign block: %v", err)
	}
	b.ValidatorPubKey = pub.Bytes()
	return b, pub.Bytes()
}

// A node must be able to catch up entirely over the sealed channel, with no
// cleartext block route served at all.
func TestSealedSyncFetchesBlockWithoutPlaintext(t *testing.T) {
	block, validatorPK := testBlock(t, 7)
	srv, cache := sealedSyncPeer(t, block)

	cfg := network.DefaultSyncConfig(validatorPK)
	cfg.Identities = cache
	cfg.AllowPlaintext = false // no downgrade permitted

	got, err := network.NewBlockFetcher(cfg).FetchBlock(srv.URL, 7)
	if err != nil {
		t.Fatalf("sealed fetch failed: %v", err)
	}
	if got.Header.Height != 7 {
		t.Fatalf("height=%d, want 7", got.Header.Height)
	}
}

// The same must hold for the head block, which the syncer uses to find a target.
func TestSealedSyncFetchesLatestBlock(t *testing.T) {
	block, validatorPK := testBlock(t, 11)
	srv, cache := sealedSyncPeer(t, block)

	cfg := network.DefaultSyncConfig(validatorPK)
	cfg.Identities = cache
	cfg.AllowPlaintext = false

	got, err := network.NewBlockFetcher(cfg).FetchLatestBlock(srv.URL)
	if err != nil {
		t.Fatalf("sealed latest fetch failed: %v", err)
	}
	if got.Header.Height != 11 {
		t.Fatalf("height=%d, want 11", got.Header.Height)
	}
}

// A sealed read must not require the write-plane token: an RPC node has to be
// able to catch up without holding the validator's secret.
func TestSealedReadNeedsNoWriteToken(t *testing.T) {
	block, validatorPK := testBlock(t, 3)

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("node identity: %v", err)
	}

	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "", // write plane disabled entirely
		ServeBlock: func(uint64) ([]byte, error) {
			return json.Marshal(block)
		},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	cfg := network.DefaultSyncConfig(validatorPK)
	cfg.Identities = network.NewIdentityCache()
	cfg.AllowPlaintext = false

	if _, err := network.NewBlockFetcher(cfg).FetchBlock(srv.URL, 3); err != nil {
		t.Fatalf("sealed read should not require a write token: %v", err)
	}
}

// Writes must still be rejected on the same endpoint when the token is wrong,
// so opening reads did not open the write plane with them.
func TestSealedWriteStillRequiresToken(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("node identity: %v", err)
	}

	applied := false
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "correct-token",
		HandleBlock: func([]byte) error {
			applied = true
			return nil
		},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	cache := network.NewIdentityCache()
	if err := cache.SendSealed(srv.URL, "wrong-token", network.KindBlock, []byte(`{}`)); err == nil {
		t.Fatal("expected a sealed block push with a bad token to be rejected")
	}
	if applied {
		t.Fatal("block was applied despite an invalid write-plane token")
	}
}
