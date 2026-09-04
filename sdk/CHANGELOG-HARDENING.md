# Changelog

## @splendor/sqc-sdk architecture top-10

- Transport owns chain IO; Connection no longer spawns a hidden `SqRpc` for MockTransport
- Wallet submits/confirms via Connection; Programs type writes as `Signer` (+ capabilities)
- Module registry (`registerModule` / `defineModule`); declarative Program writes
- DEX `poolInfo`, Exchange `positionId` / `orderHash`; KnownModuleActions drift test
- Canonical recipes: `createSession` + ConnectKit; deprecate `connectSplendor` / `createSplendorClient` / `useSplendorPQ` / prefer `Connection` over `SqcProvider`
- Shared unlock connector types (`WalletUnlockConnector`); connect-react keeps UI metadata

## @splendor/connect-react architecture top-10

- Hooks split per module (`react/hooks/{qrc20,dex,exchange,…}`); `useModules` exposes registry ids
- Connectors document SDK unlock contract adaptation

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
