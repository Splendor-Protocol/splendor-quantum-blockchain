# dApp integration guide

Connect → fund → transfer → deploy tokens → wait.

**SDK:** [`@splendor/sqc-sdk`](../package.json) v2 (umbrella). `@splendor/pq-bridge` is a deprecated re-export shim.

**Architecture:** [SDK_ARCHITECTURE.md](./SDK_ARCHITECTURE.md)

Connect → fund → transfer → wait. Use a read-only `Connection` / `SqcProvider` until the user connects a signing wallet.

**Protocol standard:** all MUST/SHOULD rules live in **[SQDP-1.md](./SQDP-1.md)**. Ship checklist: **[SQDP-1-CONFORMANCE.md](./SQDP-1-CONFORMANCE.md)**.

## Client ask → SDK answer (bridge only)

| Ask | Use |
|-----|-----|
| Scaffold a new dApp | `npm exec create-splendor-dapp my-dapp` |
| Plug-and-play wallet UI (AppKit-style) | `@splendor/connect-react` — `SplendorConnectProvider` + `ConnectKit` / widgets |
| Drop-in transfer / token / account panels | `TransferWidget`, `TokenPanel`, `DeployTokenWidget`, `AccountCard` |
| Hide PQ crypto from dApp code | WASM signer only; UI shows `SQ:` identity |
| One-touch / one config for all dApps | `sqc.networks.json` + `createSession` / `createWalletClient` / `connect` |
| WalletClient / PublicClient (viem-style) | `createWalletClient` / `createPublicClient` |
| Page script ↔ connected wallet | `exposeInjectedProvider` or `installSplendorProvider` → `window.splendor` |
| Working demo (connect + SPLD + modules) | [`../examples/web-dapp`](../examples/web-dapp) |
| Real testnet RPCs without forking package | `rpcUrls: [...]` (gateway optional for SqRpc) or `networks` / `networkConfig` / `registerNetwork` |
| Host WASM without manual copy | Vite: `@splendor/connect-react/vite` `sqcWasmPlugin`; Next: `npx sqc-copy-wasm` / `@splendor/connect-react/next` |
| Idle / tab lock must update UI | Wallet emits `lock`; connect-react sets `status: 'locked'` → Unlock CTA |
| Soft network switch without losing last wallet | `useNetwork().switchNetwork` (emits `chainChanged`) |
| No bridge markup fee | Only chain `minFee` (`PROTOCOL.minFee`); under congestion node may require higher — handle reject and retry |
| Deploy QRC-20 token | `deployQRC20(wallet, …)` or `useDeployToken()` / `DeployTokenWidget` |
| Token balance / transfer | `modules.qrc20(connection, addr)` / `useQRC20(addr)` / `TokenPanel` |
| QRC-1155 / Bridge / Contract | `modules.qrc1155` / `modules.bridge` / `modules.contract` (hooks: `useQRC1155`, `useBridge`, `useContract`) |
| DEX / Exchange | **Not public** — leftover under `@splendor/sqc-sdk/defi` until node routes them |
| Confirmation that is not fake | `waitForConfirmation` / `waitForConfirmation: true` — receipt-first, nonce fallback ([SQDP §7](./SQDP-1.md)) |

**Out of scope (documented):** WalletConnect v2, Ethereum `eth_*` / wagmi EIP-1193 adapters, CDN-hosted WASM.
**Supported interop:** SQC injected provider `window.splendor` with `splendor_*` methods (not MetaMask-compatible).

**Injected provider trust:** any page script can spoof `window.splendor` (`isSplendor`, method names). Prefer the official Splendor Quantum Wallet extension (`isExtension` / non–in-page inject) and harden with CSP so untrusted scripts cannot redefine the inject. Treat in-page `installSplendorProvider` as same-origin only — never as proof of a third-party wallet.

### Splendor Quantum Wallet extension contract

ConnectKit talks to the official extension via `ExtensionSigner`. The inject speaks **`sqc_*` + `wallet_*`** (content script accepts **array params only**):

| Action | Method | Notes |
|--------|--------|-------|
| Connect | `sqc_requestAccounts` | Opens approval UI |
| Silent resume | `sqc_accounts` | Used by `autoReconnect` |
| Disconnect | `wallet_disconnect` | Revokes **this origin only** |
| Sign | `sqc_sign` with `[message, { encoding }]` | Domain-separated by SDK |
| Send SPLD | `sqc_sendTransaction` with `[{ to, amount\|amountSPLD, fee? }]` | Returns hash string |

Install the extension before opening Connect. Soft network switch in the dApp rebinds SDK RPC only — the extension chain is changed in **wallet Settings** (listen for `chainChanged`). Use `<WrongNetworkBanner />` when chains may diverge.

## Recommended: Connect UI kit (React)

For browser dApps, use **`@splendor/connect-react`** — AppKit-style modal + connectors + widgets. PQ ML-DSA-65 stays inside WASM; your UI only shows `SQ:` identity.

```tsx
import '@splendor/connect-react/style.css';
import { sqcWasmPlugin } from '@splendor/connect-react/vite'; // vite.config.ts
import {
  SplendorConnectProvider,
  ConnectKit,
  TransferWidget,
  useSendTransfer,
} from '@splendor/connect-react';

<SplendorConnectProvider network="local" autoReconnect allowUnpinnedWasm>
  <ConnectKit />
  <TransferWidget />
</SplendorConnectProvider>
```

- WASM: prefer `sqcWasmPlugin()` (see [`WASM_HOSTING.md`](./WASM_HOSTING.md)).
- Networks: `useNetwork().switchNetwork('local')` — placeholder `*.example` RPCs are hidden unless `allowPlaceholderNetworks`.
- Transfers: `useSendTransfer()` instead of raw wallet calls.
- Testnet with real RPC only: `rpcUrls={['https://…']}` — `gatewayUrl` not required for chain RPC.

Full guide: [`../../connect-react/README.md`](../../connect-react/README.md).

**Runnable demo:** [`../examples/web-dapp`](../examples/web-dapp) — ConnectKit + balance + send SPLD + QRC-20 / 1155 + Bridge + Contract.

### Minimal React checklist

1. `npm i @splendor/connect-react @splendor/sqc-sdk` (or `npm exec create-splendor-dapp my-dapp`)
2. Vite: `sqcWasmPlugin()` from `@splendor/connect-react/vite`
3. Wrap app in `SplendorConnectProvider` + `ConnectKit` / widgets
4. Use `TransferWidget`, `useBalance`, `useSendTransfer`, `useQRC20` for panels (see web-dapp)

Lower-level (no UI): `createSession` / `createWalletClient` / `connect` from `@splendor/sqc-sdk`.

```ts
import { PROTOCOL, SQDP_VERSION, createSession, createWalletClient, createPublicClient } from '@splendor/sqc-sdk';
// createSession → bound sendSPLD / token / bridge / modules (no wallet args on writes)
// createWalletClient → wallet.connect({ mode: 'seed', seed }) / signAndSendTransaction
// PROTOCOL.chainId, PROTOCOL.minFee, PROTOCOL.signMessageDomain, PROTOCOL.confirmationCaveat
```

### Bound session (preferred headless)

```ts
const s = await createSession({ network: 'local', mode: 'seed', seed });
await s.sendSPLD(to, '0.01');
await s.token(token).transfer(to, '100');
await s.bridge(bridgeAddr).claim({ /* … */ });
```

### WalletClient (alternative headless)

```ts
const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
await wallet.connect({ mode: 'seed', seed });
await wallet.signAndSendTransaction({ type: 'transfer', to, amountSPLD: '0.01' });
```

Legacy aliases (`connectSplendor`, `createSplendorClient`, `SqcProvider`) still work but are deprecated.
## One-touch connect (headless / agents)

```ts
// Browser — Splendor extension (preferred) or seed via ConnectKit
const { wallet, info, network } = await connectSplendor({
  network: 'local', // or testnet / mainnet — from sqc.networks.json
  connect: 'extension',
});
// network.wasmUrl / execUrl already applied from config
console.log(info.address, network.id);
```

```ts
// Node agent
const { wallet } = await connectSplendor({
  network: 'local',
  connect: 'seed',
  seed: process.env.PQ_SEED,
});
```

Or create without connecting: `createSplendorClient({ network: 'local' })` then `wallet.connectFromSeed(seed)`.

## Mental model

| Concept | What it is |
|---------|------------|
| Native seed / extension | Post-quantum ML-DSA-65 key material |
| `SQ:` address | Real on-chain identity (32-byte SHA3-256 of ML-DSA-65 pubkey) |
| WASM signer | Same Go ML-DSA-65 (circl, FIPS 204) as the node (seed path) |

**Security FAQ:** funds and nonces live on the **`SQ:`** address. MetaMask / EVM addresses are not used for chain accounts.

## 1. Read-only provider (no WASM)

Prefer one-touch (loads RPC from `sqc.networks.json`):

```ts
import { createSplendorClient } from '@splendor/sqc-sdk';

const { provider } = createSplendorClient({ network: 'local' });
const status = await provider.getStatus();
```

Or construct manually:

```ts
import { SqcProvider } from '@splendor/sqc-sdk';

const provider = new SqcProvider({
  rpcUrls: ['https://rpc-a.example', 'https://rpc-b.example'],
  retries: 2,
});

const status = await provider.getStatus();
const bal = await provider.getBalance('SQ:…');
```

Share one `SqcProvider` across React components. Load WASM only after connect.

## 2. Connect (signing wallet)

Prefer one-touch / Connect UI kit so WASM paths come from `sqc.networks.json` automatically.
You still must **host** the files at those URLs (see [`WASM_HOSTING.md`](./WASM_HOSTING.md)).

```ts
import { Wallet, SplendorPQBridge } from '@splendor/sqc-sdk';
// browser: import from '@splendor/sqc-sdk/browser'
// SplendorPQBridge is an alias for Wallet

// Manual path (overrides network defaults when needed):
const wallet = Wallet.fromProvider(provider, {
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});

wallet.on('connect', ({ address }) => {
  console.log('PQ', address);
});
wallet.on('accountsChanged', ({ address }) => {
  console.log('account changed', address);
});
wallet.on('disconnect', () => console.log('disconnected'));
wallet.on('lock', () => console.log('session locked — re-connect to unlock'));

await wallet.connectFromSeed(seedHex);
// Browser extension: use connect({ mode: 'extension' }) / ExtensionSigner
```

## 3. Fund

Send SPLD to `wallet.getAddress()` (the `SQ:` address) from a faucet or another PQ wallet.

## 4. Transfer + confirmation

```ts
const res = await wallet.sendTransfer({
  to: 'SQ:…',
  amountSPLD: '0.01',
  waitForConfirmation: true,
  confirmTimeoutMs: 60_000,
});
// Prefer GET /tx/{hash}/receipt when indexed; else nonce advancement (can race)
```

## 5. Contract calls

Use QSol ABI (`sqaddress`, SHA3-256 selectors). Deploy pre-built SQC-VM bytecode
via `@splendor/sqc-sdk/contracts`, or compile offline with the monorepo
`sqc-solc` prototype (not embedded in this SDK).

```ts
import { encodeFunctionData, decodeArgUint256 } from '@splendor/sqc-sdk';
import { deployBytecode, getContract } from '@splendor/sqc-sdk/contracts';

await wallet.callContract({
  contract: 'SQ:…',
  function: 'transfer(sqaddress,uint256)',
  args: ['SQ:…', 1_000_000_000_000_000_000n],
});

// or build calldata yourself:
const data = encodeFunctionData('balanceOf(sqaddress)', [addr]);
```

Selector = `SHA3-256(signature)[0:4]` (same as Go `vm.SelectorFromSignature`).

## 6. Gateway (Docker one-pager)

```bash
cd sdk/sqc-sdk
# build wasm first: ./wasm/build.ps1
cd gateway
SQC_NETWORK=local docker compose up --build
# → http://localhost:3090  (port from sqc.networks.json gatewayPort)
```

| Env | Default | Notes |
|-----|---------|--------|
| `SQC_NETWORK` | `local` | Selects entry in `sqc.networks.json` |
| `SQC_RPC` | network `rpcUrls[0]` | Override chain node |
| `MODE` | `proxy` | Prefer for end users |
| `CORS_ORIGINS` | localhost:3000 | Explicit allowlist (not `*` by default) |
| `PUBLIC_HEALTH` | local=`1` | Expose `sqc_rpc` on `/health` only when enabled |
| `TRUST_PROXY` | unset | Enable only behind a trusted load balancer |
| `PORT` | network `gatewayPort` | Gateway listen |

**Proxy mode rejects client `evmPrivateKey`** — credentials stay on the client. Use `service-wallet` only for your own automation keys.

Serve WASM with correct MIME/CORS: [`WASM_HOSTING.md`](./WASM_HOSTING.md).

## Checklist

- [ ] Serve `sqc_signer.wasm` + `wasm_exec.js` with correct MIME / CORS
- [ ] Pin `wasmSha256` / `execSha256` for non-local browser networks
- [ ] Display **SQ:** address after connect (not 0x)
- [ ] Handle `BridgeError` codes: `USER_REJECTED`, `NO_PROVIDER`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`
- [ ] Use `rpcUrls` failover in production (`https://` for non-local)
- [ ] Confirm via `waitForConfirmation` (receipt-first; nonce fallback on minimal RPC)
- [ ] Host WASM at network `wasmUrl` / `execUrl` from `sqc.networks.json` (one-touch already resolves these; you still must serve the files)
- [ ] Remember: under congestion node fee floor may exceed `PROTOCOL.minFee` (`1000`)
