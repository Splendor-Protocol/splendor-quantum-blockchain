package state

import (
	"errors"
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

// A node upgraded from a build that wrote no watermark: every block was
// indexed live (tx list and receipts) but meta:txindex_height is absent. The
// first backfill must walk the chain once, read-only, and end with the
// watermark at the head. More heights than backfillFlushEvery so the periodic
// flush path runs too.
func TestTxIndexBackfillCompletesLegacyIndexWithoutWatermark(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	const n = uint64(backfillFlushEvery*2 + 37)
	blocks := make(map[uint64]*types.Block, n+1)
	for h := uint64(0); h <= n; h++ {
		b := &types.Block{Header: types.BlockHeader{Height: h}, Hash: [32]byte{byte(h), byte(h >> 8), 0xaa}}
		var receipts []*types.Receipt
		if h%3 == 1 {
			tx := &types.Transaction{Version: 1, Nonce: h}
			b.Transactions = []*types.Transaction{tx}
			hash, herr := tx.FullHash()
			if herr != nil {
				t.Fatal(herr)
			}
			receipts = []*types.Receipt{{TxHash: hash, BlockHeight: h}}
		}
		if err := idx.IndexBlock(b); err != nil {
			t.Fatal(err)
		}
		if err := idx.StoreBlockReceipts(receipts); err != nil {
			t.Fatal(err)
		}
		blocks[h] = b
	}
	if _, ok := idx.IndexedHeight(); ok {
		t.Fatal("legacy writes must not create a watermark")
	}

	loads := 0
	err = idx.Backfill(0, n, func(h uint64) (*types.Block, []*types.Receipt, error) {
		loads++
		return blocks[h], nil, nil // the chain store of an old build holds no receipts
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := idx.IndexedHeight(); !ok || got != n {
		t.Fatalf("indexed after legacy backfill = (%d,%v), want %d", got, ok, n)
	}
	if loads != int(n)+1 {
		t.Fatalf("first walk loaded %d heights, want %d", loads, n+1)
	}

	// The second walk is free below the watermark: only the head is re-read.
	loads = 0
	if err := idx.Backfill(0, n, func(h uint64) (*types.Block, []*types.Receipt, error) {
		loads++
		return blocks[h], nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("second walk loaded %d heights, want 1 (the watermark itself)", loads)
	}
}

func TestTxIndexBackfillSkipsHeightsBelowWatermark(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	blocks := map[uint64]*types.Block{}
	for h := uint64(1); h <= 7; h++ {
		blocks[h] = &types.Block{Header: types.BlockHeader{Height: h}, Hash: [32]byte{byte(h)}}
	}
	for h := uint64(1); h <= 5; h++ {
		if err := idx.RecordBlock(blocks[h], nil); err != nil {
			t.Fatal(err)
		}
	}
	var loaded []uint64
	if err := idx.Backfill(0, 7, func(h uint64) (*types.Block, []*types.Receipt, error) {
		loaded = append(loaded, h)
		return blocks[h], nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 || loaded[0] != 5 || loaded[2] != 7 {
		t.Fatalf("loaded %v, want [5 6 7]", loaded)
	}
	if h, ok := idx.IndexedHeight(); !ok || h != 7 {
		t.Fatalf("indexed = (%d,%v), want 7", h, ok)
	}
}

// A height whose receipts are in neither store can never complete. The walk
// must say so, hold the watermark below it, and still visit later heights
// so a repairable hole above it is not left behind.
func TestTxIndexBackfillReportsAHeightNothingCanComplete(t *testing.T) {
	idx, err := NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	blocks := map[uint64]*types.Block{}
	for h := uint64(1); h <= 5; h++ {
		blocks[h] = &types.Block{Header: types.BlockHeader{Height: h}, Hash: [32]byte{byte(h)}}
	}
	blocks[3].Transactions = []*types.Transaction{{Version: 1, Nonce: 3}}

	for round := 0; round < 2; round++ {
		var loaded []uint64
		err := idx.Backfill(0, 5, func(h uint64) (*types.Block, []*types.Receipt, error) {
			loaded = append(loaded, h)
			return blocks[h], nil, nil
		})
		if !errors.Is(err, ErrTxIndexIncomplete) {
			t.Fatalf("round %d: err = %v, want ErrTxIndexIncomplete", round, err)
		}
		if h, ok := idx.IndexedHeight(); !ok || h != 2 {
			t.Fatalf("round %d: indexed = (%d,%v), want 2", round, h, ok)
		}
		if last := loaded[len(loaded)-1]; last != 5 {
			t.Fatalf("round %d: did not walk past the hole: %v", round, loaded)
		}
	}
	// The hole's own tx list was still written, so the block stays readable,
	// and the heights above it were indexed too.
	for _, h := range []uint64{3, 4, 5} {
		if _, err := idx.db.Get(blockTxsKey(h), nil); err != nil {
			t.Fatalf("tx list for %d missing: %v", h, err)
		}
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
