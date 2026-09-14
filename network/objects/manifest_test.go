package objects

import (
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func testDescriptor() Descriptor {
	return Descriptor{
		Anchor:      Anchor{Kind: "block", ID: [32]byte{1, 2, 3}, Height: 42},
		Codec:       "rlnc-gf256-v1",
		ObjectSize:  1000,
		SymbolSize:  64,
		SourceCount: 16,
	}
}

func genSymbols(rng *rand.Rand, n int) []coding.Symbol {
	out := make([]coding.Symbol, n)
	for i := range out {
		out[i] = coding.Symbol{
			Index:        i,
			Coefficients: randBytesN(rng, 16),
			Payload:      randBytesN(rng, 64),
		}
	}
	return out
}

func randBytesN(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

// A full manifest built end-to-end — descriptor, codebook, signature — must
// verify, and its signer address must resolve to the signing key's address.
func TestSignManifestEndToEndVerifies(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	desc := testDescriptor()
	symbols := genSymbols(rng, 20)
	codebook, err := BuildCodebook(desc.descriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignManifest(desc, codebook, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(1024, 1<<20); err != nil {
		t.Fatalf("manifest failed to verify: %v", err)
	}
	addr, err := manifest.VerifiedSignerAddress()
	if err != nil {
		t.Fatal(err)
	}
	if addr != qcrypto.DeriveAddress(pub) {
		t.Fatal("verified signer address does not match the actual signer")
	}
}

// Every field of the signed body must matter: flipping any one of them
// (descriptor fields, codebook root) must invalidate the signature, since
// they all feed into signedBytes via descriptorID or directly.
func TestManifestSignatureCoversEveryField(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(2))
	desc := testDescriptor()
	symbols := genSymbols(rng, 10)
	codebook, err := BuildCodebook(desc.descriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	base, err := SignManifest(desc, codebook, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Verify(1024, 1<<20); err != nil {
		t.Fatalf("base manifest should verify: %v", err)
	}

	mutate := func(f func(*Manifest)) *Manifest {
		m := *base
		f(&m)
		return &m
	}
	cases := map[string]*Manifest{
		"anchor kind":   mutate(func(m *Manifest) { m.Descriptor.Anchor.Kind = "snapshot" }),
		"anchor id":     mutate(func(m *Manifest) { m.Descriptor.Anchor.ID[0] ^= 0xff }),
		"anchor height": mutate(func(m *Manifest) { m.Descriptor.Anchor.Height++ }),
		"codec name":    mutate(func(m *Manifest) { m.Descriptor.Codec = "uncoded-v1" }),
		"object size":   mutate(func(m *Manifest) { m.Descriptor.ObjectSize++ }),
		"symbol size":   mutate(func(m *Manifest) { m.Descriptor.SymbolSize++ }),
		"source count":  mutate(func(m *Manifest) { m.Descriptor.SourceCount++ }),
		"codebook root": mutate(func(m *Manifest) { m.CodebookRoot[0] ^= 0xff }),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if err := m.Verify(1024, 1<<20); err == nil {
				t.Fatalf("mutated %s still verified — signature does not cover this field", name)
			}
		})
	}
}

// A manifest signed by one key must not verify under another signer's
// substituted public key — the classic "swap the key, keep the signature"
// forgery attempt.
func TestManifestRejectsSignerSubstitution(t *testing.T) {
	_, priv1, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(3))
	desc := testDescriptor()
	symbols := genSymbols(rng, 10)
	codebook, err := BuildCodebook(desc.descriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignManifest(desc, codebook, priv1)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SignerPubKey = pub2.Bytes() // attacker substitutes a different key
	if err := manifest.Verify(1024, 1<<20); err == nil {
		t.Fatal("manifest verified after its signer public key was substituted")
	}
}

// A descriptor whose source count or symbol size exceeds the caller's
// bounds must be rejected before any decode work — the resource-budget
// requirement from Section 8.4/9.
func TestManifestVerifyEnforcesBounds(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(4))
	desc := testDescriptor()
	desc.SourceCount = 10000
	symbols := genSymbols(rng, 5)
	codebook, err := BuildCodebook(desc.descriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignManifest(desc, codebook, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(1024, 1<<20); err == nil {
		t.Fatal("manifest with source count over the cap was accepted")
	}
}
