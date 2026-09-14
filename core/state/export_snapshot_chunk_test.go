package state

import (
	"bytes"
	"errors"
	"math/big"
	"sort"
	"testing"

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
