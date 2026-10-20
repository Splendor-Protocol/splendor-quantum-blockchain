package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func addr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

// PendingStateRoot must equal the root Commit() actually produces, so ExecuteBlock
// can verify a block's state root before durably persisting it.
func TestPendingStateRootMatchesCommit(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	// Some committed baseline accounts.
	a := types.NewAccount(addr(0x11))
	a.AddBalance(big.NewInt(500))
	sdb.SetAccount(a)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	// Stage new + modified dirty accounts.
	a2 := types.NewAccount(addr(0x11)) // modify existing
	a2.AddBalance(big.NewInt(1234))
	sdb.SetAccount(a2)
	b := types.NewAccount(addr(0x22)) // brand new
	b.AddBalance(big.NewInt(9999))
	sdb.SetAccount(b)

	pending, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatalf("pending root: %v", err)
	}
	committed, err := sdb.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if pending != committed {
		t.Fatalf("PendingStateRoot %x != Commit root %x", pending[:8], committed[:8])
	}
}

// DiscardPending must drop uncommitted writes so a rejected block leaves committed
// state untouched.
func TestDiscardPendingLeavesCommittedStateIntact(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	a := types.NewAccount(addr(0x33))
	a.AddBalance(big.NewInt(1000))
	sdb.SetAccount(a)
	root0, _ := sdb.Commit()

	// Stage a change, then discard it.
	bad := types.NewAccount(addr(0x33))
	bad.AddBalance(big.NewInt(777777))
	sdb.SetAccount(bad)
	sdb.DiscardPending()

	bal, _ := sdb.GetBalance(addr(0x33))
	if bal.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("balance changed after discard: got %s want 1000", bal.String())
	}

	// A subsequent Commit must return the ORIGINAL root — proving the dirty write
	// was truly dropped, not merely left uncommitted. (If DiscardPending were a
	// no-op, this Commit would persist balance 777777 and yield a different root.)
	root1, err := sdb.Commit()
	if err != nil {
		t.Fatalf("commit after discard: %v", err)
	}
	if root0 != root1 {
		t.Fatalf("discarded write resurfaced on commit: root %x -> %x", root0[:8], root1[:8])
	}
}
