# Review: SDKs, widgets and dApp Studio (November 2026)

**Scope:** `@splendor/sqc-sdk` (and its gateway), `@splendor/connect-react`, `@splendor/dapp-spec`,
`create-splendor-dapp`, the deprecated `@splendor/pq-bridge` shim, the dApp Studio
(`apps/playground`) and its API (`apps/playground-api`).
**Perspectives:** functionality and test coverage, security, design and responsiveness,
accessibility, ease of use, dead or redundant code, documentation.
**Manual for testers:** [TESTER_MANUAL.md](../TESTER_MANUAL.md).
**Earlier reviews:** [archive/](./archive/) (September deep audit, November scenario audit, the
SDK hardening changelog).

## Summary

The products were already in good shape. Seeds are never stored, RPC calls are SSRF-guarded, the
Studio preview runs in an isolated iframe, the API has CSRF checks, parameterized SQL and sealed
GitHub tokens, and code generation escapes everything. The review found no critical vulnerability
in shipped code. It found and fixed:
- 4 high and 9 medium issues across security, accessibility, safety defaults and dependencies.
- A set of test gaps, design gaps, documentation drift and leftover code.

Every fix has a test that fails if the problem comes back.

## Baseline (before changes)

| Check | Result |
|-------|--------|
| `npm run test:packages` | pass (sqc-sdk 442, connect-react 550, dapp-spec 254, CLI 8) |
| `npm run test:prod` | pass |
| `npm run test:playground` | pass (406 + 83) |
| `npm run test:e2e` | 120 passed, 3 skipped |
| `npm run test:codegen` | 18 / 18 builds pass |
| `npm run test:webcontainer`, `lab:check` | pass |
| `npm audit --omit=dev` | 0 vulnerabilities |
| `npm audit` (incl. dev tooling) | 4 (2 critical, 1 high, 1 moderate): vitest 3 / tinypool / @vitest/mocker, undici |

## Findings and fixes

Severity: **High** = exploitable or misleading in a way that can cost users funds or access;
**Medium** = a real defect or gap with limited impact; **Low** = polish, hygiene or docs.

### Security

| # | Sev | Finding | Fix | Guarded by |
|---|-----|---------|-----|------------|
| S1 | High | Dev toolchain advisories: vitest ≤ 4.1.10 (tinypool prototype-pollution RCE, mocker path traversal) and undici 8.0–8.12.1 (TLS validation bypass, SRI bypass, DoS) | vitest 5.1 in every workspace and the gateway; undici 8.12.4; root `engines.node` raised to `>=22.20` (the SDK already required it, and patched undici needs ≥ 22.19) | `npm audit` = 0 (workspace and gateway) |
| S2 | Medium | The browser WASM loader fetched and `new Function`-evaluated an **unverified** `wasm_exec.js` when only `wasmSha256` was pinned (direct `loadWasmSigner` callers; `createSession` already refused this) | Fail closed unless `execSha256` is also pinned; explicit `allowUnpinnedExec` (set from `allowUnpinnedWasm`) for local development | `security-wasm.test.ts` |
| S3 | Medium | `target="_blank"` links without `noopener` (reverse-tabnabbing) in `TxResult`, `AddressDisplay`, four exported dApp blocks and two examples; `AddressDisplay` rendered any caller `href` | `rel="noopener noreferrer"` everywhere; `AddressDisplay` / `TxResult` only link to `isSafeExplorerUrl` URLs | `external-links.test.tsx` (scans all library sources), `blocks-links.test.ts` (scans exported blocks) |
| S4 | Medium | `VerifyClient` / `ValidatorsClient` passed node responses through with `as T`: `valid: "true"` counted as valid, non-object bodies accepted | Literal `valid === true` only; non-string fields dropped; non-object bodies and hash-less submits rejected (`RPC` error); validator lists keep only objects | `security-rpc-shapes.test.ts` |
| S5 | Medium | `generateProject` trusted callers to validate first | Validates against the catalog by default and names the failing paths; `validate: false` opt-out for already-validated specs | `prod-generate.prod.test.ts` |
| S6 | Low | Gateway logged "seed unlock accepted only with GATEWAY_API_KEY" even when `ALLOW_INSECURE` let it run without a key | Accurate, louder startup warning; README states the custodial risk plainly | gateway tests (26) |

The security-review agent's pass over the final diff is recorded under [Verification](#verification).

### Safety defaults and ease of use

| # | Sev | Finding | Fix | Guarded by |
|---|-----|---------|-----|------------|
| U1 | High | The Feature Lab's `.env.example` (copied on first run) started on **mainnet**, while the README says testnet; lab sends are real transactions | Default is testnet, with a comment why | manual case 1.4 |
| U2 | Medium | Silent misconfiguration: `BridgeClaimWidget` without a valid `bridgeAddress` (the manifest marks it required), `useAddressBook().save()` before a network is set, a refused `indexerUrl` | One-time development warnings (never in production) | `dev-warnings.test.tsx` |
| U3 | Low | `useBrandLabels` only on the `./hooks` entry | Also exported from the main entry | typecheck |
| U4 | Low | Duplicate `landing` template (identical to `token-project`) listed in pickers and the CLI | Removed from `TEMPLATES`; `TEMPLATE_ALIASES` keeps old `?template=landing` links and `--template landing` working | `kits.test.ts`, CLI `bin.test.mjs`, e2e `studio.spec.ts` |

### Accessibility and design

| # | Sev | Finding | Fix | Guarded by |
|---|-----|---------|-----|------------|
| A1 | High | Studio Layers tree owned buttons directly (ARIA tree may own only tree items and groups: axe *critical*) | Row buttons are mouse shortcuts hidden from assistive tech; the tree item already has Delete, Ctrl+D, arrows and F2, now listed in `aria-keyshortcuts` | e2e `a11y.spec.ts` |
| A2 | Medium | Learn code blocks and admin tables scrolled but could not be reached by keyboard | Focusable, labelled scroll regions | `Markdown.test.tsx`, `a11y.spec.ts` |
| A3 | Medium | Palette tiles' accessible names did not contain their visible text; shelf counts at 4.15:1 contrast | Visible title is the name; status and "needs a wallet" as screen-reader text; description via `aria-describedby`; counts at full muted colour | `a11y.spec.ts` |
| A4 | Medium | Light theme link / Copy text `#0d9b8c` on white = 3.4:1 (below WCAG AA) | `--sqc-accent-text` token (`#0a7d71`, 5.0:1); `ThemeOverrides.accentText` | `tokens-css.test.ts` computes contrast for every text colour in both themes |
| A5 | Medium | Widget touch targets 24px (Copy, chips, menu rows) | 44px under `pointer: coarse` | `tokens-css.test.ts` |
| A6 | Low | No OS light/dark following | `theme="auto"` (light tokens under `prefers-color-scheme: light`) | `tokens-css.test.ts`, `themes.test.ts` |
| A7 | Low | Widget button rows and headers did not wrap on narrow cards; review rows broke words mid-word | `flex-wrap`; `overflow-wrap: anywhere` | `tokens-css.test.ts` |
| A8 | Low | `ActivityFeed` refresh had no accessible name while loading ("…") | `aria-label` + `aria-busy` | `ui-kit.test.tsx` |

The review's contrast check showed one earlier concern was unfounded: dark-theme muted text
(`#8aa3ad` on `#132029`) is 6.3:1, not borderline.

**Visual pass.** Full-page screenshots of Home, Gallery, two widget pages, Learn, the template
picker, two studios (template and kit), Admin and 404 at 360, 390, 768, 1024 and 1440px. No page
scrolls sideways at any width, the studio collapses to drawers below 1100px as designed, and
widgets stay inside their cards. One cosmetic item is deferred (below).

**Bug found while testing the fixes:** absolutely positioned screen-reader text inside the palette
escaped its scroll container and made the whole page taller, so canvas clicks landed on the studio
overlay. Fixed (`.pg-piece { position: relative }`) and now covered by the mobile and tablet user
journeys. The existing layout checks only measured width, which is why the new journeys matter.

### Test coverage added

| Area | Added |
|------|-------|
| connect-react | `ConnectKit`, `NetworkBadge` (keyboard switching), `ModalHost` (focus trap, Escape), `WalletGate`, `WidgetShell` (wrong-network lock), `ReviewPanel`, `CopyButton`, `TxResult`, `useWaitForConfirmation`, `useAddressBook`, `useAccountDto`, themes, contrast, links, dev warnings |
| sqc-sdk | QRC-1155 functional suite, bridge admin writes and claim validation, `@splendor/sqc-sdk/react` SSR smoke, RPC response shapes, unpinned glue |
| CLI | Real subprocess tests of `bin/index.mjs` (help, create, kits, `--set`, errors, alias) |
| Studio e2e | Accessibility scan (axe, WCAG 2.1 AA) of every route on desktop, phone and tablet; main journeys on phone and tablet (template → preview, gallery → add, add/undo, Preview app transfer, save, export, workspaces); admin Templates page |
| CI | WebContainer smoke and a `@splendor/pq-bridge` shim build check on every PR; `test:prod` in the release workflow; the vendored-SDK codegen job builds every template (was 3) |

### Cleanup

| Removed / moved | Why |
|-----------------|-----|
| connect-react's bundled `create-splendor-dapp` bin, `scripts/create-splendor-dapp.mjs`, `create-template/` | Superseded by `packages/create-splendor-dapp`; same bin name collided with it |
| Stale `sdk/bridge/wasm` lookups (`vite.ts`, `copy-wasm-assets.mjs`) and doc references | The directory no longer exists; the signer lives in `sdk/sqc-sdk/wasm` |
| Committed `__pycache__/*.pyc` | Build output; now ignored |
| `sdk/SDK_MARKET_AUDIT.md` | A 15-line pointer to the deep audit |
| `sdk/SDK_DEEP_AUDIT.md`, `SDK_SCENARIO_AUDIT.md`, `CHANGELOG-HARDENING.md` | Moved to [archive/](./archive/) (history, linked from the package changelogs) |
| Triple `testTimeout` in connect-react's vitest config | Duplicate keys |
| Four obsolete `landing` snapshots | The template is an alias now |

**Kept on purpose, clearly marked:** every deprecated export (`useDeployContract`,
`TransferReviewPanel`, `onSent` / `onDeployed` / `onSignedIn`, `SplendorPQBridge`, `SqRpc`,
`connectSplendor`, `createSplendorClient`, the `@splendor/pq-bridge` shim, …) now says "Removed in
the next major version" in its type declaration, and both READMEs have a deprecation table with
the replacement for each.

### Documentation

- READMEs of dapp-spec and create-splendor-dapp listed 5 of the templates; they now list all of
  them, including the kits, and explain kit setup.
- connect-react README: all 12 widgets with stable / beta status, a ConnectKit and UI-kit props
  section, examples for `QrCode`, `PendingTxList` and `FeeRow`, theming (`auto`, `accentText`),
  touch targets, and a deprecation table.
- dapp-spec README example used spec version 1; the current version is 2.
- New [tester manual](../TESTER_MANUAL.md).

## Deferred (decided, not forgotten)

| Item | Why deferred |
|------|--------------|
| `sdk/sqc-sdk.js` / `sdk/sqc-sdk.d.ts` (legacy single-file SDK) | Still referenced by the chain `readme.md`, `docs/readme-claim-evidence.md` and the sqc-sdk README as a known-broken deprecated file. Remove together with those docs in a chain-docs change. |
| Removing deprecated public exports | Planned for the next major version (non-breaking review). |
| `/widgets/<id>` preview keeps a 480px minimum height | Leaves space under short widgets, but lets review and result steps fit without inner scrolling. Auto-height would need a size message from the isolated iframe. |
| Studio CSP keeps `style-src 'unsafe-inline'` | Needed by React inline styles and the theme overrides; scripts stay strict. |
| DEX / Exchange, WalletConnect, hosted publishing | Product decisions, unchanged. |

## Verification

Final run on Node 22.23, Windows, against the final working tree:

| Check | Result |
|-------|--------|
| `npm run build:packages`, CLI build, playground + API typecheck, connect-react `tsc` | pass |
| `npm run test:packages` | pass: sqc-sdk 459 (1 live test skipped), connect-react 594, dapp-spec 255, CLI 14 |
| `npm run test:prod` | pass |
| `npm run test:playground` | pass: Studio 404, API 83 |
| `npm run test:e2e` | **158 passed**, 3 skipped (viewport-conditional), on desktop, 390px phone and 768px tablet, including the accessibility scan |
| `npm run test:codegen` | 16 / 16 builds (8 templates × Vite and Next.js) |
| `npm run test:webcontainer`, `npm run lab:check` | pass |
| `npm audit` (workspace, including dev tooling) and gateway `npm audit` | **0 vulnerabilities** |
| Security review (automated agent, uncommitted diff) | no issues |
| Bugbot (automated agent, uncommitted diff) | 4 reported. 1 false: the archive files exist, the tool could not see git renames. 3 valid, all fixed with tests: `devWarn` also honours Vite's `import.meta.env.PROD`; `useAddressBook().save()` without a network returns `reason: 'no-network'`; overlapping `useWaitForConfirmation` calls no longer overwrite the latest status. |
