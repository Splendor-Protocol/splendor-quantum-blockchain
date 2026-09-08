// featsmoke drives the feat/validator surface end to end against a running
// node, the way a wallet would: real ML-DSA-65 transactions through
// /tx/submit, receipts through /tx/{hash}/receipt, module reads through
// /call. It is the smoke test docs/AWS_FEAT_VALIDATOR_TEST_PLAN.md runs, and
// it runs just as well against a local node.
//
//	featsmoke keygen --out faucet.seed           # make (or reuse) the faucet key; prints its SQ: address
//	featsmoke run --rpc http://127.0.0.1:8080 --seed faucet.seed --chain-id sqc-featval-1
//	featsmoke verify --rpc http://3.84.91.28:8080 --seed faucet.seed --chain-id sqc-featval-1
//	  # SQVL doors only: check → attest → settle → proof
//
// The faucet address must be funded at genesis. Every step prints PASS or
// FAIL; the exit code is non-zero if anything failed.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: featsmoke {keygen|run|verify} [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		keygen(os.Args[2:])
	case "run":
		os.Exit(run(os.Args[2:]))
	case "verify":
		os.Exit(runVerify(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

// ── keygen ──────────────────────────────────────────────────────────────────

func keygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "faucet.seed", "seed file (64 hex chars); created if missing, reused if present")
	fs.Bool("print-address", false, "accepted for compatibility; the address is always printed on stdout")
	_ = fs.Parse(args)

	var seed [qcrypto.SeedSize]byte
	if raw, err := os.ReadFile(*out); err == nil {
		s, err := signer.SeedFromHex(strings.TrimSpace(string(raw)))
		if err != nil {
			fatal("seed file %s: %v", *out, err)
		}
		seed = s
	} else {
		s, err := qcrypto.GenerateSeed()
		if err != nil {
			fatal("generate seed: %v", err)
		}
		seed = s
		if err := os.WriteFile(*out, []byte(hex.EncodeToString(seed[:])+"\n"), 0o600); err != nil {
			fatal("write %s: %v", *out, err)
		}
		fmt.Fprintf(os.Stderr, "wrote a new faucet seed to %s (keep it: it is the key)\n", *out)
	}
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		fatal("derive: %v", err)
	}
	fmt.Println(w.Address.String())
}

// ── run ─────────────────────────────────────────────────────────────────────

type runner struct {
	rpc      string
	chainID  string
	http     *http.Client
	w        *signer.DerivedWallet
	slhPub   *qcrypto.SLHPublicKey
	slhPriv  *qcrypto.SLHPrivateKey
	failures int
	nonce    uint64
	nonceOK  bool
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	rpc := fs.String("rpc", "http://127.0.0.1:8080", "node RPC base URL")
	seedFile := fs.String("seed", "faucet.seed", "faucet seed file from keygen")
	chainID := fs.String("chain-id", "", "chain id the node must report and every tx is signed for (required)")
	_ = fs.Parse(args)
	if *chainID == "" {
		fatal("--chain-id is required")
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
	slhPub, slhPriv, err := qcrypto.SLHKeypairFromSeed(seed)
	if err != nil {
		fatal("slh derive: %v", err)
	}
	r := &runner{rpc: strings.TrimRight(*rpc, "/"), chainID: *chainID, http: &http.Client{Timeout: 20 * time.Second}, w: w, slhPub: slhPub, slhPriv: slhPriv}
	fmt.Printf("featsmoke against %s as %s\n", r.rpc, w.Address)

	start := r.stepStatus()
	r.stepFaucet()
	r.stepStaking()
	r.stepVerify()
	r.stepQRC721()
	r.stepFeeFloor()
	r.stepStillProducing(start)

	if r.failures > 0 {
		fmt.Printf("\n%d step(s) FAILED\n", r.failures)
		return 1
	}
	fmt.Println("\nALL PASS")
	return 0
}

// runVerify exercises only the SQVL product doors end-to-end:
// POST /verify/check → deploy/register → POST /verify/attest → POST /verify/settle → GET /verify/proof/{id}.
func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	rpc := fs.String("rpc", "http://127.0.0.1:8080", "node RPC base URL")
	seedFile := fs.String("seed", "faucet.seed", "faucet seed file from keygen")
	chainID := fs.String("chain-id", "", "chain id the node must report and every tx is signed for (required)")
	_ = fs.Parse(args)
	if *chainID == "" {
		fatal("--chain-id is required")
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
	slhPub, slhPriv, err := qcrypto.SLHKeypairFromSeed(seed)
	if err != nil {
		fatal("slh derive: %v", err)
	}
	r := &runner{rpc: strings.TrimRight(*rpc, "/"), chainID: *chainID, http: &http.Client{Timeout: 20 * time.Second}, w: w, slhPub: slhPub, slhPriv: slhPriv}
	fmt.Printf("featsmoke verify (SQVL doors) against %s as %s\n", r.rpc, w.Address)
	r.stepStatus()
	r.stepFaucet()
	r.stepVerify()
	if r.failures > 0 {
		fmt.Printf("\n%d step(s) FAILED\n", r.failures)
		return 1
	}
	fmt.Println("\nALL PASS")
	return 0
}

// ── steps ───────────────────────────────────────────────────────────────────

func (r *runner) stepStatus() uint64 {
	st := r.waitStatus(90 * time.Second)
	r.check("status: chain_id", st.ChainID == r.chainID, "got %q want %q", st.ChainID, r.chainID)
	for _, f := range []string{"module_revert_v1", "validator_gates_v1", "verify_v1", "qrc721_v1"} {
		_, ok := st.Forks[f]
		r.check("status: fork "+f+" scheduled", ok, "forks=%v", st.Forks)
	}
	if h, ok := st.Forks["min_fee_v1"]; ok {
		fmt.Printf("      min_fee_v1 scheduled at height %d (head %d)\n", h, st.Height)
	} else {
		fmt.Printf("      min_fee_v1 not scheduled on this chain\n")
	}
	r.check("status: genesis_validator anchor known", st.GenesisValidator != "", "no genesis_validator in /status")
	r.check("status: producing (height >= 1)", st.Height >= 1, "height=%d", st.Height)
	return st.Height
}

func (r *runner) stepFaucet() {
	bal := r.balance(r.w.Address)
	r.check("faucet: funded at genesis", bal.Sign() > 0, "balance of %s is %s", r.w.Address, bal)
}

func (r *runner) stepStaking() {
	info, err := r.read("staking", "staking_info", map[string]any{})
	if !r.check("staking: staking_info readable", err == nil, "%v", err) {
		return
	}
	fmt.Printf("      staking_info: min_stake=%v total_staked=%v validators=%v\n", info["min_stake"], info["total_staked"], info["validator_count"])

	// Idempotent across runs: a key registers once.
	if v, err := r.read("staking", "validator", map[string]any{"address": r.w.Address.String()}); err == nil && v["registered"] == true {
		r.check("staking: register a 5,000 SPLD bond (already registered by an earlier run)", true, "")
	} else {
		stake, _ := new(big.Int).SetString("5000000000000000000000", 10) // 5,000 SPLD, above the Bronze minimum
		rc, err := r.moduleTx("staking", "register", map[string]any{"pubkey": r.w.PublicKey, "amount": stake.String()})
		if !r.check("staking: register a 5,000 SPLD bond", err == nil && rc.ok(), "%v %s", err, rc.err) {
			return
		}
	}
	v, err := r.read("staking", "validator", map[string]any{"address": r.w.Address.String()})
	r.check("staking: validator record active", err == nil && v["registered"] == true && v["status"] == "active", "%v %v", err, v)
	cp, err := r.read("staking", "can_produce", map[string]any{"address": r.w.Address.String()})
	// Bonded but never admitted: the gate must say no, and say why.
	r.check("staking: can_produce is false without an admission pass", err == nil && cp["eligible"] == false, "%v %v", err, cp)
	fmt.Printf("      can_produce: %v\n", cp["reason"])
	set, err := r.read("staking", "validator_set", map[string]any{})
	r.check("staking: validator_set readable", err == nil, "%v", err)
	if err == nil {
		fmt.Printf("      validator_set: %v\n", compact(set))
	}
}

func (r *runner) stepVerify() {
	// A dual-signed instruction; /verify/check must accept it with no state.
	var id types.InstructionID
	copy(id[:], qcrypto.HashBytes([]byte(fmt.Sprintf("featsmoke:%d", time.Now().UnixNano()))))
	ins := &types.VerifyInstruction{
		Version: types.VerifyInstructionVersion, InstructionID: id,
		Purpose: "smoke", Origin: "featsmoke", Payload: []byte("hello from featsmoke"),
		SettlementHint: "splendor-qc", SettlementAction: types.SettleFinalizeOnly,
	}
	if err := types.SignVerifyInstruction(r.chainID, ins, r.w.Private, r.slhPriv); err != nil {
		r.check("verify: dual-sign instruction", false, "%v", err)
		return
	}
	body, _ := json.Marshal(ins)
	code, resp := r.post("/verify/check", body)
	var chk map[string]any
	_ = json.Unmarshal([]byte(resp), &chk)
	r.check("verify: POST /verify/check accepts the dual signature", code == 200 && chk["valid"] == true, "%d %s", code, trim(resp))
	digest, _ := chk["digest"].(string)

	// deploy + register stay on /tx/submit (no product door for them).
	rc, err := r.moduleTx("verify", "deploy", map[string]any{"threshold": 1, "operators": []string{}})
	if !r.check("verify: deploy an instance", err == nil && rc.ok() && rc.contract != "", "%v %s", err, rc.err) {
		return
	}
	inst := rc.contract
	fmt.Printf("      instance %s\n", inst)

	rc, err = r.moduleTx("verify", "register", map[string]any{"verify": inst, "instruction": ins})
	if !r.check("verify: register the instruction", err == nil && rc.ok(), "%v %s", err, rc.err) {
		return
	}
	instAddr, _ := qcrypto.AddressFromString(inst)
	var d [32]byte
	db, _ := hex.DecodeString(digest)
	copy(d[:], db)
	sigM, sigS, err := types.SignVerifyAttestation(r.chainID, instAddr, d, r.w.Private, r.slhPriv)
	if !r.check("verify: dual-sign an attestation bound to the instance", err == nil, "%v", err) {
		return
	}
	// Product door: POST /verify/attest (not /tx/submit).
	rc, err = r.moduleDoor("/verify/attest", "verify", "attest", map[string]any{
		"verify": inst, "instruction_id": id.Hex(),
		"attestation": map[string]string{
			"pubkey": hex.EncodeToString(r.w.PublicKey), "slh_pubkey": hex.EncodeToString(r.slhPub.Bytes()),
			"sig_mldsa": hex.EncodeToString(sigM), "sig_slh": hex.EncodeToString(sigS),
		},
	})
	r.check("verify: POST /verify/attest admits attest (owner is an attestor)", err == nil && rc.ok(), "%v %s", err, rc.err)

	proof, err := r.read("verify", "proof", map[string]any{"verify": inst, "instruction_id": id.Hex()})
	r.check("verify: proof shows threshold met, unspent", err == nil && proof["threshold_met"] == true && proof["spent"] == false, "%v %v", err, compact(proof))

	// Product door: POST /verify/settle with spend (finalize_only).
	rc, err = r.moduleDoor("/verify/settle", "verify", "spend", map[string]any{"verify": inst, "instruction_id": id.Hex()})
	r.check("verify: POST /verify/settle admits spend (finalize_only) once", err == nil && rc.ok(), "%v %s", err, rc.err)
	proof, err = r.read("verify", "proof", map[string]any{"verify": inst, "instruction_id": id.Hex()})
	r.check("verify: proof shows spent", err == nil && proof["spent"] == true, "%v %v", err, compact(proof))

	rc, err = r.moduleDoor("/verify/settle", "verify", "spend", map[string]any{"verify": inst, "instruction_id": id.Hex()})
	r.check("verify: replayed settle is a failed receipt (already spent)", err == nil && !rc.ok() && strings.Contains(rc.err, "already spent"), "%v ok=%v %s", err, rc.ok(), rc.err)

	code, resp = r.get("/verify/proof/" + id.Hex() + "?instance=" + inst)
	r.check("verify: GET /verify/proof/{id} serves the record", code == 200 && strings.Contains(resp, `"spent":true`), "%d %s", code, trim(resp))
}

func (r *runner) stepQRC721() {
	rc, err := r.moduleTx("qrc721", "deploy", map[string]any{"name": "Smoke Caps", "symbol": "SMK"})
	if !r.check("qrc721: deploy a collection", err == nil && rc.ok() && rc.contract != "", "%v %s", err, rc.err) {
		return
	}
	col := rc.contract
	rc, err = r.moduleTx("qrc721", "mint", map[string]any{"collection": col, "to": r.w.Address.String(), "id": "1", "uri": "ipfs://smoke"})
	r.check("qrc721: owner mints id 1", err == nil && rc.ok(), "%v %s", err, rc.err)
	own, err := r.read("qrc721", "owner_of", map[string]any{"collection": col, "id": "1"})
	r.check("qrc721: owner_of reports the minter's recipient", err == nil && own["owner"] == r.w.Address.String(), "%v %v", err, own)
}

func (r *runner) stepFeeFloor() {
	st := r.waitStatus(20 * time.Second)
	to := qcrypto.Address{0x51}
	active := false
	if h, ok := st.Forks["min_fee_v1"]; ok && h <= st.Height+1 {
		active = true
	}
	if active {
		code, resp := r.submitTransfer(to, big.NewInt(1), big.NewInt(mempool.MinFee))
		r.check("fee floor: min_fee_v1 active — a 1,000-unit fee is refused", code == 400 && strings.Contains(resp, "fee too low"), "%d %s", code, trim(resp))
		rc, err := r.transferTx(to, big.NewInt(1), big.NewInt(mempool.MinFeeV1Amount))
		r.check("fee floor: min_fee_v1 active — 0.001 SPLD is admitted and applied", err == nil && rc.ok(), "%v %s", err, rc.err)
	} else {
		rc, err := r.transferTx(to, big.NewInt(1), big.NewInt(mempool.MinFee))
		r.check("fee floor: min_fee_v1 not yet active — the launch floor still applies", err == nil && rc.ok(), "%v %s", err, rc.err)
		if h, ok := st.Forks["min_fee_v1"]; ok {
			fmt.Printf("      re-run after height %d to observe the flip\n", h)
		}
	}
}

func (r *runner) stepStillProducing(start uint64) {
	deadline := time.Now().Add(15 * time.Second)
	st := r.waitStatus(20 * time.Second)
	for st.Height <= start && time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		st = r.waitStatus(20 * time.Second)
	}
	r.check("chain: still producing after the run", st.Height > start, "height %d -> %d after 15s", start, st.Height)
}

// ── chain plumbing ──────────────────────────────────────────────────────────

type status struct {
	ChainID          string            `json:"chain_id"`
	Height           uint64            `json:"height"`
	Forks            map[string]uint64 `json:"forks"`
	GenesisValidator string            `json:"genesis_validator"`
}

func (r *runner) waitStatus(max time.Duration) status {
	deadline := time.Now().Add(max)
	for {
		code, body := r.get("/status")
		var st status
		if code == 200 && json.Unmarshal([]byte(body), &st) == nil && st.Height >= 1 {
			return st
		}
		if time.Now().After(deadline) {
			fatal("node at %s did not report a height >= 1 within %s (last: %d %s)", r.rpc, max, code, trim(body))
		}
		time.Sleep(2 * time.Second)
	}
}

type receipt struct {
	status   int
	err      string
	contract string
}

func (rc receipt) ok() bool { return rc.status == 1 }

// feeNow is the floor the next block will enforce.
func (r *runner) feeNow() *big.Int {
	code, body := r.get("/status")
	var st status
	if code == 200 && json.Unmarshal([]byte(body), &st) == nil {
		if h, ok := st.Forks["min_fee_v1"]; ok && h <= st.Height+1 {
			return big.NewInt(mempool.MinFeeV1Amount)
		}
	}
	return big.NewInt(mempool.MinFee)
}

func (r *runner) nextNonce() uint64 {
	// Always re-read the confirmed nonce when we have no local cursor yet, and
	// keep a local cursor afterward. Concurrent faucet top-ups (sustainload)
	// can still race; submit paths bump on "duplicate pending nonce".
	if !r.nonceOK {
		r.refreshNonce()
	}
	n := r.nonce
	r.nonce++
	return n
}

func (r *runner) refreshNonce() {
	code, body := r.get("/nonce/" + r.w.Address.String())
	var n struct {
		Nonce uint64 `json:"nonce"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &n) != nil {
		fatal("nonce: %d %s", code, trim(body))
	}
	if !r.nonceOK || n.Nonce > r.nonce {
		r.nonce = n.Nonce
	}
	r.nonceOK = true
}

// bumpNonceFromDup parses "duplicate pending nonce N for …" and advances the
// local cursor past N. Returns whether it recognized the refusal.
func (r *runner) bumpNonceFromDup(resp string) bool {
	const marker = "duplicate pending nonce "
	i := strings.Index(resp, marker)
	if i < 0 {
		return false
	}
	rest := resp[i+len(marker):]
	var n uint64
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + uint64(ch-'0')
	}
	if n+1 > r.nonce {
		r.nonce = n + 1
		r.nonceOK = true
	}
	return true
}

// moduleTx signs and submits a module TxCall via /tx/submit, then waits for its receipt.
func (r *runner) moduleTx(module, action string, params any) (receipt, error) {
	tx, err := r.signedModuleCall(module, action, params)
	if err != nil {
		return receipt{}, err
	}
	return r.submitAndWait(tx)
}

// moduleDoor signs the same module TxCall but admits it through a SQVL product
// door (/verify/attest or /verify/settle) as {"tx": …}.
func (r *runner) moduleDoor(door, module, action string, params any) (receipt, error) {
	tx, err := r.signedModuleCall(module, action, params)
	if err != nil {
		return receipt{}, err
	}
	return r.submitAndWaitPath(door, tx, true)
}

func (r *runner) signedModuleCall(module, action string, params any) (*types.Transaction, error) {
	praw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: praw})
	if err != nil {
		return nil, err
	}
	tx := types.NewCall(r.w.Address, vm.ModuleRouterAddress, data, big.NewInt(0), r.feeNow(), r.nextNonce(), time.Now().Unix())
	if err := tx.Sign(r.w.Private, true); err != nil {
		return nil, err
	}
	return tx, nil
}

func (r *runner) transferTx(to qcrypto.Address, amount, fee *big.Int) (receipt, error) {
	tx := types.NewTransfer(r.w.Address, to, amount, fee, r.nextNonce(), time.Now().Unix())
	if err := tx.Sign(r.w.Private, true); err != nil {
		return receipt{}, err
	}
	return r.submitAndWait(tx)
}

// submitTransfer submits without waiting and reports the admission answer;
// a refused tx does not consume the nonce.
func (r *runner) submitTransfer(to qcrypto.Address, amount, fee *big.Int) (int, string) {
	n := r.nextNonce()
	tx := types.NewTransfer(r.w.Address, to, amount, fee, n, time.Now().Unix())
	if err := tx.Sign(r.w.Private, true); err != nil {
		return 0, err.Error()
	}
	b, _ := json.Marshal(tx)
	code, resp := r.post("/tx/submit", b)
	if code/100 != 2 {
		r.nonce = n
	}
	return code, resp
}

func (r *runner) submitAndWait(tx *types.Transaction) (receipt, error) {
	return r.submitAndWaitPath("/tx/submit", tx, false)
}

func (r *runner) submitAndWaitPath(path string, tx *types.Transaction, wrap bool) (receipt, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var body []byte
		if wrap {
			body, _ = json.Marshal(map[string]any{"tx": tx})
		} else {
			body, _ = json.Marshal(tx)
		}
		code, resp := r.post(path, body)
		if code/100 != 2 {
			if r.bumpNonceFromDup(resp) {
				// rebuild with a fresh nonce/signature
				tx.Nonce = r.nextNonce()
				tx.Timestamp = time.Now().Unix()
				if err := tx.Sign(r.w.Private, true); err != nil {
					return receipt{}, err
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			r.nonce = tx.Nonce // not admitted: the nonce is still free
			return receipt{}, fmt.Errorf("%s refused: %d %s", path, code, trim(resp))
		}
		var sub struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal([]byte(resp), &sub); err != nil || sub.Hash == "" {
			return receipt{}, fmt.Errorf("%s response: %s", path, trim(resp))
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			code, body := r.get("/tx/" + strings.TrimPrefix(sub.Hash, "0x") + "/receipt")
			if code == 200 {
				return parseReceipt(body), nil
			}
			if time.Now().After(deadline) {
				return receipt{}, fmt.Errorf("no receipt for %s within 60s (last %d %s)", sub.Hash, code, trim(body))
			}
			time.Sleep(1 * time.Second)
		}
	}
	return receipt{}, fmt.Errorf("%s: exhausted nonce retries under load", path)
}

func parseReceipt(body string) receipt {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return receipt{err: "unparseable receipt: " + trim(body)}
	}
	rc := receipt{}
	switch v := m["status"].(type) {
	case float64:
		rc.status = int(v)
	case bool:
		if v {
			rc.status = 1
		}
	case string:
		if v == "1" || strings.EqualFold(v, "success") {
			rc.status = 1
		}
	}
	if e, ok := m["error"].(string); ok {
		rc.err = e
	}
	switch v := m["contract_address"].(type) {
	case string:
		rc.contract = v
	case []any:
		var a qcrypto.Address
		if len(v) == qcrypto.AddressSize {
			for i, x := range v {
				f, _ := x.(float64)
				a[i] = byte(f)
			}
			rc.contract = a.String()
		}
	}
	return rc
}

// read is a module read through POST /call, returning the decoded value.
func (r *runner) read(module, action string, params any) (map[string]any, error) {
	praw, _ := json.Marshal(params)
	data, _ := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: praw})
	req, _ := json.Marshal(map[string]string{"to": vm.ModuleRouterAddress.String(), "data": hex.EncodeToString(data), "encoding": "hex"})
	code, body := r.post("/call", req)
	if code != 200 {
		return nil, fmt.Errorf("/call %s.%s: %d %s", module, action, code, trim(body))
	}
	var out struct {
		Decoded      map[string]any `json:"decoded"`
		Reverted     bool           `json:"reverted"`
		RevertReason string         `json:"revert_reason"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, err
	}
	if out.Reverted {
		return nil, errors.New(out.RevertReason)
	}
	return out.Decoded, nil
}

func (r *runner) balance(a qcrypto.Address) *big.Int {
	_, body := r.get("/balance/" + a.String())
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

func (r *runner) get(path string) (int, string) {
	resp, err := r.http.Get(r.rpc + path)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(bytes.TrimSpace(b))
}

func (r *runner) post(path string, body []byte) (int, string) {
	resp, err := r.http.Post(r.rpc+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(bytes.TrimSpace(b))
}

// ── reporting ───────────────────────────────────────────────────────────────

func (r *runner) check(name string, ok bool, format string, args ...any) bool {
	if ok {
		fmt.Printf("PASS  %s\n", name)
		return true
	}
	r.failures++
	fmt.Printf("FAIL  %s: %s\n", name, fmt.Sprintf(format, args...))
	return false
}

func compact(m map[string]any) string {
	b, _ := json.Marshal(m)
	return trim(string(b))
}

func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "featsmoke: "+format+"\n", args...)
	os.Exit(1)
}
