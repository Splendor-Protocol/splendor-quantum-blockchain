package x402

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// newVerifyFixture funds payer and returns a processor, a signed payload and
// matching requirements. The payer's key is registered on chain only when
// register is true; otherwise the account has a balance but no key, which is
// exactly the state of an address that has never sent a transaction.
func newVerifyFixture(t *testing.T, payerPub *qcrypto.PublicKey, payerPriv *qcrypto.PrivateKey, register bool) (*Processor, *PaymentPayload, *PaymentRequirements) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	payer := qcrypto.DeriveAddress(payerPub)
	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	if register {
		acc.PublicKey = payerPub.Bytes()
	}
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	payee := qcrypto.Address{0x55}
	now := time.Now().Unix()
	var nonce [32]byte
	nonce[0] = 0xAA
	payload := &PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	reqs := &PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 300,
	}
	return NewProcessor(sdb), payload, reqs
}

// A payer whose key was never registered on chain (no prior signed
// transaction) can verify a payment by attaching the key that derives From.
func TestVerifyFreshPayerWithPublicKey(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, false)

	// Without the key the registry has nothing to check against.
	if res := proc.Verify(reqs, payload); res.Valid {
		t.Fatal("fresh payer without public_key must not verify")
	} else if !strings.Contains(res.Reason, "public key not registered") {
		t.Fatalf("unexpected reason: %q", res.Reason)
	}

	payload.PublicKey = pub.Bytes()
	if res := proc.Verify(reqs, payload); !res.Valid {
		t.Fatalf("fresh payer with derived public_key rejected: %s", res.Reason)
	}

	// Verify is read-only: the supplied key must not have been registered.
	if _, err := proc.state.GetPublicKey(payload.From); err != types.ErrNoPublicKey {
		t.Fatalf("Verify registered a key: err=%v", err)
	}
}

// A supplied key that does not derive From is refused before any signature
// check, so an attacker cannot pair their own key with a victim's address.
func TestVerifyRejectsPublicKeyNotDerivingPayer(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	otherPub, otherPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, false)

	// Wrong key, right signature.
	payload.PublicKey = otherPub.Bytes()
	res := proc.Verify(reqs, payload)
	if res.Valid {
		t.Fatal("key that does not derive From must be rejected")
	}
	if !strings.Contains(res.Reason, "does not derive") {
		t.Fatalf("unexpected reason: %q", res.Reason)
	}

	// Wrong key AND a signature made with it: still rejected on derivation,
	// never reaching Verify.
	if err := payload.Sign(otherPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	res = proc.Verify(reqs, payload)
	if res.Valid || !strings.Contains(res.Reason, "does not derive") {
		t.Fatalf("forged signer accepted or wrong reason: valid=%v reason=%q", res.Valid, res.Reason)
	}

	// Malformed key lengths are refused with a length error, not a panic.
	for _, n := range []int{1, qcrypto.PublicKeySize - 1, qcrypto.PublicKeySize + 1} {
		payload.PublicKey = make([]byte, n)
		res = proc.Verify(reqs, payload)
		if res.Valid || !strings.Contains(res.Reason, "invalid payer public key") {
			t.Fatalf("len %d: valid=%v reason=%q", n, res.Valid, res.Reason)
		}
	}
}

// A key registered on chain is authoritative: a payload carrying a different
// key (and a signature under it) must be checked against the registry key.
func TestVerifyRegisteredKeyWinsOverSuppliedKey(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	attackerPub, attackerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, true)

	// Registered payer, no supplied key: the pre-existing path still works.
	if res := proc.Verify(reqs, payload); !res.Valid {
		t.Fatalf("registered payer rejected: %s", res.Reason)
	}

	// Registered payer, supplied key equal to the registered one: harmless.
	payload.PublicKey = pub.Bytes()
	if res := proc.Verify(reqs, payload); !res.Valid {
		t.Fatalf("registered payer with matching public_key rejected: %s", res.Reason)
	}

	// Registered payer, attacker key + attacker signature over the same body:
	// the registry key is used, so the attacker's signature does not verify.
	payload.PublicKey = attackerPub.Bytes()
	if err := payload.Sign(attackerPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	res := proc.Verify(reqs, payload)
	if res.Valid {
		t.Fatal("supplied key overrode the registered key")
	}
	if !strings.Contains(res.Reason, "invalid ML-DSA signature") {
		t.Fatalf("unexpected reason: %q", res.Reason)
	}
}

// PublicKey lives outside the signed body: attaching or dropping it must not
// change the hash, and the wire field is omitted when empty so payloads from
// older clients are byte-identical.
func TestPaymentPayloadHashExcludesPublicKey(t *testing.T) {
	pub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	var nonce [32]byte
	nonce[0] = 0x01
	payload := &PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: qcrypto.DeriveAddress(pub), To: qcrypto.Address{0x55},
		Amount: big.NewInt(500), ValidAfter: 1, ValidBefore: 2, Nonce: nonce,
	}
	h1, err := payload.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	payload.PublicKey = pub.Bytes()
	h2, err := payload.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h1 != h2 {
		t.Fatal("PublicKey changed the payload hash")
	}

	payload.PublicKey = nil
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "public_key") {
		t.Fatalf("empty public_key must be omitted from the wire: %s", raw)
	}
	payload.PublicKey = pub.Bytes()
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back PaymentPayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(back.PublicKey) != string(pub.Bytes()) {
		t.Fatal("public_key did not round-trip through JSON")
	}
}
