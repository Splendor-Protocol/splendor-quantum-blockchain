package network

import (
	"encoding/json"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// HandleIncomingBlock must fail closed: no trusted pubkey => refuse (do not apply);
// wrong pubkey => signature failure (do not apply); correct pubkey => apply.
func TestHandleIncomingBlockMandatorySignature(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	blk := &types.Block{Header: types.BlockHeader{
		Height:       1,
		ChainID:      types.ChainID,
		AlgID:        qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}),
	}}
	h, _ := blk.ComputeHash()
	blk.Hash = h
	if err := blk.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	body, _ := json.Marshal(blk)

	applied := false
	apply := func(b *types.Block) error { applied = true; return nil }

	// Empty PK → refuse, do not apply.
	if err := HandleIncomingBlock(body, nil, nil, apply); err == nil {
		t.Fatal("expected refusal with empty validator pubkey")
	}
	if applied {
		t.Fatal("block applied despite no trusted pubkey")
	}

	// Wrong PK → signature verification fails, do not apply.
	otherPub, _, _ := qcrypto.GenerateKeypair()
	if err := HandleIncomingBlock(body, otherPub.Bytes(), nil, apply); err == nil {
		t.Fatal("expected signature failure with wrong pubkey")
	}
	if applied {
		t.Fatal("block applied with an invalid signature")
	}

	// Correct PK → verify passes and the block is applied.
	if err := HandleIncomingBlock(body, pub.Bytes(), nil, apply); err != nil {
		t.Fatalf("valid signed block rejected: %v", err)
	}
	if !applied {
		t.Fatal("valid block was not applied")
	}
}
