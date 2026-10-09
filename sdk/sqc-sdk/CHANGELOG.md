# Changelog — `@splendor/sqc-sdk`

Consumer-facing changes. The internal hardening log is [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).

## 3.0.0

Major release: confirmation, submit retries and subscriptions behave differently (see below).

### Behavior changes

- **Confirmation requires a receipt.** `sendTransfer`, `callContract`, `deployContract`,
  `sendModuleCall` and `sendTransaction` with `waitForConfirmation: true` now pass
  `requireReceipt` whenever the submit returned a hash, on both the seed wallet and
  `ExtensionSigner`. A nonce advance alone no longer counts, because another transaction from
  the same account also advances it. Pass `allowNonceConfirmation: true` on nodes without a
  receipt index. `wallet.waitForConfirmation()` accepts `requireReceipt` directly.
- **`ExtensionSigner` always confirms against the node.** A `confirmation` field reported by the
  extension is ignored; with `waitForConfirmation: true` the SDK polls the node itself.
- **`SplendorWallet.sendTransaction` / `signAndSendTransaction`** require a receipt for every
  transaction type and accept `allowNonceConfirmation`.
- **Submit is not retried after a timeout.** `POST /tx/submit` that times out throws `TIMEOUT`
  with a hint to check activity, instead of resending (which could look like a failure even
  though the first send landed). A node `duplicate transaction` / `duplicate pending nonce`
  rejection is `RPC` with an "already submitted" hint.
- **RPC timeouts cover the response body.** A node that sends headers and then stalls now hits
  the request timeout instead of hanging.
- **Node subscriptions rotate across RPC URLs.** On failure the stream moves to the next URL and
  resumes from the last event id. `maxRetries` counts full passes over the URL list, and the
  counter resets once a connection delivers data. Aborting `opts.signal` closes the stream on
  both Node and the browser.
- **Write methods take fee and confirmation options.** `QRC20`, `QRC1155`, `QRC721` and
  `BridgeModule` write methods (including `transferHuman` / `approveHuman`) accept a trailing
  `WriteOpts` (`fee`, `waitForConfirmation`, `confirmTimeoutMs`, `allowNonceConfirmation`).
- **Deploy helpers report the address from the signed nonce.** `deployQRC20/1155/721`,
  `deployBridge` and `deployBytecode` compute the address from the nonce the transaction was
  actually signed with, so a concurrent send cannot shift it.
- `PROTOCOL.confirmationCaveat` is `receipt-required;nonce-opt-in-race` (was
  `receipt-preferred;nonce-fallback-race`).
- **Browser subscriptions no longer stop after one error with a single RPC URL.** `subscribe()`
  lets `EventSource` retry transient drops itself (it resends `Last-Event-ID`) and rebuilds the
  stream with jittered backoff once the browser gives up. `reconnect` and `maxRetries` now apply
  in the browser too.
- `X-SQC-Bridge` now reports `3.0.0` (was `1.4.0`).

### Added

- `WriteOpts` type and `redactRpcUrl()` export.
- `@splendor/sqc-sdk/tokens/qrc721` export subpath.
- `createSession()` binds QRC-721: `s.nft(addr)` / `s.qrc721(addr)` and `s.deployNftCollection()`.
  Session deploy helpers take `WriteOpts`.
- `QRC721`, `deployQRC721`, `getNftCollection`, `qrc721Address` — thin client for the routed
  QRC-721 actions (`deploy`, `mint`, `transfer`, `burn`, `owner_of`, `balance_of`, `token_info`).
- `RpcOptions.requestId` / `ConnectionOptions.requestId` — opt-in `X-Request-Id` header.
- `BridgeError.txBusy()` and an optional `userHint` argument on `BridgeError.busy()`.
- [`docs/ERRORS.md`](docs/ERRORS.md) — every error code with user hint and retry guidance.
- `LICENSE` (MIT) shipped with the package.

### Fixed

- A node rejection of `fee too low` at submit is `INVALID_FEE` with a congestion hint instead of
  a generic `RPC` error.
- RPC retries use jittered delays so many clients do not retry in lockstep.
- Gateway: logs carry a `reqId` (a caller's `X-Request-Id` when short and plain, else a random
  UUID) and echo it back in `x-request-id`.
- SQDP-1 §9 lists `BUSY` and `INVALID_PARAMS`.
- `QRC721.collectionInfo().owner` is an `SQ:` address instead of a raw byte array.
- Gateway: `/health` and the startup log redact credentials in the RPC URL, and CORS exposes
  `x-request-id` so browsers can read it.
- The build clears `dist/` first, so removed modules (such as the old `metamask` connector) no
  longer ship.

### Node (same repo)

- RPC CORS allow-list includes `X-SQC-Bridge` and `X-Request-Id`. Without the former, browser
  preflights from allowed origins failed for every SDK request.

## 2.0.0

See [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).
