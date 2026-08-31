// bridge-relayer — quantum-node client: status, nonce, claim status, claim
// submission and receipt polling over the node's REST API.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

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

// BridgeInfo is the /bridge/{addr} read this relayer consumes.
type BridgeInfo struct {
	Owner       string   `json:"owner"`
	Operators   []string `json:"operators"`
	Paused      bool     `json:"paused"`
	Threshold   uint64   `json:"threshold"`
	EvmChainID  uint64   `json:"evm_chain_id"`
	EvmContract string   `json:"evm_contract"`
	PoolBalance string   `json:"pool_balance"`
}

// Bridge returns the bridge instance parameters.
func (c *QuantumClient) Bridge(bridge string) (*BridgeInfo, error) {
	var out struct {
		Info BridgeInfo `json:"info"`
	}
	if err := c.getJSON("/bridge/"+bridge, &out); err != nil {
		return nil, err
	}
	return &out.Info, nil
}

// Claimed reports whether burn id is already claimed on the bridge.
func (c *QuantumClient) Claimed(bridge string, id uint64) (bool, error) {
	var out struct {
		Claimed bool `json:"claimed"`
	}
	if err := c.getJSON(fmt.Sprintf("/bridge/%s/claim/%d", bridge, id), &out); err != nil {
		return false, err
	}
	return out.Claimed, nil
}

// moduleCall is the TxCall data envelope (mirrors vm.ModuleCall).
type moduleCall struct {
	Module string `json:"module"`
	Action string `json:"action"`
	Params any    `json:"params"`
}

// attestation mirrors vm.bridgeAttestation: an attestor's pubkey and its
// signature over types.BridgeClaimDigest under CtxBridge. Go []byte, so
// standard base64 on the wire.
type attestation struct {
	PubKey    []byte `json:"pubkey"`
	Signature []byte `json:"signature"`
}

// claimParams is the wire shape of bridge.claim (mirrors vm.bridgeClaimParams).
type claimParams struct {
	Bridge       string        `json:"bridge"`
	ID           uint64        `json:"id"`
	Asset        string        `json:"asset"`
	Amount       string        `json:"amount"`
	Recipient    string        `json:"recipient"`
	EvmTx        string        `json:"evm_tx"`
	Attestations []attestation `json:"attestations"`
}

// SubmitClaim signs and submits one bridge.claim as a TxCall from the operator,
// carrying the gathered attestations, returning the accepted tx hash.
func (c *QuantumClient) SubmitClaim(
	priv *qcrypto.PrivateKey, from qcrypto.Address, nonce uint64, fee int64,
	bridge qcrypto.Address, b Burn, atts []attestation,
) (string, error) {
	data, err := json.Marshal(moduleCall{Module: "bridge", Action: "claim", Params: claimParams{
		Bridge: bridge.String(), ID: b.ID, Asset: b.Asset, Amount: b.Amount.String(),
		Recipient: b.Recipient, EvmTx: b.TxHash, Attestations: atts,
	}})
	if err != nil {
		return "", err
	}
	routerAddr := moduleRouterAddress()
	tx := types.NewCall(from, routerAddr, data, big.NewInt(0), big.NewInt(fee), nonce, time.Now().Unix())
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

// moduleRouterAddress reproduces vm.ModuleRouterAddress offline:
// SHA3-256("splendor:module-router:v1").
func moduleRouterAddress() qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor:module-router:v1"))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ReceiptStatus polls /tx/{hash}/receipt once: -1 not found yet, 0 failed
// (with the module's error text), 1 success.
//
// The endpoint returns the raw receipt, so `status` and `error` are BOTH
// top-level (rpc/explorer.go serves the receipt object directly;
// core/types/receipt.go tags them `status` and `error`). Reading `error` from
// a nested "receipt" key — the shape GET /tx/{hash} produces — would silently
// yield an empty reason on every reverted claim.
func (c *QuantumClient) ReceiptStatus(hash string) (int, string, error) {
	var out struct {
		Status *int   `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.getJSON("/tx/"+hash+"/receipt", &out); err != nil {
		return -1, "", nil // not indexed yet (or transient) — poll again
	}
	if out.Status == nil {
		return -1, "", nil
	}
	return *out.Status, out.Error, nil
}
