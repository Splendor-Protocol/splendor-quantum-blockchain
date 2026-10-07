# Splendor SQC SDK (`@splendor/sqc-sdk`)

Unified JavaScript/TypeScript SDK for **Splendor Quantum Chain** — native ML-DSA-65 wallet, QRC-20 / native modules, bytecode contracts, and dApp integration.

This package is Splendor PQ–specific (`SQ:` addresses, SHA3-256 QSol ABI, Go WASM ML-DSA-65 signer, Splendor REST RPC). It is **not** a generic EVM/ethers toolkit.

**`@splendor/pq-bridge`** remains a deprecated re-export shim for older imports.

**Do not use** the legacy root file [`sdk/sqc-sdk.js`](../sqc-sdk.js) — unsafe wire format for submit.

## Happy path (pick one)

### 1. Browser dApp — ConnectKit + widgets

```bash
npm exec create-splendor-dapp my-dapp
# or: npm i @splendor/connect-react @splendor/sqc-sdk
```

```tsx
import '@splendor/connect-react/style.css';
import {
  SplendorConnectProvider,
  ConnectKit,
  TransferWidget,
  AccountCard,
} from '@splendor/connect-react';

<SplendorConnectProvider network="local" allowUnpinnedWasm>
  <ConnectKit />
  <AccountCard />
  <TransferWidget />
</SplendorConnectProvider>
```

Optional: `exposeInjectedProvider` installs `window.splendor` (`splendor_*` methods, not `eth_*`).

### 2. Headless / agents — createSession

```js
import { createSession } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed: process.env.SEED });
await s.sendSPLD(recipient, '0.01');
await s.token(tokenAddr).transfer(recipient, '100');
```

### 3. WalletClient style (viem-adjacent)

```js
import { createPublicClient, createWalletClient } from '@splendor/sqc-sdk';

const publicClient = createPublicClient({ network: 'local' });
const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
await wallet.connect({ mode: 'seed', seed });
await wallet.signAndSendTransaction({
  type: 'transfer',
  to: recipient,
  amountSPLD: '0.01',
});
```

```js
import { createSession, fromUnits } from '@splendor/sqc-sdk';

// Preferred: connect once → module by address → no wallet/connection args
const s = await createSession({ network: 'local', mode: 'seed', seed: process.env.SEED });
await s.sendSPLD(recipient, '0.01');
await s.token(tokenAddr).transfer(recipient, '100');
await s.token(tokenAddr).transferHuman(recipient, '1.5'); // uses on-chain decimals
await s.bridge(bridgeAddr).claim({ /* … */ });
```

**Browser demo:** [`examples/web-dapp`](examples/web-dapp) — connect, SPLD, QRC-20 / 1155, Bridge, Contract.

### Recipes (bound session)

| Goal | Snippet |
|------|---------|
| Send SPLD | `await s.sendSPLD(to, '0.01')` |
| QRC-20 transfer | `await s.token(addr).transfer(to, '100')` |
| Human token amount | `await s.token(addr).transferHuman(to, '1.5')` |
| Deploy + use token | `const { token } = await s.deployToken({ name, symbol, decimals: 18, totalSupply })` |
| Bridge claim | `await s.bridge(addr).claim({ id, asset, amount, recipient, evmTx, attestations })` |
| QRC-1155 mint | `await s.collection(addr).mint(to, id, amount)` |
| Raw units helper | `fromUnits('1.5', 18)` / `toUnits(raw, 18)` |

Advanced (unbound): `modules.qrc20(connection, addr).transfer(wallet, to, amt)` still works.

DEX / Exchange: import from `@splendor/sqc-sdk/defi` only (not Session / root / ConnectKit). Node does not route them yet.

Native modules are also on subpaths `/tokens`, `/defi`, `/bridge`. See [SDK_ARCHITECTURE.md](docs/SDK_ARCHITECTURE.md).

## What is built (SDK scope)

| Capability | Status |
|------------|--------|
| Native ML-DSA-65 seed wallet (WASM) | Built |
| Transfers, deploy, call (Tx types 0x01–0x03) | Built |
| QRC-20 / QRC-1155 module clients | Built |
| QRC-721 module client | Thin — routed actions only (`deploy`, `mint`, `transfer`, `burn`, reads) |
| DEX + Exchange Program clients | Leftover `/defi` only — not node-routed |
| On-chain Bridge module client | Built (`/bridge`; ≠ `@splendor/pq-bridge` shim) |
| Bytecode contracts (`/contracts`) | Built |
| Explorer reads + `/ws` subscribe | Built |
| `modules` factory registry | Built |
| Connect UI (`@splendor/connect-react`) | Built — native PQ first |
| Browser wallet extension | Built — `ExtensionSigner` / `connect: 'extension'` |
| Legacy root APIs (`SqRpc`, `connectSplendor`, EVM derive helpers) | Deprecated — prefer `Connection` / `createSession`; kept for semver |
| Desktop / mobile wallet apps | Not in this package |
| Python / Go SDKs | Not published |
| QSol compile-from-source client | Not in SDK — use repo `sqc-solc` CLI for bytecode |

## Do not use in new code

These stay exported so existing apps keep building. Removing them is a major-version change.

| Export | Use instead |
|--------|-------------|
| `SplendorPQBridge` (default export), `Wallet` built by hand | `createSession` (headless) or `@splendor/connect-react` (UI) |
| `connectSplendor`, `createSplendorClient`, `SplendorClient` | `createSession` |
| `SqRpc`, `SqcProvider` | `Connection` |
| `seedFromEvmSignature` and the other `derive.ts` EVM helpers | Nothing — EVM→PQ unlock was removed |
| `@splendor/sqc-sdk/defi` (DEX, Exchange) | Nothing — not routed by the node |
| `@splendor/sqc-sdk/react` (`useSplendorPQ`) | `@splendor/connect-react` |
| `@splendor/pq-bridge` | `@splendor/sqc-sdk` |

Errors: branch on `BridgeError.code` and show `userHint`. Every code is listed in [`docs/ERRORS.md`](docs/ERRORS.md).

## Protocol (SQDP-1)

All dApps MUST follow the **Splendor dApp Protocol**:

- Normative: [`docs/SQDP-1.md`](docs/SQDP-1.md)
- Conformance checklist: [`docs/SQDP-1-CONFORMANCE.md`](docs/SQDP-1-CONFORMANCE.md)
- Constants: `import { PROTOCOL, SQDP_VERSION } from '@splendor/sqc-sdk'`

### Single config (networks + fees + ports)

**Edit one file:** [`src/config/sqc.networks.json`](src/config/sqc.networks.json) (mirrored at [`config/sqc.networks.json`](config/sqc.networks.json)).

Set RPC URLs, gateway port, WASM paths, min fee, default network — SDK, gateway, and Docker all read this.

### One-touch connect (dApps)

**React UI kit (recommended for browsers):** [`@splendor/connect-react`](../connect-react) — `ConnectKit` + modular connectors (Splendor extension + native seed).

**Headless (recommended):** `createSession` — bound modules, no wallet args on writes.

```js
import { createSession } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed: process.env.SEED });
await s.sendSPLD(recipient, '0.01');
```

Legacy (deprecated — prefer `createSession` / ConnectKit):

```js
import { connectSplendor, createSplendorClient } from '@splendor/sqc-sdk';

// Reads only — no signer unlocked yet
const { provider, wallet, network } = createSplendorClient({ network: 'local' });

// Or unlock in one call (headless) — native ML-DSA-65
const session = await connectSplendor({
  network: 'local',       // local | testnet | mainnet
  connect: 'seed',        // 'none' | 'seed' | 'extension'
  seed: process.env.SQC_SEED,   // 32-byte hex, post-quantum entropy
});
console.log(session.info.address, session.info.postQuantumSecure); // → true
```

Preferred beginner API:

```js
import { connect } from '@splendor/sqc-sdk';

const { wallet, connection } = await connect({
  network: 'local',
  mode: 'seed',
  seed: process.env.SQC_SEED,
});
```

## Why WASM inside Node?

This chain signs with **ML-DSA-65 (NIST FIPS 204)** via Cloudflare circl, using per-purpose domain-separation context strings (`SPLENDOR-QC-TX-v1` for transactions). A generic ML-DSA library will not produce accepted signatures unless it uses the identical context and the identical Go `encoding/json` body encoding. The Go WASM module guarantees both.

```
Your app (Node / browser)
    └─ @splendor/sqc-sdk
           └─ sqc_signer.wasm  (Go: core/crypto + core/types)
                  └─ POST /tx/submit → SQC node
```

On-chain public-key signatures use ML-DSA-65. The SDK also uses quantum-resistant hashing (SHA3-256). Accounts unlock via a native seed or the Splendor browser extension — MetaMask / ECDSA derivation is no longer offered.

## Native ML-DSA-65 (recommended)

1. Generate 32 bytes from a CSPRNG — that seed **is** the account.
2. `ML-DSA-65 keypair = mldsa65.NewKeyFromSeed(seed)`
3. `SQ address = "SQ:" + hex(SHA3-256(pubkey))`

No Ethereum key stands behind the address, so its security rests on ML-DSA-65
alone: `connectFromSeed` returns `postQuantumSecure: true` and
`keyOrigin: 'native'`. Browsers get the same thing through
[`@splendor/connect-react`](../connect-react) (create or import, first item in
the modal); the CLI `sqc-wallet` keeps its own encrypted `.sqwallet` key file.

## EVM → PQ mapping (removed)

ECDSA / MetaMask derivation (`connectFromMetaMask`, `evmKey`, `evmSignature`) was removed in the Phase 0 hardening cleanup. Use `connectFromSeed` or `connect({ mode: 'extension' })` instead.

Low-level `derive.ts` helpers (`seedFromEvmSignature`, recover helpers, etc.) remain exported as **`@deprecated` offline tooling only** — do not use them in dApps; ConnectKit no longer offers EVM→PQ unlock.

## Install / build

### Prerequisites

- Node.js ≥ 22.20 (see `package.json` `engines`)
- Go ≥ 1.21 (only to build the WASM signer once; the build script pins the exact release it compiles with)

```powershell
cd sdk/sqc-sdk
npm install

# Build WASM (requires Go)
./wasm/build.ps1   # Windows
# or: bash ./wasm/build.sh

npm run build:ts
```

Artifacts:

- `wasm/dist/sqc_signer.wasm`
- `wasm/dist/wasm_exec.js`
- `dist/*.js` (TypeScript SDK)

### Reproducing the signer digest

`sqc_signer.wasm` is content-pinned: browsers and Node both verify `wasmSha256`
before instantiating, so the bytes must be reproducible by anyone, not just by
whoever last built them. The digest is Go-release-sensitive — the same source
under go1.25.0 and go1.26.2 differs by ~166 KB — so `wasm/build.sh` and
`wasm/build.ps1` set `GOTOOLCHAIN=go1.25.0` (the floor `go.mod` declares, and the
family the node `Dockerfile` builds with) and pass `-trimpath`. Any Go 1.21+
will fetch and use that release automatically. Override only to deliberately
move the pin:

```bash
SQC_GO_TOOLCHAIN=go1.26.2 bash ./wasm/build.sh
```

After any rebuild, refresh every consumer of those bytes or they will fail
closed:

```bash
npm run sync:config                              # rewrites wasmSha256/execSha256
```

The browser wallet extension lives in its own repository
(`splendor-quantum-wallet-extension`) and re-pins from the digest published
here; it is no longer rebuilt from this tree.

`wasm/dist` is not in git. `prepack` builds the signer under the toolchain
`wasm/build.sh` pins, re-pins `sqc.networks.json` from those bytes and refuses
to publish if they still disagree, so `npm publish` always ships a signer built
from the source in this tree. Working from a checkout, run
`bash wasm/build.sh` once before `npm test` or a gateway `docker compose build`.

## Node usage

```js
import { Wallet, SqcProvider, connect } from '@splendor/sqc-sdk';

// Read-only first (no WASM)
const provider = new SqcProvider({
  rpcUrls: ['http://localhost:8080'],
});

const wallet = Wallet.fromProvider(provider);

// From a 32-byte ML-DSA-65 seed hex (native — backends / agents)
await wallet.connectFromSeed(process.env.SQC_SEED);

console.log(wallet.getAddress());
console.log(await wallet.getBalance());

await wallet.sendTransfer({
  to: 'SQ:...',
  amountSPLD: '0.01',
  waitForConfirmation: true,
});

wallet.disconnect();
```

`SplendorPQBridge` remains an alias for `Wallet`.

Example script:

```bash
PQ_SEED=<64-hex> SQC_RPC=http://localhost:8080 npm run example:node
```

## Browser

```js
import { Wallet } from '@splendor/sqc-sdk/browser';

const wallet = new Wallet({
  rpcUrl: 'https://rpc.example',
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});

// Native ML-DSA-65 seed the user created or imported
await wallet.connectFromSeed(seedHex);
await wallet.sendTransfer({ to: 'SQ:...', amountSPLD: '1' });
```

Prefer [`@splendor/connect-react`](../connect-react) for the browser — extension + seed connectors. See `examples/demo.html`.

## Contracts and QSol ABI

- **Calling convention:** SHA3-256 selectors, `sqaddress` (not Solidity `address`), 32-byte words — see SQDP-1 §5.
- **Deploy/call:** `@splendor/sqc-sdk/contracts` (`Contract`, `deployBytecode`, `getContract`).
- **Compiler:** A prototype QSol-subset compiler (`compiler/`, `cmd/sqc-solc`) lives in the monorepo and emits SQC-VM bytecode. This SDK does **not** embed a compile API yet — deploy pre-built bytecode or use `sqc-solc` offline.

## Deployable gateway

Separate Node service (Docker-ready) that talks to chain RPC only.

```bash
cd sdk/sqc-sdk/gateway
# Ensure wasm/dist exists (build from parent)
docker compose up --build
# listens on :3090
```

Env:

| Variable | Default | Meaning |
|----------|---------|---------|
| `SQC_RPC` | from network config | Chain node RPC |
| `MODE` | `proxy` | `proxy` or `service-wallet` |
| `ALLOW_CUSTODIAL_PROXY` | unset | Set `1` only on private backends to allow seed unlock (refused with CORS `*`); prefer `signedTx` |
| `GATEWAY_API_KEY` | — | Required for service-wallet mutating routes when set |
| `CORS_ORIGINS` | `http://localhost:3000,http://127.0.0.1:3000` | Comma-separated allowlist (not `*` by default) |
| `PUBLIC_HEALTH` | `1` on local, else unset/`0` | Set `1` to expose `sqc_rpc` on `/health` |
| `TRUST_PROXY` | unset/`false` | Set `1` only behind a known LB |
| `RATE_LIMIT_MAX` | `120` | Global requests / minute |
| `RATE_LIMIT_TX_MAX` | `30` | Stricter cap for `/v1/tx/*`, `/v1/derive` |
| `SERVICE_SEED` | — | 32-byte hex ML-DSA-65 seed (service-wallet) |
| `PORT` | `3090` | HTTP port |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | — | Serve TLS 1.3 directly |
| `ALLOW_CLASSICAL_TLS` | unset | Only if you terminate PQ TLS elsewhere |

### Modes

- **`proxy` (default, non-custodial):** clients sign locally; gateway accepts **`signedTx` only**.
- **`service-wallet`:** gateway holds one PQ wallet for backends / AI agents / faucets (protect with `GATEWAY_API_KEY`).

### HTTP API

- `GET /health` — liveness
- `GET /ready` — readiness (probes upstream `chain_id`)
- `GET /v1/status`
- `GET /v1/balance/:address`
- `GET /v1/nonce/:address`
- `POST /v1/derive` — disabled on public proxy; private backends only with `ALLOW_CUSTODIAL_PROXY=1`
- `POST /v1/tx/submit` — `{ signedTx }` with required `chain_id` (preferred)
- `POST /v1/tx/transfer` — transfer (`signedTx` preferred)
- `POST /v1/tx/call`

## Package layout

```
sdk/sqc-sdk/
  signer/          Go derive + sign (reuses core/crypto, core/types)
  wasm/            Go→WASM binding + build scripts
  src/             TypeScript SDK (core, wallet, tokens, contracts, …)
  gateway/         Fastify deployable server + Docker
  examples/        Node script + browser HTML demo
  docs/            SQDP-1, architecture, dApp guide
```

## dApp helpers

- **`sqc.networks.json`** — single source for RPC / gateway / fees / WASM paths / `networkId` / optional `wasmSha256`
- **`createSession` / `connect`** — preferred one-touch dApp entry (`createSession` binds modules via `withSigner`)
- **`connectSplendor` / `createSplendorClient`** — deprecated aliases; prefer `createSession`
- **`@splendor/sqc-sdk/react`** — `useSplendorPQ` (deprecated; prefer `@splendor/connect-react`)
- **`@splendor/connect-react`** — AppKit-style Connect UI (native PQ first)
- **Browser demo** — [`examples/web-dapp`](examples/web-dapp) (balance, send, QRC-20 read/write)
- **`preflight()`** — balance / nonce / fee dry-run before send
- **SQDP-1** — normative protocol + `PROTOCOL` / `SQDP_VERSION` exports
- **`Connection`** — preferred read client (Transport-backed); `SqcProvider` deprecated alias
- **`Wallet` / `SplendorPQBridge`** — signing wallet (`Wallet` preferred name)
- **QSol ABI** — `encodeFunctionData` / decode helpers (SHA3-256 selectors, `sqaddress`)
- **Events** — `connect` / `disconnect` / `accountsChanged` / `error` / `lock`
- **`rpcUrls` + retries** — failover; sticky submit URL
- Guide: [`docs/DAPP_INTEGRATION.md`](docs/DAPP_INTEGRATION.md) · Protocol: [`docs/SQDP-1.md`](docs/SQDP-1.md)

```bash
npm run sync:config    # mirror networks JSON + check Go domain message
npm test               # vitest unit + WASM session + phase2
npm run test:e2e       # offline smoke
npm run test:live      # optional live node (skips if RPC down; LIVE_E2E=1 to require)
# from repo root:
go test ./sdk/sqc-sdk/signer -count=1
```

## Security notes

- Prefer **proxy** mode for end users; never put user EVM keys or ML-DSA-65 seeds on a public gateway.
- `service-wallet` is for **your** automation keys only.
- Domain message is versioned (`v1`); changing it remaps all derived addresses — treat as a breaking change.
- This is an **identity bridge**, not a cross-chain token bridge.
- Pin `wasmSha256` / `execSha256` in production browser deploys (required for non-local browser unless `allowUnpinnedWasm`).
- Non-local RPC/gateway URLs must be `https://` unless `allowInsecureHttp: true`. `SqRpc` refuses HTTP redirects, blocks cloud-metadata and link-local hosts (including IPv4-mapped IPv6 and `fd00:ec2::254`) in Node unless `allowPrivateRpcHosts`, and error details carry only the RPC origin.
- Hosting WASM assets: see [`docs/WASM_HOSTING.md`](docs/WASM_HOSTING.md).
- **CSP:** the Go WASM runtime needs `script-src 'self' 'wasm-unsafe-eval'` (`'unsafe-eval'` on older browsers). Add `frame-ancestors 'none'` so other sites cannot frame your signing UI.
- **ABI strings:** `"42"` is decimal; hex must be `0x`-prefixed. Bare hex and free text throw instead of being guessed.
- **Amounts and fees:** raw amounts must be non-negative integers in base units. Set `maxFee` on the wallet to cap any fee a caller passes; `payX402` accepts `maxAmount` and defaults `valid_before` to now + 300 s.
- **Seeds:** `Keypair` never serializes its seed (`JSON.stringify`, `console.log`), the wallet drops a `seed` passed in its options, and WASM exports are removed from `globalThis` after load.
- **Explorer links:** `txExplorerUrl` only links 64-hex hashes, and `registerNetwork` rejects non-`https` explorer URLs (loopback `http` is allowed).
- **Local chain id:** the bundled `local` network shares the mainnet chain id, so anything signed against it is valid on mainnet. Never use a mainnet seed on a local node.
- **x402 servers:** `createX402Middleware` consumes each payment for exactly one request. The default store is in-memory; pass a durable `consumptionStore` with `putIfAbsent` when you run more than one instance.

### Server sign-in

Never trust a signature checked only in the browser. Issue a single-use nonce on the server, then verify the signed message there:

```ts
import { createMemoryNonceStore, verifySignIn } from '@splendor/sqc-sdk/auth';

const nonces = createMemoryNonceStore(); // use Redis / a DB row with atomic consume in production

app.get('/auth/nonce', (_req, res) => res.json({ nonce: nonces.issue() }));

app.post('/auth/verify', async (req, res) => {
  const { message, signatureB64, publicKeyB64, address } = req.body;
  const r = await verifySignIn({
    message, signatureB64, publicKeyB64, address,
    domain: 'https://app.example.com',
    chainId: 'splendor-quantum-1',
    nonceStore: nonces,
  });
  if (!r.ok) return res.status(401).json({ error: r.reason });
  // r.address derives from the public key and signed this exact message.
  res.json({ address: r.address });
});
```

`verifySignIn` checks the domain, age/expiry, address and chain lines, that the address derives from the public key (`SQ:` + SHA3-256), the ML-DSA-65 signature (pinned WASM verifier), and burns the nonce only after the signature passes.

## License

MIT
