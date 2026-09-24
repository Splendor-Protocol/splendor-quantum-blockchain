package network

import (
	"context"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
)

// The byte budget must reserve the block wire cap per request, so a burst of
// small reservations cannot later be blown past by large decoded blocks. It
// must also admit a single block even when the configured window is smaller
// than one wire cap (the one-large-block exception), without ever admitting a
// second until the first drains.
func TestSyncByteBudgetBoundsReservations(t *testing.T) {
	const wire = int64(block.MaxBlockWireBytes)
	b := newSyncByteBudget(128 << 20) // four blocks' worth
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if err := b.acquire(ctx, wire); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	// The fifth wire-cap reservation must wait, and time out under a short ctx.
	sctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := b.acquire(sctx, wire); err == nil {
		t.Fatal("fifth wire-cap reservation admitted over the aggregate budget")
	}

	// Releasing a held block makes room for the next.
	b.release(wire)
	if err := b.acquire(ctx, wire); err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}

	// adjust reflects the measured (small) block and frees the phantom
	// reservation, so the budget tracks real retained bytes, not the wire cap.
	b.adjust(wire, 4)
	before := b.used
	if before >= 128<<20 {
		t.Fatalf("adjust should have reduced used, got %d", before)
	}
}

func TestSyncByteBudgetOneLargeBlockException(t *testing.T) {
	const wire = int64(block.MaxBlockWireBytes)
	// Window smaller than one block: still admit exactly one.
	b := newSyncByteBudget(16 << 20)
	ctx := context.Background()
	if err := b.acquire(ctx, wire); err != nil {
		t.Fatalf("one large block should be admissible: %v", err)
	}
	sctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := b.acquire(sctx, wire); err == nil {
		t.Fatal("second large block admitted before the first drained")
	}
	b.release(wire)
	if err := b.acquire(ctx, wire); err != nil {
		t.Fatalf("after drain, another large block should be admissible: %v", err)
	}
}
