# dApp integration guide

Connect → fund → transfer → deploy tokens → wait.

**SDK:** [`@splendor/sqc-sdk`](../package.json) v2 (umbrella). `@splendor/pq-bridge` is a deprecated re-export shim.

**Architecture:** [SDK_ARCHITECTURE.md](./SDK_ARCHITECTURE.md)

Connect → fund → transfer → wait. Use a read-only `Connection` / `SqcProvider` until the user connects a signing wallet.

**Protocol standard:** all MUST/SHOULD rules live in **[SQDP-1.md](./SQDP-1.md)**. Ship checklist: **[SQDP-1-CONFORMANCE.md](./SQDP-1-CONFORMANCE.md)**.

## Client ask → SDK answer (bridge only)

| Ask | Use |
|-----|-----|
| Plug-and-play wallet UI (AppKit-style) | `@splendor/connect-react` — `SplendorConnectProvider` + `ConnectKit` / `ConnectButton` (`hostModals` default) |
| Hide PQ crypto from dApp code | WASM signer only; UI shows `SQ:` identity |
| One-touch / one config for all dApps | `sqc.networks.json` + `connectSplendor` / `createSplendorClient({ networkConfig })` |
| Real testnet RPCs without forking package | `rpcUrls: [...]` (gateway optional for SqRpc) or `customNetworks` / `registerNetwork` |
| Host WASM without manual copy | Vite: `@splendor/connect-react/vite` `sqcWasmPlugin`; Next: `npx sqc-copy-wasm` / `@splendor/connect-react/next` |
| Idle / tab lock must update UI | Bridge emits `lock`; connect-react sets `status: 'locked'` → Unlock CTA |
| Soft network switch without losing last wallet | `useNetwork().switchNetwork` (keeps last connector; reconnects when previously connected) |
| No bridge markup fee | Only chain `minFee` (`PROTOCOL.minFee`); under congestion node may require higher — handle reject and retry |
| Deploy QRC-20 token | `deployQRC20(wallet, { name, symbol, decimals, totalSupply })` or `useDeployToken()` |
| Token balance / transfer | `getToken(connection, addr)` or `useQRC20(addr)` |
| Confirmation that is not fake | `waitForConfirmation` / `waitForConfirmation: true` — receipt-first, nonce fallback ([SQDP §7](./SQDP-1.md)) |

**Out of scope (documented):** WalletConnect v2 protocol, full EIP-1193 SQC provider for wagmi, CDN-hosted WASM.

## Recommended: Connect UI kit (React)

For browser dApps, use **`@splendor/connect-react`** — AppKit-style modal + connectors. PQ ML-DSA-65 stays inside WASM; your UI only shows `SQ:` identity.

```tsx
import '@splendor/connect-react/style.css';
import { sqcWasmPlugin } from '@splendor/connect-react/vite'; // vite.config.ts
import {
  SplendorConnectProvider,
  ConnectKit,
  useSendTransfer,
} from '@splendor/connect-react';

<SplendorConnectProvider network="local" autoReconnect allowUnpinnedWasm>
  <ConnectKit />
</SplendorConnectProvider>
```

- WASM: prefer `sqcWasmPlugin()` (see [`WASM_HOSTING.md`](./WASM_HOSTING.md)).
- Networks: `useNetwork().switchNetwork('local')` — placeholder `*.example` RPCs are hidden unless `allowPlaceholderNetworks`.
- Transfers: `useSendTransfer()` instead of raw wallet calls.
- Testnet with real RPC only: `rpcUrls={['https://…']}` — `gatewayUrl` not required for chain RPC.

Full guide: [`../../connect-react/README.md`](../../connect-react/README.md).

Lower-level (no UI): `connectSplendor` from `@splendor/pq-bridge`, or `useSplendorPQ` from `@splendor/pq-bridge/react`.

```ts
import { PROTOCOL, SQDP_VERSION, connectSplendor, createSplendorClient } from '@splendor/pq-bridge';
// PROTOCOL.chainId, PROTOCOL.minFee, PROTOCOL.confirmationCaveat
// Networks: edit src/config/sqc.networks.json (single source)
```

## One-touch connect (headless / agents)

```ts
// Browser
const { wallet, info, network } = await connectSplendor({
  network: 'local', // or testnet / mainnet — from sqc.networks.json
  connect: 'metamask',
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

Or create without connecting: `createSplendorClient({ network: 'local' })` then `wallet.connectFromMetaMask()`.

## Mental model

| Concept | What it is |
|---------|------------|
| EVM / MetaMask `0x…` | Only used to **derive** the PQ key via `personal_sign` |
| `SQ:` address | Real on-chain identity (32-byte SHA3-256 of ML-DSA-65 pubkey) |
| WASM signer | Same Go ML-DSA-65 (circl, FIPS 204) as the node |

**Security FAQ:** funds and nonces live on the **`SQ:`** address, not the MetaMask address. Never treat them as interchangeable.

## 1. Read-only provider (no WASM)

Prefer one-touch (loads RPC from `sqc.networks.json`):

```ts
import { createSplendorClient } from '@splendor/pq-bridge';

const { provider } = createSplendorClient({ network: 'local' });
const status = await provider.getStatus();
```

Or construct manually:

```ts
import { SqcProvider } from '@splendor/pq-bridge';

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
import { SplendorPQBridge } from '@splendor/pq-bridge';
// browser: import from '@splendor/pq-bridge/browser'

// Manual path (overrides network defaults when needed):
const wallet = SplendorPQBridge.fromProvider(provider, {
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});

wallet.on('connect', ({ address, evmAddress }) => {
  console.log('PQ', address, 'from', evmAddress);
});
wallet.on('accountsChanged', ({ address }) => {
  console.log('re-derived after MetaMask switch', address);
});
wallet.on('disconnect', () => console.log('disconnected'));
wallet.on('lock', () => console.log('session locked — re-connect to unlock'));

await wallet.connectFromMetaMask();
// Node / agents: await wallet.connectFromEvmPrivateKey(key)
// or: await wallet.connectFromSeed(seedHex)
```

MetaMask `accountsChanged` triggers a fresh `personal_sign` and re-derives the PQ wallet (opt out with `watchMetaMaskAccounts: false`).

## 3. Fund

Send SPLD to `wallet.getAddress()` (the `SQ:` address) from a faucet or another PQ wallet. MetaMask cannot send native EVM ETH onto this chain.

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

(The `sqaddress` type name is a QSol convention, but no QSol compiler exists —
you are calling SQC-VM bytecode or a native module, not compiled QSol.)

```ts
import { encodeFunctionData, decodeArgUint256 } from '@splendor/pq-bridge';

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
cd sdk/bridge
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
