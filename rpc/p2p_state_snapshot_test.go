package rpc

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// snapshotPeer starts a node exposing only the sealed transport, answering
// get_state_snapshot with serve.
func snapshotPeer(t *testing.T, serve func() ([]byte, error)) *httptest.Server {
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
		Identity:           identity,
		Token:              "write-secret",
		ServeStateSnapshot: serve,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// signedHead builds a block at height committing to root, sealed by priv.
func signedHead(t *testing.T, height uint64, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, root [32]byte) *types.Block {
	t.Helper()
	b := &types.Block{Header: types.BlockHeader{
		Height: height, Timestamp: 1_700_000_000, ChainID: types.ChainID, AlgID: qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), StateRoot: root, ValidatorAddr: qcrypto.DeriveAddress(pub),
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

func testSnapshot(t *testing.T, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey) *types.StateSnapshot {
	t.Helper()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
	}
	return &types.StateSnapshot{
		Height: 42,
		Block:  signedHead(t, 42, pub, priv, state.SnapshotRoot(leaves)),
		Leaves: leaves,
	}
}

func snapshotFetcher(trustedPK []byte) *network.BlockFetcher {
	cfg := network.DefaultSyncConfig(trustedPK)
	cfg.Identities = network.NewIdentityCache()
	cfg.AllowPlaintext = false
	return network.NewBlockFetcher(cfg)
}

// A snapshot travels the sealed read plane like a block, with no write token.
func TestSealedStateSnapshotRead(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, genesisPub, genesisPriv)
	srv := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) })

	got, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshot(srv.URL)
	if err != nil {
		t.Fatalf("sealed snapshot fetch failed: %v", err)
	}
	if got.Height != 42 || got.Block == nil || got.Block.Hash != snap.Block.Hash || len(got.Leaves) != 2 {
		t.Fatalf("got height %d, %d leaves", got.Height, len(got.Leaves))
	}
}

// The pinned genesis key is the only anchor a node with empty state has. A
// head sealed by any other validator is not refused as hostile — leader
// rotation will produce a genesis-sealed head shortly — but it cannot be
// used, and the caller must be able to tell the two apart.
func TestFetchStateSnapshotRefusesUnanchoredHead(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, otherPub, otherPriv)
	srv := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) })

	_, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshot(srv.URL)
	if !errors.Is(err, network.ErrSnapshotNotAnchored) {
		t.Fatalf("snapshot with a head sealed by another validator = %v, want ErrSnapshotNotAnchored", err)
	}
	if !strings.Contains(err.Error(), "genesis-checkpoint") {
		t.Fatalf("unanchored error must name the genesis-checkpoint trust model: %v", err)
	}
}

func TestFetchStateSnapshotRefusesForgedOrMalformedHead(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	for name, mutate := range map[string]func(*types.StateSnapshot){
		"bad signature":   func(s *types.StateSnapshot) { s.Block.ValidatorSignature[0] ^= 0xff },
		"height mismatch": func(s *types.StateSnapshot) { s.Height = 41 },
		"no block":        func(s *types.StateSnapshot) { s.Block = nil },
		"tampered header": func(s *types.StateSnapshot) { s.Block.Header.StateRoot[0] ^= 0xff },
	} {
		t.Run(name, func(t *testing.T) {
			snap := testSnapshot(t, genesisPub, genesisPriv)
			mutate(snap)
			srv := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) })
			_, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshot(srv.URL)
			if err == nil {
				t.Fatalf("%s: accepted", name)
			}
			if errors.Is(err, network.ErrSnapshotNotAnchored) {
				t.Fatalf("%s: reported as merely unanchored, which the caller would retry forever: %v", name, err)
			}
		})
	}
}

func TestFetchStateSnapshotNeedsTrustedKey(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, genesisPub, genesisPriv)
	srv := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) })
	if _, err := snapshotFetcher(nil).FetchStateSnapshot(srv.URL); err == nil {
		t.Fatal("a snapshot was accepted with no trusted key to anchor it")
	}
}

// A peer that does not serve snapshots answers with a clear refusal.
func TestSealedStateSnapshotReadUnavailable(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	srv := snapshotPeer(t, nil)
	_, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshot(srv.URL)
	if err == nil {
		t.Fatal("fetch succeeded from a peer that serves no snapshots")
	}
}

// StateSync walks the peer pool until one snapshot imports, and names every
// failure when none does.
func TestStateSyncTriesEveryPeer(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, genesisPub, genesisPriv)
	broken := snapshotPeer(t, func() ([]byte, error) { return nil, errors.New("no snapshot here") })
	good := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) })

	cfg := network.DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = network.NewIdentityCache()
	pool := network.NewPeerPool("http://self", 10)
	pool.Add(&network.PeerInfo{Addr: broken.URL})
	pool.Add(&network.PeerInfo{Addr: good.URL})
	syncer := network.NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })

	imported := 0
	err := syncer.StateSync(func(s *types.StateSnapshot) error {
		imported++
		if s.Height != 42 {
			return errors.New("wrong snapshot")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("state sync failed with a working peer in the pool: %v", err)
	}
	if imported != 1 {
		t.Fatalf("importer called %d times, want 1", imported)
	}

	onlyBroken := network.NewPeerPool("http://self", 10)
	onlyBroken.Add(&network.PeerInfo{Addr: broken.URL})
	syncer = network.NewSyncer(cfg, onlyBroken, func() uint64 { return 0 }, func(*types.Block) error { return nil })
	err = syncer.StateSync(func(*types.StateSnapshot) error { t.Fatal("importer called with nothing to import"); return nil })
	if err == nil || !strings.Contains(err.Error(), broken.URL) {
		t.Fatalf("state sync with no serving peer = %v, want an error naming the peer", err)
	}
}

// The coded snapshot path: the object manifest is signed by the peer's NODE
// identity (transfer authentication) while the head block inside is sealed by
// the pinned genesis validator (content authentication). Both must hold.
func TestFetchStateSnapshotCodedAuthenticatesTransferAndContent(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, genesisPub, genesisPriv)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	hub := objects.NewHub()
	srv, _, nodePriv := codedObjectPeer(t, hub)
	send, err := objects.NewObjectSender(raw, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "snapshot", ID: snap.Block.Hash, Height: snap.Height},
		SymbolSize: 64,
	}, coding.NewRLNCCodec(), 2, nodePriv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	got, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshotCoded(srv.URL)
	if err != nil {
		t.Fatalf("coded snapshot fetch failed: %v", err)
	}
	if got.Height != 42 || got.Block == nil || got.Block.Hash != snap.Block.Hash || len(got.Leaves) != 2 {
		t.Fatalf("got height %d, %d leaves", got.Height, len(got.Leaves))
	}
}

// A coded transfer can be fully authenticated yet carry an unanchored head:
// the content check must refuse it exactly like the plain path does.
func TestFetchStateSnapshotCodedRefusesUnanchoredHead(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, otherPub, otherPriv) // head sealed by a NON-genesis key
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	hub := objects.NewHub()
	srv, _, nodePriv := codedObjectPeer(t, hub)
	send, err := objects.NewObjectSender(raw, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "snapshot", ID: snap.Block.Hash, Height: snap.Height},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, nodePriv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	_, err = snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshotCoded(srv.URL)
	if !errors.Is(err, network.ErrSnapshotNotAnchored) {
		t.Fatalf("coded snapshot with unanchored head = %v, want ErrSnapshotNotAnchored", err)
	}
}

// Negotiation: CodedObjects is on by default, and a peer that serves no coded
// objects must still complete a state sync through the plain single-shot path.
func TestStateSyncFallsBackToPlainWhenPeerServesNoCodedObjects(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	snap := testSnapshot(t, genesisPub, genesisPriv)
	plainOnly := snapshotPeer(t, func() ([]byte, error) { return json.Marshal(snap) }) // no coded handlers

	cfg := network.DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = network.NewIdentityCache()
	if !cfg.CodedObjects {
		t.Fatal("DefaultSyncConfig must enable CodedObjects")
	}
	pool := network.NewPeerPool("http://self", 10)
	pool.Add(&network.PeerInfo{Addr: plainOnly.URL})
	syncer := network.NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })

	imported := 0
	if err := syncer.StateSync(func(s *types.StateSnapshot) error {
		imported++
		return nil
	}); err != nil {
		t.Fatalf("state sync did not fall back to the plain path: %v", err)
	}
	if imported != 1 {
		t.Fatalf("importer called %d times, want 1", imported)
	}
}
