# Changelog

## @splendor/connect-react 2.0.0

- **BREAKING:** `usePayX402` removed. The wallet extension cannot pay x402, so the hook only ever
  worked with seed wallets. Use `wallet.payX402` / the `x402` helpers from `@splendor/sqc-sdk`.
- `useModuleCall` moved to `react/hooks/modules.ts`; the public import is unchanged.
- Widget callbacks: `TransferWidget` (`onSent`, `onError`, `defaultTo`), `TokenPanel` (`onSent`,
  `onError`, `defaultAmount`), `DeployTokenWidget` (`onDeployed`, `onError`).
- Wallet network tracking: `useNetwork()` returns `walletChainId`, `walletNetwork`,
  `walletMismatch`; `<WrongNetworkBanner />` warns when the extension is on another network.
- Standard widget contract (`BaseWidgetProps`): every widget takes `className`, `style`, `title`,
  `hideTitle`, `labels`, `onSuccess`, `onError`, `onStatusChange` and `toast`. `onSent`,
  `onDeployed` and `onSignedIn` are deprecated aliases and still fire.
- Widget text is overridable per widget (`labels`) or globally (`labels.widgets` on the provider);
  defaults are exported as `TRANSFER_LABELS`, `TOKEN_LABELS`, etc.
- Widgets render a Connect / Unlock prompt when no unlocked wallet is connected, push toasts when a
  `<TxToastHost>` is mounted, and expose `data-sqc-widget` / `data-status` on the root.
- New exported parts: `WidgetShell`, `WalletGate`, `AddressInput`, `AmountInput`, `ReviewPanel`,
  `TxResult`, `AddressDisplay`, `CopyButton`, `useTransferForm`, `useWidgetLabels`,
  `useOptionalTxToast`.
- `DeployTokenWidget`: `showDecimals`, `maxSupply`, and `onSuccess` gets `explorerUrl`.
  `AccountCard`: `showBalance`, `showNetwork`, `showExplorer`, `onCopy`, `onDisconnect`.
- Theme: `--sqc-widget-padding`, `--sqc-widget-gap`, `--sqc-widget-max-width`, `--sqc-success`;
  `ThemeOverrides` gains `border`, `danger`, `success`, `widgetPadding`. Widget inline styles
  replaced by `.sqc-field`, `.sqc-widget__actions`, `.sqc-tx-result`, `.sqc-address` and friends.

## @splendor/sqc-sdk (unreleased)

- `ExtensionSigner` converts `amountSPLD` to base units before `sqc_sendTransaction` (the extension
  rejects decimal amounts), and refuses sends when the extension is on another chain
  (`CHAIN_MISMATCH`).
- SQDP `4200` rejections keep the wallet's own message in `userHint` instead of the generic
  "Network request failed".

## BREAKING — ConnectKit / unlock / RPC (hardening)

- **MetaMask / ECDSA unlock removed** from ConnectKit and `@splendor/sqc-sdk` connect paths (`connectFromMetaMask`, MetaMask connector, EIP-6963 MetaMask primary). Normative unlock is **Splendor extension + native seed** only (SQDP-1 §3).
- **`RPC_UNAVAILABLE`** error code when all RPC endpoints fail or confirmation polling fails closed on repeated transport errors (`security-rpc.test.ts`).
- **Extension-first ConnectKit** — default browser connectors are Splendor extension + seed; `setSessionWallet` commits extension sessions without in-page WASM seed.
- **Gateway public EVM unlock / derive removed** — public proxy rejects `seed` / `evmPrivateKey` / `evmSignature`; end users submit pre-signed `signedTx` only.
- Deprecated exports: `seedFromEvmSignature` and related derive helpers — offline tooling only; not for dApps.

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
