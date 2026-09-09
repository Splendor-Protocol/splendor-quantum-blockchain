package state

import (
	"bytes"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func openState(t *testing.T) (*StateDB, string) {
	t.Helper()
	dir := t.TempDir()
	sdb, err := NewStateDB(dir)
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb, dir
}

func commitAccount(t *testing.T, sdb *StateDB, a byte, balance int64, height uint64) [32]byte {
	t.Helper()
	acc := types.NewAccount(addr(a))
	acc.AddBalance(big.NewInt(balance))
	sdb.SetAccount(acc)
	root, err := sdb.CommitWithHeight(height)
	if err != nil {
		t.Fatalf("commit %d: %v", height, err)
	}
	return root
}

// A state without an applied-height marker predates height markers (or is a
// follower that only ever applied genesis allocations); nothing anchors it to
// a block, so it cannot be served.
func TestExportSnapshotRefusesWithoutHeightMarker(t *testing.T) {
	sdb, _ := openState(t)
	acc := types.NewAccount(addr(0x11))
	acc.AddBalance(big.NewInt(1))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sdb.ExportSnapshot(1 << 20); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("export without a height marker = %v, want ErrSnapshotUnavailable", err)
	}
}

// The export is a point-in-time view: however many commits race it, the
// leaves it returns hash to exactly the root the height it reports committed.
func TestExportSnapshotIsConsistentUnderConcurrentCommit(t *testing.T) {
	sdb, _ := openState(t)
	var mu sync.Mutex
	roots := map[uint64][32]byte{}
	commit := func(h uint64) error {
		acc := types.NewAccount(addr(byte(h % 200)))
		acc.AddBalance(big.NewInt(int64(h)))
		sdb.SetAccount(acc)
		root, err := sdb.CommitWithHeight(h)
		if err != nil {
			return err
		}
		mu.Lock()
		roots[h] = root
		mu.Unlock()
		return nil
	}
	if err := commit(1); err != nil {
		t.Fatal(err)
	}

	const last = 400
	done := make(chan error, 1)
	go func() {
		for h := uint64(2); h <= last; h++ {
			if err := commit(h); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	// The root is recorded after the commit returns, so an export may see a
	// height the map does not hold yet; wait for it rather than call that a
	// torn view.
	rootAt := func(h uint64) ([32]byte, bool) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			r, ok := roots[h]
			mu.Unlock()
			if ok || time.Now().After(deadline) {
				return r, ok
			}
			time.Sleep(time.Millisecond)
		}
	}

	exports := 0
	for running := true; running; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("commit goroutine: %v", err)
			}
			running = false
		default:
		}
		height, leaves, err := sdb.ExportSnapshot(64 << 20)
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		want, ok := rootAt(height)
		if !ok {
			t.Fatalf("exported height %d was never committed", height)
		}
		if got := SnapshotRoot(leaves); got != want {
			t.Fatalf("export at height %d hashes to %x, committed root was %x (torn view)", height, got[:8], want[:8])
		}
		exports++
	}
	if exports < 2 {
		t.Fatalf("only %d exports ran, the race was not exercised", exports)
	}
	height, _, err := sdb.ExportSnapshot(64 << 20)
	if err != nil || height != last {
		t.Fatalf("final export height %d err=%v, want %d", height, err, last)
	}
}

// meta: keys are node-local bookkeeping (fork schedule, minted supply, the
// height marker) and never travel: the importer takes those from its own
// pinned genesis document.
func TestExportSnapshotExcludesMetaKeys(t *testing.T) {
	sdb, _ := openState(t)
	commitAccount(t, sdb, 0x11, 5, 1)
	if err := sdb.PutMeta("forks", []byte(`{"multi_validator":1}`)); err != nil {
		t.Fatal(err)
	}
	_, leaves, err := sdb.ExportSnapshot(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 1 {
		t.Fatalf("got %d leaves, want the single account", len(leaves))
	}
	for _, l := range leaves {
		if bytes.HasPrefix(l.Key, []byte(prefixMeta)) {
			t.Fatalf("meta key %q exported", l.Key)
		}
	}
}

func TestExportSnapshotRefusesOversize(t *testing.T) {
	sdb, _ := openState(t)
	commitAccount(t, sdb, 0x11, 5, 1)
	if _, _, err := sdb.ExportSnapshot(8); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("export past the cap = %v, want ErrSnapshotTooLarge", err)
	}
}

// A fresh node imports what a peer exported and ends up on the same root and
// height, durably.
func TestImportSnapshotRoundTrip(t *testing.T) {
	src, _ := openState(t)
	for h := uint64(1); h <= 3; h++ {
		commitAccount(t, src, byte(0x10+h), int64(100*h), h)
	}
	height, leaves, err := src.ExportSnapshot(64 << 20)
	if err != nil {
		t.Fatal(err)
	}
	srcRoot, _ := src.StateRoot()
	if got := SnapshotRoot(leaves); got != srcRoot {
		t.Fatalf("SnapshotRoot %x != committed root %x", got[:8], srcRoot[:8])
	}

	dst, dir := openState(t)
	if err := dst.ImportSnapshot(leaves, height, srcRoot); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got, _ := dst.StateRoot(); got != srcRoot {
		t.Fatalf("root after import %x, want %x", got[:8], srcRoot[:8])
	}
	if h, ok := dst.AppliedHeight(); !ok || h != 3 {
		t.Fatalf("applied height after import = (%d, %v), want (3, true)", h, ok)
	}
	if bal, _ := dst.GetBalance(addr(0x12)); bal.Cmp(big.NewInt(200)) != 0 {
		t.Fatalf("imported balance = %s, want 200", bal)
	}

	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, _ := reopened.StateRoot(); got != srcRoot {
		t.Fatalf("root after reopen %x, want %x", got[:8], srcRoot[:8])
	}
	if h, ok := reopened.AppliedHeight(); !ok || h != 3 {
		t.Fatalf("applied height after reopen = (%d, %v), want (3, true)", h, ok)
	}
}

// A root mismatch writes nothing: the state stays empty and unmarked.
func TestImportSnapshotRefusesRootMismatch(t *testing.T) {
	dst, _ := openState(t)
	leaves := []types.StateLeaf{{Key: []byte("acc:SQ:x"), Value: []byte(`{}`)}}
	var wrong [32]byte
	wrong[0] = 0xff
	if err := dst.ImportSnapshot(leaves, 5, wrong); !errors.Is(err, ErrSnapshotRootMismatch) {
		t.Fatalf("import with a wrong root = %v, want ErrSnapshotRootMismatch", err)
	}
	if got, _ := dst.StateRoot(); got != stateEmptyRoot {
		t.Fatal("a refused import wrote leaves")
	}
	if _, ok := dst.AppliedHeight(); ok {
		t.Fatal("a refused import wrote the height marker")
	}
}

// Snapshot leaves that carry meta: keys would overwrite this node's own
// bookkeeping (minted supply, fork schedule) from the wire. Refused even
// though they would also fail the root.
func TestImportSnapshotRefusesMetaAndDuplicateKeys(t *testing.T) {
	dst, _ := openState(t)
	meta := []types.StateLeaf{{Key: []byte("meta:native_minted"), Value: []byte("1")}}
	if err := dst.ImportSnapshot(meta, 1, SnapshotRoot(meta)); err == nil {
		t.Fatal("a meta key was imported")
	}
	dup := []types.StateLeaf{
		{Key: []byte("acc:SQ:x"), Value: []byte(`{"a":1}`)},
		{Key: []byte("acc:SQ:x"), Value: []byte(`{"a":2}`)},
	}
	if err := dst.ImportSnapshot(dup, 1, SnapshotRoot(dup)); err == nil {
		t.Fatal("duplicate keys were imported")
	}
}

// Only an empty, unmarked state with nothing pending may be replaced.
func TestImportSnapshotRefusesNonFreshState(t *testing.T) {
	src, _ := openState(t)
	commitAccount(t, src, 0x11, 5, 1)
	height, leaves, err := src.ExportSnapshot(64 << 20)
	if err != nil {
		t.Fatal(err)
	}
	root := SnapshotRoot(leaves)

	committed, _ := openState(t)
	commitAccount(t, committed, 0x22, 1, 1)
	if err := committed.ImportSnapshot(leaves, height, root); !errors.Is(err, ErrStateNotFresh) {
		t.Fatalf("import over committed leaves = %v, want ErrStateNotFresh", err)
	}

	pending, _ := openState(t)
	pending.SetAccount(types.NewAccount(addr(0x33)))
	if err := pending.ImportSnapshot(leaves, height, root); !errors.Is(err, ErrStateNotFresh) {
		t.Fatalf("import with pending writes = %v, want ErrStateNotFresh", err)
	}

	marked, _ := openState(t)
	if _, err := marked.CommitWithHeight(0); err != nil {
		t.Fatal(err)
	}
	if err := marked.ImportSnapshot(leaves, height, root); !errors.Is(err, ErrStateNotFresh) {
		t.Fatalf("import over a height marker = %v, want ErrStateNotFresh", err)
	}
}
