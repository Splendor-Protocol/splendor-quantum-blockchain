package main

import (
	"context"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
)

// minFeeAt is the fee floor a transaction must pay to be included in the
// block at `height`: the launch value until the chain schedules min_fee_v1,
// 0.001 SPLD from that height on. One function feeds both doors — the
// producer's pre-tx hook (judged at the block being built or applied) and
// the mempool's base fee (judged at the next block) — so the pool never
// admits what the producer would refuse, and never refuses what it would
// include.
func minFeeAt(sched forks.Schedule, height uint64) int64 {
	if sched.Active(forks.MinFeeV1, height) {
		return mempool.MinFeeV1Amount
	}
	return mempool.MinFee
}

// keepPoolFloorAligned sets the pool's base fee for the next block now and
// re-checks it every block interval, so the floor moves with the fork
// schedule on every node — full nodes admit transactions too — and moves
// within one interval of the activation height.
func keepPoolFloorAligned(ctx context.Context, pool *mempool.Mempool, sched func() forks.Schedule, head func() uint64, every time.Duration) {
	align := func() { pool.SetBaseFee(minFeeAt(sched(), head()+1)) }
	align()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				align()
			}
		}
	}()
}
