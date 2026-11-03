---
"@splendor/sqc-sdk": patch
---

The browser loader runs a pinned `wasm_exec.js` (`execSha256` set) as a `<script>` with Subresource Integrity instead of `new Function`, so the signer works under a CSP without `'unsafe-eval'` (`script-src 'self' 'wasm-unsafe-eval'`). Unpinned loads (local development) keep the previous path, and a `wasm_exec.js` loaded by another script is still refused when the `.wasm` is pinned.
