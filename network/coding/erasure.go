package coding

import "fmt"

// ErasureCodec is the "conventional erasure code" comparator the plan's
// benchmark gate (Section 9) requires RLNC to be measured against, not just
// against uncoded transfer. Unlike RLNC's randomly generated repair
// symbols, this codec's parity rows are FIXED (a Vandermonde-style matrix
// over GF(256)): the same repair symbol index always carries the same
// linear combination for a given source count. This is the defining
// difference from RLNC worth measuring — deterministic, systematic parity
// (classic Reed-Solomon-style erasure coding) versus randomized coding.
//
// Decoding reuses exactly the same Gaussian-elimination absorb/decode
// machinery as RLNC (rlncDecoder), because "given enough independent
// equations, solve the linear system" is identical in both cases — the only
// difference worth benchmarking is how the coefficient rows are generated,
// which is exactly what ErasureCodec.NewEncoder's Repair does differently.
type ErasureCodec struct{}

// NewErasureCodec returns the deterministic-parity comparator codec.
func NewErasureCodec() *ErasureCodec { return &ErasureCodec{} }

func (ErasureCodec) Name() string { return "erasure-vandermonde-v1" }

func (ErasureCodec) NewEncoder(sourceSymbols [][]byte, symbolSize int) (Encoder, error) {
	if len(sourceSymbols) == 0 {
		return nil, ErrEmptyGeneration
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

// Repair generates n deterministic Vandermonde parity rows: row r's
// coefficient for source symbol i is x_r^i in GF(256), where x_r is a
// distinct nonzero field element per repair index. This is the classical
// systematic Reed-Solomon parity construction: any k of the n+k systematic-
// plus-parity symbols recover the k source symbols, PROVIDED the resulting
// coefficient matrix is invertible — Vandermonde matrices with distinct
// evaluation points guarantee that over a field this size for the
// generation sizes this codec targets (a stronger, deterministic guarantee
// RLNC's random coefficients only hold with high, not certain, probability).
func (e *erasureEncoder) Repair(n int) []Symbol {
	out := make([]Symbol, 0, n)
	k := len(e.source)
	for r := 0; r < n; r++ {
		// Distinct nonzero evaluation point per repair row: k+r+1, wrapped
		// into GF(256)'s nonzero range (1..255). Collisions beyond 255
		// repair rows are a known, documented limit of GF(256)-based
		// Vandermonde codes, not a bug — a real deployment would size
		// generations well under this.
		x := byte((k+r)%255 + 1)
		coeffs := make([]byte, k)
		coeffs[0] = 1 // x^0 = 1
		for i := 1; i < k; i++ {
			coeffs[i] = gfMul(coeffs[i-1], x)
		}
		payload := make([]byte, e.symbolSize)
		for i, c := range coeffs {
			gfAddScaledVec(payload, c, e.source[i])
		}
		out = append(out, Symbol{Index: k + r, Coefficients: coeffs, Payload: payload})
	}
	return out
}
