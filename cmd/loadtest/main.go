// loadtest is a throughput + security probe harness for a Splendor Quantum Chain
// RPC endpoint. It signs REAL ML-DSA-65 transactions using the chain's own crypto
// so the node exercises the full verify+execute+commit path under load.
//
//	loadtest faucet-gen                       # make a faucet key; prints SQ address
//	loadtest run   --rpc URL --faucet f.key --senders 200 --burst 5000 --rounds 6
//	loadtest probe --rpc URL                  # adversarial security probes
//
// The faucet address must be funded at genesis (SQC_GENESIS_ALLOC_ADDRESS).
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: loadtest {faucet-gen|run|probe} [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "faucet-gen":
		faucetGen()
	case "run":
		run(parseFlags(os.Args[2:]))
	case "probe":
		probe(parseFlags(os.Args[2:]))
	default:
		fmt.Println("unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func parseFlags(args []string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		m[args[i]] = args[i+1]
	}
	return m
}

// ── faucet ──────────────────────────────────────────────────────────────────

func faucetGen() {
	pub, priv, err := qcrypto.GenerateKeypair()
	must(err)
	addr := qcrypto.DeriveAddress(pub)
	f := "faucet.key"
	must(os.WriteFile(f, []byte(hex.EncodeToString(priv.Bytes())), 0600))
	fmt.Printf("faucet address: %s\n", addr.String())
	fmt.Printf("faucet key saved: %s (KEEP LOCAL — never commit)\n", f)
	fmt.Printf("set SQC_GENESIS_ALLOC_ADDRESS=%s on the reset devnet to fund it\n", addr.String())
}

func loadKey(path string) (*qcrypto.PrivateKey, qcrypto.Address) {
	b, err := os.ReadFile(path)
	must(err)
	raw, err := hex.DecodeString(string(bytes.TrimSpace(b)))
	must(err)
	priv, err := qcrypto.PrivateKeyFromBytes(raw)
	must(err)
	return priv, qcrypto.DeriveAddress(priv.Public())
}

// ── load run ────────────────────────────────────────────────────────────────

type acct struct {
	priv  *qcrypto.PrivateKey
	addr  qcrypto.Address
	nonce uint64
}

func run(f map[string]string) {
	rpc := def(f["--rpc"], "http://127.0.0.1:8080")
	faucetPath := def(f["--faucet"], "faucet.key")
	senders := atoiDef(f["--senders"], 200)
	burst := atoiDef(f["--burst"], 5000)
	rounds := atoiDef(f["--rounds"], 6)

	fpriv, faddr := loadKey(faucetPath)
	fmt.Printf("rpc=%s faucet=%s senders=%d burst=%d rounds=%d\n", rpc, faddr.String()[:14], senders, burst, rounds)

	bal := getBalance(rpc, faddr)
	fmt.Printf("faucet balance: %s\n", bal.String())
	if bal.Sign() == 0 {
		fmt.Println("FATAL: faucet unfunded — set SQC_GENESIS_ALLOC_ADDRESS to the faucet address and reset")
		os.Exit(1)
	}

	// Generate senders and fund each from the faucet (one tx per sender).
	accts := make([]*acct, senders)
	fn := getNonce(rpc, faddr)
	perSender := big.NewInt(1_000_000_000_000) // plenty for many small txs + fees
	fmt.Printf("funding %d senders...\n", senders)
	for i := 0; i < senders; i++ {
		pub, priv, err := qcrypto.GenerateKeypair()
		must(err)
		a := &acct{priv: priv, addr: qcrypto.DeriveAddress(pub)}
		accts[i] = a
		tx := types.NewTransfer(faddr, a.addr, perSender, big.NewInt(1000), fn, time.Now().Unix())
		must(tx.Sign(fpriv, fn == 0))
		if code, _ := submit(rpc, tx); code != 200 {
			// mempool per-addr cap is 64: fund in waves, letting blocks drain it.
			for waited := 0; waited < 40; waited++ {
				time.Sleep(250 * time.Millisecond)
				if c2, _ := submit(rpc, tx); c2 == 200 {
					break
				}
			}
		}
		fn++
	}
	// Wait for funding to be mined (senders show a balance).
	fmt.Print("waiting for funding to confirm")
	for w := 0; w < 60; w++ {
		if getBalance(rpc, accts[senders-1].addr).Sign() > 0 && getBalance(rpc, accts[0].addr).Sign() > 0 {
			break
		}
		fmt.Print(".")
		time.Sleep(time.Second)
	}
	fmt.Println(" ok")
	for _, a := range accts {
		a.nonce = getNonce(rpc, a.addr)
	}

	// Ramp: each round fires `burst` txs spread across all senders, as fast as
	// possible, and measures accept rate + height/mempool movement.
	for r := 1; r <= rounds; r++ {
		target := burst * r
		h0, _, _ := status(rpc)
		t0 := time.Now()
		var ok, rej int64
		var wg sync.WaitGroup
		sem := make(chan struct{}, 128) // client-side concurrency cap
		for n := 0; n < target; n++ {
			a := accts[n%len(accts)]
			// each sender's nonce advances; guard with a per-acct lock via index stride
			wg.Add(1)
			sem <- struct{}{}
			go func(a *acct, idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				nonce := atomic.AddUint64(&a.nonce, 1) - 1
				to := accts[(idx+1)%len(accts)].addr
				tx := types.NewTransfer(a.addr, to, big.NewInt(1), big.NewInt(1000), nonce, time.Now().Unix())
				if tx.Sign(a.priv, nonce == 0) != nil {
					atomic.AddInt64(&rej, 1)
					return
				}
				if code, _ := submit(rpc, tx); code == 200 {
					atomic.AddInt64(&ok, 1)
				} else {
					atomic.AddInt64(&rej, 1)
				}
			}(a, n)
		}
		wg.Wait()
		submitDur := time.Since(t0)
		time.Sleep(4 * time.Second) // let the chain digest
		h1, mp, _ := status(rpc)
		fmt.Printf("round %d: target=%d accepted=%d rejected=%d submit=%.2fs submit_rate=%.0f tx/s | height %d->%d (+%d) mempool=%d\n",
			r, target, ok, rej, submitDur.Seconds(), float64(ok)/submitDur.Seconds(), h0, h1, h1-h0, mp)
	}
	fmt.Println("done. re-check /status; a healthy node keeps advancing height and drains mempool.")
}

// ── security probes ─────────────────────────────────────────────────────────

func probe(f map[string]string) {
	rpc := def(f["--rpc"], "http://127.0.0.1:8080")
	fmt.Printf("=== security probes vs %s ===\n", rpc)

	// 1. SSRF: announce a metadata-service peer — must be rejected by IsSafePeerURL.
	body := `{"addr":"http://169.254.169.254/latest/meta-data/","height":1}`
	code, resp := postRaw(rpc, "/peers/announce", []byte(body))
	report("SSRF /peers/announce (metadata IP)", code >= 400 && contains(resp, "disallowed"), code, resp)

	// 2. SSRF: loopback peer — must be rejected (public devnet).
	code, resp = postRaw(rpc, "/peers/announce", []byte(`{"addr":"http://127.0.0.1:9/","height":1}`))
	report("SSRF /peers/announce (loopback)", code >= 400 && contains(resp, "disallowed"), code, resp)

	// 2b. SSRF: internal AWS subnet — must be rejected.
	code, resp = postRaw(rpc, "/peers/announce", []byte(`{"addr":"http://10.60.0.5:8080/","height":1}`))
	report("SSRF /peers/announce (private 10.x)", code >= 400 && contains(resp, "disallowed"), code, resp)

	// 3. x402/settle must be disabled by default (503) or auth-gated (401).
	code, resp = postRaw(rpc, "/x402/settle", []byte(`{}`))
	report("x402/settle gated (503/401)", code == 503 || code == 401 || code == 404, code, resp)

	// 4. Oversized body must be rejected, not OOM the node.
	big := bytes.Repeat([]byte("A"), 2<<20)
	code, resp = postRaw(rpc, "/x402/verify", big)
	report("oversized /x402/verify body bounded", code >= 400, code, resp)

	// 5. Malformed block push must be rejected (fail-closed), not panic.
	code, resp = postRaw(rpc, "/block", []byte(`{"header":{"height":999999}}`))
	report("malformed /block push rejected", code >= 400, code, resp)

	// 6. Garbage tx must be rejected cleanly.
	code, resp = postRaw(rpc, "/tx/submit", []byte(`{"garbage":true}`))
	report("garbage /tx/submit rejected", code >= 400, code, resp)

	fmt.Println("=== probes done (node should still be serving /status) ===")
	if h, mp, ok := status(rpc); ok {
		fmt.Printf("post-probe /status: height=%d mempool=%d (node alive)\n", h, mp)
	} else {
		fmt.Println("WARNING: /status not responding after probes")
	}
}

func report(name string, pass bool, code int, resp string) {
	tag := "PASS"
	if !pass {
		tag = "FAIL"
	}
	if len(resp) > 80 {
		resp = resp[:80]
	}
	fmt.Printf("  [%s] %-42s http=%d %s\n", tag, name, code, resp)
}

// ── rpc helpers ─────────────────────────────────────────────────────────────

var client = &http.Client{Timeout: 15 * time.Second}

func submit(rpc string, tx *types.Transaction) (int, string) {
	b, _ := json.Marshal(tx)
	return postRaw(rpc, "/tx/submit", b)
}

func postRaw(rpc, path string, body []byte) (int, string) {
	resp, err := client.Post(rpc+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(bytes.TrimSpace(rb))
}

func getBalance(rpc string, a qcrypto.Address) *big.Int {
	_, s := get(rpc, "/balance/"+a.String())
	var r struct {
		Balance string `json:"balance"`
	}
	if json.Unmarshal([]byte(s), &r) == nil {
		if v, ok := new(big.Int).SetString(r.Balance, 10); ok {
			return v
		}
	}
	return big.NewInt(0)
}

func getNonce(rpc string, a qcrypto.Address) uint64 {
	_, s := get(rpc, "/nonce/"+a.String())
	var r struct {
		Nonce uint64 `json:"nonce"`
	}
	json.Unmarshal([]byte(s), &r)
	return r.Nonce
}

func status(rpc string) (uint64, int, bool) {
	code, s := get(rpc, "/status")
	if code != 200 {
		return 0, 0, false
	}
	var r struct {
		Height      uint64 `json:"height"`
		MempoolSize int    `json:"mempool_size"`
	}
	if json.Unmarshal([]byte(s), &r) != nil {
		return 0, 0, false
	}
	return r.Height, r.MempoolSize, true
}

func get(rpc, path string) (int, string) {
	resp, err := client.Get(rpc + path)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(rb)
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func atoiDef(v string, d int) int {
	if v == "" {
		return d
	}
	n := 0
	fmt.Sscanf(v, "%d", &n)
	if n == 0 {
		return d
	}
	return n
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
