# AWS parallel SQC: prove multi-validator pass+stake gates

## Source of truth (ONLY)
https://github.com/Splendor-Protocol/splendor-quantum-blockchain/tree/feat/validator-pass-stake-gates
Local checkout: `/Users/nick/Workspace/splendor/splendor-quantum-blockchain` on that branch.
Do NOT use `mascarock/sqc-validator-pass-stake-gates` or Origin `mascarock/tmp-78c14086964c44bb`.

Infra reference: `/Users/nick/Workspace/splendor/splendor-infra` (deploy via `gh` + OIDC; see README + `docs/chain-redeploy-runbook.md`).

## Goal
Deploy a **parallel** SQC chain (not mainnet, do not wipe live testnet/prod) and prove end-to-end:

Target layout: **1 master/bootstrap + 3 validators** (or equivalent).

1. Keys that are **admission-passed + bonded** (or genesis bond-exempt) **can produce** blocks.
2. Keys that fail either gate **cannot** produce / are refused by peers / ExecuteBlock.
3. Collect **evidence**: RPC URLs or IPs, heights advancing, which pubkey produced which heights, logs showing CanProduce allow/deny, pass/stake RPC reads.

## Constraints (honest)
- Phase-1 BFT / `multi_validator` fork is still incomplete (`FINAL_SUMMARY.md`). Prefer a **temporary PoA / explicit multi-producer test harness** (round-robin or scheduled producers) that still calls the real `CanProduce` / pass+bond gates — do NOT fake the gates.
- Do **not** touch mainnet (`environment=prod`). Prefer throwaway parallel: docker-compose / multiple processes on one host / new EC2(s) / a dedicated parallel env — not `reset_chain=true` on live testnet unless Niccolò explicitly confirms.
- Laptop `aws sts` session is **expired** (`aws login` needed). Prefer `gh` workflow OIDC for infra image build/rollout when possible; if raw AWS CLI is required, stop and ask for `aws login` rather than inventing credentials.
- Bash(*) allowlisted in `.claude/settings.local.json`.

## Suggested approach (smart, low drama)
1. Read `FINAL_SUMMARY.md` + how `CanProduce` / `validator_gate` / staking pass work on this branch.
2. Design smallest harness for N producers that still uses real gates (temp PoA schedule OK).
3. Build node binary/image from `feat/validator-pass-stake-gates` (`gh workflow run build-node-image.yml -R Splendor-Protocol/splendor-infra -f chain_ref=feat/validator-pass-stake-gates -f image_tag=v0.0.0-gates-mvtest` or local `go build`).
4. Bring up parallel topology (docker-compose on Air or AWS); fund/register/set_pass/stake as needed.
5. Negative tests: revoke pass or under-bond → production blocked; restore → allowed.
6. Write `/Users/nick/Workspace/splendor/splendor-quantum-blockchain/AWS_MULTIVALIDATOR_EVIDENCE.md` with: topology, IPs/URLs, commands, height samples, producer identities, pass/fail matrix, and any infra caveats.
7. When evidence is solid, stop — do not open PR unless asked.

## Success criteria
- Parallel chain running (documented endpoints)
- ≥1 successful multi-producer window with gates enforced
- Clear deny case documented
- Evidence file written; no mainnet impact

## Update 2026-09-05 (Niccolò)
- **No public explorer** for this pass — RPC endpoints / evidence only. Do not deploy Cloudflare Worker or invent quantum-multivalidator-explorer.splendor.org.
- Prefer AWS access via `AWS_PROFILE=splendor` (stardust IAM) and `~/Workspace/splendor/infra/bin/qssm` — see `~/Workspace/splendor/infra/SECRETS.md`. Prefer over `aws login`/root session.

## Update 2 — PROOF focus (Niccolò via BlockHunter Mac Pro)
Primary deliverable is **proof that 1 master + 3 validators works**, not a fancy explorer.

1. Deploy/run parallel chain: **1 master/bootstrap + 3 validators**. Temp PoA / scheduled multi-producer OK if BFT incomplete — but must call real CanProduce / pass+bond gates.
2. Scenarios (concrete):
   - Eligible (passed + bonded, or genesis bond-exempt): produces blocks
   - Non-passed OR under-bonded: blocked (logs / ExecuteBlock / peer reject)
   - Master/coordinating role clearly identified in evidence
3. Write `AWS_MULTIVALIDATOR_EVIDENCE.md` (endpoints/IPs, heights, who produced which blocks, gate denials, commands/logs).
4. **After** evidence exists: run a second pass (Claude self-review or spawn) whose ONLY job is correctness/proof review of the evidence + setup — NOT a security audit. Save as `AWS_MULTIVALIDATOR_PROOF_REVIEW.md` next to the evidence.
5. AWS_PROFILE=splendor + infra/qssm. No explorer. Source: Splendor-Protocol feat/validator-pass-stake-gates only.

## Update 3 — AWS identity
Do **not** use AWS_PROFILE=splendor (stardust). Use `default` or `splendor-640` (root console session from aws login) for admin/EC2; `openclaim-btc` for inventory reads.
