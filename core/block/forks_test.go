package block

import (
	"sync"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
)

// The executor hands its schedule to every module call, so what it reports as
// active is what consensus gates on. Nil, absent, and 0 all read "never"; the
// scheduled height is the first block under the new rules.
func TestExecutorForksActiveMatrix(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	if got := ex.Forks(); got != nil {
		t.Fatalf("a fresh executor reports a schedule: %v", got)
	}
	if ex.Forks().Active(forks.MultiValidator, 1<<40) {
		t.Fatal("nil schedule must never report an upgrade active")
	}

	ex.SetForks(forks.Schedule{forks.MultiValidator: 10, "never": 0})
	for _, tc := range []struct {
		name   string
		height uint64
		want   bool
	}{
		{"absent", 10, false},
		{"never", 10, false},
		{forks.MultiValidator, 0, false},
		{forks.MultiValidator, 9, false},
		{forks.MultiValidator, 10, true},
		{forks.MultiValidator, 11, true},
	} {
		if got := ex.Forks().Active(tc.name, tc.height); got != tc.want {
			t.Fatalf("Active(%q, %d) = %v, want %v", tc.name, tc.height, got, tc.want)
		}
	}
}

// WithForks is the construction-time form main.go uses before crash recovery,
// so a replayed block is gated exactly as it was when first applied.
func TestWithForksInstallsTheSchedule(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	ex.WithForks(forks.Schedule{forks.MultiValidator: 3})
	if at, ok := ex.Forks().At(forks.MultiValidator); !ok || at != 3 {
		t.Fatalf("Forks().At = (%d, %v), want (3, true)", at, ok)
	}
}

// Forks() feeds /status and, later, admission checks on public RPC goroutines.
// Handing those callers the live map would let a reader mutate what consensus
// is about to apply, so they get a copy.
func TestExecutorForksReturnsACopy(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	ex.SetForks(forks.Schedule{forks.MultiValidator: 10})
	got := ex.Forks()
	got["injected"] = 1
	got[forks.MultiValidator] = 1
	if _, ok := ex.Forks()["injected"]; ok {
		t.Fatal("mutating the returned schedule reached the executor")
	}
	if at, _ := ex.Forks().At(forks.MultiValidator); at != 10 {
		t.Fatalf("activation height changed through the copy: %d", at)
	}
}

// SetForks lands from the peer-bootstrap goroutine while the RPC server may be
// reading /status; under -race this pins that both go through the lock.
func TestSetForksAndForksAreSafeConcurrently(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			ex.SetForks(forks.Schedule{forks.MultiValidator: uint64(i + 1)})
		}(i)
		go func() {
			defer wg.Done()
			_ = ex.Forks().Active(forks.MultiValidator, 4)
		}()
	}
	wg.Wait()
	if _, ok := ex.Forks().At(forks.MultiValidator); !ok {
		t.Fatal("schedule lost after concurrent installs")
	}
}
