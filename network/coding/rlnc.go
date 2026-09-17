package coding

import (
	"crypto/rand"
	"fmt"
	"io"
)

// RLNCCodec is a bounded-generation, systematic-first RLNC codec over
// GF(256). "Bounded generation" and "systematic-first" both matter for the
// reasons Section 8.1/8.4 of the improvement plan gives: an unbounded
// generation makes decode cost and memory unbounded, and systematic symbols
// mean the healthy case (no losses) needs zero coding or decoding work at
// all — a receiver that gets every systematic symbol just has its object
// back, no Gaussian elimination required.
type RLNCCodec struct {
	// rnd is the coefficient randomness source; nil uses crypto/rand. A
	// benchmark or test can inject a deterministic reader (see
	// NewRLNCCodecWithReader) to make runs reproducible; production code
	// leaves it nil so repair coefficients come from crypto/rand.
	rnd io.Reader
}

// NewRLNCCodec returns the GF(256) RLNC codec with crypto/rand coefficients.
func NewRLNCCodec() *RLNCCodec { return &RLNCCodec{} }

// NewRLNCCodecWithReader returns an RLNC codec whose repair coefficients are
// drawn from r instead of crypto/rand. This is for benchmarks and tests that
// need reproducibility; it must never be used on a live path, where repair
// coefficients are security-sensitive and must be unpredictable.
func NewRLNCCodecWithReader(r io.Reader) *RLNCCodec { return &RLNCCodec{rnd: r} }

func (c RLNCCodec) Name() string { return "rlnc-gf256-v1" }

func (c RLNCCodec) NewEncoder(sourceSymbols [][]byte, symbolSize int) (Encoder, error) {
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
	return &rlncEncoder{source: cp, symbolSize: symbolSize, rnd: c.rnd}, nil
}

func (RLNCCodec) NewDecoder(sourceCount, symbolSize int) (Decoder, error) {
	if sourceCount <= 0 {
		return nil, ErrEmptyGeneration
	}
	return &rlncDecoder{
		sourceCount: sourceCount,
		symbolSize:  symbolSize,
		rows:        make([][]byte, 0, sourceCount),
		payloads:    make([][]byte, 0, sourceCount),
	}, nil
}

type rlncEncoder struct {
	source     [][]byte
	symbolSize int
	rnd        io.Reader
}

func (e *rlncEncoder) SourceCount() int { return len(e.source) }
func (e *rlncEncoder) SymbolSize() int  { return e.symbolSize }

// Systematic returns the source symbols verbatim with identity-row
// coefficients, so a decoder absorbs them exactly like any other RLNC
// symbol (no special-casing needed on the receive side) while costing the
// sender zero encoding work.
func (e *rlncEncoder) Systematic() []Symbol {
	out := make([]Symbol, len(e.source))
	for i, s := range e.source {
		row := make([]byte, len(e.source))
		row[i] = 1
		out[i] = Symbol{Index: i, Coefficients: row, Payload: append([]byte(nil), s...)}
	}
	return out
}

// Repair generates n new random linear combinations of every source symbol.
// Each call's symbols are freshly randomized: a repair symbol is never
// reused verbatim, which is what makes requesting "one more" symbol after a
// loss useful rather than a repeat of something already seen.
func (e *rlncEncoder) Repair(n int) []Symbol {
	out := make([]Symbol, 0, n)
	k := len(e.source)
	src := e.rnd
	if src == nil {
		src = rand.Reader
	}
	for r := 0; r < n; r++ {
		coeffs := make([]byte, k)
		if _, err := src.Read(coeffs); err != nil {
			// A failed coefficient read is a fatal platform condition; the
			// plan requires bounded, predictable failure rather than silently
			// emitting a zero (all-zero-coefficient, i.e. useless) symbol.
			panic(fmt.Sprintf("coding: rlnc repair symbol generation: %v", err))
		}
		payload := make([]byte, e.symbolSize)
		for i, c := range coeffs {
			if c == 0 {
				continue
			}
			gfAddScaledVec(payload, c, e.source[i])
		}
		out = append(out, Symbol{Index: k + r, Coefficients: coeffs, Payload: payload})
	}
	return out
}
