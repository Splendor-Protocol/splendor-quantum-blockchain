package block

import (
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// An account's public key is the sole authority over its funds, so it must be
// recorded only after it has verified a signature — not merely because a
// transaction carried it.
//
// This is also what makes "every stored account key is ML-DSA-65" true by
// construction rather than by label: Validate rejects any other alg_id, and
// VerifySignature checks the signature as ML-DSA-65, so nothing else can reach
// account state.
func TestPublicKeyNotRegisteredWhenSignatureFails(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)

	tx := types.NewTransfer(from, to, big.NewInt(100), big.NewInt(testFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Corrupt the signature, leaving a well-formed key that matches From.
	tx.Signature[0] ^= 0xff

	if err := ex.executeTx(tx, 1, time.Now().Unix(), &types.Receipt{}); err == nil {
		t.Fatal("a transaction with a bad signature must not apply")
	}

	acc, err := sdb.GetAccount(from)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if acc.HasPublicKey() {
		t.Fatal("public key was registered by a transaction whose signature did not verify")
	}
}

func TestPublicKeyRegisteredOnValidTransaction(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x56)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)

	tx := signedTransfer(t, from, priv, to, 100, 0)
	if err := ex.executeTx(tx, 1, time.Now().Unix(), &types.Receipt{}); err != nil {
		t.Fatalf("valid transfer should apply: %v", err)
	}

	acc, err := sdb.GetAccount(from)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if !acc.HasPublicKey() {
		t.Fatal("a verified public key should have been registered")
	}
}

// The empty alg_id that P1.1 called out is already refused, and refused before
// any key can be recorded: Validate runs first in executeTx.
func TestEmptyAlgIDRejected(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)

	tx := types.NewTransfer(from, testAddr(0x57), big.NewInt(100), big.NewInt(testFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	tx.AlgID = ""

	if err := tx.Validate(); err == nil {
		t.Fatal("a transaction with an empty alg_id must be rejected, not defaulted")
	}

	tx.AlgID = "Dilithium3"
	if err := tx.Validate(); err == nil {
		t.Fatal("a transaction naming a different scheme must be rejected")
	}
}
