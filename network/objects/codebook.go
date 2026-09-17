package objects

import (
	"encoding/binary"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// leafDomain / internalDomain separate leaf and internal Merkle node
// preimages so an internal node can never be forged to equal a leaf — the
// same construction core/state/statedb.go uses for the state root, reused
// here rather than reinvented.
const (
	leafDomain     = 0x00
	internalDomain = 0x01
)

// validSymbolIndex reports whether a symbol index survives its uint32 wire
// encoding without wrapping: a negative index, or one that does not fit in
// uint32, must never be hashed into a leaf, because uint32(i) would silently
// wrap and let two different indices bind to the same leaf.
func validSymbolIndex(i int) bool { return i >= 0 && uint64(i) <= ^uint64(0)>>32 }

// symbolLeaf hashes one complete symbol record: descriptor binding, symbol
// index, coefficients and payload — everything Section 8.3 step 4 requires
// ("manifest/object identity, generation, symbol index, coefficient
// description, payload length and payload"). Binding the descriptor ID into
// every leaf means a symbol from one manifest can never be replayed as
// belonging to another, even if the raw payload bytes happened to collide.
func symbolLeaf(descID [32]byte, s coding.Symbol) [32]byte {
	buf := make([]byte, 0, 1+32+4+4+len(s.Coefficients)+4+len(s.Payload))
	buf = append(buf, leafDomain)
	buf = append(buf, descID[:]...)
	var idxBuf [4]byte
	binary.BigEndian.PutUint32(idxBuf[:], uint32(s.Index))
	buf = append(buf, idxBuf[:]...)
	var coeffLenBuf [4]byte
	binary.BigEndian.PutUint32(coeffLenBuf[:], uint32(len(s.Coefficients)))
	buf = append(buf, coeffLenBuf[:]...)
	buf = append(buf, s.Coefficients...)
	var payLenBuf [4]byte
	binary.BigEndian.PutUint32(payLenBuf[:], uint32(len(s.Payload)))
	buf = append(buf, payLenBuf[:]...)
	buf = append(buf, s.Payload...)
	return qcrypto.Hash32(buf)
}

func internalNode(l, r [32]byte) [32]byte {
	var buf [1 + 32 + 32]byte
	buf[0] = internalDomain
	copy(buf[1:], l[:])
	copy(buf[33:], r[:])
	return qcrypto.Hash32(buf[:])
}

// Codebook is the finite, source-generated set of coded symbols for one
// generation, committed to a single Merkle root. "Finite" and
// "source-generated" both matter: a relay only ever caches and forwards
// these exact symbols, it does not create new ones (see the package doc for
// why recoding is deferred).
type Codebook struct {
	descID  [32]byte
	symbols []coding.Symbol
	leaves  [][32]byte
	// levels[0] is the leaf level; the last level has exactly one element,
	// the root. Kept in full (not just the root) so BuildProof can answer
	// any index without recomputation.
	levels [][][32]byte
}

func cloneSymbol(s coding.Symbol) coding.Symbol {
	return coding.Symbol{
		Index:        s.Index,
		Coefficients: append([]byte(nil), s.Coefficients...),
		Payload:      append([]byte(nil), s.Payload...),
	}
}

func cloneSymbols(symbols []coding.Symbol) []coding.Symbol {
	out := make([]coding.Symbol, len(symbols))
	for i, s := range symbols {
		out[i] = cloneSymbol(s)
	}
	return out
}

// BuildCodebook commits to every symbol in a generation (systematic plus a
// finite repair set) under one descriptor ID. Coefficient and payload
// buffers are deep-copied so later caller mutation cannot invalidate the
// committed proofs.
func BuildCodebook(descID [32]byte, symbols []coding.Symbol) (*Codebook, error) {
	if len(symbols) == 0 {
		return nil, errors.New("objects: codebook has no symbols")
	}
	// Copy first so the committed leaves and the stored symbols come from
	// the same independent buffers; later caller mutation cannot desync them.
	cp := cloneSymbols(symbols)
	leaves := make([][32]byte, len(cp))
	for i, s := range cp {
		if !validSymbolIndex(s.Index) {
			return nil, fmt.Errorf("objects: symbol %d has index %d, out of the uint32 wire range", i, s.Index)
		}
		leaves[i] = symbolLeaf(descID, s)
	}
	levels := [][][32]byte{leaves}
	level := leaves
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i]) // odd node carried up unchanged
				break
			}
			next = append(next, internalNode(level[i], level[i+1]))
		}
		levels = append(levels, next)
		level = next
	}
	return &Codebook{descID: descID, symbols: cp, leaves: leaves, levels: levels}, nil
}

// Root returns the codebook's Merkle root, the value a Manifest signs over.
func (c *Codebook) Root() [32]byte {
	top := c.levels[len(c.levels)-1]
	return top[0]
}

// Len is the number of committed symbols (systematic plus the finite repair
// set). A receiver uses the signed Manifest.CodebookSize, not this, so it
// never needs the whole codebook on the wire.
func (c *Codebook) Len() int { return len(c.symbols) }

// DescriptorID is the binding the codebook's leaves were hashed under.
func (c *Codebook) DescriptorID() [32]byte { return c.descID }

// SymbolProof is everything a receiver needs to check one symbol against a
// codebook root without holding the whole codebook.
type SymbolProof struct {
	Symbol   coding.Symbol
	Siblings [][32]byte // bottom-up sibling hashes
	// SiblingIsRight[i] is true when Siblings[i] is the RIGHT child at that
	// level (this leaf/subtree is the left one), so VerifySymbolProof
	// hashes in the correct order at every level.
	SiblingIsRight []bool
}

// BuildProof returns the inclusion proof for the symbol at leafIndex.
func (c *Codebook) BuildProof(leafIndex int) (SymbolProof, error) {
	if leafIndex < 0 || leafIndex >= len(c.symbols) {
		return SymbolProof{}, fmt.Errorf("objects: leaf index %d out of range [0,%d)", leafIndex, len(c.symbols))
	}
	proof := SymbolProof{Symbol: cloneSymbol(c.symbols[leafIndex])}
	idx := leafIndex
	for level := 0; level < len(c.levels)-1; level++ {
		nodes := c.levels[level]
		var siblingIdx int
		var isRight bool
		if idx%2 == 0 {
			siblingIdx = idx + 1
			isRight = true
		} else {
			siblingIdx = idx - 1
			isRight = false
		}
		if siblingIdx >= len(nodes) {
			// idx is the odd node carried up unchanged: no sibling at this
			// level, so no proof step is added, matching BuildCodebook's
			// carry-up rule.
			idx /= 2
			continue
		}
		proof.Siblings = append(proof.Siblings, nodes[siblingIdx])
		proof.SiblingIsRight = append(proof.SiblingIsRight, isRight)
		idx /= 2
	}
	return proof, nil
}

// VerifySymbolProof checks one symbol's inclusion proof against a codebook
// root, WITHOUT requiring the whole codebook — this is what a receiver runs
// on every symbol it is handed, per Section 8.3 step 6, before that symbol
// ever reaches a Decoder. Rejects invalid proofs cheaply: the hashing here
// is orders of magnitude cheaper than the GF(256) matrix work a malicious
// symbol could otherwise trigger.
func VerifySymbolProof(root, descID [32]byte, proof SymbolProof) bool {
	// Reject an out-of-range index before it can wrap in symbolLeaf's uint32
	// encoding, which would otherwise let a forged proof bind a wrong index.
	if !validSymbolIndex(proof.Symbol.Index) {
		return false
	}
	h := symbolLeaf(descID, proof.Symbol)
	if len(proof.Siblings) != len(proof.SiblingIsRight) {
		return false
	}
	for i, sib := range proof.Siblings {
		if proof.SiblingIsRight[i] {
			h = internalNode(h, sib)
		} else {
			h = internalNode(sib, h)
		}
	}
	return h == root
}
