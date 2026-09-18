#!/usr/bin/env bash
# Run one stage against devnet.
#
#   30-run.sh <s10k|s20k|s30k|s40k|sustained> [--skip-fund] [--no-collect]
#
# A stage is DS_K (20) driver containers on the node's compose network, each
# owning one funder and N senders (s10k 500, s20k 1000, s30k 1500, s40k 2000 —
# the pools are cumulative, so each stage funds only its 10k new accounts):
#
#   FUND pass   txload at --rate 0.001 for the funding step only (tops up every
#               sender below 0.25 SPLD to 1 SPLD), then exits.
#   quiet       wait until the node's mempool has been <= 2 for 3 min (the
#               heartbeat container keeps 1-2 of its own txs pending).
#   BURST pass  --one-shot at --rate 24 per container (the node's per-IP limit
#               is 25/s): every sender sends exactly one transfer; the pass ends
#               when none remain. "sustained" instead streams --rate 12 per
#               container for 15 min.
#   drain       wait for the mempool to empty again (that time is a result).
#
# The host watcher (35-watch.sh / lib/watch-host.sh) records the node every
# 10 s for the whole stage and stops the DRIVERS if a stop rule fires. Every
# timestamp lands in $DS_WORK/runs/<run-id>/run.json; 40-collect.sh then pulls
# stats/summary/logs/watch.jsonl and the Prometheus/Loki window.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"

STAGE="${1:?stage}"; shift
N=$(ds_stage_senders "$STAGE") || ds_die "unknown stage $STAGE"
SKIP_FUND=0; COLLECT=1
for a in "$@"; do case "$a" in --skip-fund) SKIP_FUND=1 ;; --no-collect) COLLECT=0 ;; *) ds_die "unknown flag $a" ;; esac; done
RUN="$STAGE-$(date -u +%Y%m%dT%H%M%SZ)"
LOCAL="$DS_WORK/runs/$RUN"; mkdir -p "$LOCAL"
HOST_RUN="$DS_HOST_ROOT/runs/$RUN"
K=$DS_K
# timestamps as T_<name> (macOS bash 3.2 has no associative arrays)
mark() { eval "T_$1=$(date +%s)"; ds_log "[$1] $(date -u +%T) $2"; }

# ── pre-checks ──────────────────────────────────────────────────────────────
st=$(curl -sS -m 15 "$DS_RPC/status")
tag=$(printf '%s' "$st" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["chain_id"]=="splendor-quantum-devnet-1"; print(d["image_tag"])')
[ "$tag" = "$DS_REQUIRED_TAG" ] || ds_die "devnet image_tag=$tag, need $DS_REQUIRED_TAG"
ssm_sh "$DS_INSTANCE" "
running=\$(docker ps -q --filter name=sqc-load-c | wc -l); echo drivers_running=\$running
units=\$(systemctl list-units --no-legend 'sqc-load-watch-*' | wc -l); echo watch_units=\$units
curl -fsS -m 5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r '\"proxy=\" + .chain_id'
test -x $DS_HOST_ROOT/bin/txload && echo txload=ok
ls $DS_HOST_ROOT/keys/*.seed | wc -l | sed 's/^/seeds=/'
n=\$(docker ps -q --filter name=sqc-node-1); echo node_id=\$(docker inspect -f '{{.Id}}' \$n | cut -c1-12)
echo node_started=\$(docker inspect -f '{{.State.StartedAt}}' \$n)
echo valset=\$(curl -s -m5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r .validator_set_size)
echo valcontainers=\$(docker ps --format '{{.Names}}' | grep -c sqc-val)
" 60 > "$LOCAL/precheck.txt"
grep -q '^drivers_running=0' "$LOCAL/precheck.txt" || ds_die "drivers already running on the host"
grep -q '^watch_units=0' "$LOCAL/precheck.txt" || ds_die "a watcher unit is still active (35-watch.sh stop <run>)"
grep -q "^proxy=$DS_CHAIN_ID" "$LOCAL/precheck.txt" || ds_die "rpc proxy is not answering (05-build-and-stage.sh)"
grep -q '^txload=ok' "$LOCAL/precheck.txt" || ds_die "txload missing on host"
grep -q "^seeds=$K" "$LOCAL/precheck.txt" || ds_die "expected $K funder seeds on host"
need=505; [ "$STAGE" = sustained ] && need=5
for i in $(seq -w 0 $((K - 1))); do
  a=$(cat "$DS_WORK/keys/funder-$i.address")
  b=$(curl -sS -m 15 "$DS_RPC/balance/$a" | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["balance"])//10**18)')
  [ "$b" -ge "$need" ] || ds_die "funder-$i holds $b SPLD (< $need)"
done
ds_log "stage $STAGE: K=$K x N=$N = $((K * N)) accounts; run $RUN; funders hold >= $need SPLD"

# ── helpers ──────────────────────────────────────────────────────────────────
start_pass() { # <pass> <extra txload flags>
  local pass="$1" flags="$2" f
  f=$(mktemp); cat > "$f" <<EOS
#!/bin/bash
set -euo pipefail
for i in \$(seq -w 0 $((K - 1))); do
  d=$HOST_RUN/$pass/c\$i; mkdir -p "\$d"
  docker run -d --name sqc-load-c\$i --network $DS_NODE_NET --cpus $DS_DRIVER_CPUS --memory $DS_DRIVER_MEM \\
    -e GOMEMLIMIT=40MiB --security-opt no-new-privileges:true --restart no \\
    --log-opt max-size=5m --log-opt max-file=1 -v $DS_HOST_ROOT:$DS_HOST_ROOT \\
    $DS_DRIVER_IMAGE sh -c "exec $DS_HOST_ROOT/bin/txload run --rpc http://$DS_NODE_SVC:8080 --chain-id $DS_CHAIN_ID \\
      --seed $DS_HOST_ROOT/keys/funder-\$i.seed --senders $N --fund $DS_SENDER_FUND_UNITS \\
      --amount-min $DS_TX_AMOUNT_UNITS --amount-max $DS_TX_AMOUNT_UNITS \\
      --lazy-keys --scan-node -1 --topup-every 0 --sample 50 --interval 5s --concurrency 16 $flags \\
      --stats \$d/stats.jsonl --summary \$d/summary.json --pid \$d/txload.pid >> \$d/txload.log 2>&1" >/dev/null
done
echo "started \$(docker ps -q --filter name=sqc-load-c | wc -l) drivers ($pass)"
EOS
  ssm_run "$DS_INSTANCE" "$f" 300 | tail -1; rm -f "$f"
}

# Wait on the host until every driver has exited (or the timeout); one short
# progress line every 30 s so the SSM output stays under its 24k cap. With a
# done-pattern, the drivers are stopped (SIGTERM, 30 s grace, so summary.json
# is written) as soon as every txload.log shows it — the FUND pass has nothing
# left to do once funding is confirmed.
wait_drivers() { # <pass> <timeout-s> [done-pattern]
  local pass="$1" timeout="$2" donepat="${3:-}"
  ssm_sh "$DS_INSTANCE" "
t0=\$(date +%s)
while :; do
  n=\$(docker ps -q --filter name=sqc-load-c | wc -l)
  st=\$(curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status || echo '{}')
  rule=\$(tail -1 $HOST_RUN/watch.jsonl 2>/dev/null | jq -r '.rule // \"\"')
  fin=\$(grep -l -E '$donepat' $HOST_RUN/$pass/c*/txload.log 2>/dev/null | wc -l)
  echo \"\$(date -u +%T) drivers=\$n height=\$(echo \"\$st\" | jq -r .height) pool=\$(echo \"\$st\" | jq -r .mempool_size) finished=\$fin rule=[\$rule]\"
  [ \"\$n\" -eq 0 ] && { echo done; exit 0; }
  if [ -n '$donepat' ] && [ \"\$fin\" -ge $K ]; then echo 'all drivers report done: stopping them'; docker ps -q --filter name=sqc-load-c | xargs -r docker stop -t 30 >/dev/null; echo done; exit 0; fi
  [ \$(( \$(date +%s) - t0 )) -ge $timeout ] && { echo 'TIMEOUT: stopping drivers'; docker ps -q --filter name=sqc-load-c | xargs -r docker stop -t 30 >/dev/null; exit 0; }
  sleep 15
done" $((timeout + 120)) | tee "$LOCAL/$pass.wait.log" | tail -3
  ssm_sh "$DS_INSTANCE" "
for c in \$(docker ps -aq --filter name=sqc-load-c); do docker inspect -f '{{.Name}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}}' \$c; done | sort
docker rm -f \$(docker ps -aq --filter name=sqc-load-c) >/dev/null 2>&1 || true
grep -h 'txload summary' $HOST_RUN/$pass/c*/txload.log | sed 's/^/  /' | head -3
grep -l 'funding:' $HOST_RUN/$pass/c*/txload.log 2>/dev/null | sed 's/^/FUNDING FAILED: /' || true
" 120 | tee "$LOCAL/$pass.exit.txt" | grep -c 'exit=0' | sed 's/^/  drivers with exit 0: /'
}

# Wait until mempool_size <= 2 for 3 consecutive minutes (18 polls, 10 s apart).
wait_quiet() { # <label> <timeout-s>
  ssm_sh "$DS_INSTANCE" "
t0=\$(date +%s); z=0
while :; do
  p=\$(curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r '.mempool_size // -1')
  if [ \"\$p\" -ge 0 ] && [ \"\$p\" -le 2 ]; then z=\$((z+1)); else z=0; fi
  [ \$((z % 6)) -eq 0 ] && echo \"\$(date -u +%T) pool=\$p quiet=\$((z*10))s\"
  [ \"\$z\" -ge 18 ] && { echo quiet; exit 0; }
  [ \$(( \$(date +%s) - t0 )) -ge $2 ] && { echo 'TIMEOUT: mempool never quiet'; exit 1; }
  sleep 10
done" $(($2 + 120)) | tee "$LOCAL/$1.quiet.log" | tail -2
}

# ── the stage ────────────────────────────────────────────────────────────────
mark run_start "watcher + $STAGE"
"$DS_HERE/35-watch.sh" start "$RUN"
if [ $SKIP_FUND = 0 ]; then
  mark fund_start "FUND pass: $K x $N senders, 1 SPLD each where below 0.25"
  start_pass fund "--rate 0.001 --duration 8m --fund-deadline 6m"
  wait_drivers fund 600 'waiting for funding to confirm ok|no funding needed|^funding:' 
  mark fund_end "fund pass finished"
  wait_quiet fund 900 || ds_warn "mempool did not go quiet after funding — continuing, recorded"
  mark quiet_end "mempool quiet"
fi
if [ "$STAGE" = sustained ]; then
  mark burst_start "SUSTAINED pass: $K x --rate 12 for 15 min"
  start_pass sustained "--rate 12 --duration 15m"
  wait_drivers sustained 1080
else
  mark burst_start "BURST pass: $((K * N)) one-shot senders at $K x 24 tx/s"
  start_pass burst "--rate 24 --one-shot --duration 20m"
  wait_drivers burst 1260
fi
mark burst_end "drivers exited"
wait_quiet drain 1200 || ds_warn "mempool did not drain within 20 min — recorded"
mark drain_end "mempool drained"
"$DS_HERE/35-watch.sh" stop "$RUN"
mark run_end "stage complete"

python3 - "$LOCAL/run.json" "$RUN" "$STAGE" "$N" "$K" "$tag" "$SKIP_FUND" "$T_run_start" "${T_fund_start:-0}" "${T_fund_end:-0}" "${T_quiet_end:-0}" "$T_burst_start" "$T_burst_end" "$T_drain_end" "$T_run_end" "$DS_DRIVER_CPUS" "$DS_DRIVER_MEM" <<'PY'
import json, sys
a = sys.argv
json.dump({"run": a[2], "stage": a[3], "senders_per_container": int(a[4]), "containers": int(a[5]), "accounts": int(a[4]) * int(a[5]),
           "image_tag": a[6], "skip_fund": a[7] == "1",
           "t": {"run_start": int(a[8]), "fund_start": int(a[9]), "fund_end": int(a[10]), "quiet_end": int(a[11]),
                 "burst_start": int(a[12]), "burst_end": int(a[13]), "drain_end": int(a[14]), "run_end": int(a[15])},
           "driver_cpus": a[16], "driver_mem": a[17],
           "mode": "sustained" if a[3] == "sustained" else "burst"}, open(a[1], "w"), indent=2)
PY
# Topology fingerprint at the end: if the node container id, its StartedAt or the
# validator-set size changed since the precheck, an external redeploy touched
# devnet during the stage and the numbers are not attributable to load alone.
ssm_sh "$DS_INSTANCE" "
n=\$(docker ps -q --filter name=sqc-node-1); echo node_id=\$(docker inspect -f '{{.Id}}' \$n | cut -c1-12)
echo node_started=\$(docker inspect -f '{{.State.StartedAt}}' \$n)
echo node_restarts=\$(docker inspect -f '{{.RestartCount}}' \$n)
echo node_oom=\$(docker inspect -f '{{.State.OOMKilled}}' \$n)
echo valset=\$(curl -s -m5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r .validator_set_size)
echo valcontainers=\$(docker ps --format '{{.Names}}' | grep -c sqc-val)
" 60 > "$LOCAL/postcheck.txt"
python3 - "$LOCAL/run.json" "$LOCAL/precheck.txt" "$LOCAL/postcheck.txt" <<'PYEOF'
import json,sys
run=json.load(open(sys.argv[1]))
def kv(p):
    d={}
    for l in open(p):
        if "=" in l: k,v=l.strip().split("=",1); d[k]=v
    return d
pre,post=kv(sys.argv[2]),kv(sys.argv[3])
changed=[]
for k in ("node_id","node_started","valset"):
    if pre.get(k) and post.get(k) and pre[k]!=post[k]: changed.append("%s %s->%s"%(k,pre[k],post[k]))
run["topology"]={"pre":pre,"post":post,"changed":changed,
    "external_interference": bool(changed) or int(post.get("valset","1") or 1)>1 or int(post.get("valcontainers","0") or 0)>0}
json.dump(run,open(sys.argv[1],"w"),indent=2)
print("topology changed mid-stage:",changed or "no","| valset=%s valcontainers=%s"%(post.get("valset"),post.get("valcontainers")))
PYEOF
ds_log "run.json: $LOCAL/run.json"
[ $COLLECT = 1 ] && "$DS_HERE/40-collect.sh" "$RUN"
