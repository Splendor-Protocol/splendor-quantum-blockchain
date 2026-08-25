package crypto

import (
	"bytes"
	"crypto/mlkem"
	"encoding/hex"
	"testing"

	mldsa65 "github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

// katSeed is a fixed seed used to derive a deterministic ML-DSA-65 keypair.
const katSeedText = "splendor-quantum-kat-seed-000001"

// Pinned known answers for katSeed. These are regression anchors: if a library
// upgrade, a parameter change, or an accidental switch back to round-3
// Dilithium alters the scheme, these hashes change and the build fails loudly
// instead of silently forking every wallet and validator on the network.
const (
	katPubKeyHash  = "a1384dd6dfb705282f8f7e239d34337ae48f1c2f212db49f8ebf87d60a396325"
	katPrivKeyHash = "ce564aeaad15f83f2ffe6c150a2347f869aa08acb0fac5b2312c474c7c429839"

	// Signatures over katMessage under each domain context. That these differ
	// is the whole point of domain separation.
	katSigHashTx    = "ca5f30a9c721be55e99a9db9773bf2332b73b1c2b15c618e1aadb6dcf8b2e8f0"
	katSigHashBlock = "e0e38906d1184322c04810027bfc93c0e3f32089e1644acd38a0d2a8476fd0bb"
)

var katMessage = []byte("splendor-quantum known-answer message")

func katKeypair(t *testing.T) (*PublicKey, *PrivateKey) {
	t.Helper()
	var seed [mldsa65.SeedSize]byte
	copy(seed[:], []byte(katSeedText))
	pub, priv := mldsa65.NewKeyFromSeed(&seed)
	return &PublicKey{k: pub}, &PrivateKey{k: priv}
}

func hashHex(b []byte) string {
	h := Hash32(b)
	return hex.EncodeToString(h[:])
}

// ML-DSA-65 parameters must match NIST FIPS 204. A mismatch means we are not
// running the algorithm the chain claims to run.
func TestMLDSAParametersMatchFIPS204(t *testing.T) {
	cases := []struct {
		name      string
		got, want int
	}{
		{"public key", PublicKeySize, 1952},
		{"private key", PrivateKeySize, 4032},
		{"signature", SignatureSize, 3309},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s size = %d, want %d (ML-DSA-65 / FIPS 204)", c.name, c.got, c.want)
		}
	}
}

// Deterministic keygen from a fixed seed must produce the pinned keypair.
func TestMLDSAKeyKnownAnswer(t *testing.T) {
	pub, priv := katKeypair(t)

	if got := hashHex(pub.Bytes()); got != katPubKeyHash {
		t.Errorf("public key changed for the pinned seed:\n got  %s\n want %s", got, katPubKeyHash)
	}
	if got := hashHex(priv.Bytes()); got != katPrivKeyHash {
		t.Errorf("private key changed for the pinned seed:\n got  %s\n want %s", got, katPrivKeyHash)
	}
}

// Signing must be deterministic and must match the pinned signature per context.
func TestMLDSASignatureKnownAnswer(t *testing.T) {
	_, priv := katKeypair(t)

	for _, c := range []struct {
		name string
		ctx  []byte
		want string
	}{
		{"transaction", CtxTransaction, katSigHashTx},
		{"block", CtxBlock, katSigHashBlock},
	} {
		sig, err := Sign(priv, katMessage, c.ctx)
		if err != nil {
			t.Fatalf("%s: sign: %v", c.name, err)
		}
		if len(sig) != SignatureSize {
			t.Fatalf("%s: signature is %d bytes, want %d", c.name, len(sig), SignatureSize)
		}
		if got := hashHex(sig); got != c.want {
			t.Errorf("%s signature changed:\n got  %s\n want %s", c.name, got, c.want)
		}

		// Determinism: consensus requires bit-identical signatures on replay.
		again, err := Sign(priv, katMessage, c.ctx)
		if err != nil {
			t.Fatalf("%s: re-sign: %v", c.name, err)
		}
		if !bytes.Equal(sig, again) {
			t.Errorf("%s: signing is not deterministic", c.name)
		}
	}
}

// A signature is only valid under the context it was produced for. This is the
// property that stops a block signature being replayed as a transaction
// signature (or any other cross-purpose confusion).
func TestMLDSAContextSeparationIsEnforced(t *testing.T) {
	pub, priv := katKeypair(t)

	contexts := map[string][]byte{
		"transaction": CtxTransaction,
		"block":       CtxBlock,
		"x402":        CtxX402,
		"vote":        CtxVote,
		"message":     CtxMessage,
		"p2p":         CtxPeerHandshake,
		"oracle":      CtxOracle,
		"permit":      CtxPermit,
		"multisig":    CtxMultisig,
	}

	for signName, signCtx := range contexts {
		sig, err := Sign(priv, katMessage, signCtx)
		if err != nil {
			t.Fatalf("sign under %s: %v", signName, err)
		}
		for verifyName, verifyCtx := range contexts {
			ok := Verify(pub, katMessage, verifyCtx, sig)
			if signName == verifyName && !ok {
				t.Errorf("signature made under %s does not verify under its own context", signName)
			}
			if signName != verifyName && ok {
				t.Errorf("SECURITY: signature made for %q verified as %q — contexts are not separated",
					signName, verifyName)
			}
		}
	}
}

// Every context must be distinct and within the FIPS 204 length limit.
func TestContextStringsAreDistinctAndValid(t *testing.T) {
	all := [][]byte{
		CtxTransaction, CtxBlock, CtxX402, CtxVote,
		CtxMessage, CtxPeerHandshake, CtxOracle, CtxPermit, CtxMultisig,
	}
	seen := make(map[string]bool, len(all))
	for _, c := range all {
		if len(c) == 0 {
			t.Error("empty context string: signatures would not be domain-separated")
		}
		if len(c) > maxContextSize {
			t.Errorf("context %q exceeds the %d-byte FIPS 204 limit", c, maxContextSize)
		}
		if seen[string(c)] {
			t.Errorf("duplicate context string %q: two purposes share a domain", c)
		}
		seen[string(c)] = true
	}
}

// Signing without a context must be refused outright, so a future call site
// cannot silently opt out of domain separation.
func TestSignRejectsEmptyContext(t *testing.T) {
	_, priv := katKeypair(t)
	if _, err := Sign(priv, katMessage, nil); err == nil {
		t.Fatal("signing with a nil context was allowed")
	}
	if _, err := Sign(priv, katMessage, []byte{}); err == nil {
		t.Fatal("signing with an empty context was allowed")
	}

	pub, _ := katKeypair(t)
	sig, _ := Sign(priv, katMessage, CtxTransaction)
	if Verify(pub, katMessage, nil, sig) {
		t.Fatal("verification with a nil context succeeded")
	}
}

// A tampered message must not verify.
func TestMLDSARejectsTamperedMessage(t *testing.T) {
	pub, priv := katKeypair(t)
	sig, err := Sign(priv, katMessage, CtxTransaction)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := append([]byte(nil), katMessage...)
	tampered[0] ^= 0xFF
	if Verify(pub, tampered, CtxTransaction, sig) {
		t.Fatal("tampered message verified")
	}
}

// ── ML-KEM-768 (FIPS 203) ────────────────────────────────────────────────────

// ML-KEM-768 parameters must match NIST FIPS 203.
func TestMLKEMParametersMatchFIPS203(t *testing.T) {
	cases := []struct {
		name      string
		got, want int
	}{
		{"encapsulation key", mlkem.EncapsulationKeySize768, 1184},
		{"ciphertext", mlkem.CiphertextSize768, 1088},
		{"shared key", mlkem.SharedKeySize, 32},
		{"seed", mlkem.SeedSize, 64},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s size = %d, want %d (ML-KEM-768 / FIPS 203)", c.name, c.got, c.want)
		}
	}
}

// Encapsulation and decapsulation must agree on the shared secret, and a
// deterministic seed must reproduce the same decapsulation key.
func TestMLKEMEncapsulateDecapsulate(t *testing.T) {
	seed := make([]byte, mlkem.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	dk, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		t.Fatalf("decapsulation key from seed: %v", err)
	}

	// The same seed must yield the same encapsulation key.
	dk2, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		t.Fatalf("second key from seed: %v", err)
	}
	if !bytes.Equal(dk.EncapsulationKey().Bytes(), dk2.EncapsulationKey().Bytes()) {
		t.Fatal("ML-KEM key generation is not deterministic for a fixed seed")
	}

	shared, ct, err := encapsulate(dk.EncapsulationKey().Bytes())
	if err != nil {
		t.Fatalf("encapsulate: %v", err)
	}
	if len(shared) != mlkem.SharedKeySize {
		t.Fatalf("shared key is %d bytes, want %d", len(shared), mlkem.SharedKeySize)
	}

	recovered, err := dk.Decapsulate(ct)
	if err != nil {
		t.Fatalf("decapsulate: %v", err)
	}
	if !bytes.Equal(shared, recovered) {
		t.Fatal("encapsulated and decapsulated shared secrets differ")
	}
}

// A corrupted ciphertext must not recover the original secret. FIPS 203 uses
// implicit rejection: decapsulation returns a pseudorandom key rather than an
// error, so the check is that the secret DIFFERS, not that it fails.
func TestMLKEMImplicitRejection(t *testing.T) {
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	shared, ct, err := encapsulate(dk.EncapsulationKey().Bytes())
	if err != nil {
		t.Fatalf("encapsulate: %v", err)
	}

	corrupted := append([]byte(nil), ct...)
	corrupted[0] ^= 0xFF

	recovered, err := dk.Decapsulate(corrupted)
	if err != nil {
		return // an explicit error is also an acceptable rejection
	}
	if bytes.Equal(shared, recovered) {
		t.Fatal("corrupted ciphertext recovered the original shared secret")
	}
}

func encapsulate(ekBytes []byte) (shared, ciphertext []byte, err error) {
	ek, err := mlkem.NewEncapsulationKey768(ekBytes)
	if err != nil {
		return nil, nil, err
	}
	shared, ciphertext = ek.Encapsulate()
	return shared, ciphertext, nil
}
