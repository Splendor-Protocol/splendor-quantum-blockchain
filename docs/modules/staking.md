# Staking: validator bonding and admission (`validator_gates_v1`)

Who is allowed to produce a block on this chain, and what it costs them to
misbehave. Two gates, both required:

- **Admission** — an entry in the pass registry, held against the producer's
  ML-DSA-65 consensus key. Granting and revoking is an admin action.
- **Bond** — a registered validator record whose stake is at or above
  `MinValidatorStake` (the Bronze tier, 3,947 SPLD) and whose status is
  `active`. The stake is real: it leaves the staker's balance.

Two rather than one because they fail differently. An admission pass says a key
is one the network agreed to let in; a bond says the key has something to lose.
Either alone is a chain anyone with a wallet — or anyone on a list — can
produce on. This is the dual-gate shape classic Splendor's `Proposal.sol` +
`Validators.sol` has, ported to this chain's storage model rather than to its
Solidity.

**Registering and bonding are open.** Anyone can sync the chain, register their
own key and stake behind it, with an ordinary signed transaction. What is not
open is *producing* — that is what the pass adds.

## What this is NOT

It does not make the chain multi-validator. `multi_validator` is a separate,
still-unimplemented upgrade: BFT vote gossip, equivocation handling, timeouts
and fork choice are not written, `qc_root` stays nil, and the chain still runs
one producer. This upgrade decides **who may produce**, not how many must
agree. A second passed-and-bonded validator does not get a turn; it is
eligible, and nothing schedules it.

There is no reward accrual either. `AddRewards` exists on the record and
nothing calls it: block rewards are Phase 2, so a validator's `rewards` stays
at zero until there is an issuance path.

## The registry

A **singleton** at `StakingAddress` = `SHA3-256("splendor_staking_contract_v1")`
— reproducible offline by any client. There is no `deploy` action: a chain has
exactly one validator set, and a second instance would be a second answer to
the only question consensus asks here.

That same address is the **escrow account**. No ML-DSA key derives to it, so
nothing but this module can move what it holds, and the invariant

```
balance(StakingAddress) >= Σ(live bonds) + Σ(pending unbonding)
```

holds at every step (the surplus is slashed stake and any donations).

## Keys, not addresses

Every entry is keyed by the **consensus key**. On this chain an address *is*
`SHA3-256(ML-DSA-65 public key)` (`qcrypto.DeriveAddress`), so the key and its
address are one value — there is no separate account identifier a pass could be
keyed by instead. Each entry still stores the full 1952-byte key, so the
binding between a pass and the key that will sign blocks is auditable rather
than assumed, and every grant/revoke has to name the key itself.

`register` likewise requires the caller to BE the key's address. A stake
registered against someone else's key would be a bond the registrant cannot
lose control of and the key holder cannot lose — it would gate nothing.

## The genesis validator

The key that sealed block 0 is **passed and bond-exempt implicitly**, with
nothing written. It has to seal block 1 before anything can be staked; a chain
that required a bond to produce its first block could not start.

It is also the registry's **initial admin**, until `set_admin` names someone
else. That key already signs every block the chain accepts, so making it the
first admission authority adds no trust the chain did not already place.

Both are defaults, not privileges: an explicit `set_pass` entry for the genesis
key overrides the implicit one, revocation included, and `staking.pass` reports
`source: "genesis"` vs `"admin"` so a reader can tell which is which.

The node supplies that address to the module — see **The anchor** below.

## Wire rules

All writes go through `TxCall` → `vm.ModuleRouterAddress` with
`{"module":"staking","action":…,"params":…}`. `tx.Amount` is ignored on the
router path — never attach value; the bond amount is the `amount` parameter and
the module moves it. Amounts are strict decimal strings (`parseUint256`).
`pubkey` is standard base64 on the wire, the same rule as a transaction's
`public_key`.

Every failure after the fork gate is SOFT (failed receipt, fee kept, nonce
consumed). Only the gate itself and an unknown action are HARD.

## Write actions

| Action | Params | Who | Effect |
|---|---|---|---|
| `register` | `pubkey`, `amount` | the key's own address | moves `amount` (≥ min) into escrow, creates the record, status `active` |
| `add_stake` | `amount` | the validator | moves more into escrow; returns an `exiting` validator to `active` once it clears the floor |
| `unstake` | `amount` | the validator | stake → unbonding, releasable at `height + UnbondingBlocks` |
| `withdraw` | — | the validator | pays matured unbonding out of escrow |
| `unjail` | `amount` (optional top-up) | the validator | jailed → active, if the bond clears the floor |
| `slash` | `validator`, `amount` | **admin** | takes up to the whole bond and jails |
| `set_pass` | `pubkey`, `passed`, `bond_exempt` | **admin** | grants or revokes admission |
| `set_admin` | `admin` | **admin** | hands the authority on |

`passed` is required and a pointer in the wire shape on purpose: a missing flag
must not silently revoke an admission a typo left out. `bond_exempt` on a
revoked pass is contradictory and refused.

Every action runs under a StateDB snapshot, so a half-applied one (escrow
moved, record not written) cannot survive its own failure.

### The last producer cannot be removed

`set_pass` (every update, not only a revocation), `slash` and `unstake` each
check, *after* their own mutation and inside their own snapshot, that at least
one key can still produce. If none can, the action is refused.

A grant can remove the last producer too. On a chain whose genesis key has
never bonded, that key produces on its implicit, bond-exempt entry; an explicit
`set_pass` for it with `passed = true` but `bond_exempt = false` makes its
eligibility depend on a bond it does not have. That update is refused until
the key is bonded (or a replacement is admitted), and reaffirming it with
`bond_exempt = true` is allowed.

This is not theoretical. Revoking the only producer's pass stops block
production, and the transaction that would undo the mistake can never be mined
— mining it needs a producer. The chain would be recoverable only by
re-genesis. So an exclusion has to be preceded by admitting a replacement:
grant the successor a pass (and let it bond, or grant it `bond_exempt`) first,
then revoke.

The check walks the bounded pass list, so it costs at most
`MaxValidatorPasses + 1` eligibility evaluations.

## Unbonding

`UnbondingBlocks = 302,400` — about 7 days at the 2-second interval. A bond
that can be pulled in the same block it was counted is not a bond; the delay is
what makes slashing possible at all, since misbehaviour is detected after the
block that contained it.

A second `unstake` before the first matures pools the amounts and **restarts
the clock**. Letting the later amount ride the earlier one's timer would make
the delay a function of when you first unstaked rather than of when you last
did — a bond releasable on demand by anyone planning one period ahead.

## Slashing

A slash always **jails**, whatever the remaining bond. Coming back is `unjail`,
a deliberate act, not something that happens when the balance recovers. An
over-estimated `amount` is capped at the bond rather than refused: the point is
to take what is there.

The slashed SPLD **stays in the escrow account** and is credited to nobody. No
action pays it out and no key derives to the module address, so it is out of
circulation for good. A slash that paid someone would give whoever can call it
a reason to call it. `staking_info.slashed_total` reports the running figure.

## Tiers

| Tier | Stake |
|---|---|
| Bronze | 3,947 SPLD |
| Silver | 39,474 SPLD |
| Gold | 394,737 SPLD |
| Platinum | 3,947,368 SPLD |

The tiers **rank** validators; `MinValidatorStake` (== Bronze) **admits** them.
They are named separately so a later change to the ranking cannot silently move
the gate.

## The predicate

```
CanProduce(addr) := pass && (bond_exempt || (registered && active && stake >= min))
```

One function (`vm.StakingContract.CanProduce`), called from both sides:

- `ProduceBlock` refuses to build a block the chain would not accept.
- `ExecuteBlock` refuses to apply one sealed by a key it does not admit, judged
  against the state at `height-1` — the same state, the same predicate.

The consensus loop asks first (`Executor.EligibleToProduce`) and skips the slot
quietly, so a validator waiting for its pass logs the reason once instead of a
production failure every two seconds.

Three things switch the gate off, each deliberate:

1. **Height 0.** Block 0 is the anchor; there is nothing yet to check it
   against.
2. **The upgrade not being active at that height.** Before it, any key the
   chain accepts signatures from may produce — which is what single-validator
   Phase 1 has always been — and replayed history is judged under the rules it
   was sealed under.
3. **An untouched registry on a node with no anchor.** `sqc-node` refuses to
   start in that state on a gated chain, so it should not be reachable; it
   fails open rather than halting a chain on a rule nobody configured.

Block **replay** after a crash (`reapplyBlock`) does not go through the gate.
Those blocks were accepted once, and a later revocation must not retroactively
invalidate committed history.

## Read actions

Served on `POST /call` with the same envelope.

| Action | Params | Returns |
|---|---|---|
| `validator` | `address` or `pubkey` | `registered`, `stake`, `unbonding`, `unbonding_height`, `status`, `tier`, `rewards`, `joined_height`, `last_slashed`, `uptime_score` |
| `pass` | `address` or `pubkey` | `listed`, `passed`, `bond_exempt`, `source` (`genesis`/`admin`), `height`, `pubkey` |
| `can_produce` | `address` or `pubkey` | `eligible`, and `reason` when not |
| `staking_info` | — | `total_staked`, `slashed_total`, `escrow_balance`, `validator_count`, `pass_count`, `initialised`, `admin`, `min_stake`, `unbonding_blocks`, `max_passes`, `tiers` |
| `validator_set` | — | every explicit entry plus the implicit genesis one, each with `stake`, `status`, `eligible` |

`can_produce` calls the same `CanProduce` consensus does, so an operator can
find out why their validator is silent without reading node logs — and gets the
answer the chain would give, not a second implementation of the rule to
disagree with the first.

Naming both `address` and `pubkey` is allowed; a pair that disagrees is an
error, not a silent preference for one. Every read also reports
`genesis_anchor_known`, so an answer given without the anchor is visibly one
the node could not fully stand behind.

The pre-existing `GET /staking/validators/{addr}`, `/staking/total-staked`,
`/staking/count` and `/staking/tiers` endpoints now read the real singleton (they
were pointed at an address nothing ever wrote to).

## The anchor

The module needs one fact the chain state does not carry: the address of the
key that sealed block 0. It is handled exactly like the fork schedule, and for
the same reason — a fact about the network every node must agree on, out of the
same verified genesis document, that has to survive a restart on a host that no
longer has that document.

`sqc-node` determines it from, strongest first:

1. what a previous run persisted (`meta:genesis_validator`, outside the state
   root, next to `meta:forks`);
2. block 0's header, when this node has it (`ValidatorAddr` is inside the
   header, so it is covered by `--genesis-hash`);
3. the `--genesis` document's `validator_pubkey_hex`, guarded by
   `--genesis-doc-hash`;
4. `--validator-pubkey-hex`, already documented as the trusted **genesis**
   validator key.

Deliberately NOT the validator's own wallet key: that is only the genesis
validator's by coincidence on a single-producer chain, and anchoring to it
would let a second producer declare itself the chain's authority.

Both genesis apply paths record it as they go, and a validator that seals block
0 without a document records it then. **A node that finds none of these on a
chain scheduling `validator_gates_v1` refuses to start**: the anchor feeds a
state transition (`set_pass` authorisation), so a node running without it would
compute different state from its peers on the first block carrying one. A
startup error is the cheaper version of that news. An empty chain is exempt —
genesis has not been applied yet, and the apply paths handle it.

`GET /status` reports the anchor as `genesis_validator` alongside `forks`, so
an operator can confirm every node agrees before an activation height arrives.

## Scheduling it

`validator_gates_v1` requires `module_revert_v1` in force no later than itself,
like every other routed module: a `register` that fails SOFT must be a failed
receipt, or the would-be validator's account jams at that nonce.

```json
"forks": { "module_revert_v1": 1, "validator_gates_v1": 1 }
```

A running chain can adopt it by fork extension (`--allow-fork-extension`). It
arrives at the activation height with an untouched registry, which is safe: the
genesis validator — the producer such a chain already runs — is admitted
implicitly, so the chain does not halt on its own upgrade. Configure it
afterwards with `set_pass` / `set_admin` from the genesis validator's account.

## Events

At `StakingAddress`:

- `ValidatorRegistered(sqaddress,uint256)` — validator indexed
- `ValidatorStakeAdded(sqaddress,uint256,uint256)` — added, new total
- `ValidatorUnstaked(sqaddress,uint256,uint64)` — amount, release height
- `ValidatorWithdrawn(sqaddress,uint256)`
- `ValidatorUnjailed(sqaddress,uint256)`
- `ValidatorSlashed(sqaddress,uint256,uint256)` — taken, remaining
- `ValidatorPassSet(sqaddress,bool,bool)` — validator and `passed` indexed,
  `bond_exempt` as data
- `StakingAdminChanged(sqaddress,sqaddress)`

## Caps and determinism

Module calls are unmetered, so everything is bounded: the pass registry holds
at most `MaxValidatorPasses = 64` entries (granted or revoked), which bounds
the `validator_set` walk. The module never reads the clock — every height comes
from the block context, so a producer and a follower judge the same call the
same way.
