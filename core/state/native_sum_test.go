package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func sumAddr(i byte) qcrypto.Address {
	var a qcrypto.Address
	a[0] = i
	a[31] = 0x77
	return a
}

func setBalance(sdb *StateDB, i byte, bal int64) {
	acc := types.NewAccount(sumAddr(i))
	acc.AddBalance(big.NewInt(bal))
	sdb.SetAccount(acc)
}

// scannedSupply totals the native balances the slow way: every committed
// record, with the dirty layer overlaid — what SumNativeBalances did on every
// call before it learned to carry the committed total across commits.
func scannedSupply(t *testing.T, sdb *StateDB) *big.Int {
	t.Helper()
	sum := new(big.Int)
	seen := map[qcrypto.Address]struct{}{}
	sdb.mu.RLock()
	for addr, acc := range sdb.dirty {
		seen[addr] = struct{}{}
		if acc != nil && acc.Balance != nil {
			sum.Add(sum, acc.Balance)
		}
	}
	sdb.mu.RUnlock()
	if err := sdb.ForEachAccount(func(a *types.Account) error {
		if _, ok := seen[a.Address]; ok {
			return nil
		}
		if a.Balance != nil {
			sum.Add(sum, a.Balance)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return sum
}

// The committed native total is carried across commits instead of rescanned,
// and must equal a scan of the records whatever mix of new accounts, balance
// changes, reverted transactions and discarded blocks preceded it.
func TestSumNativeBalancesTracksCommitsWithoutRescanning(t *testing.T) {
	dir := t.TempDir()
	sdb, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(step string) {
		t.Helper()
		got, err := sdb.SumNativeBalances()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if want := scannedSupply(t, sdb); got.Cmp(want) != 0 {
			t.Fatalf("%s: carried total %s, a scan says %s", step, got, want)
		}
	}

	setBalance(sdb, 1, 100)
	setBalance(sdb, 2, 50)
	check("pending writes on an empty state")
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	check("after the first commit")

	setBalance(sdb, 1, 70) // an existing balance moves down
	setBalance(sdb, 3, 5)  // a new account
	check("pending changes over committed state")
	snap := sdb.Snapshot()
	setBalance(sdb, 2, 999)
	if err := sdb.RevertToSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	check("after a reverted write")
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	check("after the second commit")

	setBalance(sdb, 4, 1000)
	sdb.DiscardPending()
	check("after a discarded block")
	if got, _ := sdb.SumNativeBalances(); got.Cmp(big.NewInt(125)) != 0 {
		t.Fatalf("total %s, want 125", got)
	}

	// A fresh open scans and must agree with what was carried.
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStateDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.SumNativeBalances(); err != nil || got.Cmp(big.NewInt(125)) != 0 {
		t.Fatalf("reopened total %s (%v), want 125", got, err)
	}
}

// A commit returns the root PendingStateRoot computed for the same pending
// layer, and recomputes it whenever anything moved in between.
func TestCommitReusesThePendingRootOnlyWhileUnchanged(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	commitAndCheck := func(step string) [32]byte {
		t.Helper()
		got, err := sdb.Commit()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		full := diskRoot(t, sdb)
		if got != full {
			t.Fatalf("%s: commit returned %x, a full recomputation gives %x", step, got[:8], full[:8])
		}
		return got
	}

	setBalance(sdb, 1, 10)
	pending, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got := commitAndCheck("unchanged since PendingStateRoot"); got != pending {
		t.Fatalf("commit root %x, PendingStateRoot said %x", got[:8], pending[:8])
	}

	setBalance(sdb, 2, 20)
	stale, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	setBalance(sdb, 3, 30) // a write after the root was computed
	if got := commitAndCheck("a write after PendingStateRoot"); got == stale {
		t.Fatal("commit returned a root computed before a later write")
	}

	setBalance(sdb, 4, 40)
	before, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	snap := sdb.Snapshot()
	setBalance(sdb, 4, 41)
	if err := sdb.RevertToSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if got := commitAndCheck("a reverted write after PendingStateRoot"); got != before {
		t.Fatalf("the revert restored the layer the root was computed over, yet commit gave %x for %x", got[:8], before[:8])
	}

	setBalance(sdb, 5, 50)
	if _, err := sdb.PendingStateRoot(); err != nil {
		t.Fatal(err)
	}
	sdb.DiscardPending()
	if got := commitAndCheck("a discard after PendingStateRoot"); got != before {
		t.Fatalf("nothing pending, yet the root moved from %x to %x", before[:8], got[:8])
	}
}
