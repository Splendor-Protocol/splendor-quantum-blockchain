# Blockpeak on Splendor Quantum — chain-side plan

## Context

Blockpeak (prediction markets + agent betting) runs today on the EVM chain 2692 via Solidity
contracts, viem clients, and a **permit-relay** rail: the agent signs only an EIP-2612 permit,
the pinned relayer submits `permit → transferFrom → buyFor(beneficiary=agent)` and pays gas.
The public 2692 node does not even expose the `x402_*` namespace — permit-relay *is* the live
path (`blockpeak-markets/packages/gateway/src/x402-settle.ts:22-33`).

The new chain (`splendor-quantum-blockchain`) is not an EVM (`vm/engine.go:5`), has no
Solidity compiler, and reaches native modules only through `TxCall → ModuleRouterAddress`
(`core/block/executor.go:510-516`). A three-way review (this session, cursor-agent, Codex)
converged on the same blockers and surfaced one critical bug: `QRC20.Permit`
(`vm/qrc20.go:507-540`) verifies a signature against a **caller-supplied** public key without
checking that the key derives the `owner` address, and never enforces `deadline`. It is
unexploitable today only because `permit` is not routed.

**Goal (revised after the architecture discussion):** keep the base chain *generic* and let
apps run as off-chain ledgers that settle on it non-custodially. Concretely: fixed and routed
QRC-20 permit/allowance surface, routed QRC-1155 with an owner, sane module-failure semantics,
fork gating wired into consensus, an x402 payload that no longer depends on the pubkey
registry, a signer that can sign permits and orders — and **one app-agnostic settlement module**
(`exchange`): conditions, complete sets, and operator-submitted atomic settlement of
user-signed orders, Polymarket-shaped. Matching, pricing and market metadata live in an
off-chain ledger (the gateway). No market logic in consensus.

## Decisions already made (do not re-litigate)

- **Scope:** chain-side, this repo only. Client (`blockpeak-markets`, `blockpeak-agents`) and
  wallet ports are follow-on milestones, listed at the end, not planned here.
- **Payment design: permit-relay.** No new tx type, no fee sponsor, no consensus change to
  x402 settlement. On-chain `TxX402` stays native-SPLD. Agents sign only an ML-DSA QRC-20
  permit under `CtxPermit` (deposits/splits) or an order under `CtxOrder` (trades); the
  operator submits one atomic `exchange.match` and pays the fee.
- **Chain id + 3-tier re-genesis is owned by the peer session "quantum chain analysis".**
  Mainnet keeps `splendor-quantum-1`; devnet/testnet get new ids; balances preserved. This
  plan touches no chain-id code and assumes a fresh genesis on all tiers shortly.
- **Activation heights are genesis-driven, never hardcoded.** All current forks are scheduled
  at height 1 in the new genesis documents; mainnet may *omit* `exchange_v1` (real-money gate
  stays closed).
- **Architecture (decided after the research discussion): generic settlement module +
  off-chain ledger.** Not an on-chain FPMM market module (the earlier PR-6 design is shelved,
  fully specified, in case an on-chain maker is ever wanted); not a plugin/WASM runtime
  (6–12 months, CosmWasm-class determinism/gas risk — see "How this plan was produced");
  not a custodial ledger (discards the one thing the L1 provides). Blockpeak on 2692 stays as
  is; the quantum ledger is a parallel product.

## Verified facts that shape the design (from the read-only code sweep)

- **`types.ChainID` is already a runtime variable on `main`** (`core/types/transaction.go:27-91`:
  `DefaultChainID`, `var ChainID`, `SetChainID`, `ValidateChainID`; `--chain-id` / `SQC_CHAIN_ID`
  at `cmd/node/main.go:112,314-320`; adoption from the stored head at `:949`). The peer landed it.
  No chain-id work in this plan.
- **A module call that errors is dropped, not failed.** `execCall` returns the dispatch error
  verbatim (`core/block/executor.go:533`), the per-tx snapshot is reverted and the tx excluded
  (`:384-393`, `:670-676`), and `ExecuteBlock` rejects a block containing one (`:179-180`).
  `vm/module_router.go:53-54` documents the opposite and is wrong. Consequence: a relayer whose
  tx fails permanently jams its account — the tx is never pruned and the mempool refuses a
  replacement at the same nonce (`core/mempool/mempool.go:105-110`, no replace-by-fee).
- **The mempool has no per-account nonce ordering.** `Pending` sorts by fee desc, timestamp asc,
  non-stable (`core/mempool/mempool.go:207-235`); dependent txs from one sender land in arbitrary
  order. ⇒ the relayer's buy must be **one atomic module action**, and `Pending` should never
  emit nonce n+1 before n.
- **`DispatchModuleCall` receives no block height or time** (`vm/module_router.go:55`) although
  `execCall` has both (`executor.go:526`) and hands them to the bytecode VM (`:547-550`). Block
  time is `b.Header.Timestamp` on apply and stamped once per block on produce (`:647`) — never
  `time.Now()` inside a module.
- **Module calls are unmetered** (`executor.go:527-534` returns before any gas accounting;
  `/estimate` hardcodes 0). Every loop in a module needs an explicit cap.
- **Only QRC-20 emits events**; the DEX emits none. Copy the `X / XWithEvents / xWithEvents(receipt)`
  triple (`vm/qrc20.go:236-289`). `DataString` appends raw bytes with no length prefix
  (`vm/events.go:152-155`) — strings go last or into a topic.
- **QRC-1155 `Mint` has no access control** and `DeployQRC1155` writes no owner slot
  (`vm/qrc1155.go:84-106, 260-282`). Never expose a generic `mint` action without adding one.
- **`Permit` bug confirmed verbatim** (`vm/qrc20.go:514-517` empty deadline block; `:527-533`
  caller-supplied key, no `DeriveAddress(pub)==owner`); `Permit` is unreachable today (no router
  case). `CtxPermit` has exactly one use in the repo; no client can sign a permit.
- **Adding a module touches six places**: `vm/<module>.go`, `DispatchModuleCall` switch
  (`vm/module_router.go:60-67`), `DispatchModuleReadCall` switch (`module_router_read.go:50-57`),
  the `PreflightModuleCall` allowlist (`:208-217`), optional `rpc/<module>_rpc.go`, and
  `cmd/node/main.go` registration. Lending shipped unreachable because it missed the router cases.
- **The `/idx` token index is not in this repo** — it is `splendor-infra/deploy/indexer/*.go`
  (Postgres; hardcoded per-module action table at `contract_interface.go:53-68`). Follow-on.
- **CI now exists** (`.github/workflows/ci.yml`, since `d10a7e6`/`ac4e3ae`): `go vet ./...`,
  `go test -count=1 ./...`, gofmt **scoped to changed files**, and a `wasm-drift` job that
  rebuilds the signer from source and diffs it against every committed copy and pin. **CI still
  does not run `-race`**; gofmt flags 38 files under go1.26 (project pins 1.25) — never run
  `make fmt` inside a feature commit, gofmt only what you touch.
- `Genesis.Validate()` refuses duplicate allocation addresses since `551d2f8` — test fixtures
  must allocate each address once.
- **Re-checked at `origin/main@065c77d`:** `9b902be` landed
  `core/forks` + `Genesis.Forks` hashing + `/status.forks` + a reserved `BlockHeader.QCRoot`
  (see D2 — reused, not duplicated); `5c4d266` registers an account key only after it verifies
  a signature (consistent with D3's registry-first rule); `b8e9eba` adds
  `rpc/route_registration_purity_test.go`, which any new route registrar (PR-6 `exchange_rpc`)
  must pass; `fc47a5e` makes `ExecuteBlock` credit fees to the validator like `ProduceBlock`
  (`executor.go:205-216`; `d626c64`'s message mentions it but its diff is `sqc` only) — PR-3's
  fee assertions assume that. Everything else since (P2P announce
  signing, SSRF dial checks, identity-key encryption, `sqc` uninstall, balance export) is off
  this plan's surface.

## Coordination constraints from the peer session (verified)

- Genesis file is re-read every boot (`cmd/node/main.go:718-720`) but effects apply once
  (`:477-485`); a follower with `--peer` and no `--genesis` fetches the document from the peer
  and **does not persist it** (`:1058-1085`). ⇒ the forks map must be persisted in the chain
  DB `meta:` namespace (`core/state/statedb.go:42`, same place as `meta:height`) at apply time
  on both paths, and read from there on every later boot.
- Zero x402 transactions exist on any tier today (peer verified) — no payer migration.
- The wallet hardcodes one `QUANTUM_CHAIN_ID` for all tiers
  (`wallet/src/quantum/mldsaSigner.ts:22`) — peer's change, out of scope here; do not assume
  the wallet works against testnet after the reset.
- Peer's chain-id work is on `main` (unpushed as of writing): `62d25a4` (chain id from genesis),
  `304ab96` (total_supply derived, `genesis/genesis.go` Validate only — no struct or hash change),
  `f15b69f` (WASM `sqSetChainID` via new `types.SetClientChainID`; `/healthz` fixed; DEPLOY.md
  txindex wipe). **Branch this work off `f15b69f` or later.**
- **Superseded (peer `d10a7e6`): the peer rebuilt the WASM themselves.** Digest is now
  `77278ad14c6bd83986e936c7aae83c4c24eb405dfb5f585e3f92aef5a2bcb768` at all 7 sites
  (`EXEC_SHA256` unchanged), golden vector re-verified. `main` has since moved to `7a861d9`
  (duplicate-allocation guard, CI gofmt scoping, docs) — **branch off `7a861d9` or later.**
  PR-8 therefore carries an *incremental* rebuild adding only the new exports: `bash
  sdk/bridge/wasm/build.sh` → `(cd sdk/bridge && npm run sync:config)` → `npm --prefix
  wallet-extension run copy:wasm` → hand-edit `WASM_SHA256` in `wallet-extension/background.js`.
  Record the Go toolchain in the commit body.
- **There is now CI** (`.github/workflows/ci.yml`, peer). Its `wasm-drift` job rebuilds from
  source and diffs against every committed copy and pin — `check-wasm-dist.mjs` cannot catch
  drift because binary and pin agree during one. Any PR touching bytes linked into the WASM
  (PR-8; and PR-1/PR-6a must prove they did not) must carry the sequence above or CI fails. The "no CI" caveats in Verification are
  now stale; treat CI as the gate and keep the per-PR command list as the `-race` supplement.
- `d10a7e6` also added `rpc/tls.go PQTLSConfigWithPolicy` + `--tls-require-hybrid` and hardened
  systemd units in `install.sh` — not on this plan's surface, rebase only.
- `main` is pushed at `f15b69f`. Since `304ab96`, `Genesis.Validate()` derives an empty
  `total_supply` from the allocations (mutating the receiver) and refuses a present-but-wrong one:
  **hand-built `Genesis` fixtures must use `TotalSupply: ""`**, and the order is always
  Validate → Hash. The forks map must be hashed into the same preimage or the doc pin will not
  distinguish two documents that differ only in forks.
- Do **not** route permit signing through `types.SetChainID` (node-side, refuses a second call);
  the signer uses `types.ChainID` as already set by `SetClientChainID`.
- `sdk/bridge/signer/signer.go:26` `DerivationDomainMessage` keeps `chain: splendor-quantum-1`
  **permanently** — it names a derivation scheme, not a network; changing it would change every
  EVM-derived address. Add a comment saying so wherever the plan touches that file.

## Non-goals

- Renaming mainnet's chain id; any re-genesis mechanics or runbook (peer).
- Fee sponsorship / co-signed transactions; token-denominated `TxX402` (tx type 0x06).
- QSol compiler, EVM compatibility, `eth_*` JSON-RPC.
- An on-chain AMM/FPMM, order matching, or pricing of any kind in consensus (Augur's lesson:
  ≥700k gas per fill, abandoned twice). Pricing is an off-chain maker that signs orders.
- N-outcome conditions in v1 (`outcome_count` and the id layout leave room for ≤256 later).
- A plugin/WASM contract runtime; a rollup/validium (needs proof precompiles + DA).
- The off-chain ledger itself (gateway: book, matching plan, operator key, indexer) — follow-on.
- Client/wallet/MCP ports (follow-on).

## How this plan was produced

Read-only exploration (5 code maps + a critic, 323 tool uses) → a three-lens design panel
(consensus-safety, sequencing, protocol-contract) → a comparator that resolved 15 disagreements
against the code → an architecture research pass (three web sweeps) → a consensus-safety
review of the settlement module. Every decision below cites the line that forced it.

**What the research established (sources in the session transcript):**
- *Plugins by transaction* exist in two shapes. Substrate stores the whole runtime as a WASM
  blob (a rewrite for us). CosmWasm is the bolt-on: a metered WASM VM module where contracts
  *return* messages the chain dispatches — ~3 years to 1.0, still yearly gas/determinism
  advisories (a 2024 10× mispricing caused consensus failures via stale compiled caches).
  Hardened v1 for a small team ≈ 6–12 months. Our `core/forks` is Cosmos `x/upgrade`.
- *Every surviving market keeps matching off the state machine and puts only authorization +
  settlement on it.* Polymarket: operator book, immutable Exchange settles only what users
  signed; users trust the operator for liveness/ordering, never custody. Augur: book in
  consensus, ≥700k gas per fill, abandoned twice. dYdX v4: in-validator matching buys liveness
  at the cost of determinism + MEV policing. Hyperliquid: native engine upgraded by signed
  binary at a height (= a fork schedule).
- *Off-chain ledger ladder:* custodial (days) → anchored roots (weeks; proves existence, not
  correctness) → **escrow/intent module (first non-custodial rung; one native module, the
  permit verifier generalised)** → validium/rollup (proof precompiles + DA) → app-chain (on a
  contract-less L1 its bridge is a multisig).
- Two spec-level catches verified by hand: `vm/dex.go:559-571 sqrtBigInt(2) == 2`;
  `fpmm.ts:181-183` floors the *net* while `PredictionMarket.sol:427-428` floors the *fee*.

## Design (resolved)

### D1. `vm.ModuleContext` — one plumbing change, made once
`DispatchModuleCall(stateDB, ctx vm.ModuleContext, data, receipt)` with
`ModuleContext{Caller qcrypto.Address; Nonce, Height uint64; Time int64; Forks forks.Schedule}`.
`execCall` already holds `blockHeight/blockTime` (`core/block/executor.go:526`) and hands them
to the bytecode VM (`:547-550`); the module branch (`:527-534`) now passes them too. Producer
stamps block time once (`:647`), followers use `b.Header.Timestamp` — modules never call
`time.Now()`. Non-test call sites: `executor.go:533`, `rpc/call_rpc_test.go:46`, vm tests.
Reads get an advisory, nil-safe `ReadContext{Height, Time}` from `executor.LatestBlock()`
(zero when the executor is nil — `rpc/call_rpc_test.go:32` proves that contract must hold).

### D2. Fork schedule — reuse `core/forks` (landed in `9b902be`), wire it into consensus
Already on `main`: `core/forks.Schedule map[string]uint64`; `Active(name, h)` with **absent or
0 ⇒ never** (genesis is height 0, so 0 cannot mean "from genesis"); `Validate()` **refuses
unknown names** at load — `loadForkSchedule` (`cmd/node/main.go:1195`) is fatal on one;
`Known = [multi_validator]`; `Genesis.Forks` hashed only when non-empty (`genesis/genesis.go`,
tested in `genesis/forks_test.go`); `/status` reports it when non-empty (`rpc/server.go`).
**Not there:** the executor never sees the schedule (`grep forks core/block/` is empty) and a
follower started without `--genesis` gets `nil` (`main.go:1195-1197`). This plan therefore:
- Four names, snake_case like `multi_validator`: `qrc20_v2`, `qrc1155_v1`, `exchange_v1`,
  `module_revert_v1`. **Each is added to `forks.Known` in the PR that implements its gate,
  never earlier** (PR-3 `module_revert_v1`, PR-4 `qrc20_v2`, PR-5 `qrc1155_v1`, PR-6
  `exchange_v1`) — adding a name early would let a document schedule a rule the binary silently
  does not apply, the exact failure `Validate` exists to prevent. Tier documents schedule the
  names their image implements at **height 1**
  (the first block under the new rules): devnet/testnet
  `{"qrc20_v2":1,"qrc1155_v1":1,"exchange_v1":1,"module_revert_v1":1}`; **mainnet omits
  `exchange_v1`** (real-money gate).
- `Executor.WithForks(forks.Schedule)` / `SetForks` (under `e.mu`) / `Forks()`;
  `vm.ModuleContext.Forks forks.Schedule`; gates call `ctx.Forks.Active(name, ctx.Height)`.
  Wired from the existing load at `main.go:457`, which already runs before `ReconcileState`,
  so crash replay is gated.
- Persistence on **both** apply sites — `applyLocalGenesis` (the production path: `install.sh`
  passes `--genesis` to validator *and* RPC units) and `bootstrapGenesisFromPeer`: write
  `g.Forks` to the state LevelDB under `meta:forks` (`PutMeta/GetMeta`, off-root next to
  `meta:height`, `core/state/statedb.go:43-44`; `loadLeaves` skips `meta:` at `:181-189`) and
  call `executor.SetForks` — both **before** `ApplyToState`, so the 1 s syncer can never apply a
  block under a nil schedule. `loadForkSchedule` becomes: read `meta:forks` first; if a stored
  map exists, a missing, unreadable, or differing `--genesis` file is **fatal** (today an
  unreadable file silently yields nil, `main.go:1199-1204`); if no stored map exists on a
  non-empty chain and `--genesis` is set but unreadable, fatal; only an empty chain proceeds to
  the apply path. The stored map is authoritative for a running chain; the `--genesis-doc-hash`
  pin protects the document at apply time, so the two never conflict.
- The RPC layer never caches the schedule: `rpc.Config.Forks` / `s.forks` are removed
  (`rpc/server.go:138`, `cmd/node/main.go:804`); `/status.forks`, the `/tx/submit` admission
  preflight and `/estimate` read `executor.Forks()` (a copy under `e.mu`) at request time, so a
  schedule installed by `SetForks` after `rpc.New` is visible without a restart.
- Unknown scheduled name ⇒ refused at load (peer's rule, stricter than halting at the height:
  an un-upgraded node cannot start on a document it does not understand). Consequence: a binary
  that knows a name must be deployed everywhere **before** a document scheduling it is used.
- Gates are evaluated in `vm` dispatch (via `ctx.Forks/ctx.Height`) and in `execCall` — the
  code path producer and follower share. **Never `PreTxHook`** (`RunPreTx` is reached only from
  `executeTxWithReceipt`, `executor.go:373-377`; `ProduceBlock` calls `executeTx` directly).
  Reads and `/estimate` are not gated; `PreflightModuleCall` gains optional `(forks, height)`
  so `/estimate` can *say* "not active".
- Even bug-fix behaviour changes to existing actions (the `IsContract` guard on `approve`) are
  gated under `qrc20_v2`, so the mechanism stays honest for future live upgrades.
- Deferred to a follow-on: `--override-forks` for scheduling a fork on a *running* chain. The
  peer's model treats genesis as immutable and every fork in this plan lands at genesis.

### D3. Permit — fix, then bind the chain id, then route
- `core/types/permit.go` (leaf package; the signer/WASM cannot import `vm` → `core/state` →
  leveldb): `PermitDigest(chainID, contract, owner, spender, amount, nonce, deadline)
  ([32]byte, error)` = `SHA3-256("qrc20:permit:v2:" ‖ u16be(len(chainID)) ‖ chainID ‖
  contract[32] ‖ owner[32] ‖ spender[32] ‖ amount[32 BE] ‖ nonce[8 BE] ‖ deadline[8 BE])`.
  Errors (never panics) on nil/negative/≥2²⁵⁶ amount, deadline < 0, and
  `ValidateChainID(chainID) != nil` (the u16 length prefix must never wrap). `vm.PermitMessage` becomes
  a wrapper passing `types.ChainID`. **Why bind the chain id:** token addresses are
  `SHA3(deployer‖nonce‖"qrc20")` (`vm/qrc20.go:87-93`) and the same six tokens are replayed by
  the same deployer on every tier ⇒ identical addresses ⇒ a devnet permit would be byte-valid on
  mainnet. `CtxPermit` has exactly one use in the repo and no client signs permits, so the layout
  change is free now and impossible later.
- `Permit(owner, spender, amount, deadline, sig, ownerPubKey, blockTime, receipt)` in order:
  amount bound → `deadline > 0 && blockTime <= deadline` (**deadline == 0 is rejected**, no max
  horizon) → key: registry-first via `StateDB.GetPublicKey` (`statedb.go:297-310`, the x402
  precedent at `x402.go:184`); on `ErrNoPublicKey` accept `ownerPubKey` **iff
  `qcrypto.DeriveAddress(pub) == owner`** (`core/crypto/mldsa.go:193`; the identical guard is
  used at `executor.go:441`, `mempool.go:158`, `consensus/bft.go:152`) → `Verify(CtxPermit)` →
  nonce++ → `approveWithEvents` (Approval log). Not pause-gated (parity with `approve`,
  `qrc20.go:302-316`); the subsequent `transfer_from` is. Permit never writes the registry.
- Wire: `signature` / `owner_pubkey` are Go `[]byte` ⇒ std base64 (matches
  `types.Transaction.PublicKey/Signature`, `x402.PaymentPayload.Signature`, and the WASM's own
  output). Amounts/ids are decimal strings via a new strict `parseUint256` (< 2²⁵⁶); `deadline`,
  `end_time` are JSON int64. New actions never use `parseBigOrZero`.

### D4. Module-call failure semantics (`module_revert_v1`)
In `execCall`'s module branch, **after** the fee debit/`IncrNonce`/`SetAccount`: if the fork is
inactive → legacy (return the error; tx dropped). Else `inner := Snapshot()`; dispatch under a
`recover()` that yields the deterministic soft error `"module panic"`; classify:
- **HARD** (`vm.HardModuleError`): unparseable envelope, unknown module, unknown action,
  fork-inactive. `RevertToSnapshot(inner)` and return the error — still dropped/rejected, so a
  node lacking a fork explicitly rejects a gated block instead of diverging on state root.
- **SOFT** (everything an action returns): `RevertToSnapshot(inner)`, clear `receipt.Logs` and
  `receipt.ContractAddress`, `receipt.SetFailed(err.Error())`, return nil — included, fee kept,
  nonce consumed, mirroring the bytecode branch (`executor.go:555-564`). Nested snapshots are
  supported by design (`core/state/snapshot.go:68-69`).
`KnownModuleActions` carries **two sets per module — writes and reads**. The admission preflight
(`/tx/submit`, and `/estimate` for a `TxCall`) validates against the **write set only** (a read
action inside a signed `TxCall` is refused at admission with `"<module>: %q is a read action —
use /call"`; today's single allowlist at `vm/module_router_read.go:208-217` mixes both, and a
`balance_of` TxCall would pass preflight, hit the HARD `unknown action` default, and jam the
sender forever); `DispatchModuleReadCall` and its write-rejection list use the read set. With
that split every HARD class (unparseable envelope, unknown module, unknown *write* action,
fork-inactive) is statically detectable at admission. `/tx/submit` also rejects `Amount != 0`
on router calls (the module branch returns before the value transfer at `executor.go:571-575`,
silently keeping the coins). Local, non-consensus. Owner decision to state explicitly: StateDB
I/O errors and panics inside a module are SOFT (failed receipt, deterministic text) unless the
implementer finds a case where a follower could not reproduce them — then HARD.
`mempool.Pending` sorts the **full pool** fee-desc/timestamp-asc with a tx-hash tiebreak, then
replaces the i-th occurrence of sender S with S's i-th lowest pending nonce (`byAddr` is already
nonce-sorted, `mempool.go:193-195`), **then** truncates to n — substituting after truncation
could emit nonce n+1 without n. Local only.

### D5. QRC-1155 — give it an owner before routing it
`slotMTOwner = Hash32("qrc1155:owner")`; `DeployQRC1155` writes the deployer and refuses
re-init; `Mint/MintBatch(caller, …)` gain `requireOwner`; unexported `mintUnchecked/burnUnchecked`
for in-package use; `MaxQRC1155Batch = 64` on every batch path; `TransferSingle`/`TransferBatch`
events (mint = from zero, burn = to zero; batch data = `n ‖ ids ‖ values` as 32-byte words).
The exchange's position collection lives **at the exchange address with owner == exchange** —
no key derives to it, so router `qrc1155.mint` on it is unsatisfiable for every caller. An
unexported `initQRC1155At(stateDB, addr, owner, name, uriPrefix)` (holding the re-init guard)
is used by `DeployQRC1155` and by `exchange.deploy`. **Router `qrc1155.burn`/`burn_batch`/
`transfer`/`batch_transfer` refuse collections whose owner slot equals the collection address**
(module-managed: only the owning module mutates them via `mintUnchecked/burnUnchecked`) — today
`Burn(caller, from, …)` authorises `caller == from` (`qrc1155.go:304-311`, `:179-184`), which
would let any holder burn one leg of a complete set and strand the other leg's collateral. Every
transfer path also rejects `to == collection`.

### D6. Settlement module (`exchange`) — generic, Polymarket-shaped, no market logic

Spec sources: `BlockpeakExchange.sol` (`Order` :72-83, cancel/nonce :235-250, `_fillOrder`
:302-337, `matchMint/matchMerge` :338-420, `_validateOrder/_consume` :465-485) and
`PredictionMarket.sol` split/merge (:523-580) + redeem fee (:402-443). The module holds **no
user funds outside an atomic match**: collateral and positions stay in balances until an
operator-submitted settlement moves exactly what each party signed. Deviations from the EVM are
marked ⚠ and are deliberate.

- **Instance & conditions.** `deploy{name, uri_prefix, redeem_fee_bps? (default 0, ≤2000),
  fee_recipient? (default deployer; rejected if zero or == exchange)}` at
  `SHA3(deployer ‖ decimal(nonce) ‖ u64be(height) ‖ "exchange")` (⚠ height-bound so a
  re-genesis that keeps its chain id cannot **accidentally** resurrect old orders against a
  repeated address; a deliberate replay by the deployer is out of the threat model until the
  reset-time enumeration in Follow-ons exists);
  self-owned QRC-1155 collection at the same address (no key derives to it → router
  `qrc1155.mint` can never mint positions; `qrc1155.go:179-184` blocks transfers from it).
  `create_condition{exchange, collateral, resolver, end_time, outcome_count, question_hash[32]}`
  → sequential id; **owner-or-operator only**; `end_time > ctx.Time`; `resolver` non-zero;
  `outcome_count` must be **2** in v1; `collateral` must be `IsContract` and ≠ exchange; positions are ids `condition_id*256 + outcome`. Question text lives off-chain,
  keyed by hash.
- **Complete sets.** `split{exchange, condition, amount}` (caller pays DEX-style
  `Transfer(caller→exchange)`, mints YES+NO; blocked at `end_time` and after resolution) and
  `merge{…}` (burns a set, releases collateral; blocked **only** after resolution — Solidity
  `:556-576`). Both user-callable; balance-check before every burn (`qrc1155.go:326-331` floors
  supply at 0).
- **Orders.** Signed struct `{salt[32], maker, exchange, condition u64, outcome u8, side u8
  (0 BUY / 1 SELL), share_amount, price (1e6-scaled, in (0,1e6)), expiration (0 = never),
  nonce, recipient}` — ⚠ `recipient` is signed (the EVM `fillOrderFor` lets the
  operator pick it; here an unsigned recipient would let the operator redirect shares). Digest in
  the leaf package `core/types/order.go`: `OrderDigest(chainID, …)` =
  `SHA3("exchange:order:v1:" ‖ u16be(len(chainID)) ‖ chainID ‖ exchange[32] ‖ salt[32] ‖
  maker[32] ‖ condition u64 ‖ outcome u8 ‖ side u8 ‖ share_amount[32] ‖ price[32] ‖
  expiration u64 ‖ nonce u64 ‖ recipient[32])`, fixed-width like `PermitDigest`, computed from
  `params.exchange` and asserted equal to the order's; errors on invalid amounts and on
  `ValidateChainID(chainID) != nil`. `recipient` zero means "pay the maker" (the digest still
  commits to the zero bytes); `recipient == exchange` or `== vm.ModuleRouterAddress` is rejected
  at validation (funds sent to a keyless address are stranded and would break the solvency
  accounting). Signed under a **new context
  `CtxOrder = "SPLENDOR-QC-ORDER-v1"`** (`core/crypto/mldsa.go:42-62` + both tables in
  `mldsa_kat_test.go:118-128,150-153`; nothing else enumerates contexts — free now because every
  tier re-genesises). `signature` / `maker_pubkey` base64; key resolution registry-first with
  `DeriveAddress(pk)==maker` fallback (D3). **A valid signed order is the authorization** for the
  module to move exactly the settled amounts from the maker — no QRC-20 allowance, no 1155
  approval, no gas-paying pre-approval (⚠ the EVM relies on standing approvals).
- **Matching.** `match{exchange, taker_order, maker_orders[≤16], fill_shares[]}` accepted **only
  from an operator address** (module param `operators`, owner-managed; `set_operator` event).
  Kinds mirror the Solidity: COMPLEMENTARY (buy vs sell, same outcome), MINT (BUY YES + BUY NO,
  prices sum ≥ 1e6 → split), MERGE (SELL YES + SELL NO, sum ≤ 1e6 → merge). Per order, in
  consensus: price/outcome/side valid; not cancelled; `ctx.Time <= expiration` (0 = never);
  `nonce == maker nonce`; signature valid; `fill ≤ remaining`. Per match: same condition; sides/
  outcomes as required; crossing; `fill > 0`; **no duplicate order hash inside one match**;
  condition not resolved and `ctx.Time < end_time` (⚠ the EVM fills after resolution). Kind is
  computed **per (taker, maker_i) pair** from the four side/outcome values; mixed kinds in one
  `match` are allowed (a taker BUY YES may fill partly against SELL YES makers and partly against
  BUY NO makers); `Σ fill_shares[i]` is the taker's fill and must be ≤ the taker's remaining; the
  taker's `filled` advances by the sum, each maker's by its own `fill_shares[i]`; the taker's
  bound is checked per pair. Check order: operator gate → caps → state → ML-DSA (verifies are
  the expensive part). An order whose `filled[hash] > 0` is already authorised: its signature
  check is skipped and `signature`/`maker_pubkey` may be omitted from the wire object (no
  separate `verified` slot); cancel/nonce/expiry checks still run on every fill before that
  short-cut.
- **Pricing & rounding (⚠ the load-bearing deviation).** Settle **at the maker's signed price**;
  the taker pays/receives the complement. Round in favour of the maker's bound (maker SELL →
  `ceil(price·fill/1e6)`, maker BUY → `floor`). The taker's bound is checked with a **documented
  ≤1-unit rounding tolerance**: a taker BUY pays at most `ceil(p_t·fill/1e6)`, a taker SELL
  receives at least `floor(p_t·fill/1e6)`; reject the pair otherwise, and reject `cost == 0`.
  Why the tolerance: with a strict taker bound, an equal-price cross (maker SELL @333333, taker
  BUY @333333, fill 7 → cost `ceil(2.33)=3` > `2.33`) and an exactly-crossing MINT (sum ==
  1e6) are fillable only when `price·fill` is divisible by 1e6 — the primary flow would fail.
  Consequences: equal-price and sum-==-1e6 fills always fill at any `fill`; the operator's
  rounding power is bounded to < 1 smallest collateral unit per fill; no zero-cost dust fills
  (module calls are unmetered, so the EVM's gas deterrent does not exist); and **MINT/MERGE
  produce no surplus** — the EVM retains `yesCost+noCost−fill` (`:411`, `:415`) and exposes
  admin `sweep` (`:420-423`); here there is nothing to sweep and the identity is exact.
- **Operator powers** (consensus-bounded): reorder, delay, censor, choose which crossing pairs
  fill and who is labelled taker (price improvement always inside both bounds), self-trade with
  its own orders, cancel anyone's order (grief, no loss). **Cannot:** alter any field,
  overfill, fill outside a signed bound, move funds without a signed order, mint positions,
  redirect redemptions, resolve.
- **Cancel / nonce.** `cancel_order{order}` by maker, by operator, **or by anyone carrying a
  maker signature over `"exchange:cancel:v1:" ‖ order_hash` under `CtxOrder`** (⚠ a gasless
  maker must be able to cancel against a hostile operator — cancel is idempotent, so its digest
  needs only the order hash); `increment_nonce{exchange, maker, expected_nonce, signature?,
  maker_pubkey?}` by maker, or by anyone carrying a maker signature under `CtxOrder` over
  `IncrementNonceDigest = SHA3("exchange:nonce:v1:" ‖ u16be(len(chainID)) ‖ chainID ‖
  exchange[32] ‖ maker[32] ‖ expected_nonce u64)`; on-chain `expected_nonce == current nonce`
  so each signature is single-use (⚠ without the bound nonce a captured signature would
  bulk-cancel the maker's future orders every block). Events `OrderCancelled`, `NonceIncremented`.
- **Resolution & redeem.** `resolve{exchange, condition, outcome}` resolver-only, once;
  `redeem{…}` / `redeem_for{holder}` (anyone triggers, holder is paid); winners-only fee
  `floor(gross·bps/10000)`, `payout = gross − fee` (`PredictionMarket.sol:427-428`; exact
  `net + fee == gross`). `pause` blocks `match` and `split` only; `cancel_order`,
  `increment_nonce`, `merge`, `redeem` are never gated by the exchange (a paused *collateral
  token* still blocks merge/redeem). Owner-only `pause`, `unpause`, `set_redeem_fee_bps`,
  `set_fee_recipient` (non-zero, ≠ exchange), `set_operator`, `transfer_ownership` — ⚠ pause is
  `PAUSER_ROLE` in the Solidity (`:219-225`), never the hot operator key.
- **Fork coupling.** `exchange_v1` **requires** `module_revert_v1` scheduled at ≤ its height
  (`forks.Schedule.Validate`): a failed match under legacy drop semantics would jam the operator's
  nonce (`mempool.go:105-110`).
- **Solvency identity** (fuzzed after every op): pre-resolution `supply(YES_c) == supply(NO_c)
  == locked_c`; `balance(exchange, T) >= Σ locked_c` (anyone may donate via `qrc20.transfer`;
  the fuzz asserts the inequality and that the excess changes only by donation);
  post-resolution `locked_c == supply(win)`; after every redeem `paid + fee == burned`. The
  equality on supplies holds only because router `qrc1155.burn`/`transfer` refuse
  module-owned collections (D5); the fuzz includes a holder attempting a router burn of one leg.
- **Reads** (`/call`, mirrored 1:1 by GET routes; `RegisterExchangeRoutes(r, stateDB, head
  func() vm.ReadContext)` is registered in the purity test as a closure with a zero `head`):
  `exchange_info` → `{owner, operators[], paused, redeem_fee_bps, fee_recipient,
  condition_count}`, `condition_info`, `order_status{hash}` → `{filled, cancelled}`,
  `order_remaining{order}`, `order_hash{order}` (pure — pins the TS SHA3), `nonce{maker}`,
  `is_operator{addr}`, `position{condition, holder}`, `position_id` (pure), `simulate_match`
  (dry-run with head time; returns the same error string a real match would; nil-executor
  safe). GET: `/exchange/{addr}`, `/exchange/{addr}/condition/{id}`,
  `/exchange/{addr}/order/{hash}`, `/exchange/{addr}/nonce/{maker}`,
  `/exchange/{addr}/position/{condition}/{holder}`; `simulate_match`, `order_remaining` and
  `order_hash` are `/call`-only (they carry a body). Not fork-gated.
- **Wire.** 32-byte fields (`salt`, `question_hash`, order/cancel hashes) are lowercase hex
  without `0x`, exactly 64 chars, strict; amounts/prices/ids are decimal strings via
  `parseUint256`; `expiration`, `end_time` JSON int64; `signature`/`maker_pubkey` std base64.
- **Events** (topic[0] = SHA3-256 of the signature string verbatim, `sqaddress` type names, all
  non-indexed values as 32-byte words, never strings): `ConditionCreated(uint64,sqaddress,
  sqaddress,uint64,uint8,bytes32)`, `OrderFilled(bytes32,sqaddress,sqaddress,uint64,uint8,
  uint8,uint256,uint256)` (indexed: hash, maker, taker), `MintMatched(bytes32,bytes32,uint64,
  uint256)`, `MergeMatched(bytes32,bytes32,uint64,uint256)`, `OrderCancelled(bytes32,sqaddress)`,
  `NonceIncremented(sqaddress,uint64)`, `PositionSplit(sqaddress,uint64,uint256)`,
  `PositionsMerged(sqaddress,uint64,uint256)`, `Resolved(uint64,uint8)`,
  `Redeemed(sqaddress,uint64,uint256,uint256)`, `RedeemFeeCharged(sqaddress,uint256)`,
  `OperatorSet(sqaddress,bool)`, `RedeemFeeUpdated(uint256)`, `FeeRecipientUpdated(sqaddress)`;
  precomputed topic hex lands in `docs/modules/exchange.md`. QRC-1155 `TransferSingle` from the
  exchange address on every mint/burn.
- **DoS envelope.** 17 orders with pubkeys ≈ 125 KB — inside `MaxTxDataBytes` 128 KiB
  (`mempool.go:36`) by ~4 KB, so the gateway omits `maker_pubkey` for registered makers and
  omits signatures on already-filled orders; 17 ML-DSA verifies ≈ 2–5 ms; the 32 MiB
  `MaxBlockWireBytes` (base64 on the wire) admits ~185 full-size matches/block ≈ 3,200 verifies
  ≈ 1–2 s at a 2 s interval — the `filled > 0` short-cut and rejecting non-operator `match` at
  `/tx/submit` (local, RPC has state) keep it bounded. Error strings: stable prefix `"exchange <action>: "`.
- `KnownModuleActions` remains the single source of truth (dispatch cases, read rejection list,
  preflight) with a coverage test — the lending precedent must not recur.

### D6-shelved. On-chain FPMM market module (not built — kept as a specified option)
_Not scheduled. Retained verbatim below so an on-chain maker can be added later without
re-deriving it; under the chosen architecture an AMM is an off-chain maker that signs orders._
- **Addressing:** hub = `SHA3(deployer ‖ decimal(nonce) ‖ "market")` (nonce-derived like qrc20,
  no re-init guard needed; content-derived would need the DEX guard at `dex.go:115-117`).
  Sequential `market_id` per hub from 0. **`position_id = market_id*256 + outcome`** (uint8
  outcome; decodable by indexers without a lookup; keccak-free; fits N-outcome ≤ 256 later),
  advertised in `hub_info.position_id_scheme`.
- **Storage:** hub slots `market:hub:{owner,fee_bps,fee_recipient,market_count,name,uri_prefix}`;
  per-market per-field slots keyed by `u64be(id)`, with **per-outcome reserve slots**
  `market:reserve:<id>:<outcome>` and `outcome_count` (2 in v1) so N-outcome reuses the layout.
  Minted-per-side is `QRC1155.TotalSupply(position_id)` (already maintained,
  `qrc1155.go:273-276, 326-331`), not a second counter.
- **Math (`vm/market_math.go`, pure):** `ceilDiv`; **`Isqrt` via `new(big.Int).Sqrt`** (floor;
  pin `isqrt(2)=1`); `CalcBuyAmount` — `k=yes*no`; YES: `sharesOut = (yes+in) − ceilDiv(k, no+in)`;
  `CalcSellAmount` — `B = s+bought+other; amountOut = (B − isqrt(B² − 4·s·other)) / 2` (exact in
  big.Int; `amountOut == 0` rejected, audit M1); `PriceYes = no·1e18/(yes+no)`.
  **Redeem fee uses the Solidity formula** (`fee = floor(gross·bps/10000); payout = gross − fee`)
  because `net + fee == gross` is the solvency identity; `fpmm.ts netPayout` differs by one unit
  when `gross·bps % 10000 != 0` and the gateway must be told (follow-on).
  Caps: `seed ∈ [1, 2^120]`, `question ≤ 512 bytes`, `redeem_fee_bps ≤ 2000`, everything < 2²⁵⁶.
- **Actions:** `deploy_hub{name, uri_prefix, redeem_fee_bps?, fee_recipient?}` (defaults 0 /
  deployer; `markContract` + `receipt.ContractAddress`); `create_market{hub, question, end_time,
  resolver, collateral, seed}` (collateral must be `IsContract`; `end_time > ctx.Time`; seed pulled
  DEX-style `Transfer(caller→hub)`); `buy{hub, market, outcome, amount_in, min_shares_out,
  beneficiary?}` (caller pays; free beneficiary is harmless here — Solidity `buyFor` parity);
  **`buy_for{…, beneficiary, permit{owner, amount, deadline, signature, owner_pubkey?}}`** —
  permit **required**, `beneficiary == permit.owner`, `permit.amount == amount_in` (the permit
  signs no beneficiary and no market: a free beneficiary would let a relayer mint the agent's
  shares to itself; an allowance-mode would let anyone spend any owner's standing approval to
  the hub). Executes `Permit(owner→spender=hub)` → `TransferFrom(spender=hub, owner→hub)` →
  buy core → mint to owner, in one tx; `Trade.trader = tx sender, beneficiary = owner`
  (Solidity shape). `sell` (open until resolved — parity: `sell` checks only `resolved`);
  `resolve` (resolver-only, once, no time gate — parity); `redeem` / `redeem_for{holder}`;
  `reclaim_liquidity` (creator-only, once, returns the winning-side reserve);
  `set_redeem_fee_bps` / `set_fee_recipient` / `transfer_ownership` (owner-only).
  Trading closes at `end_time` for `buy`/`buy_for` (`ctx.Time >= end_time`).
- **Reads (`/call`, mirrored 1:1 by `GET /market/...` via shared `HubInfo`/`MarketInfo` structs,
  the `dex.go:597-608 PoolInfo` pattern):** `hub_info`, `market_info` (+ advisory `trading_open`
  from `ReadContext`), `markets{offset, limit≤100}`, `quote_buy`, `quote_sell`, `price`,
  `position{holder}`, `position_id` (pure), **`simulate_buy_for`** (dry-run of permit key/
  signature/nonce/deadline against head time + balance + quote → `{valid, reason, shares_out…}`;
  restores the gateway's pre-flight belt that `/estimate` cannot provide).
- **Events** (topic[0] = SHA3-256 of the signature, `sqaddress` type names, all data as 32-byte
  words; **question last** and preceded by `SHA3(question)` because `DataString` appends raw
  bytes, `vm/events.go:152-155`): `MarketCreated`, `Trade(marketId idx, trader idx, beneficiary
  idx; outcome, is_buy, amount, shares, yes_reserve, no_reserve)`, `Resolved`, `Redeemed`,
  `RedeemFeeCharged`, `LiquidityReclaimed`, `RedeemFeeUpdated`, `FeeRecipientUpdated`;
  QRC-1155 `TransferSingle` from the same hub address on every mint/burn.
- **Error strings** are client-visible under D4: stable prefix `"<module> <action>: "`, free suffix.
- `KnownModuleActions` becomes one package-level source of truth shared by `PreflightModuleCall`,
  the read-side write-rejection list, and the dispatch switches, with a test asserting every
  `case` is listed — the lending precedent (`vm/lending.go` shipped unreachable) must not recur.

### D7. x402 fresh payer (RPC-only)
`PaymentPayload.PublicKey []byte json:"public_key,omitempty"` **outside** `payloadBody`
(`x402.go:70-79`) so hashes/signatures are unchanged. `Verify` step 7: registry-first; on
`ErrNoPublicKey` accept the supplied key iff `len == PublicKeySize && DeriveAddress(pk) == From`;
a registered key always wins. `execX402` untouched. Signer's `SignX402` attaches the key (PR-8).

### D8. Signer + WASM
`signer.PermitParams{Token, Spender, Amount string; Nonce uint64; Deadline int64}`;
`DerivedWallet.SignPermit` → JSON `{owner, token, spender, amount, nonce, deadline, chain_id,
digest_hex, signature (b64), owner_pubkey (b64)}`. Likewise `OrderParams` + `SignOrder`
(digest from `core/types/order.go`, signed under `CtxOrder`) → the exact JSON object
`match.taker_order` / `match.maker_orders[]` accept, and a `SignCancel` for the signed cancel.
Uses `types.PermitDigest(types.ChainID, …)` as set by the peer's `SetClientChainID`; never calls
`SetChainID`. `signer.ModuleRouterAddress()` recomputes `SHA3("splendor:module-router:v1")`
locally (test asserts equality with `vm.ModuleRouterAddress`). WASM exports `sqSignPermit`,
`sqSignOrder`, `sqSignCancel` and `sqGetModuleRouterAddress`; fix the wrong literal at `wallet-extension/background.js:293` to
`SQ:b07043f4ef749951893985d66494d0f4e44c8812053361c07ea24bc70a444dcd`. Add the permanent
comment on `signer.go:26` (`DerivationDomainMessage` names a derivation scheme, not a network).

## Work items — ten PRs in dependency order

Branch off `origin/main` (≥ `065c77d`; the local checkout lags by commits touching only
`install.sh`/`sqc`). Each PR is independently testable and never leaves a half-wired
module. Reuse: `newTestState/addr/moduleData/mustBig` (`vm/module_router_test.go:13-42,156`),
`newTestExecutor/fund/signedTransfer/testFee` (`core/block/executor_security_test.go:57-104` —
the only harness with real block time), `moduleEnvelope` (`rpc/call_rpc_test.go:19`),
`liveTierGenesis`/`honestGenesis`/`saveGenesis`/`newBootstrapNode`
(`genesis/genesis_test.go:14`, `cmd/node/genesis_bootstrap_test.go:28+`), `markContract`
(`vm/module_router.go:280`), the `X/XWithEvents/xWithEvents(receipt)` triple (`vm/qrc20.go:236-289`),
`EventBuilder` (`vm/events.go:104-170`), `requireOwner` (`vm/qrc20.go:171`).

**PR-1 `fix(qrc20)`: Permit hardening + `PermitDigest` v2 + `ModuleContext` plumbing** — S.
Files: `core/types/permit.go` (+`_test`), `vm/qrc20.go`, `vm/module_router.go` (`ModuleContext`,
`parseUint256`), `vm/module_router_read.go` (`ReadContext{Height uint64; Time int64}`;
`DispatchModuleReadCall(stateDB, rctx, data)`), `core/block/executor.go:533`,
`rpc/call_rpc.go:95` (fill `ReadContext` from `executor.LatestBlock()`, zero when the executor
is nil — `rpc/call_rpc_test.go:32` keeps passing), the test call sites, new
`vm/qrc20_permit_test.go`. No `permit` router action yet. Before opening: `bash
sdk/bridge/wasm/build.sh && shasum -a 256 sdk/bridge/wasm/dist/sqc_signer.wasm` must still print
`77278ad1…` (then `git checkout -- sdk/bridge/wasm/dist`) — `core/types` is linked into the
signer and `wasm-drift` runs on every PR; if the digest moves, fold the change into PR-8. Tests: digest golden (fixed vector,
reused by PR-8), chain-id moves the digest, 2²⁵⁶ errors not panics; forged-key rejected
(`does not derive`), registry path with nil key, expired, deadline 0, replay, tampered fields,
works-while-paused, `transfer_from` blocked while paused; vm-level
`TestPermitDeadlineUsesBlockTime` (explicit `blockTime`; `== deadline` accepted, `+1` rejected —
the executor-level variant needs the router action and lives in PR-4). **Should be in the
peer's re-genesis image.**

**PR-2 `feat(forks)`: wire `core/forks` into consensus** — S (was M; the schedule type,
genesis hashing, unknown-name refusal and `/status` landed in `9b902be`). Files:
`core/forks/forks.go` (`Validate` gains the cross-name rule `exchange_v1` ⇒ `module_revert_v1`
scheduled at ≤ its height; the names themselves are added to `Known` by PR-3/4/5/6),
`core/block/executor.go` (`forks` field,
`WithForks/SetForks/Forks`; +`forks_test.go`: `Active` matrix nil/absent/0/at/above),
`vm/module_router.go` (`ModuleContext.Forks forks.Schedule`), `cmd/node/main.go:457`
(`executor.WithForks(forkSchedule)`), `rpc/server.go` (drop `Config.Forks`/`s.forks`; `/status`
calls `executor.Forks()` at request time) + `cmd/node/main.go:804` (drop the field),
`core/state/statedb.go` (`PutMeta/GetMeta` for `meta:forks`, +`_test`: root unchanged, sandbox
refuses), `cmd/node/main.go` (both apply sites persist `meta:forks` + `SetForks` **before**
`ApplyToState`; `loadForkSchedule` per D2), `genesis/forks_test.go` (rows for the four names;
**fixtures use `TotalSupply: ""` and unique allocation addresses**),
`cmd/node/genesis_bootstrap_test.go` (persisted on local apply and on peer bootstrap; follower
restart without a file keeps the schedule; validator restart with a corrupt `genesis.json` on a
non-empty chain refuses to boot; differing file refused), `rpc` test (construct with no forks,
`SetForks` on the live executor, `/status.forks` reports it without restart), DEPLOY.md (the
four names, the height-1 convention, mainnet omission). **Must be in the re-genesis image, and the documents must be hashed with a
PR-2 binary — an older binary refuses them outright because it does not know the names.**

**PR-3 `feat(executor)`: `module_revert_v1` + mempool ordering + admission preflight** — S-M.
Files: `vm/errors.go` (**new**: `HardModuleError`, `ErrForkInactive`, `requireFork(ctx, name)
error`), `vm/module_router.go` (wrap the three hard sites), `core/forks/forks.go` (`Known` +=
`module_revert_v1`), `vm/module_router_read.go` (**`KnownModuleActions` shared var with separate
write/read sets + coverage test that every dispatch `case` is listed; `PreflightModuleCall(data,
forks forks.Schedule, height uint64)`** — nil/0 = no gating), `rpc/call_rpc.go:235` (call-site
update), `core/block/executor.go` (`execCall` per D4), `core/mempool/mempool.go` (`Pending` per
D4; `Add` rejects `Amount != 0` to the router), `rpc/server.go` (`/tx/submit` preflight against
the write set with `executor.Forks()` and `head+1`), fix `vm/module_router.go:53-54`,
`vm/module_router_read.go:11-14`, `rpc/call_rpc.go:91-94`. Tests: `core/block/module_revert_test.go` (soft failure included, fee
charged, nonce 2, validator credited, follower agrees; hard errors still dropped; legacy without
fork unchanged; follower-without-fork rejects with "unappliable tx"; panic → failed receipt;
crash-replay parity via `ReconcileState`), `fee_conservation_test.go` failed-module case,
`mempool_security_test.go` (nonce order with equal fee/timestamp ×50 runs, fee priority across
senders, truncation with the sender's lowest nonce at the lowest fee and n smaller than its
pending count, determinism); a signed `TxCall` carrying a read action (`balance_of`) is refused
at `/tx/submit` and never enters the mempool while the same envelope succeeds on `/call`.

**PR-4 `feat(qrc20)`: route the rest behind `qrc20_v2`** — M. Files: `vm/qrc20.go`
(`*WithEvents` for Increase/DecreaseAllowance/BurnFrom/TransferOwnership/Pause/Unpause;
`TransferFromWithEvents:361` decrements via `approveWithEvents`), `vm/events.go`
(`Paused/Unpaused/OwnershipTransferred`), `vm/module_router.go` (actions `permit, transfer_from,
increase_allowance, decrease_allowance, burn_from, transfer_ownership, pause, unpause`; gated
`requireToken` IsContract on all non-deploy writes), `vm/module_router_read.go` (reads
`permit_nonce, paused, owner`; add the new names to the `KnownModuleActions` write/read sets),
`core/forks/forks.go` (`Known` += `qrc20_v2`), `docs/modules/qrc20-permit.md` (new directory)
(layout, base64 rule, golden vector, relayer note). Tests: gated-off returns hard error and
writes nothing; permit via router end-to-end with a real keypair; transfer_from + logs; bad
base64; approve-on-bogus-token rejected post-fork / allowed pre-fork; executor-level
`TestPermitDeadlineUsesHeaderTime` via `newTestExecutor` (the only harness with real block time:
a permit whose deadline equals the header timestamp is accepted by producer and follower).

**PR-5 `feat(qrc1155)`: owner slot, events, caps, routing behind `qrc1155_v1`** — M. Files:
`vm/qrc1155.go` per D5, `vm/events.go` (`TransferSingle/TransferBatch`), router actions
`deploy, mint, mint_batch, transfer, batch_transfer, set_approval_for_all, burn, burn_batch,
transfer_ownership` (param name `collection`), reads `balance_of, balance_of_batch(≤64),
is_approved_for_all, uri, total_supply, token_info`; update `vm/inflation_test.go:104`; new
`vm/qrc1155_test.go` (owner-only mint, batch cap 65 rejected, event layouts, re-init guard,
id 2²⁵⁶ no panic, `TestModuleOwnedCollectionCannotBeMintedViaRouter`,
`TestModuleOwnedCollectionRejectsRouterBurn`, `TestTransferToCollectionRejected`);
`core/forks/forks.go` (`Known` += `qrc1155_v1`); update `vm/inflation_test.go:112`
(`tok.Mint(holder, …)` is what breaks when `Mint` gains a caller).

**PR-6a `feat(crypto,types)`: order-signing primitives** — S, lands right after PR-1 so the
client-signing surface (PR-8) is not blocked behind the module. `core/crypto/mldsa.go`
`CtxOrder = "SPLENDOR-QC-ORDER-v1"` appended **after** `CtxMultisig` (`:61`, never mid-block) +
both KAT tables (`mldsa_kat_test.go:118-128, :149-152`), `core/types/order.go` `OrderDigest` /
`CancelDigest` / `IncrementNonceDigest` (+`_test`: golden digest, every field flips it,
≥2²⁵⁶/negative/invalid chain id → error not panic). Same WASM-digest check as PR-1.

**PR-6 `feat(exchange)`: the settlement module, two commits** — L; depends on PR-3, PR-4, PR-5,
PR-6a. Adds `exchange_v1` to `forks.Known` in commit 2.
*Commit 1 — module:* `vm/exchange.go` per D6 (instance, conditions, split/merge, order
validation with the `filled > 0` short-cut, per-pair match kinds, rounding rule with the ≤1-unit
taker tolerance, cancel/nonce incl. the
signed variants, resolve/redeem, owner/operator params), `vm/events.go` topics,
`vm/exchange_test.go` (adversarial first: forged key; order signed for exchange X presented at
Y; signed under `CtxPermit` rejected; tampered price; non-operator rejected **before** any
verify; overfill across two txs; duplicate hash in one match; zero-cost fill; rounding property
over 10k random price/fill pairs — neither side outside its signed bound; cancelled /
nonce-bumped / expired (`== expiration` ok, `+1` fails); match and split rejected once resolved
or at `end_time`, merge allowed until resolved; insufficient maker balance / paused collateral →
no residue, `filled` unchanged; taker/maker relabelling stays inside both bounds; router
`qrc1155.mint`/`transfer` on the exchange address rejected; `redeem_for` pays the holder, loser
errors, fee golden 190909090 @ 250 bps → 4772727 / 186136363; caps: 17 makers, bad
`question_hash`, `outcome_count ≠ 2`, fill/maker length mismatch), `vm/exchange_invariant_test.go`
(seeded 500-op fuzz asserting the D6 solvency identity after every op; full drain to exchange
balance 0).
*Commit 2 — wiring:* `dispatchExchange/dispatchExchangeRead` incl. `simulate_match`,
`KnownModuleActions` write/read sets, `core/forks/forks.go` (`Known` += `exchange_v1`),
`rpc/exchange_rpc.go` (+`_test`: REST deep-equals `/call`; `RegisterExchangeRoutes(r, stateDB,
head func() vm.ReadContext)` registered in `rpc/route_registration_purity_test.go` as a closure
with a zero `head`), `/tx/submit` rejects non-operator `match` (local), `docs/modules/` (new
directory), `cmd/node/main.go` registration next to `RegisterDEXRoutes`,
`core/block/exchange_parity_test.go` (producer/follower roots agree across the lifecycle incl. a
failed match under `module_revert_v1` and an expiration equal to the header timestamp; follower
without `exchange_v1` rejects with "unappliable tx"; `ReconcileState` replay),
`docs/modules/exchange.md` (every shape, digest layouts with golden vectors, event topics with
precomputed hex, id scheme, the operator flow, the rounding rule, error prefixes).

**PR-7 `fix(x402)`: optional `public_key`** — S, independent, **node-side only**. Files:
`x402/x402.go`, new `x402/x402_test.go` (fresh payer accepted; wrong key rejected; registered key
wins; hash excludes the field), `rpc/security_test.go` (never-seen payer verifies). The field is
`omitempty`, so old payloads are unchanged. The signer-side `SignX402` change (attach the key)
lives in PR-8 commit A — `sqSignX402` links `SignX402` into the WASM, so touching it here would
trip `wasm-drift` and force a second rebuild.

**PR-8 `feat(signer)`: `SignPermit`/`SignOrder`/`SignCancel`/`SignIncrementNonce`, the WASM
exports, goldens, extension literal, and the ONE WASM rebuild** — M; depends on PR-1 and PR-6a.
CI's `wasm-drift` job rebuilds the signer from source on every PR and fails if the committed
binaries or any pin differ, so source and rebuild must land in **one PR** (CI runs on the PR
head). *Commit A (source):* `sdk/bridge/signer/signer.go` (+ the permanent
`DerivationDomainMessage` comment; also the `SignX402` public-key attachment, moved here from
PR-7), `signer_test.go` (golden digests = PR-1's/PR-6a's; verifies under the right context and
fails under `CtxMessage`; honours `SetClientChainID` with `t.Cleanup` restoring
`types.ChainID` via `RestoreChainIDForTest`; round-trips through `vm.QRC20Token.Permit` — tests
may import `vm`), `router_address_test.go`, `sdk/bridge/wasm/main.go` (`sqSignPermit`,
`sqSignOrder`, `sqSignCancel`, `sqSignIncrementNonce`, `sqGetModuleRouterAddress` — the last
takes no arguments and returns the bare `SQ:…` string like `sqDomainMessage`, with a test that
the JS value equals `vm.ModuleRouterAddress.String()`), `sdk/bridge/testdata/golden-vectors.json`
(`permit`, `order` keys), `wallet-extension/background.js:291-295`.
*Commit B (binaries + pins):* `bash sdk/bridge/wasm/build.sh` → `(cd sdk/bridge && npm run
sync:config)` → `npm --prefix wallet-extension run copy:wasm` → hand-edit `WASM_SHA256` at
`wallet-extension/background.js:36` → `npm run prepublishOnly`; `shasum -a 256` of both `.wasm`
copies equals every pin; **record `go version` and `GOTOOLCHAIN=go1.25.0` in the commit body**;
re-verify seed `01`×32 → `SQ:307f4559…`. The rebuild is incremental over the committed
`77278ad1…` (which already contains `sqSetChainID`) and adds only the new exports.

**PR-10 `docs`: residual truth pass** — XS. `core/types/receipt.go:71`, `readme.md:677-680`
and `:982` (router table with fork names), DEPLOY.md endpoint quick reference
(`/exchange/...`), and a grep gate: `bypassing StateDB snapshots`, `exactly two modules`,
``dispatches `qrc20` and `dex` only``, `non-zero only for TxDeploy`, and the DEPLOY.md sentence
``chain_id` is `splendor-quantum-1`` return no hits (the `Fixed at compile time` heading stays —
it still covers three real constants).

## Verification

- Per PR: `go build ./... && go vet ./...` then `go test -count=1 ./vm/ ./core/... ./genesis/
  ./cmd/node/ ./rpc/ ./x402/ ./sdk/bridge/signer/`; **`go test -race -count=1 ./core/block/
  ./core/mempool/ ./core/state/ ./vm/`** for PR-2/3/6 (CI has no `-race`; `make test-race` runs
  the full tree, the scoped list is the fast path).
- CI runs `gofmt -l` on every changed `.go` file **in full**. Before opening a PR run
  `GOTOOLCHAIN=go1.25.0 gofmt -l <touched files>` (CI's toolchain). Expect whole-file alignment
  diffs the first time `core/block/executor.go`, `vm/qrc20.go`, `vm/qrc1155.go`, `vm/events.go`,
  `core/mempool/mempool.go`, `rpc/server.go`, `x402/x402.go`, `sdk/bridge/signer/signer.go`,
  `core/crypto/mldsa.go` are touched; put that reformat in a separate first commit of the PR so
  the review diff stays readable. Never `make fmt`.
- PR-2: `sqc-node genesis-hash <doc-without-forks>` is byte-identical before/after. Boot a
  devnet with the four forks at 1 → `/status.forks`; restart a peer-bootstrapped follower with
  **no** `--genesis` → same map; start it with a file whose map differs → refuses to boot.
- Fork matrix (vm): every new action with `Forks nil`, fork at `height+1`, fork at `1`.
- Producer/follower agreement: `ProduceBlock` on executor A, `ExecuteBlock` on a fresh B, for
  every new write action, incl. a failed `match` and a permit/order whose deadline/expiration
  equals the header timestamp. Then a follower **without** `exchange_v1` must reject with
  "unappliable tx", never "state root mismatch".
- Operator jam regression on a `module_revert_v1` node: a `match` whose maker lacks balance →
  receipt `failed`, operator `/nonce` advanced, next tx mined.
- Signature safety: sign a permit and an order on devnet, replay on testnet (different chain
  id) → invalid; replay an order against a second exchange instance → invalid; replay a
  consumed order → overfill.
- Rounding/solvency: `exchange_invariant_test` under `-race`; on devnet `balance_of(exchange)`
  equals Σ locked after a trading session; a 1-share fill at any price is rejected as zero-cost.
- Manual e2e (`cmd/auditprobe` pattern): deploy collateral → `exchange.deploy` →
  `create_condition` → two agents `sqSignOrder` (BUY YES @0.6, BUY NO @0.5) → operator `match`
  (MINT) via `sqSignCall` → receipt logs `[Transfer, Transfer, TransferSingle×2, MintMatched,
  OrderFilled×2]` → `POST /logs` by `TopicOrderFilled` decodes → `resolve` → `redeem_for` pays
  net of fee; then a maker-signed `cancel_order` submitted by a third party is honoured.
- WASM: after PR-8, `shasum -a 256` matches all pins; `npm run prepublishOnly`;
  `globalThis.sqGetModuleRouterAddress()` returns `SQ:b07043f4…444dcd` in the browser console.
- CI (`vet`, `test`, gofmt-on-changed, `wasm-drift`) is the merge gate. Because CI has no
  `-race`, each PR description pastes the local `go test -race` output for the packages above;
  the reviewer re-runs PR-6's invariant tests locally before merge. Any PR that changes bytes
  linked into the WASM (`sdk/bridge/signer`, `sdk/bridge/wasm`, and possibly `core/types` or
  `core/crypto`) must carry the rebuild in the same PR or `wasm-drift` fails.

## Coordination checklist with the peer session

1. The re-genesis image must be built from a commit that includes **every PR implementing a
   fork the tier document schedules**: PR-1..PR-6 for devnet/testnet (all four names),
   PR-1..PR-5 for mainnet (which omits `exchange_v1`). A document must never schedule a name
   whose behaviour the deployed binary does not implement — `forks.Known` is a *promise*, not a
   stub; a PR-2 image that "knows" the names would produce blocks that become retroactively
   invalid once the gates deploy. If the peer must re-genesis before PR-6, the documents
   schedule only `module_revert_v1` + `qrc20_v2` (PR-1..PR-4 image) and `qrc1155_v1` /
   `exchange_v1` wait for a later re-genesis or the deferred `--override-forks`. **Owner
   decision: which set the image will carry.**
2. The new genesis documents carry the `forks` object (devnet/testnet all four at **1** —
   `core/forks` treats 0 as never; mainnet omits `exchange_v1`) and their `--genesis-doc-hash`
   pins are computed with a PR-2 binary (an older one refuses the unknown names).
3. The single WASM rebuild lands inside PR-8; `sqSetChainID` is already in the committed binary.
4. `--genesis-hash` (block-0) pins are re-read after each reset; `--genesis-doc-hash` is stable.
5. Anything live (devnet smoke, operator key, deploys) goes through the workspace ops rules:
   read `infra/SERVICES.md` / `infra/SECRETS.md`, `source ~/.config/splendor/env.sh`, use
   `infra/bin/*` (per `splendor/CLAUDE.md`). The operator key is never a plain env var.

## Follow-ons (out of scope here, listed so nothing is lost)

- **The off-chain ledger (the product):** a quantum-native gateway that keeps the book, runs
  pricing as an operator maker that *signs orders* (an FPMM curve becomes a quoting strategy,
  not consensus code), plans matches with the D6 rounding and `cost > 0` rules
  (`orderbook.ts:217` today assumes floor), uses **head block time** not wall clock
  (`orderbook.ts:55`), omits `maker_pubkey` for registered makers, submits `match` with an
  operator key held per the CLAUDE.md gates (hosted runner + KMS), indexes events via
  `POST /logs` + `OnPostBlock` (`/ws` emits nothing on non-validators), and serves the
  Blockpeak-shaped API + MCP tools. Needs a TS SHA3 `hashOrder` pinned to the Go golden vector.
- **Reset-time enumeration:** if the exchange ever runs before a re-genesis, snapshot
  `filled/cancelled/nonce` — a reset wipes them (the height-bound address only protects
  *new* instances).
- **External indexer** (`splendor-infra/deploy/indexer/contract_interface.go:53-68`): add
  `qrc1155` and `exchange` to the per-module table and `contracts.kind`.
- **Wallet:** per-network `QUANTUM_CHAIN_ID` (peer), `QuantumSigner` beyond `signTransfer`
  (`sqSignOrder`/`sqSignPermit`), `blockpeakAgentService` off the EVM EOA path.
- **Gateway math (2692, unrelated to quantum):** `fpmm.ts netPayout` is off by one unit vs
  `PredictionMarket.sol` (floor net vs floor fee).
- **v1.1:** N-outcome conditions (id layout allows ≤256); a collateral allowlist per instance; a
  local fee multiplier for signature-heavy calldata (module calls are unmetered);
  replace-by-fee; `--override-forks` for live scheduling; `vm/dex.go sqrtBigInt(2)` bug; a
  separate `gofmt` commit under `go1.25.0`; the shelved on-chain FPMM (D6-shelved) if an
  in-consensus maker is ever wanted; a plugin runtime only if the app count justifies 6–12
  months.
