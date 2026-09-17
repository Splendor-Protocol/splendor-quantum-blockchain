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
	if err := idx.Backfill(4, 5, func(h uint64) (*types.Block, error) { return blocks[h], nil }); err != nil {
		t.Fatal(err)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 5 {
		t.Fatalf("after backfill indexed = (%d,%v)", h, ok)
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
