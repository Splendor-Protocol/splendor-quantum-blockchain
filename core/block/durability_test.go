package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// C3: chain head and state commit go through persist.Write (Sync=true).
// After close+reopen the stored block, applied height, and state root remain.
func TestStoreBlockAndStateCommitSurviveReopen(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x17)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	stateDir := t.TempDir()
	chainDir := t.TempDir()
	sdb, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := NewExecutor(sdb, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, sdb, from, 1_000_000)
	tx := signedTransfer(t, from, priv, to, 50, 0)
	b, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	sdb2, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb2.Close()
	ex2, err := NewExecutor(sdb2, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ex2.Close()

	head, err := ex2.LatestBlock()
	if err != nil || head == nil || head.Hash != b.Hash {
		t.Fatalf("reopened head = %v err=%v, want %x", head, err, b.Hash[:4])
	}
	if !ex2.BlockFullyApplied(b) {
		h, _ := sdb2.AppliedHeight()
		root, _ := sdb2.StateRoot()
		t.Fatalf("reopened stores not fully applied: height=%d root=%x want height=1 root=%x", h, root[:4], b.Header.StateRoot[:4])
	}
}

// Snapshot import batches also use persist.Write. After close+reopen the
// imported head and applied height remain.
func TestSnapshotImportSurvivesReopen(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	stateDir := t.TempDir()
	chainDir := t.TempDir()
	sdb, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := NewExecutor(sdb, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.ImportStateSnapshot(snap); err != nil {
		t.Fatalf("import: %v", err)
	}
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	sdb2, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb2.Close()
	ex2, err := NewExecutor(sdb2, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ex2.Close()
	if err := ex2.ReconcileSnapshotImport(); err != nil {
		t.Fatalf("reconcile after reopen: %v", err)
	}
	head, err := ex2.LatestBlock()
	if err != nil || head == nil || head.Hash != snap.Block.Hash {
		t.Fatalf("reopened imported head = %v err=%v", head, err)
	}
	if h, ok := sdb2.AppliedHeight(); !ok || h != snap.Height {
		t.Fatalf("reopened applied height = (%d, %v), want %d", h, ok, snap.Height)
	}
}
