package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The topic constant is the keccak256 of the exact Solidity signature; a
// drift here silently blinds the relayer. The literal is cross-pinned with
// the migration dapp (apps/migration-dapp/src/selectors.ts), which verifies
// it independently against @noble/hashes.
func TestBurnedTopic(t *testing.T) {
	const want = "0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58"
	if burnedTopic0 != want {
		t.Fatalf("burnedTopic0 = %s, want %s", burnedTopic0, want)
	}
}

func word(v uint64) string {
	return fmt.Sprintf("%064x", v)
}

func makeLog(id, asset uint64, amount string, recipient string, block uint64) evmLog {
	amt, _ := new(big.Int).SetString(amount, 10)
	return evmLog{
		Address: "0x00000000000000000000000000000000000dea11",
		Topics: []string{
			burnedTopic0,
			"0x" + word(id),
			"0x" + strings.Repeat("00", 12) + strings.Repeat("aa", 20),
			"0x" + word(asset),
		},
		Data:        "0x" + fmt.Sprintf("%064x", amt) + recipient,
		BlockNumber: fmt.Sprintf("0x%x", block),
		TxHash:      "0x" + strings.Repeat("ab", 32),
		LogIndex:    "0x0",
	}
}

func TestDecodeBurn(t *testing.T) {
	rec := strings.Repeat("cd", 32)
	// asset word 1 is an SPLV burn; the migration credits it as native SPLD 1:1.
	b, err := decodeBurn(makeLog(7, 1, "5000000000000000000", rec, 99))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.ID != 7 || b.Asset != "spld" || b.Amount.String() != "5000000000000000000" {
		t.Fatalf("burn = %+v", b)
	}
	if b.Recipient != "SQ:"+rec || b.Block != 99 {
		t.Fatalf("burn = %+v", b)
	}

	bad := []struct {
		name string
		mut  func(*evmLog)
	}{
		{"removed", func(l *evmLog) { l.Removed = true }},
		{"three topics", func(l *evmLog) { l.Topics = l.Topics[:3] }},
		{"wrong topic0", func(l *evmLog) { l.Topics[0] = "0x" + word(1) }},
		{"id zero", func(l *evmLog) { l.Topics[1] = "0x" + word(0) }},
		{"asset 9", func(l *evmLog) { l.Topics[3] = "0x" + word(9) }},
		{"short data", func(l *evmLog) { l.Data = "0x" + word(1) }},
		{"zero amount", func(l *evmLog) { l.Data = "0x" + word(0) + rec }},
		{"zero recipient", func(l *evmLog) { l.Data = "0x" + word(5) + strings.Repeat("0", 64) }},
	}
	for _, tc := range bad {
		l := makeLog(7, 1, "5", rec, 99)
		tc.mut(&l)
		if _, err := decodeBurn(l); err == nil {
			t.Errorf("%s: decoded without error", tc.name)
		}
	}
}

// ── End-to-end pass against stubbed chains ──────────────────────────────────

type stubChains struct {
	t         *testing.T
	mu        sync.Mutex
	head      uint64
	logs      []evmLog
	claimed   map[uint64]bool
	submits   []claimParams
	receipts  map[string]int // hash -> status
	bridge    qcrypto.Address
	operator  qcrypto.Address
	failNext  int      // transient submit rejections to serve before accepting
	revert    bool     // when true, submitted claims mine as a SOFT revert (status 0)
	paused    bool     // when true, /bridge/{addr} reports the bridge paused
	threshold uint64   // attestation threshold /bridge/{addr} reports (0 → 1)
	operators []string // active operators /bridge/{addr} reports (nil → [operator])
	maxClaim  string   // max_claim_amount /bridge/{addr} reports ("" → omitted)
}

func (s *stubChains) evmHandler(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	defer s.mu.Unlock()
	var result any
	switch req.Method {
	case "eth_chainId":
		result = "0xa83" // 2691
	case "eth_blockNumber":
		result = fmt.Sprintf("0x%x", s.head)
	case "eth_getLogs":
		params := req.Params[0].(map[string]any)
		from, _ := parseHexQuantity(params["fromBlock"].(string))
		to, _ := parseHexQuantity(params["toBlock"].(string))
		out := []evmLog{}
		for _, l := range s.logs {
			b, _ := parseHexQuantity(l.BlockNumber)
			if b >= from && b <= to {
				out = append(out, l)
			}
		}
		result = out
	default:
		s.t.Errorf("unexpected EVM method %s", req.Method)
	}
	raw, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(raw)})
}

func (s *stubChains) quantumHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/status":
		_ = json.NewEncoder(w).Encode(map[string]any{"chain_id": types.ChainID, "height": 10})
	case strings.HasPrefix(path, "/nonce/"):
		_ = json.NewEncoder(w).Encode(map[string]any{"nonce": len(s.submits)})
	case strings.HasPrefix(path, "/bridge/") && strings.Contains(path, "/claim/"):
		var id uint64
		fmt.Sscanf(path[strings.LastIndex(path, "/")+1:], "%d", &id)
		_ = json.NewEncoder(w).Encode(map[string]any{"claimed": s.claimed[id]})
	case strings.HasPrefix(path, "/bridge/"):
		th := s.threshold
		if th == 0 {
			th = 1
		}
		ops := s.operators
		if ops == nil {
			ops = []string{s.operator.String()}
		}
		info := map[string]any{
			"owner": s.operator.String(), "operators": ops,
			"paused": s.paused, "threshold": th, "evm_chain_id": 2691,
			"evm_contract": "0x00000000000000000000000000000000000dea11",
			"pool_balance": "1000000",
		}
		if s.maxClaim != "" {
			info["max_claim_amount"] = s.maxClaim
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"info": info})
	case path == "/tx/submit":
		var tx types.Transaction
		if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
			s.t.Errorf("submit decode: %v", err)
		}
		if tx.Type != types.TxCall || tx.To != moduleRouterAddress() {
			s.t.Errorf("submit: not a router TxCall")
		}
		if len(tx.Signature) == 0 {
			s.t.Errorf("submit: unsigned tx")
		}
		if s.failNext > 0 {
			s.failNext--
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "tx rejected: fee too low"})
			return
		}
		var mc moduleCall
		var cp claimParams
		_ = json.Unmarshal(tx.Data, &mc)
		raw, _ := json.Marshal(mc.Params)
		_ = json.Unmarshal(raw, &cp)
		if mc.Module != "bridge" || mc.Action != "claim" {
			s.t.Errorf("submit: envelope %+v", mc)
		}
		// Every claim must carry a cryptographically valid attestation over the
		// canonical digest — the on-chain safety property, checked end to end.
		s.verifyAttestation(cp)
		s.submits = append(s.submits, cp)
		hash := fmt.Sprintf("%064x", len(s.submits))
		if s.revert {
			// Mined but reverted: a real on-chain failure. The claimed slot is
			// NOT set — the claim did not take.
			s.receipts[hash] = 0
		} else {
			s.receipts[hash] = 1
			s.claimed[cp.ID] = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted", "hash": hash})
	case strings.HasSuffix(path, "/receipt"):
		hash := strings.TrimSuffix(strings.TrimPrefix(path, "/tx/"), "/receipt")
		if st, ok := s.receipts[hash]; ok {
			// Raw-receipt shape: status and error are BOTH top-level (matches
			// rpc/explorer.go /tx/{hash}/receipt, not the nested /tx/{hash}).
			msg := ""
			if st == 0 {
				msg = "bridge claim: bridge is paused"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": st, "error": msg})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "transaction not found"})
	default:
		s.t.Errorf("unexpected quantum path %s", path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// verifyAttestation reconstructs the claim digest from the submitted params and
// checks at least one attestation signature verifies under CtxBridge — the same
// check the module does on-chain.
func (s *stubChains) verifyAttestation(cp claimParams) {
	if len(cp.Attestations) == 0 {
		s.t.Fatalf("claim %d carries no attestations", cp.ID)
	}
	aw, err := assetWord(cp.Asset)
	if err != nil {
		s.t.Fatalf("asset: %v", err)
	}
	recipient, err := qcrypto.AddressFromString(cp.Recipient)
	if err != nil {
		s.t.Fatalf("recipient: %v", err)
	}
	amount, _ := new(big.Int).SetString(cp.Amount, 10)
	var evmTx [32]byte
	raw, _ := hex.DecodeString(strings.TrimPrefix(cp.EvmTx, "0x"))
	copy(evmTx[:], raw)
	digest, err := types.BridgeClaimDigest(types.ChainID, s.bridge, cp.ID, aw, amount, recipient, evmTx)
	if err != nil {
		s.t.Fatalf("digest: %v", err)
	}
	ok := false
	for _, a := range cp.Attestations {
		pub, err := qcrypto.PublicKeyFromBytes(a.PubKey)
		if err != nil {
			continue
		}
		if qcrypto.Verify(pub, digest[:], qcrypto.CtxBridge, a.Signature) {
			ok = true
		}
	}
	if !ok {
		s.t.Fatalf("claim %d has no valid attestation", cp.ID)
	}
}

func newRelayerFixture(t *testing.T) (*Relayer, *stubChains) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	operator := qcrypto.DeriveAddress(pub)
	var bridge qcrypto.Address
	bridge[0] = 0xbb

	s := &stubChains{
		t: t, claimed: map[uint64]bool{}, receipts: map[string]int{},
		bridge: bridge, operator: operator,
	}
	evmSrv := httptest.NewServer(http.HandlerFunc(s.evmHandler))
	t.Cleanup(evmSrv.Close)
	qSrv := httptest.NewServer(http.HandlerFunc(s.quantumHandler))
	t.Cleanup(qSrv.Close)

	r := &Relayer{
		EVM:         NewEVMClient(evmSrv.URL),
		Quantum:     NewQuantumClient(qSrv.URL),
		EvmContract: "0x00000000000000000000000000000000000dea11",
		Bridge:      bridge, Operator: operator, Priv: priv,
		Confirmations: 100, BatchBlocks: 1000, Fee: 2000,
		MaxAttempts: 2, ReceiptWait: 5 * time.Second,
		State:     &relayerState{LastProcessed: 0},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Logf:      t.Logf,
	}
	return r, s
}

func TestRelayerPass(t *testing.T) {
	r, s := newRelayerFixture(t)
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{
		makeLog(1, 0, "1000000000000000000", rec, 50),  // spld, confirmed
		makeLog(2, 1, "2000000000000000000", rec, 60),  // splv burn, credited as spld
		makeLog(3, 0, "3000000000000000000", rec, 250), // too fresh: 300-100=200 < 250
	}

	if err := r.Preflight(2691, types.ChainID); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	n, err := r.RunOnce()
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n != 2 || len(s.submits) != 2 {
		t.Fatalf("submitted %d claims, want 2", len(s.submits))
	}
	if s.submits[0].ID != 1 || s.submits[0].Asset != "spld" || s.submits[0].Amount != "1000000000000000000" {
		t.Fatalf("claim 1 = %+v", s.submits[0])
	}
	if s.submits[1].ID != 2 || s.submits[1].Asset != "spld" {
		t.Fatalf("claim 2 = %+v", s.submits[1])
	}
	if s.submits[0].Recipient != "SQ:"+rec || s.submits[0].Bridge != r.Bridge.String() {
		t.Fatalf("claim 1 = %+v", s.submits[0])
	}
	if r.State.LastProcessed != 200 {
		t.Fatalf("cursor = %d, want 200 (head - confirmations)", r.State.LastProcessed)
	}

	// The fresh burn settles once the chain advances past its depth.
	s.mu.Lock()
	s.head = 400
	s.mu.Unlock()
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if len(s.submits) != 3 || s.submits[2].ID != 3 {
		t.Fatalf("submits = %+v", s.submits)
	}

	// A third pass finds nothing new and submits nothing.
	if n, err := r.RunOnce(); err != nil || n != 0 {
		t.Fatalf("pass 3: n=%d err=%v", n, err)
	}

	// The cursor survives a restart via the state file.
	saved, err := loadState(r.StatePath)
	if err != nil || saved.LastProcessed != r.State.LastProcessed {
		t.Fatalf("state file: %+v err=%v", saved, err)
	}
}

func TestRelayerSkipsAlreadyClaimed(t *testing.T) {
	r, s := newRelayerFixture(t)
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}
	s.claimed[1] = true // e.g. a crash after submit, before the cursor moved

	n, err := r.RunOnce()
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n != 1 || len(s.submits) != 0 {
		t.Fatalf("n=%d submits=%d, want settled without a submit", n, len(s.submits))
	}
}

// A claim that MINES AND REVERTS repeatedly is genuinely unclaimable-as-is:
// after MaxAttempts mined reverts it is recorded skipped and the cursor moves.
func TestRelayerRevertsThenSkips(t *testing.T) {
	r, s := newRelayerFixture(t)
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}
	s.revert = true // every submitted claim mines as a SOFT revert

	// Attempt 1 (mined revert) holds the cursor.
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if r.State.LastProcessed != 0 {
		t.Fatalf("cursor moved past an unsettled burn")
	}
	// Attempt 2 reaches MaxAttempts (2) → record skip, advance.
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if r.State.LastProcessed != 200 || len(r.State.Skipped) != 1 || r.State.Skipped[0].ID != 1 {
		t.Fatalf("state = %+v", r.State)
	}

	// A once-skipped burn is not re-submitted on later passes (no fee waste,
	// no duplicate skip entries).
	before := len(s.submits)
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if len(s.submits) != before || len(r.State.Skipped) != 1 {
		t.Fatalf("skipped burn re-settled: submits %d->%d, skipped %d", before, len(s.submits), len(r.State.Skipped))
	}
}

// A TRANSIENT submit rejection (rate limit, fee floor, duplicate pending
// nonce) must never count toward the skip limit — congestion is not a claim
// failure. The burn holds and is retried indefinitely; the cursor never moves.
func TestRelayerTransientSubmitNeverSkips(t *testing.T) {
	r, s := newRelayerFixture(t)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}
	s.failNext = 999 // every submit rejected, forever

	for i := 0; i < 5; i++ {
		// Advance past the maximum backoff so every pass submits and is
		// rejected, rather than only exercising the backoff guard.
		now = now.Add(time.Minute)
		if _, err := r.RunOnce(); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		s.mu.Lock()
		remaining := s.failNext
		s.mu.Unlock()
		if remaining != 999-(i+1) {
			t.Fatalf("pass %d: remaining rejections = %d, want %d", i, remaining, 999-(i+1))
		}
	}
	if r.State.LastProcessed != 0 {
		t.Fatalf("cursor advanced past a burn that only saw transient failures")
	}
	if len(r.State.Skipped) != 0 {
		t.Fatalf("transient submit failures were recorded as skips: %+v", r.State.Skipped)
	}
	if r.attempts[1] != 0 {
		t.Fatalf("transient submit failures counted as mined reverts: %d", r.attempts[1])
	}

	// Clearing the transient condition does not bypass the retry delay.
	s.mu.Lock()
	s.failNext = 0
	s.mu.Unlock()
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("backoff pass: %v", err)
	}
	if len(s.submits) != 0 || r.State.LastProcessed != 0 || len(r.State.Skipped) != 0 {
		t.Fatalf("acted before backoff expired: submits=%d state=%+v", len(s.submits), r.State)
	}

	// After the maximum retry delay the burn settles and the cursor advances,
	// without sleeping in the test or requiring manual replay.
	now = now.Add(time.Minute)
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	if len(s.submits) != 1 || r.State.LastProcessed != 200 {
		t.Fatalf("did not self-heal: submits=%d cursor=%d", len(s.submits), r.State.LastProcessed)
	}
}

// While the bridge is paused the relayer holds the cursor and submits nothing —
// pause is the incident brake, not a signal to shunt every burn into replay.
func TestRelayerHoldsWhilePaused(t *testing.T) {
	r, s := newRelayerFixture(t)
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}
	s.paused = true

	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if len(s.submits) != 0 || r.State.LastProcessed != 0 || len(r.State.Skipped) != 0 {
		t.Fatalf("paused pass acted: submits=%d cursor=%d skipped=%d", len(s.submits), r.State.LastProcessed, len(r.State.Skipped))
	}

	// Unpause → the held burn is claimed and the cursor advances.
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("unpaused pass: %v", err)
	}
	if len(s.submits) != 1 || r.State.LastProcessed != 200 {
		t.Fatalf("did not resume after unpause: submits=%d cursor=%d", len(s.submits), r.State.LastProcessed)
	}
}

// An undecodable Burned log from the pinned contract holds the cursor (never
// silently dropped) and self-heals when good data returns.
func TestRelayerHoldsOnUndecodableLog(t *testing.T) {
	r, s := newRelayerFixture(t)
	rec := strings.Repeat("cd", 32)
	good := makeLog(1, 0, "1000", rec, 50)
	corrupt := makeLog(2, 0, "2000", rec, 60)
	corrupt.Data = "0xdead" // too short to decode
	s.head = 300
	s.logs = []evmLog{good, corrupt}

	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("pass: %v", err)
	}
	// The good burn (id 1) settled; the cursor is held at 0 by the corrupt log.
	if r.State.LastProcessed != 0 {
		t.Fatalf("cursor advanced past a corrupt log: %d", r.State.LastProcessed)
	}
	if !s.claimed[1] {
		t.Fatalf("the decodable burn before the corrupt one was not claimed")
	}

	// Good data returns → the batch completes and the cursor advances.
	s.mu.Lock()
	s.logs[1] = makeLog(2, 0, "2000", rec, 60)
	s.mu.Unlock()
	if _, err := r.RunOnce(); err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	if r.State.LastProcessed != 200 || !s.claimed[2] {
		t.Fatalf("did not self-heal: cursor=%d claimed2=%v", r.State.LastProcessed, s.claimed[2])
	}
}

// --replay-id must not panic when the replayed claim fails, even on a Relayer
// whose attempts map was never initialised (the Replay path skips RunOnce).
func TestSettleNilAttemptsNoPanic(t *testing.T) {
	r, s := newRelayerFixture(t)
	r.attempts = nil // simulate the direct-Replay path
	s.revert = true
	rec := strings.Repeat("cd", 32)
	ok, err := r.settle(Burn{ID: 1, Asset: "spld", Amount: mustBig(t, "1000"), Recipient: "SQ:" + rec, TxHash: "0x" + strings.Repeat("ab", 32), Block: 50}, &BridgeInfo{Threshold: 1})
	if err != nil || ok {
		t.Fatalf("settle = (%v, %v), want (false, nil)", ok, err)
	}
	if r.attempts[1] != 1 {
		t.Fatalf("mined revert not counted: %d", r.attempts[1])
	}
}

func mustBig(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big %q", s)
	}
	return v
}

// A state file bound to a different deployment is refused, so a reused cursor
// can never silently skip a new contract's early burns.
func TestStateIdentityMismatchRefused(t *testing.T) {
	st := &relayerState{EvmChainID: 2692, EvmContract: "0xaaaa", Bridge: "SQ:aa", LastProcessed: 500}
	if err := st.verifyIdentity(2691, "0xbbbb", "SQ:bb"); err == nil {
		t.Fatal("mismatched identity accepted")
	}
	if err := st.verifyIdentity(2692, "0xaaaa", "SQ:aa"); err != nil {
		t.Fatalf("matching identity refused: %v", err)
	}
}

// M-of-N via the shared attestation dir: with threshold 2, one relayer alone
// holds (1/2), and a second relayer sharing the dir sees the first's
// attestation and submits a claim carrying two distinct attestations.
func TestRelayerMOfNViaSharedDir(t *testing.T) {
	r1, s := newRelayerFixture(t)
	s.threshold = 2
	dir := t.TempDir()
	r1.AttestationDir = dir

	// A second attestor sharing the same chains and dir, own key and cursor.
	pub2, priv2, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	r2 := *r1
	r2.Priv = priv2
	r2.Operator = qcrypto.DeriveAddress(pub2)
	r2.attempts = map[uint64]int{}
	r2.State = &relayerState{}
	r2.StatePath = filepath.Join(t.TempDir(), "state2.json")
	// Both attestors must be active operators for their shared-dir
	// attestations to count (readAttestations filters on the operator list).
	s.operators = []string{s.operator.String(), r2.Operator.String()}

	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}

	// r1 alone: 1/2 attestations → holds, submits nothing, cursor unmoved.
	if _, err := r1.RunOnce(); err != nil {
		t.Fatalf("r1 pass: %v", err)
	}
	if len(s.submits) != 0 || r1.State.LastProcessed != 0 {
		t.Fatalf("r1 acted alone: submits=%d cursor=%d", len(s.submits), r1.State.LastProcessed)
	}

	// r2 sees r1's attestation in the shared dir → 2/2 → submits.
	if _, err := r2.RunOnce(); err != nil {
		t.Fatalf("r2 pass: %v", err)
	}
	if len(s.submits) != 1 {
		t.Fatalf("r2 did not submit with quorum: submits=%d", len(s.submits))
	}
	if len(s.submits[0].Attestations) != 2 {
		t.Fatalf("claim carried %d attestations, want 2", len(s.submits[0].Attestations))
	}
}

func TestRelayerPreflightRefusals(t *testing.T) {
	r, _ := newRelayerFixture(t)
	if err := r.Preflight(9999, types.ChainID); err == nil || !strings.Contains(err.Error(), "chain id") {
		t.Fatalf("wrong evm chain id: %v", err)
	}
	if err := r.Preflight(2691, "some-other-chain"); err == nil || !strings.Contains(err.Error(), "quantum chain id") {
		t.Fatalf("wrong quantum chain id: %v", err)
	}
	r.EvmContract = "0x" + strings.Repeat("99", 20)
	if err := r.Preflight(2691, types.ChainID); err == nil || !strings.Contains(err.Error(), "burn contract") {
		t.Fatalf("wrong contract: %v", err)
	}
}

// A threshold > 1 without a shared --attestation-dir can never reach quorum:
// the relayer would hold on every burn forever, silently. Preflight must fail
// fast instead, and succeed once the dir is wired.
func TestRelayerPreflightRequiresAttestationDir(t *testing.T) {
	r, s := newRelayerFixture(t)
	s.threshold = 2
	if err := r.Preflight(2691, types.ChainID); err == nil || !strings.Contains(err.Error(), "attestation-dir") {
		t.Fatalf("preflight without attestation dir: %v", err)
	}
	r.AttestationDir = t.TempDir()
	if err := r.Preflight(2691, types.ChainID); err != nil {
		t.Fatalf("preflight with attestation dir: %v", err)
	}
}

// A burn whose amount exceeds the bridge's max_claim_amount is a
// deterministic on-chain refusal: submitting would mine-revert and burn fees
// on every attempt. The relayer records it skipped (recoverable via
// --replay-id once the cap changes) and settles the rest, without a single
// fee-burning submit.
func TestRelayerSkipsOverCapBurn(t *testing.T) {
	r, s := newRelayerFixture(t)
	s.maxClaim = "500"
	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{
		makeLog(1, 0, "1000", rec, 50), // over the 500 cap
		makeLog(2, 0, "400", rec, 60),  // under the cap: settles normally
	}
	if err := r.Preflight(2691, types.ChainID); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	n, err := r.RunOnce()
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n != 1 || len(s.submits) != 1 || s.submits[0].ID != 2 {
		t.Fatalf("n=%d submits=%v, want only burn 2 submitted", n, s.submits)
	}
	if len(r.State.Skipped) != 1 || r.State.Skipped[0].ID != 1 || !strings.Contains(r.State.Skipped[0].Reason, "max_claim_amount") {
		t.Fatalf("skipped = %+v", r.State.Skipped)
	}
	if r.State.LastProcessed != 200 {
		t.Fatalf("cursor = %d, want 200 (head - confirmations)", r.State.LastProcessed)
	}
}

// Junk in the shared attestation dir must never poison the quorum: foreign
// keys (even with valid signatures), operator keys with garbage signatures,
// and corrupt files are all ignored, while the two honest attestations still
// reach threshold and submit.
func TestRelayerAttestationDirIgnoresJunk(t *testing.T) {
	r1, s := newRelayerFixture(t)
	s.threshold = 2
	dir := t.TempDir()
	r1.AttestationDir = dir

	pub2, priv2, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	op2 := qcrypto.DeriveAddress(pub2)
	s.operators = []string{s.operator.String(), op2.String()}

	r2 := *r1
	r2.Priv = priv2
	r2.Operator = op2
	r2.attempts = map[uint64]int{}
	r2.State = &relayerState{}
	r2.StatePath = filepath.Join(t.TempDir(), "state2.json")

	rec := strings.Repeat("cd", 32)
	s.head = 300
	s.logs = []evmLog{makeLog(1, 0, "1000", rec, 50)}

	burn := Burn{ID: 1, Asset: "spld", Amount: mustBig(t, "1000"), Recipient: "SQ:" + rec, TxHash: "0x" + strings.Repeat("ab", 32), Block: 50}
	digest, err := claimDigest(r1.Bridge, burn)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}

	// r1's pass publishes its valid attestation and holds at 1/2.
	if _, err := r1.RunOnce(); err != nil {
		t.Fatalf("r1: %v", err)
	}
	if len(s.submits) != 0 {
		t.Fatalf("r1 submitted with only its own attestation")
	}

	// Plant junk: 16 well-formed attestations from a key that is NOT an
	// operator (valid signatures, wrong attestor), one file with an operator
	// pubkey but a garbage signature, and one corrupt file.
	foreignPub, foreignPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	fsig, err := qcrypto.Sign(foreignPriv, digest[:], qcrypto.CtxBridge)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	burnDir := r1.burnDir(burn)
	for i := 0; i < 16; i++ {
		raw, _ := json.Marshal(attestation{PubKey: foreignPub.Bytes(), Signature: fsig})
		if err := os.WriteFile(filepath.Join(burnDir, fmt.Sprintf("foreign-%d.json", i)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(attestation{PubKey: pub2.Bytes(), Signature: make([]byte, qcrypto.SignatureSize)})
	if err := os.WriteFile(filepath.Join(burnDir, "bad-sig.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(burnDir, "corrupt.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	// r2 still reaches quorum with exactly the two honest attestations.
	if _, err := r2.RunOnce(); err != nil {
		t.Fatalf("r2: %v", err)
	}
	if len(s.submits) != 1 {
		t.Fatalf("r2 submitted %d claims, want 1", len(s.submits))
	}
	atts := s.submits[0].Attestations
	if len(atts) != 2 {
		t.Fatalf("claim carried %d attestations, want 2 (junk must not count)", len(atts))
	}
	for _, a := range atts {
		pub, err := qcrypto.PublicKeyFromBytes(a.PubKey)
		if err != nil {
			t.Fatalf("pubkey: %v", err)
		}
		if !qcrypto.Verify(pub, digest[:], qcrypto.CtxBridge, a.Signature) {
			t.Fatalf("submitted attestation does not verify")
		}
	}
}
