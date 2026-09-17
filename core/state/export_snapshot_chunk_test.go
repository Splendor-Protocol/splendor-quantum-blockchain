package state

import (
	"bytes"
	"errors"
	"math/big"
	"sort"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// collectAllChunks walks ExportSnapshotChunk to exhaustion and returns every
// leaf it served plus the height it reported (which must never change across
// chunks of the same static state).
func collectAllChunks(t *testing.T, sdb *StateDB, chunkSize int) (uint64, []types.StateLeaf) {
	t.Helper()
	var (
		all       []types.StateLeaf
		afterKey  []byte
		height    uint64
		seenFirst bool
	)
	for i := 0; i < 100000; i++ { // hard stop against an infinite loop bug
		h, leaves, next, done, err := sdb.ExportSnapshotChunk(afterKey, chunkSize)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if !seenFirst {
			height = h
			seenFirst = true
		} else if h != height {
			t.Fatalf("chunk %d reported height %d, earlier chunks said %d", i, h, height)
		}
		all = append(all, leaves...)
		if done {
			if next != nil {
				t.Fatalf("chunk %d: done=true but nextKey is non-nil", i)
			}
			return height, all
		}
		if next == nil {
			t.Fatalf("chunk %d: done=false but nextKey is nil", i)
		}
		afterKey = next
	}
	t.Fatal("chunk walk did not terminate")
	return 0, nil
}

// Chunked export must reassemble to exactly the same leaf set — same keys,
// same values, same count — as the single-shot export, whatever the chunk
// size, including a chunk size of 1 (the most paging calls possible) and a
// chunk size bigger than the whole state (one call, same as ExportSnapshot).
func TestExportSnapshotChunkReassemblesToWholeState(t *testing.T) {
	sdb, _ := openState(t)
	for i := 0; i < 250; i++ {
		acc := types.NewAccount(addr(byte(i % 256)))
		acc.AddBalance(big.NewInt(int64(i + 1)))
		sdb.SetAccount(acc)
	}
	wantRoot, err := sdb.CommitWithHeight(9)
	if err != nil {
		t.Fatal(err)
	}
	wantHeight, wantLeaves, err := sdb.ExportSnapshot(64 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if SnapshotRoot(wantLeaves) != wantRoot {
		t.Fatal("test setup: single-shot export does not match committed root")
	}

	for _, chunkSize := range []int{1, 7, 64, 10000} {
		t.Run(sprintChunk(chunkSize), func(t *testing.T) {
			gotHeight, gotLeaves := collectAllChunks(t, sdb, chunkSize)
			if gotHeight != wantHeight {
				t.Fatalf("chunked height = %d, want %d", gotHeight, wantHeight)
			}
			if len(gotLeaves) != len(wantLeaves) {
				t.Fatalf("chunked leaf count = %d, want %d", len(gotLeaves), len(wantLeaves))
			}
			if SnapshotRoot(gotLeaves) != wantRoot {
				t.Fatalf("chunked leaves hash to a different root than the single-shot export")
			}
			sortLeaves(gotLeaves)
			sortLeaves(wantLeaves)
			for i := range gotLeaves {
				if !bytes.Equal(gotLeaves[i].Key, wantLeaves[i].Key) || !bytes.Equal(gotLeaves[i].Value, wantLeaves[i].Value) {
					t.Fatalf("leaf %d differs: got %q=%q, want %q=%q",
						i, gotLeaves[i].Key, gotLeaves[i].Value, wantLeaves[i].Key, wantLeaves[i].Value)
				}
			}
		})
	}
}

// A chunk never crosses back into meta: keys, the same rule ExportSnapshot
// enforces, so the resume cursor cannot accidentally leak node-local
// bookkeeping into a snapshot transfer.
func TestExportSnapshotChunkExcludesMetaKeys(t *testing.T) {
	sdb, _ := openState(t)
	acc := types.NewAccount(addr(0x01))
	acc.AddBalance(big.NewInt(1))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}
	_, leaves := collectAllChunks(t, sdb, 1)
	for _, l := range leaves {
		if bytes.HasPrefix(l.Key, []byte(prefixMeta)) {
			t.Fatalf("chunk export leaked a meta: key: %q", l.Key)
		}
	}
}

// No height marker means the same refusal as the single-shot export.
func TestExportSnapshotChunkRefusesWithoutHeightMarker(t *testing.T) {
	sdb, _ := openState(t)
	acc := types.NewAccount(addr(0x11))
	acc.AddBalance(big.NewInt(1))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := sdb.ExportSnapshotChunk(nil, 10); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("chunk export without a height marker = %v, want ErrSnapshotUnavailable", err)
	}
}

// A non-positive chunk size is a caller bug, not a valid "give me nothing".
func TestExportSnapshotChunkRejectsBadSize(t *testing.T) {
	sdb, _ := openState(t)
	if _, err := sdb.CommitWithHeight(0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := sdb.ExportSnapshotChunk(nil, 0); err == nil {
		t.Fatal("chunk size 0 accepted")
	}
	if _, _, _, _, err := sdb.ExportSnapshotChunk(nil, -1); err == nil {
		t.Fatal("negative chunk size accepted")
	}
}

// An empty state (committed with no leaves) reports done on the very first
// call with zero leaves, not an error and not an infinite loop.
func TestExportSnapshotChunkEmptyState(t *testing.T) {
	sdb, _ := openState(t)
	if _, err := sdb.CommitWithHeight(0); err != nil {
		t.Fatal(err)
	}
	height, leaves, next, done, err := sdb.ExportSnapshotChunk(nil, 10)
	if err != nil {
		t.Fatalf("chunk export of empty state: %v", err)
	}
	if !done || next != nil || len(leaves) != 0 || height != 0 {
		t.Fatalf("empty state chunk = height=%d leaves=%d next=%v done=%v, want 0,0,nil,true",
			height, len(leaves), next, done)
	}
}

func sortLeaves(leaves []types.StateLeaf) {
	sort.Slice(leaves, func(i, j int) bool { return bytes.Compare(leaves[i].Key, leaves[j].Key) < 0 })
}

func sprintChunk(n int) string {
	switch n {
	case 1:
		return "chunk=1"
	case 7:
		return "chunk=7"
	case 64:
		return "chunk=64"
	default:
		return "chunk=big"
	}
}

// addrI returns a distinct address for a counter, so a test can build more
// than the 256 leaves addr(byte) can express.
func addrI(i int) qcrypto.Address {
	var a qcrypto.Address
	for j := range a {
		a[j] = byte(i >> (8 * j))
	}
	return a
}

// The executor clamps an oversized maxLeaves, and the state layer must too,
// so no caller (client bug or abuse) can scan and serialize more than one
// chunk of state in a single export.
func TestExportSnapshotChunkClampsOversizedRequest(t *testing.T) {
	sdb, _ := openState(t)
	total := MaxChunkLeaves + 50
	for i := 0; i < total; i++ {
		acc := types.NewAccount(addrI(i))
		acc.AddBalance(big.NewInt(1))
		sdb.SetAccount(acc)
	}
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}

	// Ask for far more than one chunk; the server must serve at most one.
	_, leaves, nextKey, done, err := sdb.ExportSnapshotChunk(nil, total*10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != MaxChunkLeaves {
		t.Fatalf("oversized request returned %d leaves, want exactly %d", len(leaves), MaxChunkLeaves)
	}
	if done {
		t.Fatal("oversized request reported done even though leaves remain")
	}
	if len(nextKey) == 0 {
		t.Fatal("oversized request returned no resume cursor")
	}

	// The remainder must be retrievable with a follow-up, proving nothing was
	// skipped or duplicated by the clamp.
	_, rest, _, done2, err := sdb.ExportSnapshotChunk(nextKey, total*10)
	if err != nil {
		t.Fatal(err)
	}
	if !done2 {
		t.Fatal("second chunk should have drained the remaining leaves")
	}
	if len(rest) != total-MaxChunkLeaves {
		t.Fatalf("second chunk returned %d leaves, want %d", len(rest), total-MaxChunkLeaves)
	}
}

// The raw-byte budget is a hard maximum, not a stopping threshold: a page must
// never exceed it, and a leaf that would push a non-empty page over is deferred
// to the next page rather than copied and then left oversized. Walk the whole
// three-leaf state and confirm every leaf is served exactly once — no loss, no
// duplication — and no single page ever crosses the cap.
func TestExportSnapshotChunkStopsAtByteBudget(t *testing.T) {
	sdb, _ := openState(t)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}
	// Each leaf is 5-byte key + MaxChunkBytes/2 value: two leaves exceed the
	// budget, so a strict budget must serve them one per page.
	big := bytes.Repeat([]byte{0xAB}, MaxChunkBytes/2)
	for _, key := range []string{"acc:a", "acc:b", "acc:c"} {
		if err := sdb.db.Put([]byte(key), big, nil); err != nil {
			t.Fatal(err)
		}
	}

	var (
		got   []string
		after []byte
	)
	for i := 0; i < 10; i++ { // hard stop against a cursor bug
		_, leaves, next, done, err := sdb.ExportSnapshotChunk(after, MaxChunkLeaves)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, l := range leaves {
			total += len(l.Key) + len(l.Value)
			got = append(got, string(l.Key))
		}
		if total > MaxChunkBytes {
			t.Fatalf("chunk %d carried %d raw bytes, exceeding the %d-byte budget", i, total, MaxChunkBytes)
		}
		if done {
			break
		}
		if len(next) == 0 {
			t.Fatalf("chunk %d: not done but no resume cursor", i)
		}
		after = next
	}
	if len(got) != 3 || got[0] != "acc:a" || got[1] != "acc:b" || got[2] != "acc:c" {
		t.Fatalf("served leaves = %v, want [acc:a acc:b acc:c] exactly once each", got)
	}
}

// A single leaf larger than the whole budget cannot be paginated without a
// fragmentation protocol that does not exist, so it must be refused explicitly
// rather than silently emitted over the cap.
func TestExportSnapshotChunkRejectsOversizedSingleLeaf(t *testing.T) {
	sdb, _ := openState(t)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0x01}, MaxChunkBytes+1)
	if err := sdb.db.Put([]byte("acc:big"), big, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := sdb.ExportSnapshotChunk(nil, MaxChunkLeaves); err == nil {
		t.Fatal("a single leaf exceeding the byte budget was served without error")
	}
}

// A pinned session serves every page from the SAME LevelDB snapshot: a commit
// that lands mid-transfer changes a fresh export's height, but the session
// keeps returning the state it was begun at, so a multi-page transfer never
// spans two different states.
func TestSnapshotSessionIsImmutableAcrossCommits(t *testing.T) {
	sdb, _ := openState(t)
	for i := 0; i < 5; i++ {
		acc := types.NewAccount(addr(byte(i)))
		acc.AddBalance(big.NewInt(1))
		sdb.SetAccount(acc)
	}
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}
	root1, err := sdb.StateRoot()
	if err != nil {
		t.Fatal(err)
	}

	sess, err := sdb.BeginSnapshotChunkSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Release()
	if sess.Height() != 1 {
		t.Fatalf("session height = %d, want 1", sess.Height())
	}

	// The chain advances while the session is pinned.
	acc := types.NewAccount(addr(0xAA))
	acc.AddBalance(big.NewInt(9))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(2); err != nil {
		t.Fatal(err)
	}

	// A fresh export now sees height 2; the pinned session still sees 1.
	if h, _, err := sdb.ExportSnapshot(64 << 20); err != nil || h != 2 {
		t.Fatalf("fresh export height = (%d, %v), want 2", h, err)
	}

	var (
		all   []types.StateLeaf
		after []byte
	)
	for i := 0; i < 100; i++ {
		leaves, next, done, err := sess.Page(after, 2)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, leaves...)
		if done {
			break
		}
		if len(next) == 0 {
			t.Fatalf("page %d: not done but no cursor", i)
		}
		after = next
	}

	// The pinned session must still hash to the height-1 root, and only that.
	if got := SnapshotRoot(all); got != root1 {
		t.Fatalf("pinned session leaves hash to %x, want the height-1 root %x", got[:8], root1[:8])
	}
	if len(all) != 5 {
		t.Fatalf("pinned session yielded %d leaves, want the 5 committed at height 1", len(all))
	}
}

func TestSnapshotSessionPageDefaultsZeroLeaves(t *testing.T) {
	sdb, _ := openState(t)
	acc := types.NewAccount(addr(0x11))
	acc.AddBalance(big.NewInt(1))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}
	sess, err := sdb.BeginSnapshotChunkSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Release()
	leaves, _, done, err := sess.Page(nil, 0)
	if err != nil {
		t.Fatalf("Page(0) must default, got %v", err)
	}
	if len(leaves) == 0 || !done {
		t.Fatalf("Page(0) leaves=%d done=%v", len(leaves), done)
	}
}
