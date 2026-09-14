package block

import (
	"os"
	"testing"
)

// TestSnapshotMarkerWriteReadDelete exercises the basic marker lifecycle:
// write, read back identical contents, delete, and confirm it is gone.
func TestSnapshotMarkerWriteReadDelete(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	if got, err := ex.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("fresh executor: readSnapshotMarker = (%v, %v), want (nil, nil)", got, err)
	}

	marker := &snapshotImportMarker{
		Height:    7,
		BlockHash: [32]byte{0xaa},
		StateRoot: [32]byte{0xbb},
		ChainID:   "test-chain",
		Phase:     phaseStarted,
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("writeSnapshotMarker: %v", err)
	}
	if _, err := os.Stat(ex.snapshotMarkerPath()); err != nil {
		t.Fatalf("marker file missing after write: %v", err)
	}

	got, err := ex.readSnapshotMarker()
	if err != nil {
		t.Fatalf("readSnapshotMarker: %v", err)
	}
	if got == nil || got.Height != 7 || got.Phase != phaseStarted || got.ChainID != "test-chain" ||
		got.BlockHash != marker.BlockHash || got.StateRoot != marker.StateRoot {
		t.Fatalf("readSnapshotMarker = %+v, want %+v", got, marker)
	}

	// Update phase and confirm the write is idempotent / overwrites cleanly.
	marker.Phase = phaseBlockStored
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("re-write marker: %v", err)
	}
	got, err = ex.readSnapshotMarker()
	if err != nil || got == nil || got.Phase != phaseBlockStored {
		t.Fatalf("after phase update: readSnapshotMarker = (%+v, %v), want phase=%s", got, err, phaseBlockStored)
	}

	if err := ex.deleteSnapshotMarker(); err != nil {
		t.Fatalf("deleteSnapshotMarker: %v", err)
	}
	if _, err := os.Stat(ex.snapshotMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("marker file still present after delete: err=%v", err)
	}
	// Deleting an already-absent marker must not error (idempotent cleanup).
	if err := ex.deleteSnapshotMarker(); err != nil {
		t.Fatalf("delete of already-absent marker: %v", err)
	}
}

// TestReconcileSnapshotImportNoMarker is the common case: a node that never
// attempted (or already completed and cleaned up) a snapshot import must see
// ReconcileSnapshotImport as a pure no-op.
func TestReconcileSnapshotImportNoMarker(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	if err := ex.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("ReconcileSnapshotImport with no marker = %v, want nil", err)
	}
}

// TestReconcileSnapshotImportPhaseStarted simulates a crash immediately after
// the marker was written but before anything else was touched: nothing was
// persisted, so recovery must simply discard the stale marker and let the
// node retry the import from scratch on the next attempt.
func TestReconcileSnapshotImportPhaseStarted(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	marker := &snapshotImportMarker{
		Height: 5, BlockHash: [32]byte{0x01}, StateRoot: [32]byte{0x02},
		ChainID: "test-chain", Phase: phaseStarted,
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("writeSnapshotMarker: %v", err)
	}

	if err := ex.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("ReconcileSnapshotImport(phaseStarted) = %v, want nil (marker discarded)", err)
	}
	if got, err := ex.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker should be gone after recovery, got (%+v, %v)", got, err)
	}
	// Chain must still be empty — recovery from "started" never stores anything.
	if head, err := ex.LatestBlock(); err != nil || head != nil {
		t.Fatalf("chain head after phaseStarted recovery = (%v, %v), want (nil, nil)", head, err)
	}
}

// TestReconcileSnapshotImportPhaseBlockStored simulates a crash after the
// block was durably stored but before state import completed. Recovery
// cannot safely auto-complete without the original snapshot leaves, so it
// must surface an actionable error and MUST NOT delete the marker (which
// would erase the only on-disk evidence that state is missing).
func TestReconcileSnapshotImportPhaseBlockStored(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	marker := &snapshotImportMarker{
		Height: 3, BlockHash: [32]byte{0x03}, StateRoot: [32]byte{0x04},
		ChainID: "test-chain", Phase: phaseBlockStored,
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("writeSnapshotMarker: %v", err)
	}

	err := ex.ReconcileSnapshotImport()
	if err == nil {
		t.Fatal("ReconcileSnapshotImport(phaseBlockStored) = nil, want an error demanding a retry")
	}

	// The marker must survive: it is the only record that state is missing.
	got, rerr := ex.readSnapshotMarker()
	if rerr != nil || got == nil || got.Phase != phaseBlockStored {
		t.Fatalf("marker after failed recovery = (%+v, %v), want unchanged phase=%s", got, rerr, phaseBlockStored)
	}
}

// TestReconcileSnapshotImportPhaseComplete simulates a crash after both the
// block and state were committed but before the marker's own final delete
// landed. Recovery must recognise the import as done and clean the marker up
// silently — there is nothing left to redo.
func TestReconcileSnapshotImportPhaseComplete(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	marker := &snapshotImportMarker{
		Height: 9, BlockHash: [32]byte{0x05}, StateRoot: [32]byte{0x06},
		ChainID: "test-chain", Phase: phaseComplete,
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("writeSnapshotMarker: %v", err)
	}

	if err := ex.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("ReconcileSnapshotImport(phaseComplete) = %v, want nil", err)
	}
	if got, err := ex.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker should be gone after phaseComplete recovery, got (%+v, %v)", got, err)
	}
}

// TestReconcileSnapshotImportUnknownPhase guards against a corrupted or
// future-version marker silently being treated as safe.
func TestReconcileSnapshotImportUnknownPhase(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	marker := &snapshotImportMarker{
		Height: 1, BlockHash: [32]byte{0x07}, StateRoot: [32]byte{0x08},
		ChainID: "test-chain", Phase: "some-future-phase",
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatalf("writeSnapshotMarker: %v", err)
	}
	if err := ex.ReconcileSnapshotImport(); err == nil {
		t.Fatal("ReconcileSnapshotImport with an unknown phase = nil, want an error")
	}
}

// TestImportStateSnapshotCleansUpMarkerOnSuccess is the full happy path
// through the real ImportStateSnapshot: after a successful import the
// recovery marker must be gone, leaving nothing for ReconcileSnapshotImport
// to trip over on the next startup.
func TestImportStateSnapshotCleansUpMarkerOnSuccess(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	if err := exB.ImportStateSnapshot(snap); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after successful import = (%+v, %v), want (nil, nil)", got, err)
	}
	// A subsequent reconcile at startup must be a clean no-op.
	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("ReconcileSnapshotImport after successful import = %v, want nil", err)
	}
}

// TestImportStateSnapshotLeavesNoMarkerOnRootMismatch confirms a rejected
// import (e.g. tampered leaves) does not leave a dangling recovery marker
// behind — the marker is written only after the structural checks pass, so a
// pre-flight rejection must never allocate one.
func TestImportStateSnapshotLeavesNoMarkerOnRootMismatch(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snap.Leaves[0].Value = append([]byte(nil), snap.Leaves[0].Value...)
	snap.Leaves[0].Value[0] ^= 0x01

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	if err := exB.ImportStateSnapshot(snap); err == nil {
		t.Fatal("import of tampered leaves accepted")
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker left behind by a rejected import: (%+v, %v)", got, err)
	}
}
