# QRC-20 v2: permit and the allowance surface (`qrc20_v2`)

The `qrc20` native module routes twelve write actions and seven reads through
the module router (`TxCall` to `vm.ModuleRouterAddress`, JSON envelope
`{"module":"qrc20","action":…,"params":{…}}`). The five original writes
(`deploy`, `mint`, `transfer`, `approve`, `burn`) are ungated. The eight added
by v2 are gated by the `qrc20_v2` fork: before its scheduled height they are a
HARD "fork not active" error (refused at `/tx/submit`, unappliable in a block),
so a node lacking the fork rejects a block carrying one instead of diverging.

## Wire rules (every v2 action)

- **Addresses** are `SQ:` strings (`token`, `owner`, `spender`, `from`, `to`,
  `new_owner`).
- **Amounts** are decimal strings decoded by the strict `parseUint256`:
  canonical decimal only — ASCII digits, no sign, no whitespace, **no leading
  zero** (`"0"` itself is fine), value `< 2^256`. `"09"`, `"+1"`, `"1e3"` and
  `""` are errors, never silently zero.
- **`deadline`** is a JSON integer (unix seconds), judged against the **block
  header timestamp** of the block the tx lands in, never the wall clock. The
  permit is valid while `block_time <= deadline`; `deadline` must be `> 0`.
- **`signature` and `owner_pubkey`** are Go `[]byte` fields, so on the wire
  they are **standard base64** (padded, `+`/`/` alphabet) — the same encoding a
  transaction's `public_key`/`signature` and the WASM signer's output use. A
  value that does not decode is the action's own "bad params" failure (a failed
  receipt under `module_revert_v1`), not an envelope error.

## Actions

| action | params | who | effect / events |
|---|---|---|---|
| `permit` | `token, owner, spender, amount, deadline, signature, owner_pubkey?` | anyone (the relayer pays) | sets `allowance[owner][spender] = amount`, `permit_nonce[owner]++`; `Approval` |
| `transfer_from` | `token, from, to, amount` | the spender | `Transfer` then `Approval` (the decremented allowance; none if the allowance is `2^256-1`) |
| `increase_allowance` | `token, spender, amount` | the owner | `Approval` with the new total (`< 2^256`) |
| `decrease_allowance` | `token, spender, amount` | the owner | `Approval` with the new total; fails below zero |
| `burn_from` | `token, from, amount` | the spender | `Burn` then `Approval` (decremented allowance) |
| `transfer_ownership` | `token, new_owner` | the owner | `OwnershipTransferred(previous, new)`; zero refused |
| `pause` / `unpause` | `token` | the owner | `Paused(account)` / `Unpaused(account)` |

Reads on `POST /call` (never gated): `permit_nonce{token, owner}` →
`{"permit_nonce": "<decimal>"}`, `paused{token}` → `{"paused": bool}`,
`owner{token}` → `{"owner": "SQ:…"}`, next to the existing `balance_of`,
`total_supply`, `allowance`, `token_info`.

**IsContract guard.** Under `qrc20_v2` every non-deploy action first checks
that `token` is an account the module deployed (`is_contract`); a mistyped
address is refused with `qrc20: token … is not a deployed contract`. Before
the fork the pre-existing actions keep their legacy behaviour (an approve
against an undeployed address "succeeds" against nothing) — even this bug fix
is fork-gated so the rule changes at a height every node agrees on.

**Pause.** `permit`, `approve`, `increase_allowance`, `decrease_allowance`
record allowances and are not blocked by pause; `transfer`, `transfer_from`,
`mint`, `burn`, `burn_from` are.

## Permit digest (v2)

The owner signs, under the ML-DSA-65 context `CtxPermit`
(`"SPLENDOR-QC-PERMIT-v1"`), the SHA3-256 of this fixed-width preimage
(`types.PermitDigest` in `core/types/transaction.go`):

```
"qrc20:permit:v2:"
‖ u16be(len(chain_id)) ‖ chain_id
‖ contract[32] ‖ owner[32] ‖ spender[32]
‖ amount[32, big-endian] ‖ nonce[8, big-endian] ‖ deadline[8, big-endian]
```

- `chain_id` is the id the node adopted from its genesis (`types.ChainID`).
  It is inside the digest because a QRC-20 address derives from the deployer
  and its nonce only — the same token has the same address on every tier, so
  without it a devnet permit would verify byte-for-byte on mainnet.
- `nonce` is the owner's current `permit_nonce` for that token (read it with
  `permit_nonce`); it advances only on a successful permit, so a signature is
  single-use and an expired or otherwise refused permit does not consume it.
- `PermitDigest` errors (never panics) on a nil/negative/`>= 2^256` amount, a
  negative deadline, or a chain id `ValidateChainID` refuses.

### Golden vector (pinned by `core/types/permit_test.go`)

| field | value |
|---|---|
| chain_id | `splendor-quantum-1` |
| contract | 32 × `0xaa` |
| owner | 32 × `0xbb` |
| spender | 32 × `0xcc` |
| amount | `1000000000000000000` (1e18) |
| nonce | `7` |
| deadline | `1800000000` |
| **digest** | `376313a1948273267869c6fed8a3b6415e8559862a072b57d7cb7e3235cb7827` |

Any client implementation (SDK, WASM signer, TypeScript) must reproduce this
digest exactly; changing it is a wire break.

## Key resolution

Registry first: if `owner` has a registered public key (set by its first
signed transaction) that key is the only one that verifies and `owner_pubkey`
is ignored. Only an owner with **no** registered key may present
`owner_pubkey`, and then only a key whose derived address equals `owner`;
otherwise the permit is refused with `owner_pubkey does not derive owner`.
A permit never registers the presented key.

## Relayer note

The permit-relay rail: the token holder signs only the permit digest (no
transaction, no fee, no nonce of its own); a relayer submits `permit` as a
signed `TxCall` and pays the flat fee, then spends the allowance with
`transfer_from` (or hands it to a module that does). Because both are
separate transactions from the relayer, and the mempool orders one sender's
transactions by nonce, submit `permit` at nonce `n` and the spend at `n+1`.
Under `module_revert_v1` a permit that fails (expired, replayed, wrong key)
is included with a failed receipt and the relayer's nonce advances, so a bad
permit never jams the relayer's account. The `permit_nonce` read tells a
signer which nonce to put in the next digest; `/estimate` on the envelope
reports "not active" when `qrc20_v2` is not yet scheduled at the next height.

## Event topics (SHA3-256 of the signature string)

`Transfer(sqaddress,sqaddress,uint256)`, `Approval(sqaddress,sqaddress,uint256)`,
`Burn(sqaddress,uint256,uint256)`, `Paused(sqaddress)`, `Unpaused(sqaddress)`,
`OwnershipTransferred(sqaddress,sqaddress)` — indexed addresses become topics
(32-byte, left-padded); non-indexed amounts are 32-byte big-endian words in
`data`. Constants: `vm.TopicTransfer`, `vm.TopicApproval`, `vm.TopicBurn`,
`vm.TopicPaused`, `vm.TopicUnpaused`, `vm.TopicOwnershipTransferred`.
