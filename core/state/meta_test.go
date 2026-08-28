package state

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// meta: values are node-local bookkeeping. If one ever reached the state root,
// two nodes that recorded different local facts would disagree on every block.
func TestPutMetaDoesNotChangeTheStateRoot(t *testing.T) {
	dir := t.TempDir()
	sdb, err := NewStateDB(dir)
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}

	a := types.NewAccount(addr(0x11))
	a.AddBalance(big.NewInt(500))
	sdb.SetAccount(a)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	before, err := sdb.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}

	if err := sdb.PutMeta("forks", []byte(`{"multi_validator":7}`)); err != nil {
		t.Fatalf("PutMeta: %v", err)
	}
	after, err := sdb.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	if before != after {
		t.Fatalf("a meta write moved the state root: %x != %x", before[:8], after[:8])
	}

	// Durable, readable back, and still off-root after a reopen (loadLeaves
	// must skip it like it skips meta:height).
	if err := sdb.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewStateDB(dir)
	if err != nil {
		t.Fatalf("reopen statedb: %v", err)
	}
	defer reopened.Close()
	got, ok, err := reopened.GetMeta("forks")
	if err != nil || !ok {
		t.Fatalf("GetMeta after reopen = (%q, %v, %v)", got, ok, err)
	}
	if !bytes.Equal(got, []byte(`{"multi_validator":7}`)) {
		t.Fatalf("GetMeta returned %q", got)
	}
	rootAfterReopen, err := reopened.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	if rootAfterReopen != before {
		t.Fatalf("meta key became a leaf on reopen: %x != %x", rootAfterReopen[:8], before[:8])
	}
}

func TestGetMetaReportsAbsence(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	got, ok, err := sdb.GetMeta("forks")
	if err != nil {
		t.Fatalf("GetMeta on a fresh db errored: %v", err)
	}
	if ok || got != nil {
		t.Fatalf("GetMeta on a fresh db = (%q, %v), want absent", got, ok)
	}
}

// A simulation overlay is for speculative execution and must never persist
// anything — a /call could otherwise rewrite the node's own bookkeeping.
func TestSandboxRefusesPutMeta(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	sim := sdb.Simulate()
	if err := sim.PutMeta("forks", []byte("{}")); !errors.Is(err, ErrSandboxCommit) {
		t.Fatalf("sandbox PutMeta = %v, want ErrSandboxCommit", err)
	}
	if _, ok, _ := sdb.GetMeta("forks"); ok {
		t.Fatal("a sandbox write reached the real db")
	}
}

// The applied-height marker is maintained by CommitWithHeight; a caller
// overwriting it through PutMeta would confuse crash recovery.
func TestPutMetaRefusesReservedAndEmptyKeys(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	if err := sdb.PutMeta("height", []byte("0")); err == nil {
		t.Fatal("the height marker was overwritable through PutMeta")
	}
	if err := sdb.PutMeta("", []byte("x")); err == nil {
		t.Fatal("an empty meta key was accepted")
	}
}
