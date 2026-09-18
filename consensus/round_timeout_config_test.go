package consensus

import (
	"testing"
	"time"
)

// A set spread across regions needs a longer view than a set in one rack. On
// the live four-validator run every view change in a ten-minute window was a
// height led by the one member behind 200ms of added latency — it missed 29 of
// its 84 leader slots, 34.5%, while the three co-located members missed none of
// theirs. Three seconds is not enough time for that member to receive the
// previous block, build on it, sign with ML-DSA-65 and get a proposal back
// across the link, so its peers time out, advance, and then discard the
// proposal when it does arrive.
//
// So the deadline has to be an operator's choice. These pin that a configured
// value reaches the timer, and that leaving it unset keeps today's behaviour.
func TestRoundTimeoutIsConfigurable(t *testing.T) {
	set, keys := testSet(t, 3)

	t.Run("unset keeps the defaults", func(t *testing.T) {
		eng, err := NewBFTEngine(BFTConfig{
			LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		})
		if err != nil {
			t.Fatal(err)
		}
		if eng.roundTimeout != DefaultRoundTimeout {
			t.Fatalf("round timeout = %v, want the %v default", eng.roundTimeout, DefaultRoundTimeout)
		}
		if eng.voteTimeout != DefaultVoteTimeout {
			t.Fatalf("vote timeout = %v, want the %v default", eng.voteTimeout, DefaultVoteTimeout)
		}
		if DefaultRoundTimeout != 3*time.Second || DefaultVoteTimeout != 800*time.Millisecond {
			t.Fatal("the defaults moved; existing deployments would change behaviour on upgrade")
		}
	})

	t.Run("a configured value reaches the round timer", func(t *testing.T) {
		const configured = 25 * time.Second
		eng, err := NewBFTEngine(BFTConfig{
			LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
			RoundTimeout: configured,
			VoteTimeout:  7 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		eng.UpdateValidatorSet(set)
		if eng.roundTimeout != configured || eng.voteTimeout != 7*time.Second {
			t.Fatalf("engine took round=%v vote=%v", eng.roundTimeout, eng.voteTimeout)
		}

		// Waiting on a remote leader's proposal: roundTimeout + proposalWait.
		eng.proposalWait = 2 * time.Second
		eng.SetHeight(1) // (1+0)%3 == 1 → set[1] leads, so this node waits
		rt := newRoundTimer()
		defer rt.disarm()
		eng.syncRoundTimer(rt, false)
		if !rt.armed {
			t.Fatal("waiting for a remote leader with no timer armed")
		}
		if left := time.Until(rt.deadline); left < configured+time.Second {
			t.Fatalf("deadline is %v away, want about %v", left, configured+2*time.Second)
		}

		// Holding a proposal and waiting for votes: the round timeout itself.
		eng.SetHeight(3) // (3+0)%3 == 0 → this node leads
		blk := proposalAt(t, set, keys, 3, 0, 1).Block
		if !eng.handleProposal(&pendingProposal{block: blk, local: true}) {
			t.Fatal("own proposal refused")
		}
		rt2 := newRoundTimer()
		defer rt2.disarm()
		eng.syncRoundTimer(rt2, true)
		if left := time.Until(rt2.deadline); left > configured || left < configured-time.Second {
			t.Fatalf("an open proposal is allowed %v, want the configured %v", left, configured)
		}
	})

	t.Run("a short timeout still fires a view change", func(t *testing.T) {
		eng := engineFor(t, set, keys, 0, BFTConfig{RoundTimeout: 30 * time.Millisecond})
		eng.SetHeight(1) // set[1] leads, so this node waits on it
		if got := eng.Round(); got != 0 {
			t.Fatalf("round = %d before the timeout", got)
		}
		if !eng.expireRound(1, 0) {
			t.Fatal("the deadline for the current view did not change it")
		}
		if got := eng.Round(); got != 1 {
			t.Fatalf("round = %d after the timeout, want 1", got)
		}
	})
}
