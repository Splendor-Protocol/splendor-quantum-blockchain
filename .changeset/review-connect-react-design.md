---
"@splendor/connect-react": minor
---

Design, accessibility and safety review:

- `theme="auto"` follows the visitor's OS colour scheme; `ThemeOverrides.accentText` colours links and Copy buttons (defaults to `accent`).
- The light theme's link / Copy text uses a darker teal (`--sqc-accent-text`) so every text colour meets WCAG AA 4.5:1; a test checks the contrast of both built-in themes.
- On touch screens buttons, Copy, connector rows and the network badge are 44px targets; widget button rows and headers wrap on narrow screens; review rows no longer break words mid-word.
- `AddressDisplay` and `TxResult` only link to safe explorer URLs and open them with `rel="noopener noreferrer"`.
- Development warnings (once per cause, never in production builds: `NODE_ENV=production` or Vite's `import.meta.env.PROD`) for a missing or invalid `BridgeClaimWidget` `bridgeAddress`, `useAddressBook().save()` before a network is set, and an `indexerUrl` that `useActivity` refuses.
- `useAddressBook().save()` before a network is set now returns `{ ok: false, reason: 'no-network' }` (was `'invalid'`, the same as a bad address).
- `useWaitForConfirmation`: when calls overlap, only the latest one updates `status` / `error`.
- `useBrandLabels` is exported from the main entry; the `ActivityFeed` refresh button has an accessible name while loading.
- Removed the legacy `create-splendor-dapp` bin and `create-template/` from this package; use `npm create splendor-dapp@latest`.
