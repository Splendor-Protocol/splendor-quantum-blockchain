---
"@splendor/sqc-sdk": patch
"@splendor/connect-react": minor
"@splendor/dapp-spec": patch
---

Fixes from the production scenario test pass.

- `ValidatorsClient` (`list`, `get`, `isApproved`) and `SqRpc.listValidators` / `getValidator` read the routed `validators` module through `POST /call`. They called `GET /validators` routes the node does not serve and sent `isApproved` a body `/call` rejects. `get` and `isApproved` reject a bad address instead of throwing synchronously; `approveCall` / `revokeCall` / `registerCall` validate their input.
- `VerifyClient.proof` and `SqRpc.verifyProof` refuse an instruction id that is not 32-byte hex, or a bad instance address, before the request.
- `QRC1155` checks the node's rules before signing: batches of at most `QRC1155_MAX_BATCH` (64, now exported), ids and amounts below 2^256, positive mint and burn amounts, collection names up to 64 bytes and URIs up to 2048 bytes, no self-approval, no transfer to the collection address. `mintBatch` accepts fewer `uris` than `ids`, like the node.
- `useModules()` also returns `verify` and `validators` clients.
- `generateProject` throws on a page path, prop name, container kind or `hideOn` that would be written into generated code unescaped (specs that skipped `validateSpec`).
