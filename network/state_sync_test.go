package network

import (
	"testing"
	"time"
)

func TestRemainingRequestTimeoutUsesOverallBudget(t *testing.T) {
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(15 * time.Minute)
	if got := remainingRequestTimeout(deadline, 3*time.Minute, now); got != 3*time.Minute {
		t.Fatalf("plenty of budget: got %s, want per-request cap", got)
	}
	if got := remainingRequestTimeout(deadline, 3*time.Minute, deadline.Add(-30*time.Second)); got != 30*time.Second {
		t.Fatalf("near deadline: got %s, want remaining 30s (not another full 3m)", got)
	}
	if got := remainingRequestTimeout(deadline, 3*time.Minute, deadline.Add(time.Second)); got != 0 {
		t.Fatalf("past deadline: got %s, want 0", got)
	}
}
