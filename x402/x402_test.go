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

func TestVerifyMarksNonceUsed(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, true)
	if err := proc.state.MarkX402NonceSettled(NonceKey(payload.From, payload.Nonce), &types.X402Settlement{
		From:    payload.From,
		To:      payload.To,
		Amount:  new(big.Int).Set(payload.Amount),
		ChainID: types.ChainID,
		Height:  4,
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	// A payment only confirms externally once it has committed: Verify reads
	// committed state, so stage the settlement durably before checking.
	if _, err := proc.state.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	res := proc.Verify(reqs, payload)
	if res.Valid || !res.NonceUsed {
		t.Fatalf("used nonce: valid=%v nonce_used=%v reason=%q", res.Valid, res.NonceUsed, res.Reason)
	}
}

// A settlement staged only on the pending overlay (before the settling block
// commits) must NOT confirm externally: Verify treats the nonce as fresh and
// SettlementMatch reports no committed record. This is the regression guard for
// the speculative-payment confirmation bug — a proposal construction executes
// against the pending overlay before discarding it, so reading it would confirm
// a payment that never durably settled.
func TestVerifyIgnoresPendingSettlement(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, true)
	if err := proc.state.MarkX402NonceSettled(NonceKey(payload.From, payload.Nonce), &types.X402Settlement{
		From:    payload.From,
		To:      payload.To,
		Amount:  new(big.Int).Set(payload.Amount),
		ChainID: types.ChainID,
		Height:  4,
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// No commit yet: the settlement exists only on the pending overlay.
	if res := proc.Verify(reqs, payload); !res.Valid || res.NonceUsed {
		t.Fatalf("pending-only settlement must look fresh: valid=%v nonce_used=%v", res.Valid, res.NonceUsed)
	}
	if ok, err := proc.SettlementMatch(payload); err != nil || ok {
		t.Fatalf("pending-only settlement must not match: ok=%v err=%v", ok, err)
	}

	// After the settling block commits, the same payment is confirmed.
	if _, err := proc.state.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res := proc.Verify(reqs, payload); res.Valid || !res.NonceUsed {
		t.Fatalf("committed settlement must be used: valid=%v nonce_used=%v", res.Valid, res.NonceUsed)
	}
	if ok, err := proc.SettlementMatch(payload); err != nil || !ok {
		t.Fatalf("committed settlement must match: ok=%v err=%v", ok, err)
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

// A used nonce must not confirm a payment with changed recipient/amount: the
// on-chain settlement record binds the nonce to the payment that actually
// happened, and SettlementMatch must refuse any other claim.
func TestSettlementMatchRejectsChangedPayment(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, _ := newVerifyFixture(t, pub, priv, true)
	if err := proc.state.MarkX402NonceSettled(NonceKey(payload.From, payload.Nonce), &types.X402Settlement{
		From:    payload.From,
		To:      payload.To,
		Amount:  new(big.Int).Set(payload.Amount),
		ChainID: types.ChainID,
		Height:  4,
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	// SettlementMatch reads committed state: commit the staged settlement so the
	// record is durable, as it would be after the settling block commits.
	if _, err := proc.state.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Identical details: matches.
	if ok, err := proc.SettlementMatch(payload); err != nil || !ok {
		t.Fatalf("identical payment should match: ok=%v err=%v", ok, err)
	}

	// Changed recipient: rejected even though the nonce is consumed.
	changed := *payload
	changed.To = qcrypto.Address{0x77}
	if ok, err := proc.SettlementMatch(&changed); err != nil || ok {
		t.Fatalf("changed recipient must not match: ok=%v err=%v", ok, err)
	}

	// Changed amount: rejected.
	changed = *payload
	changed.Amount = big.NewInt(5000)
	if ok, err := proc.SettlementMatch(&changed); err != nil || ok {
		t.Fatalf("changed amount must not match: ok=%v err=%v", ok, err)
	}
}

// An already-used nonce must never bypass identity checks: Verify must verify
// the signature before reporting NonceUsed, so an unsigned (or wrongly signed)
// fabricated retry is refused rather than presented as an idempotent replay.
func TestVerifyUsedNonceStillChecksSignature(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	proc, payload, reqs := newVerifyFixture(t, pub, priv, true)
	if err := proc.state.MarkX402NonceSettled(NonceKey(payload.From, payload.Nonce), &types.X402Settlement{
		From:    payload.From,
		To:      payload.To,
		Amount:  new(big.Int).Set(payload.Amount),
		ChainID: types.ChainID,
		Height:  4,
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	// Verify reads committed state for the nonce check; commit first so the used
	// nonce is durably recorded.
	if _, err := proc.state.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Correct signature + used nonce: NonceUsed, not Valid.
	res := proc.Verify(reqs, payload)
	if res.Valid || !res.NonceUsed {
		t.Fatalf("signed used nonce: valid=%v nonce_used=%v", res.Valid, res.NonceUsed)
	}

	// Fabricated retry: same nonce, garbage signature. Must NOT be reported as
	// an idempotent replay — the signature check runs first.
	forged := *payload
	forged.Signature = []byte("not-a-signature")
	res = proc.Verify(reqs, &forged)
	if res.NonceUsed {
		t.Fatalf("unsigned used nonce reported NonceUsed: %+v", res)
	}
	if res.Valid {
		t.Fatalf("unsigned used nonce reported Valid")
	}
}
