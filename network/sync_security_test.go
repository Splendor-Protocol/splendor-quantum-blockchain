package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// plaintextOnlyPeer serves blocks over cleartext GET and refuses to publish an
// identity document, which is exactly how an attacker would try to force a
// syncing node off the sealed channel.
func plaintextOnlyPeer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	blk := &types.Block{Header: types.BlockHeader{
		Height:       5,
		ChainID:      types.ChainID,
		AlgID:        qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}),
	}}
	h, err := blk.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	blk.Hash = h
	if err := blk.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	body, err := json.Marshal(blk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
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
	return srv, pub.Bytes()
}

// With the sealed channel configured, a peer that cannot be sealed to must not
// be silently downgraded to cleartext.
func TestSyncRefusesPlaintextDowngrade(t *testing.T) {
	srv, validatorPK := plaintextOnlyPeer(t)

	cfg := DefaultSyncConfig(validatorPK)
	cfg.Identities = NewIdentityCache()
	cfg.AllowPlaintext = false

	_, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 5)
	if err == nil {
		t.Fatal("expected the fetch to fail rather than fall back to cleartext")
	}
	if !strings.Contains(err.Error(), "plaintext sync is disabled") {
		t.Fatalf("error should name the downgrade refusal, got: %v", err)
	}
}

// A node with no sealed transport at all must also refuse, rather than treating
// a missing identity cache as permission to use cleartext.
func TestSyncRefusesWhenNoSealedTransportConfigured(t *testing.T) {
	srv, validatorPK := plaintextOnlyPeer(t)

	cfg := DefaultSyncConfig(validatorPK)
	cfg.Identities = nil
	cfg.AllowPlaintext = false

	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 5); err == nil {
		t.Fatal("expected the fetch to fail with no sealed transport available")
	}
}

// The escape hatch still works for operators running older peers.
func TestSyncAllowsPlaintextWhenExplicitlyEnabled(t *testing.T) {
	srv, validatorPK := plaintextOnlyPeer(t)

	cfg := DefaultSyncConfig(validatorPK)
	cfg.Identities = NewIdentityCache()
	cfg.AllowPlaintext = true

	blk, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 5)
	if err != nil {
		t.Fatalf("plaintext fallback should be permitted when opted in: %v", err)
	}
	if blk.Header.Height != 5 {
		t.Fatalf("height=%d, want 5", blk.Header.Height)
	}
}
