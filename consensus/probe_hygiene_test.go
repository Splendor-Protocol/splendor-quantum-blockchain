package consensus

import (
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// TestConsensusProbeDoesNotWaitForTheBlockApply pins what /readyz and /status
// cost.
//
// The engine holds stateMu through commitBlock, and commitBlock's onCommit is
// the executor's entire block apply — 15s for a 4,173-transaction block on this
// chain's own validation run. The probe asked the engine four questions, each
// of which took that lock, so readiness stalled for the length of every apply
// and an orchestrator with a 1-5s probe timeout restarted the validator
// precisely while it was doing its job.
//
// The probe here is what cmd/node's consensusProbe does: one read of the
// published view. It must answer while the apply is in flight, and what it says
// must belong to one moment — the height being applied, with the lock held at
// that height.
func TestConsensusProbeDoesNotWaitForTheBlockApply(t *testing.T) {
	set, keys := testSet(t, 1)
	const height = uint64(3)

	applying := make(chan struct{})
	release := make(chan struct{})
	committed := make(chan *types.Block, 1)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{
		OnCommit: func(b *types.Block) error {
			close(applying)
			<-release
			committed <- b
			return nil
		},
	})
	eng.SetHeight(height)

	block := proposalAt(t, set, keys, height, 0, 5).Block
	go eng.handleProposal(&pendingProposal{block: block, local: true})
	<-applying

	probed := make(chan EngineView, 1)
	go func() { probed <- eng.View() }()
	select {
	case v := <-probed:
		if v.Height != height {
			t.Fatalf("the probe reports height %d during the apply of %d", v.Height, height)
		}
		if v.Round != 0 {
			t.Fatalf("the probe reports round %d, want 0", v.Round)
		}
		if !v.Locked || v.LockedHash != block.Hash {
			t.Fatalf("the probe reports locked=%v hash=%x, want the block being applied %x",
				v.Locked, v.LockedHash[:4], block.Hash[:4])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the readiness probe blocked on the consensus lock for the length of the block apply")
	}

	close(release)
	select {
	case got := <-committed:
		if got.Hash != block.Hash {
			t.Fatalf("committed %x, want %x", got.Hash[:4], block.Hash[:4])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the commit never finished")
	}

	// And the view keeps up afterwards: the height moves on once the apply
	// returns, so a probe cannot report a finished height as still in progress.
	deadline := time.Now().Add(3 * time.Second)
	for {
		v := eng.View()
		if v.Height == height+1 && !v.Locked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the commit the published view is still height %d locked=%v", v.Height, v.Locked)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
