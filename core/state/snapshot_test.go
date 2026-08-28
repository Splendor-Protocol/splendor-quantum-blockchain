package state

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Snapshot ids used to come from a package-global counter. A Simulate()
// sandbox has its own mutex but shared that counter, so a /call or /estimate
// running while the producer executed a block could increment it between the
// producer's outer and inner Snapshot() calls. The inner id was then not
// guaranteed to be above the outer one, an inner revert could drop the outer
// checkpoint (RevertToSnapshot deletes every id >= the reverted one), and the
// per-tx outer revert became a silent no-op — leaving fee, nonce and module
// residue in the producer's state root that followers cannot reproduce.
//
// Ids are now per-StateDB. Eight sandboxes hammer their own snapshots while
// the parent takes nested (outer, inner) pairs; every inner id must exceed
// its outer one and every nested revert must restore the exact balance and
// nonce. Run under -race.
func TestSnapshotIDsArePerStateDBUnderConcurrentSandboxes(t *testing.T) {
	s := newSimStateDB(t)
	alice := simAddr(0xa1)

	base := types.NewAccount(alice)
	base.Balance = big.NewInt(1_000)
	base.Nonce = 5
	s.SetAccount(base)

	set := func(db *StateDB, balance int64, nonce uint64) {
		acc := types.NewAccount(alice)
		acc.Balance = big.NewInt(balance)
		acc.Nonce = nonce
		db.SetAccount(acc)
	}
	check := func(what string, balance int64, nonce uint64) {
		t.Helper()
		acc, err := s.GetAccount(alice)
		if err != nil {
			t.Fatalf("%s: GetAccount: %v", what, err)
		}
		if acc.Balance.Cmp(big.NewInt(balance)) != 0 || acc.Nonce != nonce {
			t.Fatalf("%s: balance %s nonce %d, want %d/%d", what, acc.Balance, acc.Nonce, balance, nonce)
		}
	}

	stop := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				sim := s.Simulate()
				outer := sim.Snapshot()
				set(sim, 1, 99)
				inner := sim.Snapshot()
				if inner <= outer {
					errs <- errors.New("sandbox inner id <= outer id")
					return
				}
				if err := sim.RevertToSnapshot(inner); err != nil {
					errs <- err
					return
				}
				if err := sim.CommitSnapshot(outer); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	for i := 0; i < 100_000; i++ {
		outer := s.Snapshot()
		mid := int64(900 + i%7)
		set(s, mid, 6)
		inner := s.Snapshot()
		if inner <= outer {
			t.Fatalf("iteration %d: inner id %d <= outer id %d", i, inner, outer)
		}
		set(s, 1, 99)
		if err := s.RevertToSnapshot(inner); err != nil {
			t.Fatalf("iteration %d: revert inner: %v", i, err)
		}
		check("after inner revert", mid, 6)
		if err := s.RevertToSnapshot(outer); err != nil {
			t.Fatalf("iteration %d: revert outer: %v", i, err)
		}
		check("after outer revert", 1_000, 5)
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatalf("sandbox goroutine: %v", err)
	default:
	}
	if n := s.SnapshotCount(); n != 0 {
		t.Fatalf("parent leaked %d snapshots", n)
	}
	if s.nextSnap != 200_000 {
		t.Fatalf("parent issued %d ids, want 200000: a sandbox touched the parent's counter", s.nextSnap)
	}
}

// An id the StateDB is not holding is a bug in the caller's snapshot
// discipline. It used to be a silent no-op; it is now ErrUnknownSnapshot, so a
// revert that would have left residue in the pending layer fails loudly.
func TestSnapshotUnknownIDIsRejected(t *testing.T) {
	s := newSimStateDB(t)

	if err := s.RevertToSnapshot(1 << 62); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("revert of a never-issued id: %v, want ErrUnknownSnapshot", err)
	}
	if err := s.CommitSnapshot(0); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("commit of id 0: %v, want ErrUnknownSnapshot", err)
	}

	// A consumed id is unknown too.
	id := s.Snapshot()
	if err := s.RevertToSnapshot(id); err != nil {
		t.Fatalf("first revert: %v", err)
	}
	if err := s.RevertToSnapshot(id); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("second revert of the same id: %v, want ErrUnknownSnapshot", err)
	}

	// Reverting to an outer id drops the inner ones; committing one afterwards
	// is the mistake this exists to catch.
	outer := s.Snapshot()
	inner := s.Snapshot()
	if err := s.RevertToSnapshot(outer); err != nil {
		t.Fatalf("outer revert: %v", err)
	}
	if err := s.CommitSnapshot(inner); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("commit of a dropped inner id: %v, want ErrUnknownSnapshot", err)
	}

	// A sandbox's ids never touch the parent's map, in either direction.
	sim := s.Simulate()
	simID := sim.Snapshot()
	if err := s.CommitSnapshot(simID); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("parent accepted a sandbox id: %v", err)
	}
	parentID := s.Snapshot()
	if err := sim.RevertToSnapshot(parentID + 1); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("sandbox accepted a parent id: %v", err)
	}
	if err := sim.CommitSnapshot(simID); err != nil {
		t.Fatalf("sandbox commit of its own id: %v", err)
	}
	if err := s.CommitSnapshot(parentID); err != nil {
		t.Fatalf("parent commit of its own id: %v", err)
	}
}
