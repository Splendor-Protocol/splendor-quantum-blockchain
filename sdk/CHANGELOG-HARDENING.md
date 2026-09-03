# Changelog

## @splendor/sqc-sdk 2.x harden (modular)

- Fail-closed amount parsing (`fromUnits` / `fromSPLD`); honest `waitForConfirmation` (receipt `status === 1`)
- DEX `swap.token_in` is `0|1` (chain uint8); fixed module read action names; X402 middleware requires settlement
- QRC-20 v2 surface; `signBridgeClaim`; `MockTransport` / `MockSigner`
- Prefer `Wallet` / `Connection` / `createSession`; `SplendorPQBridge` remains an alias

## Docs closeout (client raise status)

- SQDP-1: receipt-first confirmation; congested minFee note; `lock` / `error` events
- DAPP_INTEGRATION: Client ask → SDK answer table; confirmation + fee checklist updates
- `PROTOCOL.confirmationCaveat` → `receipt-preferred;nonce-fallback-race`

## @splendor/pq-bridge 1.4.0

- Emit `lock` event; idle no longer locks on every tab hide (`lockOnHidden` opt-in)
- `gatewayUrl` optional for SDK resolve (rpcUrls-only overrides)
- Inline `networkConfig` / `networks` on `createSplendorClient`
- Receipt-aware `waitForConfirmation` + `getTxReceipt` / `getTx`
- Shared `public-api` + package `browser` export condition
- Harden `registerNetwork`; freeze baked-in network config
- `prepublishOnly` WASM artifact gate
- Align call/deploy `waitForConfirmation`; MetaMask reject mapping; sign mutex

## @splendor/connect-react 1.3.0

- Provider-hosted modals; soft switch before controller recreate
- Sync `customNetworks`; safe persistNetwork fallback
- Connect mutex + StrictMode-safer autoReconnect
- Locked UI state; NetworkBadge a11y; clearer connect errors
- Peer `@splendor/pq-bridge` >= 1.4.0
