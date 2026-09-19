package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

func codedObjectPeer(t *testing.T, hub *objects.Hub) (*httptest.Server, *qcrypto.PublicKey, *qcrypto.PrivateKey) {
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
		ServeObjectManifest: func(id []byte) ([]byte, error) {
			return hub.ServeManifest(id)
		},
		ServeObjectSymbol: func(id []byte, generation uint32, index uint32) ([]byte, error) {
			return hub.ServeSymbol(id, generation, index)
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pub, priv
}

func objectFetcher() *network.BlockFetcher {
	cfg := network.DefaultSyncConfig(nil)
	cfg.Identities = network.NewIdentityCache()
	cfg.AllowPlaintext = false
	return network.NewBlockFetcher(cfg)
}

// The sealed serve path hands out the multi-generation object manifest and
// per-generation symbol proofs; a receiver built from the fetched manifest
// (pinned to the peer's node identity) reconstructs the published bytes.
func TestSealedCodedObjectServePath(t *testing.T) {
	hub := objects.NewHub()
	srv, _, priv := codedObjectPeer(t, hub)

	object := bytes.Repeat([]byte("coded-live-path-"), 20) // 320 bytes
	send, err := objects.NewObjectSender(object, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: [32]byte{4, 2}, Height: 9},
		SymbolSize: 64,
	}, coding.NewRLNCCodec(), 4, priv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	identities := network.NewIdentityCache()
	doc, err := identities.Get(srv.URL)
	if err != nil {
		t.Fatalf("peer identity: %v", err)
	}
	raw, err := identities.QuerySealedTimeout(srv.URL, network.KindGetObjectManifest, []byte(`{}`),
		256<<10, time.Minute)
	if err != nil {
		t.Fatalf("sealed manifest query: %v", err)
	}
	m, err := objects.DecodeObjectManifest(raw)
	if err != nil {
		t.Fatalf("decode object manifest: %v", err)
	}
	recv, err := objects.NewObjectReceiver(m, objects.DefaultLimits(), objects.AllowObjectSignerBytes(doc.NodePubKey))
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	descID := m.DescriptorID()
	for gen := 0; gen < send.GenerationCount(); gen++ {
		for i := 0; i < send.SymbolCount(gen); i++ {
			req, err := json.Marshal(network.GetObjectSymbolRequest{
				DescriptorID: descID[:], Generation: uint32(gen), Index: uint32(i),
			})
			if err != nil {
				t.Fatal(err)
			}
			proofRaw, err := identities.QuerySealedTimeout(srv.URL, network.KindGetObjectSymbol, req,
				128<<10, time.Minute)
			if err != nil {
				t.Fatalf("sealed symbol query %d/%d: %v", gen, i, err)
			}
			proof, err := objects.DecodeSymbolProof(proofRaw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recv.AbsorbProof(gen, proof); err != nil {
				t.Fatalf("absorb %d/%d: %v", gen, i, err)
			}
		}
	}
	got, err := recv.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, object) {
		t.Fatal("reconstructed object does not match the published bytes")
	}
}

// The full fetch path: FetchCodedObject pulls the manifest and generation-aware
// symbols over the sealed channel and reconstructs the published bytes. The
// object spans several generations, so generation indexing on the wire is
// exercised end to end.
func TestSealedCodedObjectRoundTrip(t *testing.T) {
	hub := objects.NewHub()
	srv, _, priv := codedObjectPeer(t, hub)

	// 40 KiB at 64-byte symbols is 640 source symbols -> 3 generations.
	object := make([]byte, 40*1024)
	for i := range object {
		object[i] = byte(i * 7)
	}
	send, err := objects.NewObjectSender(object, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: [32]byte{4, 2}, Height: 9},
		SymbolSize: 64,
	}, coding.NewRLNCCodec(), 4, priv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if send.GenerationCount() < 2 {
		t.Fatalf("test object spans %d generations, want >= 2", send.GenerationCount())
	}
	hub.Put(send)

	got, err := objectFetcher().FetchCodedObject(srv.URL, objects.DefaultLimits())
	if err != nil {
		t.Fatalf("sealed coded fetch: %v", err)
	}
	if !bytes.Equal(got, object) {
		t.Fatal("fetched object does not match the published bytes")
	}
}

func TestFetchCodedObjectRefusesForeignSigner(t *testing.T) {
	hub := objects.NewHub()
	srv, _, _ := codedObjectPeer(t, hub)
	_, otherPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{7}, 128)
	send, err := objects.NewObjectSender(object, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, otherPriv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	_, err = objectFetcher().FetchCodedObject(srv.URL, objects.DefaultLimits())
	if !errors.Is(err, objects.ErrUnauthorizedSigner) {
		t.Fatalf("foreign-signed object = %v, want ErrUnauthorizedSigner", err)
	}
}

func TestObjectReceiverRefusesForeignSigner(t *testing.T) {
	hub := objects.NewHub()
	srv, _, _ := codedObjectPeer(t, hub)
	_, otherPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{7}, 128)
	send, err := objects.NewObjectSender(object, objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, otherPriv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	identities := network.NewIdentityCache()
	doc, err := identities.Get(srv.URL)
	if err != nil {
		t.Fatalf("peer identity: %v", err)
	}
	raw, err := identities.QuerySealedTimeout(srv.URL, network.KindGetObjectManifest, []byte(`{}`),
		256<<10, time.Minute)
	if err != nil {
		t.Fatalf("sealed manifest query: %v", err)
	}
	m, err := objects.DecodeObjectManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = objects.NewObjectReceiver(m, objects.DefaultLimits(), objects.AllowObjectSignerBytes(doc.NodePubKey))
	if !errors.Is(err, objects.ErrUnauthorizedSigner) {
		t.Fatalf("foreign-signed object = %v, want ErrUnauthorizedSigner", err)
	}
}

func TestCodedObjectKindsNotImplementedWhenUnwired(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{Identity: identity, Token: "write-secret"})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	identities := network.NewIdentityCache()
	_, err = identities.QuerySealedTimeout(srv.URL, network.KindGetObjectManifest, []byte(`{}`),
		256<<10, time.Minute)
	if err == nil {
		t.Fatal("manifest query succeeded against a node that does not serve coded objects")
	}
}
