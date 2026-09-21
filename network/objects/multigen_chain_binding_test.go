package objects

import (
	"bytes"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// The multi-generation path is the one the coded snapshot transfer uses by
// default, so the chain binding matters more here than on the single-
// generation path, not less: a manifest published on devnet must not verify or
// decode on mainnet. Peer identity cannot substitute — the same operator key
// legitimately exists on both tiers.
func TestObjectManifestFromAnotherTierIsRefused(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// Two generations' worth, so this exercises the real multi-gen path.
	object := bytes.Repeat([]byte{0xCD}, 4096)

	onChain(t, devnetChainID)
	send, err := NewObjectSender(object, ObjectDescriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{9}, Height: 77},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatalf("publish on %s: %v", devnetChainID, err)
	}
	if got := send.Manifest().Anchor.ChainID; got != devnetChainID {
		t.Fatalf("sender stamped chain id %q, want %q", got, devnetChainID)
	}
	frame, err := EncodeObjectManifest(send.Manifest())
	if err != nil {
		t.Fatal(err)
	}

	// The same signed bytes, offered to a node on another tier.
	onChain(t, mainnetChainID)
	m, err := DecodeObjectManifest(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, err = NewObjectReceiver(m, DefaultLimits(), AllowObjectSignerBytes(send.Manifest().SignerPubKey))
	if err == nil {
		t.Fatalf("a multi-generation object published on %s was accepted on %s: the coded snapshot path "+
			"would import another chain's state", devnetChainID, mainnetChainID)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("another chain")) {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	// And the same object on its own chain is still accepted, so the binding
	// refuses the foreign case rather than everything.
	onChain(t, devnetChainID)
	if _, err := NewObjectReceiver(m, DefaultLimits(), AllowObjectSignerBytes(send.Manifest().SignerPubKey)); err != nil {
		t.Fatalf("the object was refused on its own chain: %v", err)
	}
}

// ObjectSize drives the final truncation in Join, so it decides how many bytes
// a receiver gets back. It is not in the descriptor id (the id has to be
// computable before the generations exist), which left it covered by nothing:
// a field that decides the output and is not signed is a field the signature
// does not cover.
func TestObjectManifestSignatureCoversObjectSize(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	onChain(t, devnetChainID)
	object := bytes.Repeat([]byte{0x5A}, 2048)
	send, err := NewObjectSender(object, ObjectDescriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{4}, Height: 12},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	m := *send.Manifest()
	if m.ObjectSize != uint64(len(object)) {
		t.Fatalf("ObjectSize = %d, want %d", m.ObjectSize, len(object))
	}
	// Truncate the object by one symbol without touching anything else.
	tampered := m
	tampered.ObjectSize = m.ObjectSize - 64
	limits := DefaultLimits()
	if err := tampered.Verify(limits.MaxSourceCount, limits.MaxSymbolSize, limits.MaxObjectSize); err == nil {
		t.Fatal("a manifest whose ObjectSize was altered after signing still verified; " +
			"the signature does not cover the field that decides how much data the receiver keeps")
	}
	// The untouched manifest still verifies.
	if err := m.Verify(limits.MaxSourceCount, limits.MaxSymbolSize, limits.MaxObjectSize); err != nil {
		t.Fatalf("the genuine manifest was refused: %v", err)
	}
	_ = types.ChainID
}
