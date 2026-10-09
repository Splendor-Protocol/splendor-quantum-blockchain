# Splendor dApp Protocol — SQDP-1

**Status:** Normative  
**Version:** `1`  
**Implements:** `@splendor/sqc-sdk` ≥ 3.0.0 (`@splendor/pq-bridge` ≥ 1.5.0 is a deprecated re-export shim)  
**Keywords:** MUST / SHOULD / MAY (RFC 2119)

This document is the **single source of truth** for how dApps, wallets, and gateways talk to Splendor Quantum Chain via the PQ SDK. Short how-to: [DAPP_INTEGRATION.md](./DAPP_INTEGRATION.md). Conformance checklist: [SQDP-1-CONFORMANCE.md](./SQDP-1-CONFORMANCE.md).

Machine-readable constants are exported as `PROTOCOL` / `SQDP_VERSION` from `@splendor/sqc-sdk`.

**Single config file:** network RPCs, gateway ports, WASM paths, min fee, and default network MUST be taken from `src/config/sqc.networks.json` (or the published `config/sqc.networks.json` mirror). dApps SHOULD use `createSession` / `createWalletClient` / `connect({ network })` instead of hardcoding URLs.

---

## 1. Identity

1. On-chain accounts MUST use addresses of the form `SQ:` + **exactly 64 hexadecimal characters** (32 bytes).
2. Canonical form MUST use **lowercase** hex after the `SQ:` prefix (e.g. `SQ:ab12…`). Implementations MUST accept mixed-case hex and SHOULD normalize via `assertAddress`.
3. An `SQ:` address MUST NOT be treated as interchangeable with an EVM `0x` address. Balances, nonces, and transfers live **only** on the `SQ:` identity.
4. Address bytes = `SHA3-256(ML-DSA-65 public key bytes)` (1952-byte public key, NIST FIPS 204).

---

## 2. Chain

1. The network `chain_id` MUST be the string `splendor-quantum-1` unless a future SQDP revision defines another network.
2. Clients MUST call status / `assertChainId` (or equivalent) before submitting signed transactions and MUST reject a mismatch (`CHAIN_MISMATCH`).
3. External EVM wallet chain switches (if any legacy tooling is present) MUST NOT be interpreted as Splendor chain changes.

---

## 3. Account unlock (normative)

dApps and ConnectKit MUST unlock accounts **only** via:

1. **Splendor Quantum Wallet browser extension** — native ML-DSA-65 keys held in the extension (`connect` / `extensionConnector` / `ExtensionSigner`). The dApp MUST NOT receive the seed.
2. **Native seed** — 32-byte CSPRNG seed → `mldsa65.NewKeyFromSeed(seed)` in WASM (`connectFromSeed` / seed connector). Returns `postQuantumSecure: true`, `keyOrigin: 'native'`.

ECDSA / MetaMask / EVM→PQ derivation is **not** a normative unlock path. Historical derivation text lives in [Appendix A](#appendix-a-historical-evm--pq-derivation-informative).

Exported for identity: `CHAIN_ID`, `PROTOCOL`.

---

## 4. Units and fees

1. Native unit: **SPLD**. `1 SPLD = 10^18` base units.
2. Transaction `fee` MUST be an integer string of base units and MUST be ≥ `1000` (`MIN_FEE` / protocol floor in `sqc.networks.json`).
3. Clients SHOULD use `fromSPLD` / `toSPLD` helpers rather than ad-hoc decimal math.
4. The bridge SDK does **not** charge an extra product/markup fee — only the chain mempool fee applies.
5. Under congestion the live node MAY raise the accepted floor above `1000` (e.g. `MinFee * N` in mempool). Clients MUST treat node `INVALID_FEE` / reject responses as authoritative and retry with a higher fee; SDK `assertFeeAtLeastMin` only enforces the static protocol floor.

---

## 5. Contract call ABI

Named "QSol ABI" — this is the calling convention for SQC-VM bytecode and the
node's native modules. A prototype QSol-subset compiler (`compiler/`,
`cmd/sqc-solc`) exists in the monorepo and emits deployable SQC-VM bytecode;
it is not a production-complete Solidity toolchain. The TypeScript SDK encodes
and submits calls against this ABI; it does not embed a compile-from-source API.

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

1. dApps MUST submit transactions produced by the official WASM signer (`@splendor/sqc-sdk`) or an implementation that emits **identical** Go `encoding/json` wire shapes.
2. dApps MUST NOT hand-author signed tx JSON from human README examples. On the wire, `from` / `to` are JSON **arrays of 32 numbers**; `amount` / `fee` are JSON numbers; `public_key` / `signature` are base64.
3. When `nonce == 0`, the signed tx MUST include `public_key` (first-account registration).
4. Clients SHOULD prefer client-side WASM signing and `POST /tx/submit` (or gateway `/v1/tx/submit` with a pre-signed body) for end-user flows.

---

## 7. Confirmation

1. Preferred confirmation: poll `GET /tx/{hash}/receipt` until present (tx index on nodes with explorer routes — mounted by `cmd/node` via `RegisterExplorerRoutes`).
2. Opt-in fallback: **nonce advancement** — wait until account nonce `>` the nonce used when signing. It can race with other txs from the same account, so clients MUST NOT use it by default when a hash is available.
3. `/ws` is mounted as an SSE/NDJSON stream (not a browser WebSocket). Clients MAY subscribe via `Connection.subscribe()` / `subscribe()` in `@splendor/sqc-sdk`.
4. `waitForConfirmation` (or equivalent) SHOULD be used after transfers when UX requires inclusion certainty. When a submit `hash` is available, `@splendor/sqc-sdk` send paths (`sendTransfer`, `callContract`, `deployContract`, `sendModuleCall`, `sendTransaction`) **require a successful receipt** and do not accept a nonce advance. Pass `allowNonceConfirmation: true` to opt back into the nonce fallback on nodes without a receipt index.

Config field `PROTOCOL.confirmation === 'nonce'` names the **opt-in fallback** mechanism for older/minimal RPC mounts. Runtime clients SHOULD treat confirmation as **receipt-required**, with the nonce check only on explicit opt-in. Caveat string: `PROTOCOL.confirmationCaveat` (`receipt-required;nonce-opt-in-race`).

---

## 8. Connection events

Clients using `@splendor/sqc-sdk` MUST use these event names and payloads (`WalletEventMap` / `BridgeEventMap`):

| Event | Payload |
|-------|---------|
| `connect` | `{ address: string /* SQ: */, evmAddress?: string }` |
| `disconnect` | `{}` |
| `lock` | `{}` |
| `accountsChanged` | `{ address: string, evmAddress?: string }` |
| `chainChanged` | `{ chainId: number, networkId: string }` |
| `error` | `{ code: string, message: string }` |

1. After extension `accountsChanged`, wallets SHOULD refresh the connected `SQ:` account (SDK / ConnectKit default). Empty accounts SHOULD lock and emit `disconnect`.
2. `lock()` MUST clear the ML-DSA-65 key from WASM memory (seed sessions) and emit a `lock` event. `disconnect()` MUST destroy the session and emit `disconnect`.
3. Soft network switches (ConnectKit) SHOULD emit `chainChanged` via `wallet.notifyChainChanged`.

### 8.1 Injected SQC provider (`window.splendor`)

dApps MAY install an in-page provider with `installSplendorProvider({ wallet })` or ConnectKit `exposeInjectedProvider`. Methods use the **`splendor_*`** prefix (NOT Ethereum `eth_*`):

| Method | Purpose |
|--------|---------|
| `splendor_connect` / `splendor_disconnect` / `splendor_accounts` | Session |
| `splendor_signMessage` / `splendor_signTransaction` / `splendor_sendTransaction` | Signing |
| `splendor_chainId` / `splendor_switchNetwork` | Network |

Unstructured `signMessage` payloads MUST be domain-separated with `PROTOCOL.signMessageDomain` (`SPLENDOR-QC-MSG-v1`) unless the message already starts with a known protocol prefix (e.g. Splendor Connect v2) or `raw: true` is set.

WalletConnect v2 and Ethereum EIP-1193/`eth_*` for wagmi remain **out of scope**.

---

## 9. Errors

Clients MUST handle these `WalletError` / `BridgeError` codes (string `code` field):

`NOT_CONNECTED`, `USER_REJECTED`, `NO_PROVIDER`, `BUSY`, `RPC`, `RPC_UNAVAILABLE`, `NETWORK`, `WASM`, `INVALID_ADDRESS`, `INVALID_AMOUNT`, `INVALID_FEE`, `INVALID_PARAMS`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `TX_FAILED`, `CHAIN_MISMATCH`, `UNKNOWN`.

Meaning, user hint, and retry guidance for each code: [ERRORS.md](./ERRORS.md).

`RPC_UNAVAILABLE` MUST be used when all configured RPC endpoints fail (failover exhausted) or when confirmation polling fails closed on repeated transport errors. Covered by `security-rpc.test.ts`.

Gateway HTTP errors SHOULD return JSON `{ "code": "<CODE>", "message": "..." }` using the same code strings when applicable.

---

## 10. RPC client behaviour

1. Clients MAY supply `rpcUrls: string[]` for read failover. Empty lists MUST be rejected.
2. On timeout / HTTP 5xx, clients SHOULD retry (per-URL) then fail over to the next URL.
3. After a successful submit, clients SHOULD sticky-route subsequent submits to the accepting URL.
4. After **3 consecutive sticky-URL failures** (failover away from sticky), clients SHOULD clear sticky and resume from the first configured URL (`security-rpc.test.ts`).
5. Requests SHOULD send header `X-SQC-Bridge: <bridge semver>` (e.g. `3.0.0`, the `@splendor/sqc-sdk` package version).

---

## 11. Gateway

| Mode | Use |
|------|-----|
| `proxy` (default) | End-user / public — relays **pre-signed** `signedTx` only |
| `service-wallet` | Operator automation only — holds one PQ key |

1. Public gateways MUST run `proxy` mode for end users.
2. Public proxy MUST reject `seed`, `evmPrivateKey`, and `evmSignature`. Prefer client-side WASM signing + `POST /v1/tx/submit`. Custodial EVM unlock / derive endpoints are removed for public hosts.
3. Posting an ML-DSA-65 `seed` to a gateway is **custodial** for that request. `ALLOW_CUSTODIAL_PROXY=1` is for private backends only (seed / signedTx automation — not end-user EVM derive).
4. `service-wallet` MUST NOT be used to hold end-user keys. Production service-wallet SHOULD require `GATEWAY_API_KEY`.
5. Gateways SHOULD enable rate limiting, CORS allowlists, and body size limits.

---

## 12. Security threat model (normative guidance)

| Threat | Mitigation |
|--------|------------|
| Confusing `0x` with `SQ:` | UI MUST display `SQ:` after connect; never send EVM ETH as “funding” |
| Wrong chain | MUST `assertChainId` before submit (all tx types) |
| Seed theft via gateway | Public proxy MUST reject custodial credentials; submit `signedTx` only |
| Session residue | MUST `lock` / `disconnect` on logout (`lock` zeroizes ML-DSA-65 key material in WASM sessions) |
| Fake confirmation | MUST NOT treat a missing receipt as success; a nonce advance counts only on explicit opt-in, because it can race with other txs |
| Extension account race | MUST fail-closed (lock + disconnect) if session refresh fails after `accountsChanged` |
| Tampered WASM | Browser SHOULD set `wasmSha256` network pin and fail closed on mismatch |
| All RPCs down | MUST surface `RPC_UNAVAILABLE` (not an infinite hang) |

Encrypted browser session cache of ML-DSA-65 seeds is **out of scope** for SQDP-1 (default remains re-unlock each session for seed connectors).

Wire `chain_id` remains `splendor-quantum-1` for all deployments until the node defines otherwise. Client configs expose distinct `networkId` values (`splendor-local` / `splendor-testnet` / `splendor-mainnet`) for UX and failover selection.

---

## 13. Provider vs wallet

1. Read-only access SHOULD use `Connection` / `SqcProvider` (no WASM) until the user connects.
2. Signing MUST use `Wallet` / `SplendorPQBridge` / `ExtensionSigner` after connect.

---

## Appendix A — Historical EVM → PQ derivation (informative)

> **Not normative for ConnectKit or new dApps.** Low-level `derive.ts` helpers remain exported as **deprecated** offline tooling only. Keys produced this way are **not** post-quantum secure: the ML-DSA seed is a deterministic function of an ECDSA signature.

Historical clients that mapped an EVM account to a PQ wallet used this UTF-8 domain message (version `v1`):

```
Splendor Quantum Chain
Key derivation v1
chain: splendor-quantum-1
```

1. Authenticate via EIP-191 `personal_sign` of that message (or local secp256k1).
2. ML-DSA seed = `SHA3-256(signature_bytes)` (65-byte Ethereum-style signature).
3. Keypair = Cloudflare circl **ML-DSA-65** `mldsa65.NewKeyFromSeed(seed)`.
4. Changing the domain message remaps addresses (breaking).

Optional historical path: EIP-712 `SplendorPQDerive` typed data (`derivation: 'eip712-v1'`). Seed rule remains `SHA3-256(signature_bytes)`.

Exports (deprecated): `DERIVATION_DOMAIN_MESSAGE`, `seedFromEvmSignature`, related recover helpers. See `security-derive.test.ts` for vectors only.

---

## Revision history

| Version | Notes |
|---------|--------|
| SQDP-1 | Initial normative protocol aligned with `@splendor/sqc-sdk` 2.x / legacy `@splendor/pq-bridge` |
| SQDP-1 (hardening) | Normative unlock = extension + native seed; ECDSA derive → Appendix A; `RPC_UNAVAILABLE` |
| SQDP-1 (production) | `BUSY` / `INVALID_PARAMS` listed; receipt-only confirmation by default on SDK send paths; error catalog |
