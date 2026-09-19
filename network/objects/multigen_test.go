package objects

import (
	"bytes"
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// Multi-generation: an object larger than one generation must round-trip through
// the signed object manifest, the SQCO wire encoding, and the per-generation
// authenticated decoder.
func TestObjectSenderReceiverMultiGenerationRoundTrip(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	// 20 source symbols at 64 bytes; capped at 8 sources per generation forces
	// three generations (8 + 8 + 4).
	object := testObject(rng, 20*64)
	desc := ObjectDescriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{7, 7, 7}, Height: 3},
		Codec:      coding.NewRLNCCodec().Name(),
		SymbolSize: 64,
	}
	limits := Limits{MaxSourceCount: 8, MaxSymbolSize: 64}

	send, err := NewObjectSender(object, desc, coding.NewRLNCCodec(), 2, priv, limits)
	if err != nil {
		t.Fatalf("NewObjectSender: %v", err)
	}
	if send.GenerationCount() != 3 {
		t.Fatalf("generation count = %d, want 3", send.GenerationCount())
	}

	frame, err := EncodeObjectManifest(send.Manifest())
	if err != nil {
		t.Fatalf("EncodeObjectManifest: %v", err)
	}
	if typ, _ := FrameType(frame); typ != wireTypeObjectManifest {
		t.Fatalf("frame type = %d, want %d", typ, wireTypeObjectManifest)
	}
	decoded, err := DecodeObjectManifest(frame)
	if err != nil {
		t.Fatalf("DecodeObjectManifest: %v", err)
	}
	if err := decoded.Verify(limits.MaxSourceCount, limits.MaxSymbolSize); err != nil {
		t.Fatalf("decoded manifest verify: %v", err)
	}

	recv, err := NewObjectReceiver(decoded, limits, AllowObjectSignerBytes(pub.Bytes()))
	if err != nil {
		t.Fatalf("NewObjectReceiver: %v", err)
	}
	for gen := 0; gen < send.GenerationCount(); gen++ {
		for i := 0; i < send.SymbolCount(gen); i++ {
			if recv.Ready() {
				break
			}
			proof, err := send.Proof(gen, i)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recv.AbsorbProof(gen, proof); err != nil {
				t.Fatalf("absorb gen %d sym %d: %v", gen, i, err)
			}
		}
	}
	got, err := recv.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, object) {
		t.Fatalf("decoded object mismatch: got %d bytes, want %d", len(got), len(object))
	}
}

// A proof absorbed into the wrong generation must not verify: generation IDs
// differ, so a symbol from generation 0 cannot be replayed as generation 1.
func TestObjectReceiverRejectsWrongGeneration(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(2))
	object := testObject(rng, 10*64)
	desc := ObjectDescriptor{Anchor: Anchor{Kind: "snapshot", ID: [32]byte{8}, Height: 4}, Codec: coding.NewRLNCCodec().Name(), SymbolSize: 64}
	limits := Limits{MaxSourceCount: 8, MaxSymbolSize: 64}
	send, err := NewObjectSender(object, desc, coding.NewRLNCCodec(), 2, priv, limits)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := NewObjectReceiver(send.Manifest(), limits, AllowObjectSignerBytes(pub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if send.GenerationCount() < 2 {
		t.Fatal("expected at least two generations")
	}
	proof, err := send.Proof(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.AbsorbProof(1, proof); err == nil {
		t.Fatal("generation-0 proof accepted as generation-1")
	}
}

// A tampered symbol proof must be rejected before it reaches the decoder.
func TestObjectReceiverRejectsTamperedSymbol(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(3))
	object := testObject(rng, 4*64)
	desc := ObjectDescriptor{Anchor: Anchor{Kind: "block", ID: [32]byte{9}, Height: 5}, Codec: coding.NewRLNCCodec().Name(), SymbolSize: 64}
	limits := Limits{MaxSourceCount: 8, MaxSymbolSize: 64}
	send, err := NewObjectSender(object, desc, coding.NewRLNCCodec(), 0, priv, limits)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := NewObjectReceiver(send.Manifest(), limits, AllowObjectSignerBytes(pub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := send.Proof(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	proof.Symbol.Payload[0] ^= 0xFF
	if _, err := recv.AbsorbProof(0, proof); err != ErrUnauthenticatedSymbol {
		t.Fatalf("tampered symbol absorbed: err = %v, want ErrUnauthenticatedSymbol", err)
	}
}

// The single-generation object manifest path also accepts a one-generation
// object, so block-sized objects use the same code path.
func TestObjectSenderSingleGeneration(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(4))
	object := testObject(rng, 3*64)
	desc := ObjectDescriptor{Anchor: Anchor{Kind: "block", ID: [32]byte{10}, Height: 6}, Codec: coding.NewRLNCCodec().Name(), SymbolSize: 64}
	limits := Limits{MaxSourceCount: 8, MaxSymbolSize: 64}
	send, err := NewObjectSender(object, desc, coding.NewRLNCCodec(), 1, priv, limits)
	if err != nil {
		t.Fatal(err)
	}
	if send.GenerationCount() != 1 {
		t.Fatalf("generation count = %d, want 1", send.GenerationCount())
	}
	recv, err := NewObjectReceiver(send.Manifest(), limits, AllowObjectSignerBytes(pub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < send.SymbolCount(0) && !recv.Ready(); i++ {
		p, err := send.Proof(0, i)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recv.AbsorbProof(0, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := recv.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, object) {
		t.Fatal("decoded object mismatch")
	}
}
