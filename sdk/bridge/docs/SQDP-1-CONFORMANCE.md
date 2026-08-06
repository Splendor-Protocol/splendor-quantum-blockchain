# SQDP-1 conformance checklist

Use this when shipping a Splendor dApp or wallet. Normative rules: [SQDP-1.md](./SQDP-1.md).

## Identity and UI

- [ ] After connect, UI shows the **`SQ:`** address (67 chars), not the MetaMask `0x` address
- [ ] Funding instructions send SPLD to the `SQ:` address (MetaMask cannot send chain-native funds)
- [ ] Addresses are validated / canonicalized (`assertAddress` or equivalent)

## Protocol constants

- [ ] Uses `chain_id` from `PROTOCOL` / `sqc.networks.json` (not a hardcoded fork)
- [ ] Uses `createSplendorClient` / `connectSplendor({ network })` or equivalent config-driven RPC URLs
- [ ] Derivation uses the exact SQDP-1 domain message (`DERIVATION_DOMAIN_MESSAGE`)
- [ ] Fees ≥ config `minFee` (`PROTOCOL.minFee`)
- [ ] QSol signatures use `sqaddress`; selector matches SHA3-256 first 4 bytes

## SDK / signing

- [ ] Signs with `@splendor/pq-bridge` WASM **or** produces identical wire txs
- [ ] Does not hand-author signed JSON from README examples
- [ ] Calls `assertChainId` (or equivalent) before submit
- [ ] Confirms inclusion via **nonce poll** (`waitForConfirmation`) — does not depend on `GET /tx/{hash}` or `/ws`

## Errors and events

- [ ] Handles at least: `USER_REJECTED`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`, `INVALID_ADDRESS`, `INVALID_FEE`
- [ ] Listens for `connect` / `disconnect` / `accountsChanged` as needed
- [ ] Does not treat MetaMask EVM chain switch as Splendor chain change

## Browser / WASM

- [ ] Serves `sqc_signer.wasm` + `wasm_exec.js` with correct MIME and CORS
- [ ] Imports `@splendor/pq-bridge/browser` in browser builds
- [ ] Calls `lock()` or `disconnect()` on logout (zeroizes Dilithium key)
- [ ] Sets `wasmSha256` when known; fails closed on mismatch
- [ ] MetaMask `accountsChanged` fail-closed (no stale unlocked session)

## Gateway (if used)

- [ ] Public / end-user traffic uses **`proxy`** mode
- [ ] Public proxy rejects `seed`, `evmPrivateKey`, and (by default) `evmSignature`
- [ ] Prefers pre-signed `signedTx` only on public hosts
- [ ] `ALLOW_CUSTODIAL_PROXY=1` never enabled on public internet
- [ ] `service-wallet` only for operator / automation keys + `GATEWAY_API_KEY`
- [ ] Rate limit + CORS allowlist configured

## Production RPC

- [ ] Configures `rpcUrls` failover for reads
- [ ] Sticky submit behaviour understood (first accepting URL)
- [ ] Sets `wasmSha256` pin for browser WASM integrity when serving signer assets
- [ ] Uses distinct `networkId` from config (local/testnet/mainnet) even though wire `chain_id` is shared
