package objects

import (
	"crypto/subtle"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

var (
	// ErrUnauthenticatedSymbol means a proof did not verify against the
	// signed codebook root. The decoder is not touched: authenticate-
	// before-decode is the whole point of this controller.
	ErrUnauthenticatedSymbol = errors.New("objects: symbol proof failed verification")
	// ErrUnauthorizedSigner means the manifest signature is genuine but
	// the caller's Authorizer refused the signer for this anchor.
	ErrUnauthorizedSigner = errors.New("objects: manifest signer is not authorised for this anchor")
	// ErrNilAuthorizer means NewReceiver was called without a trust rule.
	// Fail closed: a receiver that "verifies the signature and then trusts
	// whoever signed" is how an unpinned peer becomes a source of objects.
	ErrNilAuthorizer = errors.New("objects: receiver requires an authorizer")
	// ErrReceiverNotReady means Decode was called before the decoder held
	// a full-rank generation of authenticated symbols.
	ErrReceiverNotReady = errors.New("objects: receiver has not reached full rank")
)

// Limits bound a receiver's (and sender's) generation shape before any
// encoder, decoder, or codebook allocation.
type Limits struct {
	MaxSourceCount uint32
	MaxSymbolSize  uint32
}

// DefaultLimits uses the coding library caps: one generation cannot exceed
// MaxSourceSymbols × MaxSymbolSize bytes of payload.
func DefaultLimits() Limits {
	return Limits{MaxSourceCount: coding.MaxSourceSymbols, MaxSymbolSize: coding.MaxSymbolSize}
}

func (l Limits) withDefaults() Limits {
	if l.MaxSourceCount == 0 {
		l.MaxSourceCount = coding.MaxSourceSymbols
	}
	if l.MaxSymbolSize == 0 {
		l.MaxSymbolSize = coding.MaxSymbolSize
	}
	return l
}

// Authorizer decides whether a verified manifest's signer is allowed to
// describe this Anchor. Manifest.Verify has already checked the signature;
// this is the "should this key be believed for a snapshot/block" rule the
// caller alone knows (typically: equal to the pinned genesis validator key).
type Authorizer func(m *Manifest) error

// AllowSignerBytes requires the manifest's signer public key to equal want
// (constant-time). That is the usual pin: the genesis validator key.
func AllowSignerBytes(want []byte) Authorizer {
	return func(m *Manifest) error {
		if m == nil || subtle.ConstantTimeCompare(m.SignerPubKey, want) != 1 {
			return ErrUnauthorizedSigner
		}
		return nil
	}
}

// Split pads object into fixed-size source symbols. The last symbol is
// zero-padded; Join trims using the original length. Padding is an object-
// layer concern, not the codec's.
func Split(object []byte, symbolSize int) ([][]byte, error) {
	if len(object) == 0 {
		return nil, errors.New("objects: object is empty")
	}
	if symbolSize <= 0 {
		return nil, fmt.Errorf("objects: symbol size must be positive, got %d", symbolSize)
	}
	if symbolSize > coding.MaxSymbolSize {
		return nil, fmt.Errorf("objects: symbol size %d exceeds %d", symbolSize, coding.MaxSymbolSize)
	}
	n := (len(object) + symbolSize - 1) / symbolSize
	if n > coding.MaxSourceSymbols {
		return nil, fmt.Errorf("objects: object needs %d source symbols, max %d", n, coding.MaxSourceSymbols)
	}
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		s := make([]byte, symbolSize)
		start := i * symbolSize
		if start < len(object) {
			end := start + symbolSize
			if end > len(object) {
				end = len(object)
			}
			copy(s, object[start:end])
		}
		out[i] = s
	}
	return out, nil
}

// Join concatenates decoded source symbols and trims to objectSize. Extra
// trailing bytes must be the zero padding Split wrote; anything else means
// the reconstructed generation does not match the descriptor.
func Join(symbols [][]byte, objectSize uint64) ([]byte, error) {
	if objectSize == 0 {
		return nil, errors.New("objects: object size is zero")
	}
	var total int
	for _, s := range symbols {
		total += len(s)
	}
	if uint64(total) < objectSize {
		return nil, fmt.Errorf("objects: decoded %d bytes, want object size %d", total, objectSize)
	}
	out := make([]byte, 0, total)
	for _, s := range symbols {
		out = append(out, s...)
	}
	for _, b := range out[objectSize:] {
		if b != 0 {
			return nil, errors.New("objects: decoded padding was not zero")
		}
	}
	return out[:objectSize], nil
}

// Sender holds a finite, source-generated codebook and the signed manifest
// that authenticates it. Relays only ever forward Proof(i) for i in
// [0, SymbolCount); they do not recode.
type Sender struct {
	manifest *Manifest
	codebook *Codebook
}

// NewSender splits object, encodes a finite codebook (systematic plus
// repairCount repair symbols), and signs the manifest. desc.Codec,
// ObjectSize and SourceCount are filled from codec and object when zero,
// and refused when they disagree with the actual encoding.
func NewSender(object []byte, desc Descriptor, codec coding.Codec, repairCount int, signer *qcrypto.PrivateKey, limits Limits) (*Sender, error) {
	if codec == nil {
		return nil, errors.New("objects: nil codec")
	}
	if signer == nil {
		return nil, errors.New("objects: nil signer")
	}
	limits = limits.withDefaults()
	desc, err := bindDescriptor(desc, object, codec.Name(), limits)
	if err != nil {
		return nil, err
	}
	source, err := Split(object, int(desc.SymbolSize))
	if err != nil {
		return nil, err
	}
	enc, err := codec.NewEncoder(source, int(desc.SymbolSize))
	if err != nil {
		return nil, err
	}
	symbols := enc.Systematic()
	if repairCount > 0 {
		repair, err := enc.Repair(repairCount)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, repair...)
	}
	cb, err := BuildCodebook(desc.DescriptorID(), symbols)
	if err != nil {
		return nil, err
	}
	manifest, err := SignManifest(desc, cb, signer)
	if err != nil {
		return nil, err
	}
	return &Sender{manifest: manifest, codebook: cb}, nil
}

func bindDescriptor(desc Descriptor, object []byte, codecName string, limits Limits) (Descriptor, error) {
	if len(object) == 0 {
		return Descriptor{}, errors.New("objects: object is empty")
	}
	if desc.SymbolSize == 0 {
		return Descriptor{}, errors.New("objects: descriptor symbol size is zero")
	}
	if desc.ObjectSize == 0 {
		desc.ObjectSize = uint64(len(object))
	} else if desc.ObjectSize != uint64(len(object)) {
		return Descriptor{}, fmt.Errorf("objects: descriptor object size %d does not match %d-byte object",
			desc.ObjectSize, len(object))
	}
	need := uint32((len(object) + int(desc.SymbolSize) - 1) / int(desc.SymbolSize))
	if desc.SourceCount == 0 {
		desc.SourceCount = need
	} else if desc.SourceCount != need {
		return Descriptor{}, fmt.Errorf("objects: descriptor source count %d does not match object/symbol_size %d",
			desc.SourceCount, need)
	}
	if desc.Codec == "" {
		desc.Codec = codecName
	} else if desc.Codec != codecName {
		return Descriptor{}, fmt.Errorf("objects: descriptor codec %q does not match encoder %q", desc.Codec, codecName)
	}
	if err := desc.Validate(limits.MaxSourceCount, limits.MaxSymbolSize); err != nil {
		return Descriptor{}, err
	}
	return desc, nil
}

// Manifest is the signed description a receiver must verify before any symbol.
func (s *Sender) Manifest() *Manifest { return s.manifest }

// SymbolCount is the finite codebook size (systematic plus committed repair).
func (s *Sender) SymbolCount() int { return s.codebook.Len() }

// Proof returns the inclusion proof for codebook leaf i. Relays forward this
// exact proof; they never build a new linear combination.
func (s *Sender) Proof(i int) (SymbolProof, error) { return s.codebook.BuildProof(i) }

// Receiver reconstructs one object from authenticated symbols. There is no
// Absorb of a raw coding.Symbol: every symbol must arrive as a proof that
// verifies against the signed codebook root first.
type Receiver struct {
	manifest *Manifest
	decoder  coding.Decoder
	descID   [32]byte
}

// NewReceiver verifies the manifest (shape, signature, codebook size) then
// the Authorizer, then constructs a decoder. The decoder is empty: no
// symbol has been absorbed yet.
func NewReceiver(m *Manifest, limits Limits, authorize Authorizer) (*Receiver, error) {
	if m == nil {
		return nil, errors.New("objects: nil manifest")
	}
	if authorize == nil {
		return nil, ErrNilAuthorizer
	}
	limits = limits.withDefaults()
	if err := m.Verify(limits.MaxSourceCount, limits.MaxSymbolSize); err != nil {
		return nil, err
	}
	if err := authorize(m); err != nil {
		return nil, err
	}
	codec, err := coding.Resolve(m.Descriptor.Codec)
	if err != nil {
		return nil, err
	}
	dec, err := codec.NewDecoder(int(m.Descriptor.SourceCount), int(m.Descriptor.SymbolSize))
	if err != nil {
		return nil, err
	}
	return &Receiver{manifest: m, decoder: dec, descID: m.DescriptorID()}, nil
}

// Manifest is the verified manifest this receiver was built from.
func (r *Receiver) Manifest() *Manifest { return r.manifest }

// Rank is how many linearly independent authenticated symbols have been
// absorbed. Decode succeeds only at Rank() == SourceCount.
func (r *Receiver) Rank() int { return r.decoder.Rank() }

// Ready reports whether Decode can succeed.
func (r *Receiver) Ready() bool {
	return r.decoder.Rank() == int(r.manifest.Descriptor.SourceCount)
}

// AbsorbProof verifies proof against the signed codebook root, THEN absorbs
// the symbol. A failed proof returns ErrUnauthenticatedSymbol and does not
// touch the decoder — the plan's "corrupt payload/proof/coefficients:
// rejection before expensive decoding".
func (r *Receiver) AbsorbProof(proof SymbolProof) (bool, error) {
	idx := proof.Symbol.Index
	if idx < 0 || uint64(idx) >= uint64(r.manifest.CodebookSize) {
		return false, ErrUnauthenticatedSymbol
	}
	if !VerifySymbolProof(r.manifest.CodebookRoot, r.descID, proof) {
		return false, ErrUnauthenticatedSymbol
	}
	return r.decoder.Absorb(proof.Symbol)
}

// Decode reconstructs the original object (padding trimmed) once Ready.
func (r *Receiver) Decode() ([]byte, error) {
	if !r.Ready() {
		return nil, fmt.Errorf("%w: rank %d, need %d", ErrReceiverNotReady, r.Rank(), r.manifest.Descriptor.SourceCount)
	}
	symbols, err := r.decoder.Decode()
	if err != nil {
		return nil, err
	}
	return Join(symbols, r.manifest.Descriptor.ObjectSize)
}
