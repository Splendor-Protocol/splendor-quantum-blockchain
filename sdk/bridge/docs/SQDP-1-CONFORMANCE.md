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
- [ ] Calls `lock()` or `disconnect()` on logout

## Gateway (if used)

- [ ] Public / end-user traffic uses **`proxy`** mode
- [ ] Never accepts end-user `evmPrivateKey` on public routes
- [ ] Prefers pre-signed `signedTx` over posting Dilithium `seed` to a public host
- [ ] `service-wallet` only for operator / automation keys

## Production RPC

- [ ] Configures `rpcUrls` failover for reads
- [ ] Sticky submit behaviour understood (first accepting URL)
