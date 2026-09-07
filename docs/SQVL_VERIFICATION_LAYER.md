# SQVL: the Splendor Quantum Verification Layer

**What it is, why it sits beside consensus rather than inside it, and how a
bank or a bridge calls it.**

Branch `feat/validator`, fork `verify_v1`, written against commit `fc945b1`
(2026-09-07). The module reference, with every field and every rule, is
[`modules/verify.md`](modules/verify.md). This document is the narrative:
read it first, then the reference.

---

## In one paragraph

SQVL is the part of Splendor Quantum Chain (SQC) that answers one question:
*did the named parties sign this statement, and has it been acted on exactly
once?* An external party — a bank, a bridge relayer, a payments application —
signs an **instruction** with two post-quantum signature schemes, ML-DSA-65
(FIPS 204) and SLH-DSA (FIPS 205). Operators chosen by whoever deployed the
verification **instance** countersign it, each with the same two schemes.
When enough of them have, the chain records that fact. From there the
instruction either **settles on SQC** in the same transaction that marks it
settled, or stays as a **record** that another system — Bitcoin, an EVM
chain, a bank's own ledger — reads before it moves value. None of it is a
sidecar: the instruction, the signature checks, the attestations, the
once-only spend and the record all live inside `sqc-node`, behind a fork
gate, the way every other SQC module does.

## The problem

A transfer of value is, before anything else, a statement: this party wants
this amount to go there, for this reason. Somebody has to check the statement
before money moves — that it was made by who it says, that the people who
must approve it did, that it has not been presented twice. On most systems
those checks happen off the ledger, in software nobody else can inspect,
under classical signatures that a large enough quantum computer will
eventually forge.

Putting post-quantum signatures on every ledger runs into size. A FIPS 205
hash-based signature is 7,856 bytes; the ledgers that carry the world's value
are not going to add that to every transaction. QuFi's answer, published on
4 September 2026, is to take the verification step out of the ledger entirely
and run it on its own network of nodes, handing the ledger a proof. SQC's
answer starts from a different place: every transaction and every block on
this chain already carries an ML-DSA-65 signature, so the chain is already
post-quantum. What SQVL adds is a *second, independent* signature family and
an *explicit, recorded approval step*, confined to the objects that need them
— the instruction and the attestations — and paid for by the party who
submits them. Ordinary transfers stay exactly as they are.

## Three steps

QuFi describes its model as instruction → verification → settlement. SQVL
has the same three steps; the difference is where each one runs.

| step | what happens | where, on SQC |
|---|---|---|
| **Instruction** | The party making the statement signs it with ML-DSA-65 *and* SLH-DSA. The statement carries a unique id, a purpose, an origin, an opaque payload and a hint about where it should settle. | Signed anywhere: the SDK's WASM signer, the Go library, or any FIPS 204/205 implementation against the published digest. |
| **Verification** | The instruction is registered under an instance. Operators of that instance attest it, each attestation dual-signed and bound to that instance. At `threshold` attestations the record shows `threshold_met`. | Inside the node, as module transactions. Every node re-verifies both signatures on the instruction and on each attestation when it applies the block. |
| **Settlement** | Either the value moves on SQC (`native_transfer`, `qrc20_transfer`) in the same receipt that marks the instruction spent, or the instruction is finalized as a record for another book to act on. | On SQC: atomic with the record. Elsewhere: the other system reads `GET /verify/proof/{id}` and does its own move. |

## Why verification is separate from consensus

SQC has a consensus layer, and it will have a stronger one. SQVL is
deliberately not part of it. Five reasons, each with the code that enforces
it.

**1. It answers a different question.** Consensus decides which block comes
next and who sealed it. On this branch that is one producer, admitted under
`validator_gates_v1` by an admission pass and a bond
([`modules/staking.md`](modules/staking.md)), sealing each block with one
ML-DSA-65 signature. SQVL decides whether one specific statement is genuine
and approved. A validator does not attest instructions by virtue of being a
validator; an attestor gets no say over blocks. The two sets of keys never
meet.

**2. Different signers, different domains.** The parties to an instruction
are external — a payer, a relayer, a compliance desk. They sign with their
own keys under their own FIPS 204/205 context string,
`SPLENDOR-QC-VERIFY-v1`. Every signature on SQC is domain-separated
(`core/crypto/mldsa.go`), so an SQVL signature can never be replayed as a
transaction, a block or a vote, and none of those can be replayed as an
attestation.

**3. The cost lands on the party who causes it.** Two keys and two
signatures are about 18 KB. Charging every transaction for that is precisely
the overhead QuFi cites for staying off-chain. SQVL charges only `register`
and `attest`, paid by whoever sends them; the producer verifies the
signatures once when it builds the block, every node verifies them again when
it applies the block, and the record is stored. Nothing else on the chain
gets larger.

**4. Policy is local, not global.** An instance has its own owner, its own
operators (up to sixteen, plus the owner) and its own threshold. A bank's
approval rules are the bank's. There is no chain-wide attestor set to govern,
and changing an instance's rules is an owner action, not a fork.

**5. The chain's finality bounds SQVL's finality — and SQVL says so.** A
record is exactly as final as the block that holds it. On this branch that is
Phase 1 single-authority finality: one producer, no Byzantine quorum
(`readme.md` §9). SQVL adds no second consensus and claims none. When the
`multi_validator` upgrade lands, records inherit its finality without
changing a byte of the module.

One consequence is worth stating on its own. **Reaching threshold moves no
value.** The only thing that moves SPLD or a QRC-20 through SQVL is the
instruction's `from` sending a `spend` transaction with its own key. So the
worst a compromised operator set can do is produce a false approval record.
It cannot take a balance.

## The flow, end to end

```
 payer / bridge / bank           operators (owner + up to 16)          any relayer          consumer
 ────────────────────            ────────────────────────────          ───────────          ────────
 dual-sign instruction
       │
       ├────── POST /verify/check ──────────────────────────────────────────────────►  valid? digest? signer?
       │                                                                              (no state, no fee)
 verify.register ──────────────────────────────────────────────────────────────────►  VerifyRegistered
                                 dual-sign attestation(chain, instance, digest)
                                           │
                                           └────── verify.attest ×N ───────────────►  VerifyAttested (1..N)
                                                   (POST /verify/attest)              threshold_met
                                                                verify.finalize ────►  VerifyFinalized
 verify.spend (only `from`, for a value move)                   (POST /verify/settle)
       └── SPLD / QRC-20 moves in the same receipt ────────────────────────────────►  VerifySpent
                                                                                             │
                                                 GET /verify/proof/{id}?instance=… ◄─────────┘  read, then act
```

1. **Someone deploys an instance.** `verify.deploy` with a threshold and an
   operator list. The deployer is the owner and always counts as an attestor.
   The instance lands at an address derived from the deployer, its nonce and
   the block height, so no two tiers can ever share one.
2. **The party makes its statement.** It fills in the instruction and signs
   the digest twice, ML-DSA-65 then SLH-DSA. In the SDK that is one call,
   `wallet.signVerifyInstruction`; the SLH-DSA half takes seconds in a
   browser and a few hundred milliseconds natively.
3. **Optionally, it checks before it pays.** `POST /verify/check` verifies
   both signatures and returns the digest and the signer's address. It
   touches no state and costs no fee — a compliance system can gate on it
   before anything is submitted.
4. **It registers.** `verify.register` carries the whole signed instruction.
   The node verifies both signatures again, refuses a settlement it could not
   perform, refuses a duplicate id, and records the instruction with its
   digest and signer. Only the instruction's signer or one of the instance's
   attestors may register; a stranger cannot fill someone's instance with
   fee-paid state.
5. **Operators attest.** Each operator signs, with both schemes, a second
   digest that names the chain, the instance and the instruction digest.
   Anyone may relay that attestation — it carries the operator's keys, so it
   authenticates itself — but the node accepts it only from an attestor of
   *this* instance who has not attested *this* instruction before.
6. **Threshold.** When the attestor count reaches the instance's threshold,
   the record reports `threshold_met`. `finalize` writes that down
   permanently without moving anything.
7. **Settlement.** `spend` settles exactly once. For `finalize_only` it just
   marks the record spent. For a value move it transfers existing funds out
   of `from` — and only `from` may call it. The transfer and the record
   update are one atomic receipt: if the transfer fails, nothing is marked.
8. **The record.** `GET /verify/proof/{id}?instance=…` serves the
   instruction, its digest and signer, the attestor list, the counts and
   flags, and the heights. It is readable for the life of the chain.

## The details

### Two signatures, two assumptions

| scheme | standard | assumption | public key | signature |
|---|---|---|---|---|
| ML-DSA-65 | FIPS 204 | module lattices | 1,952 bytes | 3,309 bytes |
| SLH-DSA-SHA2-128s | FIPS 205 | hash functions | 32 bytes | 7,856 bytes |

Both signatures are over the same digest under the same context,
`SPLENDOR-QC-VERIFY-v1`, and both are deterministic. Both are checked at
`register` and both again on every `attest`. One valid signature is not an
SQVL signature.

The point of two schemes is two unrelated hardness assumptions: a
cryptanalytic break of lattice signatures leaves the hash-based one
standing, and the reverse. It is worth being precise about what that does
*not* buy. Dual signing is not two-person control. A wallet derives both keys
from one seed, so a stolen seed yields both signatures. Separation of duty
comes from the instance — the threshold across distinct operators — not from
the pair of schemes.

Everything else on SQC — transactions, blocks, X402, the DEX, the bridge —
stays ML-DSA-65 only. The P2P transport is a separate matter: an ML-KEM-768
sealed channel, unrelated to SQVL.

### One seed, two keys

A wallet's SLH-DSA key is derived from the same 32-byte seed as its ML-DSA
key, under its own domain: the key material is read from
`SHAKE256("SPLENDOR-QC-SLH-DSA-KEYGEN-v1" ‖ seed)`
(`core/crypto/slhdsa.go`, `SLHKeypairFromSeed`). One seed, two unrelated,
reproducible identities, no second secret to back up. The SDK derives the
SLH-DSA half lazily on first SQVL use, so a wallet that never touches SQVL
never pays for it.

The SLH-DSA parameter set, SHA2-128s, is frozen into the wire format: small
signatures, slow to sign, cheap to verify — the right way round for a chain,
where one signer pays once and every node verifies. SHA2-128f would sign
about ten times faster at 17,088 bytes per signature. Switching changes
every stored instruction, so it is a decision for before the first instance
on a real tier, and it is still open ([`FEAT_VALIDATOR.md`](FEAT_VALIDATOR.md),
"Open decisions").

### The instruction

`core/types.VerifyInstruction`, `version` 1. On the wire:

| field | type | rule |
|---|---|---|
| `instruction_id` | 64 hex chars | the settlement nullifier: unique per instance, never all-zero |
| `purpose` | string, 1–64 bytes | free-form: `payment`, `kyc-attest`, `redeem`, … |
| `origin` | string, 1–128 bytes | free-form: `bitcoin`, `eip155:1`, `bank:swift`, … |
| `payload` | base64, ≤ 4,096 bytes | opaque; under the signatures |
| `settlement_hint` | string, 1–128 bytes | `splendor-qc` to settle here; anything else means prove here, settle elsewhere |
| `settlement_action` | string | `finalize_only` (default), `native_transfer`, `qrc20_transfer` |
| `from`, `to`, `amount`, `token` | strings | settlement parameters for the two transfer actions |
| `mldsa_pubkey`, `sig_mldsa`, `slh_pubkey`, `sig_slh` | base64 | filled by the signer |

The digest both keys sign:

```
SHA3-256( "sqvl:instruction:v1:"
        ‖ u16be(len(chain_id)) ‖ chain_id
        ‖ instruction_id[32]
        ‖ u16be(len(purpose)) ‖ purpose
        ‖ u16be(len(origin)) ‖ origin
        ‖ u16be(len(settlement_hint)) ‖ settlement_hint
        ‖ u16be(len(settlement_action)) ‖ settlement_action
        ‖ u16be(len(payload)) ‖ payload )
```

Three things follow from this formula.

*The chain id is inside.* An instruction signed for `splendor-quantum-1` is
invalid on every other chain, and an instruction signed for a test chain can
never be replayed on a real one.

*The signer is an address.* On SQC an address is `SHA3-256(ML-DSA-65 public
key)`, so the instruction's ML-DSA key *is* an account, and the module
records it as the signer.

*The transfer parameters are outside.* `from`, `to`, `amount` and `token`
are not in the digest. They are checked when `spend` runs, and a
value-moving `spend` is accepted only from `from` itself, signed with its
transaction key. The payer therefore authorises a transfer twice — once by
dual-signing the instruction, once by sending the spend — and the module
never moves funds on the strength of the instruction alone. Two rules for
integrators follow: anything that must sit under the two post-quantum
signatures belongs in `payload` (the bank's canonical message, the
source-chain transaction hash, the invoice), and `from` should read the
record back and compare it with what it signed before sending `spend`,
because an attestor may register the instruction as well.

**Choosing the id.** Derive it from the thing being settled — `SHA3-256` of
the SWIFT UETR, or of the source-chain transaction hash and log index —
rather than generating it at random. A retry then produces the same id and
is refused as already registered, instead of a fresh id that could settle
the same thing twice.

**`x402` is refused.** `settlement_action: "x402"` names the HTTP payment
flow behind `POST /x402/settle`, which admits its own payer-signed
transaction. The verify module cannot perform it, and a `spend` that marked
an instruction settled while moving nothing would be a false record, so it
is refused at `register` with a pointer to `finalize_only` and the x402
door. Unknown actions, and transfer actions whose parameters do not parse,
are refused at the same point: a record never holds an instruction that
could not have settled.

### The attestation

An operator's attestation is a dual signature over

```
SHA3-256( "sqvl:attest:v1:"
        ‖ u16be(len(chain_id)) ‖ chain_id
        ‖ instance[32]
        ‖ digest[32] )
```

where `digest` is the instruction digest above. The instance is inside, so
an attestation given to one instance cannot be replayed to reach threshold
on another that happens to list the same operator and the same instruction.
The chain id is inside for the same reason as before.

On the wire the attestation is four fields — `pubkey` (1,952 bytes),
`slh_pubkey` (32), `sig_mldsa` (3,309), `sig_slh` (7,856) — each as hex or
standard base64. The decoder settles which by the exact byte length the
field must have, so a value is never decoded the wrong way. The node accepts
it only if the ML-DSA key's address is the owner or a listed operator of
this instance, that address has not attested this instruction already, both
signatures verify, and the instruction is neither finalized nor spent.

The record keeps the attestor's *address*, not the signatures: they are
verified at inclusion, by every node, and then discarded. That matters when
you read a proof (below).

### The instance

`verify.deploy` creates a policy container: an owner, up to sixteen
operators, a threshold. The owner is always an attestor, so
`1 ≤ threshold ≤ 1 + len(operators)`; a threshold nobody can reach is refused
at deploy, at `set_threshold`, and by `set_operator` when a revoke would
leave it unreachable. Operators must be distinct, non-zero, and not the
owner. The owner is fixed at deploy — there is no ownership transfer on this
module.

The address is `SHA3-256(deployer ‖ ascii(decimal(nonce)) ‖ u64be(height) ‖
"verify")`, the same shape as the bridge and the exchange. The SDK predicts
it before sending and reads it from the receipt once confirmed; a deploy
that lands a block later than predicted gets a different address, and the
receipt's `contract_address` is the truth.

The owner may `pause` an instance. Paused, it refuses `register`, `attest`,
`finalize` and `spend`; the owner-only configuration actions stay available
so the owner can repair and resume.

### Settlement rules

- Both signatures are checked at `register` and both again on every
  attestation.
- Only the owner or a listed operator may attest; each attestor once;
  attestations are bound to the instance and the chain.
- `finalize` and `spend` need `threshold` attestations. `spend` also accepts
  a prior `finalize`.
- The same `instruction_id` cannot register twice under an instance and
  cannot settle twice: `spent` is sticky, and a spent or finalized
  instruction takes no further attestations.
- A value move only moves **existing** funds out of `from`, and the caller
  of `spend` must be `from`. SQVL cannot mint SPLD and cannot pull anyone
  else's balance.
- A value move needs a settlement hint beginning `splendor`. Any other hint
  may only `finalize_only` here.
- `finalize_only` may be spent by anyone once threshold is met; it moves
  nothing and marks the record.

### Failure semantics

Every action runs under one StateDB snapshot. A `spend` whose transfer fails
leaves the record exactly as it was, and no events on the receipt. The fork
gate and an unknown action are HARD refusals — never included in a block.
Every other failure is SOFT: under `module_revert_v1`, which `verify_v1`
requires no later than itself, a failed action is a failed receipt with the
fee kept and the nonce consumed, so a relayer whose call fails cannot jam
its own account. The RPC preflights the fork at the next height, so before
activation the doors answer `400 rejected` rather than accepting a
transaction that can never apply.

### Events

Emitted on the receipt from the instance address, SHA3-256 topics like every
other module:

| event | indexed | data |
|---|---|---|
| `VerifyDeployed(sqaddress,sqaddress,uint256)` | instance | owner, threshold |
| `VerifyRegistered(sqaddress,bytes32,sqaddress)` | instance, instruction_id | signer |
| `VerifyAttested(sqaddress,bytes32,sqaddress,uint256)` | instance, instruction_id | attestor, attestation count |
| `VerifyFinalized(sqaddress,bytes32)` | instance, instruction_id | — |
| `VerifySpent(sqaddress,bytes32,string)` | instance, instruction_id | settlement action |

An operator service watches `VerifyRegistered` on its instance and attests;
a consuming system watches `VerifyFinalized` or `VerifySpent` and acts.

### Sizes and fees

| object | keys + signatures | notes |
|---|---|---|
| instruction (`register`) | ≈ 17.5 KB base64 | plus the payload, ≤ 4 KiB |
| attestation (`attest`) | ≈ 17.5 KB base64, ≈ 26 KB hex | the SDK signer emits hex |
| transaction data cap | 128 KiB | `mempool.MaxTxDataBytes` |
| `POST /verify/check` body | 256 KiB | |
| `POST /verify/attest`, `/settle` body | 512 KiB | |

Both are well under the cap, but they are the largest routine transactions
on the chain, and a block's gas budget counts them by size. Price
accordingly; the per-instance operator cap bounds the worst case at
seventeen attestations per instruction.

## Reading a proof

`GET /verify/proof/{id}?instance=SQ:…` (or the `proof` read through
`POST /call`) returns the record:

```json
{
  "module": "verify", "action": "proof",
  "instance": "SQ:…",
  "instruction_id": "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
  "digest": "7ff004f2b6c79782c237bd27943dfece0887ee3e0b8a2f0fdca13bacd1fa0826",
  "signer": "SQ:307f4559431e680933fc1c62503f050140e5740068248d7374461e0311a2728e",
  "instruction": {
    "version": 1, "purpose": "payment", "origin": "bank:swift",
    "settlement_hint": "splendor-qc", "settlement_action": "native_transfer",
    "from": "SQ:307f…", "to": "SQ:dddd…", "amount": "1000000000000000000",
    "payload": "aW52b2ljZS00Mg==",
    "mldsa_pubkey": "…", "sig_mldsa": "…", "slh_pubkey": "…", "sig_slh": "…"
  },
  "settlement_action": "native_transfer",
  "attestors": ["SQ:…", "SQ:…"],
  "attestations": 2,
  "threshold": 2,
  "threshold_met": true,
  "finalized": true,
  "spent": true,
  "registered_height": 1204,
  "height": 1219
}
```

(The id, digest and signer above are the SDK's pinned golden vector,
`sdk/sqc-sdk/testdata/golden-vectors.json`.)

What a consumer can verify **for itself, offline**: the instruction's two
signatures. The record carries both public keys and both signatures, so any
FIPS 204 and FIPS 205 implementation can recompute the digest from the
fields and the chain id and check them. It can also check that the signer is
who it expected, since the signer is a function of the ML-DSA key.

What a consumer takes **from the node**: the attestor list, the counts, the
flags and the heights. Attestation signatures are not stored, and SQC does
not yet serve state inclusion proofs, so the record is a *node-served state
read*, not a self-contained proof. Trust it the way you trust any full-node
answer: ask more than one node, or run your own — syncing is open to anyone.
A consumer that needs the attestations themselves can collect them from the
`VerifyAttested` events in the receipts, which any indexer keeps.

`height` is 0 until the instruction is finalized or spent.

## Calling it from a bank or a bridge

Two flows cover the cases. In both, the caller is an ordinary RPC client; it
does not run inside consensus and needs no validator key.

### Flow A: settle on Splendor QC

A custodian wants a payment from its SQC account to a counterparty to go
through only once two of its three signing officers approve.

1. **Deploy once.** Owner = the custodian's admin wallet, operators = the
   two officer wallets, `threshold: 2`. Keep the instance address.
2. **Build the instruction** for each payment: `instruction_id =
   SHA3-256(internal payment reference)`, `purpose: "payment"`,
   `origin: "bank:<bic>"`, `payload` = the canonical payment message,
   `settlement_hint: "splendor-qc"`, `settlement_action: "native_transfer"`
   (or `qrc20_transfer` with `token`), `from` = the custodian's paying
   account, `to`, `amount` in the smallest unit as a decimal string.
3. **Sign it twice.** `wallet.signVerifyInstruction(input)` in the SDK, or
   `types.SignVerifyInstruction` in Go. Keep `digest_hex`.
4. **Check, then register.** `POST /verify/check` with the signed
   instruction; on `valid: true`, submit `verify.register` from the paying
   account.
5. **Officers attest.** Each officer's service sees `VerifyRegistered`,
   reads the record, checks the payment against its own policy, and signs
   `wallet.signVerifyAttestation({ verify: instance, digest_hex })`. Anyone
   submits it through `POST /verify/attest`.
6. **Spend.** When `threshold_met` is true, the paying account (`from`)
   sends `verify.spend` through `POST /verify/settle`. The SPLD moves and
   the record is marked spent in the same receipt. A second spend is a
   failed receipt: `already spent`.

### Flow B: prove here, settle elsewhere

A bridge holds collateral on another chain and releases it only against an
SQC-recorded, dual-signed, multi-attested instruction.

1. **Deploy** with the bridge's verifier keys as operators.
2. **Instruction**: `instruction_id = SHA3-256(source tx hash ‖ log index)`,
   `origin: "eip155:1"` (or `bitcoin`, `bank:swift`), `settlement_hint`
   naming the other book, `settlement_action: "finalize_only"`, `payload` =
   the exact release order the other side will execute.
3. **Register, attest** as above.
4. **Finalize.** Anyone sends `verify.finalize` through `POST /verify/settle`
   (or `spend`, which for `finalize_only` moves nothing).
5. **The other side reads** `GET /verify/proof/{id}?instance=…`, checks
   what it must (below), and performs the release on its own ledger with its
   own keys. That integration is the other system's code; SQC serves the
   record and nothing else.

### The HTTP surface

| method | path | plane | what it does |
|---|---|---|---|
| POST | `/verify/check` | metered | Stateless dual-signature check. `200 {valid:true, chain_id, digest, signer, instruction_id, purpose, origin, settlement_hint, settlement_action}` or `200 {valid:false, reason}`. `400` only for malformed JSON or a body over 256 KiB. |
| POST | `/verify/attest` | metered | Admits a signed `verify.attest` TxCall, bare or as `{"tx": …}`. `202 {status:"accepted", hash}` or `400 rejected: …`. Wrong module or action is `400` before admission. |
| POST | `/verify/settle` | metered | The same for `verify.spend` or `verify.finalize`. |
| GET | `/verify/proof/{id}?instance=SQ:…` | read | The record. `400` for a bad id or a missing instance; `404` if not registered. `verify=` is accepted as an alias for `instance=`. |
| POST | `/call` | metered | Any module read: `{"to": <module router>, "data": hex(JSON module call), "encoding": "hex"}` → `{"decoded": …, "reverted": false}`. The `instance` read (owner, operators, threshold, paused) has no GET door and uses this. |
| POST | `/tx/submit` | metered | The general door; the verify doors add only the action check. |

Every write is a `TxCall` (type `0x03`) to the module router address, with
`data` holding `{"module":"verify","action":…,"params":{…}}`. The SDK builds
and signs these; the module reference lists every action's parameters.

```bash
# stateless check of a signed instruction
curl -s -X POST "$RPC/verify/check" -H 'content-type: application/json' \
  -d @signed-instruction.json

# the record
curl -s "$RPC/verify/proof/$ID?instance=$INSTANCE"

# the instance policy (a module read through /call)
DATA=$(printf '{"module":"verify","action":"instance","params":{"verify":"%s"}}' "$INSTANCE" | xxd -p | tr -d '\n')
curl -s -X POST "$RPC/call" -H 'content-type: application/json' \
  -d "{\"to\":\"$ROUTER\",\"data\":\"$DATA\",\"encoding\":\"hex\"}"
```

With the SDK (`@splendor/sqc-sdk`):

```ts
import { createSession } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'local', mode: 'seed', seed });

// 1. an instance: owner = this wallet, two operators, threshold 2
const { address: instance } = await s.deployVerify({ threshold: 2, operators: [op1, op2] });

// 2. the payer dual-signs (ML-DSA-65 + SLH-DSA, in WASM)
const { instruction, digest_hex } = await s.wallet.signVerifyInstruction({
  instruction_id: idHex, purpose: 'payment', origin: 'bank:swift',
  settlement_hint: 'splendor-qc', settlement_action: 'native_transfer',
  from: s.address(), to, amount, payload,
});
await s.verify(instance).register(instruction);

// 3. each operator attests, bound to this instance; anyone relays
const att = await operatorWallet.signVerifyAttestation({ verify: instance, digest_hex });
await s.verify(instance).attest(instruction.instruction_id, att);

// 4. settle once; the record is readable for good
await s.verify(instance).spend(instruction.instruction_id);
const proof = await s.verify(instance).proof(instruction.instruction_id);
```

`SqRpc.verifyCheck`, `verifyProof`, `verifyAttest` and `verifySettle` are the
doors for an integrator that speaks one endpoint per step.

### What the consuming system must check

Before acting on a record, whether on SQC or elsewhere:

1. `instance` is the one you trust — the address you deployed or were given,
   not one named inside the instruction.
2. `digest` equals what you compute from the instruction fields and the
   chain id, and both signatures verify (the offline check).
3. `signer` is the party you expected.
4. `instruction_id` is the nullifier you derived for this settlement, and
   you have not acted on it before.
5. `threshold_met`, and `finalized` or `spent` as your policy requires;
   `attestors` is a subset of the operator set you expect (read `instance`
   for the current list — the owner can change it).
6. For a value move on SQC, `spent: true` with the `settlement_action`,
   `from`, `to` and `amount` you expect: that is the transfer having
   happened.
7. Depth: the block at `height` is behind the head by whatever margin your
   risk policy wants. On Phase 1 SQC finality is the producer's signature;
   there is no reorg protocol to wait out, and no Byzantine quorum either.

### Integration checklist

- One instance per policy, not per instruction. Instances are cheap but they
  are the trust anchor; consumers pin the address.
- Ids derived, not random.
- Payload carries everything the second signature must cover.
- Operators are separate keys on separate machines; the threshold, not the
  dual signature, is your separation of duty.
- Operators verify the record before signing; an attestation is an approval,
  not a receipt.
- `from` reads the record before `spend`.
- Consumers watch events or poll the record; nothing pushes.
- Budget for 18–26 KB per register and per attest.
- Expect the SLH-DSA signing latency: seconds in a browser wallet.

## SQVL and QuFi

QuFi announced its platform on 4 September 2026. As published: a
verification layer that sits between user intent and final settlement; a
decentralized network of independent nodes that validate with ML-DSA-65 and
SLH-DSA, with ML-KEM-1024 for key encapsulation; "compact, lightweight
proofs" delivered to participating settlement systems; "no changes to
existing settlement networks"; uBTC as a proof-of-concept on Bitcoin
Testnet4, where minting, burning and redemption are governed by hybrid
ML-DSA and SLH-DSA challenges and redemptions settle as standard Bitcoin
transactions; a Genesis Node Program for operators; EVM ecosystems, Stellar
and Solana named as targets. Their stated reason for staying external is to
spare individual blockchains the storage, bandwidth and compute of large
post-quantum signatures.

SQVL shares the shape and the two signature families, and differs in where
it lives.

| | QuFi (as published) | SQVL on SQC |
|---|---|---|
| Where verification runs | An external network of QuFi nodes | Inside `sqc-node`, as fork-gated module state |
| Who verifies | Independent node operators (Genesis Node Program) | The owner and operators of one instance, listed by its owner; re-checked by every SQC node that applies the block |
| Signatures | ML-DSA-65 + SLH-DSA, hybrid challenges | ML-DSA-65 + SLH-DSA-SHA2-128s on the instruction and on every attestation, both required |
| Key exchange | ML-KEM-1024 | None in SQVL; the P2P transport uses ML-KEM-768, separately |
| What the ledger receives | A compact proof it can read without modification | The chain *is* the ledger: the record is chain state; other ledgers read it over RPC |
| Home-chain settlement | Not the model; settlement stays on the target chain | Atomic: the transfer and the spent flag are one receipt |
| Foreign settlement | The target chain reads the proof; uBTC redeems as a Bitcoin transaction | `finalize_only` + a foreign hint; the foreign side reads `GET /verify/proof` and acts with its own keys — no adapter ships in this repository |
| Why on- or off-chain | Off-chain, to keep big signatures off ledgers | On-chain, because SQC already carries PQ signatures on everything; the second family is added only where it pays |
| Stage | Live platform, Bitcoin Testnet4 proof-of-concept | Code complete and tested on `feat/validator`; not activated on any deployed tier |

SQVL is not a QuFi node, does not speak QuFi's proof format, and SQC is not a
QuFi settlement target. The two are answers to the same question from
opposite sides of the ledger boundary, and nothing more should be read into
the resemblance.

Sources for the QuFi description:
[The Quantum Insider, 4 Sep 2026](https://thequantuminsider.com/2026/09/04/qufi-post-quantum-verification-platform-digital-assets/);
[Cointelegraph](https://cointelegraph.com/news/qufi-launches-post-quantum-verification-platform-with-bitcoin-testnet-proof).

## What is not claimed

Everything below is true of the branch today and is stated so the write-up
cannot be read as more than it is.

- **Not Byzantine, not multi-validator.** SQC on this branch is one block
  producer, admitted by pass and bond. The `multi_validator` upgrade is
  reserved and unimplemented; `consensus/bft.go` is incomplete and unwired.
  An SQVL record is as final as a Phase 1 block, which is the producer's
  signature and nothing more.
- **Not on mainnet, not on any deployed tier.** The production genesis
  document schedules `module_revert_v1`, `qrc20_v2`, `qrc1155_v1` and
  `exchange_v1`, and nothing else. `verify_v1` has run on local development
  chains (unit tests and the `featsmoke` end-to-end driver) and nowhere else;
  the throwaway AWS run in
  [`AWS_FEAT_VALIDATOR_TEST_PLAN.md`](AWS_FEAT_VALIDATOR_TEST_PLAN.md) is
  written and not launched.
- **Not a node network.** There is no SQVL network to join. Attestors are
  whoever an instance owner lists, up to sixteen plus the owner, with no
  staking, no slashing, no admission and no incentive of their own. The
  validator gates govern block producers, not attestors.
- **Not a self-contained proof.** The record is a node-served state read. It
  lets a reader re-verify the instruction's two signatures offline; it does
  not carry the attestation signatures or a state inclusion proof, and SQC
  has no inclusion-proof API yet.
- **Not QuFi, not uBTC, not a bridge to Bitcoin or Ethereum.** No adapter
  for any foreign ledger ships here. "Settle elsewhere" means the foreign
  system reads the record and does its own settlement. The one bridge in
  this repository, `bridge_v1`, is the one-way legacy-mainnet migration and
  is unrelated.
- **Not two-person control per key.** Both of a wallet's keys come from one
  seed. Separation of duty is the threshold across distinct operators.
- **Not a KYC product, a bank, a custodian or a silent ledger.** There is
  no ClearNode. NFC cards in this stack hold an encrypted backup; they do not
  sign instructions.
- **Not audited.** The repository records no third-party security or
  cryptographic audit of this codebase, and no CAVP/CMVP validation of its
  cryptographic implementations (`readme.md`).
- **Not finished deciding.** SLH-DSA 128s versus 128f is open until the
  first instance on a real tier; the wallet extension must re-pin the rebuilt
  signer before it can dual-sign.

## Status and evidence

| what | where |
|---|---|
| Module, types, RPC, SDK, WASM signer | commit `3ee461b` on `feat/validator`; port notes and the defect table in [`FEAT_VALIDATOR.md`](FEAT_VALIDATOR.md) §2 |
| Unit tests | `vm/verify_test.go` (full flow, native transfer from the caller, atomic failed spend, register authorisation, instance-bound attestations, encoding length checks, pause, refused settlements, threshold bounds, fork gate); `core/types/verify_test.go` (dual signature and domain separation, stable digest, hex id on the wire, attestation binds the instance); `rpc/sqvl_rpc_test.go` (check, array id refused, proof input validation, doors check the action); `core/crypto/slhdsa_test.go` |
| Cross-implementation pins | `sdk/sqc-sdk/testdata/golden-vectors.json`, `verify` section, pinned in Go (`sdk/sqc-sdk/signer/verify_golden_test.go`) and against the WASM build (`wasm-verify.test.ts`) |
| End to end on a live node | `cmd/featsmoke`: dual-sign → `/verify/check` → deploy → register → attest → proof `threshold_met` → spend → proof `spent` → replayed spend fails `already spent` → `GET /verify/proof` serves it. Passed on a local chain before and after the `min_fee_v1` height ([`FEAT_VALIDATOR.md`](FEAT_VALIDATOR.md), "Verification") |
| Multi-node run | planned, not executed: [`AWS_FEAT_VALIDATOR_TEST_PLAN.md`](AWS_FEAT_VALIDATOR_TEST_PLAN.md) |

```bash
go test ./core/crypto ./core/types ./vm ./rpc -run 'SLH|Verify|InstructionID'
go test ./sdk/sqc-sdk/signer -run Golden
cd sdk/sqc-sdk && npm test
```

## Where the code is

| piece | path |
|---|---|
| Instruction, digests, dual sign / check | `core/types/verify.go` |
| SLH-DSA wrapper, seed derivation | `core/crypto/slhdsa.go`; context strings in `core/crypto/mldsa.go` |
| Module: instances, register, attest, finalize, spend, reads, events | `vm/verify.go`; routed in `vm/module_router.go` and `vm/module_router_read.go` |
| Fork gate | `core/forks/forks.go` (`verify_v1`, requires `module_revert_v1`) |
| HTTP doors | `rpc/sqvl_rpc.go`; mounted in `rpc/server.go` |
| SDK | `sdk/sqc-sdk/src/verify.ts` (module client), `splendor-wallet.ts` (signing), `rpc.ts` (doors), `types.ts`, `addresses/index.ts` (`verifyAddress`) |
| WASM signer exports | `sdk/sqc-sdk/wasm/main.go`: `sqGetSLHPublicKeyHex`, `sqSignVerifyInstruction`, `sqSignVerifyAttestation` |
| End-to-end driver | `cmd/featsmoke/main.go` |
| Module reference | [`modules/verify.md`](modules/verify.md) |
