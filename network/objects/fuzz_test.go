package objects

import (
	"bytes"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// The SQCO wire frames arrive from untrusted peers, so their decoders must
// never panic on arbitrary input, must bound every allocation by the library
// caps, and must only accept canonical frames (an accepted frame round-trips
// through the encoder). These fuzz targets pin those properties.

// FuzzDecodeObjectManifest covers the multi-generation manifest frame.
func FuzzDecodeObjectManifest(f *testing.F) {
	f.Add([]byte{})
	f.Add(objectManifestFixture(f))
	f.Fuzz(func(t *testing.T, frame []byte) {
		m, err := DecodeObjectManifest(frame)
		if err != nil {
			return
		}
		if len(m.Generations) == 0 || len(m.Generations) > MaxGenerations {
			t.Fatalf("accepted manifest with %d generations", len(m.Generations))
		}
		re, err := EncodeObjectManifest(m)
		if err == nil && !bytes.Equal(re, frame) {
			t.Fatalf("accepted frame does not round-trip: %x -> %x", frame, re)
		}
		// Shape verification under the production limits must fail cleanly for
		// a fuzz frame (no valid ML-DSA signature) — and must never panic.
		limits := DefaultLimits()
		_ = m.Verify(limits.MaxSourceCount, limits.MaxSymbolSize, limits.MaxObjectSize)
	})
}

// FuzzDecodeSymbolProof covers the authenticated symbol proof frame.
func FuzzDecodeSymbolProof(f *testing.F) {
	f.Add([]byte{})
	p := SymbolProof{
		Symbol:         coding.Symbol{Index: 0, Coefficients: []byte{1}, Payload: []byte{2}},
		Siblings:       [][32]byte{{3}},
		SiblingIsRight: []bool{true},
	}
	if frame, err := EncodeSymbolProof(p); err == nil {
		f.Add(frame)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		p, err := DecodeSymbolProof(frame)
		if err != nil {
			return
		}
		if len(p.Symbol.Coefficients) > coding.MaxSourceSymbols ||
			len(p.Symbol.Payload) > coding.MaxSymbolSize ||
			len(p.Siblings) > MaxMerkleSiblings || p.Symbol.Index < 0 {
			t.Fatalf("accepted proof outside the library caps: %d coeffs, %d payload, %d siblings, index %d",
				len(p.Symbol.Coefficients), len(p.Symbol.Payload), len(p.Siblings), p.Symbol.Index)
		}
		re, err := EncodeSymbolProof(p)
		if err == nil && !bytes.Equal(re, frame) {
			t.Fatalf("accepted frame does not round-trip: %x -> %x", frame, re)
		}
	})
}

// FuzzDecodeManifest covers the single-generation manifest frame.
func FuzzDecodeManifest(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, frame []byte) {
		m, err := DecodeManifest(frame)
		if err != nil {
			return
		}
		re, err := EncodeManifest(m)
		if err == nil && !bytes.Equal(re, frame) {
			t.Fatalf("accepted frame does not round-trip: %x -> %x", frame, re)
		}
		limits := DefaultLimits()
		_ = m.Verify(limits.MaxSourceCount, limits.MaxSymbolSize)
	})
}

// FuzzFrameType covers the frame self-description probe used to tell
// single-generation and multi-generation manifest replies apart. An unknown
// wire type must be refused, not returned: callers switch on the result.
func FuzzFrameType(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, frame []byte) {
		typ, err := FrameType(frame)
		if err != nil {
			return
		}
		switch typ {
		case wireTypeManifest, wireTypeSymbolProof, wireTypeObjectManifest:
		default:
			t.Fatalf("FrameType accepted unknown wire type %d", typ)
		}
	})
}

// objectManifestFixture returns the canonical SQCO frame of a small, validly
// signed multi-generation object manifest, as a fuzz seed.
func objectManifestFixture(f *testing.F) []byte {
	f.Helper()
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		f.Fatal(err)
	}
	desc := ObjectDescriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{1}, Height: 1},
		Codec:      coding.NewRLNCCodec().Name(),
		SymbolSize: 64,
	}
	gens := []GenerationManifest{{SourceCount: 2, CodebookRoot: [32]byte{1}, CodebookSize: 2}}
	m, err := SignObjectManifest(desc, 64, gens, priv)
	if err != nil {
		f.Fatal(err)
	}
	frame, err := EncodeObjectManifest(m)
	if err != nil {
		f.Fatal(err)
	}
	return frame
}

// FuzzObjectReceiverAbsorb drives adversarial symbol streams against a
// receiver: arbitrary picks of (generation, index) — including replays and
// out-of-order absorbs — and occasional corrupted wire frames. The invariant
// is the reconstruction property itself: whenever the receiver reports full
// rank, Decode must reproduce the original object byte for byte.
func FuzzObjectReceiverAbsorb(f *testing.F) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		f.Fatal(err)
	}
	object := []byte("fuzz object payload 0123456789")
	desc := ObjectDescriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{4}, Height: 9},
		Codec:      coding.NewRLNCCodec().Name(),
		SymbolSize: 8,
	}
	limits := Limits{MaxSourceCount: 4, MaxSymbolSize: 8, MaxObjectSize: 1 << 20}
	send, err := NewObjectSender(object, desc, coding.NewRLNCCodec(), 2, priv, limits)
	if err != nil {
		f.Fatal(err)
	}
	authorize := AllowObjectSignerBytes(priv.Public().Bytes())

	f.Add([]byte{0, 0, 0, 0, 0, 0})       // replay of index 0 in gen 0
	f.Add([]byte{0, 0, 1, 0, 2, 0, 3, 0}) // sequential full rank, gen 0
	f.Add([]byte{0, 0, 1, 0, 0, 0, 0, 5, 3, 0, 2, 0})
	f.Fuzz(func(t *testing.T, seq []byte) {
		recv, err := NewObjectReceiver(send.Manifest(), limits, authorize)
		if err != nil {
			t.Fatalf("receiver: %v", err)
		}
		for i := 0; i+1 < len(seq); i += 2 {
			gen := int(seq[i]) % send.GenerationCount()
			idx := int(seq[i+1]) % send.SymbolCount(gen)
			proof, err := send.Proof(gen, idx)
			if err != nil {
				t.Fatalf("proof %d/%d: %v", gen, idx, err)
			}
			frame, err := EncodeSymbolProof(proof)
			if err != nil {
				t.Fatalf("encode proof: %v", err)
			}
			if seq[i]&0x40 != 0 && len(frame) > 0 {
				// Corrupt the wire frame: the wire decoder or the
				// codebook-root verification must refuse it — never panic.
				frame[int(seq[i+1])%len(frame)] ^= 0xff
			}
			p, err := DecodeSymbolProof(frame)
			if err != nil {
				continue
			}
			_, _ = recv.AbsorbProof(gen, p)
		}
		if recv.Ready() {
			got, err := recv.Decode()
			if err != nil {
				t.Fatalf("ready receiver failed to decode: %v", err)
			}
			if !bytes.Equal(got, object) {
				t.Fatalf("ready receiver decoded %d bytes, want the %d-byte original", len(got), len(object))
			}
		}
	})
}
