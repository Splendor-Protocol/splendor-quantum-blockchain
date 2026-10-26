package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// keccak256 returns the 32-byte Keccak-256 digest (Ethereum's legacy Keccak,
// not FIPS SHA3-256) of s as lowercase hex. hash.Hash.Sum appends the current
// state to its argument rather than hashing it, so this must Write-then-Sum.
func keccak256(s string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

var burnedTopic0 = "0x" + keccak256(burnedEventSig)

// MaxOutboundBlocks caps how many source blocks one Outbound call may scan.
// When the relayer cursor falls far behind (restart, reorg, outage) an
// unbounded [from, to] range would ask the legacy RPC for millions of logs at
// once — a runaway query that can OOM the node or time out the RPC. Cap it so
// the relayer simply does multiple passes instead of one giant one. A var so
// tests can shrink it.
var MaxOutboundBlocks uint64 = 100_000

// EVMChain adapts an EVM chain to the bridge.Chain interface.
type EVMChain struct {
	cfg     EVMConfig
	client  *http.Client
	circuit *CircuitBreaker
	limiter *RateLimiter
	reorg   *ReorgDetector
}

// NewEVMChain adapts an EVM chain to the bridge.Chain interface, wrapping every
// upstream call in a circuit breaker, rate limiter, and reorg detector.
//
// Defaults: circuit opens after 5 consecutive failures and stays open for 30s;
// rate limiter allows 10 calls/s with a burst of 20; reorg detector remembers
// the last 100 observed heights.
func NewEVMChain(cfg EVMConfig) *EVMChain {
	if cfg.ConfirmDepthValue == 0 {
		cfg.ConfirmDepthValue = 120
	}
	return &EVMChain{
		cfg:     cfg,
		client:  &http.Client{Timeout: 30 * time.Second},
		circuit: NewCircuitBreaker("legacy-evm", 5, 30*time.Second),
		limiter: NewRateLimiter(10, 20),
		reorg:   NewReorgDetector(100),
	}
}

// withHardening applies rate limiting and circuit breaking to one upstream
// call. Rate limiting is checked first (cheap, no lock on the fast path), then
// the circuit breaker. A rejected call returns immediately without touching the
// chain.
func (c *EVMChain) withHardening(f func() error) error {
	if !c.limiter.Allow() {
		return fmt.Errorf("legacy: rate limited")
	}
	return c.circuit.Call(f)
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

// maxRPCResponseBytes caps one JSON-RPC/REST response body (64 MiB): the
// endpoints are inside the operator trust boundary, but a misbehaving
// endpoint must fail the call, not balloon memory. A var (not a const) so
// tests can shrink it.
var maxRPCResponseBytes = 64 << 20

func (c *EVMChain) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("%s: marshal request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.RPCURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxRPCResponseBytes)+1))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if len(raw) > maxRPCResponseBytes {
		return fmt.Errorf("%s: response exceeds %d bytes", method, maxRPCResponseBytes)
	}
	var r struct {
		Result json.RawMessage           `json:"result"`
		Error  *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: %s", method, r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

func hexQuantity(s string) (uint64, error) {
	v, ok := new(big.Int).SetString(strings.TrimPrefix(strings.ToLower(s), "0x"), 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("invalid hex quantity %q", s)
	}
	return v.Uint64(), nil
}

func (c *EVMChain) Height(ctx context.Context) (uint64, error) {
	var height uint64
	err := c.withHardening(func() error {
		var s string
		if err := c.call(ctx, "eth_blockNumber", []any{}, &s); err != nil {
			return err
		}
		h, err := hexQuantity(s)
		if err != nil {
			return err
		}
		height = h
		return nil
	})
	if err != nil {
		return 0, err
	}
	// Reorg detection after a successful head read: a backward-moving tip is a
	// reorg in flight. Return the error so the relayer pauses rather than
	// settling against a chain that may be reorged out. This is not counted as
	// a circuit-breaker failure — the RPC is working, the chain is just
	// unsettled.
	if err := c.reorg.Observe(height, ""); err != nil {
		return 0, err
	}
	return height, nil
}

func (c *EVMChain) Balance(ctx context.Context, owner string, asset Asset) (*big.Int, error) {
	if err := ValidateEVMAddress(owner); err != nil {
		return nil, err
	}
	if err := ValidateAsset(asset); err != nil {
		return nil, err
	}
	if asset == AssetSPLV {
		tok, ok := c.cfg.TokenAddresses[asset]
		if !ok {
			return nil, fmt.Errorf("legacy: no token contract for %s", asset)
		}
		var bal *big.Int
		err := c.withHardening(func() error {
			var err error
			bal, err = c.tokenBalance(ctx, owner, tok)
			return err
		})
		if err != nil {
			return nil, err
		}
		return bal, nil
	}
	var s string
	err := c.withHardening(func() error {
		return c.call(ctx, "eth_getBalance", []any{owner, "latest"}, &s)
	})
	if err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid balance %q", s)
	}
	return v, nil
}

func (c *EVMChain) tokenBalance(ctx context.Context, owner, contract string) (*big.Int, error) {
	sel := keccak256("balanceOf(address)")[:8]
	addr := strings.TrimPrefix(strings.ToLower(owner), "0x")
	if len(addr) != 40 {
		return nil, fmt.Errorf("invalid EVM address %q", owner)
	}
	var s string
	err := c.call(ctx, "eth_call", []any{map[string]string{"to": contract, "data": "0x" + sel + fmt.Sprintf("%064s", addr)}, "latest"}, &s)
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

// Outbound returns the escrow contract's Burned events in [from, to], capped
// at MaxOutboundBlocks blocks per call. If the caller asks for more, we scan
// [from, from+MaxOutboundBlocks-1] and let the relayer continue on the next
// pass — one bounded RPC per pass instead of one runaway query.
func (c *EVMChain) Outbound(ctx context.Context, from, to uint64) ([]OutboundEvent, error) {
	if c.cfg.BurnContract == "" {
		return nil, nil
	}
	if to-from+1 > MaxOutboundBlocks {
		to = from + MaxOutboundBlocks - 1
	}
	var logs []evmLog
	err := c.withHardening(func() error {
		return c.call(ctx, "eth_getLogs", []any{map[string]any{
			"address":   c.cfg.BurnContract,
			"fromBlock": fmt.Sprintf("0x%x", from),
			"toBlock":   fmt.Sprintf("0x%x", to),
			"topics":    []string{burnedTopic0},
		}}, &logs)
	})
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

// topicAddress decodes an indexed address topic (a 32-byte word with the
// 20-byte address in its low bytes) into a lowercase 0x-hex address.
func topicAddress(s string) (string, error) {
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if len(s) != 64 {
		return "", fmt.Errorf("address topic has %d hex chars, want 64", len(s))
	}
	return "0x" + s[24:], nil
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
	sender, err := topicAddress(l.Topics[2])
	if err != nil {
		return OutboundEvent{}, fmt.Errorf("sender: %w", err)
	}
	if sender == "0x"+strings.Repeat("0", 40) {
		return OutboundEvent{}, fmt.Errorf("zero sender")
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
		Sender:    sender,
		Recipient: "SQ:" + recipient,
		Height:    block,
		TxID:      strings.ToLower(l.TxHash),
	}, nil
}

// Settle releases value on the EVM side for a reverse-direction source event,
// delegated to the injected Release func. Until a release contract is deployed
// the reverse direction holds (fails closed).
func (c *EVMChain) Settle(ctx context.Context, e OutboundEvent) (string, error) {
	if err := ValidateEVMAddress(e.Sender); err != nil {
		return "", err
	}
	if err := ValidateSQAddress(e.Recipient); err != nil {
		return "", err
	}
	if err := ValidateAsset(e.Asset); err != nil {
		return "", err
	}
	if err := ValidateAmount(e.Amount); err != nil {
		return "", err
	}
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
