// bridge-relayer — minimal EVM JSON-RPC client.
//
// The legacy Splendor mainnet exposes only the eth/net/web3/txpool/privacy
// namespaces, so logs are the one indexing primitive. Three methods cover the
// whole need — eth_chainId, eth_blockNumber, eth_getLogs — and hand-rolling
// them keeps go-ethereum (a fork of which *is* the legacy chain) out of this
// module's dependency tree.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// burnedEventSig is the Solidity event the burn contract emits:
//
//	event Burned(uint256 indexed id, address indexed sender,
//	             uint8 indexed asset, uint256 amount, bytes32 quantumRecipient)
const burnedEventSig = "Burned(uint256,address,uint8,uint256,bytes32)"

// burnedTopic0 is keccak256(burnedEventSig) — the EVM event topic, computed
// once. (Keccak, not SHA3-256: this hash lives on the legacy chain.)
var burnedTopic0 = func() string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(burnedEventSig))
	return "0x" + fmt.Sprintf("%x", h.Sum(nil))
}()

// EVMClient speaks JSON-RPC 2.0 to one legacy-chain endpoint.
type EVMClient struct {
	url    string
	client *http.Client
}

func NewEVMClient(url string) *EVMClient {
	return &EVMClient{url: url, client: &http.Client{Timeout: 30 * time.Second}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *EVMClient) call(method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.client.Post(c.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	var r rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, r.Error.Code, r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

func parseHexQuantity(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") {
		return 0, fmt.Errorf("want a 0x hex quantity, got %q", s)
	}
	v, ok := new(big.Int).SetString(s[2:], 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("invalid hex quantity %q", s)
	}
	return v.Uint64(), nil
}

// ChainID returns eth_chainId as a decimal.
func (c *EVMClient) ChainID() (uint64, error) {
	var s string
	if err := c.call("eth_chainId", []any{}, &s); err != nil {
		return 0, err
	}
	return parseHexQuantity(s)
}

// BlockNumber returns the current head height.
func (c *EVMClient) BlockNumber() (uint64, error) {
	var s string
	if err := c.call("eth_blockNumber", []any{}, &s); err != nil {
		return 0, err
	}
	return parseHexQuantity(s)
}

// evmLog is the eth_getLogs entry shape (the fields this relayer reads).
type evmLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}

// BurnedLogs fetches the burn contract's Burned events in [from, to].
func (c *EVMClient) BurnedLogs(contract string, from, to uint64) ([]evmLog, error) {
	var logs []evmLog
	err := c.call("eth_getLogs", []any{map[string]any{
		"address":   contract,
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
		"topics":    []any{burnedTopic0},
	}}, &logs)
	return logs, err
}

// Burn is one decoded Burned event.
type Burn struct {
	ID        uint64
	Asset     string // quantum payout asset; migration credits native SPLD, so this is "spld" (SPLV burns folded in — see decodeBurn)
	Amount    *big.Int
	Recipient string // SQ: address
	TxHash    string
	Block     uint64
}

// decodeBurn turns one raw log into a Burn. Shape errors are the contract's
// problem, not ours — they are reported, never claimed.
func decodeBurn(l evmLog) (Burn, error) {
	if l.Removed {
		return Burn{}, fmt.Errorf("log is removed (reorg)")
	}
	if len(l.Topics) != 4 {
		return Burn{}, fmt.Errorf("want 4 topics, got %d", len(l.Topics))
	}
	if !strings.EqualFold(l.Topics[0], burnedTopic0) {
		return Burn{}, fmt.Errorf("topic0 %s is not Burned", l.Topics[0])
	}
	id, err := topicUint64(l.Topics[1])
	if err != nil {
		return Burn{}, fmt.Errorf("id: %w", err)
	}
	if id == 0 {
		return Burn{}, fmt.Errorf("id 0 (burn ids start at 1)")
	}
	assetWord, err := topicUint64(l.Topics[3])
	if err != nil {
		return Burn{}, fmt.Errorf("asset: %w", err)
	}
	var asset string
	switch assetWord {
	case 0:
		asset = "spld"
	case 1:
		// Migration policy: an SPLV burn is credited as native SPLD 1:1 on
		// quantum (the QRC-20 SPLV leg was retired). The 2691 Burned event
		// still records asset=1, so SPLV provenance is preserved on the legacy
		// chain and recoverable via the burn id. Downstream the attestation
		// digest and the claim both read Burn.Asset, so paying SPLD stays
		// internally consistent.
		asset = "spld"
	default:
		return Burn{}, fmt.Errorf("unknown asset word %d", assetWord)
	}
	data := strings.TrimPrefix(l.Data, "0x")
	if len(data) != 128 {
		return Burn{}, fmt.Errorf("want 64 data bytes, got %d hex chars", len(data))
	}
	amount, ok := new(big.Int).SetString(data[:64], 16)
	if !ok {
		return Burn{}, fmt.Errorf("invalid amount word %q", data[:64])
	}
	if amount.Sign() <= 0 {
		return Burn{}, fmt.Errorf("non-positive amount")
	}
	recipient := strings.ToLower(data[64:])
	if recipient == strings.Repeat("0", 64) {
		return Burn{}, fmt.Errorf("zero quantum recipient")
	}
	block, err := parseHexQuantity(l.BlockNumber)
	if err != nil {
		return Burn{}, fmt.Errorf("blockNumber: %w", err)
	}
	txHash := strings.ToLower(l.TxHash)
	if len(txHash) != 66 || !strings.HasPrefix(txHash, "0x") {
		return Burn{}, fmt.Errorf("invalid tx hash %q", l.TxHash)
	}
	return Burn{
		ID: id, Asset: asset, Amount: amount,
		Recipient: "SQ:" + recipient, TxHash: txHash, Block: block,
	}, nil
}

// topicUint64 decodes a 32-byte topic word that must fit a uint64.
func topicUint64(s string) (uint64, error) {
	h := strings.TrimPrefix(strings.ToLower(s), "0x")
	if len(h) != 64 {
		return 0, fmt.Errorf("want a 32-byte topic, got %q", s)
	}
	v, ok := new(big.Int).SetString(h, 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("topic %q does not fit a uint64", s)
	}
	return v.Uint64(), nil
}
