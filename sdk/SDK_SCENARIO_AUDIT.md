# SDK scenario audit

**Date:** 2026-09-23  
**Branch:** `sdk/hardening-audit`  
**Live chain:** not assumed (`SQC_LIVE` unset; `live-e2e` skipped)

## Commands

| Package | Command | Result |
|---------|---------|--------|
| `@splendor/sqc-sdk` | `npm test` (cwd `sdk/sqc-sdk`) | **204 passed**, 1 skipped (`live-e2e`) |
| `@splendor/pq-bridge-gateway` | `npm run test:gateway` → `npm --prefix gateway test` | **22 passed** (`security.test.ts`, `tls-policy.test.ts`) |
| `@splendor/connect-react` | `npm test` (cwd `sdk/connect-react`) | **71 passed** |

## Scenario matrix

| Situation | Result | Conclusion |
|-----------|--------|------------|
| Seed unlock and ML-DSA sign/verify (`wasm-session`, `wasm-sign-message`, `security-wasm`, `wallet-api`) | **pass** | Fixed-seed golden address, domain-separated sign/verify, lock/disconnect, and fee floor behave as designed. |
| Extension connect, dual `sqc_*`/`splendor_*`, raw sign rejected, mutex, `waitForConfirmation` (`extension-signer.test.ts`) | **pass** | Alias fallback, inject dual methods, raw `signMessage` rejection, and confirmation delegation match SQDP extension contract. |
| Inject refuses secrets and `eth_*`, requires `onApprove` (`injected-provider.test.ts`) | **pass** | Seed/EVM connect paths and `eth_*` are rejected; unsigned sensitive calls need approval; extension inject is never overwritten. |
| RPC failover, no 4xx failover, sticky submit, sticky clear, all-down `RPC_UNAVAILABLE` (`security-rpc.test.ts`) | **pass** | 5xx rotates, 4xx stays sticky-safe, submit sticky clears after NETWORK streak, and total outage surfaces `RPC_UNAVAILABLE`. |
| Receipt poll fail-fast vs 404 nonce fallback (`security-rpc.test.ts` confirmation cases) | **pass** | NETWORK on receipt fails fast; persistent 404 falls back to nonce confirmation. |
| Subscribe multi-URL (`subscribe-functional.test.ts`) | **pass** | SSE parse/reconnect/`Last-Event-ID` and multi-URL rotate on stream error work under mock transport. |
| Chain mismatch fail-closed (`security-rpc.test.ts`, `security-errors.test.ts`) | **pass** | Missing/empty/mismatched `chain_id` throws `CHAIN_MISMATCH` before submit; submit rejects foreign/`missing` chain client-side. |
| ConnectKit extension+seed only, session swap, mid-connect softDisconnect, probeNetwork error (`controller.test.ts`, `connectors.test.ts`) | **pass** | `defaultBrowserConnectors` is extension then seed; epoch fencing drops stale connect; probe failure surfaces after connect; `setSessionWallet` is returned by `getWallet`. |
| Safe UI error copy (`errors.test.ts`) | **pass** | Known `BridgeError` codes and plain `Error` heuristics map to non-leaking UI strings. |
| Widgets locked / toast / wrong-network (`widgets-rtl.test.tsx`) | **pass** | Unlock CTA, toast host, and wrong-network banner/switch states render as expected under RTL. |
| Gateway rejects EVM credentials (410) and public seed (`gateway/security.test.ts`) | **pass** | Public proxy returns 410 for seed/`evmPrivateKey`/`evmSignature`; signedTx-only path is accepted past custodial gate; TLS policy tests pass. |
| QRC-20 / module / contract helpers (`qrc20-functional`, `modules-functional`, `phase2-contracts`) | **pass** | Mocked envelopes for QRC-20, QRC-1155/exchange/pool/bridge, and phase-2 contract/x402 encoding match expected shapes. |
| Live node (`live-e2e.test.ts`) | **skip** (expected) | No live RPC in this run; transfer + confirmation against a real node was not exercised. |

## Final verdict

**Works as planned.** All automated suites for `@splendor/sqc-sdk`, gateway security/TLS, and `@splendor/connect-react` were green on this pass. No product or assertion fixes were required.

See also [SDK_MARKET_AUDIT.md](./SDK_MARKET_AUDIT.md) for architecture / market-standard scorecard and DEX honesty posture.

## Explicit limits

- No live RPC / chain node (`SQC_LIVE` not set).
- No real Chrome extension process (extension provider is mocked).
- Explorer surfaces remain link-only (not covered as a runtime integration here).
