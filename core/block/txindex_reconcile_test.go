package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestReconcileTxIndexRestoresLostReceipts(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	idx, err := state.NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	ex.WithTxIndex(idx)
	fund(t, sdb, from, 1_000_000)

	tx := signedTransfer(t, from, priv, testAddr(0x61), 100, 0)
	blk, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	txHash, err := tx.FullHash()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.GetReceipt(txHash); err != nil {
		t.Fatalf("receipt missing after produce: %v", err)
	}

	idx2, err := state.NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx2.Close()
	ex.WithTxIndex(idx2)
	if _, err := idx2.GetReceipt(txHash); err == nil {
		t.Fatal("fresh index already had the receipt")
	}
	if err := ex.ReconcileTxIndex(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := idx2.GetReceipt(txHash)
	if err != nil || got == nil || got.BlockHeight != 1 {
		t.Fatalf("reconcile did not restore receipt: got=%v err=%v", got, err)
	}
	if h, ok := idx2.IndexedHeight(); !ok || h != blk.Header.Height {
		t.Fatalf("indexed height after reconcile = (%d,%v), want %d", h, ok, blk.Header.Height)
	}
}

func TestReconcileTxIndexRemakesLostSnapshotCoverage(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	idx, err := state.NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	ex.WithTxIndex(idx)

	head := &types.Block{Header: types.BlockHeader{Height: 9}, Hash: [32]byte{0x09}}
	later := &types.Block{Header: types.BlockHeader{Height: 10}, Hash: [32]byte{0x0a}}
	if err := ex.storeBlock(head); err != nil {
		t.Fatal(err)
	}
	if err := ex.storeBlock(later); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.CommitWithHeight(10); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.CoverageFrom(); ok {
		t.Fatal("coverage should start unset")
	}
	if err := ex.ReconcileTxIndex(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if h, ok := idx.CoverageFrom(); !ok || h != 9 {
		t.Fatalf("coverage = (%d,%v), want 9 after lost snapshot mark", h, ok)
	}
}
