# Changelog — `@splendor/sqc-sdk`

Consumer-facing changes. The internal hardening log is [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).

## Unreleased

### Behavior changes

- **Confirmation requires a receipt.** `sendTransfer`, `callContract`, `deployContract`,
  `sendModuleCall` and `sendTransaction` with `waitForConfirmation: true` now pass
  `requireReceipt` whenever the submit returned a hash, on both the seed wallet and
  `ExtensionSigner`. A nonce advance alone no longer counts, because another transaction from
  the same account also advances it. Pass `allowNonceConfirmation: true` on nodes without a
  receipt index. `wallet.waitForConfirmation()` accepts `requireReceipt` directly.
- **Browser subscriptions no longer stop after one error with a single RPC URL.** `subscribe()`
  lets `EventSource` retry transient drops itself (it resends `Last-Event-ID`) and rebuilds the
  stream with jittered backoff once the browser gives up. `reconnect` and `maxRetries` now apply
  in the browser too.
- `X-SQC-Bridge` now reports `2.0.0` (was `1.4.0`).

### Added

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

### Node (same repo)

- RPC CORS allow-list includes `X-SQC-Bridge` and `X-Request-Id`. Without the former, browser
  preflights from allowed origins failed for every SDK request.

## 2.0.0

See [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).
