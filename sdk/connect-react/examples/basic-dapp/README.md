# Basic dApp example

Minimal Vite + React app using `@splendor/connect-react` — reference kitchen-sink for shipped widgets.

## Run

```bash
cd sdk/sqc-sdk && npm install && npm run build
cd ../connect-react && npm install && npm run build
cd examples/basic-dapp && npm install && npm run dev
```

WASM is copied automatically by `sqcWasmPlugin()` from `@splendor/connect-react/vite`.

Open http://localhost:5173 — **Connect** → Splendor extension (when installed) or create/import an ML-DSA-65 seed. Includes:

- `ConnectKit`
- `AccountCard`, `TransferWidget`, `TokenPanel`, `DeployTokenWidget`
- `TxToastHost`, `WrongNetworkBanner`

`autoReconnect` restores an authorized extension session via `sqc_accounts` / `splendor_accounts`; a native seed is typed each session and is never persisted.
