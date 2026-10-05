# Changelog

## Security hardening + builder add-ons (unreleased)

### Breaking changes

- **ABI string arguments are base-10 unless `0x`-prefixed.** `call('set(uint256)', ['42'])` now
  stores 42 (it used to be read as hex and stored 66). Bare hex such as `'ff'`, leading zeros and
  free text throw `INVALID_PARAMS`; unsafe JS numbers (above 2^53, fractions) throw too. Pass
  `'0xff'`, a bigint or a decimal string.
- **Extension chain check fails closed.** When the extension does not report `sqc_chainId`, sends
  are refused with `CHAIN_MISMATCH`. Opt out with `allowUnknownWalletChain: true` on
  `connectExtension` / `extensionConnector`.
- **x402 replay is rejected.** A spent nonce is only accepted as an idempotent retry of the same
  request this server already granted; `createX402Middleware` now uses a bounded in-memory
  consumption store by default, checks `to`, amount and `chain_id` before calling the node, and
  answers 403 for paths with no configured price. Use a durable store with `putIfAbsent` when
  running more than one instance.
- **Extension sends wait when asked.** `waitForConfirmation: true` on an `ExtensionSigner` send now
  polls the receipt instead of returning the submit result.
- **WASM pins:** `execSha256` is required whenever `wasmSha256` is required.
- **Raw amounts** (native amount, contract `value`, `payX402` amount) must be non-negative integers
  in base units; `parseRawAmount` rejects decimals, exponents and unsafe numbers.
- **`SqRpc` requires `https://`** for non-loopback hosts (opt out with `allowInsecureHttp`) and
  refuses HTTP redirects. Metadata hosts, IPv4-mapped IPv6 and `fd00:ec2::254` are blocked.
- **Sign-in messages** reject CR/LF, control characters and Unicode line separators in every field.
  `checkAuthMessage` rejects messages without an expiry after `maxAgeSec` (default 600 s).
- **`useWallet().ready` is `false` while the wallet is locked.**
- **`DeployTokenWidget` has a review step** ("Review deploy", then "Confirm deploy"), and name and
  symbol have length limits.
- **Seed sessions ask before signing.** In-page seed wallets show `<SignApprovalModal />` before
  every send, deploy, module call and message signature. It is mounted by `ConnectButton` /
  `ModalHost`; with no approval UI mounted, signing fails closed with `USER_REJECTED`. Pass
  `onBeforeSign` on the provider to use your own approval (or `() => true` in tests and kiosks).
- **`addressExplorerUrl`** returns `null` for non-`https` explorers (loopback `http` is allowed)
  and no longer percent-encodes the `SQ:` address.
- Explorer links (`txExplorerUrl`, `AccountModal`, toasts) only render for safe schemes, and
  `registerNetwork` rejects unsafe `explorerUrl` / `txUrlTemplate` values.

### Security fixes

- `TokenPanel`: the token field is hidden while reviewing, the review shows the contract address
  and closes when the token changes; late balance results for an old token are dropped.
- `useBalance` and `useQRC20` ignore responses for a previous account, network or token.
- `useTxAction` / `useSendTransfer` block double submits; Confirm is disabled on a wallet/network
  mismatch.
- Amount fields enforce decimal places (18 for SPLD, token decimals for QRC-20).
- `ConnectModal`: seed inputs are uncontrolled and masked from autofill and session replay
  (`data-private`, `data-sentry-mask`, `data-hj-suppress`), wiped after a failed import; a copied
  seed is cleared from the clipboard after 45 s if it is still there; a revealed seed re-masks after
  60 s; a new seed must be confirmed by typing 4 of its characters back.
- `Keypair` never serializes its seed; `SplendorPQBridge` drops a `seed` passed in its options.
- WASM exports are detached from `globalThis` after load.
- Error details redact RPC URLs (origin only) and truncate invalid address input.
- QRC-20 decimals are bounded to 0–36 and can be pinned (`pinDecimals`).
- Fees: optional `maxFee` cap on the wallet; `payX402` defaults `valid_before` to now + 300 s and
  accepts `maxAmount`.
- `sqcWasmPlugin` prints and emits `sqc-wasm.sha256.json` and defines `VITE_SQC_WASM_SHA256` /
  `VITE_SQC_EXEC_SHA256`.

### Added

- `@splendor/sqc-sdk/auth`: `issueNonce`, `createMemoryNonceStore`, `verifySignIn`,
  `addressFromPublicKey`, `wasmSignatureVerifier`.
- Read hooks that work without a wallet: `useTokenInfo`, `useTokenBalance`, `useReadContract`,
  `useReadConnection`.
- `useEstimateFee` and the `FeeRow` part; `TransferWidget` blocks Confirm when the node predicts a
  revert. `showFee` on `TransferWidget`, `TokenPanel` and `DeployTokenWidget`.
- Pending transactions survive reloads (`sqc.connect.pendingTx`, max 20, 24 h):
  `usePendingTransactions` and the `PendingTxList` part.
- `@splendor/connect-react/testing`: `createMockConnectController`, `MockSplendorProvider`,
  `mockWallet`, `mockConnector`.

### Follow-ups outside TypeScript (not changed here)

- **Go WASM signer:** generate session ids from a CSPRNG inside the signer instead of accepting
  caller-chosen ids, and expose a single export handle instead of many globals (the SDK detaches
  them after load as a mitigation).
- **Local node:** use a distinct chain id such as `splendor-quantum-local-1`. The local network
  still shares the mainnet chain id, so signatures made against it are valid on mainnet; the SDK
  warns about this and refuses mainnet-id configs that point at private hosts unless
  `allowLocalMainnetChainId: true`.
- **Wallet extension:** report `sqc_chainId` on every connection so dApps do not need
  `allowUnknownWalletChain`.

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
