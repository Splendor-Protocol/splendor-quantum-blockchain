# Validators (`validators_v1`)

> One bond, no tiers. After `validators_v1` the bond is required. After
> `validators_admission_v1` only the block producer or an address an active
> validator has `approve`d may `register`. That is not unconditional
> permissionless entry. Operator lifecycle: [`VALIDATOR.md`](../VALIDATOR.md).

Block-producer registration. One bond: **2,500 SPLD**. No tiers.

The bond is a lock of existing native supply. It does not mint SPLD. After
`unregister`, the bond returns at `finalize_exit` once **32 blocks** have
passed. `validators_v1` alone required only that bond. Once
`validators_admission_v1` is scheduled, `register` is approval/producer-based.

## Writes

All are `TxCall` to the module router, gated by `validators_v1`
(requires `module_revert_v1`).

| Action | Who | Effect |
| --- | --- | --- |
| `register` | caller | Locks 2,500 SPLD and records the caller’s ML-DSA-65 pubkey |
| `unregister` | the validator | Marks exiting; starts the 32-block wait |
| `finalize_exit` | the validator | After the wait, returns the bond |
| `approve` | an active validator | Allows `address` to `register` (`validators_admission_v1`) |
| `revoke` | an active validator | Withdraws that approval; does not touch an existing registration |

`register` params: `{ "pubkey": "<hex or base64 ML-DSA-65 public key>" }`.
`approve` / `revoke` params: `{ "address": "SQ:…" }`.

## Admission (`validators_admission_v1`)

Under `validators_v1` alone the registry becomes the BFT leader set the
moment it is non-empty, and `register` asks only for the bond — so anyone
holding 2,500 SPLD could register, replace the producer set with themselves,
and stop a chain whose node they cannot reach. `validators_admission_v1`
(requires `validators_v1` no later than itself) closes that:

- `register` succeeds only when the caller is **the producer of the block the
  call executes in** — every node reads the same producer from the header, so
  this is how a chain's genesis validator enrols itself with nobody to approve
  it — **or** an address an active validator has `approve`d.
- Any other caller gets a failed receipt (`not approved`) and keeps its SPLD.
- Approval stands until `revoke`, so an approved validator that exits may
  re-register.

The BFT engine also keeps the genesis validator as the sole producer until
the registry holds **two** validators, matching the executor's rule that below
two the registry is not yet the authority on who produces; under the fork the
executor likewise refuses a block sealed by a lone registrant that is not the
genesis validator. A lone registrant is logged and ignored until a second one
registers. Operators: register the tier's own validator **first**, then
`approve` each joiner before it registers.

Two consequences worth knowing before scheduling it:

- **No single-successor hand-off.** Because the genesis validator produces
  alone below two members, a chain cannot be handed to one successor by
  registering it and unregistering the genesis key: the registry needs at
  least two members before the genesis validator steps back. Hand off through
  two registrants, or keep the genesis key registered.
- **Activate on an empty (or genesis-only) registry.** Any active validator
  may `approve`, and nothing evicts a registrant. A stranger registered
  before the fork activates stays active and can admit others. Check
  the `list` read shows nothing but the tier's own key before setting the
  activation height, and register that key first.

## Reads / RPC

| Route | What |
| --- | --- |
| module read `list` | Active set |
| module read `validator` | One record (bond, status, exit height) |
| `GET /status` | `validator_set_size`, `consensus_mode`, `head_qc_*` |
| module read `bond` | The 2,500 SPLD figure in smallest units |
| module read `approved` | `{ "address" }` → whether that address holds a standing approval |

After `multi_validator`, the BFT engine uses this set for leader rotation and
quorum certificates. Local bring-up: [`docs/multi-validator-local.md`](../multi-validator-local.md).
