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
	b := &types.Block{Header: types.BlockHeader{Height: 3}, Hash: [32]byte{0x03}}
	if err := idx.RecordBlock(b, nil); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 3 {
		t.Fatalf("indexed height = (%d,%v), want 3", h, ok)
	}

	blocks := map[uint64]*types.Block{
		4: {Header: types.BlockHeader{Height: 4}, Hash: [32]byte{0x04}},
		5: {Header: types.BlockHeader{Height: 5}, Hash: [32]byte{0x05}},
	}
	rec4 := &types.Receipt{TxHash: [32]byte{0x44}, BlockHeight: 4}
	if err := idx.Backfill(4, 5, func(h uint64) (*types.Block, []*types.Receipt, error) {
		if h == 4 {
			return blocks[h], []*types.Receipt{rec4}, nil
		}
		return blocks[h], nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 5 {
		t.Fatalf("after backfill indexed = (%d,%v)", h, ok)
	}
	got, err := idx.GetReceipt([32]byte{0x44})
	if err != nil || got == nil || got.BlockHeight != 4 {
		t.Fatalf("backfill did not restore receipt: got=%v err=%v", got, err)
	}
}

func TestTxIndexRecordBlockStoresReceiptsWithHeight(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	b := &types.Block{Header: types.BlockHeader{Height: 2}, Hash: [32]byte{0x02}}
	rcpt := &types.Receipt{TxHash: [32]byte{0x21}, BlockHeight: 2}
	if err := idx.RecordBlock(b, []*types.Receipt{rcpt}); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 2 {
		t.Fatalf("indexed = (%d,%v)", h, ok)
	}
	got, err := idx.GetReceipt([32]byte{0x21})
	if err != nil || got == nil || got.BlockHeight != 2 {
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
