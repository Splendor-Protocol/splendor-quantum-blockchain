package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
	"golang.org/x/crypto/sha3"
)

const testChainID = "sqc-txload-test-1"

func testFunder(t *testing.T) (*account, string) {
	t.Helper()
	seedHex := strings.Repeat("ab", 32)
	seed, err := signer.SeedFromHex(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	return &account{idx: -1, name: "funder", addr: w.Address, priv: w.Private}, seedHex
}

// A lazy sender must be the same account as an eager one — same seed formula,
// same address — and the key it derives on demand must sign for that address.
func TestLazySenderMatchesEager(t *testing.T) {
	f, _ := testFunder(t)
	for _, i := range []int{0, 7, 100000} {
		eager, err := deriveSender(f.addr, i, false)
		if err != nil {
			t.Fatal(err)
		}
		lazy, err := deriveSender(f.addr, i, true)
		if err != nil {
			t.Fatal(err)
		}
		if eager.addr != lazy.addr {
			t.Fatalf("sender %d: eager %s != lazy %s", i, eager.addr, lazy.addr)
		}
		if lazy.priv != nil || eager.priv == nil {
			t.Fatalf("sender %d: lazy must hold no key, eager must", i)
		}
		want := sha3.Sum256([]byte("sqc-txload/v1/" + f.addr.String() + "/" + fmt.Sprint(i)))
		if lazy.seed != want {
			t.Fatalf("sender %d: seed is not SHA3-256 of the documented tag", i)
		}
		priv, err := lazy.signingKey()
		if err != nil {
			t.Fatal(err)
		}
		if qcrypto.DeriveAddress(priv.Public()) != lazy.addr {
			t.Fatalf("sender %d: derived key does not belong to the sender address", i)
		}
	}
}

func TestParseConfigScanNodeDisabled(t *testing.T) {
	c, _ := parseConfig("test", []string{"--rpc", "http://a", "--chain-id", testChainID, "--seed", "x", "--scan-node", "-1", "--fund-deadline", "45s", "--one-shot", "--lazy-keys"})
	if c.scanNode != -1 || c.fundDeadline != 45*time.Second || !c.oneShot || !c.lazyKeys {
		t.Fatalf("flags not parsed: %+v", c)
	}
}

// fakeNode is the slice of the RPC txload talks to: /status, /balance, /nonce
// and /tx/submit. Every submission is verified with the embedded public key,
// so a lazily derived key that signed for the wrong address would show up.
type fakeNode struct {
	height   atomic.Uint64
	accepted atomic.Uint64
	bad      atomic.Uint64
	senders  atomic.Int64
}

func (f *fakeNode) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"chain_id": testChainID, "height": f.height.Add(1), "mempool_size": 0, "forks": map[string]uint64{}})
	})
	mux.HandleFunc("/balance/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"balance": "1000000000000000000000000"})
	})
	mux.HandleFunc("/nonce/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]uint64{"nonce": 0})
	})
	mux.HandleFunc("/tx/submit", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var tx types.Transaction
		if err := json.Unmarshal(body, &tx); err != nil || tx.VerifySignature(tx.PublicKey) != nil ||
			qcrypto.DeriveAddress(mustPub(tx.PublicKey)) != tx.From || tx.ChainID != testChainID {
			f.bad.Add(1)
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		f.accepted.Add(1)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"hash":"%s"}`, hex.EncodeToString(tx.Signature[:16]))
	})
	return mux
}

func mustPub(b []byte) *qcrypto.PublicKey {
	p, err := qcrypto.PublicKeyFromBytes(b)
	if err != nil {
		return &qcrypto.PublicKey{}
	}
	return p
}

// One-shot with lazy keys and no block scan against the fake node: every
// sender sends exactly once, the run ends by itself, and a pool above
// maxSummarySenders keeps its address list out of summary.json.
func TestOneShotLazyRun(t *testing.T) {
	fn := &fakeNode{}
	srv := httptest.NewServer(fn.handler())
	defer srv.Close()

	_, seedHex := testFunder(t)
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "funder.seed")
	if err := os.WriteFile(seedPath, []byte(seedHex+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.json")
	stats := filepath.Join(dir, "stats.jsonl")

	n := maxSummarySenders + 6
	code := run([]string{
		"--rpc", srv.URL, "--chain-id", testChainID, "--seed", seedPath,
		"--senders", fmt.Sprint(n), "--rate", "400", "--concurrency", "8",
		"--one-shot", "--lazy-keys", "--scan-node", "-1", "--sample", "0", "--topup-every", "0",
		"--interval", "200ms", "--duration", "60s", "--fund-deadline", "10s",
		"--amount-min", "1000", "--amount-max", "1000",
		"--stats", stats, "--summary", summary,
	})
	if code != 0 {
		t.Fatalf("run exit %d", code)
	}
	if got := fn.accepted.Load(); got != uint64(n) {
		t.Fatalf("node accepted %d, want %d", got, n)
	}
	if fn.bad.Load() != 0 {
		t.Fatalf("%d submissions failed signature verification", fn.bad.Load())
	}
	raw, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["accepted"].(float64) != float64(n) || out["one_shot_remaining"].(float64) != 0 || out["one_shot_gave_up"].(float64) != 0 {
		t.Fatalf("summary: accepted=%v remaining=%v gave_up=%v", out["accepted"], out["one_shot_remaining"], out["one_shot_gave_up"])
	}
	if _, listed := out["senders"]; listed {
		t.Fatalf("summary lists %d sender addresses; pools above %d must omit them", n, maxSummarySenders)
	}
	if out["senders_count"].(float64) != float64(n) {
		t.Fatalf("senders_count=%v want %d", out["senders_count"], n)
	}
	if out["chain"].(map[string]any)["scan_node"] != "disabled" {
		t.Fatalf("scan should be disabled: %v", out["chain"])
	}
	if _, err := os.Stat(stats); err != nil {
		t.Fatalf("stats file: %v", err)
	}
}

// The default (eager, scanning node 0, no one-shot) still lists senders and
// keeps streaming until the duration elapses.
func TestDefaultRunKeepsStreaming(t *testing.T) {
	fn := &fakeNode{}
	srv := httptest.NewServer(fn.handler())
	defer srv.Close()
	_, seedHex := testFunder(t)
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "funder.seed")
	os.WriteFile(seedPath, []byte(seedHex), 0o600)
	summary := filepath.Join(dir, "summary.json")
	// A block endpoint for the scanner, all blocks empty.
	mux := fn.handler().(*http.ServeMux)
	mux.HandleFunc("/block/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"header":{"height":1,"tx_count":0},"transactions":[]}`)
	})
	code := run([]string{
		"--rpc", srv.URL, "--chain-id", testChainID, "--seed", seedPath,
		"--senders", "3", "--rate", "50", "--sample", "0", "--topup-every", "0",
		"--interval", "300ms", "--duration", "1500ms", "--summary", summary,
	})
	if code != 0 {
		t.Fatalf("run exit %d", code)
	}
	if fn.accepted.Load() < 10 {
		t.Fatalf("expected a stream of submissions, got %d", fn.accepted.Load())
	}
	raw, _ := os.ReadFile(summary)
	var out map[string]any
	json.Unmarshal(raw, &out)
	if s, ok := out["senders"].([]any); !ok || len(s) != 3 {
		t.Fatalf("small pools must list senders: %v", out["senders"])
	}
	if out["chain"].(map[string]any)["scan_node"] != srv.URL {
		t.Fatalf("scan node: %v", out["chain"])
	}
	if _, has := out["one_shot_remaining"]; has {
		t.Fatal("one-shot fields must not appear outside one-shot mode")
	}
}
