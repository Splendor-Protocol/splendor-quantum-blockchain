// bridge-admin — turnkey bring-up of a migration-bridge instance on a quantum
// node. It submits the two module-router TxCalls a hand-crafted bring-up would
// otherwise need signed by hand:
//
//	bridge-admin deploy \
//	  --node https://quantum-rpc.splendor.org --chain-id splendor-quantum-testnet-1 \
//	  --wallet owner.sqwallet --evm-chain-id 2691 --evm-contract 0x... \
//	  --splv-name "Splendor Vested" --splv-symbol SPLV \
//	  --splv-max-supply <dec> --max-claim-amount <dec> --threshold 1 --fee 2000
//
//	bridge-admin set-operator \
//	  --node ... --chain-id ... --wallet owner.sqwallet \
//	  --bridge SQ:... --operator SQ:... --active=true --fee 2000
//
// The wallet password comes from SQC_WALLET_PASS. The signing + submit path is
// the bridge-relayer's (cmd/bridge-relayer/quantum.go SubmitClaim): a TxCall to
// the module router carrying a JSON ModuleCall, signed with the loaded
// .sqwallet, POSTed to /tx/submit, with the public key attached only on nonce 0.
// deploy then reads the instance address back from the tx receipt. Nothing here
// runs in a validator; this is an off-chain signing client.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// maxBridgeOperators mirrors vm.MaxBridgeOperators (the threshold/operator cap).
// Hardcoded rather than imported so this tool stays as light as the relayer,
// which re-derives the router address offline precisely to avoid linking
// vm → core/state → leveldb.
const maxBridgeOperators = 16

// receiptTimeout bounds how long deploy waits for its instance address to be
// indexed. Matches sqc-deploy's default receipt poll timeout.
const receiptTimeout = 60 * time.Second

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "deploy":
		err = runDeploy(os.Args[2:])
	case "set-operator":
		err = runSetOperator(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "bridge-admin: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge-admin: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bridge-admin — bring up a migration-bridge instance on a quantum node.

usage:
  bridge-admin deploy       --node URL --chain-id ID --wallet FILE \
                            --evm-chain-id N --evm-contract 0x... \
                            --splv-name NAME --splv-symbol SYM \
                            --splv-max-supply DEC --max-claim-amount DEC \
                            --threshold N [--fee N]
  bridge-admin set-operator --node URL --chain-id ID --wallet FILE \
                            --bridge SQ:... --operator SQ:... --active=BOOL [--fee N]

The wallet password is read from SQC_WALLET_PASS. Run "bridge-admin deploy -h"
or "bridge-admin set-operator -h" for the per-subcommand flags.
`)
}

// ── deploy ──────────────────────────────────────────────────────────────────

func runDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	var (
		node       = fs.String("node", "", "quantum node RPC base URL (required)")
		chainID    = fs.String("chain-id", types.DefaultChainID, "quantum chain id the node must report")
		wallet     = fs.String("wallet", "", "owner .sqwallet path; password from SQC_WALLET_PASS (required)")
		evmChainID = fs.Uint64("evm-chain-id", 0, "legacy EVM chain id burns are accepted from (required, > 0)")
		evmC       = fs.String("evm-contract", "", "legacy burn contract address, 0x + 40 hex (required)")
		splvName   = fs.String("splv-name", "", "name of the module-managed SPLV token (required)")
		splvSymbol = fs.String("splv-symbol", "", "symbol of the module-managed SPLV token (required)")
		splvMax    = fs.String("splv-max-supply", "", "SPLV cap = legacy SPLV supply, decimal smallest unit (required, > 0)")
		maxClaim   = fs.String("max-claim-amount", "", "per-claim bound for both assets, decimal smallest unit (required, > 0)")
		threshold  = fs.Uint64("threshold", 0, "distinct attestations required per claim (required, 1..16)")
		fee        = fs.Int64("fee", 2000, "tx fee in smallest SPLD units")
	)
	fs.Usage = subUsage(fs, "deploy — deploy a new bridge instance; prints its SQ: address")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Validate client-side so an obvious mistake fails before it costs a fee.
	// These mirror the module's own checks (vm/bridge.go DeployBridge).
	evmContract, err := normalizeEvmContract(*evmC)
	if err != nil {
		return err
	}
	if *evmChainID == 0 {
		return fmt.Errorf("--evm-chain-id is required and must be > 0")
	}
	if strings.TrimSpace(*splvName) == "" {
		return fmt.Errorf("--splv-name is required")
	}
	if len(*splvName) > 64 {
		return fmt.Errorf("--splv-name is %d bytes, above the 64-byte cap", len(*splvName))
	}
	if strings.TrimSpace(*splvSymbol) == "" {
		return fmt.Errorf("--splv-symbol is required")
	}
	if len(*splvSymbol) > 64 {
		return fmt.Errorf("--splv-symbol is %d bytes, above the 64-byte cap", len(*splvSymbol))
	}
	if err := positiveUint256("--splv-max-supply", *splvMax); err != nil {
		return err
	}
	if err := positiveUint256("--max-claim-amount", *maxClaim); err != nil {
		return err
	}
	if *threshold < 1 {
		return fmt.Errorf("--threshold is required and must be at least 1")
	}
	if *threshold > maxBridgeOperators {
		return fmt.Errorf("--threshold %d exceeds the operator cap of %d", *threshold, maxBridgeOperators)
	}

	priv, from, err := loadWallet(*wallet)
	if err != nil {
		return err
	}
	cl, err := dialNode(*node, *chainID)
	if err != nil {
		return err
	}
	nonce, err := cl.Nonce(from)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	hash, err := cl.submit(priv, from, nonce, *fee, moduleCall{
		Module: "bridge", Action: "deploy", Params: bridgeDeployParams{
			EvmChainID:     *evmChainID,
			EvmContract:    evmContract,
			SplvName:       *splvName,
			SplvSymbol:     *splvSymbol,
			SplvMaxSupply:  *splvMax,
			MaxClaimAmount: *maxClaim,
			Threshold:      *threshold,
		},
	})
	if err != nil {
		return err
	}
	fmt.Printf("submitted bridge.deploy as %s: tx %s\n", from, hash)

	rv, err := cl.waitReceipt(hash, receiptTimeout)
	if err != nil {
		return err
	}
	if rv.Status == nil || *rv.Status != 1 {
		return fmt.Errorf("bridge.deploy reverted on-chain: %s", receiptReason(rv))
	}
	if rv.ContractAddress == nil || *rv.ContractAddress == "" {
		return fmt.Errorf("receipt carries no contract_address: deploy created no instance")
	}
	fmt.Printf("bridge instance: %s\n", *rv.ContractAddress)
	return nil
}

// ── set-operator ────────────────────────────────────────────────────────────

func runSetOperator(args []string) error {
	fs := flag.NewFlagSet("set-operator", flag.ContinueOnError)
	var (
		node     = fs.String("node", "", "quantum node RPC base URL (required)")
		chainID  = fs.String("chain-id", types.DefaultChainID, "quantum chain id the node must report")
		wallet   = fs.String("wallet", "", "owner .sqwallet path; password from SQC_WALLET_PASS (required)")
		bridge   = fs.String("bridge", "", "bridge instance SQ: address (required)")
		operator = fs.String("operator", "", "operator SQ: address to grant or revoke (required)")
		active   = fs.Bool("active", false, "true grants the right to submit claims, false revokes it")
		fee      = fs.Int64("fee", 2000, "tx fee in smallest SPLD units")
	)
	fs.Usage = subUsage(fs, "set-operator — grant or revoke a relayer's claim-submit right (owner-only)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Parse the SQ: addresses client-side so a typo fails before it costs a fee.
	bridgeAddr, err := qcrypto.AddressFromString(*bridge)
	if err != nil {
		return fmt.Errorf("--bridge: %w", err)
	}
	operatorAddr, err := qcrypto.AddressFromString(*operator)
	if err != nil {
		return fmt.Errorf("--operator: %w", err)
	}

	priv, from, err := loadWallet(*wallet)
	if err != nil {
		return err
	}
	cl, err := dialNode(*node, *chainID)
	if err != nil {
		return err
	}
	nonce, err := cl.Nonce(from)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	hash, err := cl.submit(priv, from, nonce, *fee, moduleCall{
		Module: "bridge", Action: "set_operator", Params: bridgeSetOperatorParams{
			Bridge:   bridgeAddr.String(),
			Operator: operatorAddr.String(),
			Active:   *active,
		},
	})
	if err != nil {
		return err
	}
	verb := "revoke"
	if *active {
		verb = "grant"
	}
	fmt.Printf("submitted bridge.set_operator (%s %s) as %s: tx %s\n", verb, operatorAddr, from, hash)

	// Best-effort confirmation. set_operator failures are SOFT (a failed
	// receipt, e.g. caller-not-owner), so surface them; a receipt that has not
	// indexed yet is not a failure — the tx was accepted.
	rv, err := cl.waitReceipt(hash, receiptTimeout)
	if err != nil {
		fmt.Printf("accepted; receipt not yet indexed (%v)\n", err)
		return nil
	}
	if rv.Status == nil || *rv.Status != 1 {
		return fmt.Errorf("bridge.set_operator reverted on-chain: %s", receiptReason(rv))
	}
	fmt.Printf("confirmed: %s is now %sed\n", operatorAddr, verb)
	return nil
}

// ── wire shapes (mirror vm/bridge.go exactly) ───────────────────────────────

// bridgeDeployParams is the wire shape of bridge.deploy (vm.bridgeDeployParams).
type bridgeDeployParams struct {
	EvmChainID     uint64 `json:"evm_chain_id"`
	EvmContract    string `json:"evm_contract"`
	SplvName       string `json:"splv_name"`
	SplvSymbol     string `json:"splv_symbol"`
	SplvMaxSupply  string `json:"splv_max_supply"`
	MaxClaimAmount string `json:"max_claim_amount"`
	Threshold      uint64 `json:"threshold"`
}

// bridgeSetOperatorParams is the set_operator subset of vm.bridgeActionParams.
// Active is a plain bool (no omitempty) so "active" is ALWAYS on the wire — the
// module refuses the call when the field is absent (a missing flag must not
// silently revoke; see dispatchBridge set_operator).
type bridgeSetOperatorParams struct {
	Bridge   string `json:"bridge"`
	Operator string `json:"operator"`
	Active   bool   `json:"active"`
}

// ── quantum-node client (mirrors cmd/bridge-relayer/quantum.go) ─────────────

// QuantumClient speaks the sqc-node REST API.
type QuantumClient struct {
	base   string
	client *http.Client
}

func NewQuantumClient(base string) *QuantumClient {
	return &QuantumClient{base: base, client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *QuantumClient) getJSON(path string, out any) error {
	resp, err := c.client.Get(c.base + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("GET %s: %s", path, e.Error)
		}
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Status returns the node's chain id and height.
func (c *QuantumClient) Status() (chainID string, height uint64, err error) {
	var s struct {
		ChainID string `json:"chain_id"`
		Height  uint64 `json:"height"`
	}
	if err := c.getJSON("/status", &s); err != nil {
		return "", 0, err
	}
	return s.ChainID, s.Height, nil
}

// Nonce returns the committed nonce of addr.
func (c *QuantumClient) Nonce(addr qcrypto.Address) (uint64, error) {
	var s struct {
		Nonce uint64 `json:"nonce"`
	}
	if err := c.getJSON("/nonce/"+addr.String(), &s); err != nil {
		return 0, err
	}
	return s.Nonce, nil
}

// moduleCall is the TxCall data envelope (mirrors vm.ModuleCall).
type moduleCall struct {
	Module string `json:"module"`
	Action string `json:"action"`
	Params any    `json:"params"`
}

// submit signs a module-router TxCall carrying mc and POSTs it to /tx/submit,
// returning the accepted tx hash. This is bridge-relayer/quantum.go's
// SubmitClaim path (types.NewCall → tx.Sign(priv, nonce==0) → POST /tx/submit →
// decode {status, hash, error}) generalised over the module action.
func (c *QuantumClient) submit(priv *qcrypto.PrivateKey, from qcrypto.Address, nonce uint64, fee int64, mc moduleCall) (string, error) {
	data, err := json.Marshal(mc)
	if err != nil {
		return "", err
	}
	tx := types.NewCall(from, moduleRouterAddress(), data, big.NewInt(0), big.NewInt(fee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, nonce == 0); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	body, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Post(c.base+"/tx/submit", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Status string `json:"status"`
		Hash   string `json:"hash"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("submit rejected: %s", out.Error)
	}
	if out.Status != "accepted" || out.Hash == "" {
		return "", fmt.Errorf("submit: unexpected response status %q", out.Status)
	}
	return out.Hash, nil
}

// receiptView reads the fields deploy/set-operator need off the raw receipt the
// node serves at /tx/{hash}/receipt (status, error and contract_address are all
// top-level there — makeReceiptHandler writes the receipt object directly).
type receiptView struct {
	Status          *int    `json:"status"`
	Error           string  `json:"error"`
	ContractAddress *string `json:"contract_address"`
}

// waitReceipt polls /tx/{hash}/receipt until the tx is indexed or timeout. A
// getJSON error (or a not-yet-populated status) means "not indexed yet" — keep
// polling — mirroring bridge-relayer's ReceiptStatus semantics.
func (c *QuantumClient) waitReceipt(hash string, timeout time.Duration) (*receiptView, error) {
	deadline := time.Now().Add(timeout)
	for {
		var rv receiptView
		if err := c.getJSON("/tx/"+hash+"/receipt", &rv); err == nil && rv.Status != nil {
			return &rv, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no receipt for %s after %s", hash, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// moduleRouterAddress reproduces vm.ModuleRouterAddress offline:
// SHA3-256("splendor:module-router:v1"). Copied from bridge-relayer/quantum.go
// so this tool need not link the vm package.
func moduleRouterAddress() qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor:module-router:v1"))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ── shared setup (mirrors bridge-relayer/main.go) ───────────────────────────

// loadWallet is the relayer's wallet-load path: SQC_WALLET_PASS unlocks the
// .sqwallet, and the sender address derives from the public key.
func loadWallet(path string) (*qcrypto.PrivateKey, qcrypto.Address, error) {
	if path == "" {
		return nil, qcrypto.Address{}, fmt.Errorf("--wallet is required")
	}
	pass := os.Getenv("SQC_WALLET_PASS")
	if pass == "" {
		return nil, qcrypto.Address{}, fmt.Errorf("SQC_WALLET_PASS is not set")
	}
	pub, priv, err := qcrypto.LoadWallet(path, []byte(pass))
	if err != nil {
		return nil, qcrypto.Address{}, fmt.Errorf("wallet: %w", err)
	}
	return priv, qcrypto.DeriveAddress(pub), nil
}

// dialNode adopts the chain id for signing (SetClientChainID, as the relayer
// does) and cross-checks it against the node's /status, so a testnet-signed tx
// can never be aimed at a mainnet node by mistake.
func dialNode(node, chainID string) (*QuantumClient, error) {
	if node == "" {
		return nil, fmt.Errorf("--node is required")
	}
	if err := types.SetClientChainID(chainID); err != nil {
		return nil, fmt.Errorf("--chain-id: %w", err)
	}
	cl := NewQuantumClient(strings.TrimRight(node, "/"))
	got, _, err := cl.Status()
	if err != nil {
		return nil, fmt.Errorf("node status: %w", err)
	}
	if got != chainID {
		return nil, fmt.Errorf("node reports chain id %q but --chain-id is %q", got, chainID)
	}
	return cl, nil
}

// ── validation helpers ──────────────────────────────────────────────────────

// normalizeEvmContract lowercases and validates a 0x + 40 hex EVM address. Like
// the relayer it lowercases first (so a checksummed address is accepted) and the
// module requires the lowercase spelling on the wire (vm.parseEvmHex).
func normalizeEvmContract(s string) (string, error) {
	c := strings.ToLower(s)
	if len(c) != 42 || !strings.HasPrefix(c, "0x") {
		return "", fmt.Errorf("--evm-contract: want 0x + 40 hex chars, got %q", s)
	}
	for i := 2; i < len(c); i++ {
		ch := c[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return "", fmt.Errorf("--evm-contract: %q is not hex", s)
		}
	}
	return c, nil
}

// positiveUint256 refuses anything the module would reject as a positive
// uint256 amount (vm.parseUint256 + the "> 0" checks): a non-empty run of
// digits, no leading zero, below 2^256, and not zero.
func positiveUint256(field, s string) error {
	if s == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(s) > 78 {
		return fmt.Errorf("%s %q is too long for a uint256", field, s)
	}
	if len(s) > 1 && s[0] == '0' {
		return fmt.Errorf("%s %q has a leading zero", field, s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("%s %q must be a decimal integer", field, s)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return fmt.Errorf("%s %q is not a valid decimal", field, s)
	}
	if v.Sign() <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	if v.BitLen() > 256 {
		return fmt.Errorf("%s %q does not fit in 256 bits", field, s)
	}
	return nil
}

// receiptReason gives the receipt's error text, or a stand-in when the node
// recorded a failure with no message.
func receiptReason(rv *receiptView) string {
	if rv.Error != "" {
		return rv.Error
	}
	return "(no reason recorded)"
}

// subUsage renders a subcommand's help: a one-line synopsis then its flags.
func subUsage(fs *flag.FlagSet, synopsis string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "%s\n\nflags:\n", synopsis)
		fs.PrintDefaults()
	}
}
