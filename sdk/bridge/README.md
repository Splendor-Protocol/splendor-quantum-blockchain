# Splendor PQ Bridge SDK (`@splendor/pq-bridge`)

Node.js-first bridge that maps an **EVM wallet** (MetaMask or secp256k1 key) to a **Splendor Quantum Chain Dilithium (PQ) wallet**, then signs and submits transactions.

No changes to the core blockchain. The Dilithium signer is the **same Go code the node uses**, compiled to **WASM** and loaded inside Node (or the browser).

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

Same EVM account → same PQ wallet, every time. Non-custodial: the private Dilithium key stays in WASM memory and is wiped on `lock()`.

**Important:** The `SQ:` address is **not** your MetaMask `0x` address. Funds live on the PQ address.

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
import { SplendorPQBridge } from '@splendor/pq-bridge';

const bridge = new SplendorPQBridge({ rpcUrl: 'http://localhost:8080' });

// From EVM private key (backends / agents)
await bridge.connectFromEvmPrivateKey(process.env.EVM_KEY);

// Or from a 32-byte Dilithium seed hex
// await bridge.connectFromSeed('01'.repeat(32));

console.log(bridge.getAddress());
console.log(await bridge.getBalance());

await bridge.sendTransfer({
  to: 'SQ:...',
  amountSPLD: '0.01',
});

bridge.lock();
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
| `SQC_RPC` | `http://localhost:8080` | Chain node RPC |
| `MODE` | `proxy` | `proxy` or `service-wallet` |
| `SERVICE_SEED` | — | 32-byte hex Dilithium seed (service-wallet) |
| `SERVICE_EVM_KEY` | — | EVM key to derive service PQ wallet |
| `PORT` | `3090` | HTTP port |

### Modes

- **`proxy` (default, non-custodial):** clients sign (or send one-shot credentials); gateway relays to the chain.
- **`service-wallet`:** gateway holds one PQ wallet for backends / AI agents / faucets.

### HTTP API

- `GET /health`
- `GET /v1/status`
- `GET /v1/balance/:address`
- `GET /v1/nonce/:address`
- `POST /v1/derive` — `{ evmSignature | seed | evmPrivateKey }`
- `POST /v1/tx/submit` — `{ signedTx }`
- `POST /v1/tx/transfer` — transfer (signedTx or credentials + to/amount)
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

## Security notes

- Prefer **proxy** mode for end users; never put user EVM keys on a public gateway.
- `service-wallet` is for **your** automation keys only.
- Domain message is versioned (`v1`); changing it remaps all derived addresses — treat as a breaking change.
- This is an **identity bridge**, not a cross-chain token bridge.

## License

MIT
