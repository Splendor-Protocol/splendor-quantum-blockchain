# Splendor SQC SDK (`@splendor/sqc-sdk`)

Unified SDK for Splendor Quantum Chain — wallet, QRC-20 tokens, native modules, and dApp integration.

Node.js-first SDK that holds a **Splendor Quantum Chain ML-DSA-65 (PQ) wallet**, signs with it, and submits transactions. **`@splendor/pq-bridge`** is a deprecated re-export shim.

```js
import { connect, deployQRC20 } from '@splendor/sqc-sdk';

const { wallet } = await connect({ network: 'local', mode: 'seed', seed: process.env.SEED });
const { address } = await deployQRC20(wallet, {
  name: 'My Token', symbol: 'MTK', decimals: 18, totalSupply: '1000000',
});
```

See [SDK_ARCHITECTURE.md](docs/SDK_ARCHITECTURE.md) for package layout and extension points.

The primary path is a **native ML-DSA-65 seed** (`connect: 'seed'` / `connectFromSeed`) — 32 bytes of post-quantum entropy, no secp256k1 anywhere. The bridge can also map an **EVM wallet** (MetaMask or secp256k1 key) onto a PQ wallet; that path is kept for onboarding convenience and is **not quantum-safe** ([why](#the-evm-path-is-not-quantum-safe)).

No changes to the core blockchain. The ML-DSA-65 signer is the **same Go code the node uses**, compiled to **WASM** and loaded inside Node (or the browser).

## Protocol (SQDP-1)

All dApps MUST follow the **Splendor dApp Protocol**:

- Normative: [`docs/SQDP-1.md`](docs/SQDP-1.md)
- Conformance checklist: [`docs/SQDP-1-CONFORMANCE.md`](docs/SQDP-1-CONFORMANCE.md)
- Constants: `import { PROTOCOL, SQDP_VERSION } from '@splendor/pq-bridge'`

### Single config (networks + fees + ports)

**Edit one file:** [`src/config/sqc.networks.json`](src/config/sqc.networks.json) (mirrored at [`config/sqc.networks.json`](config/sqc.networks.json)).

Set RPC URLs, gateway port, WASM paths, min fee, default network — SDK, gateway, and Docker all read this.

### One-touch connect (dApps)

**React UI kit (recommended for browsers):** [`@splendor/connect-react`](../connect-react) — `ConnectKit` + modular connectors.

```js
import { connectSplendor, createSplendorClient } from '@splendor/pq-bridge';

// Reads only — no signer unlocked yet
const { provider, wallet, network } = createSplendorClient({ network: 'local' });

// Or unlock in one call (headless) — native ML-DSA-65, no EVM key involved
const session = await connectSplendor({
  network: 'local',       // local | testnet | mainnet
  connect: 'seed',        // seed (native, recommended) | none | evmKey | evmSignature | metamask
  seed: process.env.SQC_SEED,   // 32-byte hex, post-quantum entropy
});
console.log(session.info.address, session.info.postQuantumSecure); // → true
```

Legacy fallback for a user whose funds already sit on an EVM-derived address —
`postQuantumSecure: false`, and the `onWarning` callback fires:

```js
const session = await connectSplendor({ network: 'local', connect: 'metamask' });
```

## Why WASM inside Node?

This chain signs with **ML-DSA-65 (NIST FIPS 204)** via Cloudflare circl, using per-purpose domain-separation context strings (`SPLENDOR-QC-TX-v1` for transactions). A generic ML-DSA library will not produce accepted signatures unless it uses the identical context and the identical Go `encoding/json` body encoding. The Go WASM module guarantees both.

```
Your app (Node / browser)
    └─ @splendor/pq-bridge
           └─ sqc_signer.wasm  (Go: core/crypto + core/types)
                  └─ POST /tx/submit → SQC node
```

## Native ML-DSA-65 (recommended)

1. Generate 32 bytes from a CSPRNG — that seed **is** the account.
2. `ML-DSA-65 keypair = mldsa65.NewKeyFromSeed(seed)`
3. `SQ address = "SQ:" + hex(SHA3-256(pubkey))`

No Ethereum key stands behind the address, so its security rests on ML-DSA-65
alone: `connectFromSeed` returns `postQuantumSecure: true` and
`keyOrigin: 'native'`. Browsers get the same thing through
[`@splendor/connect-react`](../connect-react) (create or import, first item in
the modal); the CLI `sqc-wallet` keeps its own encrypted `.sqwallet` key file.

## EVM → PQ mapping (legacy)

1. User signs a fixed domain message with MetaMask / EVM key:

```
Splendor Quantum Chain
Key derivation v1
chain: splendor-quantum-1
```

2. `seed = SHA3-256(signature)`
3. `ML-DSA-65 keypair = mldsa65.NewKeyFromSeed(seed)`
4. `SQ address = "SQ:" + hex(SHA3-256(pubkey))`

Same EVM account → same PQ wallet, every time. Non-custodial: the private ML-DSA key stays in WASM memory and is **zeroized** on `lock()` / `disconnect()`.

> ### The EVM path is not quantum-safe
>
> Transactions are signed with ML-DSA-65, but the key is derived from an ECDSA
> signature, so the account is only as strong as secp256k1. A quantum adversary
> can recover the EVM private key from any signature that account has published,
> replay the deterministic derivation message, and reproduce this ML-DSA key.
>
> `connectFromMetaMask` / `connectFromEvmPrivateKey` therefore return
> `postQuantumSecure: false` and `keyOrigin: 'evm-derived'`, and fire the
> `onWarning` callback. Use `connectFromSeed` with post-quantum entropy for any
> account that holds value — it returns `postQuantumSecure: true`.

**Important:** The derivation signature **is key material** (`seed = SHA3-256(sig)`). Never log it or post it to a public gateway. The `SQ:` address is **not** your MetaMask `0x` address. Funds live on the PQ address.

## Install / build

### Prerequisites

- Node.js ≥ 18
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

`npm run prepublishOnly` refuses to publish when `wasm/dist` and the pins in
`sqc.networks.json` disagree.

## Node usage

```js
import { SplendorPQBridge, SqcProvider } from '@splendor/pq-bridge';

// Read-only first (no WASM)
const provider = new SqcProvider({
  rpcUrls: ['http://localhost:8080'],
});

const bridge = SplendorPQBridge.fromProvider(provider);

// From a 32-byte ML-DSA-65 seed hex (native — backends / agents)
await bridge.connectFromSeed(process.env.SQC_SEED);

// Legacy: derive from an EVM private key (not post-quantum)
// await bridge.connectFromEvmPrivateKey(process.env.EVM_KEY);

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

## Browser

```js
const bridge = new SplendorPQBridge({
  rpcUrl: 'https://rpc.example',
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});

// Native ML-DSA-65 seed the user created or imported
await bridge.connectFromSeed(seedHex);
await bridge.sendTransfer({ to: 'SQ:...', amountSPLD: '1' });

// Legacy fallback (labelled "not post-quantum" in the UI kit)
// await bridge.connectFromMetaMask();
```

Prefer [`@splendor/connect-react`](../connect-react) for the browser — it ships
the create/import UI and the legacy labelling. See `examples/demo.html`.

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
| `ALLOW_CUSTODIAL_PROXY` | unset | Set `1` only on private backends to allow seed/evmSignature (refused with CORS `*`) |
| `GATEWAY_API_KEY` | — | Required for service-wallet mutating routes when set |
| `CORS_ORIGINS` | `http://localhost:3000,http://127.0.0.1:3000` | Comma-separated allowlist (not `*` by default) |
| `PUBLIC_HEALTH` | `1` on local, else unset/`0` | Set `1` to expose `sqc_rpc` on `/health` |
| `TRUST_PROXY` | unset/`false` | Set `1` only behind a known LB (spoofed `X-Forwarded-For` otherwise bypasses rate limits) |
| `RATE_LIMIT_MAX` | `120` | Global requests / minute |
| `RATE_LIMIT_TX_MAX` | `30` | Stricter cap for `/v1/tx/*`, `/v1/derive`, `/v1/x402/pay` |
| `SERVICE_SEED` | — | 32-byte hex ML-DSA-65 seed (service-wallet) |
| `SERVICE_EVM_KEY` | — | EVM key to derive service PQ wallet |
| `PORT` | `3090` | HTTP port |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | — | Serve TLS 1.3 directly. Both or neither; one without the other is refused rather than silently downgraded to HTTP |
| `ALLOW_CLASSICAL_TLS` | unset | Serve TLS even where the Node build cannot do `X25519MLKEM768` (needs OpenSSL 3.5+). Without it the gateway refuses to start, because a classical-only handshake means recorded traffic is decryptable once a CRQC exists — and that is the property this gateway exists to provide. Set `1` only if you terminate PQ TLS elsewhere |

### Modes

- **`proxy` (default, non-custodial):** clients sign locally; gateway accepts **`signedTx` only** (rejects seed / private key / signature unless `ALLOW_CUSTODIAL_PROXY=1`).
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
- `POST /v1/x402/pay`

## Package layout

```
sdk/sqc-sdk/
  signer/          Go derive + sign (reuses core/crypto, core/types)
  wasm/            Go→WASM binding + build scripts
  src/             TypeScript SDK
  gateway/         Fastify deployable server + Docker
  examples/        Node script + MetaMask HTML demo
```

## dApp helpers (v1.3.1)

- **`sqc.networks.json`** — single source for RPC / gateway / fees / WASM paths / `networkId` / optional `wasmSha256`
- **`connectSplendor` / `createSplendorClient`** — one-touch dApp entry (`connect: 'seed'` for native ML-DSA; EIP-1193 `ethereumProvider` and optional EIP-712 derivation for the legacy path)
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
go test ./sdk/sqc-sdk/signer -count=1
```


- Prefer **proxy** mode for end users; never put user EVM keys or ML-DSA-65 seeds on a public gateway.
- `service-wallet` is for **your** automation keys only.
- Domain message is versioned (`v1`); changing it remaps all derived addresses — treat as a breaking change.
- This is an **identity bridge**, not a cross-chain token bridge.
- Pin `wasmSha256` / `execSha256` in production browser deploys (required for non-local browser unless `allowUnpinnedWasm`).
- Non-local RPC/gateway URLs must be `https://` unless `allowInsecureHttp: true`.
- Hosting WASM assets: see [`docs/WASM_HOSTING.md`](docs/WASM_HOSTING.md).
## License

MIT
