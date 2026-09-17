package objects

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func signedTestManifest(t *testing.T) (*Manifest, *Codebook) {
	t.Helper()
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(21))
	desc := testDescriptor()
	symbols := genSymbols(rng, 20)
	cb, err := BuildCodebook(desc.DescriptorID(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	m, err := SignManifest(desc, cb, priv)
	if err != nil {
		t.Fatal(err)
	}
	return m, cb
}

func manifestsEqual(a, b *Manifest) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Descriptor == b.Descriptor &&
		a.CodebookRoot == b.CodebookRoot &&
		a.CodebookSize == b.CodebookSize &&
		bytes.Equal(a.SignerPubKey, b.SignerPubKey) &&
		bytes.Equal(a.Signature, b.Signature)
}

func proofsEqual(a, b SymbolProof) bool {
	if a.Symbol.Index != b.Symbol.Index ||
		!bytes.Equal(a.Symbol.Coefficients, b.Symbol.Coefficients) ||
		!bytes.Equal(a.Symbol.Payload, b.Symbol.Payload) ||
		len(a.Siblings) != len(b.Siblings) ||
		len(a.SiblingIsRight) != len(b.SiblingIsRight) {
		return false
	}
	for i := range a.Siblings {
		if a.Siblings[i] != b.Siblings[i] || a.SiblingIsRight[i] != b.SiblingIsRight[i] {
			return false
		}
	}
	return true
}

func TestManifestWireRoundTrip(t *testing.T) {
	m, _ := signedTestManifest(t)
	frame, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeManifest(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !manifestsEqual(m, got) {
		t.Fatal("decoded manifest does not match the signed original")
	}
	if err := got.Verify(1024, 1<<20); err != nil {
		t.Fatalf("round-tripped manifest failed to verify: %v", err)
	}
	again, err := EncodeManifest(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame, again) {
		t.Fatal("EncodeManifest is not canonical: a decode/encode cycle changed the bytes")
	}
}

func TestSymbolProofWireRoundTrip(t *testing.T) {
	_, cb := signedTestManifest(t)
	for _, i := range []int{0, 1, cb.Len() - 1} {
		proof, err := cb.BuildProof(i)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := EncodeSymbolProof(proof)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeSymbolProof(frame)
		if err != nil {
			t.Fatal(err)
		}
		if !proofsEqual(proof, got) {
			t.Fatalf("decoded proof %d does not match", i)
		}
		if !VerifySymbolProof(cb.Root(), cb.DescriptorID(), got) {
			t.Fatalf("round-tripped proof %d failed verification", i)
		}
	}
}

func TestDecodeManifestRejectsGarbage(t *testing.T) {
	m, _ := signedTestManifest(t)
	good, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := DecodeManifest([]byte(`{"not":"sqco"}`)); err == nil {
		t.Fatal("JSON body accepted as a manifest")
	}
	if _, err := DecodeManifest(good[:4]); err == nil {
		t.Fatal("truncated magic accepted")
	}
	if _, err := DecodeManifest(append(append([]byte{}, good...), 0x00)); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing byte = %v, want ErrTrailingBytes", err)
	}

	ver := append([]byte{}, good...)
	ver[4] = 99
	if _, err := DecodeManifest(ver); !errors.Is(err, ErrUnknownWireVersion) {
		t.Fatalf("unknown version = %v, want ErrUnknownWireVersion", err)
	}

	typ := append([]byte{}, good...)
	typ[5] = wireTypeSymbolProof
	if _, err := DecodeManifest(typ); !errors.Is(err, ErrUnknownWireType) {
		t.Fatalf("wrong type = %v, want ErrUnknownWireType", err)
	}

	short := append([]byte{}, good[:len(good)-1]...)
	if _, err := DecodeManifest(short); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("truncated body = %v, want ErrTruncatedFrame", err)
	}
}

func TestDecodeSymbolProofRejectsGarbage(t *testing.T) {
	_, cb := signedTestManifest(t)
	proof, err := cb.BuildProof(2)
	if err != nil {
		t.Fatal(err)
	}
	good, err := EncodeSymbolProof(proof)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := DecodeSymbolProof(good[:6]); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("truncated proof = %v, want ErrTruncatedFrame", err)
	}
	if _, err := DecodeSymbolProof(append(append([]byte{}, good...), 0xff)); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing proof byte = %v, want ErrTrailingBytes", err)
	}

	badSide := append([]byte{}, good...)
	// Last byte of a proof with siblings is the last side flag.
	if len(badSide) == 0 {
		t.Fatal("empty encoded proof")
	}
	badSide[len(badSide)-1] = 2
	if _, err := DecodeSymbolProof(badSide); err == nil {
		t.Fatal("sibling side flag 2 accepted")
	}
}

func TestDecodeRejectsOversizedLengthPrefixWithoutHugeAlloc(t *testing.T) {
	// Craft a proof header whose payload-length u32 claims MaxSymbolSize+1
	// bytes. Decode must refuse that prefix before allocating the claimed size.
	var buf []byte
	buf = append(buf, sqcoMagic[:]...)
	buf = append(buf, WireVersion, wireTypeSymbolProof)
	var idx, coeffLen, payLen [4]byte
	binary.BigEndian.PutUint32(payLen[:], uint32(coding.MaxSymbolSize)+1)
	buf = append(buf, idx[:]...)
	buf = append(buf, coeffLen[:]...)
	buf = append(buf, payLen[:]...)
	if _, err := DecodeSymbolProof(buf); err == nil {
		t.Fatal("payload length MaxSymbolSize+1 accepted")
	}

	var kindLen [2]byte
	binary.BigEndian.PutUint16(kindLen[:], uint16(MaxAnchorKindLen)+1)
	man := append(append([]byte{}, sqcoMagic[:]...), WireVersion, wireTypeManifest)
	man = append(man, kindLen[:]...)
	if _, err := DecodeManifest(man); err == nil {
		t.Fatal("anchor kind longer than MaxAnchorKindLen accepted")
	}
}

func TestEncodeManifestRejectsWrongKeySizes(t *testing.T) {
	m, _ := signedTestManifest(t)
	m.SignerPubKey = m.SignerPubKey[:8]
	if _, err := EncodeManifest(m); err == nil {
		t.Fatal("short public key accepted")
	}
}

func TestUncodedSymbolProofRoundTrip(t *testing.T) {
	// Uncoded symbols carry empty coefficients; the wire must preserve that
	// rather than inventing a zero-length vs omitted distinction.
	descID := qcrypto.Hash32([]byte("uncoded-wire"))
	symbols := []coding.Symbol{{Index: 0, Payload: bytes.Repeat([]byte{7}, 16)}}
	cb, err := BuildCodebook(descID, symbols)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := cb.BuildProof(0)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeSymbolProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSymbolProof(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Symbol.Coefficients) != 0 {
		t.Fatalf("uncoded coefficients survived as %d bytes", len(got.Symbol.Coefficients))
	}
	if !VerifySymbolProof(cb.Root(), descID, got) {
		t.Fatal("round-tripped uncoded proof failed verification")
	}
}
