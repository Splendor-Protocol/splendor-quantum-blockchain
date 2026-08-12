# Basic dApp example

Minimal Vite + React app using `@splendor/connect-react`.

## Run

From repo root (build packages first):

```bash
cd sdk/bridge && npm install && npm run build
cd ../connect-react && npm install && npm run build
cd examples/basic-dapp && npm install && npm run dev
```

WASM is copied from `sdk/bridge/wasm/dist` into the Vite root via `vite-plugin-static-copy`.
Alternatively, from the example folder:

```bash
node ../../scripts/copy-wasm-assets.mjs ./public
```

Open http://localhost:5173 — use **Connect** (MetaMask) or **Advanced → seed**.

`autoReconnect` is enabled: a previously authorized MetaMask session can restore without a cold-prompt when `eth_accounts` is non-empty.
