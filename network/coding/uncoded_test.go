package coding

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// The uncoded baseline decodes correctly when every symbol is delivered.
func TestUncodedDecodesWhenComplete(t *testing.T) {
	rng := rand.New(rand.NewSource(20))
	source := genSourceSymbols(rng, 10, 40)
	codec := NewUncodedCodec()

	enc, err := codec.NewEncoder(source, 40)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := codec.NewDecoder(10, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range enc.Systematic() {
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb %d: %v", s.Index, err)
		}
	}
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range source {
		if !bytes.Equal(got[i], source[i]) {
			t.Fatalf("symbol %d mismatch", i)
		}
	}
}

// Uncoded has no repair concept — Repair must return nothing, and losing
// even one symbol must make Decode fail (there is no alternate combination
// to recover it from, unlike RLNC).
func TestUncodedHasNoRepairAndFailsOnLoss(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	source := genSourceSymbols(rng, 6, 16)
	codec := NewUncodedCodec()
	enc, _ := codec.NewEncoder(source, 16)

	if r := enc.Repair(5); len(r) != 0 {
		t.Fatalf("uncoded Repair returned %d symbols, want 0", len(r))
	}

	dec, _ := codec.NewDecoder(6, 16)
	systematic := enc.Systematic()
	for i, s := range systematic {
		if i == 2 {
			continue // simulate losing symbol 2
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dec.Decode(); !errors.Is(err, ErrNotEnoughSymbols) {
		t.Fatalf("decode with one symbol missing = %v, want ErrNotEnoughSymbols", err)
	}
}

// A coded symbol (non-empty Coefficients) handed to the uncoded decoder must
// be rejected outright — the two codecs' wire symbols must never be
// silently cross-accepted.
func TestUncodedRejectsCodedSymbol(t *testing.T) {
	codec := NewUncodedCodec()
	dec, _ := codec.NewDecoder(4, 8)
	if _, err := dec.Absorb(Symbol{Index: 0, Coefficients: make([]byte, 4), Payload: make([]byte, 8)}); err == nil {
		t.Fatal("uncoded decoder accepted a symbol carrying coefficients")
	}
}

func TestUncodedRejectsMalformedSymbols(t *testing.T) {
	codec := NewUncodedCodec()
	dec, _ := codec.NewDecoder(4, 16)
	if _, err := dec.Absorb(Symbol{Index: 0, Payload: make([]byte, 8)}); !errors.Is(err, ErrSymbolSizeMismatch) {
		t.Fatalf("wrong payload size = %v, want ErrSymbolSizeMismatch", err)
	}
	if _, err := dec.Absorb(Symbol{Index: 99, Payload: make([]byte, 16)}); err == nil {
		t.Fatal("out-of-range index accepted")
	}
}

// Absorbing the same index twice must not be double-counted as rank.
func TestUncodedDuplicateIndexNotDoubleCounted(t *testing.T) {
	codec := NewUncodedCodec()
	dec, _ := codec.NewDecoder(3, 8)
	s := Symbol{Index: 0, Payload: make([]byte, 8)}
	if _, err := dec.Absorb(s); err != nil {
		t.Fatal(err)
	}
	increased, err := dec.Absorb(s)
	if err != nil {
		t.Fatal(err)
	}
	if increased {
		t.Fatal("re-absorbing the same index increased rank")
	}
	if dec.Rank() != 1 {
		t.Fatalf("rank = %d, want 1", dec.Rank())
	}
}

func TestUncodedEmptyGenerationRejected(t *testing.T) {
	codec := NewUncodedCodec()
	if _, err := codec.NewEncoder(nil, 8); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("empty encoder = %v, want ErrEmptyGeneration", err)
	}
	if _, err := codec.NewDecoder(0, 8); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("zero-source decoder = %v, want ErrEmptyGeneration", err)
	}
}
