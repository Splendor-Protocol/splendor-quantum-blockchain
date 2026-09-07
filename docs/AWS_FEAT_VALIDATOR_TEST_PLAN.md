# AWS test plan: `feat/validator` on a throwaway chain

**Status: ready to run, not launched.** The `default` AWS profile on this
machine holds an invalid token (`InvalidClientTokenId` from STS) and there is
no `splendor-640` profile yet. Nothing below has created a resource. The
first command is the sign-in; everything after it is scripted and idempotent.

## Goal

Run `feat/validator` on AWS, off every real tier, and confirm on a fresh
chain that:

1. the validator gates hold (`validator_gates_v1`: pass + bond decide who may
   produce; a bonded key without a pass is *eligible: false*);
2. SQVL works end to end (`verify_v1`: dual-signed instruction → register →
   attest → spend → proof, replay refused) through the same RPC a bank or a
   bridge would call;
3. `qrc721_v1` mints and reads;
4. the native supply check never trips on an honest chain;
5. `min_fee_v1` flips the fee floor at its height and the chain keeps
   producing through it;
6. full nodes sync from the validator and stay within a few blocks.

What it does **not** test: multi-producer consensus. `multi_validator` is
still unimplemented on this branch (Dale's BFT engine was left out; see
`docs/FEAT_VALIDATOR.md`). The "3 validators" in the 4-node topology are
three *syncing full nodes*; one key produces. That is the branch's actual
state, and the plan says so rather than pretending otherwise.

## Account and credentials

| | |
|---|---|
| Account | `640168431521` (LatamDX — the same account `splendor-infra` deploys testnet/production into) |
| Region | `us-east-1` (matches `splendor-infra/environments/*.tfvars`) |
| Profile | `splendor-640` preferred, `default` fallback. **Never** the `splendor` / stardust profile for EC2. |
| Auth | `aws login` (CLI v2.27+, browser sign-in) — no long-lived keys on disk |

Sign in, then prove it:

```bash
aws login --profile splendor-640          # browser flow; creates/refreshes the profile
aws sts get-caller-identity --profile splendor-640   # must print Account 640168431521
```

If the account is behind IAM Identity Center instead, `aws configure sso
--profile splendor-640` is the equivalent. `00-preflight.sh` refuses to go on
unless STS answers with exactly that account id.

## What gets created, and what never does

Created (all tagged `Project=splendor-feat-validator`, `Owner=<you>`):

- 1 or 4 EC2 instances: Ubuntu 24.04 arm64 (Graviton) from the same SSM AMI
  parameter `splendor-infra` uses; `t4g.large` for the validator, `t4g.small`
  per full node; 40 GiB encrypted gp3 root; IMDSv2 required.
- 1 security group in the default VPC: 22 and 8080 from **your** public IP
  only, 8080 inside the VPC CIDR (peers).
- 1 ed25519 key pair, private half at `~/.sqc-feat-validator/<name>.pem`.

Never: anything in the `splendor-infra` tofu state, RDS, Cloudflare, GHCR,
`genesis-production.json`, or a real tier's chain id. The test chain is
`sqc-featval-1`, so nothing signed on it can replay against a real tier.

Cost while running: about $0.07/h for the single node, about $0.12/h for
four. Tear down the same day.

## Topology

Two phases; do the first before the second.

**Phase A — single-node gated smoke.** One instance running the validator
with the feature genesis. Exercises everything in the goal list except sync.

**Phase B — 1 producer + 3 syncing full nodes.** The validator plus three
full nodes started with `--validator-pubkey-hex` and `--peer` pointing at
the validator's private IP over the sealed ML-KEM channel, sharing one
write-plane token. Exercises sync and that every follower applies the same
gates, supply check and fee floor.

Binaries are cross-compiled here (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64`,
static, verified to build) and shipped over SSH. The hosts never need the
repository, a deploy key or Go.

## Genesis under test

Written by `10-launch.sh` into `~/.sqc-feat-validator/chain/genesis.json`:

```json
{
  "chain_id": "sqc-featval-1",
  "validator_pubkey_hex": "<fresh validator wallet>",
  "allocations": [
    { "address": "<validator>", "balance": "1000000000000000000000000" },
    { "address": "<faucet>",    "balance": "1000000000000000000000000000" }
  ],
  "total_supply": "",
  "forks": { "module_revert_v1": 1, "validator_gates_v1": 1, "verify_v1": 1,
             "qrc721_v1": 1, "min_fee_v1": 300 }
}
```

`min_fee_v1` at height 300 (about ten minutes at 2 s blocks) so the flip is
observable: run the smoke once before, once after. The faucet key is what
`featsmoke` spends from; its seed lives only in that directory.

## Commands

All under `scripts/aws/feat-validator/`. Each sources `env.sh`; override any
setting by exporting it first.

```bash
# 0. credentials + AMI + VPC sanity. Exit 2 with the aws login instructions if STS fails.
bash scripts/aws/feat-validator/00-preflight.sh

# A. single node
bash scripts/aws/feat-validator/10-launch.sh              # add --dry-run to print every aws call and run none
bash scripts/aws/feat-validator/20-smoke.sh               # featsmoke against the validator; PASS/FAIL per step
#    ... wait past height 300 (watch: curl -s http://<validator-ip>:8080/status | jq .height)
bash scripts/aws/feat-validator/20-smoke.sh               # second run: fee floor now 0.001 SPLD

# B. producer + 3 followers (after A passed; tear A down first or reuse it — the script launches fresh instances)
bash scripts/aws/feat-validator/90-teardown.sh
bash scripts/aws/feat-validator/10-launch.sh --nodes 4
bash scripts/aws/feat-validator/20-smoke.sh               # also prints every follower's height next to the validator's

# done
bash scripts/aws/feat-validator/90-teardown.sh            # --purge-local also removes ~/.sqc-feat-validator
```

`20-smoke.sh --rpc http://127.0.0.1:18545 --seed ./faucet.seed --chain-id
sqc-featval-1` runs the identical driver against a local node — which is how
it was validated before this plan was written (see "Evidence").

### What `featsmoke run` checks

`cmd/featsmoke` signs real ML-DSA-65 transactions and reads receipts, exactly
as a wallet would:

| step | passes when |
|---|---|
| `/status` | chain id matches; `validator_gates_v1`, `verify_v1`, `qrc721_v1` scheduled; `genesis_validator` anchor known; height ≥ 1 |
| faucet | funded at genesis |
| staking | `staking_info` readable; `register` of 5,000 SPLD confirms; record `active`; `can_produce` is **false** with the reason "no admission pass"; `validator_set` readable |
| verify | `POST /verify/check` accepts the dual signature; `deploy` → `register` → `attest` (instance-bound) → proof `threshold_met` → `spend` → proof `spent`; a replayed spend is a failed receipt "already spent"; `GET /verify/proof/{id}` serves it |
| qrc721 | deploy, owner mints id 1, `owner_of` agrees |
| fee floor | before height 300: a 1,000-unit fee is applied; after: it is refused "fee too low" and 0.001 SPLD is applied |
| chain | height advanced during the run (the supply check did not halt it) |

## Success criteria

- Phase A: `featsmoke` reports `ALL PASS` both before and after height 300,
  and `sqc-node` logs no `ERR` during the run.
- Phase B: `ALL PASS` again, every follower's `/status.height` within 3
  blocks of the validator's, and each follower's `/status.forks` and
  `genesis_validator` identical to the validator's.
- Nothing appeared in the infra tofu state, and no real tier's RPC saw a
  transaction (their chain ids differ, so it cannot have).

## Rollback

There is nothing to roll back on a real tier: this is a separate chain on
separate hosts. `90-teardown.sh` terminates every instance carrying the tag,
deletes the security group and the key pair, and can purge the local
directory. Running it twice is safe. If a launch fails half-way, run it
anyway.

## Guardrails

- `00-preflight.sh` refuses any account but `640168431521`.
- The security group admits your IP only; the write plane is token-gated;
  RPC binds `0.0.0.0` only inside that group.
- Wallet passphrase and faucet seed never leave `~/.sqc-feat-validator`
  except to the validator host over SSH (mode 0600, owner `sqc`).
- No real chain id, no production genesis, no infra state.

## Evidence

Before this plan was written, the same driver ran against a local chain built
from this branch (`sqc-featval-1`, `min_fee_v1` at 60, 1 s blocks): every
step passed on the run before the fee height and again after it, including
the refusal of the 1,000-unit fee once the floor moved. See
`docs/FEAT_VALIDATOR.md`, "Verification".

## When the credentials are live

1. `aws login --profile splendor-640` and `00-preflight.sh` → `READY`.
2. Phase A, both smoke runs, teardown. About 20 minutes.
3. Phase B, smoke, teardown. About 30 minutes.
4. Paste the two `featsmoke` transcripts and the follower height table into
   the PR for `feat/validator`.
