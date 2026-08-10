# SQDP-1 conformance checklist

Use this when shipping a Splendor dApp or wallet. Normative rules: [SQDP-1.md](./SQDP-1.md).

Automated coverage (run from `sdk/bridge`): `npm run test:security` (Vitest `security-*.test.ts` + Go signer + gateway inject).

## Identity and UI

- [ ] After connect, UI shows the **`SQ:`** address (67 chars), not the MetaMask `0x` address *(manual UI)*
- [ ] Funding instructions send SPLD to the `SQ:` address (MetaMask cannot send chain-native funds) *(manual UI)*
- [x] Addresses are validated / canonicalized (`assertAddress` or equivalent) — covered by `units.test.ts` / `security-errors.test.ts`

## Protocol constants

- [x] Uses `chain_id` from `PROTOCOL` / `sqc.networks.json` (not a hardcoded fork) — `protocol.test.ts` / `config-connect.test.ts`
- [x] Uses `createSplendorClient` / `connectSplendor({ network })` or equivalent config-driven RPC URLs — `config-connect.test.ts`
- [x] Derivation uses the exact SQDP-1 domain message (`DERIVATION_DOMAIN_MESSAGE`) — `security-derive.test.ts` / Go `TestDomainMessageConstant` / `testdata/golden-vectors.json`
- [x] Fees ≥ config `minFee` (`PROTOCOL.minFee`) — `security-wasm.test.ts` / Go `TestSignTransferRejectsFeeBelowMin`
- [x] QSol signatures use `sqaddress`; selector matches SHA3-256 first 4 bytes — `phase2.test.ts` / Go ABI golden

## SDK / signing

- [x] Signs with `@splendor/pq-bridge` WASM **or** produces identical wire txs — `wasm-session.test.ts` / Go round-trip + `TestTamperedTransferFailsVerify`
- [ ] Does not hand-author signed JSON from README examples *(integration practice)*
- [x] Calls `assertChainId` (or equivalent) before submit — `security-rpc.test.ts` (fail-closed on missing/mismatch)
- [x] Confirms inclusion via **nonce poll** (`waitForConfirmation`) — does not depend on `GET /tx/{hash}` or `/ws` — `security-errors.test.ts` (`TX_NOT_CONFIRMED`)

## Errors and events

- [x] Handles at least: `USER_REJECTED`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`, `INVALID_ADDRESS`, `INVALID_FEE` — `security-errors.test.ts` / `security-metamask.test.ts`
- [x] Listens for `connect` / `disconnect` / `accountsChanged` as needed — `security-metamask.test.ts` (fail-closed)
- [ ] Does not treat MetaMask EVM chain switch as Splendor chain change *(dApp UI practice)*

## Browser / WASM

- [ ] Serves `sqc_signer.wasm` + `wasm_exec.js` with correct MIME and CORS *(ops)* — checklist: [`WASM_HOSTING.md`](./WASM_HOSTING.md)
- [ ] Imports `@splendor/pq-bridge/browser` in browser builds *(bundler)*
- [x] Calls `lock()` or `disconnect()` on logout (zeroizes Dilithium key) — `security-wasm.test.ts` (re-derive Zeroize in WASM)
- [x] Sets `wasmSha256` when known; fails closed on mismatch — `security-wasm.test.ts` / `security-networks.test.ts` (browser pin required)
- [x] Pins `execSha256` for `wasm_exec.js` when configured — loaders + networks JSON / `sync:config`
- [x] MetaMask empty accounts / EIP-1193 `disconnect` destroy session — `security-metamask.test.ts`
- [x] Parallel `sendTransfer` cannot share a nonce — `security-mutex.test.ts`
- [x] Submits reject missing `signedTx.chain_id` — `security-rpc.test.ts` / `gateway/security.test.ts`

## Gateway (if used)

- [x] Public / end-user traffic uses **`proxy`** mode — `gateway/security.test.ts`
- [x] Public proxy rejects `seed`, `evmPrivateKey`, and (by default) `evmSignature` — `gateway/security.test.ts`
- [x] Prefers pre-signed `signedTx` only on public hosts — `gateway/security.test.ts` (signedTx path not custodial-rejected)
- [x] `ALLOW_CUSTODIAL_PROXY=1` refused with CORS `*` — `gateway/security.test.ts`
- [ ] `ALLOW_CUSTODIAL_PROXY=1` never enabled on public internet *(ops)*
- [x] `service-wallet` only for operator / automation keys + `GATEWAY_API_KEY` — `gateway/security.test.ts` (401 on missing/wrong key; timing-safe compare)
- [x] Rate limit + CORS allowlist configured — global + per-route tx limits (`gateway/security.test.ts` 429); `TRUST_PROXY` default false
- [x] `/ready` probes upstream `assertChainId`; `/health` is liveness — `gateway/security.test.ts`

## Production RPC

- [x] Configures `rpcUrls` failover for reads — `security-rpc.test.ts`
- [x] Sticky submit behaviour understood (first accepting URL) — `security-rpc.test.ts`
- [x] Sets `wasmSha256` pin for browser WASM integrity when serving signer assets — pin check in `security-wasm.test.ts` / networks JSON
- [x] Non-local RPC/gateway require `https://` unless `allowInsecureHttp` — `security-networks.test.ts`
- [ ] Uses distinct `networkId` from config (local/testnet/mainnet) even though wire `chain_id` is shared *(ops / config)*
