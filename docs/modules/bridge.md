# Bridge: the legacy-migration module (`bridge_v1`)

One-way migration of SPLD and SPLV from the legacy Splendor EVM mainnet
(chain 2691) to this chain. Holders burn on the legacy side — the
`QuantumMigrationBurn` contract (`contracts/legacy-burn/`) forwards the funds
to `0x…dEaD` and emits `Burned(id, sender, asset, amount, quantumRecipient)` —
and an operator-run relayer (`cmd/bridge-relayer`) claims each burn here after
a confirmation depth. The full design, alternatives and rollout plan live in
`docs/plans/2026-08-31-quantum-migration-bridge.md`.

## Custody model

**Nothing mints native SPLD.** The instance address is module-managed (no
ML-DSA key derives to it) and doubles as the **escrow pool**: ops funds it
with a plain `TxTransfer` from the genesis allocation, and only `claim` can
ever move that balance. A claim *releases* pre-minted supply; the chain-wide
supply invariant is untouched.

**SPLV is a module-managed QRC-20.** `bridge.deploy` initialises it at
`SHA3-256(bridge ‖ "bridge:splv")` with `owner == the bridge instance` and
`max_supply` fixed at deploy (set it to the legacy SPLV supply). The only mint
path is the module's own in-package call inside `claim`; no external key can
ever satisfy the token's owner check. Holders use the ordinary `qrc20`
actions (transfer, approve, burn, permit) on it afterwards.

## Instance

`bridge.deploy` lands at `SHA3-256(deployer ‖ ascii(decimal(nonce)) ‖
u64be(height) ‖ "bridge")` — the height is folded in (like the exchange,
unlike QRC-20) so devnet/testnet/mainnet instances can never share an
address. The deployer becomes the owner; relayers are granted with
`set_operator` (bounded list, `MaxBridgeOperators = 16`, exchange
semantics: first grant takes a slot, revoke keeps it, re-grant reuses it).

## Wire rules

Amounts are strict decimal strings (`parseUint256`: ASCII digits, no sign, no
leading zero, < 2^256). The burn `id` is a JSON integer (u64, ≥ 1 — ids on
the EVM side start at 1). `evm_contract` is `0x` + 40 **lowercase** hex;
`evm_tx` is `0x` + 64 lowercase hex. Every action failure after the fork gate
is SOFT (a failed receipt, fee kept, nonce consumed); only the gate itself
and an unknown action are HARD.

## Write actions

All via `TxCall` → `vm.ModuleRouterAddress` with
`{"module":"bridge","action":…,"params":…}`. `tx.Amount` is ignored on the
router path — never attach value; fund the pool with a plain transfer
instead.

| action | auth | params |
|---|---|---|
| `deploy` | anyone (deployer = owner) | `evm_chain_id` (u64 > 0), `evm_contract`, `splv_name`, `splv_symbol` (≤ 64 bytes each), `splv_max_supply` (> 0), `max_claim_amount` (> 0), `threshold` (≥ 1) |
| `claim` | submitter: owner or operator | `bridge`, `id`, `asset` (`"spld"` \| `"splv"`), `amount`, `recipient` (SQ:), `evm_tx`, `attestations` |
| `set_operator` | owner | `bridge`, `operator`, `active` (required bool) |
| `set_threshold` | owner | `bridge`, `threshold` (≥ 1, ≤ operator cap) |
| `pause` / `unpause` | owner | `bridge` |
| `transfer_ownership` | owner | `bridge`, `new_owner` (not zero, a module address or any contract) |

`claim` refuses: paused; `id` 0; unknown asset; non-positive or non-decimal
amount; amount above `max_claim_amount`; a zero, module-address or contract
recipient (no key could spend the credit); malformed or **all-zero** `evm_tx`
(a claim must reference a real legacy burn transaction — the hash is the
burn's only provenance, and an all-zero one would make the payout
unauditable against the legacy chain); fewer than `threshold` distinct valid
attestations; and — the idempotency core — an `id` whose claimed slot is
already set, whatever the asset. The idempotency slot is re-checked inside
the same state snapshot as the payout, so the credit and the claimed-slot
write are one atomic unit.

### Attestations (M-of-N)

The credit is authorised by **`threshold` distinct attestations, not by the
submitter alone**. Each `attestations[]` entry is `{pubkey, signature}` (both
standard base64): an operator's ML-DSA public key and its signature over
`types.BridgeClaimDigest(chain_id, bridge, id, asset, amount, recipient,
evm_tx)` under `CtxBridge = "SPLENDOR-QC-BRIDGE-v1"`. The module requires every
supplied attestation to be exactly the canonical key/signature sizes (1952-byte
pubkey, 3309-byte signature — wrong-size blobs are refused before any parse),
well-formed, from an **active operator**, and carry a valid signature; distinct
attestors are counted, so ≥ `threshold` distinct operators must have signed the
exact burn. The submitter is only a spam gate (owner-or-operator, pays the
fee); it has no authority of its own, and the owner has no bypass.

This is what makes the bridge safe against a single point of trust: with
`threshold ≥ 2` and operators who each watch the legacy chain with their **own**
RPC, neither one compromised operator key nor one hostile/buggy legacy RPC can
move funds — an attacker needs `threshold` keys **and** `threshold` independent
views to agree on a forged burn. `threshold = 1` is single-operator trust,
equivalent to the pre-attestation design; raise it once ≥ 2 independent
attestors exist (add operators, then `set_threshold`). Lowering operators below
the threshold fails claims closed until enough are added — the safe direction.

On success `claim` transfers from the pool (`spld` — refused SOFT when the
pool is short, and the id stays claimable after funding) or mints on the SPLV
token (`splv` — the token's `max_supply` cap refuses over-mint), records the
claim height, bumps `claimed_total_{spld,splv}`, and emits `BridgeClaimed`.

> **`max_claim_amount` and the burn contract's `maxBurn` must be equal.** The
> module rejecting an over-cap claim only protects against operator fraud; a
> legitimate user who burns above the cap on the legacy chain would strand
> those funds (the burn is irreversible, the claim never passes). The burn
> contract therefore enforces the identical ceiling, so an over-cap burn
> cannot happen. Set both at deploy; changing the cap means redeploying both.
> The burn contract also relies on a **single global `id` counter** (one
> `++burnCount` shared by `burnSpld`/`burnSplv`); the idempotency slot keys on
> `id` alone, so per-asset counters would collide id 5/spld with id 5/splv.

## Reads

Via `POST /call` (read dispatch) and mirrored as plain GETs:

| action | REST mirror | returns |
|---|---|---|
| `bridge_info` (`{bridge}`) | `GET /bridge/{addr}` | owner, operators, paused, `threshold`, `evm_chain_id`, `evm_contract`, `splv_token`, `max_claim_amount`, `claimed_total_spld/splv`, `pool_balance` |
| `claim_status` (`{bridge, id}`) | `GET /bridge/{addr}/claim/{id}` | `claimed` (+ `height` when claimed) |

## Events

`BridgeClaimed(uint64,uint8,uint256,sqaddress,bytes32)` at the bridge
address: `id` and `recipient` indexed; asset word (0 = SPLD, 1 = SPLV),
amount, and the legacy burn tx hash as data words. An SPLV claim also emits
the QRC-20 `Mint` at the token address.

## The relayer flow

1. `bridge-relayer` preflights both chain ids **and** that the bridge's
   stored `evm_chain_id`/`evm_contract` match its own flags, and that its
   wallet is the owner or an active operator. It **refuses to start** when
   the bridge threshold is > 1 and no `--attestation-dir` is given (it could
   never reach quorum — it would hold on every burn forever, silently), and
   it alarms when the bridge has fewer active operators than the threshold
   (no claim can pass until operators are added or the threshold lowered).
2. Each pass: `safe = eth_blockNumber − confirmations` (default 120 — the
   legacy chain's PoA finality is probabilistic and a real rewind incident is
   on record); fetch `Burned` logs from the cursor in bounded batches; for
   each burn, skip if `claim_status` is claimed, else gather ≥ `threshold`
   attestations (§below) and submit one `bridge.claim` with them, then wait for
   its receipt.
   - **Attestations.** The relayer always signs its own attestation
     (`BridgeClaimDigest` under `CtxBridge`, with its operator key). At
     `threshold` 1 it submits with just that. At `threshold` > 1 it needs
     `--attestation-dir <shared path>`: each attestor writes its attestation for
     a burn there and reads its peers'; any node holding ≥ `threshold` distinct
     attestations submits (the on-chain claimed slot makes concurrent submitters
     safe). Only files whose pubkey derives to an **active operator** and whose
     signature **verifies locally** over the burn's digest count — foreign
     keys, garbage signatures, corrupt files and a flooding writer are ignored
     (or halt the pass with an alarm), so junk in the dir can delay a claim
     but never poison it. Below threshold it holds and retries — waiting for
     co-attestors is not counted as a failure. Run N attestor daemons, each
     with its own operator key **and its own legacy RPC**, sharing the dir on
     storage they all trust.
3. The cursor advances only past blocks whose burns are settled; it persists
   to a state file. Restarts are safe — idempotency lives on-chain. Only a
   claim that MINES AND REVERTS counts toward `--max-attempts`; a transient
   submit rejection (rate limit, fee floor, a still-pending nonce) or a slow
   receipt is retried without counting, so congestion never turns live burns
   into skips. A genuinely-reverting claim is recorded skipped and replayed
   with `--replay-id`. A burn whose amount exceeds the bridge's
   `max_claim_amount` is a deterministic on-chain refusal: it is recorded
   skipped (with an ALARM) **without ever submitting**, so no fees are burned
   on a guarantee. While the bridge is paused the relayer holds the cursor
   and submits nothing; an undecodable log from the pinned contract also holds
   the cursor (never silently dropped) and alarms. RPC/REST response bodies
   are size-bounded (64 MiB): a misbehaving endpoint fails the pass, it
   cannot balloon memory.

Operational requirements:

- **Point the relayer at one trusted legacy RPC**, not a load balancer. It
  reads `eth_blockNumber` and `eth_getLogs` and trusts the results; a hostile
  or buggy node can fabricate or withhold logs (the RPC endpoint is inside the
  operator trust boundary), and a lagging backend behind a load balancer can
  return an empty range without error and advance the cursor past unseen
  burns. Use a node you control, ideally archive.
- **One `--state` file per deployment.** The file is stamped with its
  (evm chain id, burn contract, bridge) and refuses to load against a
  different one, so a testnet cursor can't be reused for mainnet — but give
  each tier its own path regardless.

## Caps and determinism

Module calls are unmetered, so everything is bounded: the operator walk (16),
name/symbol (64 bytes), one claim per call. The module never reads the clock
— the claim height comes from the block context. No signature domains were
added: a claim is an ordinary signed `TxCall` from the operator account.
