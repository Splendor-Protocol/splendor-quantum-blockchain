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
settled X402 payment. The instruction must name `from`, `to` and `amount`, and
all three (plus the chain and the payment nonce in `payload`) must match the
on-chain settlement record — otherwise the proof would degrade to "this payer
consumed some nonce" and would accept a payment to an unrelated payee. All
three fields are inside the signed v2 digest, so the payer's signature covers
what the proof asserts. See [`x402.md`](x402.md). The same `instruction_id`
cannot settle twice.

## `verify_identity_v1`: what changes, and the migration

`verify_identity_v1` binds operator identity to an SLH-DSA key stored in the
instance record, ties attestations to the instance and the attestor role, and
enforces the x402 settlement linkage. It is **unscheduled on every live tier**.

### Operator identities are recorded from the module's activation

`operator_identities` (on `deploy`) and `slh_pubkey` (on `set_operator`) are
stored inside the instance record from the moment `verify_v1` is active — not
from the `verify_identity_v1` height. That is what every build the network has
run does, and it has to stay that way: the instance record is a state leaf, so a
node that withheld the identities before the height would compute a different
state root for the same `deploy` than the rest of the fleet, would split a
mixed-version validator set at the first transaction carrying the field, and
would reject its own history on replay. A malformed `slh_pubkey` is rejected
before the height for the same reason — accepting one would pass a transaction
the fleet fails, which diverges the receipt status as well as the state.

Before the height the recorded key is **inert**: `attest` neither looks it up nor
compares against it. It is a registry entry and nothing more.
`verify_identity_v1` is what makes the registry mandatory and binding.

The two entry points check a supplied key at slightly different strengths, and
both are consensus behaviour rather than an inconsistency to tidy up:

| | `deploy` (`operator_identities`) | `set_operator` (`slh_pubkey`) |
| --- | --- | --- |
| Before the height | decoded **and** checked as a well-formed SLH-DSA key | decoded only |
| At/after the height | same, plus required, and must cover every operator | same, plus required **and** checked as a well-formed key |

### Migration for existing instances

Because identities are recorded from the module's activation, **they can and
should be pre-staged**. An instance owner registers each operator's SLH-DSA key
at any height before the fork, and the fork then activates onto a registry that
is already populated. There is no pre-activation gap and no separate migration
mechanism to invent — registration *is* the migration, and it is available now.

What an instance must not do is arrive at the height with no keys recorded. Such
an instance has `operators` and no identities, and until its owner registers
them:

| Action | Before the height | At/after the height, no keys recorded |
| --- | --- | --- |
| `attest` | works; a recorded key is not consulted | **fails** — `operator has no registered SLH-DSA key` |
| `deploy` | `operators` alone is fine | requires `operator_identities` with `slh_pubkey` |
| `set_operator` (active) | records `slh_pubkey` when supplied | requires `slh_pubkey`, and it must be a well-formed key |
| `spend` with `x402` | no-op success | enforced linkage (see below) |
| `finalize`, `spend` (native / QRC-20 / finalize_only) | unchanged | unchanged |

Registration is one owner-signed `set_operator` per operator, and it can be
submitted **today**, long before any height is published:

```json
{"module":"verify","action":"set_operator","params":{
  "verify":"SQ:…","operator":"SQ:…","slh_pubkey":"<hex or base64>","active":true}}
```

So the operational sequence is:

1. Have every instance owner register their operators' SLH-DSA keys now. The
   calls are accepted on the live tiers and change nothing about how the
   instance behaves until the height.
2. Publish the activation height, with enough lead time to chase the owners who
   have not registered. An instance that registered in advance crosses the
   height with no interruption at all.
3. An owner who waits submits the same calls at or after the height, and that
   instance cannot collect attestations between the height and the last of them.
   That window is avoidable, which is the point of step 1.
4. Instances that never register keep working for `register`, `finalize` and
   non-x402 `spend`, and stop being able to collect new attestations.

### x402 instructions registered under the old rules

Under the fork the `x402` settlement action stops being a no-op and becomes
proof-only linkage: the instruction must name `from`, `to` and `amount`, and all
of them — plus the chain and the payment nonce in `payload` — must match the
on-chain settlement record for that nonce.

Two things follow for already-registered instructions:

- An instruction with no `to` / `amount` cannot spend after the height. The
  fields are inside the signed digest, so they cannot be added to an existing
  instruction: register a new one, with a new `instruction_id`.
- The settlement record only exists for payments settled at or after the
  `x402_settle_v1` height. `Schedule.Validate` requires `x402_settle_v1` no
  later than `verify_identity_v1` for that reason; an instruction pointing at a
  payment settled before it cannot be proved here. See [`x402.md`](x402.md).

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
32 blocks). See [`validators.md`](validators.md).

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

