# Devnet stress test — 10k / 20k / 30k / 40k accounts, one transfer each, then 15 min sustained

Runbook and scripts for driving the **devnet** tier (`splendor-quantum-devnet-1`,
`https://quantum-rpc-devnet.splendor.org`, single validator on
`i-068aabcf6348d454c`, SSM only) with real ML-DSA-65 transfers from tens of
thousands of accounts and recording whether the node survives. DEVNET ONLY:
`env.sh` refuses any other RPC, chain id or instance.

## Shape of the test

```
laptop ── SSM ──> validator host (t4g.xlarge, 4 vCPU, 16 GiB)
                    ├─ compose network splendor-quantum-node_default
                    │    ├─ sqc-node (the validator, RPC :8080, metrics :9100)
                    │    ├─ sqc-load-c00 … c19   txload drivers (alpine:3.20, --cpus 0.1, --memory 48m)
                    │    └─ sqc-load-rpcproxy    socat: host 127.0.0.1:18080 -> sqc-node:8080
                    ├─ /srv/sqc-load/{bin/txload, keys/funder-NN.seed, runs/<run-id>/, watch.sh}
                    └─ systemd unit sqc-load-watch-<run-id>  (lib/watch-host.sh, every 10 s)
laptop ── SSM ──> quantum-obs (i-04bac137b45777113): Prometheus :9090, Loki :3100  (lib/prom-range.py)
```

* **Drivers** are `cmd/txload` (this branch adds `--lazy-keys`, `--one-shot`,
  `--scan-node -1`, `--fund-deadline`). 20 containers, one funder each, on the
  node's own compose network so the node sees 20 client IPs (its submit rate
  limit is 25 req/s per IP, burst 50 — `cmd/node/main.go --rpc-rate-limit`).
* **Accounts** are txload's deterministic senders of each funder
  (`SHA3-256("sqc-txload/v1/" + funder + "/" + i)`); the pools are cumulative,
  so stage N funds only the 10k accounts stage N-1 did not have.
* **Funders** are themselves txload senders of the devnet faucet at
  `--sender-offset 100000`; only their 20 seeds go to the host. The faucet seed
  never leaves the laptop (`infra/bin/qkey seed faucet-devnet`).
* **A stage** = FUND pass (top every new sender up to 1 SPLD) → wait until the
  mempool has been ≤ 2 for 3 min (the heartbeat container keeps 1–2 pending) →
  BURST pass (`--one-shot` at 20 × 24 tx/s: every account sends exactly one
  0.001 SPLD transfer to its neighbour) → wait for the mempool to drain.
  `sustained` streams 20 × 12 tx/s for 15 min from the 40k pool instead.
* **Stop rules** (watcher, drivers only — never the node): healthz not 200 for
  60 s, height unchanged for 120 s, MemAvailable < 100 MB, average block time
  > 5 s over 2 min. The rule that fired is the finding.

## Order

```bash
export DS_WORK=~/.sqc-devnet-stress          # keys/, runs/, figures/, report/ — never committed
scripts/aws/devnet-stress/00-preflight.sh --wait-for-tag   # account, SSM, credits, faucet, host, baseline block time; waits for image_tag v1.1.1
scripts/aws/devnet-stress/05-build-and-stage.sh            # arm64 static txload -> S3 (presigned) -> /srv/sqc-load/bin; rpc proxy; watcher
scripts/aws/devnet-stress/10-keys.sh                       # faucet seed (laptop only), 20 funder seeds, cross-checked, uploaded
scripts/aws/devnet-stress/20-fund-funders.sh               # 20 x 2200 SPLD from the faucet, through the public RPC
scripts/aws/devnet-stress/30-run.sh s10k                   # then s20k, s30k, s40k, sustained  (each ends with 40-collect.sh)
scripts/aws/devnet-stress/35-watch.sh peek <run-id>        # from another shell while a stage runs
scripts/aws/devnet-stress/50-report.py                     # figures + report/summary.{json,md}
scripts/aws/devnet-stress/90-cleanup.sh --yes              # host dir + containers + watcher units, S3 bucket, local seeds
```

Every stage writes `$DS_WORK/runs/<run-id>/run.json` (all timestamps),
`host/{fund,burst|sustained}/cNN/{stats.jsonl,summary.json,txload.log}`,
`host/watch.jsonl`, `host/stop-rule.txt` (if a rule fired) and `metrics.json`
(Prometheus series at 15 s + every `block committed` line from Loki for the
window ± 3 min).

## Verdicts (50-report.py)

* **FAIL** — a stop rule fired, node RestartCount > 0 or OOMKilled, a `killed
  process` in dmesg, healthz not 200 for ≥ 60 s, or the height stalled ≥ 120 s.
* **DEGRADED** — any block interval > 5 s, any healthz failure, < 95 % of
  submissions accepted, drain > 10 min, or one-shot senders that gave up.
* **PASS** — none of the above; the evidence line lists the numbers.

## Things to know

* `sqc_txs_failed_total` and `sqc_rpc_requests_total` are dead counters (never
  move on any tier). The report says so and does not use them.
* `docker stop -t 30` is how drivers are stopped: txload writes `summary.json`
  on SIGTERM.
* SSM `StandardOutputContent` is capped at 24 000 characters; anything larger
  is written to a file on the host and paged back by `lib/ssm.sh`
  (`ssm_fetch_tar` / `ssm_fetch_file`, gzip + base64, 20 000 chars per page).
* The instance role is SSM + CloudWatch only; the host fetches the binary
  through a 1-hour presigned S3 URL, and nothing else crosses.
* Keys: `$DS_WORK/keys/*.seed` are mode 600; `90-cleanup.sh` overwrites and
  deletes them (`rm -P`) and removes `/srv/sqc-load` on the host.
* macOS bash 3.2: no associative arrays, `date -r`, `stat -f`; the scripts
  avoid bash-4-only features.
