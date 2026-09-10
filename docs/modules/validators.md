# Validators (`validators_v1`)

> Permissionless registration is the protocol rule (one bond, no tiers).
> Public admission has not launched: there is no supported public join
> path. Operator lifecycle: [`VALIDATOR.md`](../VALIDATOR.md).

Permissionless block-producer registration. One bond, no tiers.

Anyone who can fund **2,500 SPLD** can register. The bond is a lock of
existing native supply. It does not mint SPLD. After `unregister`, the bond
returns at `finalize_exit` once **32 blocks** have passed.

## Writes

All are `TxCall` to the module router, gated by `validators_v1`
(requires `module_revert_v1`).

| Action | Who | Effect |
| --- | --- | --- |
| `register` | caller | Locks 2,500 SPLD and records the caller’s ML-DSA-65 pubkey |
| `unregister` | the validator | Marks exiting; starts the 32-block wait |
| `finalize_exit` | the validator | After the wait, returns the bond |

`register` params: `{ "pubkey": "<hex or base64 ML-DSA-65 public key>" }`.

## Reads / RPC

| Route | What |
| --- | --- |
| `GET /validators` | Active set |
| `GET /validators/{addr}` | One record (bond, status, exit height) |
| module read `bond` | The 2,500 SPLD figure in smallest units |

After `multi_validator`, the BFT engine uses this set for leader rotation and
quorum certificates. Local bring-up: [`docs/multi-validator-local.md`](../multi-validator-local.md).
