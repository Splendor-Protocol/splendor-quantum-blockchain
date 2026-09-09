package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

func TestMinFeeAtFollowsTheSchedule(t *testing.T) {
	if got := minFeeAt(nil, 10); got != mempool.MinFee {
		t.Fatalf("no schedule: %d, want the launch floor %d", got, mempool.MinFee)
	}
	sched := forks.Schedule{forks.MinFeeV1: 100}
	if got := minFeeAt(sched, 99); got != mempool.MinFee {
		t.Fatalf("one below activation: %d, want %d", got, mempool.MinFee)
	}
	if got := minFeeAt(sched, 100); got != mempool.MinFeeV1Amount {
		t.Fatalf("at activation: %d, want %d", got, mempool.MinFeeV1Amount)
	}
}

// The pool's floor is set for the NEXT block immediately, then follows the
// head across the activation height within one tick.
func TestKeepPoolFloorAlignedMovesWithTheHead(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	pool := mempool.New(10, sdb)
	sched := forks.Schedule{forks.MinFeeV1: 5}
	var head atomic.Uint64
	head.Store(3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keepPoolFloorAligned(ctx, pool, func() forks.Schedule { return sched }, head.Load, 5*time.Millisecond)
	// The pool is empty, so MinFeeNow is the uncongested base fee.
	if got := pool.MinFeeNow(); got != mempool.MinFee {
		t.Fatalf("head 3, next block 4: floor %d, want %d", got, mempool.MinFee)
	}
	head.Store(4) // next block is 5, the activation height
	deadline := time.Now().Add(2 * time.Second)
	for pool.MinFeeNow() != mempool.MinFeeV1Amount {
		if time.Now().After(deadline) {
			t.Fatalf("floor did not move to %d after the head reached the activation height", mempool.MinFeeV1Amount)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
