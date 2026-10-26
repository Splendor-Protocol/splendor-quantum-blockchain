// sqc-deploy drives QSol contracts against a LIVE node over its public RPC:
// deploy compiled bytecode, send calls, run read-only view calls, and verify
// that an address's on-chain code matches a source file.
//
//	sqc-deploy -rpc URL -seed FILE deploy <source.qsol>
//	sqc-deploy -rpc URL -seed FILE call   <address> <source.qsol> <fn> [arg...]
//	sqc-deploy -rpc URL view   <address> <source.qsol> <fn> [arg...]
//	sqc-deploy -rpc URL verify <address> <source.qsol>
//
// The seed file holds a 32-byte ML-DSA-65 seed as 64 hex chars (or 32 raw
// bytes). It is read, never printed. Arguments are decimal, 0x-hex, or SQ:
// addresses. The chain id is taken from the node's /status, so a tx is always
// signed for the tier it is sent to.
//
// verify is the source-verification primitive the explorer flow needs:
// Compile() is deterministic (same source → byte-identical bytecode, proven by
// TestCompileDeterministic), so byte-equality against /contract/{addr}/code is
// a sound proof that the source matches the deployed contract.
//
// This tool is off-chain: nothing here runs in a validator.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"

	"github.com/splendor-protocol/splendor-quantum-blockchain/compiler"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:8545", "node RPC base URL")
	seedPath := flag.String("seed", "", "path to ML-DSA-65 seed file (64 hex chars or 32 raw bytes)")
	fee := flag.Int64("fee", 100_000, "tx fee in smallest units")
	value := flag.String("value", "0", "call value (decimal)")
	timeout := flag.Duration("timeout", 60*time.Second, "receipt poll timeout")
	fleetCount := flag.Int("fleet-count", 250, "fleet: how many contracts to generate and deploy")
	fleetSeed := flag.Int64("fleet-seed", 1, "fleet: generator seed (same seed → same contracts)")
	fleetBatch := flag.Int("fleet-batch", 32, "fleet: submissions between chain catch-up checks (stay under the mempool's per-sender cap)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage:
  sqc-deploy -rpc URL -seed FILE deploy <source.qsol>
  sqc-deploy -rpc URL -seed FILE call   <address> <source.qsol> <fn> [arg...]
  sqc-deploy -rpc URL view   <address> <source.qsol> <fn> [arg...]
  sqc-deploy -rpc URL verify <address> <source.qsol>
  sqc-deploy -rpc URL -seed FILE fleet [-fleet-count N] [-fleet-seed S]
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	cl := &client{base: strings.TrimRight(*rpcURL, "/"), http: &http.Client{Timeout: 20 * time.Second}}

	var err error
	switch flag.Arg(0) {
	case "deploy":
		err = runDeploy(cl, *seedPath, flag.Args()[1:], *fee, *timeout)
	case "call":
		err = runCall(cl, *seedPath, flag.Args()[1:], *fee, *value, *timeout)
	case "view":
		err = runView(cl, flag.Args()[1:])
	case "verify":
		err = runVerify(cl, flag.Args()[1:])
	case "fleet":
		err = runFleet(cl, *seedPath, *fleetSeed, *fleetCount, *fleetBatch, *timeout)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqc-deploy: %v\n", err)
		os.Exit(1)
	}
}

// ── key handling ──────────────────────────────────────────────────────────────

// loadKey derives the ML-DSA-65 keypair from a seed file. The seed is secret:
// it is read and used, never logged or echoed.
func loadKey(path string) (*qcrypto.PublicKey, *qcrypto.PrivateKey, qcrypto.Address, error) {
	if path == "" {
		return nil, nil, qcrypto.Address{}, fmt.Errorf("this subcommand needs -seed")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, qcrypto.Address{}, err
	}
	trimmed := strings.TrimSpace(string(raw))
	var seed [32]byte
	switch {
	case len(trimmed) == 64:
		b, err := hex.DecodeString(trimmed)
		if err != nil {
			return nil, nil, qcrypto.Address{}, fmt.Errorf("seed file: %v", err)
		}
		copy(seed[:], b)
	case len(raw) == 32:
		copy(seed[:], raw)
	default:
		return nil, nil, qcrypto.Address{}, fmt.Errorf("seed file must be 64 hex chars or 32 raw bytes")
	}
	cpub, cpriv := mldsa65.NewKeyFromSeed(&seed)
	pubBytes, _ := cpub.MarshalBinary()
	privBytes, _ := cpriv.MarshalBinary()
	pub, err := qcrypto.PublicKeyFromBytes(pubBytes)
	if err != nil {
		return nil, nil, qcrypto.Address{}, err
	}
	priv, err := qcrypto.PrivateKeyFromBytes(privBytes)
	if err != nil {
		return nil, nil, qcrypto.Address{}, err
	}
	return pub, priv, qcrypto.DeriveAddress(pub), nil
}

// ── subcommands ───────────────────────────────────────────────────────────────

func runDeploy(cl *client, seedPath string, args []string, fee int64, timeout time.Duration) error {
	if len(args) != 1 {
		return fmt.Errorf("deploy wants: <source.qsol>")
	}
	c, err := compileFile(args[0])
	if err != nil {
		return err
	}
	_, priv, from, err := loadKey(seedPath)
	if err != nil {
		return err
	}
	if err := cl.adoptChainID(); err != nil {
		return err
	}
	nonce, err := cl.nonce(from)
	if err != nil {
		return err
	}
	tx := types.NewDeploy(from, c.Bytecode, big.NewInt(fee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		return err
	}
	hash, err := cl.submit(tx)
	if err != nil {
		return err
	}
	predicted := deployAddr(from, nonce)
	fmt.Printf("submitted deploy: tx %s\n", hash)
	fmt.Printf("contract address: %s\n", predicted)
	receipt, err := cl.waitReceipt(hash, timeout)
	if err != nil {
		return err
	}
	printJSON(receipt)
	return nil
}

func runCall(cl *client, seedPath string, args []string, fee int64, value string, timeout time.Duration) error {
	if len(args) < 3 {
		return fmt.Errorf("call wants: <address> <source.qsol> <fn> [arg...]")
	}
	to, err := qcrypto.AddressFromString(args[0])
	if err != nil {
		return err
	}
	data, err := encodeFromSource(args[1], args[2], args[3:])
	if err != nil {
		return err
	}
	_, priv, from, err := loadKey(seedPath)
	if err != nil {
		return err
	}
	if err := cl.adoptChainID(); err != nil {
		return err
	}
	nonce, err := cl.nonce(from)
	if err != nil {
		return err
	}
	val, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return fmt.Errorf("bad -value %q", value)
	}
	tx := types.NewCall(from, to, data, val, big.NewInt(fee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		return err
	}
	hash, err := cl.submit(tx)
	if err != nil {
		return err
	}
	fmt.Printf("submitted call: tx %s\n", hash)
	receipt, err := cl.waitReceipt(hash, timeout)
	if err != nil {
		return err
	}
	printJSON(receipt)
	return nil
}

func runView(cl *client, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("view wants: <address> <source.qsol> <fn> [arg...]")
	}
	data, err := encodeFromSource(args[1], args[2], args[3:])
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{
		"to":   args[0],
		"data": hex.EncodeToString(data),
	})
	var out struct {
		ReturnData   string `json:"return_data"`
		GasUsed      uint64 `json:"gas_used"`
		Reverted     bool   `json:"reverted"`
		RevertReason string `json:"revert_reason"`
	}
	if err := cl.postJSON("/call", body, &out); err != nil {
		return err
	}
	if out.Reverted {
		fmt.Printf("REVERTED: %s (gas %d)\n", out.RevertReason, out.GasUsed)
		return nil
	}
	ret, err := hex.DecodeString(out.ReturnData)
	if err != nil {
		return fmt.Errorf("bad return_data: %v", err)
	}
	fmt.Printf("returned: %s (0x%s, gas %d)\n", new(big.Int).SetBytes(ret), out.ReturnData, out.GasUsed)
	return nil
}

func runVerify(cl *client, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("verify wants: <address> <source.qsol>")
	}
	c, err := compileFile(args[1])
	if err != nil {
		return err
	}
	var out struct {
		Code     string `json:"code"`
		CodeHash string `json:"code_hash"`
		Native   bool   `json:"native_module"`
	}
	if err := cl.getJSON("/contract/"+args[0]+"/code", &out); err != nil {
		return err
	}
	if out.Native {
		return fmt.Errorf("%s is a native module: it has no bytecode to verify", args[0])
	}
	onchain, err := hex.DecodeString(out.Code)
	if err != nil {
		return fmt.Errorf("bad on-chain code hex: %v", err)
	}
	if !bytes.Equal(onchain, c.Bytecode) {
		fmt.Printf("MISMATCH: on-chain %d bytes (hash %s), source compiles to %d bytes\n",
			len(onchain), out.CodeHash, len(c.Bytecode))
		os.Exit(1)
	}
	fmt.Printf("VERIFIED: %s matches %s (%d bytes, code hash %s)\n", args[0], args[1], len(onchain), out.CodeHash)
	fmt.Println("functions:")
	for _, f := range c.Functions {
		fmt.Printf("  %-40s selector 0x%x\n", f.Signature, f.Selector)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func compileFile(path string) (*compiler.Contract, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := compiler.Compile(string(src))
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", path, err)
	}
	return c, nil
}

// encodeFromSource compiles the source for its ABI and encodes fn(args).
func encodeFromSource(srcPath, fn string, rawArgs []string) ([]byte, error) {
	c, err := compileFile(srcPath)
	if err != nil {
		return nil, err
	}
	for _, f := range c.Functions {
		if f.Name != fn {
			continue
		}
		args := make([]*big.Int, len(rawArgs))
		for i, ra := range rawArgs {
			v, err := parseArg(ra)
			if err != nil {
				return nil, fmt.Errorf("arg %d: %w", i, err)
			}
			args[i] = v
		}
		return compiler.EncodeCall(f, args...)
	}
	return nil, fmt.Errorf("no function %q in %s", fn, srcPath)
}

func parseArg(s string) (*big.Int, error) {
	if strings.HasPrefix(s, qcrypto.AddressPrefix) {
		a, err := qcrypto.AddressFromString(s)
		if err != nil {
			return nil, err
		}
		return compiler.EncodeAddressArg(a), nil
	}
	v, ok := new(big.Int).SetString(s, 0)
	if !ok {
		return nil, fmt.Errorf("not a number or SQ: address: %q", s)
	}
	return v, nil
}

func deployAddr(from qcrypto.Address, nonce uint64) qcrypto.Address {
	nb := make([]byte, 8)
	binary.BigEndian.PutUint64(nb, nonce)
	h := qcrypto.Hash32(append(from[:], nb...))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

// ── minimal RPC client ────────────────────────────────────────────────────────

type client struct {
	base string
	http *http.Client
}

func (c *client) getJSON(path string, out any) error {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read GET %s body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func (c *client) postJSON(path string, body []byte, out any) error {
	resp, err := c.http.Post(c.base+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read POST %s body: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: %s: %s", path, resp.Status, strings.TrimSpace(string(rb)))
	}
	return json.Unmarshal(rb, out)
}

// adoptChainID reads the node's chain id so the signed body carries the id of
// the tier the tx is sent to (devnet/testnet/mainnet each have their own).
func (c *client) adoptChainID() error {
	var st struct {
		ChainID string `json:"chain_id"`
	}
	if err := c.getJSON("/status", &st); err != nil {
		return fmt.Errorf("read /status: %w", err)
	}
	if st.ChainID == "" {
		return fmt.Errorf("node reported no chain_id")
	}
	if st.ChainID != types.ChainID {
		if err := types.SetChainID(st.ChainID); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) nonce(addr qcrypto.Address) (uint64, error) {
	var out struct {
		Nonce uint64 `json:"nonce"`
	}
	if err := c.getJSON("/nonce/"+addr.String(), &out); err != nil {
		return 0, err
	}
	return out.Nonce, nil
}

func (c *client) submit(tx *types.Transaction) (string, error) {
	body, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}
	var out struct {
		Hash string `json:"hash"`
	}
	if err := c.postJSON("/tx/submit", body, &out); err != nil {
		return "", err
	}
	return out.Hash, nil
}

// waitReceipt polls the receipt endpoint until the tx is indexed. A tx a
// producer DROPS (e.g. a reverting call on pre-vm_revert_v1 rules) never gets
// a receipt: the timeout is the signal, and the error text says so.
func (c *client) waitReceipt(hash string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var receipt map[string]any
		if err := c.getJSON("/tx/"+hash+"/receipt", &receipt); err == nil {
			return receipt, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("no receipt for %s after %s — on pre-vm_revert_v1 chains a reverting call is dropped by the producer and never lands", hash, timeout)
}
