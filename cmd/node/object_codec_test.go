package main

import "testing"

func TestRepairFor(t *testing.T) {
	if got := repairFor(1); got != 1 {
		t.Fatalf("repairFor(1) = %d, want 1", got)
	}
	if got := repairFor(16); got != 4 {
		t.Fatalf("repairFor(16) = %d, want 4", got)
	}
}
