package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func mkAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	a[0] = b
	return a
}

// Contract storage MUST be folded into the state root. Two states that differ
// only in a storage slot must produce different roots — before this change the
// root hashed accounts only, so storage divergence was invisible (silent fork),
// and PendingStateRoot must predict exactly what Commit produces.
func TestStorageIsInStateRoot(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	c := mkAddr(0xAB)
	var slot [32]byte
	slot[31] = 1

	base, _ := sdb.Commit()
	if base != stateEmptyRoot {
		t.Fatalf("fresh state root = %x, want empty root", base)
	}

	if err := sdb.SetStorageValue(c, slot, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	pending, _ := sdb.PendingStateRoot()
	if pending == base {
		t.Fatal("pending root unchanged after storage write — storage not in root")
	}
	committed, _ := sdb.Commit()
	if committed != pending {
		t.Fatalf("commit root %x != pending root %x — PendingStateRoot must predict Commit", committed, pending)
	}

	// Changing the slot value must change the root.
	if err := sdb.SetStorageValue(c, slot, []byte("world")); err != nil {
		t.Fatal(err)
	}
	changed, _ := sdb.Commit()
	if changed == committed {
		t.Fatal("root unchanged after storage value change")
	}
}

// A failed sub-call's storage writes must NOT survive a revert. This is the
// storage-residue fork hole: SetStorageValue used to write straight to LevelDB,
// so RevertToSnapshot could not undo it and a follower diverged on storage.
func TestSnapshotRevertsStorage(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	c := mkAddr(0x11)
	var slot [32]byte
	slot[0] = 7
	if err := sdb.SetStorageValue(c, slot, []byte("committed")); err != nil {
		t.Fatal(err)
	}
	root0, _ := sdb.Commit()

	snap := sdb.Snapshot()
	if err := sdb.SetStorageValue(c, slot, []byte("dirty-overwrite")); err != nil {
		t.Fatal(err)
	}
	if v, _ := sdb.GetStorageValue(c, slot); string(v) != "dirty-overwrite" {
		t.Fatalf("overlay read = %q, want dirty-overwrite", v)
	}
	if rp, _ := sdb.PendingStateRoot(); rp == root0 {
		t.Fatal("pending root should differ after overwrite")
	}

	sdb.RevertToSnapshot(snap)

	if rr, _ := sdb.PendingStateRoot(); rr != root0 {
		t.Fatalf("root after revert = %x, want %x — storage residue survived revert", rr, root0)
	}
	if v, _ := sdb.GetStorageValue(c, slot); string(v) != "committed" {
		t.Fatalf("storage residue after revert: got %q, want committed", v)
	}
}

// X402 nonces are consensus state and must be in the root too.
func TestX402NonceInStateRoot(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	base, _ := sdb.Commit()
	if err := sdb.MarkX402NonceUsed(qcrypto.Hash32([]byte("nonce-1")), 5); err != nil {
		t.Fatal(err)
	}
	after, _ := sdb.Commit()
	if after == base {
		t.Fatal("root unchanged after X402 nonce marked — nonce not in root")
	}
}

// The full state root (accounts + code + storage + nonces) must be deterministic:
// two independently-built states with the same writes produce the same root. This
// is the producer/follower agreement property the whole fix exists to guarantee.
func TestStateRootDeterministicAcrossDBs(t *testing.T) {
	build := func() [32]byte {
		sdb, err := NewStateDB(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer sdb.Close()

		a := types.NewAccount(mkAddr(1))
		a.AddBalance(big.NewInt(500))
		sdb.SetAccount(a)
		b := types.NewAccount(mkAddr(2))
		b.AddBalance(big.NewInt(999))
		sdb.SetAccount(b)

		sdb.SetCode(qcrypto.Hash32([]byte("code")), []byte("code"))

		var s1, s2 [32]byte
		s1[0] = 1
		s2[0] = 2
		sdb.SetStorageValue(mkAddr(9), s1, []byte("x"))
		sdb.SetStorageValue(mkAddr(9), s2, []byte("y"))

		sdb.MarkX402NonceUsed(qcrypto.Hash32([]byte("n")), 3)

		r, _ := sdb.Commit()
		return r
	}
	r1 := build()
	r2 := build()
	if r1 != r2 {
		t.Fatalf("state root not deterministic: %x != %x", r1, r2)
	}
	if r1 == stateEmptyRoot {
		t.Fatal("root should be non-empty")
	}
}

// The leaf index must be rebuilt from disk on reopen, yielding the identical root
// — otherwise a node would compute a different root after a restart and fork.
func TestStateRootStableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	var want [32]byte
	{
		sdb, err := NewStateDB(dir)
		if err != nil {
			t.Fatal(err)
		}
		a := types.NewAccount(mkAddr(5))
		a.AddBalance(big.NewInt(1))
		sdb.SetAccount(a)
		var slot [32]byte
		slot[3] = 9
		sdb.SetStorageValue(mkAddr(5), slot, []byte("v"))
		sdb.SetCode(qcrypto.Hash32([]byte("c")), []byte("c"))
		want, _ = sdb.Commit()
		sdb.Close()
	}
	sdb2, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb2.Close()
	got, _ := sdb2.StateRoot()
	if got != want {
		t.Fatalf("root after reopen = %x, want %x — leaf index not rebuilt from disk", got, want)
	}
}

// Merkle construction sanity: deterministic, empty sentinel, and distinct sizes.
func TestMerkleRootConstruction(t *testing.T) {
	if merkleRoot(map[string][32]byte{}) != stateEmptyRoot {
		t.Fatal("empty leaf set must map to the empty-state sentinel")
	}
	m := map[string][32]byte{
		"acc:a":   qcrypto.Hash32([]byte("1")),
		"store:b": qcrypto.Hash32([]byte("2")),
		"code:c":  qcrypto.Hash32([]byte("3")),
	}
	if merkleRoot(m) != merkleRoot(m) {
		t.Fatal("merkleRoot not deterministic across calls")
	}
	single := merkleRoot(map[string][32]byte{"k": qcrypto.Hash32([]byte("v"))})
	if single == stateEmptyRoot {
		t.Fatal("single-leaf root collided with empty root")
	}
	if single == merkleRoot(m) {
		t.Fatal("distinct leaf sets produced the same root")
	}
}

// The journaled snapshot must restore the PRE-snapshot value even when a key is
// mutated multiple times inside the snapshot (LIFO undo), and must handle nested
// snapshots — the cases where a naive undo-log breaks.
func TestJournalSnapshotSemantics(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	addr := mkAddr(0x42)
	set := func(bal int64) {
		a := types.NewAccount(addr)
		a.AddBalance(big.NewInt(bal))
		sdb.SetAccount(a)
	}
	bal := func() int64 {
		b, _ := sdb.GetBalance(addr)
		return b.Int64()
	}

	set(100)
	snap := sdb.Snapshot()
	// Multiple mutations to the same account inside the snapshot.
	set(200)
	set(300)
	set(400)
	if bal() != 400 {
		t.Fatalf("pre-revert balance = %d, want 400", bal())
	}
	sdb.RevertToSnapshot(snap)
	if bal() != 100 {
		t.Fatalf("balance after revert = %d, want 100 (LIFO undo broken)", bal())
	}

	// Nested snapshots: revert inner keeps outer's changes; revert outer undoes all.
	set(500)
	outer := sdb.Snapshot()
	set(600)
	inner := sdb.Snapshot()
	set(700)
	sdb.RevertToSnapshot(inner)
	if bal() != 600 {
		t.Fatalf("after inner revert = %d, want 600", bal())
	}
	sdb.RevertToSnapshot(outer)
	if bal() != 500 {
		t.Fatalf("after outer revert = %d, want 500", bal())
	}

	// CommitSnapshot keeps changes.
	keep := sdb.Snapshot()
	set(999)
	sdb.CommitSnapshot(keep)
	if bal() != 999 {
		t.Fatalf("after CommitSnapshot = %d, want 999", bal())
	}
}
