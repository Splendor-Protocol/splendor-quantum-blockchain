# Hosting WASM signer assets

Browser dApps must serve `sqc_signer.wasm` and `wasm_exec.js` (from `sdk/bridge/wasm/dist` after `npm run build:wasm`) with correct MIME types and CORS.

## MIME types

| File | Content-Type |
|------|----------------|
| `sqc_signer.wasm` | `application/wasm` |
| `wasm_exec.js` | `application/javascript` or `text/javascript` |

Incorrect MIME (especially `application/octet-stream` for WASM) causes `WebAssembly.instantiate` / fetch failures.

## CORS

If assets are on a different origin than the dApp, allow the dApp origin:

```
Access-Control-Allow-Origin: https://your-dapp.example
Access-Control-Allow-Methods: GET, HEAD, OPTIONS
```

Prefer an explicit origin over `*`. The SDK fetches both files (exec is hash-checked before `eval`-style load; WASM is hash-checked before instantiate).

## Integrity pins

Set `wasmSha256` and `execSha256` from `sqc.networks.json` (refreshed by `npm run sync:config` when artifacts exist). Non-local browser networks **require** `wasmSha256` unless `allowUnpinnedWasm: true`.

```ts
await connectSplendor({
  network: 'mainnet',
  // pins come from sqc.networks.json via createSplendorClient
});
```

## Example (nginx)

```nginx
location /sqc_signer.wasm {
  types { application/wasm wasm; }
  add_header Access-Control-Allow-Origin "https://your-dapp.example";
}
location /wasm_exec.js {
  add_header Access-Control-Allow-Origin "https://your-dapp.example";
}
```
