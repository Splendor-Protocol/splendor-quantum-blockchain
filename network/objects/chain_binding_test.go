package objects

import (
	"bytes"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// The three tiers this binary serves. A validator key legitimately exists on
// more than one of them — the same operator runs devnet and mainnet — so
// "who signed it" can never answer "which chain is it from".
const (
	devnetChainID  = "splendor-quantum-devnet-1"
	mainnetChainID = "splendor-quantum-1"
)

// onChain points this process at one tier, the way a node adopts its chain id
// from its genesis document at start-up. Cleanups restore in LIFO order, so a
// test may call it more than once to stand in for two different nodes.
func onChain(t *testing.T, id string) {
	t.Helper()
	prevID, prevAdopted := types.ChainID, types.ChainIDAdoptedForTest()
	t.Cleanup(func() { types.RestoreChainIDForTest(prevID, prevAdopted) })
	types.RestoreChainIDForTest(id, true)
}

// A snapshot object published on devnet must not be verifiable, let alone
// decodable, on mainnet. Peer identity proves who supplied the bytes; it does
// not prove the bytes belong to this chain, and the receiver pins the signer
// key, which the same operator holds on both tiers.
func TestManifestFromAnotherTierIsRefused(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{0xAB}, 256)

	// A devnet node publishes and signs a snapshot object.
	onChain(t, devnetChainID)
	send, err := NewSender(object, Descriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{7}, Height: 42},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatalf("publish on %s: %v", devnetChainID, err)
	}
	frame, err := EncodeManifest(send.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	proofs := make([][]byte, send.SymbolCount())
	for i := range proofs {
		p, perr := send.Proof(i)
		if perr != nil {
			t.Fatal(perr)
		}
		if proofs[i], perr = EncodeSymbolProof(p); perr != nil {
			t.Fatal(perr)
		}
	}

	// A mainnet node is handed those exact bytes and pins the signer it knows.
	onChain(t, mainnetChainID)
	m, err := DecodeManifest(frame)
	if err != nil {
		t.Fatalf("manifest frame did not parse: %v", err)
	}
	recv, err := NewReceiver(m, DefaultLimits(), AllowSignerBytes(pub.Bytes()))
	if err != nil {
		// Refused before a symbol was spent on: the requirement.
		return
	}
	for i, raw := range proofs {
		p, derr := DecodeSymbolProof(raw)
		if derr != nil {
			t.Fatalf("symbol %d: %v", i, derr)
		}
		if _, aerr := recv.AbsorbProof(p); aerr != nil {
			t.Fatalf("a %s receiver built a decoder for a %s manifest and then rejected symbol %d: %v",
				mainnetChainID, devnetChainID, i, aerr)
		}
	}
	got, err := recv.Decode()
	if err != nil {
		t.Fatalf("a %s receiver accepted a %s manifest and absorbed every symbol: %v",
			mainnetChainID, devnetChainID, err)
	}
	if bytes.Equal(got, object) {
		t.Fatalf("a manifest published on %s was verified and decoded byte-for-byte on %s: "+
			"nothing in the descriptor names the chain, so the transfer layer cannot tell the tiers apart",
			devnetChainID, mainnetChainID)
	}
	t.Fatalf("a manifest published on %s was accepted on %s", devnetChainID, mainnetChainID)
}

// Every symbol leaf binds to the descriptor id (codebook.go), so if two tiers
// derive the same id for the same object then a symbol proof minted on one is
// a valid proof inside the other's transfer. The id must separate them.
func TestDescriptorIDSeparatesTiers(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{1}, 128)
	desc := Descriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{3}, Height: 9},
		SymbolSize: 64,
	}

	onChain(t, devnetChainID)
	dev, err := NewSender(object, desc, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatalf("publish on %s: %v", devnetChainID, err)
	}
	onChain(t, mainnetChainID)
	prod, err := NewSender(object, desc, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatalf("publish on %s: %v", mainnetChainID, err)
	}

	devID, prodID := dev.Manifest().DescriptorID(), prod.Manifest().DescriptorID()
	if devID == prodID {
		t.Fatalf("the same object at the same height derives descriptor id %x on both %s and %s: "+
			"symbol leaves bind to this id, so a symbol proof minted on one tier verifies inside the other's transfer",
			devID[:8], devnetChainID, mainnetChainID)
	}
}
