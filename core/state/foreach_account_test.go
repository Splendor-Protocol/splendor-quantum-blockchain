package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func addrN(n byte) qcrypto.Address {
	var a qcrypto.Address
	a[0] = n
	return a
}

func fund(t *testing.T, s *StateDB, n byte, amount int64) qcrypto.Address {
	t.Helper()
	addr := addrN(n)
	acc := types.NewAccount(addr)
	acc.Balance = big.NewInt(amount)
	s.SetAccount(acc)
	return addr
}

func TestForEachAccountVisitsEveryFundedAccount(t *testing.T) {
	s, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want := map[string]int64{}
	for i := byte(1); i <= 5; i++ {
		want[fund(t, s, i, int64(i)*1000).String()] = int64(i) * 1000
	}
	if _, err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	got := map[string]int64{}
	if err := s.ForEachAccount(func(a *types.Account) error {
		got[a.Address.String()] = a.Balance.Int64()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != len(want) {
		t.Fatalf("visited %d accounts, want %d", len(got), len(want))
	}
	for addr, bal := range want {
		if got[addr] != bal {
			t.Fatalf("account %s: balance %d, want %d", addr[:12], got[addr], bal)
		}
	}
}

// The export built on this becomes a chain's opening balances, so it has to
// report what is durably on disk. An uncommitted write is state that may still
// be discarded; counting it would mint a supply that the source chain never
// actually had.
func TestForEachAccountIgnoresUncommittedWrites(t *testing.T) {
	s, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	committed := fund(t, s, 1, 500)
	if _, err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	pendingOnly := fund(t, s, 2, 900) // staged, never committed

	seen := map[string]bool{}
	if err := s.ForEachAccount(func(a *types.Account) error {
		seen[a.Address.String()] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if !seen[committed.String()] {
		t.Fatal("a committed account was not visited")
	}
	if seen[pendingOnly.String()] {
		t.Fatal("an uncommitted account was visited; the export would mint a balance the chain never committed")
	}
}

// Only account leaves may be visited. Contract storage is what made the earlier
// chains unreplayable, and it must not be able to reach a new genesis through
// this path.
func TestForEachAccountSkipsNonAccountLeaves(t *testing.T) {
	s, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	fund(t, s, 1, 100)
	var slot [32]byte
	slot[0] = 0xAA
	if err := s.SetStorageValue(addrN(9), slot, []byte("contract storage")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.ForEachAccount(func(*types.Account) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("visited %d leaves, want exactly the 1 account (storage must not appear)", n)
	}
}
