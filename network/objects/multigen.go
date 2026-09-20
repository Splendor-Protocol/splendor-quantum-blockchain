package objects

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// MaxGenerations caps how many generations one object manifest may describe.
// A 64 MiB snapshot at 16 KiB symbols (256 sources/generation) is 16
// generations, so 4096 leaves enormous headroom while bounding the wire decode
// before any per-generation allocation.
const MaxGenerations = 4096

// GenerationManifest is one generation's committed shape: how many source
// symbols it holds, and the Merkle root of its (finite, source-generated)
// codebook. A receiver does not need the codebook itself — only the root and
// the committed size, which the signed object manifest carries.
type GenerationManifest struct {
	SourceCount  uint32
	CodebookRoot [32]byte
	CodebookSize uint32
}

// ObjectManifest is the signed, transmissible description of a multi-generation
// coded object. One signature covers the anchor, codec shape and EVERY
// generation's codebook root, so a source cannot commit to N generations and
// later swap one. A single-generation object is simply the one-generation case.
//
// Ordering (same as the single-generation Manifest): the object descriptor is
// fixed first (excludes generation roots), generation IDs are derived from it,
// each generation's codebook binds its symbols to that ID, and only then is the
// manifest (descriptor + generation roots) signed — closing the loop without a
// circular hash.
type ObjectManifest struct {
	Anchor       Anchor
	Codec        string
	SymbolSize   uint32
	ObjectSize   uint64
	Generations  []GenerationManifest
	SignerPubKey []byte
	Signature    []byte
}

// ObjectDescriptor is the codec-parameter half of an object manifest's identity,
// fixed before any symbol exists.
type ObjectDescriptor struct {
	Anchor     Anchor
	Codec      string
	SymbolSize uint32
}

// objectDescriptorID binds the descriptor (not the generation roots) so the
// per-generation IDs below can be computed before any codebook exists.
func (d ObjectDescriptor) objectDescriptorID() [32]byte {
	buf := make([]byte, 0, 64+len(d.Anchor.Kind)+len(d.Codec))
	buf = append(buf, []byte(d.Anchor.Kind)...)
	buf = append(buf, 0x00)
	buf = append(buf, d.Anchor.ID[:]...)
	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], d.Anchor.Height)
	buf = append(buf, heightBuf[:]...)
	buf = append(buf, []byte(d.Codec)...)
	buf = append(buf, 0x00)
	var symBuf [4]byte
	binary.BigEndian.PutUint32(symBuf[:], d.SymbolSize)
	buf = append(buf, symBuf[:]...)
	return qcrypto.Hash32(buf)
}

// DescriptorID returns the manifest's object binding identifier.
func (m *ObjectManifest) DescriptorID() [32]byte {
	return m.Descriptor().objectDescriptorID()
}

// Descriptor reconstructs the object descriptor half of the manifest.
func (m *ObjectManifest) Descriptor() ObjectDescriptor {
	return ObjectDescriptor{Anchor: m.Anchor, Codec: m.Codec, SymbolSize: m.SymbolSize}
}

// generationID is the binding each generation's symbol leaves hash under. It
// must NOT include the codebook root (that would be circular) — it binds the
// object descriptor, the generation index and that generation's source count,
// so a symbol from generation i can never be replayed as generation j.
func (m *ObjectManifest) generationID(gen int) [32]byte {
	descID := m.DescriptorID()
	buf := make([]byte, 0, 32+4+4)
	buf = append(buf, descID[:]...)
	var genBuf [4]byte
	binary.BigEndian.PutUint32(genBuf[:], uint32(gen))
	buf = append(buf, genBuf[:]...)
	var cntBuf [4]byte
	binary.BigEndian.PutUint32(cntBuf[:], m.Generations[gen].SourceCount)
	buf = append(buf, cntBuf[:]...)
	return qcrypto.Hash32(buf)
}

// signedBytes is the exact preimage Sign/Verify operate over: the object
// descriptor ID, then every generation's source count, codebook root and
// codebook size.
func (m *ObjectManifest) signedBytes() []byte {
	descID := m.DescriptorID()
	buf := make([]byte, 0, 32+len(m.Generations)*40)
	buf = append(buf, descID[:]...)
	for _, g := range m.Generations {
		var cntBuf, sizeBuf [4]byte
		binary.BigEndian.PutUint32(cntBuf[:], g.SourceCount)
		binary.BigEndian.PutUint32(sizeBuf[:], g.CodebookSize)
		buf = append(buf, cntBuf[:]...)
		buf = append(buf, g.CodebookRoot[:]...)
		buf = append(buf, sizeBuf[:]...)
	}
	return buf
}

// SignObjectManifest signs a multi-generation manifest built for the given
// descriptor and generation shapes.
func SignObjectManifest(desc ObjectDescriptor, objectSize uint64, generations []GenerationManifest, signer *qcrypto.PrivateKey) (*ObjectManifest, error) {
	if signer == nil {
		return nil, errors.New("objects: nil signer")
	}
	if len(generations) == 0 {
		return nil, errors.New("objects: object manifest has no generations")
	}
	m := &ObjectManifest{
		Anchor:       desc.Anchor,
		Codec:        desc.Codec,
		SymbolSize:   desc.SymbolSize,
		ObjectSize:   objectSize,
		Generations:  append([]GenerationManifest(nil), generations...),
		SignerPubKey: signer.Public().Bytes(),
	}
	sig, err := qcrypto.Sign(signer, m.signedBytes(), qcrypto.CtxObjectManifestMulti)
	if err != nil {
		return nil, fmt.Errorf("objects: sign object manifest: %w", err)
	}
	m.Signature = sig
	return m, nil
}

// Verify checks the object manifest's internal consistency (anchor, codec
// resolvable, symbol size, generation count and per-generation codebook size)
// and its ML-DSA-65 signature. It does NOT check whether SignerPubKey is
// authorised for the anchor — the caller's ObjectAuthorizer decides that.
func (m *ObjectManifest) Verify(maxSourceCount, maxSymbolSize uint32, maxObjectSize uint64) error {
	if m == nil {
		return errors.New("objects: nil object manifest")
	}
	if m.Anchor.Kind == "" || len(m.Anchor.Kind) > MaxAnchorKindLen {
		return fmt.Errorf("objects: object manifest anchor kind %q is not a valid length", m.Anchor.Kind)
	}
	if !anchorKinds[m.Anchor.Kind] {
		return fmt.Errorf("objects: object manifest anchor kind %q is not one this node can validate", m.Anchor.Kind)
	}
	if m.Codec == "" || len(m.Codec) > MaxCodecNameLen {
		return errors.New("objects: object manifest codec name is empty or too long")
	}
	if _, err := coding.Resolve(m.Codec); err != nil {
		return fmt.Errorf("objects: %w", err)
	}
	if m.SymbolSize == 0 {
		return errors.New("objects: object manifest symbol size is zero")
	}
	if m.SymbolSize > maxSymbolSize {
		return fmt.Errorf("objects: object manifest symbol size %d exceeds the %d cap", m.SymbolSize, maxSymbolSize)
	}
	if m.ObjectSize == 0 {
		return errors.New("objects: object manifest object size is zero")
	}
	if len(m.Generations) == 0 {
		return errors.New("objects: object manifest has no generations")
	}
	if len(m.Generations) > MaxGenerations {
		return fmt.Errorf("objects: object manifest has %d generations, max %d", len(m.Generations), MaxGenerations)
	}
	var total uint64
	for i, g := range m.Generations {
		if g.SourceCount == 0 {
			return fmt.Errorf("objects: generation %d source count is zero", i)
		}
		if g.SourceCount > maxSourceCount {
			return fmt.Errorf("objects: generation %d source count %d exceeds the %d cap", i, g.SourceCount, maxSourceCount)
		}
		if g.CodebookSize < g.SourceCount {
			return fmt.Errorf("objects: generation %d codebook size %d is smaller than source count %d", i, g.CodebookSize, g.SourceCount)
		}
		if int(g.CodebookSize) > int(g.SourceCount)+coding.MaxRepairSymbols {
			return fmt.Errorf("objects: generation %d codebook size %d exceeds source count %d plus the %d repair cap",
				i, g.CodebookSize, g.SourceCount, coding.MaxRepairSymbols)
		}
		total += uint64(g.SourceCount) * uint64(m.SymbolSize)
	}
	// The total generation capacity is what the receiver will actually
	// allocate (one decoder per generation), so it is the quantity that must
	// be bounded against the peer-supplied manifest — not just ObjectSize,
	// which an attacker can understate relative to the committed capacity.
	if total > maxObjectSize {
		return fmt.Errorf("objects: object manifest capacity %d exceeds the %d-byte cap", total, maxObjectSize)
	}
	if m.ObjectSize > total {
		return fmt.Errorf("objects: object manifest object size %d exceeds generations' capacity %d", m.ObjectSize, total)
	}
	pub, err := qcrypto.PublicKeyFromBytes(m.SignerPubKey)
	if err != nil {
		return fmt.Errorf("objects: object manifest signer public key: %w", err)
	}
	if !qcrypto.Verify(pub, m.signedBytes(), qcrypto.CtxObjectManifestMulti, m.Signature) {
		return errors.New("objects: object manifest signature invalid")
	}
	return nil
}

// VerifiedSignerAddress returns the SQ address the object manifest's signature
// resolves to. Call only after Verify succeeds.
func (m *ObjectManifest) VerifiedSignerAddress() (qcrypto.Address, error) {
	pub, err := qcrypto.PublicKeyFromBytes(m.SignerPubKey)
	if err != nil {
		return qcrypto.Address{}, err
	}
	return qcrypto.DeriveAddress(pub), nil
}

// ObjectAuthorizer decides whether a verified object manifest's signer is
// allowed to describe this anchor (mirrors Authorizer for the single-generation
// manifest).
type ObjectAuthorizer func(m *ObjectManifest) error

// AllowObjectSignerBytes requires the object manifest's signer public key to
// equal want (constant-time).
func AllowObjectSignerBytes(want []byte) ObjectAuthorizer {
	return func(m *ObjectManifest) error {
		if m == nil || subtle.ConstantTimeCompare(m.SignerPubKey, want) != 1 {
			return ErrUnauthorizedSigner
		}
		return nil
	}
}

// ObjectSender holds a finite, source-generated codebook per generation and the
// one signed object manifest that authenticates all of them.
type ObjectSender struct {
	manifest *ObjectManifest
	gens     []*generation
}

type generation struct {
	descID      [32]byte
	sourceCount int
	codebook    *Codebook
}

// NewObjectSender splits object across generations (each at most maxSourceCount
// source symbols), encodes a finite codebook per generation, and signs one
// object manifest over all of them. An object that fits one generation is still
// valid here (one generation), so the object path is a strict superset of the
// single-generation path.
func NewObjectSender(object []byte, desc ObjectDescriptor, codec coding.Codec, repairCount int, signer *qcrypto.PrivateKey, limits Limits) (*ObjectSender, error) {
	if codec == nil {
		return nil, errors.New("objects: nil codec")
	}
	if signer == nil {
		return nil, errors.New("objects: nil signer")
	}
	if len(object) == 0 {
		return nil, errors.New("objects: object is empty")
	}
	if desc.SymbolSize == 0 {
		return nil, errors.New("objects: descriptor symbol size is zero")
	}
	limits = limits.withDefaults()
	if uint64(len(object)) > limits.MaxObjectSize {
		return nil, fmt.Errorf("objects: object size %d exceeds the %d-byte cap", len(object), limits.MaxObjectSize)
	}
	if desc.SymbolSize > limits.MaxSymbolSize {
		return nil, fmt.Errorf("objects: descriptor symbol size %d exceeds the %d cap", desc.SymbolSize, limits.MaxSymbolSize)
	}
	if desc.Codec == "" {
		desc.Codec = codec.Name()
	} else if desc.Codec != codec.Name() {
		return nil, fmt.Errorf("objects: descriptor codec %q does not match encoder %q", desc.Codec, codec.Name())
	}

	perGenBytes := int(limits.MaxSourceCount) * int(desc.SymbolSize)
	var gens []*generation
	for off := 0; off < len(object); off += perGenBytes {
		end := off + perGenBytes
		if end > len(object) {
			end = len(object)
		}
		chunk := object[off:end]
		source, err := Split(chunk, int(desc.SymbolSize))
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
		// Derive the generation ID from the object descriptor, generation index
		// and this generation's source count — all known before the codebook.
		provisional := &ObjectManifest{
			Anchor:      desc.Anchor,
			Codec:       desc.Codec,
			SymbolSize:  desc.SymbolSize,
			ObjectSize:  uint64(len(object)),
			Generations: make([]GenerationManifest, len(gens)+1),
		}
		for i, g := range gens {
			provisional.Generations[i].SourceCount = uint32(g.sourceCount)
		}
		provisional.Generations[len(gens)].SourceCount = uint32(len(source))
		genID := provisional.generationID(len(gens))
		cb, err := BuildCodebook(genID, symbols)
		if err != nil {
			return nil, err
		}
		gens = append(gens, &generation{descID: genID, sourceCount: len(source), codebook: cb})
	}

	genManifests := make([]GenerationManifest, len(gens))
	for i, g := range gens {
		genManifests[i] = GenerationManifest{
			SourceCount:  uint32(g.sourceCount),
			CodebookRoot: g.codebook.Root(),
			CodebookSize: uint32(g.codebook.Len()),
		}
	}
	manifest, err := SignObjectManifest(desc, uint64(len(object)), genManifests, signer)
	if err != nil {
		return nil, err
	}
	return &ObjectSender{manifest: manifest, gens: gens}, nil
}

// Manifest returns the signed object manifest.
func (s *ObjectSender) Manifest() *ObjectManifest { return s.manifest }

// GenerationCount is the number of generations.
func (s *ObjectSender) GenerationCount() int { return len(s.gens) }

// SymbolCount returns the finite codebook size of generation gen.
func (s *ObjectSender) SymbolCount(gen int) int { return s.gens[gen].codebook.Len() }

// Proof returns the inclusion proof for codebook leaf (gen, index).
func (s *ObjectSender) Proof(gen, index int) (SymbolProof, error) {
	if gen < 0 || gen >= len(s.gens) {
		return SymbolProof{}, fmt.Errorf("objects: generation %d out of range [0,%d)", gen, len(s.gens))
	}
	return s.gens[gen].codebook.BuildProof(index)
}

// ObjectReceiver reconstructs one object across generations from authenticated
// symbols. Every symbol arrives as a proof verified against its generation's
// codebook root (committed by the signed object manifest) before it reaches a
// decoder.
type ObjectReceiver struct {
	manifest *ObjectManifest
	gens     []*genReceiver
}

type genReceiver struct {
	descID       [32]byte
	sourceCount  int
	codebookRoot [32]byte
	codebookSize uint32
	decoder      coding.Decoder
}

// NewObjectReceiver verifies the object manifest (shape, signature) then the
// ObjectAuthorizer, then constructs a decoder per generation.
func NewObjectReceiver(m *ObjectManifest, limits Limits, authorize ObjectAuthorizer) (*ObjectReceiver, error) {
	if m == nil {
		return nil, errors.New("objects: nil object manifest")
	}
	if authorize == nil {
		return nil, ErrNilAuthorizer
	}
	limits = limits.withDefaults()
	if err := m.Verify(limits.MaxSourceCount, limits.MaxSymbolSize, limits.MaxObjectSize); err != nil {
		return nil, err
	}
	if err := authorize(m); err != nil {
		return nil, err
	}
	codec, err := coding.Resolve(m.Codec)
	if err != nil {
		return nil, err
	}
	gens := make([]*genReceiver, len(m.Generations))
	for i, g := range m.Generations {
		dec, err := codec.NewDecoder(int(g.SourceCount), int(m.SymbolSize))
		if err != nil {
			return nil, err
		}
		gens[i] = &genReceiver{
			descID:       m.generationID(i),
			sourceCount:  int(g.SourceCount),
			codebookRoot: g.CodebookRoot,
			codebookSize: g.CodebookSize,
			decoder:      dec,
		}
	}
	return &ObjectReceiver{manifest: m, gens: gens}, nil
}

// Manifest is the verified object manifest this receiver was built from.
func (r *ObjectReceiver) Manifest() *ObjectManifest { return r.manifest }

// GenerationCount is the number of generations.
func (r *ObjectReceiver) GenerationCount() int { return len(r.gens) }

// Ready reports whether every generation has reached full rank.
func (r *ObjectReceiver) Ready() bool {
	for _, g := range r.gens {
		if g.decoder.Rank() != g.sourceCount {
			return false
		}
	}
	return true
}

// GenReady reports whether generation gen alone has reached full rank, so a
// fetcher can stop requesting symbols for a finished generation while others
// continue.
func (r *ObjectReceiver) GenReady(gen int) bool {
	if gen < 0 || gen >= len(r.gens) {
		return false
	}
	return r.gens[gen].decoder.Rank() == r.gens[gen].sourceCount
}

// AbsorbProof verifies the proof against generation gen's codebook root, THEN
// absorbs the symbol.
func (r *ObjectReceiver) AbsorbProof(gen int, proof SymbolProof) (bool, error) {
	if gen < 0 || gen >= len(r.gens) {
		return false, fmt.Errorf("objects: generation %d out of range [0,%d)", gen, len(r.gens))
	}
	g := r.gens[gen]
	idx := proof.Symbol.Index
	if idx < 0 || uint64(idx) >= uint64(g.codebookSize) {
		return false, ErrUnauthenticatedSymbol
	}
	if !VerifySymbolProof(g.codebookRoot, g.descID, proof) {
		return false, ErrUnauthenticatedSymbol
	}
	return g.decoder.Absorb(proof.Symbol)
}

// Decode reconstructs the original object (concatenated generations, padding
// trimmed) once every generation is full rank.
func (r *ObjectReceiver) Decode() ([]byte, error) {
	if !r.Ready() {
		return nil, fmt.Errorf("%w: not all generations reached full rank", ErrReceiverNotReady)
	}
	var out []byte
	for _, g := range r.gens {
		symbols, err := g.decoder.Decode()
		if err != nil {
			return nil, err
		}
		for _, s := range symbols {
			out = append(out, s...)
		}
	}
	return Join([][]byte{out}, r.manifest.ObjectSize)
}
