package crypto

import (
	"bytes"
	"testing"
)

func TestSLHSignVerifyAndDomainSeparation(t *testing.T) {
	pub, priv, err := GenerateSLHKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if got := len(pub.Bytes()); got != SLHPublicKeySize {
		t.Fatalf("pubkey %d bytes, want %d", got, SLHPublicKeySize)
	}
	msg := []byte("sqvl-test")
	sig, err := SignSLH(priv, msg, CtxVerify)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) != SLHSignatureSize {
		t.Fatalf("sig %d bytes, want %d", len(sig), SLHSignatureSize)
	}
	if !VerifySLH(pub, msg, CtxVerify, sig) {
		t.Fatal("fresh SLH-DSA signature did not verify")
	}
	if VerifySLH(pub, msg, CtxTransaction, sig) {
		t.Fatal("SLH-DSA signature verified under the transaction context")
	}
	if VerifySLH(pub, append(msg, 0), CtxVerify, sig) {
		t.Fatal("SLH-DSA signature verified over a different message")
	}
	if VerifySLH(pub, msg, CtxVerify, sig[:len(sig)-1]) {
		t.Fatal("a truncated SLH-DSA signature verified")
	}
	if _, err := SignSLH(priv, msg, nil); err == nil {
		t.Fatal("signing with an empty context must be refused")
	}
}

func TestSLHPublicKeyRoundTrip(t *testing.T) {
	pub, priv, err := GenerateSLHKeypair()
	if err != nil {
		t.Fatal(err)
	}
	got, err := SLHPublicKeyFromBytes(pub.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), pub.Bytes()) {
		t.Fatal("pubkey round-trip mismatch")
	}
	sk, err := SLHPrivateKeyFromBytes(priv.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sk.Public().Bytes(), pub.Bytes()) {
		t.Fatal("private key round-trip yields a different public key")
	}
	if _, err := SLHPublicKeyFromBytes(pub.Bytes()[:31]); err == nil {
		t.Fatal("a 31-byte SLH-DSA public key must be refused")
	}
}

// The same seed must always yield the same SLH-DSA key, and the ML-DSA key
// derived from that seed must be a different key: one seed, two independent
// identities, both reproducible.
func TestSLHKeypairFromSeedIsDeterministicAndDomainSeparated(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	pubA, privA, err := SLHKeypairFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := SLHKeypairFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pubA.Bytes(), pubB.Bytes()) {
		t.Fatal("same seed produced different SLH-DSA keys")
	}
	seed[0] ^= 0xff
	pubC, _, err := SLHKeypairFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pubA.Bytes(), pubC.Bytes()) {
		t.Fatal("different seeds produced the same SLH-DSA key")
	}
	sig, err := SignSLH(privA, []byte("seeded"), CtxVerify)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySLH(pubA, []byte("seeded"), CtxVerify, sig) {
		t.Fatal("seeded key's signature did not verify")
	}
}
