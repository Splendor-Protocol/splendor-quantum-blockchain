package rpc

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"testing"

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
		ServeObjectSymbol: func(id []byte, index uint32) ([]byte, error) {
			return hub.ServeSymbol(id, index)
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

func TestSealedCodedObjectRoundTrip(t *testing.T) {
	hub := objects.NewHub()
	srv, pub, priv := codedObjectPeer(t, hub)

	object := bytes.Repeat([]byte("coded-live-path-"), 20) // 320 bytes
	send, err := objects.NewSender(object, objects.Descriptor{
		Anchor:     objects.Anchor{Kind: "block", ID: [32]byte{4, 2}, Height: 9},
		SymbolSize: 64,
	}, coding.NewRLNCCodec(), 4, priv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	hub.Put(send)

	got, err := objectFetcher().FetchCodedObject(srv.URL, objects.DefaultLimits())
	if err != nil {
		t.Fatalf("sealed coded fetch: %v", err)
	}
	if !bytes.Equal(got, object) {
		t.Fatal("fetched object does not match the published bytes")
	}
	_ = pub
}

func TestFetchCodedObjectRefusesForeignSigner(t *testing.T) {
	hub := objects.NewHub()
	srv, _, _ := codedObjectPeer(t, hub)
	_, otherPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{7}, 128)
	send, err := objects.NewSender(object, objects.Descriptor{
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

	_, err = objectFetcher().FetchCodedObject(srv.URL, objects.DefaultLimits())
	if err == nil {
		t.Fatal("fetch succeeded against a node that does not serve coded objects")
	}
}
