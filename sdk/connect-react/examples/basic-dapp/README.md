# Basic dApp example

Minimal Vite + React app using `@splendor/connect-react`.

## Run

From repo root (build packages first):

```bash
cd sdk/bridge && npm install && npm run build
cd ../connect-react && npm install && npm run build
cd examples/basic-dapp && npm install && npm run dev
```

Serve WASM from `sdk/bridge/wasm/dist` (copied to Vite public root via `vite-plugin-static-copy`).

Open http://localhost:5173 — use **Connect** (MetaMask) or **Advanced → seed**.
