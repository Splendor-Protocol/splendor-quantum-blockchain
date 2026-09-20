package block

import (
	"bytes"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"testing"
	"time"
)

// pinnedSnapshotSessions counts the sessions still holding a LevelDB snapshot.
// A pinned snapshot is what keeps compaction from reclaiming the state the
// session was begun at, so it is the resource the lifecycle has to release.
func pinnedSnapshotSessions(e *Executor) int {
	e.snapshotChunkMu.Lock()
	defer e.snapshotChunkMu.Unlock()
	n := 0
	for _, sc := range e.snapshotChunkSessions {
		sc.mu.Lock()
		if sc.session != nil {
			n++
		}
		sc.mu.Unlock()
	}
	return n
}

// shortSnapshotSessionLifecycle drives ONE executor's session lifecycle in
// milliseconds. It configures that executor rather than a package var,
// because assigning to a global races the sweeper goroutine of any executor
// an earlier test left running -- a write to a variable a live goroutine is
// reading, which is exactly what -race reported.
//
// Must be called before the first session starts: the sweeper reads the
// interval once, when it is lazily started.
func shortSnapshotSessionLifecycle(t *testing.T, e *Executor, ttl, sweep time.Duration) {
	t.Helper()
	e.snapshotSessionTTL, e.snapshotSweepInterval = ttl, sweep
}

func waitForPinned(e *Executor, want int, limit time.Duration) int {
	deadline := time.Now().Add(limit)
	for {
		got := pinnedSnapshotSessions(e)
		if got == want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestExpiredSnapshotSessionReleasesWithoutAnotherRequest is R2: expiry that
// is only evaluated while starting or paging a session leaves a quiet node
// pinning an expired snapshot indefinitely. Nothing else asks this executor
// for anything after the first page.
func TestExpiredSnapshotSessionReleasesWithoutAnotherRequest(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)
	shortSnapshotSessionLifecycle(t, ex, 40*time.Millisecond, 10*time.Millisecond)

	_, first, err := ex.StartSnapshotChunkSessionFrom("peer-a", 1)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if first.Done {
		t.Fatal("test needs a multi-page session")
	}
	if got := pinnedSnapshotSessions(ex); got != 1 {
		t.Fatalf("pinned sessions after start = %d, want 1", got)
	}

	if got := waitForPinned(ex, 0, 2*time.Second); got != 0 {
		t.Fatalf("expired session still pinned (%d) with no further request: a quiet node never releases it", got)
	}
}

// TestCompletedSessionUnpinsButKeepsFinalPageRetryable is R2: a client that
// receives Done stops asking, so holding the pinned snapshot until "the next
// request" holds it forever. The final page itself must stay retryable — a
// lost response is the whole reason the session was retained — but only that
// page, not the state view it was read from.
func TestCompletedSessionUnpinsButKeepsFinalPageRetryable(t *testing.T) {
	ex, _, _, _, _, _ := threeBlockChain(t)

	id, page, err := ex.StartSnapshotChunkSessionFrom("peer-a", 1)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if page.Done {
		t.Fatal("test needs a multi-page session")
	}
	afterKey := page.NextKey
	for i := 0; !page.Done; i++ {
		if i > 64 {
			t.Fatal("session never completed")
		}
		afterKey = page.NextKey
		page, err = ex.PageSnapshotChunkSession(id, afterKey, 1)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
	}

	if got := pinnedSnapshotSessions(ex); got != 0 {
		t.Fatalf("pinned sessions after the final page = %d, want 0: the snapshot must be released as soon as Done is produced", got)
	}

	again, err := ex.PageSnapshotChunkSession(id, afterKey, 1)
	if err != nil {
		t.Fatalf("retrying the final page must succeed for a client that lost the response: %v", err)
	}
	if !again.Done || len(again.Leaves) != len(page.Leaves) {
		t.Fatalf("final-page retry returned done=%v leaves=%d, want done=true leaves=%d",
			again.Done, len(again.Leaves), len(page.Leaves))
	}
	for i := range page.Leaves {
		if !bytes.Equal(again.Leaves[i].Key, page.Leaves[i].Key) ||
			!bytes.Equal(again.Leaves[i].Value, page.Leaves[i].Value) {
			t.Fatal("final-page retry served different leaves")
		}
	}
	if again.Height != page.Height {
		t.Fatalf("final-page retry height %d != %d: pages must never be combined across roots", again.Height, page.Height)
	}
}

// A transfer that keeps asking must not lose its session to the TTL.
//
// The expiry was fixed when the session was created, which capped a whole
// transfer at the TTL rather than capping how long an IDLE session is kept. The
// downloader's own budget is longer than the TTL, so a large transfer was
// guaranteed to lose its session part-way, reopen at a newer head, and be told
// the mismatch as "the staged checkpoint is gone" — turning a slow download into
// an abandoned one. Serving a page is proof somebody is still there, so the TTL
// now runs from the last page.
func TestAnActiveTransferKeepsItsSessionPastTheTTL(t *testing.T) {
	const ttl = 120 * time.Millisecond
	ex, _, _, _, _, _ := threeBlockChain(t)
	shortSnapshotSessionLifecycle(t, ex, ttl, 10*time.Millisecond)

	id, page, err := ex.StartSnapshotChunkSessionFrom("peer-a", 1)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if page.Done {
		t.Fatal("test needs a multi-page session")
	}

	// Page steadily, resting longer than the TTL would allow between requests:
	// the shape of a transfer that simply takes a while. Every page must be
	// served, and the whole exchange has to outlive the TTL or it proves nothing.
	started := time.Now()
	pages := 0
	for !page.Done {
		if pages > 64 {
			t.Fatal("session never completed")
		}
		time.Sleep(ttl * 3 / 4)
		page, err = ex.PageSnapshotChunkSession(id, page.NextKey, 1)
		if err != nil {
			t.Fatalf("page %d, %s into a %s TTL: %v — an active transfer lost its session",
				pages+1, time.Since(started), ttl, err)
		}
		pages++
	}
	if elapsed := time.Since(started); elapsed <= ttl {
		t.Fatalf("the transfer finished in %s, inside the %s TTL, so it never tested anything", elapsed, ttl)
	}
	if pages < 2 {
		t.Fatalf("only %d pages served", pages)
	}

	// And an idle session still expires on exactly the same clock: refreshing on
	// use must not turn the TTL into "never".
	id2, page2, err := ex.StartSnapshotChunkSessionFrom("peer-b", 1)
	if err != nil {
		t.Fatalf("start second session: %v", err)
	}
	if page2.Done {
		t.Fatal("test needs a multi-page session")
	}
	if got := waitForPinned(ex, 0, 2*time.Second); got != 0 {
		t.Fatalf("an idle session is still pinned (%d): refreshing on use must not defeat expiry", got)
	}
	if _, err := ex.PageSnapshotChunkSession(id2, page2.NextKey, 1); err == nil {
		t.Fatal("an idle session that outlived its TTL was still served")
	}
}

// The two bounds on the final-page cache have to agree with the size of a page.
//
// They did not: 16 entries against 8 MiB total, when one page may be
// state.MaxChunkBytes. Two full-size pages filled the byte budget, so a third
// client completing its transfer evicted a second client's retained page, and
// that client's retry was told to start a new transfer — which on a live chain
// means downloading the whole state again. A count bound that the byte bound
// makes unreachable is not a bound, it is a misleading number.
func TestFinalPageCacheBoundsAgreeWithThePageSize(t *testing.T) {
	if int64(maxSnapshotFinalCache)*int64(state.MaxChunkBytes) > maxSnapshotFinalCacheBytes {
		t.Fatalf("the cache admits %d pages but only %d bytes, and one page may be %d: "+
			"%d full-size pages fill the budget, so a later completion evicts an earlier "+
			"client's retained page and its retry restarts from scratch",
			maxSnapshotFinalCache, maxSnapshotFinalCacheBytes, state.MaxChunkBytes,
			maxSnapshotFinalCacheBytes/state.MaxChunkBytes)
	}
	// And the per-page guard has to be reachable, or it is decoration: a page
	// over the protocol's own chunk cap must be refused rather than retained.
	if int64(state.MaxChunkBytes) > maxSnapshotFinalCacheBytes {
		t.Fatalf("a single page (%d) exceeds the whole cache budget (%d)",
			state.MaxChunkBytes, maxSnapshotFinalCacheBytes)
	}
}
