package network

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// sealedBlock builds a structurally valid block at `height`, signed by a fresh
// key, with the producer key attached the way a multi-producer node emits it.
func sealedBlock(t *testing.T, height uint64) (*types.Block, *qcrypto.PublicKey, *qcrypto.PrivateKey) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	merkle, err := types.ComputeTxMerkleRoot(nil)
	if err != nil {
		t.Fatalf("merkle: %v", err)
	}
	b := &types.Block{Header: types.BlockHeader{
		Height:        height,
		Timestamp:     time.Now().Unix(),
		ChainID:       types.ChainID,
		AlgID:         qcrypto.AlgMLDSA65,
		TxMerkleRoot:  merkle,
		ValidatorAddr: qcrypto.DeriveAddress(pub),
	}}
	hash, err := b.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b.Hash = hash
	if err := b.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	b.ValidatorPubKey = pub.Bytes()
	return b, pub, priv
}

// The honest case: a block carrying the key that sealed it, bound to the
// address in its header.
func TestVerifyProducerBoundAcceptsSelfConsistentBlock(t *testing.T) {
	b, _, _ := sealedBlock(t, 7)
	if err := VerifyProducerBound(b); err != nil {
		t.Fatalf("a correctly sealed block was refused: %v", err)
	}
}

// The check that carries the weight. A block's embedded key always verifies
// its own signature, so what makes the key meaningful is that it must hash to
// the address in the header — and the header is inside the block hash the
// signature covers. Swap the key and the binding breaks.
func TestVerifyProducerBoundRefusesKeyThatIsNotTheHeaderAddress(t *testing.T) {
	b, _, _ := sealedBlock(t, 7)
	otherPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	b.ValidatorPubKey = otherPub.Bytes()

	err = VerifyProducerBound(b)
	if err == nil {
		t.Fatal("a block whose key does not derive to its header address was accepted")
	}
	if !strings.Contains(err.Error(), "derives to") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A signature that does not match, under a key that does hash to the header
// address, is still a refusal — the binding is necessary, not sufficient.
func TestVerifyProducerBoundRefusesBadSignature(t *testing.T) {
	b, _, _ := sealedBlock(t, 7)
	b.ValidatorSignature[0] ^= 0xff
	if err := VerifyProducerBound(b); err == nil {
		t.Fatal("a block with a corrupted signature was accepted")
	}
}

// A block with no key at all cannot be authenticated on this path, and the
// refusal has to say so rather than fall through to some other rule.
func TestVerifyProducerBoundRefusesMissingKey(t *testing.T) {
	b, _, _ := sealedBlock(t, 7)
	b.ValidatorPubKey = nil
	err := VerifyProducerBound(b)
	if err == nil {
		t.Fatal("a block carrying no producer key was accepted")
	}
	if !strings.Contains(err.Error(), "carries no producer key") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// The ingest wrapper must not apply a block it could not authenticate: the
// apply callback is where the chain's admission gate lives, and reaching it
// with an unverified block would put the wrong producer in front of it.
func TestHandleIncomingBlockMultiProducerDoesNotApplyUnverified(t *testing.T) {
	b, _, _ := sealedBlock(t, 7)
	otherPub, _, _ := qcrypto.GenerateKeypair()
	b.ValidatorPubKey = otherPub.Bytes()
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	applied := false
	if err := HandleIncomingBlockMultiProducer(body, func(*types.Block) error {
		applied = true
		return nil
	}); err == nil {
		t.Fatal("an unauthenticated block was accepted")
	}
	if applied {
		t.Fatal("the block reached the apply path despite failing verification")
	}

	// And the honest one does get there.
	good, _, _ := sealedBlock(t, 8)
	body, _ = json.Marshal(good)
	seen := uint64(0)
	if err := HandleIncomingBlockMultiProducer(body, func(bl *types.Block) error {
		seen = bl.Header.Height
		return nil
	}); err != nil {
		t.Fatalf("a correctly sealed block was refused: %v", err)
	}
	if seen != 8 {
		t.Fatalf("apply saw height %d, want 8", seen)
	}
}
