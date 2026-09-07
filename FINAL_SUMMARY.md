# SQC dual-gate validators — implementation summary

Branch: `feat/validator-pass-stake-gates`, pushed to `origin`. The validator
work is the commits from `769a9ec` (forks) to the review-fix commit at the
tip; the branch also carries earlier SDK/signer work merged from `main`.
Spec: `docs-sqc-implement-pass-stake.md`.

Anyone can sync the chain and stake. Only a key that is **admission-passed**
and **bonded above the minimum** may seal a block. Both gates are on-chain,
both are checked by one function, and that function is called from block
production and block acceptance alike.

---

## What landed

### 1. `validator_gates_v1` — the fork name

`core/forks/forks.go`. Added to `Known` and to the `module_revert_v1`
dependency rule, like every other routed module (a `register` that fails SOFT
must be a failed receipt, or the would-be validator's account jams at that
nonce). Before the activation height nothing here is reachable and any key the
chain accepts signatures from may produce — Phase 1 unchanged.

### 2. The staking module is a real bond, with an admission registry

`vm/staking.go`, rewritten from the orphaned original.

**What the original actually did:** `Register`'s doc said "the staker must have
already transferred the stake to the contract" — nothing did, so a bond was a
number in a slot and `Unstake` paid out coins that had never been taken.
`Slash` mutated the caller's `*big.Int`. The contract address came from a
function called `DeployStaking` that deployed nothing.

**Now:**

- One **singleton** at `StakingAddress` = `SHA3-256("splendor_staking_contract_v1")`,
  which is also the **escrow account**. `register`/`add_stake` move SPLD out of
  the staker's balance into it; `unstake` moves stake into unbonding and starts
  a ~7-day clock (`UnbondingBlocks = 302_400`); `withdraw` pays out after it.
  `balance(StakingAddress) >= Σ(bonds) + Σ(unbonding)` holds at every step.
- A **pass registry keyed by the ML-DSA-65 consensus key**. On this chain an
  address *is* `SHA3-256(pubkey)`, so key and address are one value — but the
  entry stores the full 1952 bytes, so a grant names the key that will sign
  blocks rather than a digest someone claims came from it. `register` likewise
  requires the caller to BE the key's address.
- **Admin**: the genesis validator until `set_admin` names someone else. That
  key already signs every block the chain accepts, so it adds no new trust.
- **The genesis validator is passed and bond-exempt implicitly** — it must seal
  block 1 before anything can be staked. An explicit entry overrides that,
  revocation included; reads report `source: "genesis"` vs `"admin"`.
- **Slashing always jails.** Coming back is `unjail`, a deliberate act.
  Slashed SPLD stays in escrow, credited to nobody (no action pays it out, no
  key derives to the address) — a slash that paid someone would give whoever
  can call it a reason to.
- Every action runs under a StateDB snapshot, so a half-applied one (escrow
  moved, record not written) cannot survive its own failure.

### 3. Routed

`vm/module_router.go`, `vm/module_router_read.go`. `staking` is the sixth
module the router reaches.

- **Writes** (all gated on `validator_gates_v1`): `register`, `add_stake`,
  `unstake`, `withdraw`, `unjail` — open to anyone; `slash`, `set_pass`,
  `set_admin` — admin-only, checked inside the module.
- **Reads** on `POST /call`: `validator`, `pass`, `can_produce`,
  `staking_info`, `validator_set`.
- No `deploy` action: a chain has one validator set.
- `ModuleContext`/`ReadContext` gained `GenesisValidator` — the address that
  sealed block 0, the module's bootstrap authority.
- `rpc/gov_rpc.go`'s pre-existing `/staking/*` routes now read the real
  singleton (they were pointed at an address nothing ever wrote to).

### 4. The eligibility predicate, wired to consensus

`vm.StakingContract.CanProduce` = `pass && (bond_exempt || active && stake >= min)`.

`core/block/validator_gate.go` connects it:

- `ProduceBlock` refuses to build a block the chain would not accept.
- `ExecuteBlock` refuses to apply one sealed by a key it does not admit, judged
  against the state at `height-1`.
- `consensus/validator.go` asks first (`EligibleToProduce`) and skips the slot
  **quietly**, logging the reason once — otherwise a validator waiting for its
  pass logs a production failure every two seconds and buries the real problem.

Three things switch it off, each deliberate: height 0 (block 0 is the anchor),
the upgrade not being active (replayed history is judged under the rules it was
sealed under), and an untouched registry on a node with no anchor (a state
`cmd/node` refuses to start in — kept as a safety net that fails open rather
than halting a chain on a rule nobody configured). Crash **replay**
(`reapplyBlock`) bypasses the gate: those blocks were accepted once, and a later
revocation must not retroactively invalidate committed history.

### 5. The genesis validator anchor

`cmd/node/genesis_validator.go`. The gate needs one fact the state does not
carry: which key sealed block 0. Handled exactly like the fork schedule — same
problem shape, same verified genesis document, same `meta:` persistence outside
the state root.

Sources, strongest first: what a previous run recorded → block 0's header →
the `--genesis` document → `--validator-pubkey-hex` (already documented as the
trusted *genesis* validator key). Deliberately **not** the validator's own
wallet key: that is only the genesis validator's by coincidence on a
single-producer chain, and anchoring to it would let a second producer declare
itself the chain's authority.

**A node that finds none of them on a chain scheduling `validator_gates_v1`
refuses to start.** The anchor feeds a state transition (`set_pass`
authorisation), so a node running without it would compute different state from
its peers on the first block carrying one. A startup error is the cheaper
version of that news. An empty chain is exempt — the apply paths record it as
they go.

`GET /status` reports it as `genesis_validator`, next to `forks`.

### 6. The last producer cannot be removed

Found by running it, not by reading it. On a live single-validator chain with
the gates on, revoking the producer's own pass was accepted, the block carrying
the revocation was sealed, and the next one never came:

```
WRN not producing: this validator is not admitted by the chain's validator
    gates error="validator has no admission pass: SQ:520c15… was admitted
    and then revoked" height=89
```

The transaction that would undo that can never be mined, because mining it
needs a producer. The chain was recoverable only by re-genesis — from one admin
transaction, with no warning.

So `set_pass` (revoking), `slash` and `unstake` now check, after their own
mutation and inside their own snapshot, that some key can still produce, and
refuse if none can. `unstake` is included because a solo validator withdrawing
its own bond halts everyone else exactly as a revocation does. The walk is
bounded by `MaxValidatorPasses` (64).

---

## Deviations from the spec, and why

**The genesis validator's pass is implicit, not written into genesis state.**
The spec offered "auto-pass + ensure genesis bond, or treat genesis as exempt
only via explicit pass". Seeding an explicit entry in `Genesis.ApplyToState`
would change the genesis state root, and therefore the chain identity, of every
live tier — and it would do nothing for a chain that adopts the upgrade by fork
extension, which has no genesis run left to seed it. The implicit entry gets the
same result (`pass=true`, bond-exempt, documented, readable via
`staking.pass`), costs no state-root churn, is revocable by the admin like any
other, and cannot halt a chain adopting the upgrade late.

**No proposal+vote admission.** The spec marked it a stretch, "prefer admin
pass if vote is heavy". `vm/governance.go` is itself unrouted and its `Execute`
applies nothing, so wiring a vote would have meant routing and finishing
governance too. Admin pass first, `set_admin` so the authority can move to a
governance contract later without touching this module.

**`claim_rewards` is not routed.** Nothing on the chain credits rewards —
issuance is Phase 2 — so an action that could only ever pay zero would be
surface without substance. `AddRewards` stays as unrouted Go API so the record
shape need not change when issuance lands.

**Scope held.** No BFT vote gossip, equivocation, timeouts or fork choice. No
scheduled PoA for N>1. No Solidity ports. No explorer or SDK work. `qc_root`
stays nil and `multi_validator` stays reserved and unimplemented —
`AcknowledgeIncomplete` and the Phase-2 gates are untouched. A second
passed-and-bonded validator is *eligible*; nothing gives it a turn.

---

## Verification

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test -count=1 ./...` | all packages pass |
| `go test -race ./vm/ ./core/block/ ./consensus/ ./cmd/node/` | clean |
| `gofmt -l` on every changed file | clean (incl. a pre-existing drift in `rpc/gov_rpc.go`, which CI's changed-files gofmt step would have failed on) |
| Live single-validator chain | see below |

New tests: `vm/staking_test.go` (module: escrow, unbonding clock, slashing,
admission, predicate, atomicity, the last-producer guard),
`vm/staking_router_test.go` (router: end-to-end register/stake/unstake/withdraw,
admission over the wire, HARD-before-fork vs SOFT-after, `/call` refuses writes,
admission preflight), `core/block/validator_gate_test.go` (both gate call sites,
follower rejection, fork-extension safety, genesis exemption),
`consensus/validator_gate_test.go` (quiet skip + ungated regression),
`cmd/node/genesis_validator_test.go` (anchor sources, persistence across
restart, the start-up refusal).

### Live chain smoke test

A throwaway chain with `{"module_revert_v1": 1, "validator_gates_v1": 1}`:

- anchor established from the genesis document, block 0 sealed, blocks produced;
- `GET /status` reports `forks` and `genesis_validator`;
- a signed `staking.register` for 5,000 SPLD landed at height 45 — validator
  `active`, tier `Bronze`, `escrow_balance = 5000…`, `total_staked = 5000…`,
  and the staker's balance down by exactly that;
- `set_pass` for a second key: `validator_set` shows it `passed: true,
  eligible: false`, `can_produce` explains *"holds a pass but has never
  registered a stake"*;
- revoking the sole producer's pass: **before** the guard, the chain halted at
  height 89; **after**, the same transaction is a failed receipt (fee kept,
  nonce consumed, error in the receipt) and the chain keeps producing.

---

## How to demo it

```bash
go build -o sqc-node ./cmd/node && go build -o sqc-wallet ./cmd/wallet

printf 'pw\npw\n' | ./sqc-wallet generate --out validator.sqwallet
PUB=$(printf 'pw\n' | ./sqc-wallet pubkey --wallet validator.sqwallet | tail -1)
ADDR=$(printf 'pw\n' | ./sqc-wallet address --wallet validator.sqwallet | grep -o 'SQ:[0-9a-f]*')

cat > genesis.json <<EOF
{ "chain_id": "sqc-demo", "network_name": "demo", "timestamp": 1786618272,
  "validator_pubkey_hex": "$PUB",
  "allocations": [{ "address": "$ADDR", "balance": "1000000000000000000000000" }],
  "total_supply": "1000000000000000000000000",
  "forks": { "module_revert_v1": 1, "validator_gates_v1": 1 } }
EOF

echo -n pw > wallet.pass && chmod 600 wallet.pass
./sqc-node --data-dir ./data --rpc-addr 127.0.0.1:18545 --validator \
  --wallet validator.sqwallet --wallet-pass-file wallet.pass \
  --genesis genesis.json --chain-id sqc-demo --block-interval 1
```

`GET /status` shows `genesis_validator` and `forks`. Module reads go to
`POST /call` against `SHA3-256("splendor:module-router:v1")` with hex-encoded
`{"module":"staking","action":"staking_info","params":{}}`. Writes are ordinary
signed `TxCall`s to the same address — `docs/modules/staking.md` has the full
wire format, and `/tmp/sqcsmoke/tool/main.go` (from the smoke run) is a 60-line
driver that builds, signs and submits one.

Enabling it on an existing chain: add `validator_gates_v1` above the current
head, restart every node with `--allow-fork-extension` and a re-pinned
`--genesis-doc-hash`, and make sure each one can determine the anchor (it will
refuse to start otherwise). The chain's own producer keeps producing through
the activation — the genesis key is admitted implicitly — so the registry can
be configured afterwards, at leisure, from that account.

---

## Files

40 files, +5,548 / −304 over `36fbfe8..HEAD` (the validator commits plus the
review fixes below). Core: `vm/staking.go` (rewritten), `vm/module_router.go`,
`vm/module_router_read.go`, `vm/events.go`, `core/forks/forks.go`,
`core/block/validator_gate.go` (new), `core/block/executor.go`,
`consensus/validator.go`, `cmd/node/genesis_validator.go` (new),
`cmd/node/main.go`, `rpc/call_rpc.go`, `rpc/server.go`, `rpc/gov_rpc.go`.
Docs: `docs/modules/staking.md` (new), `readme.md` §8/§9/§20/§22, `DEPLOY.md`
"Fork Schedule".

## Review fixes (2026-09-05)

Codex review findings, each confirmed by a failing regression test before the
fix:

- `vm/staking.go`: the last-producer check now runs after **every** `set_pass`
  update. A grant that overwrote the genesis key's implicit, bond-exempt entry
  with a non-exempt one removed the only producer on a chain where that key
  had never bonded. (`TestReaffirmingTheSoleProducerPassWithoutExemptionIsRefused`)
- `compiler/codegen.go`: a returning internal function called as a statement
  wrote its discarded return value into the caller's local slot 0; inlined
  calls (arguments, return word, callee locals) were not counted in the frame,
  so they overlapped the mapping-slot cache and the emit region. Frames now
  count inlined calls, callees get their own return word and see only their
  own bindings, the cache is bypassed inside inlined bodies and invalidated by
  callee writes, and recursion is refused instead of looping.
  (`compiler/inline_calls_test.go`)
- `compiler/lexer.go`: `break`, `continue`, `string`, `bytes32` are keywords.
  `compiler/codegen.go`: `string`/`bytes32` render as `bytes32` in signatures
  and the ABI, so `f(bytes32)` and `f(uint256)` are distinct and hash the right
  selector. (`compiler/lexer_keywords_test.go`)
- `core/state/statedb.go` + `rpc/state_rpc.go`: `GET /state/proof/{address}`
  now serves `proof_path` with the side of every sibling, plus `leaf_index`
  and `leaf_count`; the old instruction to derive sides from index parity was
  wrong for odd leaf counts. `state.VerifyMerkleProof` is the verifier's half.
  The handler no longer dereferences a nil executor.
- `sdk/sqc-sdk`: `deployExchange` / `deployBridge` return the receipt's
  `contract_address` when the call was confirmed, and label the next-height
  prediction as unconfirmed otherwise (`confirmed`, `predictedAddress`).

## Follow-ups, not done here

- Reward accrual and an issuance path (`AddRewards` has no caller).
- Delegation. The original had an unused delegator slot; it was removed rather
  than left implying a feature.
- Uptime scoring is stored but nothing writes it.
- Multi-validator scheduling — a separate ADR, and the real Phase 2 work.
- Explorer / SDK / wallet surfaces for the new reads.
