---
"@splendor/sqc-sdk": patch
---

Security review fixes:

- The browser WASM loader no longer fetches and evaluates an unverified `wasm_exec.js` when only `wasmSha256` is pinned; pass `execSha256` too (or `allowUnpinnedExec` / `allowUnpinnedWasm` for local development). `createSession` already required both pins on non-local networks; this closes the same gap for direct `loadWasmSigner` callers.
- `VerifyClient` and `ValidatorsClient` treat node responses as untrusted: `check()` counts only a literal `valid: true`, drops non-string fields, and rejects non-object bodies; `attest` / `settle` require a tx hash; `ValidatorsClient.list()` keeps only object entries and `get()` rejects a non-object record (`RPC` error).
- The gateway now warns loudly at startup when `ALLOW_CUSTODIAL_PROXY=1` runs without `GATEWAY_API_KEY`.
- Every deprecated export says it is removed in the next major version.
