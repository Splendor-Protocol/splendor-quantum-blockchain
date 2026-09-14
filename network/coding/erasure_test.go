package coding

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// The healthy case (systematic only) must decode exactly, same as RLNC.
func TestErasureSystematicOnlyDecodesExactly(t *testing.T) {
	rng := rand.New(rand.NewSource(30))
	source := genSourceSymbols(rng, 16, 64)
	codec := NewErasureCodec()

	enc, err := codec.NewEncoder(source, 64)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := codec.NewDecoder(16, 64)
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

// Losing some systematic symbols and recovering via deterministic parity
// rows must still decode exactly.
func TestErasureRecoversFromLossUsingParitySymbols(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	const k = 12
	source := genSourceSymbols(rng, k, 64)
	codec := NewErasureCodec()
	enc, err := codec.NewEncoder(source, 64)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := codec.NewDecoder(k, 64)
	if err != nil {
		t.Fatal(err)
	}

	systematic := enc.Systematic()
	lost := 0
	for i, s := range systematic {
		if i%4 == 0 {
			lost++
			continue
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range enc.Repair(lost) {
		if dec.Rank() == k {
			break
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb parity %d: %v", s.Index, err)
		}
	}
	if dec.Rank() != k {
		t.Fatalf("rank = %d, want %d (Vandermonde parity should deterministically recover exactly `lost` losses)", dec.Rank(), k)
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

// Parity rows must be genuinely distinct (different evaluation points), or
// the deterministic-recovery guarantee this codec exists to provide would
// not hold.
func TestErasureParityRowsAreDistinct(t *testing.T) {
	rng := rand.New(rand.NewSource(32))
	source := genSourceSymbols(rng, 8, 32)
	codec := NewErasureCodec()
	enc, _ := codec.NewEncoder(source, 32)
	repair := enc.Repair(5)
	seen := make(map[string]bool)
	for _, s := range repair {
		key := string(s.Coefficients)
		if seen[key] {
			t.Fatalf("duplicate parity coefficient row for symbol index %d", s.Index)
		}
		seen[key] = true
	}
}

func TestErasureEmptyGenerationRejected(t *testing.T) {
	codec := NewErasureCodec()
	if _, err := codec.NewEncoder(nil, 16); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("empty encoder = %v, want ErrEmptyGeneration", err)
	}
	if _, err := codec.NewDecoder(0, 16); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("zero-source decoder = %v, want ErrEmptyGeneration", err)
	}
}
