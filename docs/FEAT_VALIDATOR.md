# `feat/validator` — what is on this branch, what is not, and why

Branch `feat/validator`, tracking `origin/feat/validator`. Built on
2026-09-07 from `origin/main @ 36fbfe8` plus the validator gates branch
(`feat/validator-pass-stake-gates @ 8137cc7`, merged as `46a9112`), then
three commits porting the portable parts of Dale's uncommitted tree, then
tooling and this document.

Dale's tree arrived as a zip: a dirty working copy on `main @ 25d4479`, never
committed, 436 of its 479 "modified" files CRLF-only rewrites. It could not be
merged; it was mined. Every piece below was re-authored onto this branch from
CR-stripped copies or `git diff -w` hunks, reviewed, tested, and carries a
`Co-authored-by: Dale` trailer. His full review is
`/tmp/dale-zip/claude/PORTABILITY_REPORT.md` (local, not committed).

## Commits

| commit | what |
|---|---|
| `46a9112` | merge: validator gates (`validator_gates_v1`) onto `feat/validator` — **ours, the producer source of truth** |
| `3ee461b` | verify: SQVL, the dual-signed (ML-DSA-65 + SLH-DSA) verify/settlement module (`verify_v1`) |
| `04488c4` | supply cap + `min_fee_v1` |
| `c7717a6` | vm: harden lending, oracle and QRC-721; route `qrc721_v1` |
| the commit this file arrived in (`git log -1 --format=%h -- docs/FEAT_VALIDATOR.md`) | `featsmoke` driver, AWS scripts, this document, FINAL_SUMMARY addendum |

## Included

### 1. Validator gates — `validator_gates_v1` (already on the branch)

`vm/staking.go` as a real escrowed bond with an admission-pass registry keyed
by the ML-DSA consensus key; `CanProduce = pass && (bond_exempt || active &&
stake >= min)`; the genesis-validator anchor; the gate called from
`ProduceBlock` and `ExecuteBlock`; the last-producer guard. Spec:
`docs-sqc-implement-pass-stake.md`; summary: `FINAL_SUMMARY.md`; module doc:
`docs/modules/staking.md`. **Nothing from Dale's tree touches it.**

### 2. SQVL — `verify_v1` (commit `3ee461b`)

Dale's design, kept: an instruction signed by **both** ML-DSA-65 and
SLH-DSA-SHA2-128s under `SPLENDOR-QC-VERIFY-v1`; instances with an owner,
operators and a threshold; `register → attest → finalize/spend` with a sticky
nullifier; `/verify/check`, `/verify/attest`, `/verify/settle`,
`/verify/proof/{id}`; the SLH-DSA wrapper over circl. Doc:
`docs/modules/verify.md`.

Changed in the port, each with a test (`vm/verify_test.go`,
`core/types/verify_test.go`, `rpc/sqvl_rpc_test.go`):

| defect in the zip | fix |
|---|---|
| `instruction_id` marshalled as a JSON array of 32 ints; the SDK declared a string | `types.InstructionID`: hex on the wire, array form refused |
| attestations signed only the instruction digest — replayable across instances | attestation digest binds `(chain, instance, digest)` |
| `x402` settlement was a silent no-op at `spend` | refused at `register`, with the pointer to `/x402/settle`; unknown actions and unparseable transfer fields refused there too |
| `register` open to anyone holding a valid signature | signer or an attestor of the instance |
| `pause` stopped only `register` | stops register / attest / finalize / spend |
| threshold unbounded (`deploy` with 0 operators, any threshold) | `1 ≤ threshold ≤ owner + operators`, at deploy, `set_threshold`, and `set_operator` revoke |
| `b64orHex` guessed the encoding | decode by exact byte length, hex or base64 |
| no atomicity, no events | one snapshot per action; `VerifyDeployed/Registered/Attested/Finalized/Spent` |
| ad-hoc instance address | `SHA3(deployer ‖ nonce ‖ u64be(height) ‖ "verify")`, the bridge/exchange shape |
| SLH key had to be generated fresh per session | `crypto.SLHKeypairFromSeed`: derived from the wallet seed under its own domain |
| `docs/modules/verify.md` pointed validators at his 2,500 SPLD module | points at `staking.md` |

SDK (`sdk/sqc-sdk`): `VerifyModule` / `deployVerify` / `getVerify`
(registered module `verify`), `SqRpc.verify*` doors,
`Wallet.signVerifyInstruction` / `signVerifyAttestation` /
`getSLHPublicKeyHex`, `verifyAddress`, `session.verify`. Signer WASM rebuilt
under the pinned go1.25.0 with `sqGetSLHPublicKeyHex`,
`sqSignVerifyInstruction`, `sqSignVerifyAttestation`; pins re-synced
(sha256 `bec3f8d6dcb4804ab191ca86263d91eb694379c828998170d9579bcecf700c2e`).
Golden vectors gained a `verify` section pinned in Go and checked against the
WASM build. **The wallet extension must re-pin its copy of the signer**
(`sdk/CHANGELOG-HARDENING.md`).

Decision recorded, not made: SLH-DSA **128s** stays (7,856-byte signatures,
seconds to sign in WASM, cheap to verify). Moving to 128f (17 KB, ~10× faster
signing) changes every stored instruction; decide before the first instance
is deployed on a real tier.

### 3. Native supply hard cap (commit `04488c4`)

`genesis.ApplyToState` refuses a genesis above 26 billion SPLD and records
what it minted (`native_minted` meta, outside the state root). Every block
apply, produce and crash-replay sums the staged balances and refuses a total
above that record (`genesis.CheckNativeSupply`, `StateDB.SumNativeBalances`).

Changed from Dale's version, deliberately: the cap is **not** in
`Genesis.Validate` (re-run on the pinned document every start of a running
chain), and a chain with no minted record **passes** (his fell back to the
constant). Either would halt a live tier that minted differently before this
rule — and no tier's true minted total is verifiable from this machine; the
readme itself notes one tier over-minted. `DefaultGenesis` keeps its 1 B dev
faucet (his change to 26 B is out). Cost: one pass over every account per
block; an incremental counter is the follow-up before mainnet scale.

### 4. `min_fee_v1` (commit `04488c4`)

Fork name; mempool base fee `SetBaseFee`; the producer pre-tx hook and the
pool floor both fed by `cmd/node/min_fee.go: minFeeAt`, aligned on every node
at start and every block interval. Standalone fork (no module, no
`module_revert_v1`).

SDK: `MIN_FEE_V1` / `PROTOCOL.minFeeV1` exported and documented;
**`MIN_FEE` / `PROTOCOL.minFee` unchanged.** Dale flipped the default, the
golden vectors and the tests to 10^15 — a 10^12× change that breaks every
wallet pinned to the launch constant the day it ships. That is a coordinated
release with the wallet extension, not a side effect of a chain feature; no
tier has scheduled the fork yet.

### 5. Module hardening (commit `c7717a6`)

Library-level, each with Dale's test:

- **lending**: `SetOracle` / `Oracle` / `BorrowFromOracle` — a routed borrow
  must never take the borrower's own quote for its collateral.
- **oracle**: feeder index (`ListFeeders`); `GetPrice` discovers feeders.
- **qrc721**: deployer recorded as collection owner; `Mint` is owner-only.

### 6. `qrc721_v1` routed (commit `c7717a6`)

The NFT module whole: `deploy`, `mint`, `transfer`, `transfer_from`,
`approve`, `set_approval_for_all`, `burn`; reads `owner_of`, `balance_of`,
`token_uri`, `get_approved`, `is_approved_for_all`, `token_info`; standard
events; undeployed collection refused; requires `module_revert_v1`. Doc:
`docs/modules/qrc721.md`. Tests: `vm/qrc721_router_test.go`. The SDK fixture
lists it with every action as a declared gap (no `QRC721` Program yet).

### 7. Tooling and docs (this commit)

`cmd/featsmoke`: end-to-end driver (gates, SQVL, qrc721, fee floor, supply
check) against any node. `scripts/aws/feat-validator/`: preflight, launch
(1 or 4 nodes, `--dry-run`), smoke, teardown.
`docs/AWS_FEAT_VALIDATOR_TEST_PLAN.md`. This file. `FINAL_SUMMARY.md`
addendum. Module rows in `readme.md`, `DEPLOY.md`.

## Excluded

| piece of Dale's tree | why not |
|---|---|
| **`validators_v1`** — `vm/staking.go` rewritten to a flat 2,500 SPLD bond, 32-block unbond, no pass, no slashing, `ListActive` index; `validators` module; `/validators` routes; `validators.ts`; `docs/modules/validators.md` | Writes the same storage slots as `validator_gates_v1`'s staking module under a different schema; they cannot coexist. The gates are the spec'd, reviewed producer model on this branch. Product decision, made: gates stay. |
| **`multi_validator` BFT** — `consensus/bft.go` changes, `consensus/multivalidator.go`, `QCVote` / `ComputeQCRoot` / `ComputeVotedHash`, p2p `vote` / `proposal` kinds, `cmd/node` wiring, `verifyProducerAndQC`, `BuildProposal` | Reads his registry (`ListActive`), falls back to the block's own embedded key when the producer is unknown, skips the quorum below two validators, removes `AcknowledgeIncomplete`, and changes `Block.Validate()`'s hash rule without its own fork. **Future: rebase BFT on `CanProduce`** — proposer must pass `requireEligibleProducer`, vote-key map from `Passes() ∧ CanProduce`, `RefreshSet` from `staking.validator_set`, hash-rule change under its own gate. Its own ADR and PR. |
| **`genesis-production.json`** edit (comment + 11 forks at height 1) | Changes the live genesis hash. Never. |
| **`DefaultGenesis`** dev faucet 1 B → 26 B | Changes every dev-genesis fixture; unrelated to the cap. |
| **`Genesis.Validate` cap** and the constant fallback in the per-block check | Would halt a live tier that minted more before the rule (see §3). |
| **SDK `minFee` default flip** (`PROTOCOL.minFee`, golden vectors, `protocol.test.ts`, `DAPP_INTEGRATION` to 10^15) | Coordinated release, not a side effect (see §4). |
| **`lending_v1` routing** | No liquidation path fed by the oracle; `Liquidate` still takes caller prices. Needs its own review. Hardening landed. |
| **`oracle_v1` routing** | `submit_price` signed the *inclusion* height, which a feeder cannot know; and there is no SDK signing path for `CtxOracle`. Design fix (height in params + freshness window) and a signer export first. Hardening landed. |
| **`multisig_v1` routing** | `submit` without `confirm` / `execute` / `revoke`. |
| **`governance_v1` routing** | `Execute` applies nothing (`FINAL_SUMMARY.md`). |
| **`qrc4626_v1` routing** | `deposit` without `withdraw` / `redeem` — a vault you cannot leave. |
| `vm/extra_dispatch.go` as a file, and his relaxation of `TestKnownModuleActionsCoverEveryDispatchCase` to scan it | The drift guard reads the router files on purpose; dispatch lives there. |
| `readme.md`, `DEPLOY.md`, `CICD_DEPLOYMENT.md` rewrites | Prose built around the flat bond, BFT and the 26 B default; ours is rewritten against the gates. Rows for the new modules added instead. |
| `docs/multi-validator-local.md`, `genesis-local.json`, `examples/sqvl-external-app.mjs`, `docs/modules/standards.md` | Describe the excluded pieces (the example calls `/validators`); the SQVL flow is in `docs/modules/verify.md` with an SDK snippet. |
| `core/types/qcroot_compat_test.go` change, `consensus/bft_test.go` rewrite | Belong to the BFT piece. |
| CRLF rewrites of 436 files, `.github/workflows`, `Dockerfile`, `Makefile`, `contracts/legacy-burn/lib/forge-std/**` | No content change. Dale needs `core.autocrlf=input` (or `* text=auto eol=lf` in `.gitattributes`) before pushing anything. |

## Open decisions

1. SLH-DSA 128s vs 128f, before the first `verify` instance on a real tier.
2. When (and with which wallet-extension release) `PROTOCOL.minFee` flips.
3. Lending / oracle routing after their reviews; a `QRC721` SDK Program.
4. BFT on `CanProduce` — the real Phase 2.
5. Incremental supply counter before mainnet scale.
6. Dale's git identity: the trailers carry `dale@splendor-protocol.invalid`
   (RFC 2606 reserved, cannot misattribute anyone) because no address for
   him exists in any repo on this machine or the org's visible membership.
   Fix it on the squash/merge PR.

## Verification

| check | result |
|---|---|
| `go build ./...`, `go vet ./...` | clean |
| `gofmt -l` on every changed Go file | clean (pre-existing unformatted files untouched) |
| `go test -count=1 ./...` | all packages ok (Go 1.25.0 via `GOTOOLCHAIN=auto`) |
| `sdk/sqc-sdk`: `tsc --noEmit`, `npm run build:ts`, `npm test` | 35 files, 171 tests passed (incl. `wasm-verify.test.ts` against the rebuilt signer) |
| `sdk/connect-react`: `tsc --noEmit`, `npm test` | 69 tests passed |
| `sdk/sqc-sdk/scripts/check-wasm-dist.mjs` | pins match the rebuilt `sqc_signer.wasm` |
| `go test ./sdk/sqc-sdk/signer -run 'Golden\|Domain'` | ok (verify golden pinned) |
| linux/arm64 static cross-build of `sqc-node`, `sqc-wallet` | ok |
| local chain smoke (`featsmoke`, `sqc-featval-1`, `min_fee_v1` at 40, 1 s blocks) | ALL PASS before the fee height (28 steps) and after it (29 steps); node logged no `ERR` — see the FINAL_SUMMARY addendum |

## Follow-ups (not done here)

- The AWS run itself (`docs/AWS_FEAT_VALIDATOR_TEST_PLAN.md`): credentials
  were not live on this machine.
- Wallet extension re-pin of the signer WASM.
- Everything under "Open decisions".

## Where to read next

- Shareable FAQ: [`docs/VALIDATOR_FAQ.md`](VALIDATOR_FAQ.md)
- Staking / CanProduce: [`docs/modules/staking.md`](modules/staking.md)
- SQVL verify module: [`docs/modules/verify.md`](modules/verify.md)
- Lab deploy steps (infra): `docs/lab-validator-deploy.md` on `splendor-infra` @ `feat/validator`
