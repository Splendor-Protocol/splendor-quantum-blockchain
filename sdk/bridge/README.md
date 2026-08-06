# Splendor PQ Bridge SDK (`@splendor/pq-bridge`)

Node.js-first bridge that maps an **EVM wallet** (MetaMask or secp256k1 key) to a **Splendor Quantum Chain Dilithium (PQ) wallet**, then signs and submits transactions.

No changes to the core blockchain. The Dilithium signer is the **same Go code the node uses**, compiled to **WASM** and loaded inside Node (or the browser).

## Protocol (SQDP-1)

All dApps MUST follow the **Splendor dApp Protocol**:

- Normative: [`docs/SQDP-1.md`](docs/SQDP-1.md)
- Conformance checklist: [`docs/SQDP-1-CONFORMANCE.md`](docs/SQDP-1-CONFORMANCE.md)
- Constants: `import { PROTOCOL, SQDP_VERSION } from '@splendor/pq-bridge'`

### Single config (networks + fees + ports)

**Edit one file:** [`src/config/sqc.networks.json`](src/config/sqc.networks.json) (mirrored at [`config/sqc.networks.json`](config/sqc.networks.json)).

Set RPC URLs, gateway port, WASM paths, min fee, default network — SDK, gateway, and Docker all read this.

### One-touch connect (dApps)

```js
import { connectSplendor, createSplendorClient } from '@splendor/pq-bridge';

// Reads only — no MetaMask yet
const { provider, wallet, network } = createSplendorClient({ network: 'local' });

// Or unlock in one call
const session = await connectSplendor({
  network: 'local',       // local | testnet | mainnet
  connect: 'metamask',    // none | metamask | seed | evmKey | evmSignature
});
console.log(session.info.address, session.network.rpcUrls);
```

## Why WASM inside Node?

This chain uses Cloudflare circl **Dilithium mode3 (round 3)**. Pure-JS ML-DSA libraries (FIPS 204) are **not wire-compatible**. The Go WASM module guarantees signatures the node will accept.

```
Your app (Node / browser)
    └─ @splendor/pq-bridge
           └─ sqc_signer.wasm  (Go: core/crypto + core/types)
                  └─ POST /tx/submit → SQC node
```

## EVM → PQ mapping

1. User signs a fixed domain message with MetaMask / EVM key:

```
Splendor Quantum Chain
Key derivation v1
chain: splendor-quantum-1
```

2. `seed = SHA3-256(signature)`
3. `Dilithium keypair = mode3.NewKeyFromSeed(seed)`
4. `SQ address = "SQ:" + hex(SHA3-256(pubkey))`

Same EVM account → same PQ wallet, every time. Non-custodial: the private Dilithium key stays in WASM memory and is **zeroized** on `lock()` / `disconnect()`.

**Important:** The derivation signature **is key material** (`seed = SHA3-256(sig)`). Never log it or post it to a public gateway. The `SQ:` address is **not** your MetaMask `0x` address. Funds live on the PQ address.

## Install / build

### Prerequisites

- Node.js ≥ 18
- Go ≥ 1.23 (only to build the WASM signer once)

```powershell
cd sdk/bridge
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

## Node usage

```js
import { SplendorPQBridge, SqcProvider } from '@splendor/pq-bridge';

// Read-only first (no WASM)
const provider = new SqcProvider({
  rpcUrls: ['http://localhost:8080'],
});

const bridge = SplendorPQBridge.fromProvider(provider);

// From EVM private key (backends / agents)
await bridge.connectFromEvmPrivateKey(process.env.EVM_KEY);

// Or from a 32-byte Dilithium seed hex
// await bridge.connectFromSeed('01'.repeat(32));

console.log(bridge.getAddress());
console.log(await bridge.getBalance());

await bridge.sendTransfer({
  to: 'SQ:...',
  amountSPLD: '0.01',
  waitForConfirmation: true,
});

bridge.disconnect();
```

Example script:

```bash
EVM_KEY=0x... SQC_RPC=http://localhost:8080 npm run example:node
```

## Browser (MetaMask)

```js
const bridge = new SplendorPQBridge({
  rpcUrl: 'https://rpc.example',
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});
await bridge.connectFromMetaMask();
await bridge.sendTransfer({ to: 'SQ:...', amountSPLD: '1' });
```

See `examples/demo.html`.

## Deployable gateway

Separate Node service (Docker-ready) that talks to chain RPC only.

```bash
cd sdk/bridge/gateway
# Ensure wasm/dist exists (build from parent)
docker compose up --build
# listens on :3090
```

Env:

| Variable | Default | Meaning |
|----------|---------|---------|
| `SQC_RPC` | from network config | Chain node RPC |
| `MODE` | `proxy` | `proxy` or `service-wallet` |
| `ALLOW_CUSTODIAL_PROXY` | unset | Set `1` only on private backends to allow seed/evmSignature |
| `GATEWAY_API_KEY` | — | Required for service-wallet mutating routes when set |
| `CORS_ORIGINS` | `*` | Comma-separated allowlist |
| `PUBLIC_HEALTH` | `1` | Set `0` to hide `sqc_rpc` from `/health` |
| `SERVICE_SEED` | — | 32-byte hex Dilithium seed (service-wallet) |
| `SERVICE_EVM_KEY` | — | EVM key to derive service PQ wallet |
| `PORT` | `3090` | HTTP port |

### Modes

- **`proxy` (default, non-custodial):** clients sign locally; gateway accepts **`signedTx` only** (rejects seed / private key / signature unless `ALLOW_CUSTODIAL_PROXY=1`).
- **`service-wallet`:** gateway holds one PQ wallet for backends / AI agents / faucets (protect with `GATEWAY_API_KEY`).

### HTTP API

- `GET /health`
- `GET /v1/status`
- `GET /v1/balance/:address`
- `GET /v1/nonce/:address`
- `POST /v1/derive` — disabled on public proxy; private backends only with `ALLOW_CUSTODIAL_PROXY=1`
- `POST /v1/tx/submit` — `{ signedTx }` (preferred)
- `POST /v1/tx/transfer` — transfer (`signedTx` preferred)
- `POST /v1/tx/call`
- `POST /v1/x402/pay`

## Package layout

```
sdk/bridge/
  signer/          Go derive + sign (reuses core/crypto, core/types)
  wasm/            Go→WASM binding + build scripts
  src/             TypeScript SDK
  gateway/         Fastify deployable server + Docker
  examples/        Node script + MetaMask HTML demo
```

## dApp helpers (v1.3.1)

- **`sqc.networks.json`** — single source for RPC / gateway / fees / WASM paths / `networkId` / optional `wasmSha256`
- **`connectSplendor` / `createSplendorClient`** — one-touch dApp entry (EIP-1193 `ethereumProvider`, optional EIP-712 derivation)
- **`@splendor/pq-bridge/react`** — published `useSplendorPQ` hook
- **`preflight()`** — balance / nonce / fee dry-run before send
- **SQDP-1** — normative protocol + `PROTOCOL` / `SQDP_VERSION` exports
- **`SqcProvider`** — read-only RPC client (no WASM until connect)
- **`SplendorPQBridge.fromProvider(provider)`** — signing wallet sharing RPC
- **QSol ABI** — `encodeFunctionData` / decode helpers (SHA3-256 selectors)
- **Events** — `connect` / `disconnect` / `accountsChanged` / `error`
- **`rpcUrls` + retries** — failover; sticky submit URL
- Canonical `SQ:` addresses; gateway errors `{ code, message }`
- Guide: [`docs/DAPP_INTEGRATION.md`](docs/DAPP_INTEGRATION.md) · Protocol: [`docs/SQDP-1.md`](docs/SQDP-1.md)


```bash
npm run sync:config    # mirror networks JSON + check Go domain message
npm test               # vitest unit + WASM session + phase2
npm run test:e2e       # offline smoke
npm run test:live      # optional live node (skips if RPC down; LIVE_E2E=1 to require)
# from repo root:
go test ./sdk/bridge/signer -count=1
```


- Prefer **proxy** mode for end users; never put user EVM keys or Dilithium seeds on a public gateway.
- `service-wallet` is for **your** automation keys only.
- Domain message is versioned (`v1`); changing it remaps all derived addresses — treat as a breaking change.
- This is an **identity bridge**, not a cross-chain token bridge.
- Pin `wasmSha256` in production browser deploys.
## License

MIT
