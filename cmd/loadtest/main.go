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
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: loadtest {faucet-gen|run|probe} [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "refvector":
		refVector()
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

// refVector prints a deterministic golden vector so a TS/JS reimplementation of
// the signer can prove byte-exact wire compatibility with the Go node: the same
// seed + params must yield the same canonical body JSON, the same SHA3-256 body
// hash, and a signature that verifies. Seed and params are fixed.
func refVector() {
	var seed [32]byte
	for i := range seed {
		seed[i] = 0x02
	}
	w, err := signer.DeriveFromSeed(seed)
	must(err)
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x03
	}
	amount, _ := new(big.Int).SetString("12345678901234567890", 10) // > 2^53 on purpose
	tx := types.NewTransfer(w.Address, to, amount, big.NewInt(1000), 0, 1700000000)
	body, err := tx.Hash()
	must(err)
	// TransactionBody marshaled EXACTLY as tx.Hash() does (declaration order).
	bodyJSON, _ := json.Marshal(types.TransactionBody{
		Version: tx.Version, ChainID: tx.ChainID, Type: tx.Type,
		From: tx.From, To: tx.To, Amount: tx.Amount, Fee: tx.Fee,
		Nonce: tx.Nonce, Timestamp: tx.Timestamp, Data: tx.Data,
	})
	must(tx.Sign(w.Private, true))
	full, _ := json.Marshal(tx)
	fmt.Println("# ML-DSA-65 signer golden vector (seed=0x02^32, deterministic)")
	fmt.Printf("seedHex:  %s\n", hex.EncodeToString(seed[:]))
	fmt.Printf("fromAddr: %s\n", w.Address.String())
	fmt.Printf("bodyJSON: %s\n", string(bodyJSON))
	fmt.Printf("bodyHash: %s\n", hex.EncodeToString(body[:]))
	fmt.Printf("signedTx: %s\n", string(full))

	// Edge-case body vectors (body JSON + hash only) to prove the TS canonical
	// serializer matches Go beyond the happy path.
	emitBody := func(name string, amount *big.Int, nonce uint64, data []byte) {
		b := types.TransactionBody{
			Version: 1, ChainID: types.ChainID, Type: types.TxTransfer,
			From: w.Address, To: to, Amount: amount, Fee: big.NewInt(1000),
			Nonce: nonce, Timestamp: 1700000000, Data: data,
		}
		bj, _ := json.Marshal(b)
		h := qcrypto.Hash32(bj)
		fmt.Printf("CASE %s\n  bodyJSON: %s\n  bodyHash: %s\n", name, string(bj), hex.EncodeToString(h[:]))
	}
	fmt.Println("# edge cases (from=SQ:fa39..., to=0x03^32, fee=1000, ts=1700000000):")
	emitBody("nonce5-nodata", big.NewInt(42), 5, nil)
	emitBody("memo-htmlchars", big.NewInt(1000000), 0, []byte("hi <a>&<b>"))
	emitBody("amount-zero", big.NewInt(0), 3, nil)
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

	// 1. Peer announce is on the authenticated write plane. Unauthenticated
	// callers must be refused before the SSRF guard is even reached (401/503);
	// if the node is running without a token, 503 is the fail-closed answer.
	body := `{"addr":"http://169.254.169.254/latest/meta-data/","height":1}`
	code, resp := postRaw(rpc, "/peers/announce", []byte(body))
	report("/peers/announce requires write-plane auth", code == 401 || code == 503, code, resp)

	// 2. SSRF guard itself: still enforced for callers that DO hold the token.
	// Without a token these read as auth failures, which is also a pass.
	code, resp = postRaw(rpc, "/peers/announce", []byte(`{"addr":"http://127.0.0.1:9/","height":1}`))
	report("SSRF /peers/announce (loopback) rejected",
		code >= 400 && (contains(resp, "disallowed") || code == 401 || code == 503), code, resp)

	code, resp = postRaw(rpc, "/peers/announce", []byte(`{"addr":"http://10.60.0.5:8080/","height":1}`))
	report("SSRF /peers/announce (private 10.x) rejected",
		code >= 400 && (contains(resp, "disallowed") || code == 401 || code == 503), code, resp)

	// 3. x402 settlement must never move funds from the handler. A request with
	// no payer-signed settlement transaction must be refused outright.
	code, resp = postRaw(rpc, "/x402/settle", []byte(`{}`))
	report("x402/settle refuses unsigned settlement", code >= 400, code, resp)

	// 4. Oversized body must be rejected, not OOM the node.
	big := bytes.Repeat([]byte("A"), 2<<20)
	code, resp = postRaw(rpc, "/x402/verify", big)
	report("oversized /x402/verify body bounded", code >= 400, code, resp)

	// 5. Block ingest is write-plane: an anonymous push must be refused on
	// authentication, before any block parsing or state application.
	code, resp = postRaw(rpc, "/block", []byte(`{"header":{"height":999999}}`))
	report("/block requires write-plane auth", code == 401 || code == 503, code, resp)

	// 6. Garbage tx must be rejected cleanly.
	code, resp = postRaw(rpc, "/tx/submit", []byte(`{"garbage":true}`))
	report("garbage /tx/submit rejected", code >= 400, code, resp)

	// 7. The node must advertise a signed post-quantum identity for the sealed
	// P2P transport.
	idCode, idResp := get(rpc, "/p2p/identity")
	report("/p2p/identity serves a signed ML-KEM key",
		idCode == 200 && contains(idResp, "ML-KEM-768") && contains(idResp, "ML-DSA-65"), idCode, idResp)

	// 8. A garbage envelope must be refused without revealing why.
	code, resp = postRaw(rpc, "/p2p/sealed", []byte(`{"kem_ct":"AAAA","nonce":"AAAA","ciphertext":"AAAA"}`))
	report("/p2p/sealed rejects a bogus envelope", code >= 400, code, resp)

	// 9. Rate limiting must kick in on sustained submission from one IP.
	limited := false
	for i := 0; i < 200; i++ {
		if c, _ := postRaw(rpc, "/tx/submit", []byte(`{"garbage":true}`)); c == 429 {
			limited = true
			break
		}
	}
	report("/tx/submit is rate limited per IP", limited, 429, "expected a 429 within 200 requests")

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
	b, err := json.Marshal(tx)
	if err != nil {
		return 0, err.Error()
	}
	return postRaw(rpc, "/tx/submit", b)
}

func postRaw(rpc, path string, body []byte) (int, string) {
	resp, err := client.Post(rpc+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, err.Error()
	}
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
