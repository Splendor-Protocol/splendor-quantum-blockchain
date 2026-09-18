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
	codebook, err := BuildCodebook(desc.DescriptorID(), symbols)
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
	if manifest.CodebookSize != uint32(codebook.Len()) {
		t.Fatalf("CodebookSize = %d, want %d", manifest.CodebookSize, codebook.Len())
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
	symbols := genSymbols(rng, 20)
	codebook, err := BuildCodebook(desc.DescriptorID(), symbols)
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
		"codebook size": mutate(func(m *Manifest) { m.CodebookSize++ }),
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
	symbols := genSymbols(rng, 20)
	codebook, err := BuildCodebook(desc.DescriptorID(), symbols)
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
	symbols := genSymbols(rng, int(desc.SourceCount))
	codebook, err := BuildCodebook(desc.DescriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignManifest(desc, codebook, priv)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Descriptor.SourceCount = 10000
	if err := manifest.Verify(1024, 1<<20); err == nil {
		t.Fatal("manifest with source count over the cap was accepted")
	}
}

// Signing needs a real codebook; a nil one is a caller bug and must error,
// not panic partway through the manifest construction.
func TestSignManifestRejectsNilCodebook(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignManifest(testDescriptor(), nil, priv); err == nil {
		t.Fatal("SignManifest accepted a nil codebook")
	}
}

// The codebook commits to symbol leaves bound to one descriptorID; signing a
// manifest whose Descriptor derives a different descriptorID would commit to a
// codebook this manifest's own proofs can never match. Refused at sign time.
func TestSignManifestRejectsMismatchedCodebook(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(5))
	codebookDesc := testDescriptor()
	symbols := genSymbols(rng, 8)
	codebook, err := BuildCodebook(codebookDesc.DescriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}

	other := testDescriptor()
	other.ObjectSize++ // different descriptorID, still a valid shape
	if _, err := SignManifest(other, codebook, priv); err == nil {
		t.Fatal("SignManifest accepted a codebook built for a different descriptor")
	}
}

// A codebook smaller than the descriptor's source count can never supply a
// full-rank generation; refusing it at sign time keeps CodebookSize honest.
func TestSignManifestRejectsUndersizedCodebook(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(8))
	desc := testDescriptor()
	symbols := genSymbols(rng, 3) // source count is 16
	codebook, err := BuildCodebook(desc.DescriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignManifest(desc, codebook, priv); err == nil {
		t.Fatal("SignManifest accepted a codebook smaller than source count")
	}
}

// The binding identifier must be the same value whether asked of the
// Descriptor or of a ratified Manifest — that single value is what symbol
// proofs are verified against, so any divergence would break proof checks.
func TestDescriptorIDMatchesManifest(t *testing.T) {
	desc := testDescriptor()
	if desc.DescriptorID() == [32]byte{} {
		t.Fatal("DescriptorID is the zero value — descriptor fields are not being bound")
	}
	m := Manifest{Descriptor: desc}
	if m.DescriptorID() != desc.DescriptorID() {
		t.Fatal("Manifest.DescriptorID differs from Descriptor.DescriptorID")
	}
	changed := desc
	changed.Anchor.Kind = "snapshot"
	if changed.DescriptorID() == desc.DescriptorID() {
		t.Fatal("a changed descriptor field did not change DescriptorID")
	}
}

// A descriptor naming a codec no receiver can resolve must be rejected at
// validation time — the exact fail-closed behaviour the Codec field documents.
func TestValidateRejectsUnknownCodec(t *testing.T) {
	desc := testDescriptor()
	desc.Codec = "rlnc-over-fancy-future-field-v99"
	if err := desc.Validate(1024, 1<<20); err == nil {
		t.Fatal("descriptor with an unresolvable codec name passed validation")
	}
	for _, name := range []string{"uncoded-v1", "erasure-rs-v1", "rlnc-gf256-v1"} {
		ok := testDescriptor()
		ok.Codec = name
		if err := ok.Validate(1024, 1<<20); err != nil {
			t.Fatalf("known codec %q rejected: %v", name, err)
		}
	}
}

// Anchor kinds are a closed set: only a kind with a chain-level check on the
// decoded bytes may pass, and the match is exact (no case folding, no
// whitespace) since Kind is part of the signed descriptor.
func TestValidateRejectsUnknownAnchorKind(t *testing.T) {
	for _, kind := range []string{"model", "bench", "Block", "snapshot "} {
		d := testDescriptor()
		d.Anchor.Kind = kind
		if err := d.Validate(1024, 1<<20); err == nil {
			t.Fatalf("anchor kind %q passed validation", kind)
		}
	}
	for _, kind := range []string{"block", "snapshot"} {
		d := testDescriptor()
		d.Anchor.Kind = kind
		if err := d.Validate(1024, 1<<20); err != nil {
			t.Fatalf("known anchor kind %q rejected: %v", kind, err)
		}
	}
}

func TestValidateRejectsOversizedKindOrCodec(t *testing.T) {
	kind := testDescriptor()
	kind.Anchor.Kind = string(make([]byte, MaxAnchorKindLen+1))
	if err := kind.Validate(1024, 1<<20); err == nil {
		t.Fatal("oversized anchor kind passed validation")
	}
	codec := testDescriptor()
	codec.Codec = "uncoded-v1" + string(make([]byte, MaxCodecNameLen))
	if err := codec.Validate(1024, 1<<20); err == nil {
		t.Fatal("oversized codec name passed validation")
	}
}

// A codebook must refuse a symbol whose index cannot be encoded on the wire:
// the leaf hash casts index through uint32, and a negative index would wrap
// instead of being distinct.
func TestBuildCodebookRejectsInvalidSymbolIndex(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	desc := testDescriptor()
	symbols := genSymbols(rng, 3)
	symbols[1].Index = -1
	if _, err := BuildCodebook(desc.DescriptorID(), symbols); err == nil {
		t.Fatal("codebook accepted a symbol with a negative index")
	}
	tooBig := genSymbols(rng, 3)
	tooBig[2].Index = 1 << 33 // requires more than 32 bits on the wire
	if _, err := BuildCodebook(desc.DescriptorID(), tooBig); err == nil {
		t.Fatal("codebook accepted a symbol index that does not fit in uint32")
	}
}
