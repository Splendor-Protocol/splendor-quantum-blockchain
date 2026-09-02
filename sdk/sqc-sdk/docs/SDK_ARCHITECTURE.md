# Splendor SQC SDK Architecture (`@splendor/sqc-sdk` v2)

Unified JavaScript/TypeScript SDK for Splendor Quantum Chain — wallet, native modules, and dApp integration.

## Recommended entry paths

| Who | Use |
|-----|-----|
| Headless / agents / Node | `createSession()` (bound modules) or `connect()` from `@splendor/sqc-sdk` |
| Browser UI | `@splendor/connect-react` (`ConnectKit` + `useDex` / `useQRC20` / …) |
| Full UI demo | [`examples/web-dapp`](../examples/web-dapp) |
| Legacy imports | `@splendor/pq-bridge` (deprecated shim) |

Prefer `createSession` / `modules.bind(connection, wallet)` so writes omit the wallet argument. Unbound `modules.qrc20(connection, addr).transfer(wallet, …)` remains for advanced use.

`Wallet` is the preferred name; `SplendorPQBridge` remains an alias.

## Package layout

```
@splendor/sqc-sdk
├── .                    # connect(), modules, deployQRC20(), Wallet
├── /core                # Connection, Transport, Program, module-call, subscribe
├── /wallet              # Keypair, connectors, connect()
├── /tokens              # QRC20 + QRC1155
├── /tokens/qrc20
├── /tokens/qrc1155
├── /contracts           # bytecode deploy/call
├── /defi                # DEX + Exchange
├── /bridge              # on-chain bridge_v1 (≠ pq-bridge shim)
├── /x402                # payment protocol helpers
├── /addresses           # MODULE_ROUTER, qrc20/1155/exchange/dex/bridge addresses
├── /browser
└── /react               # useSplendorPQ (prefer connect-react for UI)
```

## Layers

1. **Transport** — `RpcTransport` wraps HTTP RPC (`SqRpc`) with failover and `POST /call` for module reads.
2. **Connection** — read-only chain client (`getBalance`, `moduleCall`, `subscribe`).
3. **Wallet** (`Wallet` / `SplendorPQBridge`) — WASM ML-DSA-65 signing + submit.
4. **Program** — typed native module clients (`QRC20`, `QRC1155`, `DEX`, `Exchange`, `BridgeModule`).
5. **Recipes** — `connect()`, `modules.*`, `deployQRC20()`, `deployExchange()`, etc.

## Module registry

```ts
import { connect, modules, deployQRC20 } from '@splendor/sqc-sdk';

const { wallet, connection } = await connect({ network: 'local', mode: 'seed', seed });
const { address } = await deployQRC20(wallet, { name: 'T', symbol: 'T', decimals: 18, totalSupply: '1000' });
const token = modules.qrc20(connection, address);
await token.transfer(wallet, recipient, '10');

const book = modules.exchange(connection, exchangeAddr);
const order = await book.signOrder(wallet, { /* … */ });
```

## Built vs next (SDK modules)

| Module | SDK Program | Notes |
|--------|-------------|--------|
| QRC-20 | Built | deploy, transfer, approve, permit |
| QRC-1155 | Built | deploy, mint, transfer, burn |
| DEX | Built | deploy_pool, add_liquidity, swap |
| Exchange | Built | deploy, conditions, match, cancel, signing |
| Bridge (`bridge_v1`) | Built | deploy, claim, operators |
| Bytecode contracts | Built | QSol ABI call + deployBytecode |
| QSol compile-in-SDK | Not built | use monorepo `sqc-solc` offline |

## Native modules

Writes: `wallet.sendModuleCall({ module, action, params })` → `TxCall` to `MODULE_ROUTER_ADDRESS`.

Reads: `connection.moduleCall(envelope)` → `POST /call` with same JSON envelope.

Programs inject the instance address under the correct key (`token`, `collection`, `pool`, `exchange`, `bridge`).

## SQDP-1

Wallet derivation, fees, confirmation, and WASM pinning follow [SQDP-1.md](SQDP-1.md).

## Adding a new Program

1. Add `src/<area>/<name>.ts` extending `Program` (override `instanceParamKey` if not `token`).
2. Add address helper in `src/addresses/index.ts` matching Go `vm` derivation.
3. Export from subpath + `modules` factory + `public-api.ts`.
4. Add functional tests under `src/__tests__/`.
