// txload is the steady-rate real-traffic driver for a Splendor Quantum Chain
// RPC endpoint — or several at once. It signs REAL ML-DSA-65 transfers with
// the chain's own signer, the way cmd/featsmoke does, from a deterministic
// pool of sender accounts funded once by a funder key, and submits them at a
// fixed rate, round-robin over senders and nodes. Every interval it records
// acceptance, submit round-trip, inclusion latency, head progress and error
// classes (JSONL), and it writes a summary at exit — so a run doubles as a
// capacity measurement.
//
//	txload run --rpc http://a:8080,http://b:8080 --chain-id sqc-featval-1 \
//	    --wallet rogue.sqwallet --pass-file pass.txt \
//	    --senders 8 --rate 1.2 --duration 1h --stats stats.jsonl --summary summary.json
//
// Funder: --seed (featsmoke keygen format, 64 hex chars) or --wallet plus
// --pass-file (sqc-wallet format). Senders are derived from the funder key —
// SHA3-256 over a tag, the funder address and the index — so a restart reuses
// the same accounts and their committed nonces; --sender-offset gives a
// concurrent run its own disjoint set.
//
// Each sender is bound to ONE node. The producers do not gossip transactions,
// so a transfer lands only when the node it was submitted to seals a block;
// binding keeps each sender's nonce sequence in a single pool and spreads
// traffic over every producer's slots.
//
// Nonce discipline: a sender's nonce advances only when the pool answers 202
// or "duplicate pending nonce" (a retry after a lost reply); "nonce too low"
// resyncs from /nonce; a per-address or pool-full refusal pauses the sender.
// A gap can never be created, so nothing is ever pinned in a pool.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: txload {run|addrs} [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(run(os.Args[2:]))
	case "addrs":
		os.Exit(addrs(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

// ── configuration ───────────────────────────────────────────────────────────

type config struct {
	rpcs         []string
	chainID      string
	seedFile     string
	walletFile   string
	passFile     string
	senders      int
	senderOffset int
	rate         float64
	duration     time.Duration
	concurrency  int
	fee          *big.Int // nil = ask the chain (min_fee_v1 aware)
	amountMin    *big.Int
	amountMax    *big.Int
	fundAmt      *big.Int
	sample       int
	interval     time.Duration
	statsPath    string
	summaryPath  string
	pidPath      string
	scanNode     int
	topupEvery   int
	verbose      bool
}

func parseConfig(name string, args []string) (config, *flag.FlagSet) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var c config
	rpc := fs.String("rpc", "http://127.0.0.1:8080", "node RPC base URL(s), comma-separated; senders are bound round-robin")
	fs.StringVar(&c.chainID, "chain-id", "", "chain id every tx is signed for (required)")
	fs.StringVar(&c.seedFile, "seed", "", "funder seed file (featsmoke keygen format)")
	fs.StringVar(&c.walletFile, "wallet", "", "funder wallet file (sqc-wallet format); needs --pass-file")
	fs.StringVar(&c.passFile, "pass-file", "", "password file for --wallet")
	fs.IntVar(&c.senders, "senders", 8, "number of sender accounts")
	fs.IntVar(&c.senderOffset, "sender-offset", 0, "first sender index (a concurrent run must use a disjoint range)")
	fs.Float64Var(&c.rate, "rate", 1.2, "target submissions per second across all senders")
	fs.DurationVar(&c.duration, "duration", 0, "how long to run; 0 = until SIGINT/SIGTERM")
	fs.IntVar(&c.concurrency, "concurrency", 32, "max in-flight submissions")
	fee := fs.String("fee", "", "fee per tx in smallest units; default = the chain's floor (min_fee_v1 aware)")
	amin := fs.String("amount-min", "1000000000000000", "smallest transfer amount (default 0.001 SPLD)")
	amax := fs.String("amount-max", "1000000000000000000", "largest transfer amount (default 1 SPLD)")
	fund := fs.String("fund", "1000000000000000000000", "funder → sender top-up amount (default 1,000 SPLD)")
	fs.IntVar(&c.sample, "sample", 10, "track inclusion latency for 1 in N accepted txs (0 = off)")
	fs.DurationVar(&c.interval, "interval", 30*time.Second, "stats interval")
	fs.StringVar(&c.statsPath, "stats", "", "append one JSON line per interval here")
	fs.StringVar(&c.summaryPath, "summary", "", "write the end-of-run summary JSON here")
	fs.StringVar(&c.pidPath, "pid", "", "write this process id here")
	fs.IntVar(&c.scanNode, "scan-node", 0, "index of the --rpc node whose blocks are scanned for inclusion counts")
	fs.IntVar(&c.topupEvery, "topup-every", 10, "re-check sender balances every N intervals and top up from the funder")
	fs.BoolVar(&c.verbose, "v", false, "log every submission")
	_ = fs.Parse(args)

	for _, u := range strings.Split(*rpc, ",") {
		if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
			c.rpcs = append(c.rpcs, u)
		}
	}
	if len(c.rpcs) == 0 {
		fatal("--rpc is required")
	}
	if c.chainID == "" {
		fatal("--chain-id is required")
	}
	if c.seedFile == "" && c.walletFile == "" {
		fatal("a funder is required: --seed or --wallet/--pass-file")
	}
	if c.walletFile != "" && c.passFile == "" {
		fatal("--wallet needs --pass-file")
	}
	if c.senders < 1 {
		fatal("--senders must be >= 1")
	}
	if c.rate <= 0 {
		fatal("--rate must be > 0")
	}
	if c.scanNode < 0 || c.scanNode >= len(c.rpcs) {
		fatal("--scan-node out of range")
	}
	if *fee != "" {
		c.fee = mustBig(*fee, "--fee")
	}
	c.amountMin = mustBig(*amin, "--amount-min")
	c.amountMax = mustBig(*amax, "--amount-max")
	if c.amountMax.Cmp(c.amountMin) < 0 {
		fatal("--amount-max is below --amount-min")
	}
	c.fundAmt = mustBig(*fund, "--fund")
	return c, fs
}

// ── accounts ────────────────────────────────────────────────────────────────

type node struct {
	url  string
	http *http.Client
}

type account struct {
	idx        int
	name       string
	addr       qcrypto.Address
	priv       *qcrypto.PrivateKey
	node       *node
	mu         sync.Mutex
	nonce      uint64
	nonceOK    bool
	pauseUntil atomic.Int64 // unix nanos
}

func (a *account) paused() bool { return time.Now().UnixNano() < a.pauseUntil.Load() }
func (a *account) pause(d time.Duration) {
	a.pauseUntil.Store(time.Now().Add(d).UnixNano())
}

func loadFunder(c config) (*account, error) {
	if c.seedFile != "" {
		raw, err := os.ReadFile(c.seedFile)
		if err != nil {
			return nil, fmt.Errorf("read seed: %w", err)
		}
		seed, err := signer.SeedFromHex(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
		w, err := signer.DeriveFromSeed(seed)
		if err != nil {
			return nil, fmt.Errorf("derive: %w", err)
		}
		return &account{idx: -1, name: "funder", addr: w.Address, priv: w.Private}, nil
	}
	pass, err := os.ReadFile(c.passFile)
	if err != nil {
		return nil, fmt.Errorf("read pass file: %w", err)
	}
	pub, priv, err := qcrypto.LoadWallet(c.walletFile, bytes.TrimRight(pass, "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("load wallet: %w", err)
	}
	return &account{idx: -1, name: "funder", addr: qcrypto.DeriveAddress(pub), priv: priv}, nil
}

// deriveSender makes sender i's key from the funder's ADDRESS (public) and a
// domain tag. The funder's secret never enters the derivation: knowing a
// sender key reveals nothing about the funder. Senders hold only what the
// funder tops them up with.
func deriveSender(funder qcrypto.Address, i int) (*account, error) {
	seed := qcrypto.Hash32([]byte("sqc-txload/v1/" + funder.String() + "/" + strconv.Itoa(i)))
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		return nil, err
	}
	return &account{idx: i, name: "s" + strconv.Itoa(i), addr: w.Address, priv: w.Private}, nil
}

// ── driver ──────────────────────────────────────────────────────────────────

type driver struct {
	cfg       config
	nodes     []*node
	funder    *account
	senders   []*account
	senderSet map[qcrypto.Address]int
	feeMu     sync.Mutex
	fee       *big.Int
	fundLow   *big.Int
	log       *os.File
	startedAt time.Time

	// counters (atomic)
	submitted, accepted, transport, rateLimited, skipped, dupPending, nonceResync uint64
	sampleTimeouts, samplesFailed, retries                                        uint64
	rejMu                                                                         sync.Mutex
	rejected                                                                      map[string]uint64
	rejSamples                                                                    []string

	// latencies (seconds)
	latMu   sync.Mutex
	rttWin  []float64
	rttAll  []float64
	inclWin []float64
	inclAll []float64

	// chain scan
	scanMu           sync.Mutex
	lastScanned      uint64
	scanStart        uint64
	blocksSeen       uint64
	blocksWithMine   uint64
	blocksWithAny    uint64
	includedMine     uint64
	includedAll      uint64
	includedMineWin  uint64
	includedAllWin   uint64
	blocksSeenWin    uint64
	blocksWithAnyWin uint64
}

type sendResult int

const (
	sendAccepted sendResult = iota
	sendAlreadyPending
	sendPaused
	sendFailed
)

var minFeeRe = regexp.MustCompile(`minimum (\d+)`)

func run(args []string) int {
	cfg, _ := parseConfig("run", args)
	if err := types.SetClientChainID(cfg.chainID); err != nil {
		fatal("chain id: %v", err)
	}
	d := &driver{cfg: cfg, rejected: map[string]uint64{}, senderSet: map[qcrypto.Address]int{}}
	for _, u := range cfg.rpcs {
		d.nodes = append(d.nodes, &node{url: u, http: &http.Client{Timeout: 20 * time.Second}})
	}
	f, err := loadFunder(cfg)
	if err != nil {
		fatal("%v", err)
	}
	f.node = d.nodes[0]
	d.funder = f
	for i := 0; i < cfg.senders; i++ {
		s, err := deriveSender(f.addr, cfg.senderOffset+i)
		if err != nil {
			fatal("derive sender %d: %v", i, err)
		}
		s.node = d.nodes[i%len(d.nodes)]
		d.senders = append(d.senders, s)
		d.senderSet[s.addr] = s.idx
	}
	d.fundLow = new(big.Int).Div(cfg.fundAmt, big.NewInt(4))
	if cfg.pidPath != "" {
		if err := os.WriteFile(cfg.pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
			fatal("pid file: %v", err)
		}
		defer os.Remove(cfg.pidPath)
	}
	if cfg.statsPath != "" {
		d.log, err = os.OpenFile(cfg.statsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fatal("stats file: %v", err)
		}
		defer d.log.Close()
	}

	// Chain sanity: every node must be on the requested chain.
	for _, n := range d.nodes {
		st, err := d.status(n)
		if err != nil {
			fatal("%s: /status: %v", n.url, err)
		}
		if st.ChainID != cfg.chainID {
			fatal("%s reports chain %q, want %q", n.url, st.ChainID, cfg.chainID)
		}
	}
	st, _ := d.status(d.nodes[cfg.scanNode])
	d.lastScanned, d.scanStart = st.Height, st.Height
	if cfg.fee != nil {
		d.fee = cfg.fee
	} else {
		d.fee = feeFor(st)
	}
	fmt.Printf("txload: chain=%s nodes=%d senders=%d (offset %d) rate=%.2f/s fee=%s amount=[%s,%s] funder=%s head=%d\n",
		cfg.chainID, len(d.nodes), len(d.senders), cfg.senderOffset, cfg.rate, d.fee, cfg.amountMin, cfg.amountMax, f.addr, st.Height)
	for i, s := range d.senders {
		if i < 8 || cfg.verbose {
			fmt.Printf("  %-5s %s → %s\n", s.name, s.addr, s.node.url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.duration)
		defer cancel()
	}

	if err := d.fund(ctx, d.senders); err != nil {
		fmt.Fprintln(os.Stderr, "funding:", err)
		return 1
	}
	d.startedAt = time.Now()
	fmt.Printf("txload: streaming at %.2f tx/s (%s per tick); stats every %s\n", cfg.rate, time.Duration(float64(time.Second)/cfg.rate), cfg.interval)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.report(ctx) }()
	d.loop(ctx)
	wg.Wait()
	d.summary()
	return 0
}

// addrs prints the sender addresses a configuration would use, so an operator
// can look them up on the explorer or pre-fund them.
func addrs(args []string) int {
	cfg, _ := parseConfig("addrs", args)
	f, err := loadFunder(cfg)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("funder %s\n", f.addr)
	for i := 0; i < cfg.senders; i++ {
		s, err := deriveSender(f.addr, cfg.senderOffset+i)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%-5s %s → %s\n", s.name, s.addr, cfg.rpcs[i%len(cfg.rpcs)])
	}
	return 0
}

// ── funding ─────────────────────────────────────────────────────────────────

func (d *driver) fund(ctx context.Context, senders []*account) error {
	var need []*account
	for _, s := range senders {
		bal, err := d.balance(s.node, s.addr)
		if err != nil {
			return fmt.Errorf("%s balance: %w", s.name, err)
		}
		if bal.Cmp(d.fundLow) < 0 {
			need = append(need, s)
		}
	}
	if len(need) == 0 {
		fmt.Printf("txload: all %d senders hold ≥ %s; no funding needed\n", len(senders), d.fundLow)
		return nil
	}
	fbal, err := d.balance(d.funder.node, d.funder.addr)
	if err != nil {
		return fmt.Errorf("funder balance: %w", err)
	}
	total := new(big.Int).Mul(d.cfg.fundAmt, big.NewInt(int64(len(need))))
	if fbal.Cmp(total) < 0 {
		return fmt.Errorf("funder %s holds %s, needs %s to fund %d senders", d.funder.addr, fbal, total, len(need))
	}
	fmt.Printf("txload: funding %d senders with %s each from %s (balance %s)\n", len(need), d.cfg.fundAmt, d.funder.addr, fbal)
	for _, s := range need {
		for attempt := 0; ; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.funder.mu.Lock()
			res := d.sendLocked(d.funder, s.addr, d.cfg.fundAmt)
			d.funder.mu.Unlock()
			if res == sendAccepted || res == sendAlreadyPending {
				break
			}
			if attempt > 60 {
				return fmt.Errorf("could not fund %s after %d attempts", s.name, attempt)
			}
			time.Sleep(2 * time.Second) // per-address cap or a full pool: let a block drain it
		}
	}
	fmt.Print("txload: waiting for funding to confirm")
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pending := 0
		for _, s := range need {
			bal, err := d.balance(s.node, s.addr)
			if err != nil || bal.Cmp(d.fundLow) < 0 {
				pending++
			}
		}
		if pending == 0 {
			fmt.Println(" ok")
			return nil
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}
	fmt.Println()
	return errors.New("funding not confirmed within 3 minutes")
}

// ── submission ──────────────────────────────────────────────────────────────

func (d *driver) currentFee() *big.Int {
	d.feeMu.Lock()
	defer d.feeMu.Unlock()
	return new(big.Int).Set(d.fee)
}

func (d *driver) raiseFee(to *big.Int) {
	d.feeMu.Lock()
	defer d.feeMu.Unlock()
	if to.Cmp(d.fee) > 0 {
		fmt.Printf("txload: fee floor moved, raising fee %s → %s\n", d.fee, to)
		d.fee = to
	}
}

// sendLocked signs and submits one transfer from a (whose mutex the caller
// holds) at its next nonce. See the package comment for the nonce rules.
func (d *driver) sendLocked(a *account, to qcrypto.Address, amount *big.Int) sendResult {
	if a.paused() {
		return sendPaused
	}
	if !a.nonceOK {
		n, err := d.nonce(a.node, a.addr)
		if err != nil {
			atomic.AddUint64(&d.transport, 1)
			return sendFailed
		}
		a.nonce, a.nonceOK = n, true
	}
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			atomic.AddUint64(&d.retries, 1)
		}
		tx := types.NewTransfer(a.addr, to, amount, d.currentFee(), a.nonce, time.Now().Unix())
		if err := tx.Sign(a.priv, true); err != nil {
			fatal("sign: %v", err)
		}
		body, _ := json.Marshal(tx)
		t0 := time.Now()
		code, resp, err := d.post(a.node, "/tx/submit", body)
		rtt := time.Since(t0)
		atomic.AddUint64(&d.submitted, 1)
		d.recordRTT(rtt)
		if d.cfg.verbose {
			fmt.Printf("  %s %s nonce=%d → %d %s (%s)\n", time.Now().UTC().Format(time.RFC3339), a.name, a.nonce, code, trim(resp, 80), rtt.Truncate(time.Millisecond))
		}
		switch {
		case err != nil:
			// Outcome unknown: the pool may hold it. Retry the same nonce; a
			// "duplicate pending" answer then confirms it went in.
			atomic.AddUint64(&d.transport, 1)
			time.Sleep(300 * time.Millisecond)
			continue
		case code == http.StatusAccepted:
			n := atomic.AddUint64(&d.accepted, 1)
			a.nonce++
			if d.cfg.sample > 0 && n%uint64(d.cfg.sample) == 0 {
				var sub struct {
					Hash string `json:"hash"`
				}
				if json.Unmarshal([]byte(resp), &sub) == nil && sub.Hash != "" {
					go d.trackInclusion(a.node, sub.Hash, t0)
				}
			}
			return sendAccepted
		case code == http.StatusTooManyRequests:
			atomic.AddUint64(&d.rateLimited, 1)
			time.Sleep(250 * time.Millisecond)
			continue
		case strings.Contains(resp, "duplicate pending nonce"), strings.Contains(resp, "duplicate transaction"):
			// Already in the pool: a retry after a lost reply. Nothing to resend.
			atomic.AddUint64(&d.dupPending, 1)
			a.nonce++
			return sendAlreadyPending
		case strings.Contains(resp, "nonce too low"):
			atomic.AddUint64(&d.nonceResync, 1)
			if n, err := d.nonce(a.node, a.addr); err == nil {
				a.nonce = n
			}
			continue
		case strings.Contains(resp, "per-address mempool limit"):
			d.reject("per-address-limit", resp)
			a.pause(3 * time.Second)
			return sendPaused
		case strings.Contains(resp, "mempool") && strings.Contains(resp, "full"):
			d.reject("mempool-full", resp)
			a.pause(3 * time.Second)
			return sendPaused
		case strings.Contains(resp, "fee too low"):
			d.reject("fee-too-low", resp)
			if m := minFeeRe.FindStringSubmatch(resp); m != nil {
				if min, ok := new(big.Int).SetString(m[1], 10); ok {
					d.raiseFee(min)
					continue
				}
			}
			return sendFailed
		case strings.Contains(strings.ToLower(resp), "insufficient"):
			d.reject("insufficient-balance", resp)
			a.pause(10 * time.Second)
			return sendFailed
		default:
			d.reject("http-"+strconv.Itoa(code), resp)
			return sendFailed
		}
	}
	d.reject("gave-up", "")
	return sendFailed
}

func (d *driver) reject(class, body string) {
	d.rejMu.Lock()
	defer d.rejMu.Unlock()
	d.rejected[class]++
	if len(d.rejSamples) < 20 {
		d.rejSamples = append(d.rejSamples, class+": "+trim(body, 160))
	}
}

func (d *driver) trackInclusion(n *node, hash string, t0 time.Time) {
	deadline := t0.Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		code, body, err := d.get(n, "/tx/"+hash)
		if err != nil || code != http.StatusOK {
			continue
		}
		var r struct {
			BlockHeight uint64 `json:"block_height"`
			Receipt     *struct {
				Status int    `json:"status"`
				Error  string `json:"error"`
			} `json:"receipt"`
		}
		if json.Unmarshal(body, &r) != nil {
			continue
		}
		if r.Receipt != nil && r.Receipt.Status != 1 {
			atomic.AddUint64(&d.samplesFailed, 1)
			d.reject("receipt-failed", r.Receipt.Error)
		}
		d.recordInclusion(time.Since(t0))
		return
	}
	atomic.AddUint64(&d.sampleTimeouts, 1)
}

func (d *driver) recordRTT(rtt time.Duration) {
	d.latMu.Lock()
	d.rttWin = append(d.rttWin, rtt.Seconds())
	d.rttAll = append(d.rttAll, rtt.Seconds())
	d.latMu.Unlock()
}

func (d *driver) recordInclusion(lat time.Duration) {
	d.latMu.Lock()
	d.inclWin = append(d.inclWin, lat.Seconds())
	d.inclAll = append(d.inclAll, lat.Seconds())
	d.latMu.Unlock()
}

// ── rate loop ───────────────────────────────────────────────────────────────

func (d *driver) loop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(float64(time.Second) / d.cfg.rate))
	defer ticker.Stop()
	sem := make(chan struct{}, d.cfg.concurrency)
	var wg sync.WaitGroup
	next := 0
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	span := new(big.Int).Sub(d.cfg.amountMax, d.cfg.amountMin)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
		}
		// The next free, unpaused sender. TryLock keeps one slow node from
		// stalling the tick for everyone.
		var a *account
		for k := 0; k < len(d.senders); k++ {
			c := d.senders[(next+k)%len(d.senders)]
			if c.paused() {
				continue
			}
			if c.mu.TryLock() {
				a = c
				next = (next + k + 1) % len(d.senders)
				break
			}
		}
		if a == nil {
			atomic.AddUint64(&d.skipped, 1)
			continue
		}
		select {
		case sem <- struct{}{}:
		default:
			a.mu.Unlock()
			atomic.AddUint64(&d.skipped, 1)
			continue
		}
		to := d.senders[(a.idx-d.cfg.senderOffset+1)%len(d.senders)].addr
		amount := new(big.Int).Set(d.cfg.amountMin)
		if span.Sign() > 0 {
			amount.Add(amount, new(big.Int).Rand(rng, span))
		}
		wg.Add(1)
		go func(a *account, to qcrypto.Address, amount *big.Int) {
			defer wg.Done()
			defer func() { <-sem }()
			d.sendLocked(a, to, amount)
			a.mu.Unlock()
		}(a, to, amount)
	}
}

// ── reporting ───────────────────────────────────────────────────────────────

type snap struct {
	submitted, accepted, transport, rateLimited, skipped, dupPending, nonceResync, retries uint64
}

func (d *driver) snapshot() snap {
	return snap{
		submitted:   atomic.LoadUint64(&d.submitted),
		accepted:    atomic.LoadUint64(&d.accepted),
		transport:   atomic.LoadUint64(&d.transport),
		rateLimited: atomic.LoadUint64(&d.rateLimited),
		skipped:     atomic.LoadUint64(&d.skipped),
		dupPending:  atomic.LoadUint64(&d.dupPending),
		nonceResync: atomic.LoadUint64(&d.nonceResync),
		retries:     atomic.LoadUint64(&d.retries),
	}
}

func (d *driver) report(ctx context.Context) {
	t := time.NewTicker(d.cfg.interval)
	defer t.Stop()
	prev := d.snapshot()
	prevAt := time.Now()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ticks++
		cur := d.snapshot()
		now := time.Now()
		dt := now.Sub(prevAt).Seconds()

		heights := map[string]uint64{}
		pools := map[string]int{}
		for _, n := range d.nodes {
			if st, err := d.status(n); err == nil {
				heights[n.url] = st.Height
				pools[n.url] = st.MempoolSize
			} else {
				heights[n.url] = 0
				pools[n.url] = -1
			}
		}
		d.scanBlocks(heights[d.nodes[d.cfg.scanNode].url])

		d.latMu.Lock()
		rttWin, inclWin := d.rttWin, d.inclWin
		d.rttWin, d.inclWin = nil, nil
		d.latMu.Unlock()
		d.scanMu.Lock()
		incMine, incAll, bSeen, bAny := d.includedMineWin, d.includedAllWin, d.blocksSeenWin, d.blocksWithAnyWin
		d.includedMineWin, d.includedAllWin, d.blocksSeenWin, d.blocksWithAnyWin = 0, 0, 0, 0
		cumMine, cumAll := d.includedMine, d.includedAll
		d.scanMu.Unlock()
		d.rejMu.Lock()
		rej := map[string]uint64{}
		var rejTotal uint64
		for k, v := range d.rejected {
			rej[k] = v
			rejTotal += v
		}
		d.rejMu.Unlock()

		line := map[string]any{
			"ts":                 now.UTC().Format(time.RFC3339),
			"elapsed_s":          round(now.Sub(d.startedAt).Seconds()),
			"window_s":           round(dt),
			"submitted":          cur.submitted - prev.submitted,
			"accepted":           cur.accepted - prev.accepted,
			"accept_rate_tps":    round(float64(cur.accepted-prev.accepted) / dt),
			"transport_errors":   cur.transport - prev.transport,
			"rate_limited_429":   cur.rateLimited - prev.rateLimited,
			"skipped_ticks":      cur.skipped - prev.skipped,
			"dup_pending":        cur.dupPending - prev.dupPending,
			"nonce_resyncs":      cur.nonceResync - prev.nonceResync,
			"retries":            cur.retries - prev.retries,
			"blocks_scanned":     bSeen,
			"blocks_with_txs":    bAny,
			"included_mine":      incMine,
			"included_all":       incAll,
			"included_mine_tps":  round(float64(incMine) / dt),
			"rtt_ms_p50":         pct(rttWin, 50) * 1000,
			"rtt_ms_p95":         pct(rttWin, 95) * 1000,
			"incl_s_p50":         pct(inclWin, 50),
			"incl_s_p95":         pct(inclWin, 95),
			"incl_samples":       len(inclWin),
			"heights":            heights,
			"mempool":            pools,
			"cum_submitted":      cur.submitted,
			"cum_accepted":       cur.accepted,
			"cum_rejected":       rejTotal,
			"cum_rejected_by":    rej,
			"cum_included_mine":  cumMine,
			"cum_included_all":   cumAll,
			"cum_sample_timeout": atomic.LoadUint64(&d.sampleTimeouts),
			"fee":                d.currentFee().String(),
		}
		head := heights[d.nodes[d.cfg.scanNode].url]
		fmt.Printf("%s | %5.0fs | acc %4d (%.2f/s) rej %3d 429 %3d err %3d | incl mine %4d all %4d in %3d blk (%d w/tx) | rtt p50 %4.0fms p95 %4.0fms | incl p50 %4.1fs p95 %4.1fs (%d) | head %d pool %v | cum acc %d incl %d\n",
			now.UTC().Format("15:04:05"), now.Sub(d.startedAt).Seconds(),
			cur.accepted-prev.accepted, float64(cur.accepted-prev.accepted)/dt, rejTotal, cur.rateLimited-prev.rateLimited, cur.transport-prev.transport,
			incMine, incAll, bSeen, bAny,
			pct(rttWin, 50)*1000, pct(rttWin, 95)*1000, pct(inclWin, 50), pct(inclWin, 95), len(inclWin),
			head, poolList(d.nodes, pools), cur.accepted, cumMine)
		if d.log != nil {
			b, _ := json.Marshal(line)
			d.log.Write(append(b, '\n'))
		}
		prev, prevAt = cur, now

		if d.cfg.topupEvery > 0 && ticks%d.cfg.topupEvery == 0 {
			d.topUp(ctx)
		}
	}
}

func (d *driver) topUp(ctx context.Context) {
	var low []*account
	for _, s := range d.senders {
		if bal, err := d.balance(s.node, s.addr); err == nil && bal.Cmp(d.fundLow) < 0 {
			low = append(low, s)
		}
	}
	if len(low) == 0 {
		return
	}
	fmt.Printf("txload: topping up %d senders\n", len(low))
	for _, s := range low {
		if ctx.Err() != nil {
			return
		}
		d.funder.mu.Lock()
		d.sendLocked(d.funder, s.addr, d.cfg.fundAmt)
		d.funder.mu.Unlock()
	}
}

type wireBlock struct {
	Header struct {
		Height  uint64 `json:"height"`
		TxCount int    `json:"tx_count"`
	} `json:"header"`
	Transactions []struct {
		From qcrypto.Address `json:"from"`
	} `json:"transactions"`
}

// scanBlocks walks every block sealed since the last scan on the scan node and
// counts the transactions that came from this run's senders — the ground truth
// the explorer shows, as opposed to what the pools accepted.
func (d *driver) scanBlocks(head uint64) {
	d.scanMu.Lock()
	from := d.lastScanned + 1
	d.scanMu.Unlock()
	if head == 0 || head < from {
		return
	}
	if head-from > 400 { // never let a long gap stall the reporter
		from = head - 400
	}
	n := d.nodes[d.cfg.scanNode]
	for h := from; h <= head; h++ {
		code, body, err := d.get(n, "/block/"+strconv.FormatUint(h, 10))
		if err != nil || code != http.StatusOK {
			return // retry from here next interval
		}
		var b wireBlock
		if json.Unmarshal(body, &b) != nil {
			return
		}
		var mine uint64
		for _, tx := range b.Transactions {
			if _, ok := d.senderSet[tx.From]; ok {
				mine++
			}
		}
		d.scanMu.Lock()
		d.lastScanned = h
		d.blocksSeen++
		d.blocksSeenWin++
		if b.Header.TxCount > 0 {
			d.blocksWithAny++
			d.blocksWithAnyWin++
		}
		if mine > 0 {
			d.blocksWithMine++
		}
		d.includedMine += mine
		d.includedMineWin += mine
		d.includedAll += uint64(b.Header.TxCount)
		d.includedAllWin += uint64(b.Header.TxCount)
		d.scanMu.Unlock()
	}
}

func (d *driver) summary() {
	// One last scan so the count covers everything sealed before the stop.
	if st, err := d.status(d.nodes[d.cfg.scanNode]); err == nil {
		d.scanBlocks(st.Height)
	}
	cur := d.snapshot()
	elapsed := time.Since(d.startedAt).Seconds()
	d.rejMu.Lock()
	rej := map[string]uint64{}
	var rejTotal uint64
	for k, v := range d.rejected {
		rej[k] = v
		rejTotal += v
	}
	samples := append([]string(nil), d.rejSamples...)
	d.rejMu.Unlock()
	d.latMu.Lock()
	rtt, incl := append([]float64(nil), d.rttAll...), append([]float64(nil), d.inclAll...)
	d.latMu.Unlock()
	d.scanMu.Lock()
	sc := map[string]any{
		"scan_node":         d.nodes[d.cfg.scanNode].url,
		"height_start":      d.scanStart,
		"height_end":        d.lastScanned,
		"blocks_scanned":    d.blocksSeen,
		"blocks_with_mine":  d.blocksWithMine,
		"blocks_with_any":   d.blocksWithAny,
		"included_mine":     d.includedMine,
		"included_all":      d.includedAll,
		"included_mine_tps": round(float64(d.includedMine) / elapsed),
	}
	d.scanMu.Unlock()
	senders := make([]string, 0, len(d.senders))
	for _, s := range d.senders {
		senders = append(senders, s.addr.String())
	}
	out := map[string]any{
		"chain_id":            d.cfg.chainID,
		"nodes":               d.cfg.rpcs,
		"funder":              d.funder.addr.String(),
		"senders":             senders,
		"sender_offset":       d.cfg.senderOffset,
		"target_rate_tps":     d.cfg.rate,
		"fee":                 d.currentFee().String(),
		"started_at":          d.startedAt.UTC().Format(time.RFC3339),
		"ended_at":            time.Now().UTC().Format(time.RFC3339),
		"elapsed_s":           round(elapsed),
		"submitted":           cur.submitted,
		"accepted":            cur.accepted,
		"accepted_tps":        round(float64(cur.accepted) / elapsed),
		"accepted_per_day":    round(float64(cur.accepted) / elapsed * 86400),
		"rejected":            rejTotal,
		"rejected_by":         rej,
		"rejected_samples":    samples,
		"transport_errors":    cur.transport,
		"rate_limited_429":    cur.rateLimited,
		"skipped_ticks":       cur.skipped,
		"dup_pending":         cur.dupPending,
		"nonce_resyncs":       cur.nonceResync,
		"retries":             cur.retries,
		"rtt_ms":              map[string]float64{"p50": pct(rtt, 50) * 1000, "p95": pct(rtt, 95) * 1000, "p99": pct(rtt, 99) * 1000, "max": pct(rtt, 100) * 1000, "n": float64(len(rtt))},
		"inclusion_s":         map[string]float64{"p50": pct(incl, 50), "p95": pct(incl, 95), "p99": pct(incl, 99), "max": pct(incl, 100), "n": float64(len(incl))},
		"inclusion_timeouts":  atomic.LoadUint64(&d.sampleTimeouts),
		"inclusion_failed_rc": atomic.LoadUint64(&d.samplesFailed),
		"chain":               sc,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Printf("\ntxload summary: accepted %d/%d (%.1f%%) in %.0fs = %.2f tx/s (%.0f/day); included from my senders %v of %v scanned blocks; rtt p50 %.0fms p95 %.0fms; inclusion p50 %.1fs p95 %.1fs (%d samples); rejected %d %v\n",
		cur.accepted, cur.submitted, 100*float64(cur.accepted)/float64(max(cur.submitted, 1)), elapsed, float64(cur.accepted)/elapsed, float64(cur.accepted)/elapsed*86400,
		sc["included_mine"], sc["blocks_scanned"], pct(rtt, 50)*1000, pct(rtt, 95)*1000, pct(incl, 50), pct(incl, 95), len(incl), rejTotal, rej)
	if d.cfg.summaryPath != "" {
		if err := os.WriteFile(d.cfg.summaryPath, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "summary:", err)
		}
	} else {
		fmt.Println(string(b))
	}
}

// ── rpc ─────────────────────────────────────────────────────────────────────

type statusResp struct {
	ChainID     string            `json:"chain_id"`
	Height      uint64            `json:"height"`
	MempoolSize int               `json:"mempool_size"`
	Forks       map[string]uint64 `json:"forks"`
}

func (d *driver) status(n *node) (statusResp, error) {
	var st statusResp
	code, body, err := d.get(n, "/status")
	if err != nil {
		return st, err
	}
	if code != http.StatusOK {
		return st, fmt.Errorf("status %d: %s", code, trim(string(body), 120))
	}
	return st, json.Unmarshal(body, &st)
}

func feeFor(st statusResp) *big.Int {
	if h, ok := st.Forks["min_fee_v1"]; ok && h <= st.Height+1 {
		return big.NewInt(mempool.MinFeeV1Amount)
	}
	return big.NewInt(mempool.MinFee)
}

func (d *driver) balance(n *node, a qcrypto.Address) (*big.Int, error) {
	code, body, err := d.get(n, "/balance/"+a.String())
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("balance %d: %s", code, trim(string(body), 120))
	}
	var r struct {
		Balance string `json:"balance"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(r.Balance, 10)
	if !ok {
		return nil, fmt.Errorf("balance: bad number %q", r.Balance)
	}
	return v, nil
}

func (d *driver) nonce(n *node, a qcrypto.Address) (uint64, error) {
	code, body, err := d.get(n, "/nonce/"+a.String())
	if err != nil {
		return 0, err
	}
	if code != http.StatusOK {
		return 0, fmt.Errorf("nonce %d: %s", code, trim(string(body), 120))
	}
	var r struct {
		Nonce uint64 `json:"nonce"`
	}
	return r.Nonce, json.Unmarshal(body, &r)
}

func (d *driver) get(n *node, path string) (int, []byte, error) {
	resp, err := n.http.Get(n.url + path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, err
}

func (d *driver) post(n *node, path string, body []byte) (int, string, error) {
	resp, err := n.http.Post(n.url+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(bytes.TrimSpace(b)), nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func pct(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if p >= 100 {
		return s[len(s)-1]
	}
	i := int(float64(len(s)-1) * p / 100)
	return s[i]
}

func poolList(nodes []*node, pools map[string]int) []int {
	out := make([]int, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, pools[n.url])
	}
	return out
}

func round(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func mustBig(s, flagName string) *big.Int {
	v, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
	if !ok || v.Sign() < 0 {
		fatal("%s: %q is not a non-negative integer", flagName, s)
	}
	return v
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "txload: "+format+"\n", args...)
	os.Exit(1)
}

var _ = hex.EncodeToString
