package coding

import "fmt"

// UncodedCodec is the "no coding at all" baseline: every source symbol is
// sent as itself, no repair symbols exist, and a lost symbol can only be
// recovered by re-requesting it by index. This is comparator #2 in the
// plan's benchmark gate (Section 9) — RLNC only ships if it measurably beats
// this, not because coding sounds better in principle.
type UncodedCodec struct{}

// NewUncodedCodec returns the uncoded baseline codec.
func NewUncodedCodec() *UncodedCodec { return &UncodedCodec{} }

func (UncodedCodec) Name() string { return "uncoded-v1" }

func (UncodedCodec) NewEncoder(sourceSymbols [][]byte, symbolSize int) (Encoder, error) {
	if err := validateGenerationShape(len(sourceSymbols), symbolSize); err != nil {
		return nil, err
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
	return &uncodedEncoder{source: cp, symbolSize: symbolSize}, nil
}

func (UncodedCodec) NewDecoder(sourceCount, symbolSize int) (Decoder, error) {
	if err := validateGenerationShape(sourceCount, symbolSize); err != nil {
		return nil, err
	}
	return &uncodedDecoder{
		sourceCount: sourceCount,
		symbolSize:  symbolSize,
		have:        make([][]byte, sourceCount),
	}, nil
}

type uncodedEncoder struct {
	source     [][]byte
	symbolSize int
}

func (e *uncodedEncoder) SourceCount() int { return len(e.source) }
func (e *uncodedEncoder) SymbolSize() int  { return e.symbolSize }

func (e *uncodedEncoder) Systematic() []Symbol {
	out := make([]Symbol, len(e.source))
	for i, s := range e.source {
		out[i] = Symbol{Index: i, Payload: append([]byte(nil), s...)} // no coefficients: uncoded
	}
	return out
}

// Repair has nothing to offer: there is no coding, so there is no new
// combination to send. A caller needing recovery must re-request specific
// missing indices from Systematic() (or from an alternate peer).
func (e *uncodedEncoder) Repair(int) ([]Symbol, error) { return nil, nil }

type uncodedDecoder struct {
	sourceCount int
	symbolSize  int
	have        [][]byte
	rank        int
}

func (d *uncodedDecoder) SourceCount() int { return d.sourceCount }
func (d *uncodedDecoder) Rank() int        { return d.rank }

func (d *uncodedDecoder) Absorb(s Symbol) (bool, error) {
	if len(s.Payload) != d.symbolSize {
		return false, fmt.Errorf("%w: got %d bytes, want %d", ErrSymbolSizeMismatch, len(s.Payload), d.symbolSize)
	}
	if len(s.Coefficients) != 0 {
		return false, fmt.Errorf("coding: uncoded decoder received a coded symbol (has coefficients)")
	}
	if s.Index < 0 || s.Index >= d.sourceCount {
		return false, fmt.Errorf("coding: symbol index %d out of range [0,%d)", s.Index, d.sourceCount)
	}
	if d.have[s.Index] != nil {
		return false, nil // duplicate of an index already held: not useful, not an error
	}
	d.have[s.Index] = append([]byte(nil), s.Payload...)
	d.rank++
	return true, nil
}

func (d *uncodedDecoder) Decode() ([][]byte, error) {
	if d.rank < d.sourceCount {
		return nil, fmt.Errorf("%w: have %d/%d symbols", ErrNotEnoughSymbols, d.rank, d.sourceCount)
	}
	out := make([][]byte, d.sourceCount)
	copy(out, d.have)
	return out, nil
}
