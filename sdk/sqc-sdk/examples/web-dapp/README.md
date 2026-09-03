# Splendor SQC — SDK web demo

Small Vite + React app that exercises `@splendor/sqc-sdk` + `@splendor/connect-react`:

1. **Wallet connect** — native ML-DSA-65 (Create / Import) via ConnectKit  
2. **Receive** — show + copy your `SQ:` address  
3. **Balance** — native SPLD with refresh  
4. **Send** — native SPLD transfer  
5. **QRC-20** — paste token address; read + transfer / approve  
6. **DEX** — paste pool address; read reserves + swap  
7. **Bridge** — paste bridge address; read info + claim  

## Prerequisites

- Node.js ≥ 18  
- Local Splendor node RPC at `http://127.0.0.1:8080` (or set network via env)  
- Built packages:

```bash
cd sdk/sqc-sdk && npm install && npm run build
cd ../connect-react && npm install && npm run build
```

## Run

```bash
cd sdk/sqc-sdk/examples/web-dapp
npm install
npm run dev
```

Open http://localhost:5174

Optional: `VITE_SQC_NETWORK=local` (default).

## Manual test checklist

1. **Connect** → Create native wallet → see `SQ:` address  
2. Fund the address (faucet / CLI) → **Refresh** balance  
3. **Send** SPLD to another `SQ:` → hash + balance drop  
4. Paste a deployed **QRC-20** → Load → transfer / approve  
5. Paste a **DEX pool** → reserves + swap  
6. Paste a **Bridge** → info + claim (needs valid attestations on real nets)  

## Headless equivalent (no UI)

```ts
import { createSession } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed: process.env.SEED });
await s.sendSPLD('SQ:…', '0.01');
await s.token('SQ:…token').transfer('SQ:…', '100');
await s.modules.dex('SQ:…pool').swap({ tokenIn: 0, amountIn: '100', minAmountOut: '1' });
```

## Notes

- WASM is copied by `sqcWasmPlugin()`; this demo uses `allowUnpinnedWasm` for monorepo rebuilds. Production apps must pin `wasmSha256`.  
- Signing is ML-DSA-65 in WASM — not MetaMask for chain txs.
