# Implement: SQC dual-gate validators (classic Splendor port)

Repo: `/Users/nick/Workspace/splendor/splendor-quantum-blockchain`
Reference (read-only inspiration): `/Users/nick/Workspace/splendor/splendorblockchain/System-Contracts/contracts/Proposal.sol` + `Validators.sol`

## Product goal
Anyone can clone/sync and stake-support. Only validators that are (1) admission-passed and (2) bonded above min stake may produce blocks. Port classic Splendor dual-gate spirit — NOT the EVM contracts wholesale.

## Smart scope (do this PR; do NOT boil the ocean)

### IN SCOPE — Layer B gates + wiring
1. **Admission / pass (key-bound)**
   - Add a validator pass/allowlist keyed by **validator pubkey** (ML-DSA / consensus signing key), NOT EVM address alone.
   - Genesis validator pubkey starts `pass=true`.
   - Admin (or governance-capable authority already in tree) can `setPass(pubkey, bool)` / `setUnpassed` equivalent.
   - Optional stretch if cheap: proposal+vote among active validators like classic `Proposal.sol` — only if it fits existing governance without a redesign. Prefer admin pass first if vote is heavy.

2. **Stake gating — wire orphaned staking**
   - `vm/staking.go` exists (Register/AddStake/Unstake/Slash/tiers) but is NOT in the module router (only qrc20/qrc1155/dex/exchange/bridge today). Wire it in properly.
   - Expose needed RPC/tx paths so Register + stake work end-to-end in tests.
   - Reuse classic-ish floors/tiers if constants already exist (Bronze ~3947 SPLD spirit — use whatever units SQC already uses; don't invent a token redesign).

3. **Consensus eligibility predicate**
   - Single function used by block production / peer rejection: `CanProduce(pubkey) := pass[pubkey] && bondedStake(pubkey) >= min`.
   - Today Phase-1 is still single genesis validator — KEEP that for safety, but the genesis key must satisfy the same predicate (auto-pass + ensure genesis bond or treat genesis as exempt only via explicit `pass` + documented min).
   - Do NOT pretend multi-validator BFT is complete. Do NOT enable `qc_root` / multi_validator fork. Leave `AcknowledgeIncomplete` / Phase-2 gates alone unless a tiny hook is required to call the predicate.

4. **Docs / honesty**
   - Short note in README or `docs/` : sync open; validators = pass + bond; multi-validator BFT still Phase-2 unfinished.
   - No whitepaper fantasy about AI bidding.

### OUT OF SCOPE (explicitly)
- Finishing BFT vote gossip / equivocation / timeouts / fork-choice
- Temporary scheduled PoA for N>1 (separate ADR)
- Porting Solidity system contracts
- Explorer / SDK UI work
- Mainnet deploy / infra changes unless a config flag is required

## Implementation style
- Small, reviewable commits on a branch: `feat/validator-pass-stake-gates`
- Prefer extending existing types over new parallel systems
- Tests: unit tests for pass+stake predicate; wire-staking smoke if test harness exists
- Keep genesis single-validator behavior working (regression)
- Match repo style; run relevant `go test` packages

## Success criteria
1. Staking module reachable via router; register/stake txs work in tests
2. Pass map exists, genesis passed, admin can revoke/grant by pubkey
3. Block producer eligibility checks pass+bond (or documented genesis bootstrap)
4. `go test` for touched packages green
5. Branch committed; summarize files changed + how to demo; do NOT force-push; push remote only if `origin` already configured and push is normal for this clone — otherwise leave local branch ready

## Context from prior R&D
- Live consensus: `consensus/validator.go` Phase 1 timer + genesis key
- Genesis: one `validator_pubkey_hex` in `genesis/genesis.go`
- Staking orphaned; governance validator-set type also unwired
- Classic Splendor: `Proposal.pass` + active-validator majority vote; then stake/tiers for ranking

Investigate the tree yourself; treat the above paths as hypotheses. Implement the cleanest fit.
