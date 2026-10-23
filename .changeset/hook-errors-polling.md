---
"@splendor/connect-react": minor
---

- Transaction errors no longer read "Connection failed". `connectErrorMessage(err, context)` takes an optional `'tx'` context; transaction hooks and widgets use it, so an unknown failure says "Transaction failed — try again" and a second send says "A transaction is already in progress". `INVALID_PARAMS` errors show the SDK's own message (which argument is wrong). New `ErrorContext` type.
- Hooks throw a `BridgeError` instead of a plain `Error` when called too early: `NOT_CONNECTED` without a wallet, `INVALID_PARAMS` when the token, collection, bridge or contract address is not set. Messages are unchanged; `errorMessage` now gives UI copy for them.
- `useQRC20`, `useQRC1155`, `useBridge` and `useContract` return `errorMessage`, like the other transaction hooks.
- Extension `error` events keep `INVALID_PARAMS` (it used to become `UNKNOWN`) and a short `userHint` the wallet sends.
- `useModuleCall` reports a malformed call as `INVALID_PARAMS`.
- Polling pauses while the browser tab is hidden and runs once when it is visible again (`useBalance`, `useQRC20`, `useTokenInfo`, `useTokenBalance`, `useReadContract`, `usePendingTransactions`).
- `useQRC1155().refresh` is deprecated: it never did anything.
- Docs: QRC-721 is listed with `useQRC721` / `useDeployQRC721` instead of "use `useModuleCall`".
