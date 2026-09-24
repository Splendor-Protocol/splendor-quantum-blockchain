package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
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

// A bounded-batch import that was interrupted after leaf values were published
// but before the height marker is resumable: CanResumeSnapshotImport reports
// true, and ImportSnapshotJSONLResume re-writes the same verified leaves and
// commits the height marker.
func TestImportSnapshotJSONLResumeAfterPartialPublish(t *testing.T) {
	leaves := testDownloadLeaves()
	root := SnapshotRoot(leaves)
	dir := t.TempDir()
	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	head := &types.Block{Header: types.BlockHeader{Height: 9, StateRoot: root}, Hash: [32]byte{0x09}}
	if err := d.AcceptHead(head, 9); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves, nil, true, ""); err != nil {
		t.Fatal(err)
	}

	statePath := t.TempDir()
	dst, err := NewStateDB(statePath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted publish: write the leaf values directly, no
	// height marker, then reopen so loadLeaves repopulates the in-memory index
	// from the partial DB — exactly the state a restart would observe.
	idx := make(map[string][32]byte)
	partial := new(leveldb.Batch)
	for _, l := range leaves {
		idx[string(l.Key)] = qcrypto.Hash32(l.Value)
		partial.Put(l.Key, l.Value)
	}
	if err := persist.Write(dst.db, partial); err != nil {
		t.Fatal(err)
	}
	dst.Close()

	dst, err = NewStateDB(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if h, ok := dst.AppliedHeight(); ok || h != 0 {
		t.Fatalf("partial publish must have no height marker, got (%d,%v)", h, ok)
	}
	if dst.IsFresh() {
		t.Fatal("partial publish must not be fresh")
	}
	if !dst.CanResumeSnapshotImport() {
		t.Fatal("partial publish must be resumable")
	}

	// Resuming over the partial publish must succeed and commit the height.
	if err := dst.ImportSnapshotJSONLResume(d.leavesPath(), 9, root); err != nil {
		t.Fatalf("resume import: %v", err)
	}
	if h, ok := dst.AppliedHeight(); !ok || h != 9 {
		t.Fatalf("resumed height = (%d,%v)", h, ok)
	}
	if got, _ := dst.StateRoot(); got != root {
		t.Fatalf("resumed root %x != %x", got[:8], root[:8])
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

func TestSnapshotDownloadResetQuarantinesObsolete(t *testing.T) {
	dir := t.TempDir()
	leaves := testDownloadLeaves()
	head := &types.Block{Header: types.BlockHeader{Height: 2, StateRoot: SnapshotRoot(leaves)}, Hash: [32]byte{0x05}}
	d, err := OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(head, 2); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves[:1], leaves[0].Key, false, "sess-old"); err != nil {
		t.Fatal(err)
	}
	if !d.HasHead() {
		t.Fatal("expected a staged head before reset")
	}

	// Reset quarantines the obsolete staging and returns to empty.
	if err := d.Reset(); err != nil {
		t.Fatal(err)
	}
	if d.HasHead() || d.Height() != 0 || d.LeafBytes() != 0 || d.Done() {
		t.Fatalf("after reset: head=%v height=%d bytes=%d done=%v", d.HasHead(), d.Height(), d.LeafBytes(), d.Done())
	}
	// The obsolete staging is preserved under an .obsolete- suffix, not deleted.
	matches, _ := filepath.Glob(dir + ".obsolete-*")
	if len(matches) == 0 {
		t.Fatal("obsolete staging was not quarantined (no .obsolete-* directory)")
	}

	// A fresh transfer against a new head must proceed normally.
	newHead := &types.Block{Header: types.BlockHeader{Height: 9, StateRoot: SnapshotRoot(leaves)}, Hash: [32]byte{0x06}}
	if err := d.AcceptHead(newHead, 9); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(leaves, leaves[2].Key, true, "sess-new"); err != nil {
		t.Fatal(err)
	}
	if !d.Done() {
		t.Fatal("expected done after fresh transfer")
	}

	// A completed download is never reset (its leaves are import evidence).
	if err := d.Reset(); err == nil {
		t.Fatal("Reset on a completed download should be refused")
	}
}
