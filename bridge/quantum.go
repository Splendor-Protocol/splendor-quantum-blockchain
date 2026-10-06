package bridge

import (
	"context"
	"encoding/json"
	"fmt"
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
	cfg    QuantumConfig
	client *http.Client
}

func NewQuantumChain(cfg QuantumConfig) *QuantumChain {
	if cfg.ConfirmDepthValue == 0 {
		cfg.ConfirmDepthValue = 1
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &QuantumChain{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
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
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("GET %s: %s", path, e.Error)
		}
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Height returns the node's head height.
func (c *QuantumChain) Height(ctx context.Context) (uint64, error) {
	var s struct {
		Height uint64 `json:"height"`
	}
	if err := c.getJSON(ctx, "/status", &s); err != nil {
		return 0, err
	}
	return s.Height, nil
}

// Balance returns the native SPLD balance (or, via TokenBalance, the QRC-20
// SPLV balance) of owner, in smallest units.
func (c *QuantumChain) Balance(ctx context.Context, owner string, asset Asset) (*big.Int, error) {
	if asset == AssetSPLV {
		if c.cfg.TokenBalance == nil {
			return nil, fmt.Errorf("quantum: no QRC-20 balance reader wired for %s", asset)
		}
		return c.cfg.TokenBalance(ctx, owner)
	}
	var s struct {
		Balance string `json:"balance"`
	}
	if err := c.getJSON(ctx, "/balance/"+owner, &s); err != nil {
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
	return nil, nil
}

// Settle credits value on the quantum chain for a legacy source event, via the
// injected Claim func (bridge.claim). nil holds the legacy->quantum direction.
func (c *QuantumChain) Settle(ctx context.Context, e OutboundEvent) (string, error) {
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
	var out struct {
		Claimed bool `json:"claimed"`
	}
	if err := c.getJSON(ctx, "/bridge/"+c.cfg.BridgeAddress+"/claim/"+id, &out); err != nil {
		return false, err
	}
	return out.Claimed, nil
}
