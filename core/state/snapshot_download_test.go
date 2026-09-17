package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func testDownloadLeaves() []types.StateLeaf {
	return []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
	}
}

func TestSnapshotDownloadRoundTripAndResume(t *testing.T) {
	dir := t.TempDir()
	leaves := testDownloadLeaves()
	root := SnapshotRoot(leaves)
	head := &types.Block{Header: types.BlockHeader{Height: 7, StateRoot: root, ChainID: "test"}, Hash: [32]byte{0x11}}

	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(head, 7); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves[:2], leaves[1].Key, false, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if d.Done() || d.LeafBytes() == 0 || !bytes.Equal(d.AfterKey(), leaves[1].Key) {
		t.Fatalf("progress after first page: done=%v bytes=%d after=%q", d.Done(), d.LeafBytes(), d.AfterKey())
	}

	// Reopen: cursor and integrity must survive.
	d2, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if d2.SessionID() != "sess-1" || !bytes.Equal(d2.AfterKey(), leaves[1].Key) || d2.BlockHash() != head.Hash {
		t.Fatalf("reopen progress = %+v", d2.Progress())
	}
	if err := d2.AppendPage(leaves[1:], leaves[2].Key, true, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if !d2.Done() {
		t.Fatal("expected done after final page")
	}
	if err := d2.VerifyRoot(); err != nil {
		t.Fatal(err)
	}
	snap, err := d2.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Leaves) != 3 || SnapshotRoot(snap.Leaves) != root {
		t.Fatalf("assembled %d leaves, root mismatch", len(snap.Leaves))
	}
}

func TestSnapshotDownloadRefusesDifferentHead(t *testing.T) {
	dir := t.TempDir()
	leaves := testDownloadLeaves()
	head := &types.Block{Header: types.BlockHeader{Height: 1, StateRoot: SnapshotRoot(leaves)}, Hash: [32]byte{0x01}}
	other := &types.Block{Header: types.BlockHeader{Height: 1, StateRoot: SnapshotRoot(leaves)}, Hash: [32]byte{0x02}}
	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(head, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(other, 1); !errors.Is(err, ErrSnapshotDownloadMismatch) {
		t.Fatalf("AcceptHead different hash = %v, want mismatch", err)
	}
	if d.BlockHash() != head.Hash {
		t.Fatal("mismatch mutated staged identity")
	}
}

func TestSnapshotDownloadEnforcesDiskBudget(t *testing.T) {
	dir := t.TempDir()
	leaves := testDownloadLeaves()
	head := &types.Block{Header: types.BlockHeader{Height: 1, StateRoot: SnapshotRoot(leaves)}, Hash: [32]byte{0x03}}
	d, err := OpenSnapshotDownload(dir, 8) // smaller than one leaf pair
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(head, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves[:1], leaves[0].Key, false, ""); !errors.Is(err, ErrSnapshotDownloadTooLarge) {
		t.Fatalf("tiny disk budget = %v, want ErrSnapshotDownloadTooLarge", err)
	}
}

func TestSnapshotDownloadTruncatesTornTail(t *testing.T) {
	dir := t.TempDir()
	leaves := testDownloadLeaves()
	head := &types.Block{Header: types.BlockHeader{Height: 1, StateRoot: SnapshotRoot(leaves[:1])}, Hash: [32]byte{0x04}}
	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(head, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves[:1], leaves[0].Key, false, "s"); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after writing extra bytes and before progress.
	if err := os.WriteFile(filepath.Join(dir, snapshotDownloadLeavesFile),
		append(mustRead(t, filepath.Join(dir, snapshotDownloadLeavesFile)), []byte("{\"key\":\"e30=\",\"value\":\"e30=\"}\n")...),
		0o600); err != nil {
		t.Fatal(err)
	}
	d2, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Progress().LeafCount != 1 {
		t.Fatalf("torn tail should be dropped back to 1 leaf, got %d", d2.Progress().LeafCount)
	}
}

func TestImportSnapshotJSONLRoundTrip(t *testing.T) {
	leaves := testDownloadLeaves()
	root := SnapshotRoot(leaves)
	dir := t.TempDir()
	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	head := &types.Block{Header: types.BlockHeader{Height: 4, StateRoot: root}, Hash: [32]byte{0x05}}
	if err := d.AcceptHead(head, 4); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves, nil, true, ""); err != nil {
		t.Fatal(err)
	}

	dst, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportSnapshotJSONL(d.leavesPath(), 4, root); err != nil {
		t.Fatal(err)
	}
	if h, ok := dst.AppliedHeight(); !ok || h != 4 {
		t.Fatalf("applied height = (%d,%v)", h, ok)
	}
	if got, _ := dst.StateRoot(); got != root {
		t.Fatalf("imported root %x != %x", got[:8], root[:8])
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
