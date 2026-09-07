# Verify: the SQVL settlement layer (`verify_v1`)

> The narrative — why verification is separate from consensus, the flow in
> plain language, how a bank or a bridge calls it, and how it relates to
> QuFi — is [`../SQVL_VERIFICATION_LAYER.md`](../SQVL_VERIFICATION_LAYER.md).
> This file is the reference: every field, every rule, every door.

SQVL is Splendor's **verification / settlement layer**: a native module that
records an instruction signed with two post-quantum schemes, collects operator
attestations, and then either moves value on this chain or leaves a record
other books can accept. It is **not a sidecar**: instructions, dual-signature
checks, attestations, the spend nullifier and the record all live in
`sqc-node` the same way QRC-20, the exchange and the bridge do. `POST
/verify/*` is the same HTTP door as `POST /tx/submit`. If the node is down,
verification is down.

Every write is a `TxCall` to `vm.ModuleRouterAddress` carrying

```json
{"module":"verify","action":"…","params":{…}}
```

gated by `verify_v1`, which requires `module_revert_v1` no later than itself
(a failed action must be a failed receipt — fee kept, nonce consumed — or the
caller's account jams at that nonce).

## Dual signatures

An SQVL **instruction** and every operator **attestation** carry two
signatures, both under the context `SPLENDOR-QC-VERIFY-v1`, and both must
verify:

| Scheme | Standard | Public key | Signature |
|---|---|---|---|
| ML-DSA-65 | FIPS 204 | 1,952 bytes | 3,309 bytes |
| SLH-DSA-SHA2-128s | FIPS 205 | 32 bytes | 7,856 bytes |

Two schemes on two different hardness assumptions (lattices, hash functions):
breaking one leaves the other standing. Both signatures are deterministic.
Ordinary transactions, blocks, X402 and the DEX stay ML-DSA-65 only; P2P
stays ML-KEM-768.

What two schemes do **not** give you: two-person control. A wallet derives
both keys from one seed (below), so a stolen seed yields both signatures.
Separation of duty is the instance threshold across distinct operators, not
the pair of schemes.

The SLH-DSA "s" (small) parameter set is frozen into the wire format. It
signs slowly — a few hundred milliseconds natively, seconds in the browser
WASM signer — and verifies cheaply, which is the right way round for a chain.
The "f" set would sign ten times faster at 17,088 bytes per signature; that
would change every stored instruction, so it is a decision for before the
first instance is deployed on a real tier, not after.

A wallet's SLH-DSA key is derived from the same 32-byte seed as its ML-DSA
key, under its own domain (`SHAKE256("SPLENDOR-QC-SLH-DSA-KEYGEN-v1" ‖
seed)`, `crypto.SLHKeypairFromSeed`), so one seed yields two unrelated,
reproducible identities and there is no second secret to back up. The SDK
derives the SLH-DSA half lazily on first SQVL use.

## The instruction

`core/types.VerifyInstruction`. On the wire:

| field | type | notes |
|---|---|---|
| `version` | int | must be `1` |
| `instruction_id` | **64 hex chars** | the settlement nullifier; unique per instance; never all-zero; `0x` accepted on input, never produced; a JSON array is refused |
| `purpose` | string, 1–64 B | free-form (`payment`, `kyc-attest`, …) |
| `origin` | string, 1–128 B | free-form (`bitcoin`, `eip155:1`, `bank:swift`, …) |
| `payload` | base64 ≤ 4,096 B | opaque; under the signatures |
| `settlement_hint` | string, 1–128 B | `splendor-qc` to settle here; anything else means "prove here, settle elsewhere" |
| `settlement_action` | string ≤ 64 B | `finalize_only` (default when empty), `native_transfer`, `qrc20_transfer` |
| `from`, `to`, `amount`, `token` | strings | settlement parameters for the two transfer actions; `amount` is a strict decimal string in the smallest unit |
| `mldsa_pubkey`, `sig_mldsa`, `slh_pubkey`, `sig_slh` | base64 | filled by the signer |

The digest both keys sign is

```
SHA3-256("sqvl:instruction:v1:" ‖ u16be(len(chain_id)) ‖ chain_id
         ‖ instruction_id[32] ‖ u16be(len(purpose)) ‖ purpose
         ‖ u16be(len(origin)) ‖ origin ‖ u16be(len(settlement_hint)) ‖ settlement_hint
         ‖ u16be(len(settlement_action)) ‖ settlement_action
         ‖ u16be(len(payload)) ‖ payload)
```

The chain id is inside, so an instruction signed for one chain is invalid on
every other. `from` / `to` / `amount` / `token` are deliberately **not**
inside: they are settlement parameters the module checks at `spend`, and a
proof-only instruction has none. What protects the payer is that a
value-moving `spend` is accepted only from `from` itself, signed with its
transaction key — the module never moves funds on the strength of the
instruction alone. Two consequences for integrators:

- anything that must sit under the two post-quantum signatures goes in
  `payload` (the canonical bank message, the source-chain transaction hash,
  the invoice);
- `from` should read the record (`proof`) and compare it with what it signed
  before sending `spend`, since an attestor of the instance may `register`
  the instruction too.

The **signer** of an instruction is the address of its ML-DSA key — on this
chain a key *is* an address — and the module records it.

Derive `instruction_id` from the thing being settled (`SHA3-256` of the SWIFT
UETR, of the source-chain tx hash and log index, …) rather than at random: a
retry then hits "already registered" instead of settling the same thing under
a fresh id.

### `x402` is refused

`settlement_action: "x402"` names the HTTP payment flow, which lives behind
`POST /x402/settle` and admits its own payer-signed transaction. It is not
something the verify module can perform, and a `spend` that marked an
instruction settled while moving nothing would be a false record. So it is
refused at `register`, with a message that says to use `finalize_only` and
settle the payment through the x402 door. Unknown actions are refused the
same way, and a transfer action whose `from` / `to` / `amount` / `token` do
not parse (or whose `to` is the zero address, or whose `amount` is not
positive) is refused before it is recorded.

## Instances

`verify.deploy` creates a **policy container**: an owner, up to
`MaxVerifyOperators = 16` operators, and a `threshold`. The owner is always an
attestor too, so `1 ≤ threshold ≤ 1 + len(operators)` — a threshold nobody can
reach is refused at deploy, at `set_threshold`, and by `set_operator` when
revoking would leave it unreachable ("lower the threshold first"). Operators
must be distinct, non-zero, and not the owner. The owner is fixed at deploy:
this module has no ownership-transfer action.

The instance lands at `SHA3-256(deployer ‖ ascii(decimal(nonce)) ‖
u64be(height) ‖ "verify")` — the height is folded in, like the bridge and the
exchange, so instances on different tiers can never share an address. The SDK
derives it the same way (`verifyAddress`) and predicts it before sending; a
deploy that lands a block later than predicted gets a different address, and
the receipt's `contract_address` is the source of truth once confirmed
(`deployVerify` reports `confirmed`).

## Attestations

An operator's attestation is a dual signature over

```
SHA3-256("sqvl:attest:v1:" ‖ u16be(len(chain_id)) ‖ chain_id ‖ instance[32] ‖ digest[32])
```

where `digest` is the instruction digest above. **The instance is inside**:
an attestation given to one instance cannot be replayed to reach threshold on
another that happens to list the same operator and the same instruction. The
attestation carries its own keys, so anyone may relay it; what it must be is
from an attestor of *this* instance who has not attested *this* instruction
yet.

Attestation fields (`pubkey`, `slh_pubkey`, `sig_mldsa`, `sig_slh`) are
accepted as hex (optionally `0x`-prefixed) or standard base64. The decoder
settles which by the exact byte length the field must have — hex of *n* bytes
is exactly 2*n* characters, base64 is about 4/3 *n* — so a value is never
decoded the wrong way. The SDK's `signVerifyAttestation` emits hex.

The record keeps the **attestor's address**, not the attestation signatures:
they are verified at inclusion by every node and then discarded. A consumer
that needs them collects them from `VerifyAttested` events.

## Write actions

| action | who | params |
|---|---|---|
| `deploy` | anyone (deployer = owner) | `threshold` (≥ 1), `operators` (`SQ:` list, ≤ 16, distinct, no zero, not the owner) |
| `register` | the instruction's signer, or an attestor | `verify`, `instruction` |
| `attest` | anyone relaying an attestor's signatures | `verify`, `instruction_id`, `attestation` |
| `finalize` | anyone, once threshold is met | `verify`, `instruction_id` |
| `spend` | anyone for `finalize_only`; **`from` itself** for a value move | `verify`, `instruction_id` |
| `set_operator` | owner | `verify`, `operator`, `active` (required bool) |
| `set_threshold` | owner | `verify`, `threshold` |
| `pause` / `unpause` | owner | `verify` |

Every action runs under one StateDB snapshot: a `spend` whose transfer fails
leaves the record exactly as it was (and no events on the receipt). The fork
gate and an unknown action are HARD; every other failure is SOFT. The RPC
preflights the gate at head+1, so before the height the doors answer `400
rejected` instead of admitting a transaction that can never apply.

### Settlement rules (what cannot be manipulated)

- Both signatures are checked at `register`, and both again on every
  attestation. One valid signature is not an SQVL signature.
- Only the owner or a listed operator may attest; the same attestor cannot
  attest twice; attestations are bound to the instance and the chain.
- `finalize` and `spend` need `threshold` attestations (`spend` also accepts
  a prior `finalize`).
- The same `instruction_id` cannot register twice under an instance and
  cannot settle twice: `spent` is sticky, and a spent or finalized
  instruction takes no further attestations. `spend` also sets `finalized`
  and records the height.
- A value move (`native_transfer`, `qrc20_transfer`) only moves **existing**
  funds out of `from`, and the caller of `spend` **must be `from`**. SQVL
  cannot mint SPLD and cannot pull someone else's balance.
- A value move needs a settlement hint that begins with `splendor`. Any
  other hint may only `finalize_only` here (the "prove here, settle
  elsewhere" flow).
- A **paused** instance refuses `register`, `attest`, `finalize` and `spend`
  until unpaused; the owner-only configuration actions stay available so the
  owner can repair and resume.

### Events

Emitted on the receipt, from the instance address, SHA3-256 topics like every
other module:

| event | indexed | data |
|---|---|---|
| `VerifyDeployed(sqaddress,sqaddress,uint256)` | instance | owner, threshold |
| `VerifyRegistered(sqaddress,bytes32,sqaddress)` | instance, instruction_id | signer |
| `VerifyAttested(sqaddress,bytes32,sqaddress,uint256)` | instance, instruction_id | attestor, attestation count |
| `VerifyFinalized(sqaddress,bytes32)` | instance, instruction_id | — |
| `VerifySpent(sqaddress,bytes32,string)` | instance, instruction_id | settlement action |

### Sizes and fees

| object | size | notes |
|---|---|---|
| `register` (two keys, two signatures) | ≈ 17.5 KB base64 | plus the payload, ≤ 4 KiB |
| `attest` | ≈ 17.5 KB base64, ≈ 26 KB hex | the SDK signer emits hex |
| tx data cap | 128 KiB | `mempool.MaxTxDataBytes` |
| `POST /verify/check` body | 256 KiB | |
| `POST /verify/attest`, `/verify/settle` body | 512 KiB | |

Both sit well under the transaction data cap, but they are the largest
routine transactions on the chain, and a block's gas budget counts them by
size. Price accordingly; the per-instance operator cap bounds the worst case
at seventeen attestations per instruction.

## Reads

`POST /call` against the module router, or the doors below. The `/call`
body is `{"to": <vm.ModuleRouterAddress>, "data": hex(module-call JSON),
"encoding": "hex"}` and the answer carries the result under `decoded`
(`reverted` / `revert_reason` on failure).

| action | params | returns |
|---|---|---|
| `proof` | `verify`, `instruction_id` | `instance`, `instruction_id`, `digest`, `signer`, `instruction` (with keys and signatures), `settlement_action`, `attestors[]`, `attestations`, `threshold`, `threshold_met`, `finalized`, `spent`, `registered_height`, `height` (0 until finalized or spent) |
| `instance` | `verify` | `address`, `owner`, `threshold`, `operators[]`, `attestors` (count: owner + operators), `paused`, `height` |

What a reader can verify offline from a `proof`: the instruction's two
signatures, from the keys and signatures in the record and the chain id. What
it takes from the node: the attestor list, counts, flags and heights. There
is no state inclusion proof yet; the record is a node-served state read.

## Product API (the `/verify/*` doors)

| Method | Path | Plane | What it does |
| --- | --- | --- | --- |
| POST | `/verify/check` | metered | Stateless dual-signature check of an instruction. `200 {valid:true, chain_id, digest, signer, instruction_id, purpose, origin, settlement_hint, settlement_action}`, or `200 {valid:false, reason}` (bad signature, bad field, wrong version). `400` only for malformed JSON or a body above 256 KiB. No state. |
| POST | `/verify/attest` | metered | Admits a signed `verify.attest` TxCall (bare or `{"tx": …}`), after checking it is one — anything else is `400` before admission. Then the same path as `/tx/submit`: `202 {status:"accepted", hash}` or `400 rejected: …`. |
| POST | `/verify/settle` | metered | Admits a signed `verify.spend` or `verify.finalize` TxCall the same way. |
| GET | `/verify/proof/{id}?instance=SQ:…` | read | The `proof` read by URL. `verify=` is an alias for `instance=`. `400` for a bad id or a missing instance; `404` when the instruction is not registered under that instance. |

Foreign chains and banks **call this RPC**. They do not run inside
consensus. They can settle here (`settlement_hint: "splendor-qc"`) or take
the record and settle on Bitcoin, Ethereum, Solana or their own books.

## SDK

```ts
import { createSession } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed });

// 1. an instance (owner = this wallet, one operator, threshold 1)
const { address: instance } = await s.deployVerify({ threshold: 1, operators: [op] });

// 2. the payer dual-signs an instruction (ML-DSA-65 + SLH-DSA, in WASM)
const { instruction, digest_hex } = await s.wallet.signVerifyInstruction({
  instruction_id: idHex, purpose: 'payment', origin: 'bank:swift',
  settlement_hint: 'splendor-qc', settlement_action: 'native_transfer',
  from: s.address(), to, amount,
});
await s.verify(instance).register(instruction);

// 3. an operator attests (bound to this instance), anyone relays it
const att = await operatorWallet.signVerifyAttestation({ verify: instance, digest_hex });
await s.verify(instance).attest(instruction.instruction_id, att);

// 4. settle once; the record is readable forever
await s.verify(instance).spend(instruction.instruction_id);
await s.verify(instance).proof(instruction.instruction_id);
```

`SqRpc.verifyCheck / verifyProof / verifyAttest / verifySettle` are the doors.
The wallet's SLH-DSA public key is `wallet.getSLHPublicKeyHex()`. The signer
WASM exports `sqGetSLHPublicKeyHex`, `sqSignVerifyInstruction` and
`sqSignVerifyAttestation`; `sdk/sqc-sdk/testdata/golden-vectors.json`
(`verify`) pins the digests for every other implementation. Signing
SLH-DSA-128s takes seconds in the browser; keep it off the UI thread's
critical path.

## What SQVL is not

It is not a sidecar, a KYC product, a bank, or a silent ledger. There is no
ClearNode. NFC cards in this stack store an encrypted backup; they do not
sign SQVL instructions. It is not a node network: attestors are whoever an
instance owner lists, with no staking, slashing or admission of their own.
Its record is not a self-contained proof (no inclusion proof, no stored
attestation signatures). And it says nothing about **who produces blocks**:
validator admission and bonding are the `staking` module under
`validator_gates_v1` — see [`staking.md`](staking.md) — and a record is
exactly as final as the block that holds it, which on this branch is Phase 1
single-authority finality.

## Typical flows

**Settle on Splendor QC** — build the instruction with `from`, `to`,
`amount`, hint `splendor-qc`, action `native_transfer` or `qrc20_transfer`;
the payer dual-signs it; `register` → operators `attest` to threshold →
`spend` by `from`. The transfer executes in the same receipt. Replay fails.

**Prove here, settle elsewhere** — the same dual-signed instruction with
`finalize_only` and a hint naming the other book (`bitcoin`, `eip155:1`,
`bank:swift`, …); attest, then `finalize` (or `spend`, which for
`finalize_only` moves nothing). `GET /verify/proof/{id}` is the committed
record the other chain or bank reads. It is an integration that *calls*
this RPC; it does not run inside consensus, and no adapter for the other
side ships in this repository.

## Enabling it

```json
"forks": { "module_revert_v1": 1, "verify_v1": 1 }
```

On an existing chain, add `verify_v1` above the current head and restart
every node with `--allow-fork-extension` and a re-pinned `--genesis-doc-hash`
(see `DEPLOY.md`, "Fork Schedule"). Before the height nothing here is
reachable; a `verify` call is a HARD "fork not active" refusal.

**Status.** `genesis-production.json` schedules `module_revert_v1`,
`qrc20_v2`, `qrc1155_v1` and `exchange_v1` only; no deployed tier has
activated `verify_v1`. It has run on local development chains (the tests
below and `cmd/featsmoke`); the AWS throwaway-chain run
(`docs/AWS_FEAT_VALIDATOR_TEST_PLAN.md`) is written and not launched.

## Tests

- `core/crypto/slhdsa_test.go` — sign/verify and domain separation, key
  round-trip, seeded derivation is deterministic and domain-separated.
- `core/types/verify_test.go` — dual signature and domain separation, stable
  digest, hex `instruction_id` on the wire (array refused), attestation digest
  binds the instance.
- `vm/verify_test.go` — the full flow through the router, native transfer
  settles from the caller, a failed spend is atomic, register requires the
  signer or an attestor, attestation bound to the instance, encodings are
  length-checked, pause stops every advance, settlements the module cannot
  perform are refused, threshold bounded by the attestors, fork gate.
- `rpc/sqvl_rpc_test.go` — `/verify/check` dual signature, array id refused,
  `/verify/proof` validates its inputs, the doors check the action before
  admitting.
- `sdk/sqc-sdk/signer/verify_golden_test.go`, `wasm-verify.test.ts`,
  `verify-module.test.ts` — golden vectors pinned in Go and against the WASM
  build; the SDK module client.
- `cmd/featsmoke` — end to end against a live node: dual-sign →
  `/verify/check` → deploy → register → attest → proof `threshold_met` →
  spend → proof `spent` → replayed spend is `already spent` →
  `GET /verify/proof/{id}` serves the record.

```bash
go test ./core/crypto ./core/types ./vm ./rpc -run 'SLH|Verify|InstructionID'
go test ./sdk/sqc-sdk/signer -run Golden
cd sdk/sqc-sdk && npm test
```
