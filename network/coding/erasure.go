package coding

import "fmt"

// ErasureCodec is the "conventional erasure code" comparator the plan's
// benchmark gate (Section 9) requires RLNC to be measured against, not just
// against uncoded transfer. Unlike RLNC's randomly generated repair
// symbols, this codec's parity rows are FIXED (a systematic Cauchy matrix
// over GF(256)): the same repair symbol index always carries the same
// linear combination for a given source count. This is the defining
// difference from RLNC worth measuring — deterministic, systematic parity
// (a maximum-distance-separable Reed-Solomon code) versus randomized coding.
//
// Decoding reuses exactly the same Gaussian-elimination absorb/decode
// machinery as RLNC (rlncDecoder), because "given enough independent
// equations, solve the linear system" is identical in both cases — the only
// difference worth benchmarking is how the coefficient rows are generated,
// which is exactly what ErasureCodec.NewEncoder's Repair does differently.
type ErasureCodec struct{}

// NewErasureCodec returns the deterministic-parity comparator codec.
func NewErasureCodec() *ErasureCodec { return &ErasureCodec{} }

// maxErasureSources bounds a generation so the systematic Cauchy construction
// always has room for its source evaluation points (X = {1..k}) plus at least
// one distinct parity evaluation point (Y = {k+1..}) within GF(256)'s 255
// nonzero elements, keeping X and Y disjoint as the Cauchy MDS argument
// requires. k+n <= 255, so k <= 254 guarantees at least one repair is possible.
const maxErasureSources = 254

func (ErasureCodec) Name() string { return "erasure-rs-v1" }

func (ErasureCodec) NewEncoder(sourceSymbols [][]byte, symbolSize int) (Encoder, error) {
	if len(sourceSymbols) == 0 {
		return nil, ErrEmptyGeneration
	}
	if len(sourceSymbols) > maxErasureSources {
		return nil, fmt.Errorf("coding: erasure generation of %d sources exceeds the %d-source GF(256) limit",
			len(sourceSymbols), maxErasureSources)
	}
	for i, s := range sourceSymbols {
		if len(s) != symbolSize {
			return nil, fmt.Errorf("%w: source symbol %d is %d bytes, want %d",
				ErrSymbolSizeMismatch, i, len(s), symbolSize)
		}
	}
	cp := make([][]byte, len(sourceSymbols))
	for i, s := range sourceSymbols {
		cp[i] = append([]byte(nil), s...)
	}
	return &erasureEncoder{source: cp, symbolSize: symbolSize}, nil
}

func (ErasureCodec) NewDecoder(sourceCount, symbolSize int) (Decoder, error) {
	if sourceCount <= 0 {
		return nil, ErrEmptyGeneration
	}
	if sourceCount > maxErasureSources {
		return nil, fmt.Errorf("coding: erasure generation of %d sources exceeds the %d-source GF(256) limit",
			sourceCount, maxErasureSources)
	}
	// The decoder is identical to RLNC's: absorb reduces any linearly
	// independent row, whatever generated it.
	return &rlncDecoder{
		sourceCount: sourceCount,
		symbolSize:  symbolSize,
		rows:        make([][]byte, 0, sourceCount),
		payloads:    make([][]byte, 0, sourceCount),
	}, nil
}

type erasureEncoder struct {
	source     [][]byte
	symbolSize int
}

func (e *erasureEncoder) SourceCount() int { return len(e.source) }
func (e *erasureEncoder) SymbolSize() int  { return e.symbolSize }

func (e *erasureEncoder) Systematic() []Symbol {
	out := make([]Symbol, len(e.source))
	for i, s := range e.source {
		row := make([]byte, len(e.source))
		row[i] = 1
		out[i] = Symbol{Index: i, Coefficients: row, Payload: append([]byte(nil), s...)}
	}
	return out
}

// Repair generates n deterministic parity rows from a systematic Cauchy
// Reed-Solomon construction. The generator matrix is [I_k ; C], where C's
// row r / column i entry is 1/(x_i + y_r) with x_i = i+1 (the k source
// columns) and y_r = k+1+r (the parity rows): two disjoint sets of distinct
// nonzero field elements, valid while k+n <= 255 (addition is XOR in
// GF(256), and disjointness is what guarantees no denominator is zero).
//
// Why Cauchy, and not the earlier raw Vandermonde evaluation rows: a Cauchy
// matrix is MDS — every square submatrix is invertible — so [I_k ; C] has
// the property that ANY k of its k+n rows are linearly independent. A
// Vandermonde-evaluation matrix only keeps that property when the selected
// rows are ALL evaluation rows; mixing systematic identity rows with a few
// evaluation rows loses it, and the concrete counterexample (k=32, losing
// sources 0 and 17, the first two repairs both reducing to [1,13] -> rank
// 31) proved the old construction was not MDS. This restores the
// deterministic "any k of n recover k" guarantee this codec represents.
func (e *erasureEncoder) Repair(n int) []Symbol {
	out := make([]Symbol, 0, n)
	k := len(e.source)
	for r := 0; r < n; r++ {
		y := byte(k + 1 + r) // y_r: nonzero, and > all x_i while k+n <= 255
		coeffs := make([]byte, k)
		for i := 0; i < k; i++ {
			// 1/(x_i + y_r); x_i != y_r by construction, so denom is nonzero.
			coeffs[i] = gfInvNonzero(byte(i+1) ^ y)
		}
		payload := make([]byte, e.symbolSize)
		for i, c := range coeffs {
			gfAddScaledVec(payload, c, e.source[i])
		}
		out = append(out, Symbol{Index: k + r, Coefficients: coeffs, Payload: payload})
	}
	return out
}
