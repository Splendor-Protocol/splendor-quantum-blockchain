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

// This is the exact counterexample the independent review reproduced against
// the shipped code: 32 sources, systematic indices 0 and 17 lost, first two
// repairs (indices 32 and 33). The pre-fix Vandermonde-evaluation rows both
// reduced to [1,13] over the two missing columns, stalling the decoder at
// rank 31 and proving that construction is NOT maximum-distance-separable.
// The Cauchy construction must supply two independent equations and recover
// all 32 source symbols.
func TestErasureRecoversAuditCounterexample(t *testing.T) {
	rng := rand.New(rand.NewSource(2026))
	const k = 32
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
	for i, s := range systematic {
		if i == 0 || i == 17 {
			continue
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb systematic %d: %v", i, err)
		}
	}
	if dec.Rank() != k-2 {
		t.Fatalf("rank after 30 systematic symbols = %d, want %d", dec.Rank(), k-2)
	}

	for _, s := range enc.Repair(2) {
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb repair %d: %v", s.Index, err)
		}
	}
	if dec.Rank() != k {
		t.Fatalf("rank after first two repairs = %d, want %d (systematic Cauchy RS must be MDS)", dec.Rank(), k)
	}
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range source {
		if !bytes.Equal(got[i], source[i]) {
			t.Fatalf("symbol %d mismatch: got %x want %x", i, got[i], source[i])
		}
	}
}

// MDS means EVERY choice of k symbols out of the k systematic + m parity
// symbols is enough to recover. Exercise it exhaustively for a small
// generation so no erasure pattern can be "lucky": for k=4 with 3 parity
// rows, every one of the C(7,4)=35 choices must decode to the source.
func TestErasureAnyKRecoverSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(2027))
	const k = 4
	const parity = 3
	source := genSourceSymbols(rng, k, 32)
	codec := NewErasureCodec()
	enc, err := codec.NewEncoder(source, 32)
	if err != nil {
		t.Fatal(err)
	}
	all := append(enc.Systematic(), enc.Repair(parity)...)
	if len(all) != k+parity {
		t.Fatalf("have %d symbols, want %d", len(all), k+parity)
	}

	for _, combo := range indexCombinations(len(all), k) {
		dec, err := codec.NewDecoder(k, 32)
		if err != nil {
			t.Fatal(err)
		}
		for _, idx := range combo {
			if _, err := dec.Absorb(all[idx]); err != nil {
				t.Fatalf("combo %v absorb %d: %v", combo, idx, err)
			}
		}
		if dec.Rank() != k {
			t.Fatalf("combo %v: rank = %d, want %d", combo, dec.Rank(), k)
		}
		got, err := dec.Decode()
		if err != nil {
			t.Fatalf("combo %v decode: %v", combo, err)
		}
		for i := range source {
			if !bytes.Equal(got[i], source[i]) {
				t.Fatalf("combo %v: symbol %d mismatch", combo, i)
			}
		}
	}
}

// The systematic Cauchy construction needs disjoint source/parity evaluation
// points within GF(256)'s 255 nonzero elements; a generation beyond that has
// no valid parity rows and must be refused rather than silently mis-encode.
func TestErasureRejectsOversizedGeneration(t *testing.T) {
	codec := NewErasureCodec()
	big := make([][]byte, maxErasureSources+1)
	for i := range big {
		big[i] = make([]byte, 16)
	}
	if _, err := codec.NewEncoder(big, 16); err == nil {
		t.Fatal("encoder accepted a generation larger than GF(256) supports")
	}
	if _, err := codec.NewDecoder(maxErasureSources+1, 16); err == nil {
		t.Fatal("decoder accepted a generation larger than GF(256) supports")
	}
}

// indexCombinations returns every k-element subset of {0..n-1}, so an MDS
// property can be checked over every erasure pattern rather than a few samples.
func indexCombinations(n, k int) [][]int {
	var out [][]int
	var cur []int
	var rec func(start int)
	rec = func(start int) {
		if len(cur) == k {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := start; i < n; i++ {
			cur = append(cur, i)
			rec(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0)
	return out
}
