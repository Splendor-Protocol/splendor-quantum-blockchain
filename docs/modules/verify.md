# SQVL: native verify + settlement (`verify_v1`)

SQVL is **not a sidecar**. Instructions, dual-sig checks, attestations, spend /
nullifier, and proofs live in `sqc-node` the same way QRC-20 and the bridge do.
`POST /verify/*` is the same HTTP door as `POST /tx/submit`. If the node is
down, verification is down.

Every write is a `TxCall` to `vm.ModuleRouterAddress` carrying

```json
{"module":"verify","action":"…","params":{…}}
```

gated by `verify_v1` (which requires `module_revert_v1` no later than itself).

## Dual signatures

SQVL instructions and operator attestations require **both**:

- ML-DSA-65 under context `SPLENDOR-QC-VERIFY-v1`
- SLH-DSA-SHA2-128s under the same context

Ordinary transactions, blocks, and X402 stay ML-DSA-65 only. P2P stays
ML-KEM-768. DEX is not a routed native module and is not a live ML-DSA
surface.

`origin` and `settlement_hint` are free-form (`bitcoin`, `eip155:1`,
`bank:swift`, `splendor-qc`, `external:…`). Settlement on Splendor QC uses
`settlement_action`: `native_transfer` | `qrc20_transfer` | `finalize_only` |
`x402`. `native_transfer` and `qrc20_transfer` move funds. `finalize_only`
is proof-only. `x402` is not a second debit: before `verify_identity_v1` it
is a no-op success; under that fork it is proof-only linkage to an already
consumed X402 payment nonce. The same `instruction_id` cannot settle twice.

## Product API (existing quantum RPC)

| Method | Path | What it does |
| --- | --- | --- |
| POST | `/verify/check` | Read-only dual-sig check. No state. |
| POST | `/verify/attest` | Admits a signed `verify.attest` TxCall (X402-style). |
| POST | `/verify/settle` | Admits a signed `verify.spend` or `verify.finalize` TxCall. |
| GET | `/verify/proof/{id}?instance=` | On-chain proof. No webhook. |

Foreign chains and banks **call this RPC**. They do not run inside consensus.
They can settle here (`settlement_hint=splendor-qc`) or take the proof and
settle on Bitcoin / Ethereum / Solana / their books.

## Module actions

**Writes:** `deploy`, `register`, `attest`, `finalize`, `spend`,
`set_operator`, `set_threshold`, `pause`, `unpause`

**Reads:** `proof`, `instance`

Typical flow: deploy a verify instance → register a dual-signed instruction →
operators attest until threshold → `spend` (native / QRC-20 / finalize_only)
or take `GET /verify/proof/{id}` elsewhere. Replaying the same id fails.

## SDK

```ts
import { SqRpc, getVerify, getValidators } from '@splendor/sqc-sdk';

const rpc = new SqRpc({ rpcUrl: 'https://quantum-rpc.splendor.org' });
const verify = getVerify(rpc);
await verify.check(instruction);
await verify.proof(instructionId, instance);
```

Validator register is the `validators` module (2,500 SPLD bond, returned after
32 blocks). See [`validators.md`](validators.md) and `GET /validators`.

## What SQVL is (and is not)

SQVL is Splendor’s **settlement / verification layer**: a native module that
records a dual-signed instruction, collects operator attestations, then either
moves value on Splendor QC or emits a proof other books can accept.

It is **not** a sidecar, a KYC product, a bank, or a silent-ledger. There is
no ClearNode. NFC cards in this stack store an encrypted backup; they do not
sign SQVL instructions.

## Settlement rules (what cannot be manipulated)

- Dual signatures are checked before register and on every attestation.
- Only listed operators may attest; the same operator cannot attest twice.
- `spend` / settle requires threshold attestations (or a prior `finalize`).
- The same `instruction_id` cannot settle twice (`Spent` is sticky).
- On-chain value move (`native_transfer`, `qrc20_transfer`) only moves
  **existing** funds from `from`, and the TxCall **caller must be `from`**.
  SQVL cannot mint SPLD and cannot pull someone else’s balance.
- A hint that is not `splendor-qc` / `splendor*` cannot move value here
  unless the action is `finalize_only` (proof-only).
- Owner-only: `set_operator`, `set_threshold`, `pause` / `unpause`.

## Typical flows

**Settle on Splendor QC**

1. App / bank builds a `VerifyInstruction` (from, to, amount, hint
   `splendor-qc`, action `native_transfer` or `qrc20_transfer`).
2. Payer signs it with ML-DSA-65 **and** SLH-DSA (WASM: `sqSignVerifyInstruction`).
3. `register` → operators `attest` to threshold → `spend`.
4. Native or QRC-20 transfer executes in the same receipt. Replay fails.

**Prove here, settle elsewhere**

1. Same dual-signed instruction, `settlement_action=finalize_only`,
   hint `bitcoin` / `eip155:1` / `bank:swift` / …
2. Attest + finalize (or spend with finalize_only).
3. `GET /verify/proof/{id}` is the committed record. The other chain or
   bank is an integration that *calls* this RPC. It does not run inside
   consensus.

Example app: [`examples/sqvl-external-app.mjs`](../../examples/sqvl-external-app.mjs).
SDK: `sdk/sqc-sdk/src/verify.ts`.

