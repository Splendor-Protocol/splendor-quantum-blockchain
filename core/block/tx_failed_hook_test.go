package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// sqc_txs_failed_total is driven from the executor: the producer reports the
// candidates it executed and dropped plus every receipt it committed as
// failed, and a follower applying the same block reports the failed receipts
// it committed. Nothing drove the counter before, so the "Transactions
// failed" and "Failure ratio" panels read zero by construction.
func TestTxFailedHookCountsDroppedAndFailedReceipts(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x66)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	var produced, applied int
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	exA.WithForks(revertForks()).WithTxFailedHook(func(n int) { produced += n })
	fund(t, sdbA, from, start)

	good := signedTransfer(t, from, priv, to, 100, 0)
	soft := softFailingCall(t, from, priv, 1)                 // packed with a failed receipt
	broke := signedTransfer(t, from, priv, to, 10_000_000, 2) // its turn, unaffordable: dropped
	b1, skipped, err := exA.ProduceBlockReport([]*types.Transaction{good, soft, broke}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 2 || len(skipped) != 1 {
		t.Fatalf("block packed %d txs and skipped %d, want 2 and 1", len(b1.Transactions), len(skipped))
	}
	if produced != 2 {
		t.Fatalf("producer reported %d failed txs, want 2 (one dropped, one failed receipt)", produced)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(revertForks()).WithTxFailedHook(func(n int) { applied += n })
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied != 1 {
		t.Fatalf("follower reported %d failed txs, want 1 (the failed receipt; nothing was dropped)", applied)
	}
}

// A block with nothing to report never calls the hook, and an executor with
// no hook settles a failing block without one.
func TestTxFailedHookSilentWhenNothingFailed(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x66)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	calls := 0
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(revertForks()).WithTxFailedHook(func(int) { calls++ })
	fund(t, sdb, from, 1_000_000)
	b1, err := ex.ProduceBlock([]*types.Transaction{signedTransfer(t, from, priv, to, 100, 0)}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if calls != 0 {
		t.Fatalf("hook called %d times for a clean block, want 0", calls)
	}

	ex.WithTxFailedHook(nil)
	if _, err := ex.ProduceBlock([]*types.Transaction{softFailingCall(t, from, priv, 1)}, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2); err != nil {
		t.Fatalf("produce without a hook: %v", err)
	}
}
