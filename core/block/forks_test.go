package block

import (
	"sync"
	"testing"
	"time"

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

// Forks() serves /status, the /tx/submit preflight and /estimate, so it must
// never queue behind a block transition. A goroutine holds the block lock
// for the whole test (the shape of a long ProduceBlock/ReconcileState) and
// Forks() still returns; SetForks, by contrast, must still wait for that lock
// so an install can never land in the middle of a block, and the executor's
// own in-block read stays on the schedule the block started under.
func TestForksReadsDoNotWaitOnTheBlockLock(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 10})

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		ex.mu.Lock()
		close(held)
		<-release
		ex.mu.Unlock()
	}()
	<-held

	read := make(chan forks.Schedule, 1)
	go func() { read <- ex.Forks() }()
	select {
	case got := <-read:
		if at, _ := got.At(forks.MultiValidator); at != 10 {
			t.Fatalf("Forks() under a held block lock = %v, want the installed schedule", got)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Forks() blocked behind the block lock")
	}

	installed := make(chan struct{})
	go func() {
		ex.SetForks(forks.Schedule{forks.MultiValidator: 20})
		close(installed)
	}()
	select {
	case <-installed:
		close(release)
		t.Fatal("SetForks landed while a block transition held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	if at, _ := ex.schedule().At(forks.MultiValidator); at != 10 {
		t.Fatalf("in-block schedule changed to %d while the block lock was held", at)
	}

	close(release)
	<-installed
	if at, _ := ex.Forks().At(forks.MultiValidator); at != 20 {
		t.Fatalf("schedule after the block released the lock = %d, want 20", at)
	}
}

// The installed schedule is a private copy: a caller that keeps mutating the
// map it handed over can no longer reach what consensus reads.
func TestInstalledScheduleIsDetachedFromTheCallersMap(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	mine := forks.Schedule{forks.MultiValidator: 10}
	ex.WithForks(mine)
	mine[forks.MultiValidator] = 1
	mine["injected"] = 1
	if at, _ := ex.schedule().At(forks.MultiValidator); at != 10 {
		t.Fatalf("caller's later write reached the executor: %d", at)
	}
	if _, ok := ex.schedule().At("injected"); ok {
		t.Fatal("caller's later insert reached the executor")
	}
	ex.SetForks(nil)
	if ex.Forks() != nil || ex.schedule() != nil {
		t.Fatal("SetForks(nil) did not clear the schedule")
	}
}
