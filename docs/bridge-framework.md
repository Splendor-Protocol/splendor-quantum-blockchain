# Bridge framework (`bridge/`)

A plug-and-play cross-chain bridge between the legacy EVM Splendor chain
(chain 2691) and the Splendor Quantum Chain. It generalises the existing
one-way migration (`vm/bridge.go` + `cmd/bridge-relayer`) into a two-way,
chain-agnostic framework.

## Architecture

```
                ┌─────────────────────────────────────────┐
                │               Relayer                   │
                │  (bridge.NewRelayer(left, right))       │
                │  RunOnce(LeftToRight | RightToLeft)    │
                └───────────────┬─────────────────────────┘
                                │  bridge.Chain (interface)
                ┌───────────────┴───────────────┐
                │                               │
        ┌───────▼────────┐            ┌─────────▼─────────┐
        │   EVMChain      │            │   QuantumChain    │
        │ (any EVM chain) │            │  (sqc-node REST)  │
        │  eth_* JSON-RPC │            │  /status /balance │
        └────────────────┘            └───────────────────┘
```

A `Chain` is the whole plug-in surface:

```go
type Chain interface {
    Name() string
    ChainID() string
    ConfirmDepth() uint64
    Height(ctx) (uint64, error)
    Assets() map[Asset]AssetInfo
    Balance(ctx, owner, asset) (*big.Int, error)
    Outbound(ctx, from, to) ([]OutboundEvent, error) // source-side lock/burn
    Settle(ctx, e) (string, error)                    // sink-side mint/release
    Settled(ctx, id) (bool, error)                    // idempotency
}
```

Assets are addressed by a **stable symbolic name** (`native`, `splv`, …); each
chain maps the name to its own representation. Adding a chain is a new
`Chain` implementation — the engine and the asset registry are chain-agnostic.

## Value flow (lock-mint / burn-release)

1. A user sources value on chain A: locks native funds, or burns a registered
   token, into a bridge-controlled escrow; chain A emits a source event.
2. The relayer watches source events on A, waits for `ConfirmDepth`, then
   settles on B: credits the matching asset (or mints the wrapped form) to the
   destination account.
3. The reverse direction runs symmetrically, so value travels both ways.

Idempotency lives on-chain (`Settled` + idempotent `Settle`): a re-submitted
settlement is a no-op, so a relayer crash/restart never double-credits.

## Assets

| Symbol | Legacy (EVM) | Quantum |
|---|---|---|
| `native` | SPLD (ether) | SPLD |
| `splv` | SPLV (ERC-20) | SPLV (QRC-20, wrapped) |

## What is wired today

- **`bridge/bridge.go`** — the `Chain` interface, asset registry, `Relayer`
  engine with per-source cursors, both directions.
- **`bridge/evm.go`** — generic EVM adapter: `eth_chainId`/`eth_blockNumber`/
  `eth_getLogs`/`eth_getBalance`/`eth_call`, decodes the `Burned` event into
  `OutboundEvent`. Settlement is delegated to an injected `Release` func.
- **`bridge/quantum.go`** — quantum adapter: `/status`, `/balance/{addr}`,
  `/bridge/{addr}/claim/{id}`; settlement is delegated to an injected `Claim`
  func (the exact `bridge.claim` submission `cmd/bridge-relayer` already builds).
- **`cmd/sqbridge`** — interactive console (status, assets, balances, relay).

## What remains (on-chain primitives, not framework work)

The framework is complete and tested; the two on-chain primitives the
**reverse** direction still needs, and the two injected settlement funcs, are:

1. **Legacy release contract** (Solidity): `release(id, asset, to, amount)`
   guarded by operator/attestation, which unlocks escrowed value on the EVM
   side. Wire it into `EVMConfig.Release` + `EVMChain.Settled`.
2. **Quantum `bridge.lock` action**: a module action that escrows SPLD (and
   burns QRC-20 SPLV) and emits a source event. Wire its reader into
   `QuantumChain.Outbound` and its submitter into `QuantumChain.Settle`.

The existing legacy→quantum migration (burn → `bridge.claim`) already covers
the left→right direction end to end; the framework runs it unchanged.

## Security notes

- Settlement idempotency is on-chain, never in the relayer's memory.
- The relayer is a **trusted operator**: it decides which events to settle and
  when. Production deployments should gate each settle behind M-of-N
  attestations, exactly as `bridge_v1.claim` already does on the quantum side.
- Confirm depth on the legacy PoA chain is probabilistic (120 blocks by
  default) and is a per-chain policy, not a constant of the framework.
