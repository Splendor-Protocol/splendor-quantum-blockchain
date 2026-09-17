package objects

import (
	"bytes"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func TestHubServesCurrentAndRejectsUnknown(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{3}, 128)
	send, err := NewSender(object, Descriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	if _, err := h.ServeManifest(nil); err == nil {
		t.Fatal("empty hub served a manifest")
	}
	h.Put(send)
	frame, err := h.ServeManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeManifest(frame)
	if err != nil {
		t.Fatal(err)
	}
	id := m.DescriptorID()
	again, err := h.ServeManifest(id[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame, again) {
		t.Fatal("current vs descriptor-id lookup returned different frames")
	}
	proof, err := h.ServeSymbol(id[:], 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSymbolProof(proof); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ServeSymbol(id[:], uint32(send.SymbolCount())); err == nil {
		t.Fatal("out-of-range symbol served")
	}
	var other [32]byte
	other[0] = 0xff
	if _, err := h.ServeManifest(other[:]); err == nil {
		t.Fatal("unknown descriptor id served")
	}
}

func TestHubPutReplacesPrevious(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewSender(bytes.Repeat([]byte{1}, 64), Descriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSender(bytes.Repeat([]byte{2}, 64), Descriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{2}, Height: 2},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.Put(first)
	old := first.Manifest().DescriptorID()
	h.Put(second)
	if _, err := h.ServeManifest(old[:]); err == nil {
		t.Fatal("replaced object still served")
	}
	cur := h.Current()
	if cur.Manifest().Descriptor.Anchor.Height != 2 {
		t.Fatal("current object was not replaced")
	}
}
