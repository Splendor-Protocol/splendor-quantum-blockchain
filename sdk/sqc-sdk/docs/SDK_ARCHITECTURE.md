# Splendor SQC SDK Architecture (`@splendor/sqc-sdk` v2)

Unified JavaScript/TypeScript SDK for Splendor Quantum Chain — wallet, native modules, and dApp integration.

## Canonical export map

| Import | Purpose |
|--------|---------|
| `@splendor/sqc-sdk` → `createSession`, `createPublicClient`, `createWalletClient`, `connect` | Recipes (happy path) |
| `@splendor/sqc-sdk/core` | `Connection`, `Transport`, `Program`, `Signer`, module-call |
| `@splendor/sqc-sdk/wallet` | `Keypair`, factory connectors, `WasmSession`, `installBrowserSessionPolicy` |
| `@splendor/sqc-sdk/tokens`, `/defi`, `/bridge`, `/contracts`, `/x402` | Domain Program clients |
| `@splendor/sqc-sdk/errors` | `BridgeError` / `WalletError` / `isBridgeError` |
| `@splendor/connect-react` | ConnectKit UI (connect + SPLD/QRC-20 widgets) |

Prefer **`createSession`** (headless) and **ConnectKit** (UI). Prefer `Connection` for reads. Do **not** construct `SqRpc` or default-import `SplendorPQBridge` in new apps — use `Wallet` / factory connectors.

Deprecated root exports (still present one minor): `connectSplendor`, `createSplendorClient`, `SqRpc`, `SqcProvider`, `useSplendorPQ`, default `SplendorPQBridge` name. Session shape is **`ClientSession`** (the `SplendorClient` **class** is a factory; do not use the old type alias name).

## Recommended entry paths

| Who | Use |
|-----|-----|
| Headless / agents / Node | `createSession()` (bound modules) |
| Read-only | `createPublicClient()` → `Connection` only (no Wallet) |
| Browser UI | `@splendor/connect-react` (`ConnectKit` / `SplendorConnectProvider`) |
| Full UI demo | [`examples/web-dapp`](../examples/web-dapp) |
| Legacy imports | `@splendor/pq-bridge` (deprecated shim) |

### Built-in Program clients vs on-chain modules

Typed `modules.*` / `bindModules` clients ship for: **qrc20**, **qrc1155**, **exchange**, **dex**, **bridge**, **contract**.

On-chain modules **without** dedicated SDK Program clients (use `sendModuleCall` / `registerModule` — do not stub fake Programs): **lending**, **oracle**, **multisig**, **governance**, **qrc721**, **qrc4626**. Verify/Validators helpers exist as `VerifyClient` / `ValidatorsClient` (Connection/Transport-backed).

## Package layout

```
@splendor/sqc-sdk
├── .                    # createSession(), modules, deployQRC20(), Wallet
├── /core                # Connection, Transport, Program, Signer, module-registry
├── /wallet              # Keypair, connectors, connect(), WasmSession, browser policy
├── /tokens              # QRC20 + QRC1155
├── /tokens/qrc20
├── /tokens/qrc1155
├── /contracts           # bytecode deploy/call
├── /defi                # DEX + Exchange
├── /bridge              # on-chain bridge_v1 (≠ pq-bridge shim)
├── /x402                # payment protocol helpers
├── /addresses           # MODULE_ROUTER, qrc20/1155/exchange/dex/bridge addresses
├── /browser
└── /react               # useSplendorPQ (deprecated; prefer connect-react)
```

## Layers

1. **Transport (chain IO)** — owns balance, nonce, module reads, submit, confirmation, x402 verify/settle, subscribe. `RpcTransport` wraps `SqRpc` privately; `MockTransport` scripts the same surface for tests (no hidden live RPC).
2. **Connection** — thin façade over Transport. Do not use a parallel `SqRpc` for production paths. `connection.rpc` is deprecated and only works when the transport is `RpcTransport`.
3. **Signer / Wallet** — `Wallet` implements `Signer`. Crypto session lives in **`WasmSession`**. Browser idle/visibility is **`installBrowserSessionPolicy`** (host-owned; ConnectKit / Wallet opts install it). Extension unlock uses **`ExtensionSigner`** (keys stay in the Chrome extension).
4. **Program** — typed native module clients. Writes take `Signer`; deploy actions use `isDeployAction()` overrides.
5. **Module registry** — `registerModule` / `defineModule` / `listModules`.
6. **Recipes** — `createSession()`, `connect()` via factory connectors on a **shared Connection**, `modules.*`, `deployQRC20()`, etc.

```mermaid
flowchart LR
  UI[connect-react] --> Ctrl[Controller]
  Ctrl --> Conn[WalletFactoryConnector]
  Conn --> W[Wallet + WasmSession]
  W --> C[Connection]
  C --> T[Transport]
  T --> R[SqRpc private]
  Policy[BrowserSessionPolicy] -.-> W
```

## Module registry

```ts
import { createSession, modules, registerModule, defineModule } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed });
await s.token(tokenAddr).transfer(recipient, '10');

registerModule(
  defineModule({
    id: 'myModule',
    create: (connection, address) => new MyProgram(connection, address),
  }),
);
```

## Built vs next (SDK modules)

| Module | SDK Program | Notes |
|--------|-------------|--------|
| QRC-20 | Built | deploy, transfer, approve, permit |
| QRC-1155 | Built | deploy, mint, transfer, burn |
| DEX | Built | deploy_pool, add_liquidity, swap, pool_info |
| Exchange | Built | deploy, conditions, match, cancel, signing, position_id, order_hash |
| Bridge (`bridge_v1`) | Built | deploy, claim, operators |
| Bytecode contracts | Built | QSol ABI call + deployBytecode |
| QSol compile-in-SDK | Not built | use monorepo `sqc-solc` offline |
| QRC-721 / lending / oracle | Roadmap | no stubs until on-chain ABI stable |
| WalletConnect / mobile | Deferred | ConnectKit is browser-injected first |
| ABI/codegen for QSol | Roadmap | viem-like typed contracts later |

## Native modules

Writes: `signer.sendModuleCall({ module, action, params })` → `TxCall` to `MODULE_ROUTER_ADDRESS` (via Connection → Transport).

Reads: `connection.moduleCall(envelope)` → Transport `POST /call` with the same JSON envelope.

## Connectors

- **Headless** (`WalletFactoryConnector`): `connect(connection) → Wallet` — used by `connect()`; **must reuse** the passed Connection (never `toRpcOptions()` clone).
- **Unlock** (`WalletUnlockConnector`): `unlock({ wallet, connection }) → DeriveInfo` — canonical for UI.
- **connect-react** wraps unlock with UI metadata (`name`, `icon`, `keyOrigin`).

## Errors

Public surfaces throw `BridgeError` / `WalletError` with a stable `.code`. Catch with `isBridgeError`. Prefer `WalletError` on new throw sites.

## SQDP-1

Wallet derivation, fees, confirmation, and WASM pinning follow [SQDP-1.md](SQDP-1.md).

## Adding a new Program

1. Implement `src/<area>/<name>.ts` extending `Program` (override `instanceParamKey` / `isDeployAction` if needed).
2. Add address helper in `src/addresses/index.ts` matching Go `vm` derivation.
3. Call `registerModule(defineModule({ id, create }))` (built-ins do this from `modules.ts`).
4. Export from a package subpath + `public-api.ts` if first-party.
5. Add functional tests under `src/__tests__/`.

No hand-editing of Session bind tables is required for registry-backed modules.
