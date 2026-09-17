package block

import (
	"errors"
	"testing"
	"time"

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

// SnapshotChunkSession must stay pinned at the height it was begun at even
// after the chain advances: every page reports the START height, and the final
// page is replayable once before the session is released.
func TestSnapshotChunkSessionIsImmutableAcrossCommits(t *testing.T) {
	ex, _, vpub, vpriv, _, _ := threeBlockChain(t)
	vaddr := qcrypto.DeriveAddress(vpub)
	headBefore, _ := ex.LatestBlock()

	id, first, err := ex.StartSnapshotChunkSession(1)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if id == "" || first.SessionID != id {
		t.Fatalf("session id %q not echoed in first page %q", id, first.SessionID)
	}
	if first.Block == nil || first.Height != headBefore.Header.Height {
		t.Fatalf("first page height %d block %v, want head %d with a block", first.Height, first.Block == nil, headBefore.Header.Height)
	}
	if first.Done {
		t.Fatalf("state produced only one page (leaves=%d); cannot exercise multi-page immutability", len(first.Leaves))
	}

	// Advance the chain while the session is open.
	if _, err := ex.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, headBefore.Hash, headBefore.Header.Height+1); err != nil {
		t.Fatalf("produce block %d: %v", headBefore.Header.Height+1, err)
	}

	collected := len(first.Leaves)
	page := first
	after := first.NextKey
	for !page.Done {
		next, err := ex.PageSnapshotChunkSession(id, after, 1)
		if err != nil {
			t.Fatalf("page after %x: %v", after, err)
		}
		if next.Height != headBefore.Header.Height {
			t.Fatalf("page height %d, want pinned session height %d", next.Height, headBefore.Header.Height)
		}
		if next.SessionID != id {
			t.Fatalf("page session id %q, want %q", next.SessionID, id)
		}
		collected += len(next.Leaves)
		page = next
		after = next.NextKey
	}
	if collected == 0 {
		t.Fatal("collected no leaves across the whole session")
	}

	// The final page is replayable once (lost-response retry), then released.
	replay, err := ex.PageSnapshotChunkSession(id, after, 1)
	if err != nil {
		t.Fatalf("final page must be replayable once: %v", err)
	}
	if !replay.Done {
		t.Fatal("replayed page must still be the final page")
	}
	if _, err := ex.PageSnapshotChunkSession(id, after, 1); err == nil {
		t.Fatal("session still resolvable after the final-page replay")
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

func TestPageSnapshotChunkSessionNormalizesZeroLeaves(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	id, first, err := ex.StartSnapshotChunkSession(1)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if first.Done {
		t.Fatal("need a multi-page session to exercise Page")
	}
	page, err := ex.PageSnapshotChunkSession(id, first.NextKey, 0)
	if err != nil {
		t.Fatalf("page with maxLeaves=0: %v", err)
	}
	if len(page.Leaves) == 0 && !page.Done {
		t.Fatal("zero maxLeaves must default like Start, not reject")
	}
}

func TestSnapshotChunkSessionQuota(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	var ids []string
	for i := 0; i < maxSnapshotChunkSessions; i++ {
		id, first, err := ex.StartSnapshotChunkSessionFrom("peer-a", 1)
		if err != nil {
			// per-source cap hits first
			if i < maxSnapshotChunkSessionsPerSource {
				t.Fatalf("session %d: %v", i, err)
			}
			break
		}
		if first.Done {
			t.Fatal("quota test needs retained multi-page sessions")
		}
		ids = append(ids, id)
	}
	if len(ids) != maxSnapshotChunkSessionsPerSource {
		t.Fatalf("kept %d sessions from one source, want per-source cap %d", len(ids), maxSnapshotChunkSessionsPerSource)
	}
	if _, _, err := ex.StartSnapshotChunkSessionFrom("peer-a", 1); err == nil {
		t.Fatal("per-source quota did not refuse")
	}
	if _, first, err := ex.StartSnapshotChunkSessionFrom("peer-b", 1); err != nil {
		t.Fatalf("a different source should still start: %v", err)
	} else if first.Done {
		t.Fatal("peer-b session already done")
	}

	exB, _, _, _, _, _ := threeBlockChain(t)
	for i := 0; i < maxSnapshotChunkSessions; i++ {
		_, first, err := exB.StartSnapshotChunkSessionFrom("", 1)
		if err != nil {
			t.Fatalf("global session %d: %v", i, err)
		}
		if first.Done {
			t.Fatal("global quota test needs retained sessions")
		}
	}
	if _, _, err := exB.StartSnapshotChunkSessionFrom("", 1); err == nil {
		t.Fatal("global session quota did not refuse")
	}
}

func TestSinglePageSnapshotSessionIsNotRetained(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	id, first, err := ex.StartSnapshotChunkSession(MaxSnapshotChunkLeaves)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Done {
		t.Fatal("expected a single-page snapshot at default page size")
	}
	ex.snapshotChunkMu.Lock()
	_, kept := ex.snapshotChunkSessions[id]
	ex.snapshotChunkMu.Unlock()
	if kept {
		t.Fatal("a completed first page must not retain a session")
	}
	if _, err := ex.PageSnapshotChunkSession(id, nil, 1); err == nil {
		t.Fatal("single-page session was still resolvable")
	}
}

func TestSnapshotChunkSessionExpiryOnLookup(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	id, first, err := ex.StartSnapshotChunkSession(1)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if first.Done {
		t.Fatal("need a retained session")
	}
	ex.snapshotChunkMu.Lock()
	ex.snapshotChunkSessions[id].expires = time.Now().Add(-time.Second)
	ex.snapshotChunkMu.Unlock()
	if _, err := ex.PageSnapshotChunkSession(id, first.NextKey, 1); err == nil {
		t.Fatal("expired session must fail closed on lookup")
	}
}

func TestSnapshotChunkSessionsReleasedOnClose(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer sdb.Close()
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	fund(t, sdb, from, 1_000_000)
	tx := signedTransfer(t, from, priv, testAddr(0x77), 100, 0)
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatal(err)
	}
	id, first, err := ex.StartSnapshotChunkSession(1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Done {
		t.Fatal("Close test needs a retained session")
	}
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.PageSnapshotChunkSession(id, first.NextKey, 1); err == nil {
		t.Fatal("session survived Close")
	}
}

func TestSnapshotChunkPageDuringCloseDoesNotPanic(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	id, first, err := ex.StartSnapshotChunkSession(1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Done {
		t.Fatal("need a retained session")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 32; i++ {
			_, _ = ex.PageSnapshotChunkSession(id, first.NextKey, 1)
		}
	}()
	time.Sleep(time.Millisecond)
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
}
