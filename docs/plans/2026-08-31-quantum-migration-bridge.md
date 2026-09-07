# SPLD/SPLV Quantum Migration Bridge — design

## Goal

Any holder of SPLD (native) or SPLV (ERC-20 `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689`) on
the legacy Splendor mainnet (EVM chain 2691) can **burn at least 1 token** there and receive
the same amount on the Splendor Quantum chain — SPLD as the native coin, SPLV as a QRC-20.
Three surfaces: (1) chain-side plumbing in this repo, (2) a dedicated migration dapp styled
like `splendor-quantum-explorer`, (3) a native flow in the Splendor Wallet app.

## Decisions

- **Nothing mints native SPLD on the quantum chain.** The chain has no post-genesis mint and
  `genesis.go:283-285` documents supply conservation as an invariant, pinned by
  `core/state/inflation_test.go` and `core/block/fee_conservation_test.go`. The bridge holds a
  **pre-funded escrow pool** at its own module-managed address (no ML-DSA key can derive to it)
  and *transfers* on claim. Ops funds the pool once with a plain `TxTransfer` from the genesis
  allocation. Quantum SPLD is pre-minted supply being released, not new supply.
- **SPLV on quantum is a QRC-20 owned by the bridge module instance** (module-managed owner,
  same pattern as the exchange-owned QRC-1155, `vm/exchange.go:269`). The module mints on
  claim via the in-package API; `max_supply` is set at deploy so cumulative mints can never
  exceed the legacy SPLV supply. No external key can ever satisfy `requireOwner`.
- **Burning on 2691 is forwarding to `0x…dEaD`.** The legacy chain has no protocol burn
  primitive (fees are 100% redistributed). The burn contract forwards native SPLD and SPLV
  straight to `0x000000000000000000000000000000000000dEaD` and emits one event; it holds no
  funds, ever, and is **non-upgradeable** — there is nothing an upgrade could steal and no
  "rescue" story to erode trust in the burn. This diverges from the `SplvToSpldSwap` UUPS
  precedent deliberately: that contract custodies a payout pool; this one custodies nothing.
- **Claims are operator-submitted, idempotent, atomic.** One `bridge.claim` module action per
  burn (mempool has no nonce ordering and no replace-by-fee, so multi-tx claims are out).
  Idempotency lives on-chain in a per-burn-id slot — a relayer crash/restart can never
  double-pay. Failures are SOFT under `module_revert_v1` so a bad claim can't jam the
  relayer's nonce.
- **Trust model: M-of-N attestations (no single point of trust).** A `claim` is authorised
  by `threshold` DISTINCT operator attestations over `types.BridgeClaimDigest` (under
  `CtxBridge`), not by the submitter alone. With `threshold ≥ 2` and operators who each watch
  the legacy chain with their own RPC, neither one compromised operator key nor one hostile
  legacy RPC can move funds — an attacker needs `threshold` keys AND `threshold` independent
  views to agree on a forged burn. `threshold = 1` is single-operator trust (the earlier
  design); `set_threshold` raises it once independent attestors exist. Operators are a bounded
  set (max 16); the owner sets policy (operators, threshold, pause) but has no claim bypass.
  Bounded, auditable via events, stoppable via `pause`. See `docs/modules/bridge.md` §Attestations.
- **Confirmation depth on 2691 before claiming: 120 blocks (~2 min at 1 s blocks),
  configurable.** Congress PoA has clique-style probabilistic finality only, and
  `infra/rpc-fleet` documents a real rewind incident. "Instant finality" marketing is not a
  bridge-safety argument.
- **Minimum burn: 1 whole token (10^18 units) per burn, both assets** — the product
  requirement "at least 1 required", enforced in the EVM contract.
- **Fork-gated `bridge_v1`**, scheduled per-tier in genesis like the other modules; requires
  `module_revert_v1` at the same or earlier height (jam-proofing).

## Approaches considered

1. **Escrow-pool module + operator relayer (chosen).** Fits every existing pattern in the
   repo (routed module, owner/operator, module-managed addresses, idempotency slots, events),
   conserves supply, keeps trust bounded and auditable.
2. **Genesis-configured mint authority.** Rejected: breaks the tested supply invariant, needs
   a `Genesis` struct + `Hash()` change and a `--genesis-doc-hash` re-pin on every tier, and
   hardcodes a key that can never rotate without re-genesis.
3. **Pure off-chain payout daemon (no node changes).** Faucet-style service holding a funded
   hot key, sending plain transfers + owner-key QRC-20 mints. Fastest to ship, rejected: no
   on-chain idempotency (double-pay on crash is only WAL-mitigated), the hot key holds the
   whole pool, and there is no on-chain burn→credit audit trail.

## Architecture

```
2691 (EVM, Congress PoA)                      splendor-quantum-* (this repo)
┌──────────────────────────┐                  ┌───────────────────────────────┐
│ QuantumMigrationBurn.sol │   eth_getLogs    │  bridge module (vm/bridge.go) │
│  burnSpld{value}(SQ)  ───┼──► Burned(id,…) ─┼─► claim(id,asset,amt,SQ,tx)   │
│  burnSplv(amt,SQ)        │   ≥120 confs     │   pool ──transfer──► SQ addr  │
│  → 0x…dEaD, holds nothing│                  │   splv ──mint(cap)─► SQ addr  │
└──────────────────────────┘                  │   claimed[id] slot(idempotent)│
        ▲                                     └───────────────▲───────────────┘
        │ MetaMask                                            │ TxCall (operator ML-DSA key)
┌───────┴──────────┐                          ┌───────────────┴───────────────┐
│ migration dapp   │◄────status polling───────│ bridge-relayer (Go daemon)    │
│ (Worker, explorer│      /bridge/... reads   │ cursor + confirm depth + retry│
│  design language)│                          └───────────────────────────────┘
└──────────────────┘
```

### 1. `QuantumMigrationBurn.sol` (chain 2691) — `contracts/legacy-burn/`

Solidity 0.8.28, `evm_version = london` (the node is pre-Merge geth ~1.10.x — never compile
with a Cancun default). Non-upgradeable. OpenZeppelin `AccessControl` (PAUSER_ROLE only) +
`Pausable` + `ReentrancyGuard`.

- `burnSpld(bytes32 quantumRecipient) payable` — requires `1e18 <= msg.value <= maxBurn`,
  `quantumRecipient != 0`, not paused. Forwards the full value to `0x…dEaD`.
- `burnSplv(uint256 amount, bytes32 quantumRecipient)` — requires `1e18 <= amount <= maxBurn`;
  `transferFrom(msg.sender, 0x…dEaD, amount)` with a `balanceOf(dEaD)` before/after check
  (`received == amount`, the swap contract's fee-on-transfer defence).
- **`maxBurn` (immutable) MUST equal the bridge module's `max_claim_amount`.** The module
  refuses to credit a claim above that cap, so a burn above it would be destroyed and never
  claimable — enforcing the same ceiling on the burn side makes an un-creditable burn
  structurally impossible; a larger migration is several sub-cap burns. Deploy the module
  first, read its cap, pass the identical value to the burn constructor. This closes the
  review's one money-loss finding (over-cap burn stranding).
- `event Burned(uint256 indexed id, address indexed sender, uint8 indexed asset,
  uint256 amount, bytes32 quantumRecipient)` — `id` is a monotonically increasing counter
  starting at 1; `asset` is 0 = SPLD, 1 = SPLV. `quantumRecipient` is the 32 raw bytes of the
  `SQ:` address (the hex after the prefix).
- No withdraw/rescue functions of any kind. No deadline (migration is open-ended; `pause` is
  the incident brake). SPLV address and min-burn are immutables set at construction.

Foundry project is self-contained here for review + CI; the deploy itself should be run from
`wallet/contracts` (the established 2691 Foundry home with the RPC/verify matrix) by copying
`src/` + `script/` there — noted in the contract README.

### 2. `bridge` module (this repo) — `vm/bridge.go`

Routed native module, modeled line-by-line on `vm/exchange.go`. All amounts are strict
decimal strings (`parseUint256` rules). All loops bounded. Module calls are unmetered — no
unbounded work.

Instance address: `SHA3-256(deployer ‖ ascii(decimal(nonce)) ‖ u64be(height) ‖ "bridge")` —
height included (exchange pattern) so tiers cannot collide. The SPLV token is deployed *by*
`bridge.deploy` at `SHA3-256(bridgeAddr ‖ "bridge:splv")` with QRC-20 owner = the bridge
instance address (module-managed; a new package-internal `initQRC20At` mirrors
`initQRC1155At`).

Write actions (via `TxCall` → `ModuleRouterAddress`, all SOFT-failing):

| action | auth | params |
|---|---|---|
| `deploy` | anyone (deployer becomes owner) | `evm_chain_id`, `evm_contract` (0x + 40 hex), `splv_name`, `splv_symbol`, `splv_max_supply`, `max_claim_amount` (both > 0 required) |
| `set_operator` | owner | `bridge`, `operator` (SQ:), `active` (explicit bool, exchange rule) |
| `pause` / `unpause` | owner | `bridge` |
| `transfer_ownership` | owner | `bridge`, `new_owner` (refuses contract/router targets, `requireOwnableTarget`) |
| `claim` | owner or operator | `bridge`, `id` (decimal, = EVM event id), `asset` (`"spld"`/`"splv"`), `amount`, `recipient` (SQ:), `evm_tx` (0x + 64 hex, audit trail only) |

`claim` semantics: refuse if paused, `amount == 0`, `amount > max_claim_amount`, or the
`SHA3("bridge:claimed:" ‖ bridgeAddr ‖ u64be(id))` slot is set. For `spld`: refuse if pool
balance at the bridge address < amount, else `stateDB.Transfer(bridge → recipient)`. For
`splv`: internal `MintWithEvents(bridge, recipient, amount)` — the QRC-20 `max_supply` cap
refuses over-mint. Then set the claimed slot (records claim height + amount hash) and bump
`claimed_total_{spld,splv}` accounting slots. Emits
`BridgeClaimed(id, asset, amount, recipient, evm_tx)`.

Pool funding is a plain `TxTransfer` to the bridge instance address — no action needed; no
key derives to that address, so only `claim` can ever move it. A `withdraw_pool` explicitly
does **not** exist in v1 (owner cannot pull the pool back; recovering an over-funded pool is
a fork-level decision, not a hot path).

Read actions (via `POST /call`): `bridge_info` (owner, paused, evm chain/contract, splv token
address, caps, claimed totals, pool balance, bounded operator list), `claim_status`
(`bridge`, `id` → claimed?, height). Plus REST mirrors in `rpc/bridge_rpc.go`:
`GET /bridge/{addr}` and `GET /bridge/{addr}/claim/{id}` (registered purely, `SimGuard`).

Wiring (the six places, per `docs/plans/2026-08-27` §): `vm/bridge.go`, `DispatchModuleCall`,
`DispatchModuleReadCall`, `KnownModuleActions`, `rpc/bridge_rpc.go`, `cmd/node/main.go`.
Fork: `bridge_v1` added to `forks.Known` + `Schedule.Validate` dependency on
`module_revert_v1`. Events follow `vm/events.go` rules (no `DataString` in non-final
position; 32-byte words).

### 3. `bridge-relayer` (this repo) — `cmd/bridge-relayer/`

Small Go daemon, no new module dependencies: a hand-rolled minimal EVM JSON-RPC client
(`eth_blockNumber`, `eth_getLogs`, `eth_chainId`) over `net/http` — the 2691 node exposes
only `eth,net,web3,txpool,privacy`, so logs are the indexing primitive (no `debug`/`trace`).

Loop: `safe = eth_blockNumber − confirmations`; fetch `Burned` logs from cursor to `safe`
(range-capped batches); for each log: decode (id, sender, asset, amount, quantumRecipient),
skip if `claim_status` says claimed, else build the `bridge.claim` envelope, sign with the
operator `.sqwallet` (ML-DSA-65, `CtxTransaction` — a claim is an ordinary module TxCall; no
new signature domain needed), `POST /tx/submit`, poll the receipt, then advance the cursor
(persisted to a state file; restarts are safe because idempotency is on-chain). Refuses to
start if `eth_chainId` ≠ configured chain id or the quantum `/status` chain id ≠ expected
(the blockpeak-credits "I-3 chain-id mismatch" lesson). Never attaches value to a module
call (`tx.Amount` is ignored on the router path). Structured logs; per-iteration error
backoff; a permanently SOFT-failing claim is logged and skipped after N attempts, never
blocks the cursor (it can be re-submitted manually — the slot guarantees safety).

### 4. Migration dapp — moved to `Splendor-Protocol/splendor-quantum-dapps`

**The dapp now lives in its own repo, `splendor-quantum-dapps`** (infra decision 0001: one
service, one repo), as one dapp inside the **`dapps.splendor.org` portal**: the portal at `/`
(icon buttons — Bridge, Explorer, Faucet) and the bridge at `/bridge` with its read-only API
under `/bridge/api/*`, plus Splendor Connect (optional `SQ:`-address prefill). It was
scaffolded in this repo for review during the bridge build and then extracted; the code and
its README are authoritative there.

Single Cloudflare Worker, faucet architecture (no framework: router + inline-script pages),
explorer design language (ink `#070709`, acid `#c8ff63`, violet `#936dff`, cyan `#66e7ff`,
mono-uppercase data register). Worker API (server-side fetch, no keys, read-only):

Client flow (all EVM interaction via the user's injected wallet — page CSP stays
`connect-src 'self'` + wallet extension, no third-party scripts):

1. Connect MetaMask; auto-add/switch to chain `0xa83` (2691).
2. Enter the destination `SQ:` address (paste from the Splendor Wallet app; validated
   `SQ:[0-9a-f]{64}` with the wallet's `extractSqAddress` liberal-paste rules).
3. Pick SPLD or SPLV + amount (min 1; MAX button). SPLV shows an approve step first: the
   user approves exactly the burn amount to the burn contract, which then pulls it via
   `transferFrom` straight to `0x…dEaD`.
4. Submit burn; then a status timeline driven by Worker endpoints:
   `burn sent → N/120 confirmations → claim submitted → credited on quantum` (terminal state
   links the quantum explorer tx/address).

Worker API (server-side fetch, no keys, read-only):
- `GET /api/info` — burn contract address, SPLV address, min burn, confirmation depth, bridge
  address, pool balance + claimed totals (from `/bridge/{addr}`), paused flags.
- `GET /api/status/{burnTxHash}` — 2691 receipt + confirmation count (via server-side RPC) +
  decoded burns → per-burn quantum `claim_status`; the credited step links the recipient's
  address page on the quantum explorer (the claim tx hash is not exposed by the REST read).
  Rate-limited per-IP (KV), byte-bounded bodies, strict CSP with per-response nonce — all
  faucet patterns.

### 5. Splendor Wallet app — integration spec (separate repo, not edited in this job)

The wallet already has both halves: EVM signing on 2691 (`walletService`, ethers v5) and a
complete quantum module (`src/quantum/`, ML-DSA-65, `SQ:` accounts). The migration screen is
a structural clone of the proven `SplvSwapScreen` + `splvToSpldSwapService` pair:

- `src/utils/quantumMigrationService.ts` — clone of `splvToSpldSwapService.ts`: address map
  `{2691: <burn contract>}` + `isDeployed()` guard, `getStatus()` via one `Promise.all`
  (SPLD/SPLV balances, allowance, contract paused, min burn), `approveSplv()`, `burnSpld()`,
  `burnSplv()`, `friendlyBurnError()` mapping the custom errors.
- `src/screens/QuantumMigrationScreen.tsx` — clone of `SplvSwapScreen.tsx`: pin
  `selected_network` to `'splendor'` while focused and restore on exit
  (`networkToRestoreRef` pattern); `Phase = 'idle'|'approving'|'burning'` +
  committed-amount lock (the SPL-169 fix); asset toggle SPLD/SPLV; destination picker that
  offers the wallet's own quantum account (`getChainAccount()`, create inline via
  `QuantumSignerController` if absent) or a pasted `SQ:` address (`extractSqAddress`);
  biometric confirm; post-burn status card polling the dapp Worker's `/api/status/{tx}` (or
  the quantum RPC directly) until credited.
- Registration: route in `navigation/types.ts`, screen in `App.tsx` gated by
  `isFeatureEnabled('QUANTUM_MIGRATION')` (new STABLE flag in `constants/Features.ts`), tile
  in `DappsScreen.tsx` behind `useFeatureAccess('QUANTUM_MIGRATION')` for staged rollout
  (SPLV_SWAP precedent), ABI JSON in `src/contracts/abi/` via `contracts/sync-abi.sh`.
- The wallet needs **no quantum-side signing changes**: the bridge operator mints/transfers;
  the wallet only reads `getTokenBalances()` / `getHistory()`, which already exist. SPLV
  appears on the quantum side once the indexer knows the token address (follow-on:
  `splendor-infra` indexer action table).

## Failure modes

- **Reorg on 2691 after claim** — prevented by the 120-block confirmation depth; the relayer
  only reads `≤ latest − depth`. A reorg deeper than that is a manual-ops event (pause).
- **Relayer crash between submit and cursor advance** — re-submits on restart; the claimed
  slot makes the duplicate a SOFT no-op.
- **Pool underfunded** — `claim` SOFT-fails with an explicit error; relayer logs and retries
  on a backoff; users' burns are never lost (the id stays claimable forever).
- **Claim permanently failing** (cap too low, paused) — SOFT, skipped after N attempts,
  alarmed via logs; resolvable by owner action then manual re-run (`--replay-id`).
- **Operator key compromise** — bounded by pool balance + `max_claim_amount` + SPLV
  `max_supply`; owner `pause` + `set_operator active=false`; every claim is an on-chain
  event with the EVM tx hash for audit.
- **User burns with a wrong/unknown `SQ:` recipient** — funds go where the burn event says;
  the dapp and wallet validate format and confirm with an explicit "this address, this
  chain" step. A zero recipient is refused on-chain.
- **Direct SPLV `transfer` to the burn contract** (no event, no claim) — the contract holds
  nothing by design, so this is user loss like sending to any wrong address; the dapp warns.
  (The swap contract's `convertDirectTransfer` escape hatch does not apply — there is no
  custody.)

## Testing

- Go: module unit tests mirroring `vm/exchange` coverage (deploy, operator gating, claim
  paths, idempotency, caps, pause, supply conservation incl. a bridge-pool variant of
  `fee_conservation_test.go`), router/read/preflight registration tests (the coverage test
  pins `KnownModuleActions`), fork-validation tests, relayer log-decode + loop tests against
  `httptest` stubs of both chains.
- Solidity: forge tests — min burn, zero recipient, pause, SPLV fee-on-transfer defence,
  event shape, dEaD forwarding, reentrancy.
- Dapp: vitest for the Worker routes (info/status shape, rate limit, bounded bodies).

## Rollout

1. Land chain-side + relayer + contracts + dapp scaffold (this branch).
2. Devnet: schedule `bridge_v1` in devnet genesis (fresh tier or planned restart), deploy a
   mock SPLV + burn contract on 2692 (testnet stand-in for 2691), run the relayer, e2e.
3. Testnet quantum + 2692, dapp on a staging domain, wallet build behind `useFeatureAccess`.
4. Mainnet: `bridge_v1` at a scheduled height in the mainnet genesis/fork plan, burn contract
   deployed from `wallet/contracts` (timelock org policy), pool funded from the genesis
   allocation sized to the migration budget, relayer on splendor-infra (SSM, no SSH),
   `quantum-migration.splendor.org` live, wallet flag ramped.

## Follow-ons (explicitly out of v1)

- ~~M-of-N operator attestations on `claim`~~ — **done** (`threshold`, `attestations`,
  `set_threshold`, `CtxBridge`/`BridgeClaimDigest`, relayer `--attestation-dir`).
- `splendor-infra` indexer support for `bridge` events + SPLV token metadata.
- Reverse direction (quantum → legacy) — not planned; migration is one-way by design.
- An EVM-signature→SQ derivation helper in the dapp (`@splendor/pq-bridge`) for users
  without the wallet app — deliberately excluded v1: that path is not quantum-safe and the
  wallet's independent-key design is the recommended custody story.
