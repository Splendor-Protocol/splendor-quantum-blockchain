package main

import "testing"

func TestRepairFor(t *testing.T) {
	if got := repairFor(1); got != 1 {
		t.Fatalf("repairFor(1) = %d, want 1", got)
	}
	if got := repairFor(16); got != 8 {
		t.Fatalf("repairFor(16) = %d, want 8 (50%% repair: measured netbench recovery at 20%% loss)", got)
	}
}

func TestObjectSnapshotStale(t *testing.T) {
	if objectSnapshotStale(100, 100+objectSnapshotRepublishLag) {
		t.Fatal("a snapshot exactly at the lag boundary is still fresh")
	}
	if !objectSnapshotStale(100, 100+objectSnapshotRepublishLag+1) {
		t.Fatal("a snapshot one block past the lag boundary must be stale")
	}
	if objectSnapshotStale(0, objectSnapshotRepublishLag) {
		t.Fatal("a fresh chain under the lag is not stale")
	}
}
