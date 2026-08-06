# Splendor dApp Protocol — SQDP-1

**Status:** Normative  
**Version:** `1`  
**Implements:** `@splendor/pq-bridge` ≥ 1.2.1  
**Keywords:** MUST / SHOULD / MAY (RFC 2119)

This document is the **single source of truth** for how dApps, wallets, and gateways talk to Splendor Quantum Chain via the PQ bridge. Short how-to: [DAPP_INTEGRATION.md](./DAPP_INTEGRATION.md). Conformance checklist: [SQDP-1-CONFORMANCE.md](./SQDP-1-CONFORMANCE.md).

Machine-readable constants are exported as `PROTOCOL` / `SQDP_VERSION` from `@splendor/pq-bridge`.

**Single config file:** network RPCs, gateway ports, WASM paths, min fee, and default network MUST be taken from `src/config/sqc.networks.json` (or the published `config/sqc.networks.json` mirror). dApps SHOULD use `createSplendorClient` / `connectSplendor({ network })` instead of hardcoding URLs.

---

## 1. Identity

1. On-chain accounts MUST use addresses of the form `SQ:` + **exactly 64 hexadecimal characters** (32 bytes).
2. Canonical form MUST use **lowercase** hex after the `SQ:` prefix (e.g. `SQ:ab12…`). Implementations MUST accept mixed-case hex and SHOULD normalize via `assertAddress`.
3. An `SQ:` address MUST NOT be treated as interchangeable with an EVM `0x` address. Balances, nonces, and transfers live **only** on the `SQ:` identity.
4. Address bytes = `SHA3-256(Dilithium Mode3 public key bytes)`.

---

## 2. Chain

1. The network `chain_id` MUST be the string `splendor-quantum-1` unless a future SQDP revision defines another network.
2. Clients MUST call status / `assertChainId` (or equivalent) before submitting signed transactions and MUST reject a mismatch (`CHAIN_MISMATCH`).
3. MetaMask (or other EVM) chain switches MUST NOT be interpreted as Splendor chain changes. Derivation does not require a specific EVM chain id.

---

## 3. Key derivation (EVM → PQ)

1. Clients that map an EVM account to a PQ wallet MUST use this exact UTF-8 domain message (version `v1`):

```
Splendor Quantum Chain
Key derivation v1
chain: splendor-quantum-1
```

2. The user MUST authenticate via EIP-191 `personal_sign` of that message (MetaMask or local secp256k1).
3. Dilithium seed MUST be `SHA3-256(signature_bytes)` (65-byte Ethereum-style signature).
4. Keypair MUST be Cloudflare circl **Dilithium mode3 (round 3)** `NewKeyFromSeed(seed)`. Pure-JS ML-DSA (FIPS 204) MUST NOT be used for wire signatures — it is incompatible.
5. Changing the domain message is a **breaking** protocol change (new address map).

Exported: `DERIVATION_DOMAIN_MESSAGE`, `CHAIN_ID`.

---

## 4. Units and fees

1. Native unit: **SPLD**. `1 SPLD = 10^18` base units.
2. Transaction `fee` MUST be an integer string of base units and MUST be ≥ `1000` (`MIN_FEE` / mempool minimum).
3. Clients SHOULD use `fromSPLD` / `toSPLD` helpers rather than ad-hoc decimal math.

---

## 5. QSol ABI

1. Function selector MUST be the first **4 bytes** of `SHA3-256(UTF-8 signature)` — **not** Keccak-256.
2. Arguments MUST be packed as 32-byte big-endian words after the selector.
3. Address-typed parameters in signatures MUST use the type name **`sqaddress`** (not `address`).
4. Golden test: `transfer(sqaddress,uint256)` selector = `14cde9ab`.
5. Encoding an `SQ:` address into a word MUST use the 32 raw bytes (strip `SQ:`), right-aligned in the 32-byte slot (full 32 bytes occupied).

Exported: `encodeFunctionData`, `functionSelectorHex`, decode helpers.

---

## 6. Transactions

| Type | Value | SDK |
|------|-------|-----|
| Transfer | `0x01` | `sendTransfer` / `signTransferOnly` |
| Deploy | `0x02` | `deployContract` |
| Call | `0x03` | `callContract` |
| X402 | `0x05` | `payX402` |

1. dApps MUST submit transactions produced by the official WASM signer (`@splendor/pq-bridge`) or an implementation that emits **identical** Go `encoding/json` wire shapes.
2. dApps MUST NOT hand-author signed tx JSON from human README examples. On the wire, `from` / `to` are JSON **arrays of 32 numbers**; `amount` / `fee` are JSON numbers; `public_key` / `signature` are base64.
3. When `nonce == 0`, the signed tx MUST include `public_key` (first-account registration).
4. Clients SHOULD prefer client-side WASM signing and `POST /tx/submit` (or gateway `/v1/tx/submit` with a pre-signed body) for end-user flows.

---

## 7. Confirmation

1. The supported confirmation mechanism is **nonce advancement**: wait until account nonce `>` the nonce used when signing.
2. Clients MUST NOT rely on `GET /tx/{hash}` (stub — not implemented on the node).
3. Clients MUST NOT rely on `/ws` subscriptions (code exists but is **not mounted** on the live RPC router).
4. `waitForConfirmation` (or equivalent) SHOULD be used after transfers when UX requires inclusion certainty.

`PROTOCOL.confirmation === 'nonce'`.

---

## 8. Connection events

Clients using `@splendor/pq-bridge` MUST use these event names and payloads:

| Event | Payload |
|-------|---------|
| `connect` | `{ address: string /* SQ: */, evmAddress?: string }` |
| `disconnect` | `{}` |
| `accountsChanged` | `{ address: string, evmAddress?: string }` |

1. After MetaMask `accountsChanged`, wallets SHOULD re-`personal_sign` and re-derive (SDK default). Empty accounts SHOULD lock and emit `disconnect`.
2. `lock()` MUST clear the Dilithium key from WASM memory. `disconnect()` MUST destroy the session.

---

## 9. Errors

Clients MUST handle these `BridgeError` codes (string `code` field):

`NOT_CONNECTED`, `USER_REJECTED`, `RPC`, `NETWORK`, `WASM`, `INVALID_ADDRESS`, `INVALID_AMOUNT`, `INVALID_FEE`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`, `UNKNOWN`.

Gateway HTTP errors SHOULD return JSON `{ "code": "<CODE>", "message": "..." }` using the same code strings when applicable.

---

## 10. RPC client behaviour

1. Clients MAY supply `rpcUrls: string[]` for read failover. Empty lists MUST be rejected.
2. On timeout / HTTP 5xx, clients SHOULD retry (per-URL) then fail over to the next URL.
3. After a successful submit, clients SHOULD sticky-route subsequent submits to the accepting URL.
4. Requests SHOULD send header `X-SQC-Bridge: <bridge semver>` (e.g. `1.2.1`).

---

## 11. Gateway

| Mode | Use |
|------|-----|
| `proxy` (default) | End-user / public — relays **pre-signed** `signedTx` only |
| `service-wallet` | Operator automation only — holds one PQ key |

1. Public gateways MUST run `proxy` mode for end users.
2. Public proxy MUST reject `seed`, `evmPrivateKey`, and (unless `ALLOW_CUSTODIAL_PROXY=1`) `evmSignature`. Prefer client-side WASM signing + `POST /v1/tx/submit`.
3. Posting a Dilithium `seed` or derivation signature to a gateway is **custodial** for that request. `ALLOW_CUSTODIAL_PROXY=1` is for private backends only.
4. `service-wallet` MUST NOT be used to hold end-user keys. Production service-wallet SHOULD require `GATEWAY_API_KEY`.
5. Gateways SHOULD enable rate limiting, CORS allowlists, and body size limits.

---

## 12. Security threat model (normative guidance)

| Threat | Mitigation |
|--------|------------|
| Phishing a different derivation message | MUST use exact SQDP-1 domain text; show it in UI before sign — the signature **is key material** |
| Confusing `0x` with `SQ:` | UI MUST display `SQ:` after connect; never send EVM ETH as “funding” |
| Wrong chain | MUST `assertChainId` before submit (all tx types) |
| Seed / EVM key / derivation sig theft via gateway | Public proxy MUST reject custodial credentials; submit `signedTx` only |
| Session residue | MUST `lock` / `disconnect` on logout (`lock` zeroizes Dilithium key material) |
| Fake confirmation | MUST NOT treat `/tx/{hash}` 404 stub as success; nonce poll can race with other txs |
| MetaMask account race | MUST fail-closed (lock + disconnect) if re-derive fails after `accountsChanged` |
| Tampered WASM | Browser SHOULD set `wasmSha256` network pin and fail closed on mismatch |
| Signature / address mismatch | MUST `ecrecover` and bind claimed EVM address before derive |

Encrypted browser session cache of Dilithium seeds is **out of scope** for SQDP-1 (default remains re-sign each session).

Optional SQDP-1.1 path: EIP-712 `SplendorPQDerive` typed data (`derivation: 'eip712-v1'`). Seed rule remains `SHA3-256(signature_bytes)`. Default remains EIP-191 `personal_sign` (`eip191-v1`).

Wire `chain_id` remains `splendor-quantum-1` for all deployments until the node defines otherwise. Client configs expose distinct `networkId` values (`splendor-local` / `splendor-testnet` / `splendor-mainnet`) for UX and failover selection.

---

## 13. Provider vs wallet

1. Read-only access SHOULD use `SqcProvider` (no WASM) until the user connects.
2. Signing MUST use `SplendorPQBridge` (or `fromProvider`) after connect.

---

## Revision history

| Version | Notes |
|---------|--------|
| SQDP-1 | Initial normative protocol aligned with `@splendor/pq-bridge` 1.2.x |
