# SQDP-1 conformance checklist

Use this when shipping a Splendor dApp or wallet. Normative rules: [SQDP-1.md](./SQDP-1.md).

Automated coverage (run from `sdk/sqc-sdk`): `npm test` (Vitest including `security-*.test.ts` + WASM golden vectors). Go signer: `go test ./sdk/sqc-sdk/signer -count=1`. Full security suite: `npm run test:security`. ConnectKit: `npm test` from `sdk/connect-react` (`controller.test.ts`, `extension-signer` coverage via SDK).

## Identity and UI

- [ ] After connect, UI shows the **`SQ:`** address (67 chars), not an EVM `0x` address *(manual UI)*
- [ ] Funding instructions send SPLD to the `SQ:` address (EVM wallets cannot send chain-native funds) *(manual UI)*
- [x] Addresses are validated / canonicalized (`assertAddress` or equivalent) — covered by `units.test.ts` / `security-errors.test.ts`

## Protocol constants

- [x] Uses `chain_id` from `PROTOCOL` / `sqc.networks.json` (not a hardcoded fork) — `protocol.test.ts` / `config-connect.test.ts`
- [x] Uses `createSession` / `createWalletClient` / `connect({ network })` or equivalent config-driven RPC URLs — `config-connect.test.ts`
- [x] Fees ≥ config `minFee` (`PROTOCOL.minFee`) — `security-wasm.test.ts` / Go `TestSignTransferRejectsFeeBelowMin`
- [x] Contract call signatures use `sqaddress`; selector matches SHA3-256 first 4 bytes — `phase2.test.ts` / Go ABI golden (`vm.SelectorFromSignature`)

## Unlock (extension + native seed)

- [x] ConnectKit / dApp unlock is Splendor extension **or** native seed only — `extension-signer.test.ts` / connect-react `controller.test.ts` / `connectors.test.ts`
- [x] Historical EVM→PQ domain message vectors remain for offline tooling only — `security-derive.test.ts` / Go `TestDomainMessageConstant` / `testdata/golden-vectors.json` (not a ConnectKit path)
- [ ] Does not offer MetaMask / ECDSA derive as a primary connect option *(product / UI)*

## SDK / signing

- [x] Signs with `@splendor/sqc-sdk` WASM **or** extension-held keys producing identical wire txs — `wasm-session.test.ts` / `extension-signer.test.ts` / Go round-trip + `TestTamperedTransferFailsVerify`
- [ ] Does not hand-author signed JSON from README examples *(integration practice)*
- [x] Calls `assertChainId` (or equivalent) before submit — `security-rpc.test.ts` (fail-closed on missing/mismatch)
- [x] Confirms inclusion via a **required receipt** (nonce check only with `allowNonceConfirmation`) (`waitForConfirmation`) — `security-errors.test.ts` (`TX_NOT_CONFIRMED`) / `security-rpc.test.ts`

## Errors and events

- [x] Handles at least: `USER_REJECTED`, `NO_PROVIDER`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`, `INVALID_ADDRESS`, `INVALID_FEE`, `RPC_UNAVAILABLE` — `security-errors.test.ts` / `security-rpc.test.ts`
- [x] Listens for `connect` / `disconnect` / `accountsChanged` / `lock` as needed — connect-react `controller.test.ts` (lock + accountsChanged); extension provider events in `extension-signer.test.ts`
- [ ] Does not treat external EVM chain switch as Splendor chain change *(dApp UI practice)*

## Browser / WASM

- [ ] Serves `sqc_signer.wasm` + `wasm_exec.js` with correct MIME and CORS *(ops)* — checklist: [`WASM_HOSTING.md`](./WASM_HOSTING.md)
- [ ] Imports `@splendor/sqc-sdk/browser` in browser builds *(bundler)*
- [x] Calls `lock()` or `disconnect()` on logout (zeroizes ML-DSA-65 key for WASM sessions) — `security-wasm.test.ts`
- [x] Sets `wasmSha256` when known; fails closed on mismatch — `security-wasm.test.ts` / `security-networks.test.ts` (browser pin required)
- [x] Pins `execSha256` for `wasm_exec.js` when configured — loaders + networks JSON / `sync:config`
- [x] Parallel `sendTransfer` cannot share a nonce — `security-mutex.test.ts`
- [x] Submits reject missing `signedTx.chain_id` — `security-rpc.test.ts` / `gateway/security.test.ts`

## Gateway (if used)

- [x] Public / end-user traffic uses **`proxy`** mode — `gateway/security.test.ts`
- [x] Public proxy rejects `seed`, `evmPrivateKey`, and `evmSignature` — `gateway/security.test.ts`
- [x] Prefers pre-signed `signedTx` only on public hosts — `gateway/security.test.ts` (signedTx path not custodial-rejected)
- [x] `ALLOW_CUSTODIAL_PROXY=1` refused with CORS `*` — `gateway/security.test.ts`
- [ ] `ALLOW_CUSTODIAL_PROXY=1` never enabled on public internet *(ops)*
- [x] `service-wallet` only for operator / automation keys + `GATEWAY_API_KEY` — `gateway/security.test.ts` (401 on missing/wrong key; timing-safe compare)
- [x] Rate limit + CORS allowlist configured — global + per-route tx limits (`gateway/security.test.ts` 429); `TRUST_PROXY` default false
- [x] `/ready` probes upstream `assertChainId`; `/health` is liveness — `gateway/security.test.ts`

## Production RPC

- [x] Configures `rpcUrls` failover for reads — `security-rpc.test.ts`
- [x] Sticky submit behaviour understood (first accepting URL); sticky cleared after 3 consecutive sticky failures — `security-rpc.test.ts`
- [x] Surfaces `RPC_UNAVAILABLE` when all endpoints fail / confirmation infra fails closed — `security-rpc.test.ts`
- [x] Sets `wasmSha256` pin for browser WASM integrity when serving signer assets — pin check in `security-wasm.test.ts` / networks JSON
- [x] Non-local RPC/gateway require `https://` unless `allowInsecureHttp` — `security-networks.test.ts`
- [ ] Uses distinct `networkId` from config (local/testnet/mainnet) even though wire `chain_id` is shared *(ops / config)*
