---
"@splendor/sqc-sdk": minor
---

Security hardening. Two of these refuse input that used to be accepted; check your integration:

- `checkAuthMessage` / `verifySignIn`: with `expectedChainId` / `chainId` set, a sign-in message that has no `Chain ID` line is now rejected with reason `chain` (before, only a mismatching one was).
- `signMessage(msg, { raw: true })` on SDK wallets (`Wallet`, `SplendorPQBridge`, `createWalletClient`) is refused with `USER_REJECTED` unless the wallet is created with the new `allowRawSign: true` option. Injected and extension providers already refused it.
- Browser WASM loader: when `wasmSha256` is pinned without `execSha256`, a `wasm_exec.js` loaded by another script is refused instead of trusted.
- `discoverExtensionProvider` prefers an EIP-6963 announcement with rdns `network.splendor.wallet` over the `window.splendor` global, and keeps the first provider it finds for the page's lifetime (`clearExtensionDiscoveryCache()` resets it). New export: `SPLENDOR_WALLET_RDNS`.
- Gateway: `/health` shows `service_address` only to callers with the API key; malformed `signedTx` JSON answers `400 INVALID_PARAMS`.
