# SDK scenario audit

**Date:** 2026-11-06 final review (scenario pass 2026-11-04, previous pass 2026-09-23)  
**Live chain:** not assumed (`SQC_LIVE` unset; `live-e2e` skipped)

## Final review, 2026-11-06

Everything below ran on Windows 11 with Node 22 and the pinned Go toolchain (go1.25.14).

| Check | Command | Result |
|-------|---------|--------|
| Pre-publish gate | `npm run release:check` | **pass**: pins, builds, every package suite, `test:prod`, dry-run pack of all four packages |
| Signer, security, gateway | `npm run test:security -w @splendor/sqc-sdk` | **121** security tests, Go signer (golden vectors, domain) **ok**, gateway **26** |
| Runtime dependencies | `npm audit --omit=dev` | **0 vulnerabilities** (a moderate `fast-uri` advisory under playground-api's Fastify was fixed in the lockfile) |
| Registry signatures | `npm audit signatures` | **436** verified signatures, **131** attestations |
| Browser e2e | `npm run test:e2e` | **120 passed**, 3 skipped (viewport-conditional), in 6.7 min, against the production build under the deploy CSP |
| Responsive | `responsive.spec.ts` in `chromium` (1280), `tablet` (768×1024), `mobile` (390×844) | no sideways scroll on any page, phone menu, Learn topic picker, inspector drawer as a dialog, columns stack at ≤720 px, laptop canvas zoom |
| Production security | `security.spec.ts` | deploy CSP with no violations, signer loads by SRI under `wasm-unsafe-eval`, opaque preview iframe, only `/assets` cross-origin |
| Generated dApps | `scripts/codegen-build-matrix.mjs` | `wallet-dashboard` Vite from packed tarballs **ok**, Vite with vendored SDK **ok**, `nft-drop` Next.js **ok** |
| Vendor bundle / StackBlitz | `pack:sdk-vendor`, `check:sdk-vendor`, `test:webcontainer` | **pass** |
| Typecheck and build | playground, playground-api, all packages | **pass** |

### Problems this review found and fixed

| Area | Problem | Fix |
|------|---------|-----|
| `wasm/build.ps1` | Pinned `go1.25.0` while `build.sh` pins `go1.25.14` and `go.mod` requires ≥ 1.25.14, so `npm run build:wasm` could not build on Windows, and any build it did make differed from the pins | Pins `go1.25.14`, same as `build.sh`. Two Windows builds produced the same bytes, which are now the committed pins |
| `@splendor/sqc-sdk` `prepack` | Called `bash` directly, so `npm pack` / `npm publish` failed on Windows | `scripts/prepack.mjs`: platform build script, re-pin, compile, `check-wasm-dist` |
| Changeset | `create-splendor-dapp` was missing, so the published CLI would keep scaffolding against the previous SDK | Added (`patch`) |
| Token stats block | Rendered "â€”" (mis-encoded) for missing values | Renders "—" |
| Gallery "Download as component" | Blocks that use `parseList` / `pageHref` downloaded with an import of a file that was not included, so the file did not compile | Shared helpers the block uses are inlined; a test checks every block |
| connect-react test timeout | Two jsdom widget flows (under 1 s alone) passed the 5 s default when 42 files ran in parallel on a loaded machine | `testTimeout: 15_000` |

### Cleanup

- Removed `commit-msg.txt`, a leftover draft of an already-made commit message.
- sqc-sdk: `addresses` uses `@noble/hashes/utils` instead of its own hex and concat helpers; `verify.ts` and `validators.ts` share `core/chain-io.ts`; deprecated `SqRpc.verifyProof` / `listValidators` / `getValidator` delegate to `VerifyClient` / `ValidatorsClient` instead of repeating their logic.
- connect-react: one lazy loader per sqc-sdk subpath (`react/lazySdk.ts`) replaces ten inline `import()` calls across the hooks.
- dapp-spec blocks: `parseArgs`, `formatUnits` and `pulse` live once in `blocks/parseList.ts` instead of seven copies (ContractRead, ContractWrite, ContractCall, ContractAction, LiveStat, TokenInfoCard, TokenStats, Tokenomics). That file was already copied into every export, so generated file lists are unchanged.

No public export was removed or renamed.

### Size

| Artifact | Size |
|----------|------|
| `@splendor/sqc-sdk` tarball | 1.3 MB packed, 4.8 MB unpacked, 157 files (the signer `.wasm` is most of it) |
| `@splendor/connect-react` tarball | 298.5 kB packed, 1.1 MB unpacked, 214 files |
| `@splendor/dapp-spec` tarball | 114.3 kB packed, 453 kB unpacked, 124 files |
| `create-splendor-dapp` tarball | 7.7 kB packed, 6 files |
| `connect-react/dist/embed.js` | 460.7 kB, 138.2 kB gzip |
| Playground largest chunk (`templates`) | 552.9 kB, 158.9 kB gzip (under the 800 kB limit) |

The 501 tarball entries contain no test files, no source maps, no `src/`, no env or key files. `sqc-sdk` ships `wasm/dist/sqc_signer.wasm`, `wasm/dist/wasm_exec.js` and both copies of `sqc.networks.json`, whose pins `check-wasm-dist` verified against those bytes.

## Publishing

The full runbook is [docs/RELEASING_PACKAGES.md](../docs/RELEASING_PACKAGES.md). Packages are published from GitHub Actions with npm provenance, not from a laptop.

1. **Check locally:** `npm run release:check`. It needs Go (the signer is rebuilt by `prepack`) and ends with each tarball's file list.
2. **Merge to `main`** with the pending changesets. `release-packages.yml` opens a **"chore: version packages"** PR with the bumps and changelogs.
3. **Review and merge that PR.** The workflow builds, tests, verifies the signer pins and publishes every new version under `latest`, after a required reviewer approves the `npm-publish` environment.
4. **Pre-release:** run the workflow with `channel=next` (Actions tab or Admin → Releases) to publish `x.y.z-next-<datetime>` under the `next` tag. `dry_run=true` builds and packs without publishing.

One-time setup (the `npm-publish` environment, `NPM_TOKEN`, Actions permissions) is in the runbook's first section.

## Commands

| Package | Command | Before this pass | After |
|---------|---------|------------------|-------|
| `@splendor/sqc-sdk` | `npm test` (cwd `sdk/sqc-sdk`) | 400 passed, 1 skipped | **442 passed**, 1 skipped (`live-e2e`) |
| `@splendor/pq-bridge-gateway` | `npm run test:gateway` | — | **26 passed** |
| `@splendor/connect-react` | `npm test` (cwd `sdk/connect-react`) | 458 passed | **550 passed** |
| `@splendor/dapp-spec` | `npm test -w @splendor/dapp-spec` | 135 passed | **254 passed** (+ block typecheck) |
| `create-splendor-dapp` | `npm test -w create-splendor-dapp` | — | **8 passed** |
| `@splendor/playground` | `npm test -w @splendor/playground` | 300 passed | **406 passed** (+ `tsc --noEmit`) |
| `@splendor/playground-api` | `npm test -w @splendor/playground-api` | 83 passed | **83 passed** |
| Production scenarios only | `npm run test:prod` (repo root) | — | **359 passed** |

Run alone, no test takes more than 5 s. The slowest production file is `connect-react/src/__tests__/prod/widgets.prod.test.tsx` at about 3–4 s in total; its slowest case is about 0.4 s.

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
- Playwright runs Chromium only (desktop, 768 px and 390 px viewports). Firefox and Safari/WebKit, and visual regressions, are not covered.
- Unit widget tests run in jsdom; real-browser layout and focus are covered only through the Playwright playground flows.
- The signer was rebuilt on Windows. CI's `wasm-drift` job rebuilds it on Linux on every push to `main` and re-pins automatically if the bytes differ, so the committed pins are confirmed there.
- QRC-1155 rules are checked against the Go router source (`vm/module_router.go`, `vm/qrc1155.go`), not a running node.
