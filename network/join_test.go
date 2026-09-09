package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// sealedBy builds an empty, structurally valid block at height sealed by the
// given key, with the header naming that key's address.
func sealedBy(t *testing.T, height uint64, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey) *types.Block {
	t.Helper()
	b := &types.Block{Header: types.BlockHeader{
		Height: height, Timestamp: time.Now().Unix(), ChainID: types.ChainID, AlgID: qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: qcrypto.DeriveAddress(pub),
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

// plaintextPeerServing answers every block GET with b and serves no sealed
// transport, so the fetcher is exercised on the plaintext path.
func plaintextPeerServing(t *testing.T, b *types.Block) *httptest.Server {
	t.Helper()
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/p2p/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func onlyKeyOf(pub *qcrypto.PublicKey) ProducerKeyLookup {
	return func(addr qcrypto.Address) []byte {
		if addr == qcrypto.DeriveAddress(pub) {
			return pub.Bytes()
		}
		return nil
	}
}

// Under a rotating BFT leader most blocks are sealed by a key other than the
// genesis validator's. A block whose producer the registry lists as active is
// authenticated against that key, bound to the header's address.
func TestFetchBlockAcceptsActiveSetProducer(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 9, otherPub, otherPriv))

	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = onlyKeyOf(otherPub)
	got, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9)
	if err != nil {
		t.Fatalf("block from an active-set producer refused: %v", err)
	}
	if got.Header.Height != 9 {
		t.Fatalf("height %d, want 9", got.Header.Height)
	}
}

func TestFetchBlockRejectsUnknownProducer(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 9, otherPub, otherPriv))

	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return nil }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("block from a producer outside the active set accepted")
	}

	// A resolver handing back a key that does not derive to the header address
	// is a hostile or buggy registry and must not authenticate anything.
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return genesisPub.Bytes() }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("key not derived from the header address accepted")
	}

	// No resolver: today's single-key behaviour, unchanged.
	cfg.LookupProducerKey = nil
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("without a resolver a non-genesis producer must be refused")
	}
}

// The pinned key still authenticates the genesis validator's own blocks, with
// or without a resolver.
func TestFetchBlockStillAcceptsPinnedKey(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 9, genesisPub, genesisPriv))
	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return nil }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err != nil {
		t.Fatalf("genesis validator's block refused with a resolver present: %v", err)
	}
}

// Block 0 is the anchor everything else hangs from; the registry it would be
// resolved against does not exist yet, so only the pinned key may verify it.
func TestFetchBlockGenesisOnlyPinnedKey(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 0, otherPub, otherPriv))
	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = onlyKeyOf(otherPub)
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 0); err == nil {
		t.Fatal("block 0 must only ever verify against the pinned key")
	}
}

func TestFetchLatestBlockUsesResolver(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 12, otherPub, otherPriv))
	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	if _, err := NewBlockFetcher(cfg).FetchLatestBlock(srv.URL); err == nil {
		t.Fatal("latest block from a non-genesis producer accepted without a resolver")
	}
	cfg.LookupProducerKey = onlyKeyOf(otherPub)
	if _, err := NewBlockFetcher(cfg).FetchLatestBlock(srv.URL); err != nil {
		t.Fatalf("latest block from an active-set producer refused: %v", err)
	}
}

// The push path applies the same rule as the pull path.
func TestHandleIncomingBlockUsesResolver(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	body, err := json.Marshal(sealedBy(t, 4, otherPub, otherPriv))
	if err != nil {
		t.Fatal(err)
	}
	applied := false
	apply := func(*types.Block) error { applied = true; return nil }
	if err := HandleIncomingBlock(body, genesisPub.Bytes(), nil, apply); err == nil || applied {
		t.Fatal("pushed block from a non-genesis producer applied without a resolver")
	}
	if err := HandleIncomingBlock(body, genesisPub.Bytes(), onlyKeyOf(otherPub), apply); err != nil || !applied {
		t.Fatalf("pushed block from an active-set producer refused: %v", err)
	}
}

// VerifyBlockProducer is the one rule every ingress shares. No trusted key
// means nothing verifies, whatever the resolver says.
func TestVerifyBlockProducerFailsClosedWithoutTrustedKey(t *testing.T) {
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	b := sealedBy(t, 4, otherPub, otherPriv)
	if err := VerifyBlockProducer(b, nil, onlyKeyOf(otherPub)); err == nil {
		t.Fatal("a block verified with no trusted key configured")
	}
}

// The trusted key learned from a pinned genesis block is visible to callers
// (the push path reads it at call time) and safe to read concurrently.
func TestSyncerExposesBootstrappedTrustedKey(t *testing.T) {
	pub, _, _ := qcrypto.GenerateKeypair()
	pool := NewPeerPool("http://self", 10)
	s := NewSyncer(DefaultSyncConfig(nil), pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })
	if got := s.TrustedPubKey(); got != nil {
		t.Fatalf("fresh syncer reports a trusted key: %x", got[:4])
	}
	s.fetcher.setTrustedPubKey(pub.Bytes())
	if got := s.TrustedPubKey(); string(got) != string(pub.Bytes()) {
		t.Fatal("trusted key not exposed after bootstrap")
	}
}
