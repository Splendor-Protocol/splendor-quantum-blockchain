package block

import (
	"errors"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The producer judges a candidate's nonce before it does anything else with
// it, and reports what it left out and why: a stale nonce and a transaction
// that failed at its turn will never apply and are for the pool to drop; a
// nonce ahead of the sender's next is kept. The block is the block it always
// built.
func TestProducerReportsWhyCandidatesWereLeftOut(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)
	b1, err := ex.ProduceBlock([]*types.Transaction{signedTransfer(t, from, priv, to, 100, 0)}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("block 1: %v", err)
	}

	stale := signedTransfer(t, from, priv, to, 1, 0)          // already applied
	good := signedTransfer(t, from, priv, to, 1, 1)           // the sender's next
	broke := signedTransfer(t, from, priv, to, 10_000_000, 2) // its turn, unaffordable
	gap := signedTransfer(t, from, priv, to, 1, 3)            // behind the failure
	b2, skipped, err := ex.ProduceBlockReport([]*types.Transaction{stale, good, broke, gap}, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2)
	if err != nil {
		t.Fatalf("block 2: %v", err)
	}
	if len(b2.Transactions) != 1 || b2.Transactions[0].Nonce != 1 {
		t.Fatalf("block 2 packed %d txs, want exactly the nonce-1 transfer", len(b2.Transactions))
	}
	if len(skipped) != 3 {
		t.Fatalf("%d candidates reported skipped, want 3", len(skipped))
	}
	byNonce := map[uint64]SkippedTx{}
	for _, s := range skipped {
		byNonce[s.Tx.Nonce] = s
	}
	if s := byNonce[0]; !errors.Is(s.Err, types.ErrInvalidNonce) || !s.Unappliable() {
		t.Fatalf("stale nonce reported as %v (unappliable=%v), want ErrInvalidNonce, to drop", s.Err, s.Unappliable())
	}
	if s := byNonce[2]; !errors.Is(s.Err, types.ErrInsufficientBalance) || !s.Unappliable() {
		t.Fatalf("failure at its turn reported as %v (unappliable=%v), want ErrInsufficientBalance, to drop", s.Err, s.Unappliable())
	}
	if s := byNonce[3]; !errors.Is(s.Err, ErrNonceGap) || s.Unappliable() {
		t.Fatalf("nonce behind a failure reported as %v (unappliable=%v), want ErrNonceGap, to keep", s.Err, s.Unappliable())
	}
	if n, _ := sdb.GetNonce(from); n != 2 {
		t.Fatalf("sender nonce %d after block 2, want 2", n)
	}
}

// A candidate the producer cannot pack is refused on its nonce before its
// signature is looked at, and a transaction at its turn is still held to its
// signature. Every queued entry used to cost an ML-DSA-65 verification on
// every block for as long as it queued.
func TestNonceIsJudgedBeforeTheSignature(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)

	forge := func(nonce uint64) *types.Transaction {
		tx := signedTransfer(t, from, priv, to, 1, nonce)
		tx.Signature[0] ^= 0xFF // no longer verifies
		return tx
	}
	ahead, atTurn := forge(5), forge(0)
	_, skipped, err := ex.BuildProposalReport([]*types.Transaction{ahead, atTurn}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 2 {
		t.Fatalf("%d skipped, want both", len(skipped))
	}
	for _, s := range skipped {
		switch s.Tx.Nonce {
		case 5:
			if !errors.Is(s.Err, ErrNonceGap) {
				t.Fatalf("the nonce ahead was executed as far as %v, want it left out as a gap", s.Err)
			}
		case 0:
			if !errors.Is(s.Err, types.ErrInvalidSignature) {
				t.Fatalf("the forged signature at its turn was reported as %v", s.Err)
			}
		}
	}

	// A follower applying a block that carries the forged, out-of-turn
	// transaction still rejects it — on the nonce, which is judged first.
	bad := forgeBlock(t, []*types.Transaction{ahead}, vaddr, vpriv, 1, [32]byte{})
	if err := ex.ExecuteBlock(bad); !errors.Is(err, types.ErrInvalidNonce) {
		t.Fatalf("follower judged the block as %v, want the nonce refused", err)
	}
}
