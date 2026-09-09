package block

import (
	"errors"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// threeBlockChain funds `from` and seals three transfer-bearing blocks on a
// fresh executor, returning it with the producer's keys.
func threeBlockChain(t *testing.T) (*Executor, *state.StateDB, *qcrypto.PublicKey, *qcrypto.PrivateKey, qcrypto.Address, *qcrypto.PrivateKey) {
	t.Helper()
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	t.Cleanup(func() { ex.Close(); sdb.Close() })
	fund(t, sdb, from, 1_000_000)
	var prev [32]byte
	for h := uint64(1); h <= 3; h++ {
		tx := signedTransfer(t, from, priv, testAddr(0x77), 100, h-1)
		b, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, prev, h)
		if err != nil {
			t.Fatalf("produce %d: %v", h, err)
		}
		prev = b.Hash
	}
	return ex, sdb, vpub, vpriv, from, priv
}

func TestExportStateSnapshotCarriesHeadBlock(t *testing.T) {
	ex, sdb, _, _, _, _ := threeBlockChain(t)
	snap, err := ex.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	head, _ := ex.LatestBlock()
	if snap.Height != 3 || snap.Block == nil || snap.Block.Header.Height != 3 || snap.Block.Hash != head.Hash {
		t.Fatalf("snapshot height %d block %v, want the head block at 3", snap.Height, snap.Block != nil)
	}
	if got := state.SnapshotRoot(snap.Leaves); got != snap.Block.Header.StateRoot {
		t.Fatalf("leaves hash to %x, head state_root is %x", got[:8], snap.Block.Header.StateRoot[:8])
	}
	if root, _ := sdb.StateRoot(); root != snap.Block.Header.StateRoot {
		t.Fatal("committed root differs from the head block's state_root")
	}
}

func TestExportStateSnapshotRefusesEmptyChain(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	if _, err := ex.ExportStateSnapshot(); !errors.Is(err, state.ErrSnapshotUnavailable) {
		t.Fatalf("export on an empty chain = %v, want ErrSnapshotUnavailable", err)
	}
}

// Export from A after three blocks, import into a fresh B, and B applies A's
// block 4 — the whole point: B never replayed 1..3.
func TestImportStateSnapshotRoundTrip(t *testing.T) {
	exA, sdbA, vpub, vpriv, from, priv := threeBlockChain(t)
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
	headB, _ := exB.LatestBlock()
	if headB == nil || headB.Header.Height != 3 || headB.Hash != snap.Block.Hash {
		t.Fatalf("B head after import = %+v, want A's block 3", headB)
	}
	if h, ok := sdbB.AppliedHeight(); !ok || h != 3 {
		t.Fatalf("B state height = (%d, %v), want (3, true)", h, ok)
	}
	rootA, _ := sdbA.StateRoot()
	if rootB, _ := sdbB.StateRoot(); rootB != rootA {
		t.Fatalf("B root %x != A root %x", rootB[:8], rootA[:8])
	}

	tx := signedTransfer(t, from, priv, testAddr(0x77), 100, 3)
	b4, err := exA.ProduceBlock([]*types.Transaction{tx}, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, snap.Block.Hash, 4)
	if err != nil {
		t.Fatalf("produce 4: %v", err)
	}
	if err := exB.ExecuteBlock(b4); err != nil {
		t.Fatalf("B could not apply A's block 4 on the imported state: %v", err)
	}
	if h, _ := exB.LatestHeight(); h != 4 {
		t.Fatalf("B head = %d, want 4", h)
	}
	rootA, _ = sdbA.StateRoot()
	if rootB, _ := sdbB.StateRoot(); rootB != rootA {
		t.Fatalf("after block 4: B root %x != A root %x", rootB[:8], rootA[:8])
	}
}

// A tampered leaf fails the header root; nothing is written on either DB.
func TestImportStateSnapshotRefusesRootMismatch(t *testing.T) {
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
	if err := exB.ImportStateSnapshot(snap); !errors.Is(err, state.ErrSnapshotRootMismatch) {
		t.Fatalf("import of tampered leaves = %v, want ErrSnapshotRootMismatch", err)
	}
	if head, _ := exB.LatestBlock(); head != nil {
		t.Fatal("a refused import stored the head block")
	}
	if _, ok := sdbB.AppliedHeight(); ok {
		t.Fatal("a refused import marked the state")
	}
}

func TestImportStateSnapshotRefusesNonEmptyChain(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	if _, err := exB.ProduceBlock(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatal(err)
	}
	if err := exB.ImportStateSnapshot(snap); err == nil {
		t.Fatal("import over an existing chain accepted")
	}
	if h, _ := exB.LatestHeight(); h != 1 {
		t.Fatalf("head changed to %d", h)
	}
}

func TestImportStateSnapshotRefusesNonEmptyState(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	snap, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, testAddr(0x42), 1)
	if err := exB.ImportStateSnapshot(snap); !errors.Is(err, state.ErrStateNotFresh) {
		t.Fatalf("import over committed state = %v, want ErrStateNotFresh", err)
	}
	if head, _ := exB.LatestBlock(); head != nil {
		t.Fatal("a refused import stored the head block")
	}
}

func TestImportStateSnapshotRefusesMalformedSnapshot(t *testing.T) {
	exA, _, _, _, _, _ := threeBlockChain(t)
	good, err := exA.ExportStateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(s *types.StateSnapshot){
		"no block":         func(s *types.StateSnapshot) { s.Block = nil },
		"height mismatch":  func(s *types.StateSnapshot) { s.Height = 2 },
		"bad block hash":   func(s *types.StateSnapshot) { s.Block.Hash[0] ^= 0xff },
		"wrong chain id":   func(s *types.StateSnapshot) { s.Block.Header.ChainID = "evil-chain" },
		"unsigned block":   func(s *types.StateSnapshot) { s.Block.ValidatorSignature = nil },
		"no leaves at all": func(s *types.StateSnapshot) { s.Leaves = nil },
	} {
		t.Run(name, func(t *testing.T) {
			snap := *good
			blk := *good.Block
			snap.Block = &blk
			mutate(&snap)
			exB, sdbB := newTestExecutor(t)
			defer exB.Close()
			defer sdbB.Close()
			if err := exB.ImportStateSnapshot(&snap); err == nil {
				t.Fatalf("%s: accepted", name)
			}
			if head, _ := exB.LatestBlock(); head != nil {
				t.Fatalf("%s: stored the head block", name)
			}
		})
	}
}
