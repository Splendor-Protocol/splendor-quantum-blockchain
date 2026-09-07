# Changelog

## @splendor/sqc-sdk — `min_fee_v1` and `qrc721_v1` notes

- `MIN_FEE_V1` / `PROTOCOL.minFeeV1` (`1000000000000000` = 0.001 SPLD): the floor on a network
  whose `/status.forks` lists `min_fee_v1`. **`MIN_FEE` / `PROTOCOL.minFee` are unchanged**
  (1,000): flipping the default is a coordinated release with the wallet extension and every
  dApp pinning the constant, and no tier has scheduled the upgrade yet. Pass `fee: MIN_FEE_V1`
  explicitly where the network needs it.
- `qrc721_v1` is routed on-chain (owner-only mint, ERC-721 transfers/approvals/burn). No SDK
  Program yet: `known-module-actions.json` lists it with every action as a declared gap.

## @splendor/sqc-sdk — SQVL verify module (`verify_v1`)

- **Signer WASM rebuilt** (go1.25.0, `wasm/build.sh`): new exports `sqGetSLHPublicKeyHex`,
  `sqSignVerifyInstruction`, `sqSignVerifyAttestation`. `sqc_signer.wasm` sha256 is now
  `bec3f8d6dcb4804ab191ca86263d91eb694379c828998170d9579bcecf700c2e` (pinned in both
  `sqc.networks.json` copies). **`splendor-quantum-wallet-extension` must run
  `npm run sync:signer`** to take the new bytes and re-pin `WASM_SHA256`; its committed copy
  is stale until it does.
- Every session now carries an SLH-DSA-SHA2-128s key derived from the same seed as the
  ML-DSA key (own domain, derived lazily on first SQVL use). `Wallet.getSLHPublicKeyHex()`,
  `Wallet.signVerifyInstruction()`, `Wallet.signVerifyAttestation()`; `Signer` gains the two
  optional capabilities.
- `VerifyModule` / `deployVerify` / `getVerify` (Program: `register`, `attest`, `finalize`,
  `spend`, `setOperator`, `setThreshold`, `pause`, `unpause`; reads `proof`, `instance`),
  registered as built-in module `verify`; `verifyAddress` derivation; `session.verify(addr)` /
  `session.deployVerify`.
- `SqRpc.verifyCheck` / `verifyProof` / `verifyAttest` / `verifySettle` — the `/verify/*` doors.
- Golden vectors: `verify` section (instruction digest, instance-bound attestation digest,
  seeded SLH public key), pinned by Go `TestGoldenVerifyVectorsPinDigests` and
  `wasm-verify.test.ts`.
- Wire note: `instruction_id` is a 64-char hex string (never an integer array); byte fields are
  base64. Attestation fields accept hex or base64, length-checked.

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
