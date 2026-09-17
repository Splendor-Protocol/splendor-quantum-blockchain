package coding

import (
	"errors"
	"fmt"
)

var (
	// ErrNotEnoughSymbols means a decoder was asked to reconstruct a
	// generation before it held enough independent (full-rank) symbols.
	// Collecting a certain NUMBER of coded symbols is not, by itself, a
	// guarantee of decodability — the symbols must be linearly independent.
	ErrNotEnoughSymbols = errors.New("coding: not enough independent symbols to decode this generation")
	// ErrSymbolSizeMismatch means a symbol's payload length does not match
	// the generation's fixed symbol size.
	ErrSymbolSizeMismatch = errors.New("coding: symbol payload size does not match generation symbol size")
	// ErrEmptyGeneration means an encoder was asked to encode zero source
	// symbols, which has no well-defined coded output.
	ErrEmptyGeneration = errors.New("coding: generation has no source symbols")
	// ErrDuplicateSymbol means a decoder was handed a symbol whose
	// coefficient vector is already a linear combination of rows already
	// absorbed (rank did not increase) — not useful, and MUST NOT be
	// silently accepted as progress.
	ErrDuplicateSymbol = errors.New("coding: symbol did not increase decoder rank (dependent or duplicate)")
)

// Symbol is one coded (or, for the uncoded baseline, systematic) unit of a
// generation. Coefficients is empty for genuinely uncoded chunks; RLNC
// symbols carry one coefficient per source symbol in the generation.
//
// This type carries no authentication of its own — see network/objects for
// the manifest layer that binds a symbol to a Merkle-committed, ML-DSA-65
// signed descriptor BEFORE it is ever handed to a Decoder. A codec here only
// ever answers "does this set of bytes decode correctly", never "should this
// data be trusted".
type Symbol struct {
	// Index identifies the symbol within its generation. For systematic
	// symbols (the first SourceCount symbols of a generation) Index equals
	// the source symbol's position; for repair symbols Index is
	// >= SourceCount.
	Index int
	// Coefficients is the linear-combination row over GF(256): one byte per
	// source symbol. Empty for uncoded symbols, where Payload IS source
	// symbol Index verbatim (Coefficients would be the identity row, which
	// costs bytes to send for no benefit).
	Coefficients []byte
	// Payload is the symbol's data, always SymbolSize bytes (the last
	// source symbol of an object is zero-padded to fit; the codec never
	// exposes an SymbolSize the object it authenticates does not agree on).
	Payload []byte
}

// Encoder turns a generation's fixed-size source symbols into a stream of
// output symbols a Decoder can reconstruct the generation from.
type Encoder interface {
	// SourceCount returns the number of source symbols in the generation
	// this encoder was built for.
	SourceCount() int
	// SymbolSize returns the fixed payload length of every symbol.
	SymbolSize() int
	// Systematic returns the SourceCount uncoded source symbols first (with
	// codec-appropriate Coefficients, e.g. an identity row for RLNC so a
	// receiver decodes them as normal rows), the "healthy case" per the
	// plan: a receiver with a clean channel needs no repair symbols at all.
	Systematic() []Symbol
	// Repair returns up to n freshly generated coded symbols beyond the
	// systematic set. For RLNC these are new random linear combinations;
	// for the uncoded baseline this returns nothing (there is no repair
	// concept without coding — a lost chunk must be re-requested).
	Repair(n int) []Symbol
}

// Decoder absorbs symbols until it holds a full-rank generation, then
// reconstructs the original source symbols in order.
type Decoder interface {
	// SourceCount returns the number of source symbols this decoder needs
	// to reach full rank.
	SourceCount() int
	// Rank returns how many linearly independent symbols have been
	// absorbed so far. Decode only succeeds once Rank() == SourceCount().
	Rank() int
	// Absorb feeds one symbol into the decoder's working matrix. Returns
	// true if the symbol increased rank (was useful), false if it was
	// linearly dependent on symbols already held (ErrDuplicateSymbol is
	// NOT returned here — a dependent symbol during normal RLNC reception
	// is expected and not an error condition by itself; only Decode()
	// before full rank is).
	//
	// Absorb validates Payload/Coefficients length against SymbolSize and
	// SourceCount and returns an error for a malformed symbol BEFORE any
	// matrix work — a decoder must reject garbage cheaply, since a hostile
	// or buggy source can otherwise force unbounded row-reduction work per
	// bad symbol (see the plan's "corrupt payload/proof/coefficients:
	// rejection before expensive decoding" benchmark scenario).
	Absorb(s Symbol) (increasedRank bool, err error)
	// Decode reconstructs the SourceCount source symbols, in order, once
	// full rank has been reached. Returns ErrNotEnoughSymbols otherwise.
	Decode() ([][]byte, error)
}

// Codec constructs a matched Encoder/Decoder pair for one generation. Codecs
// are compared against each other by cmd/netbench, never assumed superior —
// per the plan, RLNC ships only if benchmarks demonstrate it, and the
// uncoded and erasure-code codecs exist specifically to keep that comparison
// honest.
type Codec interface {
	// Name identifies the codec for benchmark reporting and manifest
	// negotiation (e.g. "uncoded-v1", "rlnc-gf256-v1", "erasure-rs-v1").
	Name() string
	// NewEncoder builds an Encoder for the given source symbols, all of
	// which must already be exactly symbolSize bytes (callers pad the last
	// real symbol before calling this — padding is a framing concern of
	// the object layer, not the codec's).
	NewEncoder(sourceSymbols [][]byte, symbolSize int) (Encoder, error)
	// NewDecoder builds an empty Decoder for a generation of the given
	// shape, ready to Absorb symbols.
	NewDecoder(sourceCount, symbolSize int) (Decoder, error)
}

// Resolve maps a canonical codec name (a Codec.Name() value) to the codec a
// receiver must use. This is the negotiation primitive for the object layer:
// a manifest names a codec by string, and this is the single place that maps
// that string to the codec — a receiver NEVER infers a codec from an unknown
// name. Unknown names fail closed so a future/foreign codec cannot be
// silently mis-decoded.
func Resolve(name string) (Codec, error) {
	switch name {
	case NewUncodedCodec().Name():
		return NewUncodedCodec(), nil
	case NewErasureCodec().Name():
		return NewErasureCodec(), nil
	case NewRLNCCodec().Name():
		return NewRLNCCodec(), nil
	default:
		return nil, fmt.Errorf("coding: unrecognised codec name %q", name)
	}
}
