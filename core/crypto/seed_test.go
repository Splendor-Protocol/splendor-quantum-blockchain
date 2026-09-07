package crypto

import (
	"bytes"
	"testing"
)

func TestKeypairFromSeedDeterministic(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	pub1, priv1 := KeypairFromSeed(seed)
	pub2, priv2 := KeypairFromSeed(seed)
	if !bytes.Equal(pub1.Bytes(), pub2.Bytes()) {
		t.Fatal("same seed produced different public keys")
	}
	if !bytes.Equal(priv1.Bytes(), priv2.Bytes()) {
		t.Fatal("same seed produced different private keys")
	}
	addr1 := DeriveAddress(pub1)
	addr2 := DeriveAddress(pub2)
	if addr1 != addr2 {
		t.Fatalf("address mismatch: %s vs %s", addr1, addr2)
	}
}

func TestDifferentSeedsDifferentKeys(t *testing.T) {
	var a, b [SeedSize]byte
	a[0] = 1
	b[0] = 2
	pubA, _ := KeypairFromSeed(a)
	pubB, _ := KeypairFromSeed(b)
	if bytes.Equal(pubA.Bytes(), pubB.Bytes()) {
		t.Fatal("different seeds produced the same public key")
	}
}

func TestGenerateSeedLength(t *testing.T) {
	s, err := GenerateSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != SeedSize {
		t.Fatalf("seed length %d, want %d", len(s), SeedSize)
	}
}
