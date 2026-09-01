# Splendor SQC SDK Architecture (`@splendor/sqc-sdk` v2)

Unified JavaScript/TypeScript SDK for Splendor Quantum Chain — wallet, native modules, and dApp integration.

## Package layout

```
@splendor/sqc-sdk
├── .                    # connect(), deployQRC20(), SplendorClient
├── /core                # Connection, Transport, Program, module-call
├── /wallet              # Keypair, connectors, connect()
├── /tokens              # QRC20, deployQRC20, getToken
├── /tokens/qrc20        # QRC20 class only
├── /contracts           # bytecode deploy/call (Phase 2)
├── /defi                # DEX, exchange (Phase 3)
├── /x402                # payment protocol
├── /addresses           # MODULE_ROUTER, qrc20Address()
├── /browser             # browser WASM entry
└── /react               # useSplendorPQ hook
```

`@splendor/pq-bridge` is a **deprecated shim** that re-exports this package.

## Layers

1. **Transport** — `RpcTransport` wraps HTTP RPC (`SqRpc`) with failover and `POST /call` for module reads.
2. **Connection** — read-only chain client (`getBalance`, `moduleCall`, `getTxReceipt`).
3. **Wallet** (`SplendorPQBridge` / `Wallet`) — WASM ML-DSA-65 signing + submit.
4. **Program** — typed native module clients (`QRC20`, future `DEX`, `Exchange`).
5. **Recipes** — `connect()`, `deployQRC20()` for beginners.

## Native modules

Writes: `wallet.sendModuleCall({ module, action, params })` → `TxCall` to `MODULE_ROUTER_ADDRESS`.

Reads: `connection.moduleCall(envelope)` → `POST /call` with same JSON envelope.

## SQDP-1

Wallet derivation, fees, confirmation, and WASM pinning follow [SQDP-1.md](SQDP-1.md). This document covers SDK structure only.

## Phase 2 (shipped)

- **`@splendor/sqc-sdk/contracts`** — `Contract`, `deployBytecode`, `getContract` (bytecode deploy/call)
- **`@splendor/sqc-sdk/x402`** — `X402Client`, `createX402Middleware`, `wallet.payX402`
- **Explorer reads** on `Connection`: `estimateGas`, `getLogs`, `verifyContract`, `getContractCode`, `getContractStorage`
- **Subscriptions** — `connection.subscribe()` / `subscribe()` via `/ws` SSE
- **connect-react** — `usePayX402` hook

## Adding a new Program

1. Add `src/<module>/program.ts` extending `Program`.
2. Map actions to `vm` module router (`module_router.go`).
3. Add golden vectors under `testdata/`.
4. Export from subpath `package.json` exports.
