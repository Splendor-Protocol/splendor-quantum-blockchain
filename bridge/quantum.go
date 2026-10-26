package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// QuantumConfig wires the Splendor Quantum Chain side of the bridge.
type QuantumConfig struct {
	// BaseURL is the node REST API, e.g. http://localhost:8080.
	BaseURL string
	// ConfirmDepthValue is how many blocks behind the tip source events are
	// final. Quantum blocks are final-once-committed, so this is small.
	ConfirmDepthValue uint64
	// BridgeAddress is the deployed bridge_v1 instance address (SQ:...).
	BridgeAddress string
	// Claim submits the legacy->quantum settlement (bridge.claim) and returns
	// the accepted tx hash. It is injected because it needs the operator's
	// ML-DSA key, nonce and attestations — exactly what cmd/bridge-relayer
	// already builds. nil holds the legacy->quantum direction.
	Claim func(ctx context.Context, e OutboundEvent) (string, error)
	// TokenBalance reads a QRC-20 balance for AssetSPLV. nil returns an error
	// for token balances.
	TokenBalance func(ctx context.Context, owner string) (*big.Int, error)
}

// QuantumChain adapts the quantum node REST API to bridge.Chain.
type QuantumChain struct {
	cfg     QuantumConfig
	client  *http.Client
	circuit *CircuitBreaker
	limiter *RateLimiter
	reorg   *ReorgDetector
}

// NewQuantumChain adapts the quantum node REST API to bridge.Chain, wrapping
// every upstream call in a circuit breaker, rate limiter, and reorg detector.
//
// Defaults: circuit opens after 5 consecutive failures and stays open for 30s;
// rate limiter allows 10 calls/s with a burst of 20; reorg detector remembers
// the last 100 observed heights.
func NewQuantumChain(cfg QuantumConfig) *QuantumChain {
	if cfg.ConfirmDepthValue == 0 {
		cfg.ConfirmDepthValue = 1
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &QuantumChain{
		cfg:     cfg,
		client:  &http.Client{Timeout: 30 * time.Second},
		circuit: NewCircuitBreaker("quantum", 5, 30*time.Second),
		limiter: NewRateLimiter(10, 20),
		reorg:   NewReorgDetector(100),
	}
}

// withHardening applies rate limiting and circuit breaking to one upstream
// call. Rate limiting is checked first (cheap, no lock on the fast path), then
// the circuit breaker. A rejected call returns immediately without touching the
// chain.
func (c *QuantumChain) withHardening(f func() error) error {
	if !c.limiter.Allow() {
		return fmt.Errorf("quantum: rate limited")
	}
	return c.circuit.Call(f)
}

func (c *QuantumChain) Name() string    { return "quantum" }
func (c *QuantumChain) ChainID() string { return "sqc" }
func (c *QuantumChain) ConfirmDepth() uint64 {
	return c.cfg.ConfirmDepthValue
}

func (c *QuantumChain) Assets() map[Asset]AssetInfo {
	return map[Asset]AssetInfo{
		AssetNative: {Symbol: "SPLD", Wrapped: false},
		AssetSPLV:   {Symbol: "SPLV", Wrapped: true}, // QRC-20 SPLV is the wrapped form
	}
}

func (c *QuantumChain) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, int64(maxRPCResponseBytes))).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("GET %s: %s", path, e.Error)
		}
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	// Bound the response body (see maxRPCResponseBytes in evm.go): a
	// misbehaving node must fail the call, not balloon memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxRPCResponseBytes)+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if len(raw) > maxRPCResponseBytes {
		return fmt.Errorf("GET %s: response exceeds %d bytes", path, maxRPCResponseBytes)
	}
	return json.Unmarshal(raw, out)
}

// Height returns the node's head height.
func (c *QuantumChain) Height(ctx context.Context) (uint64, error) {
	var height uint64
	err := c.withHardening(func() error {
		var s struct {
			Height uint64 `json:"height"`
		}
		if err := c.getJSON(ctx, "/status", &s); err != nil {
			return err
		}
		height = s.Height
		return nil
	})
	if err != nil {
		return 0, err
	}
	// Reorg detection after a successful head read: a backward-moving tip is a
	// reorg in flight. Return the error so the relayer pauses rather than
	// settling against a chain that may be reorged out.
	if err := c.reorg.Observe(height, ""); err != nil {
		return 0, err
	}
	return height, nil
}

// Balance returns the native SPLD balance (or, via TokenBalance, the QRC-20
// SPLV balance) of owner, in smallest units.
func (c *QuantumChain) Balance(ctx context.Context, owner string, asset Asset) (*big.Int, error) {
	if err := ValidateSQAddress(owner); err != nil {
		return nil, err
	}
	if err := ValidateAsset(asset); err != nil {
		return nil, err
	}
	if asset == AssetSPLV {
		if c.cfg.TokenBalance == nil {
			return nil, fmt.Errorf("quantum: no QRC-20 balance reader wired for %s", asset)
		}
		var bal *big.Int
		err := c.withHardening(func() error {
			var err error
			bal, err = c.cfg.TokenBalance(ctx, owner)
			return err
		})
		if err != nil {
			return nil, err
		}
		return bal, nil
	}
	var s struct {
		Balance string `json:"balance"`
	}
	err := c.withHardening(func() error {
		return c.getJSON(ctx, "/balance/"+owner, &s)
	})
	if err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(s.Balance, 10)
	if !ok {
		return nil, fmt.Errorf("invalid balance %q", s.Balance)
	}
	return v, nil
}

// Outbound lists quantum-side source events (a bridge.lock / bridge.deposit).
// The quantum bridge module currently only implements claim (legacy->quantum);
// a lock action that emits a source event is the on-chain primitive the
// reverse direction still needs, so this returns no events until it lands.
func (c *QuantumChain) Outbound(ctx context.Context, from, to uint64) ([]OutboundEvent, error) {
	var events []OutboundEvent
	err := c.withHardening(func() error {
		events = nil
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// Settle credits value on the quantum chain for a legacy source event, via the
// injected Claim func (bridge.claim). nil holds the legacy->quantum direction.
func (c *QuantumChain) Settle(ctx context.Context, e OutboundEvent) (string, error) {
	if err := ValidateSQAddress(e.Recipient); err != nil {
		return "", err
	}
	if err := ValidateEVMAddress(e.Sender); err != nil {
		return "", err
	}
	if err := ValidateAsset(e.Asset); err != nil {
		return "", err
	}
	if err := ValidateAmount(e.Amount); err != nil {
		return "", err
	}
	if c.cfg.Claim == nil {
		return "", fmt.Errorf("quantum: no claim submitter wired (legacy->quantum direction)")
	}
	return c.cfg.Claim(ctx, e)
}

// Settled reports whether a legacy burn id is already claimed on the bridge.
func (c *QuantumChain) Settled(ctx context.Context, id string) (bool, error) {
	if c.cfg.BridgeAddress == "" {
		return false, nil
	}
	if err := ValidateSQAddress(c.cfg.BridgeAddress); err != nil {
		return false, err
	}
	var out struct {
		Claimed bool `json:"claimed"`
	}
	err := c.withHardening(func() error {
		return c.getJSON(ctx, "/bridge/"+c.cfg.BridgeAddress+"/claim/"+id, &out)
	})
	if err != nil {
		return false, err
	}
	return out.Claimed, nil
}
