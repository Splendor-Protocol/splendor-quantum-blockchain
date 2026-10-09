# Changelog — `@splendor/connect-react`

Consumer-facing changes. The internal hardening log is [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).

## 3.0.0

Requires `@splendor/sqc-sdk` ≥ 3.0.0.

### Fixed

- **TokenPanel** sends the reviewed fee, blocks Confirm when the SPLD balance cannot cover the
  network fee, and keeps Review disabled until the token balance is known.
- **DeployTokenWidget** estimates the fee even with `review={false}` (and blocks Deploy when it
  is unaffordable, loading or reverting), closes the review when the wallet or network changes,
  and its result panel shows `confirmed` only for a confirmed deploy.
- Widgets reset their form when the wallet or network changes (not while a send is pending), and
  Confirm ignores a second click while a send is pending.
- **SignInButton** ignores double clicks: the button is disabled from the nonce request on, so
  `nonce` is called once.
- Balances refresh right after any send recorded by a widget or hook, without waiting for the
  next poll; `useQRC20` transfers no longer block on a balance refresh.
- `useEstimateFee` times out after 15 s and treats an empty node response as an error instead of
  loading forever.
- A failed shared read no longer evicts a newer in-flight read for the same key.

- **TokenPanel and DeployTokenWidget review shows the real fee.** The estimate is now made for
  the actual `qrc20.transfer` / `qrc20.deploy` module call, a predicted revert is shown, and
  Confirm stays disabled while the fee is loading or the node predicts a revert. Previously the
  fee was estimated for an empty call and reverts were hidden.
- **TransferWidget** disables Confirm while the fee loads, reserves the minimum network fee from
  Max and the balance check, and blocks Confirm when amount plus the estimated fee exceeds the
  balance.
- Transfer and deploy sends use the fee shown on the review screen instead of the minimum fee.
- **DeployTokenWidget** success toast reports `confirmed` from the receipt instead of always
  `true`. `useDeployToken().deploy()` returns `confirmed`.
- **`SplendorConnectProvider`** no longer drops the `networks` prop when `customNetworks` is
  omitted.
- `useTransferForm` follows changes to `defaultTo` / `defaultAmount` until the user edits that
  field (for example a new invoice).
- A second click while a transaction is signing says "A transaction is already in progress"
  instead of "Connection already in progress".
- `SignInButton` shows "Could not get a sign-in challenge" when the `nonce` callback fails,
  instead of the raw browser or server error.
- README `SignInButton` `onSuccess` fields match the real result (`signatureB64`,
  `publicKeyB64`, `messageDomain`).

### Performance

- `useBalance` and `useQRC20` share in-flight and recent reads across mounted hooks, so several
  widgets showing the same balance make one RPC call per poll window. `refresh()` still forces a
  fresh read.
- `usePendingTransactions` checks pending receipts in parallel.

### Added

- Labels: `insufficientWithFee` (all widgets), `nonceFailed` (`SignInButton`),
  `feeUnaffordable` (all widgets).
- `useQRC20().transfer` / `transferHuman` accept `WriteOpts` (for example `{ fee }`).
- `useConnectController`, `useConnectState` and `useNetworkSwitch` are exported from the
  package root.
- npm package ships `docs/` (the guide the README links to), `CHANGELOG.md` and `LICENSE`.

## 2.0.0

See [`../CHANGELOG-HARDENING.md`](../CHANGELOG-HARDENING.md).
