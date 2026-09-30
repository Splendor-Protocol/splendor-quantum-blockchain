package state

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func openTestState(t *testing.T) *StateDB {
	t.Helper()
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}

func pendingRoot(t *testing.T, sdb *StateDB) [32]byte {
	t.Helper()
	r, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func committedRoot(t *testing.T, sdb *StateDB) [32]byte {
	t.Helper()
	r, err := sdb.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Each distinct change set over one committed state is hashed once. Asking
// again, staging the same writes after a discard (a block built, then applied)
// and the commit that follows are all answered without rehashing the leaves.
func TestPendingRootHashedOncePerChangeSet(t *testing.T) {
	sdb := openTestState(t)
	computed := func() uint64 { return sdb.RootStats().Computed }
	stage := func() {
		setBalance(sdb, 1, 10)
		setBalance(sdb, 2, 20)
	}

	stage()
	before := computed()
	r1 := pendingRoot(t, sdb)
	if got := computed(); got != before+1 {
		t.Fatalf("first pending root hashed %d times, want 1", got-before)
	}
	if pendingRoot(t, sdb) != r1 || computed() != before+1 {
		t.Fatal("asking again rehashed the same change set")
	}
	sdb.DiscardPending()
	stage()
	if pendingRoot(t, sdb) != r1 || computed() != before+1 {
		t.Fatal("the same writes staged again after a discard were rehashed")
	}
	got, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if got != r1 || computed() != before+1 {
		t.Fatalf("commit returned %x after %d hashes, want %x after 1", got[:8], computed()-before, r1[:8])
	}
	if c, d := committedRoot(t, sdb), diskRoot(t, sdb); c != r1 || d != r1 {
		t.Fatalf("committed root %x, disk %x, want %x", c[:8], d[:8], r1[:8])
	}

	// The same kind of change over the new committed state is a new question.
	setBalance(sdb, 1, 11)
	before = computed()
	pendingRoot(t, sdb)
	if computed() != before+1 {
		t.Fatal("a change over a new committed state was answered from an old one")
	}
}

// An empty pending layer, and writes that leave every leaf as committed, need
// no hashing: the committed root is the answer.
func TestUnchangedStateIsNotRehashed(t *testing.T) {
	sdb := openTestState(t)
	setBalance(sdb, 1, 10)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	want := committedRoot(t, sdb)
	before := sdb.RootStats().Computed

	if pendingRoot(t, sdb) != want {
		t.Fatal("an empty pending layer moved the root")
	}
	setBalance(sdb, 1, 10) // the committed value, written again
	if pendingRoot(t, sdb) != want {
		t.Fatal("rewriting a leaf with its own value moved the root")
	}
	got, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if got != want || diskRoot(t, sdb) != want {
		t.Fatalf("commit of unchanged leaves gave %x, want %x", got[:8], want[:8])
	}
	if n := sdb.RootStats().Computed - before; n != 0 {
		t.Fatalf("unchanged state was hashed %d times", n)
	}
}

// A failed state write installs nothing: the committed root and the disk stay
// where they were, and the retry commits the same change set without hashing
// it again.
func TestFailedCommitKeepsTheCommittedTree(t *testing.T) {
	sdb := openTestState(t)
	setBalance(sdb, 1, 10)
	r0, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	setBalance(sdb, 2, 20)
	want := pendingRoot(t, sdb)
	before := sdb.RootStats().Computed

	sdb.FailNextCommit(errors.New("injected"))
	if _, err := sdb.Commit(); err == nil {
		t.Fatal("injected failure did not fail the commit")
	}
	if committedRoot(t, sdb) != r0 || diskRoot(t, sdb) != r0 {
		t.Fatal("a failed commit moved the committed root")
	}
	got, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if got != want || diskRoot(t, sdb) != want {
		t.Fatalf("retry committed %x, want %x", got[:8], want[:8])
	}
	if n := sdb.RootStats().Computed - before; n != 0 {
		t.Fatalf("the retry rehashed the change set %d times", n)
	}
}

// Readers never see a commit half installed: every committed root a reader
// observes, with its height, is the root the writer produced at that height,
// and every proof verifies against the root it reports.
func TestCommittedViewsAreWholeUnderConcurrentCommits(t *testing.T) {
	sdb := openTestState(t)
	var roots sync.Map // height -> root
	var done atomic.Bool
	errs := make(chan error, 8)

	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				v := sdb.CommittedView()
				if want, ok := roots.Load(v.Height); ok && v.HeightKnown && want.([32]byte) != v.Root {
					errs <- errors.New("CommittedView paired a height with another height's root")
					return
				}
				p, err := sdb.AccountProofContext(context.Background(), sumAddr(1))
				if err != nil {
					continue // not committed yet
				}
				if err := VerifyAccountProof(p); err != nil {
					errs <- err
					return
				}
				if want, ok := roots.Load(p.Height); ok && p.HeightKnown && want.([32]byte) != p.Root {
					errs <- errors.New("a proof paired a height with another height's root")
					return
				}
			}
		}()
	}

	for h := uint64(1); h <= 150; h++ {
		setBalance(sdb, 1, int64(h))
		setBalance(sdb, byte(h), int64(h))
		var slot [32]byte
		slot[0] = byte(h)
		if err := sdb.SetStorageValue(qcrypto.Address{9}, slot, []byte{byte(h)}); err != nil {
			t.Fatal(err)
		}
		root, err := sdb.CommitWithHeight(h)
		if err != nil {
			t.Fatal(err)
		}
		roots.Store(h, root)
	}
	done.Store(true)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// A write that fails at the disk — not the FailNextCommit hook, which returns
// before any tree is built — must leave the committed tree where it was: the
// node may not report a root that is not on disk, and a reopen must agree.
func TestFailedDurableWriteKeepsTheCommittedTree(t *testing.T) {
	dir := t.TempDir()
	sdb, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	setBalance(sdb, 1, 10)
	r1, err := sdb.CommitWithHeight(1)
	if err != nil {
		t.Fatal(err)
	}
	setBalance(sdb, 2, 20)
	if _, err := sdb.PendingStateRoot(); err != nil { // the new tree is built and remembered
		t.Fatal(err)
	}
	if err := sdb.db.SetReadOnly(); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.CommitWithHeight(2); err == nil {
		t.Fatal("a commit to a read-only database succeeded")
	}
	if got := committedRoot(t, sdb); got != r1 {
		t.Fatalf("the failed write moved the committed root to %x, want %x", got[:8], r1[:8])
	}
	if v := sdb.CommittedView(); v.Root != r1 || v.Height != 1 {
		t.Fatalf("CommittedView = %x at %d, want %x at 1", v.Root[:8], v.Height, r1[:8])
	}
	if d := diskRoot(t, sdb); d != r1 {
		t.Fatalf("disk root %x, want %x", d[:8], r1[:8])
	}
	sdb.Close()
	reopened, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := committedRoot(t, reopened); got != r1 {
		t.Fatalf("root after reopen %x, want %x", got[:8], r1[:8])
	}
}
