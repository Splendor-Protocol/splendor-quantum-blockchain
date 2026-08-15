# Changelog

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
