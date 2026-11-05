---
"@splendor/sqc-sdk": patch
"@splendor/connect-react": minor
"@splendor/dapp-spec": patch
"create-splendor-dapp": patch
---

Fixes from the production scenario test pass.

- `ValidatorsClient` (`list`, `get`, `isApproved`) and `SqRpc.listValidators` / `getValidator` read the routed `validators` module through `POST /call`. They called `GET /validators` routes the node does not serve and sent `isApproved` a body `/call` rejects. `get` and `isApproved` reject a bad address instead of throwing synchronously; `approveCall` / `revokeCall` / `registerCall` validate their input.
- `VerifyClient.proof` and `SqRpc.verifyProof` refuse an instruction id that is not 32-byte hex, or a bad instance address, before the request.
- `QRC1155` checks the node's rules before signing: batches of at most `QRC1155_MAX_BATCH` (64, now exported), ids and amounts below 2^256, positive mint and burn amounts, collection names up to 64 bytes and URIs up to 2048 bytes, no self-approval, no transfer to the collection address. `mintBatch` accepts fewer `uris` than `ids`, like the node.
- `useModules()` also returns `verify` and `validators` clients.
- `generateProject` throws on a page path, prop name, container kind or `hideOn` that would be written into generated code unescaped (specs that skipped `validateSpec`).
- The Token stats block shows "—" for missing values; it rendered the mis-encoded "â€”". Blocks share `parseArgs`, `formatUnits` and `pulse` from `blocks/parseList.ts` instead of copying them.
- `@splendor/sqc-sdk` `prepack` runs on Windows as well (`scripts/prepack.mjs` picks `wasm/build.ps1` or `wasm/build.sh`), and `build.ps1` pins the same Go release as `build.sh` (go1.25.14), so a Windows build produces the pinned signer bytes.
- create-splendor-dapp: picks up the SDK releases above.
