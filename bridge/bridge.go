// Package bridge implements a plug-and-play cross-chain bridge framework.
//
// A bridge moves value between exactly two chains — today the legacy EVM
// Splendor mainnet (chain 2691) and the Splendor Quantum Chain — using the
// lock-mint / burn-release pattern:
//
//  1. A user "sources" value on chain A: they lock native funds, or burn a
//     registered token, into an escrow the bridge controls, and chain A emits
//     a source event.
//  2. The relayer watches source events on A, waits for finality (confirm
//     depth), then "settles" on B: it credits the matching asset (or mints the
//     wrapped representation) to the destination account.
//  3. The reverse direction works symmetrically, so value travels both ways.
//
// Adding a chain means implementing Chain. The relayer engine and the asset
// registry know nothing about the concrete chains: they address assets by a
// stable symbolic name ("native", "splv", ...) and each Chain maps that name to
// its own on-chain representation.
package bridge

import (
	"context"
	"fmt"
	"math/big"
)

// Asset is a stable symbolic name shared by both chains. It is the registry
// key; each Chain maps it to its own on-chain asset (native coin, ERC-20,
// QRC-20, ...).
type Asset string

const (
	// AssetNative is the native coin on both chains: SPLD (EVM ether) on the
	// legacy chain and SPLD on the quantum chain.
	AssetNative Asset = "native"
	// AssetSPLV is the legacy SPLV (ERC-20) token bridged to/from the quantum
	// SPLV QRC-20 token.
	AssetSPLV Asset = "splv"
)

// AssetInfo describes one asset on one chain.
type AssetInfo struct {
	// Symbol is the display symbol on this chain (e.g. "SPLD", "SPLV").
	Symbol string
	// Wrapped is true when this chain holds the bridged (wrapped) form of the
	// asset rather than the canonical form. Informational: settlement still
	// mints/releases whatever the chain considers the local form.
	Wrapped bool
}

// OutboundEvent is a user-initiated source-side deposit/lock/burn that must be
// settled on the opposite chain. ID is the idempotency key: it is stable and
// unique on the source chain, so settling the same event twice is a no-op.
type OutboundEvent struct {
	ID        string   // idempotency key, unique per source chain
	Chain     string   // source chain name
	Asset     Asset    // symbolic asset
	Amount    *big.Int // smallest units on the source chain
	Sender    string   // source-chain sender (EVM address or SQ: address)
	Recipient string   // destination-chain recipient (SQ: address or EVM address)
	Height    uint64   // source block height the event was emitted at
	TxID      string   // source transaction hash
}

// Chain is the pluggable adapter a relayer drives. Each side of a bridge
// implements it once; a new chain is a new implementation, nothing more.
type Chain interface {
	// Name is a short stable identifier ("legacy", "quantum", ...).
	Name() string
	// ChainID identifies the chain ("2691", or the quantum chain id).
	ChainID() string
	// ConfirmDepth is how many blocks behind the tip a source event must be
	// before it is considered final enough to settle.
	ConfirmDepth() uint64
	// Height returns the current head height.
	Height(ctx context.Context) (uint64, error)
	// Assets reports the assets this chain can source and settle.
	Assets() map[Asset]AssetInfo
	// Balance returns the spendable balance of owner for asset, in the asset's
	// smallest units. owner is a chain-native address string.
	Balance(ctx context.Context, owner string, asset Asset) (*big.Int, error)
	// Outbound lists source events emitted in [from, to], inclusive.
	Outbound(ctx context.Context, from, to uint64) ([]OutboundEvent, error)
	// Settle credits value on THIS chain for a source event from the other
	// chain: it mints the wrapped form or releases escrowed canonical value to
	// e.Recipient. Idempotent: settling an already-settled event returns its
	// existing tx id (or "") without double-crediting.
	Settle(ctx context.Context, e OutboundEvent) (string, error)
	// Settled reports whether e.ID is already settled on this chain.
	Settled(ctx context.Context, id string) (bool, error)
}

// Direction is which way value travels in a relay pass.
type Direction int

const (
	// LeftToRight relays Left -> Right.
	LeftToRight Direction = iota
	// RightToLeft relays Right -> Left.
	RightToLeft
)

// Relayer moves value between two chains in both directions. It owns a cursor
// per source chain so a pass resumes where the previous one stopped.
type Relayer struct {
	Left  Chain
	Right Chain

	cursors map[string]uint64
}

// NewRelayer builds a relayer for a pair of chains.
func NewRelayer(left, right Chain) *Relayer {
	return &Relayer{Left: left, Right: right, cursors: make(map[string]uint64)}
}

// Cursor returns the last-processed source height for src, so a caller can
// persist and restore it across restarts.
func (r *Relayer) Cursor(src Chain) uint64 { return r.cursors[src.Name()] }

// SetCursor seeds the source cursor (e.g. from a persisted state file). A zero
// cursor scans from genesis; production should seed it to the contract
// deployment block.
func (r *Relayer) SetCursor(src Chain, h uint64) { r.cursors[src.Name()] = h }

// RunOnce performs one relay pass in dir, advancing the source cursor and
// returning the events settled this pass. A transient settlement error aborts
// the pass and leaves the cursor where it was, so the next pass retries; the
// per-event Settled/Settle idempotency makes a duplicate re-submit safe.
func (r *Relayer) RunOnce(ctx context.Context, dir Direction) ([]OutboundEvent, error) {
	src, dst := r.Left, r.Right
	if dir == RightToLeft {
		src, dst = r.Right, r.Left
	}
	head, err := src.Height(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s height: %w", src.Name(), err)
	}
	confirm := src.ConfirmDepth()
	to := uint64(0)
	if head >= confirm {
		to = head - confirm
	}
	from := r.cursors[src.Name()]
	events, err := src.Outbound(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("%s outbound [%d,%d]: %w", src.Name(), from, to, err)
	}
	var settled []OutboundEvent
	for _, e := range events {
		if e.ID == "" {
			continue
		}
		done, err := dst.Settled(ctx, e.ID)
		if err != nil {
			return settled, fmt.Errorf("%s settled %s: %w", dst.Name(), e.ID, err)
		}
		if done {
			continue
		}
		if _, err := dst.Settle(ctx, e); err != nil {
			return settled, fmt.Errorf("settle %s (asset %s, %s) on %s: %w", e.ID, e.Asset, e.Amount, dst.Name(), err)
		}
		settled = append(settled, e)
	}
	if to > r.cursors[src.Name()] {
		r.cursors[src.Name()] = to
	}
	return settled, nil
}
