# SDK scenario audit

**Date:** 2026-11-04 (previous pass 2026-09-23)  
**Live chain:** not assumed (`SQC_LIVE` unset; `live-e2e` skipped)

## Commands

| Package | Command | Before this pass | After |
|---------|---------|------------------|-------|
| `@splendor/sqc-sdk` | `npm test` (cwd `sdk/sqc-sdk`) | 400 passed, 1 skipped | **442 passed**, 1 skipped (`live-e2e`) |
| `@splendor/pq-bridge-gateway` | `npm run test:gateway` | — | **26 passed** |
| `@splendor/connect-react` | `npm test` (cwd `sdk/connect-react`) | 458 passed | **550 passed** |
| `@splendor/dapp-spec` | `npm test -w @splendor/dapp-spec` | 135 passed | **254 passed** (+ block typecheck) |
| `create-splendor-dapp` | `npm test -w create-splendor-dapp` | — | **8 passed** |
| `@splendor/playground` | `npm test -w @splendor/playground` | 300 passed | **405 passed** (+ `tsc --noEmit`) |
| `@splendor/playground-api` | `npm test -w @splendor/playground-api` | 83 passed | **83 passed** |
| Production scenarios only | `npm run test:prod` (repo root) | — | **358 passed** |

No test takes more than 5 s. The slowest production file is `connect-react/src/__tests__/prod/widgets.prod.test.tsx` at about 3–4 s in total; its slowest case is about 0.4 s.

## Difficulty levels

Every production scenario is tagged in its test name, so `vitest run -t "\[L3\]"` runs one level.

| Level | Meaning |
|-------|---------|
| `[L1]` | Standard: valid input, connected wallet, mock chain; the feature completes |
| `[L2]` | Boundary: empty or invalid input, over balance, locked or disconnected wallet, wrong network, list and batch caps |
| `[L3]` | Failure: RPC down, failed or double submit, disconnect mid-send, hostile input; errors stay `BridgeError` codes or safe UI copy |
| `[L4]` | Load: parallel sends with distinct nonces, full-cap batches, repeated connect/disconnect, whole-catalog and 20-page builds |

## Production scenario files

| File | Covers |
|------|--------|
| `sqc-sdk/src/__tests__/prod/qrc1155.prod.test.ts` | Every QRC-1155 read and write; node limits (batch cap, 2^256, name/URI bytes, zero amounts, self-approval, transfer to collection); locked and failing signers; full-cap batches |
| `sqc-sdk/src/__tests__/prod/verify-validators.prod.test.ts` | `VerifyClient` routes and id validation; `ValidatorsClient` module reads over `Connection` and a bare get/post transport; empty set; outage; 50 parallel checks |
| `sqc-sdk/src/__tests__/prod/connect-matrix.prod.test.ts` | Gateway and https policy, seed mode without a seed, RPC down, disconnect during a send, lock/relock, two wallets sending in parallel, 10 connect cycles, DEX stays unrouted |
| `connect-react/src/__tests__/prod/widgets.prod.test.tsx` | All 12 widgets × connected / disconnected / locked / wrong network / RPC failure with no raw error text; load-failed alerts; one send per double-click for transfer, token and deploy; NFT, balance and activity caps; every widget on one page |
| `connect-react/src/__tests__/prod/tooling.prod.test.tsx` | WASM asset copy and hashes, sibling discovery, read-only re-copy, browser stub; embed pinning (mainnet refuses unpinned), unknown widget names, bad `data-props`, HTML in props, 20 hosts mounted twice; `useModules` verify and validators |
| `dapp-spec/src/__tests__/prod-generate.prod.test.ts` | Every placeable catalog entry generates for Vite and Next; blank spec; hostile text round-trips; codegen fails closed on unvalidated specs; 20 pages × 40 blocks |
| `playground/src/prod/templates.prod.test.ts` | Every template validates, previews (filled and unfilled) and exports; unknown widget and `javascript:` link blocked; preview mock transfers, fees, over-balance and parallel sends |
| `playground/src/prod/catalog.prod.test.ts` | Every placeable gallery entry has a valid preview spec and every code export, with no demo address; embed snippets round-trip hostile text; whole catalog in under 5 s |

## Fixes this pass made

| Area | Problem found | Fix |
|------|---------------|-----|
| `ValidatorsClient`, `SqRpc.listValidators` / `getValidator` | Called `GET /validators` and `GET /validators/{addr}`, which the node does not serve; `isApproved` posted a body that `POST /call` rejects. Did not work against a real node | Read the routed `validators` module (`list`, `bond`, `validator`, `approved`) through `moduleCall` / `POST /call`; validate addresses; async rejections |
| `VerifyClient.proof`, `SqRpc.verifyProof` | Sent any id; the node answers 400 unless it is 32-byte hex | Validate id and instance before the request |
| `QRC1155` | Signed transactions the node refuses: batches over 64, values at 2^256, zero mint or burn amounts, long names or URIs, self-approval, transfer to the collection. Required `uris` to match `ids` exactly where the node accepts fewer | Same checks client-side, before any fee; `QRC1155_MAX_BATCH` exported |
| `useModules()` | No verify or validators client although the SDK ships both | Returns `verify` and `validators` |
| `generateProject` | Wrote page paths, prop names, container kinds and `hideOn` into generated code unescaped; only safe when the caller had run `validateSpec` (the playground and CLI do) | Throws on any of them outside the allowed form |
| Playground preview mock | Module calls, contract calls and x402 payments to yourself charged the fee without a balance check, so preview SPLD could go negative | Every send must cover its fee, like the node |

The gallery offers embed snippets for 6 widgets while `embed.tsx` mounts 14. The other 8 ship in the unreleased `connect-react-chain-widgets` changeset, and the snippet loads the published `embed.js`, so they stay off until that release. This is noted on `EMBED_WIDGETS`.

## Earlier scenario matrix (still passing)

| Situation | Tests |
|-----------|-------|
| Seed unlock and ML-DSA sign/verify, lock/disconnect, fee floor | `wasm-session`, `wasm-sign-message`, `security-wasm`, `wallet-api` |
| Extension connect, dual `sqc_*`/`splendor_*`, raw sign rejected, mutex | `extension-signer.test.ts` |
| Inject refuses secrets and `eth_*`, requires `onApprove` | `injected-provider.test.ts` |
| RPC failover, sticky submit, all-down `RPC_UNAVAILABLE`, receipt fallback | `security-rpc.test.ts` |
| Subscribe multi-URL, SSE reconnect | `subscribe-functional.test.ts` |
| Chain mismatch fail-closed | `security-rpc.test.ts`, `security-errors.test.ts` |
| ConnectKit connectors, session swap, epoch fencing | `controller.test.ts`, `connectors.test.ts` |
| Safe UI error copy | `errors.test.ts` |
| Gateway rejects EVM credentials and public seed; TLS policy | `gateway/security.test.ts`, `gateway/tls-policy.test.ts` |

## Explicit limits

- No live RPC or chain node (`SQC_LIVE`, `E2E_LIVE` not set). Every chain answer is a mock, `MockTransport`, a scripted `fetch`, or the playground's in-memory chain.
- No real browser extension process; the extension provider is mocked.
- Playwright e2e was not expanded or run in this pass.
- Widget tests run in jsdom; layout, focus order across real browsers, and visual regressions are not covered.
- QRC-1155 rules are checked against the Go router source (`vm/module_router.go`, `vm/qrc1155.go`), not a running node.
