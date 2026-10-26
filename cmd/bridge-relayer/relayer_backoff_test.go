package main

import (
	"strings"
	"testing"
	"time"
)

func TestRelayerTransientBackoffGrowsAndCaps(t *testing.T) {
	r, s := newRelayerFixture(t)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", strings.Repeat("cd", 32), 50)}
	s.failNext = 100

	pass := func(wantFailures int) {
		t.Helper()
		if _, err := r.RunOnce(); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		failures := 100 - s.failNext
		s.mu.Unlock()
		if failures != wantFailures {
			t.Fatalf("at %s: submit failures = %d, want %d", now, failures, wantFailures)
		}
	}

	pass(1)
	for i, seconds := range []int{1, 2, 4, 8, 16, 32, 60, 60} {
		// A suppressed pass must neither submit nor extend the retry deadline.
		now = now.Add(time.Duration(seconds)*time.Second - time.Nanosecond)
		pass(i + 1)
		now = now.Add(time.Nanosecond)
		pass(i + 2)
	}
	if r.attempts[1] != 0 || r.State.LastProcessed != 0 || len(r.State.Skipped) != 0 {
		t.Fatalf("transient failures affected skip state: attempts=%d state=%+v", r.attempts[1], r.State)
	}
}

func TestRelayerTransientBackoffAtDefaultPoll(t *testing.T) {
	r, s := newRelayerFixture(t)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", strings.Repeat("cd", 32), 50)}
	s.failNext = 100

	// With the default 15s polling cadence, retries first get suppressed at
	// 75s (16s backoff), then for multiple polls as backoff reaches its cap.
	for poll, wantFailures := range []int{1, 2, 3, 4, 5, 5, 6, 6, 6, 7, 7, 7, 7, 8} {
		if _, err := r.RunOnce(); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		failures := 100 - s.failNext
		s.mu.Unlock()
		if failures != wantFailures {
			t.Fatalf("poll at %ds: submit failures = %d, want %d", poll*15, failures, wantFailures)
		}
		now = now.Add(15 * time.Second)
	}
}

func TestRelayerTransientBackoffClears(t *testing.T) {
	for _, outcome := range []string{"claimed", "mined revert", "pending receipt", "already claimed"} {
		t.Run(outcome, func(t *testing.T) {
			r, s := newRelayerFixture(t)
			now := time.Unix(0, 0)
			r.now = func() time.Time { return now }
			s.head = 300
			s.logs = []evmLog{makeLog(1, 0, "1000", strings.Repeat("cd", 32), 50)}
			s.failNext = 1
			if _, err := r.RunOnce(); err != nil {
				t.Fatal(err)
			}
			if _, ok := r.transientAt[1]; !ok || r.transientFailures[1] != 1 {
				t.Fatal("transient failure did not establish backoff state")
			}

			s.mu.Lock()
			switch outcome {
			case "mined revert":
				s.revert = true
			case "pending receipt":
				r.ReceiptWait = 0
			case "already claimed":
				s.claimed[1] = true
			}
			s.mu.Unlock()
			// An on-chain claim can settle even while the backoff is active.
			if outcome != "already claimed" {
				now = now.Add(time.Second)
			}
			if _, err := r.RunOnce(); err != nil {
				t.Fatal(err)
			}
			if _, ok := r.transientAt[1]; ok {
				t.Fatal("stale transient timestamp after successful submit or existing claim")
			}
			if _, ok := r.transientFailures[1]; ok {
				t.Fatal("stale transient failure count after successful submit or existing claim")
			}
			if outcome == "mined revert" && r.attempts[1] != 1 {
				t.Fatalf("mined revert count = %d, want 1", r.attempts[1])
			}
		})
	}
}

func TestRelayerTransientBackoffIsPerBurn(t *testing.T) {
	r, s := newRelayerFixture(t)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }
	s.failNext = 1
	burn := Burn{ID: 1, Asset: "spld", Amount: mustBig(t, "1000"), Recipient: "SQ:" + strings.Repeat("cd", 32), TxHash: "0x" + strings.Repeat("ab", 32), Block: 50}
	info := &BridgeInfo{Threshold: 1}
	// Prior mined reverts must not enlarge this burn's first transient delay.
	r.attempts = map[uint64]int{1: 3}
	if ok, err := r.settle(burn, info); err != nil || ok {
		t.Fatalf("initial rejection: ok=%v err=%v", ok, err)
	}
	other := burn
	other.ID = 2
	if ok, err := r.settle(other, info); err != nil || !ok {
		t.Fatalf("unrelated burn held by backoff: ok=%v err=%v", ok, err)
	}
	if _, ok := r.transientAt[1]; !ok || r.transientFailures[1] != 1 {
		t.Fatal("settling another burn cleared the original burn's backoff")
	}
	now = now.Add(time.Second)
	if ok, err := r.settle(burn, info); err != nil || !ok {
		t.Fatalf("first transient retry affected by mined reverts: ok=%v err=%v", ok, err)
	}
}
