// sustainload drives a steady stream of real ML-DSA-65 transfers at a fixed
// rate against a running node, from one funded seed. It is the sustained-load
// companion to featsmoke (one pass over every feature) and loadtest (bursts):
// it runs until stopped, keeps the sender's nonce in step with the chain, and
// writes a line per transaction, per block and per summary interval so a run
// leaves evidence behind.
//
//	sustainload --rpc http://127.0.0.1:8080 --seed faucet.seed --chain-id sqc-featval-1 \
//	            --rate 0.9 --log sustained.log --pid sustained.pid
//
// The seed's address must be funded. Stop with SIGINT/SIGTERM (kill $(cat
// sustained.pid)); the driver writes a final summary and removes the pid file.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
)

// ── pure decision logic (unit-tested) ───────────────────────────────────────

// action is what a submit answer means for the sender's nonce counter.
type action int

const (
	actAdvance action = iota // admitted: the nonce is spent, move on
	actSkip                  // someone else holds this nonce in the pool: step past it
	actResync                // the chain is ahead of us: re-read the committed nonce
	actRetry                 // refused for a reason unrelated to the nonce: try the same nonce again
)

func (a action) String() string {
	switch a {
	case actAdvance:
		return "advance"
	case actSkip:
		return "skip"
	case actResync:
		return "resync"
	default:
		return "retry"
	}
}

// parseSubmit classifies a /tx/submit answer. The node answers 202 with the
// tx hash when the pool admitted the tx; refusals are 4xx with an "error"
// field, and a transport failure arrives as code 0 with the error text.
func parseSubmit(code int, body string) (action, string, string) {
	if code/100 == 2 {
		var sub struct {
			Hash string `json:"hash"`
		}
		_ = json.Unmarshal([]byte(body), &sub)
		return actAdvance, sub.Hash, ""
	}
	msg := body
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &e) == nil && e.Error != "" {
		msg = e.Error
	}
	msg = trim(msg)
	switch {
	case strings.Contains(msg, "nonce too low"):
		return actResync, "", msg
	case strings.Contains(msg, "duplicate pending nonce"):
		return actSkip, "", msg
	}
	return actRetry, "", msg
}

// shouldResync says whether a sender whose counter is `pending` ahead of the
// committed nonce should fall back to the committed nonce: only when the pool
// is empty (so the pending txs are gone, not merely waiting) and the committed
// nonce has not moved for longer than `after`.
func shouldResync(pending uint64, mempoolSize int, stuckFor, after time.Duration) bool {
	return pending > 0 && mempoolSize == 0 && stuckFor >= after
}

// rateWindow counts events over a trailing window. Before the window has
// filled, the rate is over the time elapsed so far, so a fresh run reports
// its true pace instead of a number diluted by an empty window.
type rateWindow struct {
	window time.Duration
	start  time.Time
	events []time.Time
}

func newRateWindow(window time.Duration, start time.Time) *rateWindow {
	return &rateWindow{window: window, start: start}
}

func (w *rateWindow) add(t time.Time) { w.events = append(w.events, t) }

func (w *rateWindow) rate(now time.Time) float64 {
	cut := now.Add(-w.window)
	i := 0
	for i < len(w.events) && w.events[i].Before(cut) {
		i++
	}
	w.events = w.events[i:]
	span := now.Sub(w.start)
	if span > w.window {
		span = w.window
	}
	if span <= 0 {
		return 0
	}
	return float64(len(w.events)) / span.Seconds()
}

// deriveSubSeed derives a sender seed from the root (faucet) seed and a
// label, so a load run can own its nonce stream without a second secret on
// disk: the root funds it, the derived key signs the traffic.
func deriveSubSeed(root [qcrypto.SeedSize]byte, label string) [qcrypto.SeedSize]byte {
	return qcrypto.Hash32(append([]byte("sustainload/"+label+"/"), root[:]...))
}

// needsTopUp says whether a sender balance has fallen under the reserve.
func needsTopUp(balance, reserve *big.Int) bool {
	return balance.Cmp(reserve) < 0
}

// blockTxCount reads header.tx_count from a /block/{height} body.
func blockTxCount(body string) (int, error) {
	var b struct {
		Header *struct {
			TxCount int `json:"tx_count"`
		} `json:"header"`
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return 0, err
	}
	if b.Header == nil {
		return 0, errors.New("no header in block body: " + trim(body))
	}
	return b.Header.TxCount, nil
}

// ── driver ──────────────────────────────────────────────────────────────────

type driver struct {
	rpc     string
	chainID string
	http    *http.Client
	w       *signer.DerivedWallet // the sender
	faucet  *signer.DerivedWallet // the root key; nil unless --sub-account
	topup   bool                  // may the faucet fund the sender
	reserve *big.Int
	topupBy *big.Int
	to      qcrypto.Address
	amount  *big.Int
	fee     *big.Int

	nonce      uint64 // next nonce to sign
	startNonce uint64 // committed nonce at start; committed-startNonce = our mined txs
	ok, errs   uint64
	skips      uint64
	resyncs    uint64
	win        *rateWindow
	start      time.Time

	// committed-nonce watch, for the stall rule
	committed   uint64
	committedAt time.Time

	// chain observation, written by the observer goroutine
	mu         sync.Mutex
	height     uint64
	startH     uint64
	mempool    int
	blockTxs   uint64 // tx_count summed over the blocks observed since start
	blocksSeen uint64

	logMu sync.Mutex
	out   io.Writer
}

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:8080", "node RPC base URL")
	seedFile := flag.String("seed", "faucet.seed", "sender seed file (64 hex chars, as featsmoke keygen writes it)")
	chainID := flag.String("chain-id", "", "chain id every tx is signed for (required)")
	rate := flag.Float64("rate", 0.9, "target transactions per second")
	toStr := flag.String("to", "", "recipient address (default: the featsmoke sink SQ:51…)")
	amountStr := flag.String("amount", "1", "amount per transfer, smallest units")
	feeStr := flag.String("fee", strconv.FormatInt(mempool.MinFeeV1Amount, 10), "fee per transfer, smallest units (default: the min_fee_v1 floor, 0.001 SPLD)")
	logPath := flag.String("log", "", "append log lines here (default: stdout)")
	pidPath := flag.String("pid", "", "write the pid here and refuse to start if that pid is alive")
	duration := flag.Duration("duration", 0, "stop after this long (0 = until signalled)")
	summaryEvery := flag.Duration("summary-every", 30*time.Second, "interval between summary lines")
	stallAfter := flag.Duration("stall-after", 90*time.Second, "resync the nonce when nothing commits for this long while the pool is empty")
	subAccount := flag.String("sub-account", "", "send from a key derived from the seed and this label instead of the seed's own key (own nonce stream; the seed's key only funds it)")
	faucetTopup := flag.Bool("faucet-topup", false, "with --sub-account: let the seed's key fund the sender when it is under --reserve (spends a nonce on the seed's key)")
	reserveStr := flag.String("reserve", "100000000000000000000", "with --sub-account: minimum sender balance, smallest units (default 100 SPLD)")
	topupStr := flag.String("topup-amount", "1000000000000000000000", "with --sub-account: amount per top-up, smallest units (default 1,000 SPLD)")
	showSender := flag.Bool("show-sender", false, "print the sender address and exit (to fund a --sub-account by hand)")
	flag.Parse()

	if *chainID == "" {
		fatal("--chain-id is required")
	}
	if *rate <= 0 || *rate > 50 {
		fatal("--rate must be in (0, 50]")
	}
	if err := types.SetClientChainID(*chainID); err != nil {
		fatal("chain id: %v", err)
	}
	raw, err := os.ReadFile(*seedFile)
	if err != nil {
		fatal("read seed: %v", err)
	}
	seed, err := signer.SeedFromHex(strings.TrimSpace(string(raw)))
	if err != nil {
		fatal("seed: %v", err)
	}
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		fatal("derive: %v", err)
	}
	var faucet *signer.DerivedWallet
	if *subAccount != "" {
		faucet = w
		if w, err = signer.DeriveFromSeed(deriveSubSeed(seed, *subAccount)); err != nil {
			fatal("derive sub-account: %v", err)
		}
	}
	if *showSender {
		fmt.Println(w.Address.String())
		return
	}
	reserve, ok := new(big.Int).SetString(*reserveStr, 10)
	if !ok || reserve.Sign() < 0 {
		fatal("--reserve must be a non-negative integer")
	}
	topupBy, ok := new(big.Int).SetString(*topupStr, 10)
	if !ok || topupBy.Sign() <= 0 {
		fatal("--topup-amount must be a positive integer")
	}
	to := qcrypto.Address{0x51}
	if *toStr != "" {
		if to, err = qcrypto.AddressFromString(*toStr); err != nil {
			fatal("--to: %v", err)
		}
	}
	amount, ok := new(big.Int).SetString(*amountStr, 10)
	if !ok || amount.Sign() < 0 {
		fatal("--amount must be a non-negative integer")
	}
	fee, ok := new(big.Int).SetString(*feeStr, 10)
	if !ok || fee.Sign() < 0 {
		fatal("--fee must be a non-negative integer")
	}

	var out io.Writer = os.Stdout
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fatal("open log: %v", err)
		}
		defer f.Close()
		out = f
	}
	if *pidPath != "" {
		if err := writePID(*pidPath); err != nil {
			fatal("%v", err)
		}
		defer os.Remove(*pidPath)
	}

	d := &driver{
		rpc: strings.TrimRight(*rpc, "/"), chainID: *chainID,
		http: &http.Client{Timeout: 10 * time.Second},
		w:    w, faucet: faucet, topup: *faucetTopup, reserve: reserve, topupBy: topupBy,
		to: to, amount: amount, fee: fee,
		start: time.Now(), out: out,
	}
	d.win = newRateWindow(60*time.Second, d.start)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	if d.faucet != nil {
		if err := d.ensureFunded(ctx); err != nil {
			d.logf("fatal", "%v", err)
			fatal("%v", err)
		}
	}
	n, err := d.committedNonce(ctx, d.w.Address, 60*time.Second)
	if err != nil {
		d.logf("fatal", "%v", err)
		fatal("%v", err)
	}
	d.nonce, d.startNonce, d.committed, d.committedAt = n, n, n, time.Now()
	if st, err := d.status(); err == nil {
		d.height, d.startH, d.mempool = st.Height, st.Height, st.MempoolSize
	}
	mode := "seed-key"
	if d.faucet != nil {
		mode = "sub-account:" + *subAccount + " funded-by=" + d.faucet.Address.String() + " faucet-topup=" + strconv.FormatBool(d.topup)
	}
	d.logf("start", "pid=%d rpc=%s chain=%s mode=%s from=%s to=%s rate=%.2f/s amount=%s fee=%s nonce=%d bal=%s h=%d",
		os.Getpid(), d.rpc, d.chainID, mode, d.w.Address, d.to, *rate, d.amount, d.fee, d.nonce, d.balance(d.w.Address), d.height)

	go d.observe(ctx)

	interval := time.Duration(float64(time.Second) / *rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastSummary := time.Now()
	for {
		select {
		case <-ctx.Done():
			d.summary(time.Now())
			d.logf("stop", "reason=%s ok=%d err=%d", stopReason(ctx), d.ok, d.errs)
			return
		case now := <-ticker.C:
			d.step(now)
			if now.Sub(lastSummary) >= *summaryEvery {
				d.summary(now)
				d.watchNonce(now, *stallAfter)
				if d.faucet != nil && d.topup {
					if err := d.ensureFunded(ctx); err != nil {
						d.logf("warn", "top-up: %v", err)
					}
				}
				lastSummary = now
			}
		}
	}
}

func stopReason(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "duration elapsed"
	}
	return "signal"
}

// step signs and submits one transfer at the current nonce and updates the
// counter according to the node's answer.
func (d *driver) step(now time.Time) {
	n := d.nonce
	tx := types.NewTransfer(d.w.Address, d.to, d.amount, d.fee, n, now.Unix())
	if err := tx.Sign(d.w.Private, true); err != nil {
		d.errs++
		d.logf("err", "nonce=%d sign: %v", n, err)
		return
	}
	body, _ := json.Marshal(tx)
	code, resp := d.post("/tx/submit", body)
	act, hash, msg := parseSubmit(code, resp)
	h, mp := d.snapshot()
	switch act {
	case actAdvance:
		d.nonce++
		d.ok++
		d.win.add(now)
		d.logf("ok", "nonce=%d hash=%s h=%d mp=%d ok=%d err=%d r60=%.2f/s", n, short(hash), h, mp, d.ok, d.errs, d.win.rate(now))
	case actSkip:
		d.nonce++
		d.skips++
		d.errs++
		d.logf("err", "nonce=%d code=%d act=%s h=%d mp=%d ok=%d err=%d r60=%.2f/s msg=%q", n, code, act, h, mp, d.ok, d.errs, d.win.rate(now), msg)
	case actResync:
		d.resyncs++
		d.errs++
		if c, err := d.committedNonce(context.Background(), d.w.Address, 5*time.Second); err == nil {
			d.nonce = c
		}
		d.logf("err", "nonce=%d code=%d act=%s->%d h=%d mp=%d ok=%d err=%d r60=%.2f/s msg=%q", n, code, act, d.nonce, h, mp, d.ok, d.errs, d.win.rate(now), msg)
	default:
		d.errs++
		d.logf("err", "nonce=%d code=%d act=%s h=%d mp=%d ok=%d err=%d r60=%.2f/s msg=%q", n, code, act, h, mp, d.ok, d.errs, d.win.rate(now), msg)
	}
}

// watchNonce compares our counter with the chain and applies the stall rule.
func (d *driver) watchNonce(now time.Time, stallAfter time.Duration) {
	c, err := d.committedNonce(context.Background(), d.w.Address, 5*time.Second)
	if err != nil {
		d.logf("warn", "nonce read failed: %v", err)
		return
	}
	if c != d.committed {
		d.committed, d.committedAt = c, now
	}
	pending := uint64(0)
	if d.nonce > c {
		pending = d.nonce - c
	}
	_, mp := d.snapshot()
	if shouldResync(pending, mp, now.Sub(d.committedAt), stallAfter) {
		d.resyncs++
		d.logf("warn", "stall: committed nonce %d unchanged for %s with %d pending and an empty pool; resync %d->%d", c, now.Sub(d.committedAt).Truncate(time.Second), pending, d.nonce, c)
		d.nonce = c
		d.committedAt = now
	}
}

func (d *driver) summary(now time.Time) {
	c, err := d.committedNonce(context.Background(), d.w.Address, 5*time.Second)
	committed := "?"
	mined := "?"
	if err == nil {
		committed = strconv.FormatUint(c, 10)
		mined = strconv.FormatUint(c-d.startNonce, 10)
	}
	pending := uint64(0)
	if err == nil && d.nonce > c {
		pending = d.nonce - c
	}
	d.mu.Lock()
	h, startH, mp, blockTxs, blocks := d.height, d.startH, d.mempool, d.blockTxs, d.blocksSeen
	d.mu.Unlock()
	up := now.Sub(d.start)
	avg := 0.0
	if up > 0 {
		avg = float64(d.ok) / up.Seconds()
	}
	d.logf("sum", "up=%s ok=%d err=%d skip=%d resync=%d submitted_nonce=%d committed=%s mined=%s pending=%d bal=%s h=%d->%d (+%d) blocks_seen=%d block_txs=%d mp=%d r60=%.2f/s avg=%.2f/s",
		up.Truncate(time.Second), d.ok, d.errs, d.skips, d.resyncs, d.nonce, committed, mined, pending, d.balance(d.w.Address), startH, h, h-startH, blocks, blockTxs, mp, d.win.rate(now), avg)
}

// observe polls /status every 2s and logs each new block with its tx count.
func (d *driver) observe(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := d.status()
		if err != nil {
			d.logf("warn", "status: %v", err)
			continue
		}
		d.mu.Lock()
		last := d.height
		d.mempool = st.MempoolSize
		d.mu.Unlock()
		if st.Height <= last {
			continue
		}
		from := last + 1
		if st.Height-from >= 30 { // a long gap: do not replay history one block at a time
			d.logf("warn", "height jumped %d->%d; counting only the last 30 blocks", last, st.Height)
			from = st.Height - 29
		}
		for h := from; h <= st.Height; h++ {
			code, body := d.get("/block/" + strconv.FormatUint(h, 10))
			n, err := blockTxCount(body)
			if code != 200 || err != nil {
				d.logf("warn", "block %d: %d %s", h, code, trim(body))
				continue
			}
			d.mu.Lock()
			d.blockTxs += uint64(n)
			d.blocksSeen++
			total := d.blockTxs
			d.mu.Unlock()
			d.logf("blk", "h=%d txs=%d block_txs=%d mp=%d", h, n, total, st.MempoolSize)
		}
		d.mu.Lock()
		d.height = st.Height
		d.mu.Unlock()
	}
}

func (d *driver) snapshot() (uint64, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.height, d.mempool
}

// ── chain plumbing ──────────────────────────────────────────────────────────

type status struct {
	ChainID     string `json:"chain_id"`
	Height      uint64 `json:"height"`
	MempoolSize int    `json:"mempool_size"`
}

func (d *driver) status() (status, error) {
	code, body := d.get("/status")
	var st status
	if code != 200 {
		return st, fmt.Errorf("/status: %d %s", code, trim(body))
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		return st, fmt.Errorf("/status: %v", err)
	}
	if st.ChainID != d.chainID {
		return st, fmt.Errorf("/status: chain_id %q, want %q", st.ChainID, d.chainID)
	}
	return st, nil
}

// committedNonce reads an account's committed nonce, retrying until max.
func (d *driver) committedNonce(ctx context.Context, addr qcrypto.Address, max time.Duration) (uint64, error) {
	deadline := time.Now().Add(max)
	for {
		code, body := d.get("/nonce/" + addr.String())
		var n struct {
			Nonce uint64 `json:"nonce"`
		}
		if code == 200 && json.Unmarshal([]byte(body), &n) == nil {
			return n.Nonce, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("nonce for %s: %d %s", addr, code, trim(body))
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (d *driver) balance(a qcrypto.Address) *big.Int {
	_, body := d.get("/balance/" + a.String())
	var b struct {
		Balance string `json:"balance"`
	}
	if json.Unmarshal([]byte(body), &b) == nil {
		if v, ok := new(big.Int).SetString(b.Balance, 10); ok {
			return v
		}
	}
	return big.NewInt(0)
}

// ensureFunded keeps the sub-account above the reserve. Without
// --faucet-topup it only checks: an underfunded sender is a fatal start and a
// warning later, so the seed's own nonce stream is never touched by surprise.
func (d *driver) ensureFunded(ctx context.Context) error {
	bal := d.balance(d.w.Address)
	if !needsTopUp(bal, d.reserve) {
		return nil
	}
	if !d.topup {
		return fmt.Errorf("sender %s balance %s is under the reserve %s; fund it, or pass --faucet-topup to let %s fund it",
			d.w.Address, bal, d.reserve, d.faucet.Address)
	}
	fn, err := d.committedNonce(ctx, d.faucet.Address, 30*time.Second)
	if err != nil {
		return err
	}
	// The faucet may have other senders on its nonce stream (another driver,
	// featsmoke): step past pending nonces, re-read on "too low", and give up
	// after a bounded number of attempts.
	for attempt := 0; attempt < 40; attempt++ {
		tx := types.NewTransfer(d.faucet.Address, d.w.Address, d.topupBy, d.fee, fn, time.Now().Unix())
		if err := tx.Sign(d.faucet.Private, true); err != nil {
			return err
		}
		body, _ := json.Marshal(tx)
		code, resp := d.post("/tx/submit", body)
		act, hash, msg := parseSubmit(code, resp)
		switch act {
		case actAdvance:
			d.logf("fund", "faucet=%s nonce=%d -> %s amount=%s hash=%s", d.faucet.Address, fn, d.w.Address, d.topupBy, short(hash))
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				if !needsTopUp(d.balance(d.w.Address), d.reserve) {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			return fmt.Errorf("top-up %s not applied within 90s", short(hash))
		case actSkip:
			fn++
		case actResync:
			if fn, err = d.committedNonce(ctx, d.faucet.Address, 10*time.Second); err != nil {
				return err
			}
		default:
			d.logf("warn", "fund attempt %d refused: code=%d msg=%q", attempt, code, msg)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
	}
	return errors.New("top-up: gave up after 40 attempts")
}

func (d *driver) get(path string) (int, string) {
	resp, err := d.http.Get(d.rpc + path)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err.Error()
	}
	return resp.StatusCode, string(bytes.TrimSpace(b))
}

func (d *driver) post(path string, body []byte) (int, string) {
	resp, err := d.http.Post(d.rpc+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err.Error()
	}
	return resp.StatusCode, string(bytes.TrimSpace(b))
}

// ── pid file, logging, helpers ──────────────────────────────────────────────

// writePID refuses to start over a live driver: the pool rejects a second
// sender on the same nonces, so two drivers on one seed only fight.
func writePID(path string) error {
	if raw, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
			if err := syscall.Kill(pid, 0); err == nil {
				return fmt.Errorf("another sustainload is running (pid %d, %s); stop it first", pid, path)
			}
		}
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func (d *driver) logf(kind, format string, args ...any) {
	d.logMu.Lock()
	defer d.logMu.Unlock()
	fmt.Fprintf(d.out, "%s %-5s %s\n", time.Now().UTC().Format(time.RFC3339), kind, fmt.Sprintf(format, args...))
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sustainload: "+format+"\n", args...)
	os.Exit(1)
}
