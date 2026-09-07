# Splendor dApp

Vite + React scaffold with `@splendor/connect-react` (ConnectKit + widgets).

## Run

```bash
npm install
npm run dev
```

## Production: pin the signer

Compute the WASM digest and pass it to the provider:

```bash
shasum -a 256 node_modules/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm
```

```tsx
<SplendorConnectProvider network="testnet" wasmSha256="<digest>" rpcUrls={['https://…']}>
```

Never ship `allowUnpinnedWasm` outside local development.

## Optional: page-level provider

Set `exposeInjectedProvider` on `SplendorConnectProvider` to install `window.splendor`
(SQC `splendor_*` methods — not Ethereum `eth_*`).
