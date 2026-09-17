package objects

import (
	"encoding/binary"
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// Manifest is the complete, signed, transmissible description of one coded
// (or uncoded) object transfer. It is built in the order Section 8.3
// specifies:
//  1. Descriptor is fixed first (excludes the codebook root and signature).
//  2. descriptorID is derived from the Descriptor alone.
//  3. Every symbol leaf binds to that descriptorID (see codebook.go).
//  4. The codebook root commits to every symbol leaf.
//  5. THEN the Manifest (Descriptor + CodebookRoot) is signed.
//
// This ordering is what avoids the circular construction the plan warns
// against: nothing here ever needs a manifest hash to exist before the
// symbol leaves that would reference it.
type Manifest struct {
	Descriptor   Descriptor
	CodebookRoot [32]byte
	// CodebookSize is the number of committed symbols (systematic plus the
	// finite repair set). Signed with the root so a receiver knows the
	// legal leaf-index range without holding the codebook, and a source
	// cannot advertise a larger set than it actually committed.
	CodebookSize uint32
	// SignerPubKey is the ML-DSA-65 public key of whoever produced this
	// manifest — the "source" in Section 8.3's terms. A receiver checks
	// this key against whatever authorises it for the manifest's Anchor
	// (e.g. the pinned genesis validator key for a "block" or "snapshot"
	// anchor); Manifest itself does not decide who is authorised, only
	// whether the signature is genuine.
	SignerPubKey []byte
	Signature    []byte
}

// DescriptorID returns the manifest's binding identifier, so external
// callers can verify per-symbol Merkle proofs against the exact value the
// signed manifest commits to.
func (m Manifest) DescriptorID() [32]byte { return m.Descriptor.DescriptorID() }

// signedBytes is the exact preimage Sign/Verify operate over: descriptorID
// (not the raw Descriptor fields again — one canonical binding), the
// codebook root, and the committed codebook size. The signer's public key is
// deliberately NOT included here: it is authenticated by the ML-DSA-65
// signature itself (a signature only verifies under the key that made it),
// and folding it into the signed bytes would add nothing but exposure to an
// unrelated encoding bug.
func (m Manifest) signedBytes() []byte {
	descID := m.DescriptorID()
	buf := make([]byte, 0, 68)
	buf = append(buf, descID[:]...)
	buf = append(buf, m.CodebookRoot[:]...)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], m.CodebookSize)
	buf = append(buf, n[:]...)
	return buf
}

// SignManifest builds and signs a manifest for the given descriptor and
// codebook, using CtxObjectManifest so this signature can never be replayed
// as authorising anything else ML-DSA-65 signs on this chain.
func SignManifest(desc Descriptor, codebook *Codebook, signer *qcrypto.PrivateKey) (*Manifest, error) {
	if signer == nil {
		return nil, errors.New("objects: nil signer")
	}
	if codebook == nil {
		return nil, errors.New("objects: nil codebook")
	}
	// The codebook must have been built for exactly this descriptor: its root
	// commits to symbol leaves bound to a descriptorID, and signing a manifest
	// whose Descriptor derives a different descriptorID would commit to symbols
	// this manifest's own proof verification can never match. Reject rather
	// than let a caller sign an internally inconsistent manifest.
	if codebook.descID != desc.DescriptorID() {
		return nil, errors.New("objects: codebook was built for a different descriptor")
	}
	n := codebook.Len()
	if n < int(desc.SourceCount) {
		return nil, fmt.Errorf("objects: codebook size %d is smaller than source count %d", n, desc.SourceCount)
	}
	if n > int(desc.SourceCount)+coding.MaxRepairSymbols {
		return nil, fmt.Errorf("objects: codebook size %d exceeds source count %d plus the %d repair cap",
			n, desc.SourceCount, coding.MaxRepairSymbols)
	}
	m := &Manifest{
		Descriptor:   desc,
		CodebookRoot: codebook.Root(),
		CodebookSize: uint32(n),
		SignerPubKey: signer.Public().Bytes(),
	}
	sig, err := qcrypto.Sign(signer, m.signedBytes(), qcrypto.CtxObjectManifest)
	if err != nil {
		return nil, fmt.Errorf("objects: sign manifest: %w", err)
	}
	m.Signature = sig
	return m, nil
}

// Verify checks the manifest's own internal consistency (Descriptor shape,
// bounded by the caller's limits) and its ML-DSA-65 signature. It does NOT
// check whether SignerPubKey is authorised for the manifest's Anchor — that
// decision belongs to the caller, who alone knows the anchor's trust rule
// (e.g. "must be the pinned genesis validator key" for a block anchor).
// A manifest that verifies here has only proven "this exact signer really
// produced this exact descriptor+codebook pairing", nothing about whether
// that signer should be believed for this object.
func (m *Manifest) Verify(maxSourceCount, maxSymbolSize uint32) error {
	if err := m.Descriptor.Validate(maxSourceCount, maxSymbolSize); err != nil {
		return err
	}
	if m.CodebookSize < m.Descriptor.SourceCount {
		return fmt.Errorf("objects: codebook size %d is smaller than source count %d", m.CodebookSize, m.Descriptor.SourceCount)
	}
	if int(m.CodebookSize) > int(m.Descriptor.SourceCount)+coding.MaxRepairSymbols {
		return fmt.Errorf("objects: codebook size %d exceeds source count %d plus the %d repair cap",
			m.CodebookSize, m.Descriptor.SourceCount, coding.MaxRepairSymbols)
	}
	pub, err := qcrypto.PublicKeyFromBytes(m.SignerPubKey)
	if err != nil {
		return fmt.Errorf("objects: manifest signer public key: %w", err)
	}
	if !qcrypto.Verify(pub, m.signedBytes(), qcrypto.CtxObjectManifest, m.Signature) {
		return errors.New("objects: manifest signature invalid")
	}
	return nil
}

// VerifiedSignerAddress returns the SQ address the manifest's signature
// resolves to, for callers to check against their own trust anchor
// (e.g. the pinned genesis validator address). Call only after Verify
// succeeds — this does not itself check the signature.
func (m *Manifest) VerifiedSignerAddress() (qcrypto.Address, error) {
	pub, err := qcrypto.PublicKeyFromBytes(m.SignerPubKey)
	if err != nil {
		return qcrypto.Address{}, err
	}
	return qcrypto.DeriveAddress(pub), nil
}
