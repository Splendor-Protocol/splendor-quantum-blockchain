package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// EVMConfig wires the legacy-EVM side. It is deliberately generic: any EVM
// chain exposing a Burned-emitting escrow (and, for the reverse direction, a
// release contract) plugs in unchanged.
type EVMConfig struct {
	RPCURL            string
	ChainIDValue      uint64
	BurnContract      string
	TokenAddresses    map[Asset]string
	ConfirmDepthValue uint64
	// Release submits the reverse-direction release on this chain. Pluggable
	// because it depends on the deployed release contract; until one exists the
	// reverse direction holds rather than fabricating a call.
	Release func(ctx context.Context, e OutboundEvent) (string, error)
}

const burnedEventSig = "Burned(uint256,address,uint8,uint256,bytes32)"

var burnedTopic0 = "0x" + fmt.Sprintf("%x", sha3.NewLegacyKeccak256().Sum([]byte(burnedEventSig)))

// EVMChain adapts an EVM chain to the bridge.Chain interface.
type EVMChain struct {
	cfg    EVMConfig
	client *http.Client
}

func NewEVMChain(cfg EVMConfig) *EVMChain {
	if cfg.ConfirmDepthValue == 0 {
		cfg.ConfirmDepthValue = 120
	}
	return &EVMChain{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *EVMChain) Name() string    { return "legacy" }
func (c *EVMChain) ChainID() string { return fmt.Sprintf("%d", c.cfg.ChainIDValue) }
func (c *EVMChain) ConfirmDepth() uint64 {
	return c.cfg.ConfirmDepthValue
}

func (c *EVMChain) Assets() map[Asset]AssetInfo {
	out := map[Asset]AssetInfo{AssetNative: {Symbol: "SPLD", Wrapped: false}}
	if _, ok := c.cfg.TokenAddresses[AssetSPLV]; ok {
		out[AssetSPLV] = AssetInfo{Symbol: "SPLV", Wrapped: false}
	}
	return out
}

func (c *EVMChain) call(method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	resp, err := c.client.Post(c.cfg.RPCURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	var r struct {
		Result json.RawMessage           `json:"result"`
		Error  *struct{ Message string } `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: %s", method, r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

func hexQuantity(s string) (uint64, error) {
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("invalid hex quantity %q", s)
	}
	return v.Uint64(), nil
}

func (c *EVMChain) Height(ctx context.Context) (uint64, error) {
	var s string
	if err := c.call("eth_blockNumber", []any{}, &s); err != nil {
		return 0, err
	}
	return hexQuantity(s)
}

func (c *EVMChain) Balance(ctx context.Context, owner string, asset Asset) (*big.Int, error) {
	if asset == AssetSPLV {
		tok, ok := c.cfg.TokenAddresses[asset]
		if !ok {
			return nil, fmt.Errorf("legacy: no token contract for %s", asset)
		}
		return c.tokenBalance(owner, tok)
	}
	var s string
	if err := c.call("eth_getBalance", []any{owner, "latest"}, &s); err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid balance %q", s)
	}
	return v, nil
}

func (c *EVMChain) tokenBalance(owner, contract string) (*big.Int, error) {
	sel := fmt.Sprintf("%x", sha3.NewLegacyKeccak256().Sum([]byte("balanceOf(address)")))[:8]
	addr := strings.TrimPrefix(strings.ToLower(owner), "0x")
	if len(addr) != 40 {
		return nil, fmt.Errorf("invalid EVM address %q", owner)
	}
	var s string
	err := c.call("eth_call", []any{map[string]string{"to": contract, "data": "0x" + sel + fmt.Sprintf("%064s", addr)}, "latest"}, &s)
	if err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid token balance %q", s)
	}
	return v, nil
}

type evmLog struct {
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	TxHash      string   `json:"txHash"`
	Removed     bool     `json:"removed"`
}

// Outbound returns the escrow contract's Burned events in [from, to].
func (c *EVMChain) Outbound(ctx context.Context, from, to uint64) ([]OutboundEvent, error) {
	if c.cfg.BurnContract == "" {
		return nil, nil
	}
	var logs []evmLog
	err := c.call("eth_getLogs", []any{map[string]any{
		"address":   c.cfg.BurnContract,
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
		"topics":    []string{burnedTopic0},
	}}, &logs)
	if err != nil {
		return nil, err
	}
	out := make([]OutboundEvent, 0, len(logs))
	for _, l := range logs {
		e, err := decodeBurned(l)
		if err != nil {
			return nil, err // fail closed: an undecodable log holds the cursor
		}
		out = append(out, e)
	}
	return out, nil
}

func topicUint64(s string) (uint64, error) {
	v, ok := new(big.Int).SetString(strings.TrimPrefix(strings.ToLower(s), "0x"), 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("topic %q does not fit a uint64", s)
	}
	return v.Uint64(), nil
}

// decodeBurned decodes one Burned log into an OutboundEvent.
func decodeBurned(l evmLog) (OutboundEvent, error) {
	if l.Removed {
		return OutboundEvent{}, fmt.Errorf("log removed (reorg)")
	}
	if len(l.Topics) != 4 || !strings.EqualFold(l.Topics[0], burnedTopic0) {
		return OutboundEvent{}, fmt.Errorf("not a Burned log")
	}
	id, err := topicUint64(l.Topics[1])
	if err != nil || id == 0 {
		return OutboundEvent{}, fmt.Errorf("bad burn id")
	}
	assetWord, err := topicUint64(l.Topics[3])
	if err != nil {
		return OutboundEvent{}, fmt.Errorf("asset: %w", err)
	}
	asset := AssetNative
	switch assetWord {
	case 0:
		asset = AssetNative
	case 1:
		asset = AssetSPLV
	default:
		return OutboundEvent{}, fmt.Errorf("unknown asset word %d", assetWord)
	}
	data := strings.TrimPrefix(l.Data, "0x")
	if len(data) != 128 {
		return OutboundEvent{}, fmt.Errorf("want 64 data bytes, got %d hex chars", len(data))
	}
	amount, ok := new(big.Int).SetString(data[:64], 16)
	if !ok || amount.Sign() <= 0 {
		return OutboundEvent{}, fmt.Errorf("bad amount word")
	}
	recipient := strings.ToLower(data[64:])
	if recipient == strings.Repeat("0", 64) {
		return OutboundEvent{}, fmt.Errorf("zero quantum recipient")
	}
	block, err := hexQuantity(l.BlockNumber)
	if err != nil {
		return OutboundEvent{}, err
	}
	return OutboundEvent{
		ID:        fmt.Sprintf("%d", id),
		Chain:     "legacy",
		Asset:     asset,
		Amount:    amount,
		Recipient: "SQ:" + recipient,
		Height:    block,
		TxID:      strings.ToLower(l.TxHash),
	}, nil
}

// Settle releases value on the EVM side for a reverse-direction source event,
// delegated to the injected Release func. Until a release contract is deployed
// the reverse direction holds (fails closed).
func (c *EVMChain) Settle(ctx context.Context, e OutboundEvent) (string, error) {
	if c.cfg.Release == nil {
		return "", fmt.Errorf("legacy: no release contract wired (reverse direction not yet deployable)")
	}
	return c.cfg.Release(ctx, e)
}

// Settled reports whether e.ID is already released on the EVM side. Without a
// release contract it reports false, so the reverse direction holds.
func (c *EVMChain) Settled(ctx context.Context, id string) (bool, error) {
	return false, nil
}
