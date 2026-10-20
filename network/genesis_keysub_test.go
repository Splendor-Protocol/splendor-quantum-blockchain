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

// A peer must not be able to hand a bootstrapping node its own validator key.
//
// The attack: serve the GENUINE pinned header (so the recomputed hash matches the
// pin), but attach an attacker-controlled ValidatorPubKey — which lives outside
// the header and so does not affect that hash — set Hash to any value, and sign
// that value with the attacker's key. VerifySignature checks the served Hash, so
// without Validate() binding Hash to the header, both checks pass and the node
// adopts the attacker as the chain's validator.
func TestGenesisBootstrapRejectsSubstitutedValidatorKey(t *testing.T) {
	honestPub, honestPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	attackerPub, attackerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// The real genesis block, as the honest chain produced it.
	genesis := types.GenesisBlock(qcrypto.DeriveAddress(honestPub), [32]byte{}, 1)
	pinned, err := genesis.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	genesis.Hash = pinned
	sig, err := qcrypto.Sign(honestPriv, pinned[:], qcrypto.CtxBlock)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	genesis.ValidatorSignature = sig
	genesis.ValidatorPubKey = honestPub.Bytes()

	// The hostile version: same header (so it still hashes to the pin), attacker's
	// key, and a signature over a Hash the attacker chose.
	hostile := *genesis
	forged := [32]byte{0xde, 0xad, 0xbe, 0xef}
	hostile.Hash = forged
	hostile.ValidatorPubKey = attackerPub.Bytes()
	hostile.ValidatorSignature, err = qcrypto.Sign(attackerPriv, forged[:], qcrypto.CtxBlock)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(&hostile)
	}))
	defer srv.Close()

	AllowPrivatePeers = true
	defer func() { AllowPrivatePeers = false }()

	f := NewBlockFetcher(SyncConfig{GenesisHash: pinned, RequestTimeout: 5 * time.Second, AllowPlaintext: true})
	got, err := f.FetchGenesisValidatorPubKey(srv.URL)
	if err == nil {
		t.Fatalf("adopted a substituted validator key: got %x…", got[:8])
	}
	// It must fail for the right reason, not incidentally.
	if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "does not derive") {
		t.Fatalf("rejected, but not for the substitution: %v", err)
	}
}
