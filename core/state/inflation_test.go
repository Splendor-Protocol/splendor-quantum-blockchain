package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func newFundedAccount(a qcrypto.Address, amount int64) *types.Account {
	acc := types.NewAccount(a)
	acc.AddBalance(big.NewInt(amount))
	return acc
}

// Transfer(x, x, n) must be a no-op on the balance. Pre-fix, Transfer read the
// sender and the recipient into two SEPARATE account copies, applied -n to one
// and +n to the other, then wrote both back — so for from==to the last write won
// and the account gained n out of thin air. Repeating it doubles a balance for
// free and mints native SPLD.
func TestSelfTransferDoesNotMint(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	a := addr(0xAA)
	acc := newFundedAccount(a, 1_000)
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	for i := 0; i < 8; i++ {
		if err := sdb.Transfer(a, a, big.NewInt(1_000)); err != nil {
			t.Fatalf("self-transfer %d: %v", i, err)
		}
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	bal, _ := sdb.GetBalance(a)
	if bal.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("self-transfer minted SPLD: balance = %s, want 1000", bal)
	}
}

// A self-transfer larger than the balance must still fail — the balance check
// may not be bypassed just because sender and recipient are the same account.
func TestSelfTransferRespectsBalance(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	a := addr(0xBB)
	sdb.SetAccount(newFundedAccount(a, 100))
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	if err := sdb.Transfer(a, a, big.NewInt(500)); err == nil {
		t.Fatal("over-balance self-transfer was accepted")
	}
	bal, _ := sdb.GetBalance(a)
	if bal.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("balance = %s, want 100", bal)
	}
}

// Ordinary two-party transfers must keep working and conserve supply.
func TestTransferConservesSupply(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	from, to := addr(0xCC), addr(0xDD)
	sdb.SetAccount(newFundedAccount(from, 1_000))
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	if err := sdb.Transfer(from, to, big.NewInt(400)); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	fb, _ := sdb.GetBalance(from)
	tb, _ := sdb.GetBalance(to)
	if fb.Cmp(big.NewInt(600)) != 0 || tb.Cmp(big.NewInt(400)) != 0 {
		t.Fatalf("from = %s (want 600), to = %s (want 400)", fb, tb)
	}
	if total := new(big.Int).Add(fb, tb); total.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("supply = %s, want 1000", total)
	}
}

func TestSumNativeBalancesIncludesDirtyWrites(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	a, b := addr(0x11), addr(0x22)
	sdb.SetAccount(newFundedAccount(a, 700))
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	sdb.SetAccount(newFundedAccount(b, 300))

	got, err := sdb.SumNativeBalances()
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if got.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("sum = %s, want 1000 (committed 700 + dirty 300)", got)
	}
}
