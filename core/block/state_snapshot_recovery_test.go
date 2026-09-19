package block

import (
	"errors"
	"os"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
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

// TestReconcileSnapshotImportPhaseBlockStored simulates a stale "block_stored"
// marker whose block never actually landed (e.g. storeBlock was the crash
// point, or the disk state was rolled back). Recovery reconciles from the
// ACTUAL stores, not the phase: with neither block nor state present it treats
// the import as never started and returns to a clean slate, deleting the
// marker rather than trusting a phase that no longer matches reality.
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

	if err := ex.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("ReconcileSnapshotImport(stale block_stored marker) = %v, want nil (clean slate)", err)
	}

	got, rerr := ex.readSnapshotMarker()
	if rerr != nil || got != nil {
		t.Fatalf("marker after recovery = (%+v, %v), want (nil, nil)", got, rerr)
	}
	if head, err := ex.LatestBlock(); err != nil || head != nil {
		t.Fatalf("chain head after recovery = (%v, %v), want (nil, nil)", head, err)
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

// TestReconcileCompletesBlockStoredImportFromStaging simulates a crash after
// the head block landed but before the state import: marker (still stuck at
// "started" because the advancing write was itself the crash point), the
// retained snapshot, and the block are all on disk, with state empty. Recovery
// must complete the state from the retained snapshot and leave both stores
// consistent, with marker and retention cleaned up.
func TestReconcileCompletesBlockStoredImportFromStaging(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseStarted,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.writeSnapshotStaging(snap); err != nil {
		t.Fatal(err)
	}
	if err := exB.storeBlock(snap.Block); err != nil {
		t.Fatal(err)
	}

	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got, err := exB.GetBlockByHeight(snap.Height); err != nil || got.Hash != snap.Block.Hash {
		t.Fatalf("block after recovery = (hash %x, err %v), want %x", got.Hash[:8], err, snap.Block.Hash[:8])
	}
	if h, ok := sdbB.AppliedHeight(); !ok || h != snap.Height {
		t.Fatalf("applied height after recovery = (%d,%v), want (%d,true)", h, ok, snap.Height)
	}
	if root, _ := sdbB.StateRoot(); root != snap.Block.Header.StateRoot {
		t.Fatalf("state root after recovery = %x, want %x", root[:8], snap.Block.Header.StateRoot[:8])
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after recovery = (%v,%v), want (nil,nil)", got, err)
	}
	if _, err := os.Stat(exB.snapshotStagingPath()); !os.IsNotExist(err) {
		t.Fatalf("retained snapshot still present after recovery: %v", err)
	}
}

// TestReconcileRecognizesAlreadyCompletedImport simulates a crash after the
// state commit but before the marker/retention deletion: block AND state are
// both present and correct. Recovery must recognise the import as done and just
// drop the marker/retention, leaving the stores untouched.
func TestReconcileRecognizesAlreadyCompletedImport(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseBlockStored,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.writeSnapshotStaging(snap); err != nil {
		t.Fatal(err)
	}
	if err := exB.storeBlock(snap.Block); err != nil {
		t.Fatal(err)
	}
	if err := exB.state.ImportSnapshot(snap.Leaves, snap.Height, snap.Block.Header.StateRoot); err != nil {
		t.Fatal(err)
	}

	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if root, _ := sdbB.StateRoot(); root != snap.Block.Header.StateRoot {
		t.Fatalf("state root changed by recovery: %x, want %x", root[:8], snap.Block.Header.StateRoot[:8])
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after recovery = (%v,%v), want (nil,nil)", got, err)
	}
	if _, err := os.Stat(exB.snapshotStagingPath()); !os.IsNotExist(err) {
		t.Fatalf("retained snapshot still present after recovery: %v", err)
	}
}

// TestReconcileCompletesNothingPersistedFromStaging: marker and verified
// staging exist, but neither store does. Recovery must restore both from
// staging rather than discard evidence that can finish the import.
func TestReconcileCompletesNothingPersistedFromStaging(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseStarted,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.writeSnapshotStaging(snap); err != nil {
		t.Fatal(err)
	}

	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got, err := exB.GetBlockByHeight(snap.Height); err != nil || got.Hash != snap.Block.Hash {
		t.Fatalf("block after recovery = (%v, %v), want %x", got, err, snap.Block.Hash[:8])
	}
	if h, ok := sdbB.AppliedHeight(); !ok || h != snap.Height {
		t.Fatalf("applied height after recovery = (%d,%v), want %d", h, ok, snap.Height)
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after recovery = (%v,%v), want (nil,nil)", got, err)
	}
	if _, err := os.Stat(exB.snapshotStagingPath()); !os.IsNotExist(err) {
		t.Fatalf("retained snapshot still present after recovery: %v", err)
	}
}

// TestReconcileDiscardsNothingPersisted is the no-staging counterpart: a stale
// marker with empty stores and no verified snapshot must be dropped so retry
// can start clean.
func TestReconcileDiscardsNothingPersisted(t *testing.T) {
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	marker := &snapshotImportMarker{
		Height: 5, BlockHash: [32]byte{0x01}, StateRoot: [32]byte{0x02},
		ChainID: "test-chain", Phase: phaseStarted,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}

	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after recovery = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestReconcileRefusesCorruptedStaging simulates a crash after the block
// landed, but the retained snapshot has been corrupted. Recovery must refuse
// rather than import leaves that no longer hash to the committed root.
func TestReconcileRefusesCorruptedStaging(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()

	staged := *snap
	staged.Leaves = append([]types.StateLeaf(nil), snap.Leaves...)
	staged.Leaves[0].Value = append([]byte(nil), staged.Leaves[0].Value...)
	staged.Leaves[0].Value[0] ^= 0x01

	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseBlockStored,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.writeSnapshotStaging(&staged); err != nil {
		t.Fatal(err)
	}
	if err := exB.storeBlock(snap.Block); err != nil {
		t.Fatal(err)
	}

	if err := exB.ReconcileSnapshotImport(); err == nil {
		t.Fatal("reconcile accepted a corrupted retained snapshot")
	}
	if h, ok := sdbB.AppliedHeight(); ok {
		t.Fatalf("corrupted snapshot was imported anyway: applied height = %d", h)
	}
	if got, err := exB.readSnapshotMarker(); err != nil || got == nil {
		t.Fatalf("marker should still be present after refused recovery, got (%v,%v)", got, err)
	}
}

// R4: matching applied state without the head must not drop evidence. Staging
// that still has the block lets recovery restore it, then delete.
func TestReconcileRestoresMissingBlockWhenStateMatches(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	if err := sdbB.ImportSnapshot(snap.Leaves, snap.Height, snap.Block.Header.StateRoot); err != nil {
		t.Fatal(err)
	}
	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseComplete,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.writeSnapshotStaging(snap); err != nil {
		t.Fatal(err)
	}
	if err := exB.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := exB.GetBlockByHeight(snap.Height)
	if err != nil || got.Hash != snap.Block.Hash {
		t.Fatalf("restored block = (%v, %v)", got, err)
	}
	if m, _ := exB.readSnapshotMarker(); m != nil {
		t.Fatal("evidence should be gone after restore")
	}
}

// R4: matching state, missing block, no staging — keep the marker.
func TestReconcileKeepsEvidenceWhenStateMatchesAndBlockMissing(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	if err := sdbB.ImportSnapshot(snap.Leaves, snap.Height, snap.Block.Header.StateRoot); err != nil {
		t.Fatal(err)
	}
	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseComplete,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.ReconcileSnapshotImport(); err == nil {
		t.Fatal("expected error when the head cannot be restored")
	}
	if m, _ := exB.readSnapshotMarker(); m == nil {
		t.Fatal("marker was dropped even though the block is still missing")
	}
}

// R4: missing block + dirty/mismatched state must never delete evidence.
func TestReconcileKeepsEvidenceWhenStateMismatchedAndBlockMissing(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, testAddr(0x42), 100)
	marker := &snapshotImportMarker{
		Height: 3, BlockHash: [32]byte{0x09}, StateRoot: [32]byte{0x0a},
		ChainID: "test-chain", Phase: phaseStarted,
	}
	if err := ex.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := ex.ReconcileSnapshotImport(); err == nil {
		t.Fatal("mismatched state + missing block must not look like a clean slate")
	}
	if m, _ := ex.readSnapshotMarker(); m == nil {
		t.Fatal("evidence dropped on missing block + mismatched state")
	}
}

// R4: legacy interrupted import (block stored, no staging) is retryable.
func TestReconcileLegacyNoStagingIsIncomplete(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseBlockStored,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.storeBlock(snap.Block); err != nil {
		t.Fatal(err)
	}
	// An incomplete R3 download must not make reconcile fatal: the retry
	// path still owns that directory.
	dl, err := state.OpenSnapshotDownload(exB.snapshotDownloadPath(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := dl.AcceptHead(snap.Block, snap.Height); err != nil {
		t.Fatal(err)
	}
	if len(snap.Leaves) == 0 {
		t.Fatal("need leaves")
	}
	if err := dl.AppendPage(snap.Leaves[:1], snap.Leaves[0].Key, false, "partial"); err != nil {
		t.Fatal(err)
	}
	err = exB.ReconcileSnapshotImport()
	if err == nil || !errors.Is(err, ErrSnapshotImportIncomplete) {
		t.Fatalf("legacy no-staging / partial download = %v, want ErrSnapshotImportIncomplete", err)
	}
	if m, _ := exB.readSnapshotMarker(); m == nil {
		t.Fatal("marker dropped on a retryable incomplete import")
	}
	if !exB.HasIncompleteSnapshotImport() {
		t.Fatal("HasIncompleteSnapshotImport = false")
	}

	if err := exB.ReconcileState(); err != nil {
		t.Fatalf("ReconcileState during incomplete snapshot import = %v, want nil (must not replay the snapshot head)", err)
	}
	if h, ok := sdbB.AppliedHeight(); ok {
		t.Fatalf("ReconcileState applied height %d; it must not replay a snapshot head", h)
	}
	if err := exB.ImportStateSnapshot(snap); err != nil {
		t.Fatalf("resume import: %v", err)
	}
	if h, ok := sdbB.AppliedHeight(); !ok || h != snap.Height {
		t.Fatalf("applied after resume = (%d,%v)", h, ok)
	}
	if m, _ := exB.readSnapshotMarker(); m != nil {
		t.Fatal("marker left after resumed import")
	}
}

// TestReconcileResumesInterruptedBoundedBatchPublish simulates a crash in the
// middle of ImportSnapshotJSONLResume's bounded-batch publish: the block is
// stored and the leaf VALUES were written, but the height marker (written last)
// never landed. Recovery must recognise the resumable partial state and re-run
// the import from the retained, verified download — not refuse it as a mismatch
// or discard the evidence.
func TestReconcileResumesInterruptedBoundedBatchPublish(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	statePath := t.TempDir()
	chainPath := t.TempDir()
	sdbB, err := state.NewStateDB(statePath)
	if err != nil {
		t.Fatal(err)
	}
	exB, err := NewExecutor(sdbB, chainPath)
	if err != nil {
		t.Fatal(err)
	}

	marker := &snapshotImportMarker{
		Height: snap.Height, BlockHash: snap.Block.Hash, StateRoot: snap.Block.Header.StateRoot,
		ChainID: snap.Block.Header.ChainID, Phase: phaseBlockStored,
	}
	if err := exB.writeSnapshotMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := exB.storeBlock(snap.Block); err != nil {
		t.Fatal(err)
	}

	// A complete, verified download the recovery path can resume from.
	dl, err := state.OpenSnapshotDownload(exB.snapshotDownloadPath(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := dl.AcceptHead(snap.Block, snap.Height); err != nil {
		t.Fatal(err)
	}
	if err := dl.AppendPage(snap.Leaves, nil, true, ""); err != nil {
		t.Fatal(err)
	}

	// Simulate the interrupted publish: close everything, write the leaf values
	// directly (no height marker), and reopen so loadLeaves repopulates the
	// in-memory index — exactly what a restart would observe.
	if err := exB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sdbB.Close(); err != nil {
		t.Fatal(err)
	}
	rawDB, err := leveldb.OpenFile(statePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	partial := new(leveldb.Batch)
	for _, l := range snap.Leaves {
		partial.Put(l.Key, l.Value)
	}
	if err := rawDB.Write(partial, nil); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	rawDB.Close()

	sdbB2, err := state.NewStateDB(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer sdbB2.Close()
	exB2, err := NewExecutor(sdbB2, chainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer exB2.Close()

	if !sdbB2.CanResumeSnapshotImport() {
		t.Fatal("partial publish must be resumable")
	}

	if err := exB2.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if h, ok := sdbB2.AppliedHeight(); !ok || h != snap.Height {
		t.Fatalf("applied height after resume = (%d,%v), want (%d,true)", h, ok, snap.Height)
	}
	if root, _ := sdbB2.StateRoot(); root != snap.Block.Header.StateRoot {
		t.Fatalf("state root after resume = %x, want %x", root[:8], snap.Block.Header.StateRoot[:8])
	}
	if got, err := exB2.readSnapshotMarker(); err != nil || got != nil {
		t.Fatalf("marker after resume = (%v,%v), want (nil,nil)", got, err)
	}
}
