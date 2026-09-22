# Splendor SQC — SDK web demo

Small Vite + React app that exercises `@splendor/sqc-sdk` + `@splendor/connect-react`:

1. **Wallet connect** — Splendor extension (when installed) or native ML-DSA-65 seed via ConnectKit
2. **Receive** — show + copy your `SQ:` address
3. **Balance** — native SPLD with refresh
4. **Send** — native SPLD transfer
5. **QRC-20** — paste token address; read + transfer / approve
6. **QRC-1155 / Bridge / Contract** — hook-driven panels

DEX / Exchange are **not** in this demo. Leftover clients remain under `@splendor/sqc-sdk/defi` only until the node routes those modules.

For drop-in widgets (`TransferWidget`, `TokenPanel`, `TxToastHost`, …) see
[`../../connect-react/examples/basic-dapp`](../../connect-react/examples/basic-dapp).

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

## Notes

- WASM is copied by `sqcWasmPlugin()`. Dev builds may omit the pin (`allowUnpinnedWasm`); production requires `VITE_SQC_WASM_SHA256` (same gate as basic-dapp).  
- Signing is ML-DSA-65 (WASM seed or extension). Confirmation is RPC receipt polling — explorer UI downtime does not mean the tx failed.
