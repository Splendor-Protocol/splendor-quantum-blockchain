package main

import (
	"math/big"
	"testing"
	"time"
)

func TestParseSubmitAcceptedAdvancesNonce(t *testing.T) {
	act, hash, _ := parseSubmit(202, `{"status":"accepted","hash":"ab12cd34ef"}`)
	if act != actAdvance {
		t.Fatalf("action = %v, want actAdvance", act)
	}
	if hash != "ab12cd34ef" {
		t.Fatalf("hash = %q, want ab12cd34ef", hash)
	}
}

func TestParseSubmitNonceTooLowResyncs(t *testing.T) {
	act, _, msg := parseSubmit(400, `{"error":"rejected: nonce too low: committed nonce for SQ:829a is 12, got 10"}`)
	if act != actResync {
		t.Fatalf("action = %v, want actResync", act)
	}
	if msg == "" {
		t.Fatal("expected the node's error text to be surfaced")
	}
}

func TestParseSubmitDuplicatePendingSkipsNonce(t *testing.T) {
	act, _, _ := parseSubmit(400, `{"error":"rejected: duplicate pending nonce 12 for SQ:829a"}`)
	if act != actSkip {
		t.Fatalf("action = %v, want actSkip", act)
	}
}

func TestParseSubmitOtherRefusalsRetrySameNonce(t *testing.T) {
	cases := []struct {
		code int
		body string
	}{
		{400, `{"error":"rejected: fee too low: minimum 1000000000000000 at current pool load, got 1000"}`},
		{429, `{"error":"rate limit exceeded"}`},
		{503, `service unavailable`},
		{0, `Post "http://x/tx/submit": dial tcp: connection refused`},
	}
	for _, c := range cases {
		act, _, _ := parseSubmit(c.code, c.body)
		if act != actRetry {
			t.Errorf("code %d body %q: action = %v, want actRetry", c.code, c.body, act)
		}
	}
}

func TestRateWindowUsesElapsedTimeBeforeWindowFills(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	w := newRateWindow(60*time.Second, t0)
	for i := 1; i <= 9; i++ {
		w.add(t0.Add(time.Duration(i) * time.Second))
	}
	got := w.rate(t0.Add(10 * time.Second))
	if got < 0.89 || got > 0.91 {
		t.Fatalf("rate after 10s with 9 events = %.3f, want 0.9", got)
	}
}

func TestRateWindowDropsEventsOlderThanWindow(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	w := newRateWindow(60*time.Second, t0)
	for i := 1; i <= 9; i++ {
		w.add(t0.Add(time.Duration(i) * time.Second))
	}
	w.add(t0.Add(100 * time.Second))
	got := w.rate(t0.Add(120 * time.Second))
	want := 1.0 / 60.0
	if got < want-0.001 || got > want+0.001 {
		t.Fatalf("rate at t+120s = %.4f, want %.4f (only the t+100s event is inside the window)", got, want)
	}
}

func TestBlockTxCountReadsHeader(t *testing.T) {
	body := `{"header":{"height":3602,"timestamp":1788782643,"chain_id":"sqc-featval-1","tx_count":3},"hash":[1,2],"transactions":null}`
	n, err := blockTxCount(body)
	if err != nil {
		t.Fatalf("blockTxCount: %v", err)
	}
	if n != 3 {
		t.Fatalf("tx_count = %d, want 3", n)
	}
	if _, err := blockTxCount(`{"error":"block 99 not found"}`); err == nil {
		t.Fatal("a body without a header must be an error, not tx_count 0")
	}
}

func TestShouldResyncOnlyWhenPoolIsEmptyAndCommittedNonceIsStuck(t *testing.T) {
	// My counter is ahead of the chain, the pool holds nothing, and nothing has
	// been committed for a long time: the node dropped my pending txs (restart,
	// prune). Resync so the next tx reuses the committed nonce.
	if !shouldResync(3, 0, 120*time.Second, 90*time.Second) {
		t.Fatal("pending=3 mempool=0 stuck 120s: want resync")
	}
	// The pool still holds txs: the chain is just slow or halted. Waiting is
	// right; resyncing would only produce duplicate-nonce noise.
	if shouldResync(3, 3, 120*time.Second, 90*time.Second) {
		t.Fatal("pending=3 mempool=3: must not resync while the pool holds txs")
	}
	// Nothing pending: nothing to resync.
	if shouldResync(0, 0, 120*time.Second, 90*time.Second) {
		t.Fatal("pending=0: must not resync")
	}
	// Not stuck long enough yet.
	if shouldResync(3, 0, 10*time.Second, 90*time.Second) {
		t.Fatal("stuck only 10s: must not resync yet")
	}
}

func TestDeriveSubSeedIsDeterministicAndDistinct(t *testing.T) {
	var root [32]byte
	for i := range root {
		root[i] = byte(i)
	}
	a := deriveSubSeed(root, "load")
	b := deriveSubSeed(root, "load")
	c := deriveSubSeed(root, "other")
	if a != b {
		t.Fatal("same root and label must derive the same seed")
	}
	if a == root {
		t.Fatal("derived seed must not equal the root seed")
	}
	if a == c {
		t.Fatal("different labels must derive different seeds")
	}
}

func TestNeedsTopUpWhenBalanceBelowReserve(t *testing.T) {
	if !needsTopUp(big.NewInt(999), big.NewInt(1000)) {
		t.Fatal("balance below the reserve must request a top-up")
	}
	if needsTopUp(big.NewInt(1000), big.NewInt(1000)) {
		t.Fatal("balance at the reserve must not request a top-up")
	}
}
