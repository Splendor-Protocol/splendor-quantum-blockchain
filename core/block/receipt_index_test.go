package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A receipt's TxIndex must be the transaction's position in the BLOCK, not in
// the candidate list the producer started from.
//
// ProduceBlock skips candidates — a bad nonce, an unfunded sender, a hook
// rejection — so after any skip the candidate index runs ahead of the block
// position. Both follower paths (ExecuteBlock, reapplyBlock) range over
// b.Transactions and get it right, so a producing node ends up contradicting
// every other node about the same block, and contradicting itself: the explorer
// serves a correct top-level tx_index from the location index next to an
// embedded receipt that disagrees. Receipt.AddLog copies TxIndex into every log,
// so /logs reports the wrong index in the field indexers use to order and
// dedupe events within a block.
//
// Not a consensus fault — the header commits no receipt root, so state roots
// still agree — but it is chain-derived data two honest nodes serve differently.
func TestReceiptIndexIsBlockPositionNotCandidatePosition(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithTxIndex(sink)
	fund(t, sdb, from, 1_000_000)

	// The first candidate is unappliable (nonce 7 when 0 is expected), so the
	// producer drops it and the second becomes block position 0.
	skipped := signedTransfer(t, from, priv, testAddr(0x51), 100, 7)
	good := signedTransfer(t, from, priv, testAddr(0x52), 100, 0)

	blk, err := ex.ProduceBlock([]*types.Transaction{skipped, good}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(blk.Transactions) != 1 {
		t.Fatalf("block has %d txs, want 1 (the bad-nonce candidate is skipped)", len(blk.Transactions))
	}
	if len(sink.receipts) != 1 {
		t.Fatalf("got %d receipts, want 1", len(sink.receipts))
	}
	if got := sink.receipts[0].TxIndex; got != 0 {
		t.Fatalf("receipt TxIndex = %d, want 0 — the tx is at position 0 of the block, "+
			"not position %d of the candidate list", got, got)
	}
	for i, l := range sink.receipts[0].Logs {
		if l.TxIndex != 0 {
			t.Fatalf("log %d carries TxIndex %d, want 0", i, l.TxIndex)
		}
	}
}
