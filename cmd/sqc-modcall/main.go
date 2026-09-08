// sqc-modcall — the module-call driver used to exercise native modules (the
// validators registry, verify, qrc721, …) from a shell, and the forged-block
// prober used to test a node's inbound block check from the outside.
//
// This is TEST TOOLING, not a wallet. It reads the password from a file, prints
// receipts, and has one subcommand whose whole purpose is to be refused.
//
//	sqc-modcall write  --node URL --wallet W --pass-file P --chain-id ID \
//	                   --module validators --action register --params '{...}' [--wait]
//	sqc-modcall read   --node URL --module validators --action validator --params '{...}'
//	sqc-modcall forge  --node URL --wallet W --pass-file P --chain-id ID
//	sqc-modcall addr
//
// `forge` seals a syntactically valid, correctly linked block with a key of the
// caller's choosing and pushes it to a node. The inbound path verifies the seal
// against the node's trusted validator key, so the only right answer is a
// refusal; anything else is the finding.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "write":
		err = cmdWrite(os.Args[2:])
	case "read":
		err = cmdRead(os.Args[2:])
	case "forge":
		err = cmdForge(os.Args[2:])
	case "addr":
		fmt.Printf("router     %s\nvalidators %s\n", vm.ModuleRouterAddress, vm.ValidatorsModuleAddress())
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sqc-modcall <write|read|forge|addr> [flags]")
	os.Exit(2)
}

// ── flags ────────────────────────────────────────────────────────────────────

type flags map[string]string

func parseFlags(args []string) flags {
	f := flags{}
	for i := 0; i < len(args); i++ {
		a := strings.TrimPrefix(args[i], "--")
		if k, v, ok := strings.Cut(a, "="); ok {
			f[k] = v
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			f[a] = args[i+1]
			i++
			continue
		}
		f[a] = "true"
	}
	return f
}

func (f flags) need(k string) (string, error) {
	if v, ok := f[k]; ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("--%s is required", k)
}

func (f flags) or(k, def string) string {
	if v, ok := f[k]; ok && v != "" {
		return v
	}
	return def
}

// ── shared helpers ───────────────────────────────────────────────────────────

func loadWallet(f flags) (*qcrypto.PublicKey, *qcrypto.PrivateKey, error) {
	path, err := f.need("wallet")
	if err != nil {
		return nil, nil, err
	}
	passFile, err := f.need("pass-file")
	if err != nil {
		return nil, nil, err
	}
	pass, err := os.ReadFile(passFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read pass file: %w", err)
	}
	return qcrypto.LoadWallet(path, bytes.TrimRight(pass, "\r\n"))
}

// adoptChainID pins the chain id every signature commits to. Without it the
// tool signs for mainnet and the node refuses the transaction, which is the
// correct outcome but a confusing one to debug.
func adoptChainID(f flags) error {
	id, err := f.need("chain-id")
	if err != nil {
		return err
	}
	return types.SetClientChainID(id)
}

func getJSON(url string, out any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func postJSON(url string, headers map[string]string, in any, out any) (int, string, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	if out != nil && resp.StatusCode == http.StatusOK {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, strings.TrimSpace(string(raw)), nil
}

// moduleEnvelope is the router's wire shape, shared by reads and writes.
func moduleEnvelope(module, action, params string) ([]byte, error) {
	if params == "" {
		params = "{}"
	}
	if !json.Valid([]byte(params)) {
		return nil, fmt.Errorf("--params is not valid JSON: %s", params)
	}
	return json.Marshal(struct {
		Module string          `json:"module"`
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}{module, action, json.RawMessage(params)})
}

// ── write ────────────────────────────────────────────────────────────────────

func cmdWrite(args []string) error {
	f := parseFlags(args)
	node := f.or("node", "http://127.0.0.1:8080")
	if err := adoptChainID(f); err != nil {
		return err
	}
	pub, priv, err := loadWallet(f)
	if err != nil {
		return err
	}
	from := qcrypto.DeriveAddress(pub)

	module, err := f.need("module")
	if err != nil {
		return err
	}
	action, err := f.need("action")
	if err != nil {
		return err
	}
	// {{PUBKEY}} expands to the caller's own consensus key in base64, so a
	// register/set_pass for the signing wallet does not need it pasted in.
	params := strings.ReplaceAll(f.or("params", "{}"), "{{PUBKEY}}",
		base64.StdEncoding.EncodeToString(pub.Bytes()))
	calldata, err := moduleEnvelope(module, action, params)
	if err != nil {
		return err
	}

	var nonceResp struct {
		Nonce uint64 `json:"nonce"`
	}
	if err := getJSON(node+"/nonce/"+from.String(), &nonceResp); err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	fee, ok := new(big.Int).SetString(f.or("fee", "100000"), 10)
	if !ok {
		return fmt.Errorf("invalid --fee")
	}
	// Amount is deliberately zero: the router ignores tx.Amount, and a bond
	// attached as value would leave the chain rather than enter escrow.
	tx := types.NewCall(from, vm.ModuleRouterAddress, calldata,
		big.NewInt(0), fee, nonceResp.Nonce, time.Now().Unix())
	if err := tx.Sign(priv, nonceResp.Nonce == 0); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	status, body, err := postJSON(node+"/tx/submit", nil, tx, nil)
	if err != nil {
		return err
	}
	// The node answers 202 for "in the mempool", not 200: it has accepted the
	// transaction, not executed it.
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("submit rejected (%d): %s", status, body)
	}
	full, err := tx.FullHash()
	if err != nil {
		return err
	}
	hash := hex.EncodeToString(full[:])
	fmt.Printf("submitted %s.%s from=%s nonce=%d tx=%s\n", module, action, from, nonceResp.Nonce, hash)

	if f["wait"] != "true" {
		return nil
	}
	return waitReceipt(node, hash)
}

// waitReceipt polls for the receipt and prints it. A SOFT module failure is a
// receipt with status 0 and the reason inside it — the tool prints it and does
// NOT exit non-zero, because "the chain refused this on purpose" is frequently
// the result being tested for.
func waitReceipt(node, hash string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var r map[string]any
		if err := getJSON(node+"/tx/"+hash+"/receipt", &r); err == nil && len(r) > 0 {
			out, _ := json.Marshal(r)
			fmt.Printf("receipt %s\n", out)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("no receipt for %s within 60s", hash)
}

// ── read ─────────────────────────────────────────────────────────────────────

func cmdRead(args []string) error {
	f := parseFlags(args)
	node := f.or("node", "http://127.0.0.1:8080")
	module, err := f.need("module")
	if err != nil {
		return err
	}
	action, err := f.need("action")
	if err != nil {
		return err
	}
	calldata, err := moduleEnvelope(module, action, f.or("params", "{}"))
	if err != nil {
		return err
	}
	var out map[string]any
	status, body, err := postJSON(node+"/call", nil, map[string]string{
		"to":   vm.ModuleRouterAddress.String(),
		"data": hex.EncodeToString(calldata),
	}, &out)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("call failed (%d): %s", status, body)
	}
	pretty, _ := json.MarshalIndent(out["decoded"], "", "  ")
	fmt.Println(string(pretty))
	return nil
}

// ── forge ────────────────────────────────────────────────────────────────────

// cmdForge seals a block with the caller's key at the node's next height and
// pushes it. Everything about the block is well-formed — hash, signature,
// merkle root, linkage — EXCEPT that the key sealing it may not be one the
// chain admits. That isolates the producer gate: the block reaches
// ExecuteBlock's admission check with nothing else for it to fail on, and the
// state root is deliberately junk so that if the gate ever let it through, the
// block would still be refused rather than applied.
func cmdForge(args []string) error {
	f := parseFlags(args)
	node := f.or("node", "http://127.0.0.1:8080")
	target := f.or("target", node)
	if err := adoptChainID(f); err != nil {
		return err
	}
	pub, priv, err := loadWallet(f)
	if err != nil {
		return err
	}
	addr := qcrypto.DeriveAddress(pub)

	var latest types.Block
	if err := getJSON(node+"/block/latest", &latest); err != nil {
		return fmt.Errorf("latest block: %w", err)
	}
	height := latest.Header.Height + 1

	merkle, err := types.ComputeTxMerkleRoot(nil)
	if err != nil {
		return err
	}
	b := &types.Block{Header: types.BlockHeader{
		Height:        height,
		Timestamp:     time.Now().Unix(),
		ChainID:       types.ChainID,
		AlgID:         qcrypto.AlgMLDSA65,
		PrevHash:      latest.Hash,
		TxMerkleRoot:  merkle,
		StateRoot:     [32]byte{0xde, 0xad, 0xbe, 0xef},
		ValidatorAddr: addr,
		TxCount:       0,
	}}
	hash, err := b.ComputeHash()
	if err != nil {
		return err
	}
	b.Hash = hash
	if err := b.SignBlock(priv); err != nil {
		return err
	}
	b.ValidatorPubKey = pub.Bytes()

	headers := map[string]string{}
	if tok := f.or("auth-token", ""); tok != "" {
		headers["X-Node-Auth"] = tok
	}
	status, body, err := postJSON(target+"/block", headers, b, nil)
	if err != nil {
		return err
	}
	fmt.Printf("forged block height=%d producer=%s target=%s\nHTTP %d %s\n",
		height, addr, target, status, body)
	if status == http.StatusOK {
		return fmt.Errorf("THE FORGED BLOCK WAS ACCEPTED — the producer gate did not hold")
	}
	return nil
}
