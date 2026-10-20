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
}

func TestSLHPublicKeyRoundTrip(t *testing.T) {
	pub, _, err := GenerateSLHKeypair()
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
}
