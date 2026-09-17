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
	if got.BlockHash != blk.Hash {
		t.Fatalf("produce-path backfill BlockHash = %x, want %x", got.BlockHash[:8], blk.Hash[:8])
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

func TestReconcileTxIndexFillsHoleAfterLaterRecord(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)

	var blocks []*types.Block
	var hashes [][32]byte
	prev := [32]byte{}
	for i, nonce := range []uint64{0, 1, 2} {
		tx := signedTransfer(t, from, priv, testAddr(byte(0x70+i)), 10, nonce)
		blk, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, prev, uint64(i+1))
		if err != nil {
			t.Fatalf("produce %d: %v", i+1, err)
		}
		h, err := tx.FullHash()
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, blk)
		hashes = append(hashes, h)
		prev = blk.Hash
	}

	idx, err := state.NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	r1 := &types.Receipt{TxHash: hashes[0], BlockHeight: 1, BlockHash: blocks[0].Hash}
	r3 := &types.Receipt{TxHash: hashes[2], BlockHeight: 3, BlockHash: blocks[2].Hash}
	if err := idx.RecordBlock(blocks[0], []*types.Receipt{r1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.RecordBlock(blocks[2], []*types.Receipt{r3}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 1 {
		t.Fatalf("index jumped hole: (%d,%v)", h, ok)
	}
	ex.WithTxIndex(idx)
	if err := ex.ReconcileTxIndex(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := idx.GetReceipt(hashes[1])
	if err != nil || got == nil || got.BlockHeight != 2 {
		t.Fatalf("hole receipt = %v err=%v", got, err)
	}
	if got.BlockHash != blocks[1].Hash {
		t.Fatalf("hole BlockHash = %x, want %x", got.BlockHash[:8], blocks[1].Hash[:8])
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 3 {
		t.Fatalf("indexed after hole fill = (%d,%v)", h, ok)
	}
}

func TestReconcileTxIndexRemakeKeepsLaterIndexedHeight(t *testing.T) {
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
	if err := idx.MarkSnapshotCoverage(9); err != nil {
		t.Fatal(err)
	}
	if err := idx.RecordBlock(later, nil); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 10 {
		t.Fatalf("indexed = (%d,%v)", h, ok)
	}
	if err := idx.ClearCoverageFrom(); err != nil {
		t.Fatal(err)
	}
	if err := ex.ReconcileTxIndex(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if h, ok := idx.CoverageFrom(); !ok || h != 9 {
		t.Fatalf("coverage = (%d,%v)", h, ok)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 10 {
		t.Fatalf("indexed after remake = (%d,%v), want 10", h, ok)
	}
}

func TestProduceReceiptCarriesBlockHash(t *testing.T) {
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

	tx := signedTransfer(t, from, priv, testAddr(0x62), 100, 0)
	blk, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	txHash, err := tx.FullHash()
	if err != nil {
		t.Fatal(err)
	}
	got, err := idx.GetReceipt(txHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockHash != blk.Hash {
		t.Fatalf("receipt BlockHash = %x, want %x", got.BlockHash[:8], blk.Hash[:8])
	}
}
