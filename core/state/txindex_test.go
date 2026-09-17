package state

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestTxIndexRecordsHeightAndBackfills(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, ok := idx.IndexedHeight(); ok {
		t.Fatal("fresh index should have no height")
	}
	b := &types.Block{Header: types.BlockHeader{Height: 1}, Hash: [32]byte{0x01}}
	if err := idx.RecordBlock(b, nil); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 1 {
		t.Fatalf("indexed height = (%d,%v), want 1", h, ok)
	}

	blocks := map[uint64]*types.Block{
		2: {Header: types.BlockHeader{Height: 2}, Hash: [32]byte{0x02}},
		3: {Header: types.BlockHeader{Height: 3}, Hash: [32]byte{0x03}},
	}
	rec2 := &types.Receipt{TxHash: [32]byte{0x22}, BlockHeight: 2}
	if err := idx.Backfill(2, 3, func(h uint64) (*types.Block, []*types.Receipt, error) {
		if h == 2 {
			return blocks[h], []*types.Receipt{rec2}, nil
		}
		return blocks[h], nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 3 {
		t.Fatalf("after backfill indexed = (%d,%v)", h, ok)
	}
	got, err := idx.GetReceipt([32]byte{0x22})
	if err != nil || got == nil || got.BlockHeight != 2 {
		t.Fatalf("backfill did not restore receipt: got=%v err=%v", got, err)
	}
}

func TestTxIndexRecordBlockStoresReceiptsWithHeight(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	b := &types.Block{Header: types.BlockHeader{Height: 1}, Hash: [32]byte{0x02}}
	rcpt := &types.Receipt{TxHash: [32]byte{0x21}, BlockHeight: 1}
	if err := idx.RecordBlock(b, []*types.Receipt{rcpt}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 1 {
		t.Fatalf("indexed = (%d,%v)", h, ok)
	}
	got, err := idx.GetReceipt([32]byte{0x21})
	if err != nil || got == nil || got.BlockHeight != 1 {
		t.Fatalf("receipt missing after RecordBlock: %v %v", got, err)
	}
}

func TestTxIndexSnapshotCoverage(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.MarkSnapshotCoverage(9); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.CoverageFrom(); !ok || h != 9 {
		t.Fatalf("coverage = (%d,%v)", h, ok)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 9 {
		t.Fatalf("indexed after snapshot mark = (%d,%v)", h, ok)
	}
}

func TestTxIndexDoesNotAdvancePastAHole(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	b1 := &types.Block{Header: types.BlockHeader{Height: 1}, Hash: [32]byte{0x01}}
	b3 := &types.Block{Header: types.BlockHeader{Height: 3}, Hash: [32]byte{0x03}}
	r3 := &types.Receipt{TxHash: [32]byte{0x33}, BlockHeight: 3}
	if err := idx.RecordBlock(b1, nil); err != nil {
		t.Fatal(err)
	}
	if err := idx.RecordBlock(b3, []*types.Receipt{r3}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 1 {
		t.Fatalf("watermark jumped the hole: (%d,%v)", h, ok)
	}
	if _, err := idx.GetReceipt([32]byte{0x33}); err != nil {
		t.Fatal(err)
	}
	b2 := &types.Block{Header: types.BlockHeader{Height: 2}, Hash: [32]byte{0x02}}
	r2 := &types.Receipt{TxHash: [32]byte{0x22}, BlockHeight: 2}
	if err := idx.Backfill(2, 3, func(h uint64) (*types.Block, []*types.Receipt, error) {
		if h == 2 {
			return b2, []*types.Receipt{r2}, nil
		}
		return b3, []*types.Receipt{r3}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 3 {
		t.Fatalf("after filling hole indexed = (%d,%v)", h, ok)
	}
	if got, err := idx.GetReceipt([32]byte{0x22}); err != nil || got.BlockHeight != 2 {
		t.Fatalf("hole receipt = %v %v", got, err)
	}
}

func TestMarkSnapshotCoverageKeepsLaterIndexedHeight(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.MarkSnapshotCoverage(9); err != nil {
		t.Fatal(err)
	}
	b10 := &types.Block{Header: types.BlockHeader{Height: 10}, Hash: [32]byte{0x0a}}
	if err := idx.RecordBlock(b10, nil); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 10 {
		t.Fatalf("indexed = (%d,%v)", h, ok)
	}
	if err := idx.MarkSnapshotCoverage(9); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 10 {
		t.Fatalf("remake clobbered indexed height: (%d,%v)", h, ok)
	}
	if h, ok := idx.CoverageFrom(); !ok || h != 9 {
		t.Fatalf("coverage = (%d,%v)", h, ok)
	}
}
