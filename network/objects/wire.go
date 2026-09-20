package objects

import (
	"encoding/binary"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// SQCO is the four-byte magic of a coded-object frame ("Splendor Quantum
// Coded Object"). It lets a receiver reject JSON or leftover snapshot bytes
// before parsing any length prefix.
var sqcoMagic = [4]byte{'S', 'Q', 'C', 'O'}

const (
	wireTypeManifest       byte = 1
	wireTypeSymbolProof    byte = 2
	wireTypeObjectManifest byte = 3
)

var (
	// ErrUnknownWireVersion means the frame's version is not WireVersion.
	ErrUnknownWireVersion = errors.New("objects: unknown SQCO wire version")
	// ErrUnknownWireType means the frame is not a manifest or symbol proof.
	ErrUnknownWireType = errors.New("objects: unknown SQCO wire type")
	// ErrTruncatedFrame means a length prefix ran past the remaining bytes.
	ErrTruncatedFrame = errors.New("objects: truncated SQCO frame")
	// ErrTrailingBytes means a decoder did not consume the whole frame —
	// canonical decode refuses leftover so two encodings cannot alias.
	ErrTrailingBytes = errors.New("objects: trailing bytes after SQCO frame")
)

// EncodeManifest writes a versioned SQCO frame for m. The encoding is
// canonical: the same Manifest always yields the same bytes, and DecodeManifest
// of those bytes reconstructs an equal value. Length prefixes are bounded by
// the constants in this package and by network/coding's generation caps.
func EncodeManifest(m *Manifest) ([]byte, error) {
	if m == nil {
		return nil, errors.New("objects: nil manifest")
	}
	var w writer
	w.magic()
	w.u8(WireVersion)
	w.u8(wireTypeManifest)
	// Chain id leads the frame for the same reason it leads the descriptor
	// preimage: it is the outermost scope of the object's identity, and a
	// receiver should be able to reject another tier's frame before it has
	// parsed anything it would have to trust.
	if err := w.str16(m.Descriptor.Anchor.ChainID, MaxChainIDLen, "anchor chain id"); err != nil {
		return nil, err
	}
	if err := w.str16(m.Descriptor.Anchor.Kind, MaxAnchorKindLen, "anchor kind"); err != nil {
		return nil, err
	}
	w.bytes32(m.Descriptor.Anchor.ID)
	w.u64(m.Descriptor.Anchor.Height)
	if err := w.str16(m.Descriptor.Codec, MaxCodecNameLen, "codec name"); err != nil {
		return nil, err
	}
	w.u64(m.Descriptor.ObjectSize)
	w.u32(m.Descriptor.SymbolSize)
	w.u32(m.Descriptor.SourceCount)
	w.bytes32(m.CodebookRoot)
	w.u32(m.CodebookSize)
	if err := w.blob16(m.SignerPubKey, qcrypto.PublicKeySize, "signer public key"); err != nil {
		return nil, err
	}
	if err := w.blob16(m.Signature, qcrypto.SignatureSize, "signature"); err != nil {
		return nil, err
	}
	return w.b, nil
}

// DecodeManifest parses a frame produced by EncodeManifest. Oversized length
// prefixes fail before the claimed buffer is allocated.
func DecodeManifest(frame []byte) (*Manifest, error) {
	r := newReader(frame)
	if err := r.requireMagic(); err != nil {
		return nil, err
	}
	ver, err := r.u8()
	if err != nil {
		return nil, err
	}
	if ver != WireVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownWireVersion, ver)
	}
	typ, err := r.u8()
	if err != nil {
		return nil, err
	}
	if typ != wireTypeManifest {
		return nil, fmt.Errorf("%w: %d (want manifest)", ErrUnknownWireType, typ)
	}
	chainID, err := r.str16(MaxChainIDLen, "anchor chain id")
	if err != nil {
		return nil, err
	}
	kind, err := r.str16(MaxAnchorKindLen, "anchor kind")
	if err != nil {
		return nil, err
	}
	id, err := r.bytes32()
	if err != nil {
		return nil, err
	}
	height, err := r.u64()
	if err != nil {
		return nil, err
	}
	codec, err := r.str16(MaxCodecNameLen, "codec name")
	if err != nil {
		return nil, err
	}
	objectSize, err := r.u64()
	if err != nil {
		return nil, err
	}
	symbolSize, err := r.u32()
	if err != nil {
		return nil, err
	}
	sourceCount, err := r.u32()
	if err != nil {
		return nil, err
	}
	root, err := r.bytes32()
	if err != nil {
		return nil, err
	}
	codebookSize, err := r.u32()
	if err != nil {
		return nil, err
	}
	pub, err := r.blob16(qcrypto.PublicKeySize, "signer public key")
	if err != nil {
		return nil, err
	}
	sig, err := r.blob16(qcrypto.SignatureSize, "signature")
	if err != nil {
		return nil, err
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return &Manifest{
		Descriptor: Descriptor{
			Anchor:      Anchor{ChainID: chainID, Kind: kind, ID: id, Height: height},
			Codec:       codec,
			ObjectSize:  objectSize,
			SymbolSize:  symbolSize,
			SourceCount: sourceCount,
		},
		CodebookRoot: root,
		CodebookSize: codebookSize,
		SignerPubKey: pub,
		Signature:    sig,
	}, nil
}

// EncodeSymbolProof writes a versioned SQCO frame for one authenticated
// symbol. SiblingIsRight is packed as 0/1 bytes; any other value is refused
// on decode rather than coerced.
func EncodeSymbolProof(p SymbolProof) ([]byte, error) {
	if len(p.Siblings) != len(p.SiblingIsRight) {
		return nil, errors.New("objects: symbol proof sibling vectors have different lengths")
	}
	if len(p.Siblings) > MaxMerkleSiblings {
		return nil, fmt.Errorf("objects: symbol proof has %d siblings, max %d", len(p.Siblings), MaxMerkleSiblings)
	}
	if !validSymbolIndex(p.Symbol.Index) {
		return nil, fmt.Errorf("objects: symbol index %d is out of the uint32 wire range", p.Symbol.Index)
	}
	var w writer
	w.magic()
	w.u8(WireVersion)
	w.u8(wireTypeSymbolProof)
	w.u32(uint32(p.Symbol.Index))
	if err := w.blob32(p.Symbol.Coefficients, coding.MaxSourceSymbols, "coefficients"); err != nil {
		return nil, err
	}
	if err := w.blob32(p.Symbol.Payload, coding.MaxSymbolSize, "payload"); err != nil {
		return nil, err
	}
	w.u16(uint16(len(p.Siblings)))
	for i, sib := range p.Siblings {
		w.bytes32(sib)
		if p.SiblingIsRight[i] {
			w.u8(1)
		} else {
			w.u8(0)
		}
	}
	return w.b, nil
}

// DecodeSymbolProof parses a frame produced by EncodeSymbolProof. Claimed
// coefficient, payload, and sibling counts are checked against the library
// caps before any of those buffers are allocated.
func DecodeSymbolProof(frame []byte) (SymbolProof, error) {
	r := newReader(frame)
	if err := r.requireMagic(); err != nil {
		return SymbolProof{}, err
	}
	ver, err := r.u8()
	if err != nil {
		return SymbolProof{}, err
	}
	if ver != WireVersion {
		return SymbolProof{}, fmt.Errorf("%w: %d", ErrUnknownWireVersion, ver)
	}
	typ, err := r.u8()
	if err != nil {
		return SymbolProof{}, err
	}
	if typ != wireTypeSymbolProof {
		return SymbolProof{}, fmt.Errorf("%w: %d (want symbol proof)", ErrUnknownWireType, typ)
	}
	idx, err := r.u32()
	if err != nil {
		return SymbolProof{}, err
	}
	coeffs, err := r.blob32(coding.MaxSourceSymbols, "coefficients")
	if err != nil {
		return SymbolProof{}, err
	}
	payload, err := r.blob32(coding.MaxSymbolSize, "payload")
	if err != nil {
		return SymbolProof{}, err
	}
	nSib, err := r.u16()
	if err != nil {
		return SymbolProof{}, err
	}
	if int(nSib) > MaxMerkleSiblings {
		return SymbolProof{}, fmt.Errorf("objects: symbol proof has %d siblings, max %d", nSib, MaxMerkleSiblings)
	}
	siblings := make([][32]byte, 0, nSib)
	flags := make([]bool, 0, nSib)
	for i := 0; i < int(nSib); i++ {
		h, err := r.bytes32()
		if err != nil {
			return SymbolProof{}, err
		}
		side, err := r.u8()
		if err != nil {
			return SymbolProof{}, err
		}
		if side > 1 {
			return SymbolProof{}, fmt.Errorf("objects: sibling side flag %d is not 0 or 1", side)
		}
		siblings = append(siblings, h)
		flags = append(flags, side == 1)
	}
	if err := r.done(); err != nil {
		return SymbolProof{}, err
	}
	return SymbolProof{
		Symbol: coding.Symbol{
			Index:        int(idx),
			Coefficients: coeffs,
			Payload:      payload,
		},
		Siblings:       siblings,
		SiblingIsRight: flags,
	}, nil
}

type writer struct{ b []byte }

// FrameType returns the SQCO wire type of a frame (1 manifest, 2 symbol proof,
// 3 object manifest), without decoding the body. The manifest reply is
// self-describing, so a fetch can decide whether the peer served a
// single-generation or multi-generation object. A frame whose type byte is
// not one of the three known types is refused: callers switch on the result,
// so accepting unknown types here would push the rejection burden onto every
// caller.
func FrameType(frame []byte) (byte, error) {
	r := newReader(frame)
	if err := r.requireMagic(); err != nil {
		return 0, err
	}
	ver, err := r.u8()
	if err != nil {
		return 0, err
	}
	if ver != WireVersion {
		return 0, fmt.Errorf("%w: %d", ErrUnknownWireVersion, ver)
	}
	typ, err := r.u8()
	if err != nil {
		return 0, err
	}
	switch typ {
	case wireTypeManifest, wireTypeSymbolProof, wireTypeObjectManifest:
		return typ, nil
	default:
		return 0, fmt.Errorf("%w: %d", ErrUnknownWireType, typ)
	}
}

// EncodeObjectManifest writes a versioned SQCO frame for a multi-generation
// object manifest. Generation count is bounded by MaxGenerations before any
// allocation.
func EncodeObjectManifest(m *ObjectManifest) ([]byte, error) {
	if m == nil {
		return nil, errors.New("objects: nil object manifest")
	}
	if len(m.Generations) == 0 || len(m.Generations) > MaxGenerations {
		return nil, fmt.Errorf("objects: object manifest has %d generations, must be in [1,%d]", len(m.Generations), MaxGenerations)
	}
	var w writer
	w.magic()
	w.u8(WireVersion)
	w.u8(wireTypeObjectManifest)
	// Chain id leads the frame for the same reason it leads the descriptor
	// preimage: it is the outermost scope of the object's identity, and a
	// receiver should be able to reject another tier's frame before it has
	// parsed anything it would have to trust.
	if err := w.str16(m.Anchor.ChainID, MaxChainIDLen, "anchor chain id"); err != nil {
		return nil, err
	}
	if err := w.str16(m.Anchor.Kind, MaxAnchorKindLen, "anchor kind"); err != nil {
		return nil, err
	}
	w.bytes32(m.Anchor.ID)
	w.u64(m.Anchor.Height)
	if err := w.str16(m.Codec, MaxCodecNameLen, "codec name"); err != nil {
		return nil, err
	}
	w.u64(m.ObjectSize)
	w.u32(m.SymbolSize)
	w.u16(uint16(len(m.Generations)))
	for _, g := range m.Generations {
		w.u32(g.SourceCount)
		w.bytes32(g.CodebookRoot)
		w.u32(g.CodebookSize)
	}
	if err := w.blob16(m.SignerPubKey, qcrypto.PublicKeySize, "signer public key"); err != nil {
		return nil, err
	}
	if err := w.blob16(m.Signature, qcrypto.SignatureSize, "signature"); err != nil {
		return nil, err
	}
	return w.b, nil
}

// DecodeObjectManifest parses a frame produced by EncodeObjectManifest.
// Claimed generation, symbol, and signer lengths are checked against the
// constants before allocation.
func DecodeObjectManifest(frame []byte) (*ObjectManifest, error) {
	r := newReader(frame)
	if err := r.requireMagic(); err != nil {
		return nil, err
	}
	ver, err := r.u8()
	if err != nil {
		return nil, err
	}
	if ver != WireVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownWireVersion, ver)
	}
	typ, err := r.u8()
	if err != nil {
		return nil, err
	}
	if typ != wireTypeObjectManifest {
		return nil, fmt.Errorf("%w: %d (want object manifest)", ErrUnknownWireType, typ)
	}
	chainID, err := r.str16(MaxChainIDLen, "anchor chain id")
	if err != nil {
		return nil, err
	}
	kind, err := r.str16(MaxAnchorKindLen, "anchor kind")
	if err != nil {
		return nil, err
	}
	id, err := r.bytes32()
	if err != nil {
		return nil, err
	}
	height, err := r.u64()
	if err != nil {
		return nil, err
	}
	codec, err := r.str16(MaxCodecNameLen, "codec name")
	if err != nil {
		return nil, err
	}
	objectSize, err := r.u64()
	if err != nil {
		return nil, err
	}
	symbolSize, err := r.u32()
	if err != nil {
		return nil, err
	}
	nGen, err := r.u16()
	if err != nil {
		return nil, err
	}
	if int(nGen) == 0 || int(nGen) > MaxGenerations {
		return nil, fmt.Errorf("objects: object manifest has %d generations, must be in [1,%d]", nGen, MaxGenerations)
	}
	gens := make([]GenerationManifest, 0, nGen)
	for i := 0; i < int(nGen); i++ {
		src, err := r.u32()
		if err != nil {
			return nil, err
		}
		root, err := r.bytes32()
		if err != nil {
			return nil, err
		}
		size, err := r.u32()
		if err != nil {
			return nil, err
		}
		gens = append(gens, GenerationManifest{SourceCount: src, CodebookRoot: root, CodebookSize: size})
	}
	pub, err := r.blob16(qcrypto.PublicKeySize, "signer public key")
	if err != nil {
		return nil, err
	}
	sig, err := r.blob16(qcrypto.SignatureSize, "signature")
	if err != nil {
		return nil, err
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return &ObjectManifest{
		Anchor:       Anchor{ChainID: chainID, Kind: kind, ID: id, Height: height},
		Codec:        codec,
		ObjectSize:   objectSize,
		SymbolSize:   symbolSize,
		Generations:  gens,
		SignerPubKey: pub,
		Signature:    sig,
	}, nil
}

func (w *writer) magic()    { w.b = append(w.b, sqcoMagic[:]...) }
func (w *writer) u8(v byte) { w.b = append(w.b, v) }
func (w *writer) u16(v uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	w.b = append(w.b, buf[:]...)
}
func (w *writer) u32(v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	w.b = append(w.b, buf[:]...)
}
func (w *writer) u64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	w.b = append(w.b, buf[:]...)
}
func (w *writer) bytes32(v [32]byte) { w.b = append(w.b, v[:]...) }

func (w *writer) str16(s string, max int, what string) error {
	if len(s) > max {
		return fmt.Errorf("objects: %s is %d bytes, max %d", what, len(s), max)
	}
	w.u16(uint16(len(s)))
	w.b = append(w.b, s...)
	return nil
}

func (w *writer) blob16(b []byte, exact int, what string) error {
	if len(b) != exact {
		return fmt.Errorf("objects: %s is %d bytes, want %d", what, len(b), exact)
	}
	w.u16(uint16(len(b)))
	w.b = append(w.b, b...)
	return nil
}

func (w *writer) blob32(b []byte, max int, what string) error {
	if len(b) > max {
		return fmt.Errorf("objects: %s is %d bytes, max %d", what, len(b), max)
	}
	w.u32(uint32(len(b)))
	w.b = append(w.b, b...)
	return nil
}

type reader struct {
	b []byte
	i int
}

func newReader(b []byte) *reader { return &reader{b: b} }

func (r *reader) remain() int { return len(r.b) - r.i }

func (r *reader) need(n int) error {
	if n < 0 || r.remain() < n {
		return ErrTruncatedFrame
	}
	return nil
}

func (r *reader) requireMagic() error {
	if err := r.need(4); err != nil {
		return err
	}
	if r.b[r.i] != sqcoMagic[0] || r.b[r.i+1] != sqcoMagic[1] || r.b[r.i+2] != sqcoMagic[2] || r.b[r.i+3] != sqcoMagic[3] {
		return errors.New("objects: frame is not an SQCO object")
	}
	r.i += 4
	return nil
}

func (r *reader) u8() (byte, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint16(r.b[r.i:])
	r.i += 2
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v, nil
}

func (r *reader) u64() (uint64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint64(r.b[r.i:])
	r.i += 8
	return v, nil
}

func (r *reader) bytes32() ([32]byte, error) {
	var out [32]byte
	if err := r.need(32); err != nil {
		return out, err
	}
	copy(out[:], r.b[r.i:r.i+32])
	r.i += 32
	return out, nil
}

func (r *reader) take(n, max int, what string) ([]byte, error) {
	if n < 0 || n > max {
		return nil, fmt.Errorf("objects: %s length %d exceeds max %d", what, n, max)
	}
	if err := r.need(n); err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, r.b[r.i:r.i+n])
	r.i += n
	return out, nil
}

func (r *reader) str16(max int, what string) (string, error) {
	n, err := r.u16()
	if err != nil {
		return "", err
	}
	b, err := r.take(int(n), max, what)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *reader) blob16(exact int, what string) ([]byte, error) {
	n, err := r.u16()
	if err != nil {
		return nil, err
	}
	if int(n) != exact {
		return nil, fmt.Errorf("objects: %s is %d bytes, want %d", what, n, exact)
	}
	return r.take(int(n), exact, what)
}

func (r *reader) blob32(max int, what string) ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	return r.take(int(n), max, what)
}

func (r *reader) done() error {
	if r.remain() != 0 {
		return ErrTrailingBytes
	}
	return nil
}
