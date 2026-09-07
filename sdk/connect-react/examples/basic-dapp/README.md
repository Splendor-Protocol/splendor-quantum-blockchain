# Basic dApp example

Minimal Vite + React app using `@splendor/connect-react`.

## Run

```bash
cd sdk/sqc-sdk && npm install && npm run build
cd ../connect-react && npm install && npm run build
cd examples/basic-dapp && npm install && npm run dev
```

WASM is copied automatically by `sqcWasmPlugin()` from `@splendor/connect-react/vite`.

Open http://localhost:5173 — **Connect** → create a new ML-DSA-65 wallet or import a 32-byte seed. MetaMask stays available under **Legacy** (labelled not post-quantum). Use the network badge to switch among selectable networks (local by default; placeholder testnet/mainnet URLs are filtered out).

`autoReconnect` restores an authorized MetaMask session via `eth_accounts` without a cold prompt; a native seed is typed in each session and is never persisted.
