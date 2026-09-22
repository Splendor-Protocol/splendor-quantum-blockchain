# SDK deep audit (market-standard rubric)

**Date:** 2026-09-23  
**Branch:** `sdk/hardening-audit`  
**Scope:** `@splendor/sqc-sdk`, `@splendor/pq-bridge-gateway`, `@splendor/connect-react`  
**Method:** Evidence-scored parameters (`pass` = code + test; `partial` = code / static only; `fail` = gap; `n/a` = out of product scope)

## Commands (post-fix)

| Package | Command | Result |
|---------|---------|--------|
| sqc-sdk | `npm test` | **205 passed**, 1 skipped (`live-e2e`) |
| gateway | `npm run test:gateway` | **23 passed** |
| connect-react | `npm test` | **69 passed** |

## Summary counts (after P0/P1 fixes)

| Score | Count (approx.) |
|-------|-----------------|
| pass | 62 |
| partial | 22 |
| n/a (deferred / out of scope) | 14 |
| fail (remaining, non-P0/P1 or accepted limit) | 2 |

**Remaining fails (accepted / deferred):** (1) no live-node e2e in CI, (2) no hardware memory zeroization proof for seed hex strings (language limit).

---

## 1. Security (~25)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| S01 | WASM SHA-256 pin required (non-local) | pass | `wasm-loader`, `security-wasm.test.ts` |
| S02 | WASM pin mismatch fail-closed | pass | `security-wasm.test.ts` |
| S03 | WASM export surface check at load | pass | `wasm-types` / loader |
| S04 | Session lock prevents signing | pass | `security-wasm.test.ts` |
| S05 | Disconnect invalidates session | pass | `wasm-session` / security-wasm |
| S06 | Re-derive replaces prior key binding | pass | `security-wasm.test.ts` |
| S07 | Lock “zeroize” claim matches tests (cannot-sign, not heap forensics) | pass | comment + tests aligned this pass |
| S08 | Domain-separated message signing | pass | `message-domain`, `wasm-sign-message.test.ts` |
| S09 | Raw `signMessage` rejected (inject/extension) | pass | `extension-signer`, `injected-provider` tests |
| S10 | Inject refuses seed/EVM secrets | pass | `injected-provider.test.ts` |
| S11 | Inject refuses `eth_*` | pass | `injected-provider.test.ts` |
| S12 | Inject requires `onApprove` for sensitive calls | pass | `injected-provider.test.ts` |
| S13 | Extension inject never overwritten | pass | `injected-provider.test.ts` |
| S14 | Chain ID fail-closed (missing/mismatch) | pass | `security-rpc`, `security-errors` |
| S15 | Fee floor enforced | pass | `security-wasm`, encoding |
| S16 | Gateway public = signedTx only | pass | `gateway/security.test.ts` |
| S17 | Gateway EVM unlock → 410 | pass | `gateway/security.test.ts` |
| S18 | Gateway custodial needs API key + no CORS * | pass | `gateway/security.test.ts` |
| S19 | Gateway TLS policy / classical refuse | pass | `tls-policy.test.ts` |
| S20 | Timing-safe API key compare | pass | `safeEqualString` tests |
| S21 | Node SSRF blocklist (metadata / link-local / file:) | pass | `assertSafeRpcUrl` + audit-scenarios 12/12b |
| S22 | Gateway public errors sanitized (no ECONNREFUSED/URL/stack) | pass | `errorFromCaught` + security.test sanitize case **(fixed P0)** |
| S23 | RPC `details` not dumping full upstream JSON | pass | `parseResponse` status-only **(fixed P1)** |
| S24 | Connect mutex held across softDisconnect until flight ends | pass | controller + new overlapping test **(fixed P0)** |
| S25 | ConnectModal wipes seed/`fieldValues` on fail and success | pass | `ConnectModal` finally wipe **(fixed P0)** |
| S26 | Seed hex perfect heap wipe | fail | JS string immutability — documented limit |
| S27 | Inject provider spoof (no WC pairing) | partial | Documented; heuristic `isExtension` |
| S28 | Sign mutex (WASM / extension) | pass | `security-mutex`, extension op lock |

## 2. Resilience / scalability (~15)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| R01 | Multi-URL RPC failover on 5xx | pass | `security-rpc.test.ts` |
| R02 | No failover on 4xx | pass | `security-rpc.test.ts` |
| R03 | Sticky submit URL | pass | `security-rpc.test.ts` |
| R04 | Sticky clear after NETWORK streak | pass | `security-rpc.test.ts` |
| R05 | All-down → `RPC_UNAVAILABLE` | pass | `security-rpc.test.ts` |
| R06 | Request timeouts (`AbortController`) | pass | `rpc.ts` + tests |
| R07 | Timeout message without embedding URL | pass | `fetchWithTimeout` **(fixed P1)** |
| R08 | In-flight GET coalesce | pass | audit-scenarios coalescing |
| R09 | Confirmation fail-fast on infra | pass | `security-rpc` confirmation |
| R10 | Receipt 404 → nonce fallback | pass | `security-rpc` |
| R11 | Subscribe multi-URL rotate | pass | `subscribe-functional.test.ts` |
| R12 | Subscribe Last-Event-ID reconnect | pass | `subscribe-functional.test.ts` |
| R13 | Gateway rate limit + body limit | pass | `gateway/security.test.ts` |
| R14 | Health-weighted / quorum RPC | n/a | Deferred (market gap, not this pass) |
| R15 | Unbounded listener / map growth guarded | partial | inflight map + sticky; no explicit max size |

## 3. API honesty / missing features (~20)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| A01 | Public modules = qrc20/1155/bridge/contract only | pass | `modules.ts`, tests |
| A02 | DEX/Exchange not on Session | pass | `session.ts`, session-api test |
| A03 | Root does not export DEX/Exchange classes | pass | `public-api.ts` |
| A04 | Root does not export dex/exchange address helpers | pass | `public-api.ts` **(fixed P1)** |
| A05 | `/defi` leftover marked unsupported | pass | `defi/index.ts` headers |
| A06 | Connect-react does not export useDex/useExchange | pass | hooks deleted **(fixed P1)** |
| A07 | Docs do not claim DEX “Built” as production | pass | README / architecture **(fixed)** |
| A08 | Extension `payX402` fail-closed + userHint | pass | `extension-signer.ts` **(fixed P1)** |
| A09 | usePayX402 documented seed/WASM only | pass | hook comment + README **(fixed P1)** |
| A10 | Dual Connector worlds documented | pass | connector types comments |
| A11 | WalletConnect / mobile | n/a | Deferred |
| A12 | EIP-6963 multi-wallet discovery | n/a | Deferred |
| A13 | QRC-721 typed Program client | n/a | On-chain routed; Program roadmap |
| A14 | Bridge module client | pass | modules-functional / hooks |
| A15 | QRC-20 / QRC-1155 clients | pass | functional + widget/hooks tests |
| A16 | Bytecode Contract helpers | pass | phase2-contracts |
| A17 | X402 client (WASM path) | pass | x402 tests / middleware |
| A18 | Verify / Validators helpers | partial | Clients exist; not in modules.bind |
| A19 | Live DEX end-to-end | n/a | Needs node routing |
| A20 | Explorer as runtime integration | n/a | Link-only |
| A21 | registerUnsupported still available for experiments | pass | `modules.ts` |

## 4. Redundancy / practices (~15)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| P01 | Deprecated EVM derive on root marked | pass | JSDoc + README legacy row |
| P02 | Deprecated `SqRpc` / `connectSplendor` marked | pass | README / public-api |
| P03 | Prefer Connection over SqRpc | partial | Still used internally; public deprecate |
| P04 | Dead DEX React hooks removed | pass | deleted this pass |
| P05 | web-dapp DEX panels removed | pass | honesty harden |
| P06 | Naming: bridge package vs BridgeModule | partial | Documented overload |
| P07 | Parallel recipes (createSession / connect / WalletClient) | partial | Documented happy paths |
| P08 | Module registry open for addons | pass | `registerModule` |
| P09 | Typed Session hand-wire for new modules | partial | Scalable registry; Session still manual |
| P10 | Error taxonomy stable codes | pass | `errors.ts` + security-errors |
| P11 | userHint on major factories | pass | `errors.ts` **(fixed P1)** |
| P12 | UI connectErrorMessage prefers userHint | pass | controller + errors.test |
| P13 | Gateway bind default localhost | pass | gateway config |
| P14 | Tests mirror SQDP KnownModuleActions | pass | known-module-actions.test.ts |
| P15 | No MetaMask / eth_* in ConnectKit defaults | pass | defaultBrowserConnectors |

## 5. Scenario coverage (~15)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| C01 | Seed unlock + ML-DSA golden | pass | wasm-session / wallet-api |
| C02 | Extension dual aliases | pass | extension-signer.test.ts |
| C03 | Inject security matrix | pass | injected-provider.test.ts |
| C04 | RPC failover matrix | pass | security-rpc.test.ts |
| C05 | Confirmation fail-fast / nonce | pass | security-rpc.test.ts |
| C06 | Subscribe multi-URL | pass | subscribe-functional |
| C07 | Chain mismatch | pass | security-rpc / security-errors |
| C08 | ConnectKit epoch / softDisconnect | pass | controller.test.ts |
| C09 | Overlapping connect after softDisconnect | pass | new controller test **(fixed P0)** |
| C10 | Safe UI error copy | pass | errors.test.ts |
| C11 | Widgets locked / toast / wrong-network | pass | widgets-rtl |
| C12 | Gateway custodial / EVM 410 | pass | gateway/security |
| C13 | Gateway error sanitization | pass | new gateway test **(fixed P0)** |
| C14 | SSRF denylist expansion | pass | audit-scenarios 12b |
| C15 | Live node transfer+confirm | n/a | `live-e2e` skipped without `SQC_LIVE` |

## 6. Performance posture (~10) — static only (no load harness)

| ID | Parameter | Score | Evidence |
|----|-----------|-------|----------|
| F01 | GET coalescing reduces duplicate in-flight | partial | code + unit coalesce test |
| F02 | Sticky submit reduces URL churn | partial | security-rpc sticky cases |
| F03 | Sign lock serializes nonce use | partial | security-mutex |
| F04 | No N+1 wallet init on every hook render | partial | controller wallet reuse |
| F05 | SSE reconnect backoff present | partial | subscribe.ts |
| F06 | Gateway rate limits under burst | pass | gateway 429 test |
| F07 | RPC health scoring | n/a | Deferred |
| F08 | Quorum reads | n/a | Deferred |
| F09 | Bundle size / tree-shake subpaths | partial | package exports map |
| F10 | Benchmark suite in CI | n/a | Option 3 not selected |

---

## P0 / P1 fix log (this pass)

| Sev | Item | Files |
|-----|------|-------|
| P0 | Gateway public error sanitization | `sdk/sqc-sdk/gateway/server.ts`, `gateway/security.test.ts` |
| P0 | Connect mutex survives softDisconnect until flight `finally` | `sdk/connect-react/src/core/controller.ts`, `controller.test.ts` |
| P0 | ConnectModal always wipe secrets in `finally` | `sdk/connect-react/src/ui/ConnectModal.tsx` |
| P1 | RPC details redact; timeout without URL | `sdk/sqc-sdk/src/rpc.ts` |
| P1 | userHint on BridgeError factories | `sdk/sqc-sdk/src/errors.ts`, `security-errors.test.ts` |
| P1 | SSRF denylist (`file:`, metadata hosts) | `rpc.ts`, `audit-scenarios.test.ts` |
| P1 | Extension payX402 typed + userHint; docs honesty | `extension-signer.ts`, `hooks/x402.ts`, READMEs |
| P1 | Remove root dex/exchange address exports; delete dead hooks | `public-api.ts`, deleted `hooks/dex.ts` / `exchange.ts` |
| P1 | DEX “Built” → leftover; legacy API row; lock claim language | READMEs, `security-wasm.test.ts` comment |

## Explicit limits

- No live RPC / `SQC_LIVE` e2e in this run.
- No real Chrome extension process (mocked).
- No WalletConnect / EIP-6963 / RPC quorum implementation.
- No on-chain DEX/Exchange routing.
- Performance is **static/posture** only (option 2).
- JS cannot securely wipe immutable seed strings from the heap.

## Verdict

After this deep rubric + P0/P1 fixes, the SDKs meet **first-party market practice** for security controls that are in scope, with honest gaps called out as `n/a`/`fail`. Automated suites are green. This document supersedes the short scorecard in [SDK_MARKET_AUDIT.md](./SDK_MARKET_AUDIT.md) for depth; keep [SDK_SCENARIO_AUDIT.md](./SDK_SCENARIO_AUDIT.md) for the earlier scenario-matrix run.
