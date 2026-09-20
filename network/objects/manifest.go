// Package objects implements the authenticated large-object transfer layer
// that sits between network/coding's codecs and the wire: canonical
// manifests, per-symbol Merkle authentication, and ML-DSA-65 signed
// provenance, per Section 8.2/8.3 of the Sept 2026 improvement plan.
//
// This package answers "should this symbol be trusted enough to spend
// decode work on", never "is this coded data readable" (that's
// network/coding) and never "is this a valid chain object" (that remains
// core/types and core/block's job unchanged — a receiver decodes here, then
// runs the EXISTING block/state validators on the reconstructed bytes; a
// coded transfer never gets its own, weaker acceptance path).
//
// Deliberately deferred: arbitrary relay recoding. A relay that creates a
// new linear combination cannot reuse the original symbol's Merkle proof, so
// only a source-generated, finite codebook is authenticated here (Section
// 8.3's "finite symbol codebook first"). Homomorphic authentication that
// would let relays recode is a separate cryptographic design, out of scope,
// and would introduce non-PQ assumptions (pairings/ECC) this codebase
// deliberately avoids.
package objects

import (
	"encoding/binary"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// Wire-format bounds. These cap length prefixes before any allocation so a
// hostile frame cannot force a multi-gigabyte buffer from a lying u32.
const (
	// WireVersion is the SQCO envelope version this package emits and accepts.
	//
	// 2 added Anchor.ChainID. A version-1 frame carries no chain id, so there
	// is no safe way to read one: assuming the local chain is exactly the
	// cross-tier replay this field exists to stop. Decode refuses it by
	// version instead (ErrUnknownWireVersion), which is why this had to be a
	// version bump rather than an optional trailing field.
	WireVersion = 2
	// MaxChainIDLen is the maximum Anchor.ChainID string on the wire. It is
	// the same 64-character bound core/types.ValidateChainID enforces on the
	// id a genesis document may name.
	MaxChainIDLen = 64
	// MaxAnchorKindLen is the maximum Anchor.Kind string on the wire.
	MaxAnchorKindLen = 32
	// MaxCodecNameLen is the maximum Descriptor.Codec string on the wire.
	MaxCodecNameLen = 64
	// MaxMerkleSiblings caps a SymbolProof's Merkle path. A codebook of
	// MaxSourceSymbols+MaxRepairSymbols leaves needs fewer than 16 hashes;
	// 32 leaves room without letting a frame demand an unbounded path.
	MaxMerkleSiblings = 32
)

// Anchor identifies the already-authenticated object a manifest describes:
// a block hash, a snapshot's state root at a given height, or an authorized
// model manifest hash. The manifest's authentication is only as strong as
// this anchor — a manifest can be perfectly signed and still describe
// content that fails the anchor's own check (a source can sign inconsistent
// data), which is why Decode (in this package) is followed by the object's
// EXISTING chain-level validation, never treated as sufficient on its own.
type Anchor struct {
	// ChainID is the network the anchored object belongs to, the id the node
	// adopted from its genesis document (core/types.ChainID).
	//
	// Without it a manifest is chain-agnostic, and a manifest that names no
	// chain is valid on every chain that will accept its signer. Splendor
	// runs three tiers -- splendor-quantum-devnet-1, -testnet-1 and
	// splendor-quantum-1 -- and one validator key legitimately exists on more
	// than one of them, so pinning the signer answers "who sent this" and
	// never "which chain is this from". The same reasoning that put the chain
	// id inside a signed transaction body (EIP-155) and inside every
	// CometBFT CanonicalVote's SignBytes applies to an object manifest: the
	// binding has to be in what is signed, not in who signed it.
	ChainID string
	// Kind names what this anchor is. Validate accepts only the kinds this
	// node can check at chain level after decode ("block", "snapshot");
	// "model" is reserved and refused until such a check exists. Part of the
	// signed descriptor so a manifest cannot be replayed across kinds.
	Kind string
	// ID is the anchor's own identifying hash: a block hash, a state root,
	// or a model manifest hash. Exactly 32 bytes (SHA3-256).
	ID [32]byte
	// Height disambiguates a snapshot anchor at a given chain height from
	// another at a different height sharing an otherwise similar shape.
	// Zero for anchor kinds where it does not apply (e.g. "model").
	Height uint64
}

// Descriptor is the codec-parameter half of a manifest's identity: the part
// that must be fixed BEFORE any symbol hash exists, so the symbol leaves can
// bind to a descriptor ID without a circular construction (a manifest hash
// cannot depend on symbol hashes that themselves depend on that manifest
// hash — see the plan's explicit warning on this point).
type Descriptor struct {
	Anchor Anchor
	// Codec names the codec used, e.g. "rlnc-gf256-v1", "uncoded-v1". Part
	// of the signed descriptor: a receiver refuses an unrecognised codec
	// name outright rather than guessing how to decode payloads.
	Codec string
	// ObjectSize is the exact byte length of the original, unpadded object.
	// A decoder trims the final symbol's padding using this value.
	ObjectSize uint64
	// SymbolSize is the fixed payload length of every symbol in every
	// generation.
	SymbolSize uint32
	// SourceCount is the number of source (systematic) symbols in the
	// generation. This manifest format describes exactly one generation;
	// an object spanning more than one generation is multiple manifests.
	SourceCount uint32
}

// descriptorDomain tags this preimage so a descriptor id can never collide
// with any other SHA3-256 preimage this codebase hashes, and so the id
// changes wholesale between preimage versions instead of two versions being
// silently confusable. v2 is the version that carries the chain id.
var descriptorDomain = []byte("SPLENDOR-QC-OBJDESC-v2")

// DescriptorID derives the binding identifier from every Descriptor field
// EXCEPT the codebook roots and signature (which do not exist yet at this
// point) — this is the anchor symbol leaves bind to, and what the final
// signed manifest also commits to, closing the loop without a circular hash.
// Exported so the transport layer can compute the same value a receiver needs
// to verify per-symbol Merkle proofs (VerifySymbolProof) against a manifest.
//
// The chain id is the FIRST field after the domain tag: it is the outermost
// scope of the identity, and a manifest for another network must not be able
// to derive this node's id for any object at all.
//
// Every variable-length field carries an explicit u32 length prefix rather
// than the 0x00 separators this preimage used before. With three
// variable-length fields (chain id, anchor kind, codec name) concatenated,
// separators are only unambiguous while every one of them is known not to
// contain 0x00 — which Validate enforces but DescriptorID, exported and
// callable on an unchecked Descriptor, does not. Length prefixes are
// unambiguous by construction.
func (d Descriptor) DescriptorID() [32]byte {
	buf := make([]byte, 0, 96+len(d.Anchor.ChainID)+len(d.Anchor.Kind)+len(d.Codec))
	buf = appendLenPrefixed(buf, descriptorDomain)
	buf = appendLenPrefixed(buf, []byte(d.Anchor.ChainID))
	buf = appendLenPrefixed(buf, []byte(d.Anchor.Kind))
	buf = append(buf, d.Anchor.ID[:]...)
	buf = appendU64(buf, d.Anchor.Height)
	buf = appendLenPrefixed(buf, []byte(d.Codec))
	buf = appendU64(buf, d.ObjectSize)
	buf = appendU32(buf, d.SymbolSize)
	buf = appendU32(buf, d.SourceCount)
	return qcrypto.Hash32(buf)
}

// appendLenPrefixed writes a u32 length then the bytes, so no concatenation
// of fields can be reparsed as a different split.
func appendLenPrefixed(dst, b []byte) []byte {
	dst = appendU32(dst, uint32(len(b)))
	return append(dst, b...)
}

func appendU32(dst []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(dst, buf[:]...)
}

func appendU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

// anchorKinds is the closed set of Anchor.Kind values Validate accepts. A
// decoded object is only as trustworthy as the chain-level check that runs
// on the reconstructed bytes, and this repository has one for exactly these
// two kinds. Anything else is refused before a symbol is decoded rather than
// reconstructed and then ignored.
var anchorKinds = map[string]bool{"block": true, "snapshot": true}

// Validate checks a Descriptor's own internal consistency (not signatures —
// see Manifest.Verify), rejecting the shapes that would let a symbol count
// or size be used to force unbounded work downstream.
func (d Descriptor) Validate(maxSourceCount uint32, maxSymbolSize uint32) error {
	// An empty chain id is refused rather than defaulted. Defaulting it to
	// the local chain would reintroduce exactly the replay this field exists
	// to stop: a frame that names no chain would once again be valid on
	// every chain willing to accept its signer.
	if d.Anchor.ChainID == "" {
		return errors.New("objects: descriptor anchor chain id is empty")
	}
	if len(d.Anchor.ChainID) > MaxChainIDLen {
		return fmt.Errorf("objects: descriptor anchor chain id is %d bytes, max %d", len(d.Anchor.ChainID), MaxChainIDLen)
	}
	if err := types.ValidateChainID(d.Anchor.ChainID); err != nil {
		return fmt.Errorf("objects: descriptor anchor chain id: %w", err)
	}
	if d.Anchor.Kind == "" {
		return errors.New("objects: descriptor anchor kind is empty")
	}
	if len(d.Anchor.Kind) > MaxAnchorKindLen {
		return fmt.Errorf("objects: descriptor anchor kind is %d bytes, max %d", len(d.Anchor.Kind), MaxAnchorKindLen)
	}
	if !anchorKinds[d.Anchor.Kind] {
		return fmt.Errorf("objects: descriptor anchor kind %q is not one this node can validate", d.Anchor.Kind)
	}
	if d.Codec == "" {
		return errors.New("objects: descriptor codec name is empty")
	}
	if len(d.Codec) > MaxCodecNameLen {
		return fmt.Errorf("objects: descriptor codec name is %d bytes, max %d", len(d.Codec), MaxCodecNameLen)
	}
	// A receiver refuses an unrecognised codec name outright rather than
	// guessing how to decode payloads; this is enforced against the canonical
	// codec registry so a manifest can never name a codec nobody can resolve.
	if _, err := coding.Resolve(d.Codec); err != nil {
		return fmt.Errorf("objects: %w", err)
	}
	if d.SourceCount == 0 {
		return errors.New("objects: descriptor source count is zero")
	}
	if d.SourceCount > maxSourceCount {
		return fmt.Errorf("objects: descriptor source count %d exceeds the %d cap", d.SourceCount, maxSourceCount)
	}
	if d.SymbolSize == 0 {
		return errors.New("objects: descriptor symbol size is zero")
	}
	if d.SymbolSize > maxSymbolSize {
		return fmt.Errorf("objects: descriptor symbol size %d exceeds the %d cap", d.SymbolSize, maxSymbolSize)
	}
	if d.ObjectSize == 0 {
		return errors.New("objects: descriptor object size is zero")
	}
	maxObjectSize := uint64(d.SourceCount) * uint64(d.SymbolSize)
	if d.ObjectSize > maxObjectSize {
		return fmt.Errorf("objects: descriptor object size %d exceeds source_count*symbol_size %d",
			d.ObjectSize, maxObjectSize)
	}
	return nil
}
