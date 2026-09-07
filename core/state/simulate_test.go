package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func newSimStateDB(t *testing.T) *StateDB {
	t.Helper()
	s, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("NewStateDB: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func simAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

func mustBalance(t *testing.T, s *StateDB, addr qcrypto.Address) *big.Int {
	t.Helper()
	bal, err := s.GetBalance(addr)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	return bal
}

// A simulation must not be able to disturb the pending block the executor is
// building. Before Simulate() existed, /call handed the live StateDB to the VM;
// a revert inside the simulated call replayed the shared journal past the
// executor's mark and deleted its snapshot ID, letting an anonymous RPC caller
// roll back consensus work in progress.
func TestSimulateDoesNotDisturbParentPendingState(t *testing.T) {
	s := newSimStateDB(t)

	alice, bob := simAddr(0xa1), simAddr(0xb0)

	acc := types.NewAccount(alice)
	acc.Balance = big.NewInt(1_000)
	s.SetAccount(acc)

	// The executor is mid-block: a snapshot is open and writes are pending.
	execSnap := s.Snapshot()
	mid := types.NewAccount(alice)
	mid.Balance = big.NewInt(900)
	s.SetAccount(mid)

	sim := s.Simulate()

	// The sandbox sees the parent's pending layer...
	if got := mustBalance(t, sim, alice); got.Cmp(big.NewInt(900)) != 0 {
		t.Fatalf("sandbox should observe parent pending balance 900, got %s", got)
	}

	// ...then writes and reverts inside its own journal.
	simSnap := sim.Snapshot()
	drained := types.NewAccount(alice)
	drained.Balance = big.NewInt(0)
	sim.SetAccount(drained)
	credited := types.NewAccount(bob)
	credited.Balance = big.NewInt(900)
	sim.SetAccount(credited)
	sim.RevertToSnapshot(simSnap)

	if got := mustBalance(t, s, alice); got.Cmp(big.NewInt(900)) != 0 {
		t.Fatalf("parent pending balance corrupted by simulation: got %s, want 900", got)
	}
	if got := mustBalance(t, s, bob); got.Sign() != 0 {
		t.Fatalf("simulation leaked a credit into parent state: bob=%s", got)
	}

	// The executor's own checkpoint must still be usable.
	s.RevertToSnapshot(execSnap)
	if got := mustBalance(t, s, alice); got.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("executor snapshot no longer reverts correctly: got %s, want 1000", got)
	}
}

// Sandbox writes must never reach disk, whatever the caller does with them.
func TestSimulateRefusesToCommit(t *testing.T) {
	s := newSimStateDB(t)
	sim := s.Simulate()

	acc := types.NewAccount(simAddr(0xc0))
	acc.Balance = big.NewInt(42)
	sim.SetAccount(acc)

	if _, err := sim.Commit(); err != ErrSandboxCommit {
		t.Fatalf("sandbox Commit() = %v, want ErrSandboxCommit", err)
	}
	if _, err := sim.CommitWithHeight(7); err != ErrSandboxCommit {
		t.Fatalf("sandbox CommitWithHeight() = %v, want ErrSandboxCommit", err)
	}
	if _, err := sim.StateRoot(); err != ErrSandboxCommit {
		t.Fatalf("sandbox StateRoot() = %v, want ErrSandboxCommit", err)
	}
	if _, err := sim.PendingStateRoot(); err != ErrSandboxCommit {
		t.Fatalf("sandbox PendingStateRoot() = %v, want ErrSandboxCommit", err)
	}

	if got := mustBalance(t, s, simAddr(0xc0)); got.Sign() != 0 {
		t.Fatalf("sandbox write reached the parent: %s", got)
	}
}

// Committed state is shared, so a simulation started after a commit sees it.
func TestSimulateReadsCommittedState(t *testing.T) {
	s := newSimStateDB(t)

	addr := simAddr(0xd0)
	acc := types.NewAccount(addr)
	acc.Balance = big.NewInt(555)
	s.SetAccount(acc)
	if _, err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if got := mustBalance(t, s.Simulate(), addr); got.Cmp(big.NewInt(555)) != 0 {
		t.Fatalf("sandbox balance = %s, want 555", got)
	}
}
