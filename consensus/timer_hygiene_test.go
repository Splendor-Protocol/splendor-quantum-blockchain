package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// waitForOpenProposal blocks until the engine holds a proposal open at
// height, or fails the test.
func waitForOpenProposal(t *testing.T, eng *BFTEngine, height uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		eng.stateMu.Lock()
		open := eng.voteSet != nil && eng.proposal != nil && eng.currentHeight == height
		eng.stateMu.Unlock()
		if open {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no proposal opened at height %d", height)
}

// A sole validator seals every height on its own vote and is never waiting on
// anyone, so the round timer must stay disarmed — even when applying a block
// takes longer than the round timeout and the next proposal comes later
// still. The timer used to be re-armed after every proposal and fire on a
// height already sealed: "round timeout, view change" on a chain of one.
func TestSoleValidatorSealsWithoutRoundTimeouts(t *testing.T) {
	set, keys := testSet(t, 1)
	const roundTimeout = 30 * time.Millisecond
	var viewChanges atomic.Int32
	committed := make(chan *types.Block, 8)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		RoundTimeout: roundTimeout,
		OnCommit: func(b *types.Block) error {
			time.Sleep(2 * roundTimeout) // an apply slower than the round timeout
			committed <- b
			return nil
		},
		OnViewChange: func(uint64, uint32) { viewChanges.Add(1) },
	})
	eng.SetHeight(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	for h := uint64(1); h <= 3; h++ {
		eng.ProposeBlock(proposalAt(t, set, keys, h, 0, int64(h)).Block)
		select {
		case b := <-committed:
			if b.Header.Height != h {
				t.Fatalf("committed height %d, want %d", b.Header.Height, h)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("height %d never sealed", h)
		}
		// Idle past the round timeout before the next proposal, as the
		// block-interval tick does.
		time.Sleep(2 * roundTimeout)
	}
	if n := viewChanges.Load(); n != 0 {
		t.Fatalf("%d view changes on a sole validator, want 0", n)
	}
	if h, r := eng.Height(), eng.Round(); h != 4 || r != 0 {
		t.Fatalf("engine at %d/%d after three sealed heights, want 4/0", h, r)
	}
}

// A commit that arrives by vote — the leader's own case — must drop the
// deadline that was set when the proposal opened. It used to survive the
// commit and fire the moment the apply returned, view-changing a height that
// was already sealed.
func TestCommitByVoteDropsTheRoundDeadline(t *testing.T) {
	set, keys := testSet(t, 3)
	const roundTimeout = 40 * time.Millisecond
	var viewChanges atomic.Int32
	committed := make(chan *types.Block, 1)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		RoundTimeout: roundTimeout,
		OnCommit: func(b *types.Block) error {
			time.Sleep(2 * roundTimeout) // the deadline expires during the apply
			committed <- b
			return nil
		},
		OnViewChange: func(uint64, uint32) { viewChanges.Add(1) },
	})
	// Height 2: set[2] leads round 0. Height 3: set[0], this node, leads, so
	// after the commit nothing is awaited from anyone.
	eng.SetHeight(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	msg := proposalAt(t, set, keys, 2, 0, 1)
	eng.SubmitProposal(msg)
	waitForOpenProposal(t, eng, 2)
	third := &Vote{Height: 2, Round: 0, BlockHash: msg.Block.Hash, ValidatorAddr: set[1].Address, Timestamp: 1}
	if err := third.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	eng.SubmitVote(third)
	select {
	case <-committed:
	case <-time.After(2 * time.Second):
		t.Fatal("height 2 never committed")
	}
	time.Sleep(4 * roundTimeout)
	if n := viewChanges.Load(); n != 0 {
		t.Fatalf("%d view changes after the commit, want 0", n)
	}
	if h, r := eng.Height(), eng.Round(); h != 3 || r != 0 {
		t.Fatalf("engine at %d/%d after the commit, want 3/0", h, r)
	}
}

// A leader that never proposes must still be rotated away, and only after
// the round timeout plus the block interval it is allowed to propose in.
func TestSilentRemoteLeaderIsStillRotatedAway(t *testing.T) {
	set, keys := testSet(t, 3)
	const roundTimeout, proposalWait = 30 * time.Millisecond, 30 * time.Millisecond
	viewChanged := make(chan [2]uint64, 4)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		RoundTimeout: roundTimeout,
		OnViewChange: func(h uint64, r uint32) { viewChanged <- [2]uint64{h, uint64(r)} },
	})
	eng.proposalWait = proposalWait
	eng.SetHeight(1) // set[1] leads round 0 and never proposes
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	select {
	case hr := <-viewChanged:
		if hr != [2]uint64{1, 1} {
			t.Fatalf("view change to %v, want height 1 round 1", hr)
		}
		if el := time.Since(start); el < roundTimeout+proposalWait {
			t.Fatalf("leader rotated after %v, before the %v it is allowed", el, roundTimeout+proposalWait)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent leader was never rotated away")
	}
}

// The timer is armed for exactly what the engine waits on at its current
// view, keeps its deadline across unrelated events, is renewed by the
// leader's rebroadcast of an open proposal, and is dropped by a commit.
func TestRoundTimerFollowsWhatTheEngineWaitsFor(t *testing.T) {
	// A sole validator: nothing to wait for, before or after sealing.
	solo, soloKeys := testSet(t, 1)
	eng := engineFor(t, solo, soloKeys, 0, BFTConfig{RoundTimeout: time.Hour})
	eng.SetHeight(1)
	rt := newRoundTimer()
	defer rt.disarm()
	eng.syncRoundTimer(rt, false)
	if rt.armed {
		t.Fatal("a sole validator armed the round timer with nothing to wait for")
	}
	accepted := eng.handleProposal(&pendingProposal{block: proposalAt(t, solo, soloKeys, 1, 0, 1).Block, local: true})
	if !accepted || eng.Height() != 2 {
		t.Fatalf("sole validator's proposal: accepted=%v height=%d, want sealed and at height 2", accepted, eng.Height())
	}
	eng.syncRoundTimer(rt, accepted)
	if rt.armed {
		t.Fatal("a sealed height left the round timer armed")
	}

	// Three members and a remote leader: waiting for its proposal, for the
	// round timeout plus the interval it proposes on; the same view asked
	// again keeps its deadline; a view this node leads waits for nobody.
	set, keys := testSet(t, 3)
	eng = engineFor(t, set, keys, 0, BFTConfig{RoundTimeout: time.Hour})
	eng.proposalWait = time.Minute
	eng.SetHeight(1) // set[1] leads
	rt2 := newRoundTimer()
	defer rt2.disarm()
	eng.syncRoundTimer(rt2, false)
	if !rt2.armed || rt2.height != 1 || rt2.round != 0 {
		t.Fatalf("waiting for a remote leader: armed=%v for %d/%d, want 1/0", rt2.armed, rt2.height, rt2.round)
	}
	if left := time.Until(rt2.deadline); left < time.Hour+30*time.Second {
		t.Fatalf("a remote leader's proposal is allowed %v, want roundTimeout+proposalWait", left)
	}
	first := rt2.deadline
	eng.syncRoundTimer(rt2, false)
	if rt2.deadline != first {
		t.Fatal("re-evaluating the same view moved its deadline")
	}
	eng.SetHeight(3) // set[0], this node, leads round 0
	eng.syncRoundTimer(rt2, false)
	if rt2.armed {
		t.Fatal("armed while this node leads the view and has proposed nothing")
	}

	// An open proposal: the round timeout, renewed only by a rebroadcast at
	// the same view, then a fresh wait for the next leader after a timeout.
	two, twoKeys := testSet(t, 2)
	eng = engineFor(t, two, twoKeys, 0, BFTConfig{RoundTimeout: time.Hour})
	eng.proposalWait = time.Minute
	eng.SetHeight(4) // set[0] leads round 0
	rt3 := newRoundTimer()
	defer rt3.disarm()
	blk := proposalAt(t, two, twoKeys, 4, 0, 1).Block
	if !eng.handleProposal(&pendingProposal{block: blk, local: true}) {
		t.Fatal("own proposal refused")
	}
	eng.syncRoundTimer(rt3, true)
	if !rt3.armed || rt3.height != 4 || rt3.round != 0 {
		t.Fatalf("open proposal: armed=%v for %d/%d, want 4/0", rt3.armed, rt3.height, rt3.round)
	}
	if left := time.Until(rt3.deadline); left > time.Hour || left < time.Hour-time.Minute {
		t.Fatalf("an open proposal is allowed %v, want the round timeout", left)
	}
	d1 := rt3.deadline
	eng.syncRoundTimer(rt3, false) // a vote that did not complete the quorum
	if rt3.deadline != d1 {
		t.Fatal("an event that changed nothing moved the deadline")
	}
	time.Sleep(5 * time.Millisecond)
	if !eng.handleProposal(&pendingProposal{block: blk, local: true}) {
		t.Fatal("rebroadcast of the locked proposal refused")
	}
	eng.syncRoundTimer(rt3, true)
	if !rt3.deadline.After(d1) {
		t.Fatal("the leader's rebroadcast did not renew the deadline")
	}
	eng.handleTimeout() // round 1: set[1] leads
	eng.syncRoundTimer(rt3, false)
	if !rt3.armed || rt3.height != 4 || rt3.round != 1 {
		t.Fatalf("after the view change: armed=%v for %d/%d, want 4/1", rt3.armed, rt3.height, rt3.round)
	}
}

// A deadline that fires for a view the engine has already left — the height
// committed, the round moved, SetHeight ran from the producer's tick — must
// change nothing.
func TestStaleRoundTimerFiringIsIgnored(t *testing.T) {
	set, keys := testSet(t, 3)
	viewChanged := make(chan struct{}, 4)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		OnViewChange: func(uint64, uint32) { viewChanged <- struct{}{} },
	})
	eng.SetHeight(3)
	if eng.expireRound(1, 0) {
		t.Fatal("a deadline for height 1 fired at height 3")
	}
	if eng.Round() != 0 {
		t.Fatalf("round %d after a stale firing, want 0", eng.Round())
	}
	select {
	case <-viewChanged:
		t.Fatal("a stale firing triggered a view change")
	case <-time.After(50 * time.Millisecond):
	}
	if !eng.expireRound(3, 0) {
		t.Fatal("the deadline for the current view did not fire")
	}
	if eng.Round() != 1 {
		t.Fatalf("round %d after the current view timed out, want 1", eng.Round())
	}
	select {
	case <-viewChanged:
	case <-time.After(time.Second):
		t.Fatal("no view change after the current view timed out")
	}
}
