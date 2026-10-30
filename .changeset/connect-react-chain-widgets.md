---
"@splendor/connect-react": minor
---

Chain-native widgets and hooks.

- New widgets (manifest, root exports and `embed` entries): `NftPanel` (QRC-721 grid, send, owner-only mint), `BridgeClaimWidget` (status, burn lookup, opt-in operator claim), `ValidatorList`, `ActivityFeed`, `ReceiveWidget` (QR code and `splendor:` payment link), `AddressBook` and `TokenBalances`, plus a `QrCode` part.
- New hooks: `useValidators` (routed `validators.list` / `bond` / `approved` reads), `useActivity` (QRC-20 and QRC-1155 transfer logs plus pending sends, optional `/idx/v1/logs` indexer) and `useAddressBook`.
- `useBridge().info` and `claimed` now read through the page connection, so they work before a wallet connects.
- `AddressInput` takes `contacts`; `TransferWidget`, `TokenPanel` and `NftPanel` offer saved contacts as quick picks.
- Helpers: `encodeQr` / `qrSvgPath` (dependency-free QR encoder), `buildPaymentUri` / `parsePaymentUri`, `parseActivityLog` / `mergeActivity` and the transfer topic constants.
- `@splendor/connect-react/testing`: `mockWallet` answers `connection.getStatus` / `getLogs` (`height`, `logs` options) and records `payX402`.
