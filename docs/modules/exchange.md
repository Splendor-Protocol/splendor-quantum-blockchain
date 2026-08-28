# Exchange: the settlement module (`exchange_v1`)

The `exchange` native module is the chain's app-agnostic, Polymarket-shaped
settlement layer: **conditions** (a collateral token, a resolver, an end time,
two outcomes), **complete sets** (split collateral into YES+NO positions and
merge them back), and **operator-submitted atomic settlement of orders users
signed off-chain**. Matching, pricing and market metadata live in an off-chain
ledger (the gateway); what reaches consensus is *authorisation* (a maker's
ML-DSA-65 signature over an order) and *settlement* (moving exactly what that
order allows). There is no market logic in consensus.

Every write is a `TxCall` to `vm.ModuleRouterAddress` carrying the JSON
envelope `{"module":"exchange","action":…,"params":{…}}`, gated by the
`exchange_v1` fork: before its scheduled height every action is a HARD
"fork not active" error (refused at `/tx/submit`, unappliable in a block), so
a node lacking the fork rejects a block carrying one instead of diverging.
`exchange_v1` requires `module_revert_v1` scheduled at the same or an earlier
height (`forks.Schedule.Validate` refuses a document that gets this wrong): a
failed `match` must be a failed receipt, never a dropped transaction that jams
the operator's nonce.

## Custody model

The module holds user collateral **only** as the backing of minted position
sets. Before resolution, for every condition `c`:

```
supply(YES_c) == supply(NO_c) == locked_c
balance(exchange, collateral) >= Σ locked_c
```

Everything else stays in its holder's balance until a settlement the holder
signed for moves it. There is no allowance and no approval: **a valid signed
order is the authorisation**, and the module moves the settled amounts from
the maker directly (`QRC20Token.TransferWithEvents(maker, …)` — never through
`transfer_from`). After resolution `locked_c == supply(winning outcome)`, and
every redeem satisfies `paid + fee == burned`.

Positions are a QRC-1155 collection living **at the exchange address with
owner == exchange**, so it is module-managed: no key derives to the address,
the router's `qrc1155.mint` is unsatisfiable, and `qrc1155.transfer`/`burn`
refuse it (`managed by a native module`). Only the module mutates position
balances. **Position id = `condition_id * 256 + outcome`** (`position_id` read;
golden: condition 42, outcome 1 → `10753`). Condition ids run `1..condition_count`.

## Wire rules

- **Addresses** are `SQ:` strings.
- **Amounts, prices, share counts and position ids** are decimal strings
  through the strict `parseUint256` (canonical decimal, no leading zero,
  `< 2^256`). Condition ids, nonces, `outcome` and `side` are JSON integers
  (they are `u64`/`u8` in the signed struct); `expiration` and `end_time` are
  JSON `int64` unix seconds, judged against the **block header timestamp**.
- **32-byte fields** (`salt`, `question_hash`, order hashes) are lowercase hex
  without `0x`, exactly 64 chars, strict.
- **`signature`, `maker_pubkey`, `cancel_signature`** are Go `[]byte`, so on
  the wire they are **standard base64** (padded), like a transaction's
  `public_key`/`signature` and the WASM signer's output.
- **Price** is scaled by `1e6`: `600000` is 0.60 collateral per share, and must
  be strictly inside `(0, 1000000)`.
- Every error carries the stable prefix **`exchange <action>: `** and is SOFT
  (a failed receipt under `module_revert_v1`; fee kept, nonce consumed). Bad
  params are the action's own complaint, not an envelope error.

## Instance

`deploy{name, uri_prefix?, redeem_fee_bps? (default 0, ≤ 2000),
fee_recipient? (default deployer; zero or the exchange itself refused)}` lands
at

```
SHA3-256(deployer[32] ‖ decimal(tx nonce) ‖ u64be(block height) ‖ "exchange")
```

(`vm.ExchangeAddress`). The height is inside so a re-genesis that keeps its
chain id cannot accidentally resurrect old orders against a repeated address.
The receipt's `contract_address` is the instance. The deployer is the owner;
operators are granted afterwards.

## Write actions

| action | params | who | effect / events |
|---|---|---|---|
| `deploy` | `name, uri_prefix?, redeem_fee_bps?, fee_recipient?` | anyone | new instance + self-owned position collection; `contract_address` |
| `create_condition` | `exchange, collateral, resolver, end_time, outcome_count, question_hash` | owner or operator | sequential id; `end_time > block time`; `resolver` non-zero; `outcome_count` **must be 2** in v1; `collateral` a deployed contract ≠ exchange; `ConditionCreated` |
| `split` | `exchange, condition, amount` | anyone | `amount` collateral caller → exchange, mints `amount` of every outcome; blocked while paused, at/after `end_time`, once resolved; `Transfer, TransferSingle×2, PositionSplit` |
| `merge` | `exchange, condition, amount` | anyone | burns `amount` of every outcome, releases collateral; blocked **only** once resolved; `TransferSingle×2, Transfer, PositionsMerged` |
| `match` | `exchange, taker_order, maker_orders[≤16], fill_shares[]` | an active operator | atomic settlement, see below |
| `cancel_order` | `exchange, order, cancel_signature?, maker_pubkey?` | maker, operator, or anyone with the maker's signature over `CancelDigest` | idempotent; `OrderCancelled` |
| `increment_nonce` | `exchange, maker, expected_nonce, signature?, maker_pubkey?` | maker, or anyone with the maker's signature over `IncrementNonceDigest` | `expected_nonce == current` (single-use); `NonceIncremented` |
| `resolve` | `exchange, condition, outcome` | the condition's resolver, once | `Resolved` |
| `redeem` | `exchange, condition` | any holder | burns the caller's winning shares, pays `gross − fee`; `TransferSingle, Transfer[, Transfer, RedeemFeeCharged], Redeemed` |
| `redeem_for` | `exchange, condition, holder` | anyone | same, the **holder** is paid |
| `pause` / `unpause` | `exchange` | owner | blocks `match` and `split` only; `Paused` / `Unpaused` |
| `set_redeem_fee_bps` | `exchange, redeem_fee_bps (≤ 2000)` | owner | `RedeemFeeUpdated` |
| `set_fee_recipient` | `exchange, fee_recipient` | owner | non-zero, ≠ exchange; `FeeRecipientUpdated` |
| `set_operator` | `exchange, operator, active` | owner | `active` is **required** (`true` grants, `false` revokes; a missing flag is refused so it can never silently revoke); ≤ 64 distinct operators per instance; `OperatorSet` |
| `transfer_ownership` | `exchange, new_owner` | owner | non-zero, not a module address; `OwnershipTransferred` |

`cancel_order`, `increment_nonce`, `merge`, `redeem` and `redeem_for` are never
gated by the exchange's pause (a paused *collateral token* still blocks
`merge`/`redeem`). The redeem fee is winners-only:
`fee = floor(gross · bps / 10000)`, `payout = gross − fee`
(golden: `190909090` @ 250 bps → fee `4772727`, payout `186136363`).

## Orders

The signed struct (`types.Order`):

```
{ salt[32], maker, exchange, condition u64, outcome u8, side u8 (0 BUY / 1 SELL),
  share_amount u256, price u256 (1e6-scaled, in (0, 1e6)), expiration i64 (0 = never),
  nonce u64, recipient }
```

Wire shape (`match.taker_order`, `match.maker_orders[]`, `cancel_order.order`):

```json
{
  "salt": "<64 hex>", "maker": "SQ:…", "exchange": "SQ:…",
  "condition": 1, "outcome": 0, "side": 0,
  "share_amount": "100", "price": "600000", "expiration": 0, "nonce": 0,
  "recipient": "SQ:…",            // optional; omitted or zero = pay the maker
  "signature": "<base64>",         // optional once the order has filled > 0
  "maker_pubkey": "<base64>"       // optional for a maker with a registered key
}
```

- **`recipient` is signed.** Zero means "pay the maker" (the digest still
  commits to the zero bytes). `recipient == exchange` or `== ModuleRouterAddress`
  is refused (funds sent to a keyless address are stranded).
- **The hash is computed with the exchange the call is made on**, and the
  wire `exchange` must equal it: an order signed for instance X presented at
  Y is refused (`order is for exchange X, not Y`) and would never verify there.
- **Key resolution is registry-first**: a maker with a registered key (from
  its first signed transaction) verifies only against that key and
  `maker_pubkey` is ignored; a maker with no registered key must supply
  `maker_pubkey`, and only a key whose derived address equals `maker` is
  accepted (`maker_pubkey does not derive …`). The module never writes the
  registry.
- **Filled orders are already authorised**: an order whose `filled > 0` skips
  the signature check, so `signature` and `maker_pubkey` may be omitted from
  later fills. Cancel, expiry and nonce checks still run on every fill.

### Order digest (`types.OrderDigest`, signed under `CtxOrder = "SPLENDOR-QC-ORDER-v1"`)

```
"exchange:order:v1:" ‖ u16be(len(chain_id)) ‖ chain_id
‖ exchange[32] ‖ salt[32] ‖ maker[32]
‖ condition[8 BE] ‖ outcome[1] ‖ side[1]
‖ share_amount[32 BE] ‖ price[32 BE]
‖ expiration[8 BE] ‖ nonce[8 BE] ‖ recipient[32]
digest = SHA3-256(preimage)
```

Errors (never panics) on a nil/negative/`≥ 2^256` amount or price, a negative
expiration, or a chain id `ValidateChainID` refuses.

### Cancel digest (`types.CancelDigest`)

```
digest = SHA3-256("exchange:cancel:v1:" ‖ order_hash[32])
```

Cancel is idempotent and the order hash already commits to the chain id and
the exchange, so the preimage needs nothing else.

### Increment-nonce digest (`types.IncrementNonceDigest`)

```
"exchange:nonce:v1:" ‖ u16be(len(chain_id)) ‖ chain_id
‖ exchange[32] ‖ maker[32] ‖ expected_nonce[8 BE]
digest = SHA3-256(preimage)
```

`expected_nonce` must equal the maker's current nonce on-chain, so each
signature is single-use — without the bound nonce a captured signature would
bulk-cancel the maker's future orders every block.

### Golden vectors (pinned by `core/types/order_test.go`)

| field | value |
|---|---|
| chain_id | `splendor-quantum-1` |
| exchange | 32 × `0xee` |
| salt | `01 02 … 20` (bytes 1..32) |
| maker | 32 × `0xaa` |
| condition / outcome / side | `42` / `1` / `0` (BUY) |
| share_amount / price | `1000000000000000000` (1e18) / `600000` |
| expiration / nonce | `1800000000` / `3` |
| recipient | 32 × `0xbb` |
| **order digest** | `4ff70bf19d522519733bdd58ea58328cb48cf34d88cc2439f67935e9fe0699d1` |
| **cancel digest** (of that hash) | `2b4ca8255e5bed928d8c1b8f2a26716c75f97d31e84e03ab9f2dc705b9bf6d6b` |
| **increment-nonce digest** (exchange `0xee…`, maker `0xaa…`, expected nonce `3`) | `bee44b73c6ccbf4eac5012ec7d646aeca5acddf8141c937160c0759310c1e5b3` |

Any client implementation (SDK, WASM signer, TypeScript `hashOrder`) must
reproduce these exactly; changing one is a wire break.

## Matching

`match{exchange, taker_order, maker_orders[≤16], fill_shares[]}` is accepted
**only from an active operator** of the instance (module param `operators`,
owner-managed). `fill_shares[i]` is settled against `maker_orders[i]`;
`Σ fill_shares` is the taker's fill and must be ≤ the taker's remaining.

**Check order** (nothing is written before the last step; the whole call
reverts on any error): operator gate → pause → caps (`1..16` makers,
`fill_shares` length, every fill `> 0`) → static validation of every order
(ranges, exchange binding, recipient rule, hash) → condition state (exists,
not resolved, `block time < end_time`, outcomes in range, same condition for
every maker, **no duplicate hash**, taker not among the makers) → per-order
state (not cancelled; `block time <= expiration` with `0 = never`;
`nonce == maker nonce`; `fill ≤ remaining`) → per-pair pricing → ML-DSA
verification (skipped for an order with `filled > 0`) → apply.

**Kinds** are computed per `(taker, maker_i)` pair from the four side/outcome
values; mixed kinds in one match are allowed:

| kind | pair | settlement |
|---|---|---|
| COMPLEMENTARY | buy vs sell, same outcome | shares seller → buyer, collateral buyer → seller |
| MINT | BUY YES + BUY NO, `p_taker + p_maker ≥ 1e6` | both pay, a set is minted, each gets its outcome; `MintMatched` |
| MERGE | SELL YES + SELL NO, `p_taker + p_maker ≤ 1e6` | both deliver, the set is burned, each is paid; `MergeMatched` |

**Pricing & rounding (the load-bearing rule).** Every pair settles **at the
maker's signed price**, rounded in favour of the maker's bound (maker SELL →
`ceil(price·fill/1e6)`, maker BUY → `floor`); the taker pays or receives the
complement. The taker's bound is checked with a **≤ 1-unit rounding
tolerance**: per pair, a taker BUY pays at most `ceil(p_t·fill/1e6)`, a taker
SELL receives at least `floor(p_t·fill/1e6)`; the pair is refused otherwise.
The same bound is then enforced **once more on the aggregate** of the match:
a taker BUY pays at most `ceil(p_t·Σfill/1e6)` over every pair, a taker SELL
receives at least `floor(p_t·Σfill/1e6)`, and the whole match is refused as
`taker bound exceeded` otherwise — so an operator cannot split one fill into
n one-unit pairs and collect n units of rounding. Every collateral leg must be
non-zero: a `cost == 0` fill is refused (module calls are unmetered, so a
free dust fill has no gas deterrent). Consequences: equal-price crosses and
sum-==-1e6 mints/merges fill at any size; the operator's rounding power is
bounded to **< 1 smallest collateral unit per match (aggregate), never more
than the signed bound plus one unit**; MINT/MERGE produce **no surplus**
(`makerCost + takerCost == fill` exactly) — there is nothing to sweep.

**Operator powers** (consensus-bounded): reorder, delay, censor, choose which
crossing pairs fill and who is labelled taker (always inside both bounds),
self-trade with its own orders, cancel anyone's order. **Cannot:** alter any
field, overfill, fill outside a signed bound, move funds without a signed
order, mint positions, redirect redemptions, resolve.

**Receipt of a MINT match**, in order:
`Transfer` (maker → exchange), `Transfer` (taker → exchange), `TransferSingle`
(mint maker's outcome), `TransferSingle` (mint taker's outcome), `MintMatched`,
`OrderFilled` (maker), `OrderFilled` (taker, with `taker` = the exchange
address as in the Solidity exchange).

## The operator flow

1. Read the maker's nonce (`nonce` read) and let the maker sign an order
   (`sqSignOrder`) — no transaction, no fee, no allowance.
2. Keep the book off-chain; plan fills with the rounding rule above and
   `cost > 0`; use the **head block time**, not the wall clock, for
   expirations and `end_time`.
3. Dry-run with `simulate_match` (`caller` = the operator) — it returns the
   exact error string a real match would produce at the current head.
4. Submit `match` as a signed `TxCall` from the operator key; omit
   `maker_pubkey` for makers with a registered key and `signature` for
   orders already partly filled (17 orders with fresh keys ≈ 125 KB, inside
   the 128 KiB tx limit). `/tx/submit` refuses a `match` from a non-operator
   locally; under `module_revert_v1` a match that fails in consensus is a
   failed receipt and the operator's nonce advances.
5. Index `OrderFilled`/`MintMatched`/`MergeMatched` from receipts (`POST /logs`).
6. After the resolver's `resolve`, trigger `redeem_for` for holders.

## Reads

On `POST /call` (envelope to `vm.ModuleRouterAddress`), never fork-gated. Every
answer carries `module`, `action`, `exchange` and `exists` (the instance is a
deployed contract) except the pure `position_id`.

| action | params | answer |
|---|---|---|
| `exchange_info` | `exchange` | `owner, operators[], paused, redeem_fee_bps, fee_recipient, condition_count` |
| `condition_info` | `exchange, condition` | `condition, collateral, resolver, end_time, outcome_count, question_hash, resolved, winning_outcome, locked, position_ids[]` |
| `order_status` | `exchange, hash` | `hash, filled, cancelled` |
| `order_remaining` | `exchange, order` | `hash, share_amount, filled, cancelled, remaining` (0 when cancelled) |
| `order_hash` | `exchange, order` | `hash` — pure; pins the client SHA3 |
| `nonce` | `exchange, maker` | `maker, nonce` |
| `is_operator` | `exchange, address` | `address, is_operator` |
| `position` | `exchange, condition, holder` | `condition, holder, position_ids[], balances[]` (indexed by outcome) |
| `position_id` | `condition, outcome` | `condition, outcome, id` — pure |
| `simulate_match` | `exchange, caller, taker_order, maker_orders, fill_shares` | `caller, ok, error, logs, head_height, head_time` — dry run at the head, always reverted |

GET mirrors (identical bodies, same read dispatch): `/exchange/{addr}`,
`/exchange/{addr}/condition/{id}`, `/exchange/{addr}/order/{hash}`,
`/exchange/{addr}/nonce/{maker}`, `/exchange/{addr}/position/{condition}/{holder}`.
`order_hash`, `order_remaining` and `simulate_match` carry a body and are
`/call`-only.

## Events

`topic[0]` = SHA3-256 of the signature string verbatim; indexed values become
topics (32-byte, left-padded); every non-indexed value is a 32-byte big-endian
word in `data`, never a string. `sqaddress` is the 32-byte SQ address.

| event | indexed | data words | topic[0] |
|---|---|---|---|
| `ConditionCreated(uint64,sqaddress,sqaddress,uint64,uint8,bytes32)` | condition | collateral, resolver, end_time, outcome_count, question_hash | `4cc6e614117ecaa15f755b4f793612716b8f127b78e8c42d8daf2d301492bceb` |
| `OrderFilled(bytes32,sqaddress,sqaddress,uint64,uint8,uint8,uint256,uint256)` | hash, maker, taker | condition, outcome, side, fill, cost | `b0b988bae90030ea724d544dada8dca144409e30c42f6cfa2476652783b9e9cd` |
| `MintMatched(bytes32,bytes32,uint64,uint256)` | taker hash, maker hash | condition, fill | `d61dae09da3d748c1d5dac274a718ab17b6576a9551d702520eb9411f9565760` |
| `MergeMatched(bytes32,bytes32,uint64,uint256)` | taker hash, maker hash | condition, fill | `0238602d9f0afba010ce7a94d53dbce55e4205da11b00112436e5734c46b20b2` |
| `OrderCancelled(bytes32,sqaddress)` | hash, maker | — | `4bb646f5b666287d086a60dc8da222d3f171cac914967b0363b7cfed0900d8b4` |
| `NonceIncremented(sqaddress,uint64)` | maker | new nonce | `7dfe773962e9066784e7a84eb7fe8662de4c4a617b6dced8630c8b18a1bb1085` |
| `PositionSplit(sqaddress,uint64,uint256)` | holder | condition, amount | `38bee9cbce97233484fef63002749daa0cf8677ac08f1a28461785e2112e5b49` |
| `PositionsMerged(sqaddress,uint64,uint256)` | holder | condition, amount | `4b13b85012438efaa23cc35dd54d437752a76f988dabd1cff3969bedf66b7b07` |
| `Resolved(uint64,uint8)` | condition | outcome | `8a45b846305ad1fe822b79606ea3be220a5610d15f63e9a2e39a628e25ef1aba` |
| `Redeemed(sqaddress,uint64,uint256,uint256)` | holder | condition, burned, payout | `bb6fe4cbb91b534d55fce52ec7471217eed4780299ee8146faaff06ce21ee732` |
| `RedeemFeeCharged(sqaddress,uint256)` | fee recipient | fee | `57b05b5075d07d034f17299f9bcab3becee2724b23e284e821458480b4df5c70` |
| `OperatorSet(sqaddress,bool)` | operator | active (0/1) | `655833b1b04d387bba9597589c4f2d4c09a18d852607c40b43a544a806d42b2c` |
| `RedeemFeeUpdated(uint256)` | — | bps | `623b0c6e27b88ca35e0dfe3cee2a7d2caee6003d5aa4738b5babe9ce5d1ac537` |
| `FeeRecipientUpdated(sqaddress)` | recipient | — | `dd770cf64c4b083587a11f54d57d354d5e9aa6e4cef1d93d0fba6bd58d76d747` |

Shared with the token modules, emitted from the exchange address:
`Paused(sqaddress)` `522447d2…13dd8`, `Unpaused(sqaddress)` `ea3b4428…6a339`,
`OwnershipTransferred(sqaddress,sqaddress)` `bb98c3bc…14735`; from the
position collection: `TransferSingle(sqaddress,sqaddress,sqaddress,uint256,uint256)`
`0c9b1ec2…3b2e` (mint = from zero, burn = to zero, operator = the exchange);
from the collateral token: `Transfer(sqaddress,sqaddress,uint256)`
`9cd5034f…e748`. Constants: `vm.TopicConditionCreated` … `vm.TopicFeeRecipientUpdated`.

## Caps and determinism

`MaxExchangeMakers = 16`, `MaxExchangeOperators = 64`,
`MaxExchangeRedeemFeeBps = 2000`, `outcome_count == 2`. The module never reads
the wall clock (block time arrives in the module context), never iterates a Go
map, and formats no pointer; every error string is a function of the inputs and
the state. Every write snapshots the StateDB and the receipt's log count on
entry and reverts both on error, so a failed match leaves no residue whether
or not the executor's `module_revert_v1` snapshot is in force.
