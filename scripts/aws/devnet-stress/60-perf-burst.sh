#!/usr/bin/env bash
# One FIXED, repeatable comparison burst, fired unchanged N times so the only
# variable is the devnet validator-set size.
#
#   60-perf-burst.sh check                 host/chain readiness (run before every firing)
#   60-perf-burst.sh fund                  pre-fund the sender block ONCE (reused by every firing)
#   60-perf-burst.sh balances              confirm the funded senders still hold enough
#   60-perf-burst.sh fire <label>          fire the burst once (label e.g. perf-n1) and collect
#
# The burst is exactly the clean s10k burst: 20 containers x 500 one-shot
# senders = 10,000 accounts, --rate 24 per container, same --fund/--amount/
# --cpus/--memory/--interval. The ONLY deviation is --sender-offset
# $DS_PERF_OFFSET, a block disjoint from every earlier stage, so the same
# accounts can be reused for all firings without colliding with s10k/s20k/s30k.
#
# Senders are funded once (fund) and keep their balance across firings: each
# firing spends 0.001 SPLD + fee per sender, far below the 0.25 SPLD top-up
# threshold, so no firing re-funds and every firing is identical work.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"

K=$DS_K
N=${DS_PERF_SENDERS:-500}
OFF=${DS_PERF_OFFSET:-500000}
RATE=${DS_PERF_RATE:-24}

perf_flags() { # <pass> <extra flags>  — the fixed driver command
  echo "--senders $N --sender-offset $OFF --fund $DS_SENDER_FUND_UNITS" \
       "--amount-min $DS_TX_AMOUNT_UNITS --amount-max $DS_TX_AMOUNT_UNITS" \
       "--lazy-keys --scan-node -1 --topup-every 0 --sample 50 --interval 5s --concurrency 16 $2"
}

start_pass() { # <run-id> <pass> <extra txload flags>
  local run="$1" pass="$2" flags="$3" f
  f=$(mktemp); cat > "$f" <<EOS
#!/bin/bash
set -euo pipefail
for i in \$(seq -w 0 $((K - 1))); do
  d=$DS_HOST_ROOT/runs/$run/$pass/c\$i; mkdir -p "\$d"
  docker run -d --name sqc-load-c\$i --network $DS_NODE_NET --cpus $DS_DRIVER_CPUS --memory $DS_DRIVER_MEM \\
    -e GOMEMLIMIT=40MiB --security-opt no-new-privileges:true --restart no \\
    --log-opt max-size=5m --log-opt max-file=1 -v $DS_HOST_ROOT:$DS_HOST_ROOT \\
    $DS_DRIVER_IMAGE sh -c "exec $DS_HOST_ROOT/bin/txload run --rpc http://$DS_NODE_SVC:8080 --chain-id $DS_CHAIN_ID \\
      --seed $DS_HOST_ROOT/keys/funder-\$i.seed $(perf_flags "$pass" "$flags") \\
      --stats \$d/stats.jsonl --summary \$d/summary.json --pid \$d/txload.pid >> \$d/txload.log 2>&1" >/dev/null
done
echo "started \$(docker ps -q --filter name=sqc-load-c | wc -l) drivers ($pass)"
EOS
  ssm_run "$DS_INSTANCE" "$f" 300 | tail -1; rm -f "$f"
}

wait_drivers() { # <run-id> <pass> <timeout-s> [done-pattern]
  local run="$1" pass="$2" timeout="$3" donepat="${4:-}"
  ssm_sh "$DS_INSTANCE" "
t0=\$(date +%s)
while :; do
  n=\$(docker ps -q --filter name=sqc-load-c | wc -l)
  st=\$(curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status || echo '{}')
  rule=\$(tail -1 $DS_HOST_ROOT/runs/$run/watch.jsonl 2>/dev/null | jq -r '.rule // \"\"')
  fin=\$(grep -l -E '$donepat' $DS_HOST_ROOT/runs/$run/$pass/c*/txload.log 2>/dev/null | wc -l)
  echo \"\$(date -u +%T) drivers=\$n height=\$(echo \"\$st\" | jq -r .height) pool=\$(echo \"\$st\" | jq -r .mempool_size) finished=\$fin rule=[\$rule]\"
  [ \"\$n\" -eq 0 ] && { echo done; exit 0; }
  if [ -n '$donepat' ] && [ \"\$fin\" -ge $K ]; then echo 'all drivers report done: stopping them'; docker ps -q --filter name=sqc-load-c | xargs -r docker stop -t 30 >/dev/null; echo done; exit 0; fi
  [ \$(( \$(date +%s) - t0 )) -ge $timeout ] && { echo 'TIMEOUT: stopping drivers'; docker ps -q --filter name=sqc-load-c | xargs -r docker stop -t 30 >/dev/null; exit 0; }
  sleep 10
done" $((timeout + 120))
  ssm_sh "$DS_INSTANCE" "
for c in \$(docker ps -aq --filter name=sqc-load-c); do docker inspect -f '{{.Name}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}}' \$c; done | sort | uniq -c -f1 | head -4
docker rm -f \$(docker ps -aq --filter name=sqc-load-c) >/dev/null 2>&1 || true" 120
}

wait_quiet() { # <timeout-s>
  ssm_sh "$DS_INSTANCE" "
t0=\$(date +%s); z=0
while :; do
  p=\$(curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r '.mempool_size // -1')
  if [ \"\$p\" -ge 0 ] && [ \"\$p\" -le 2 ]; then z=\$((z+1)); else z=0; fi
  [ \$((z % 6)) -eq 0 ] && [ \$z -gt 0 ] && echo \"\$(date -u +%T) pool=\$p quiet=\$((z*10))s\"
  [ \"\$z\" -ge 18 ] && { echo quiet; exit 0; }
  [ \$(( \$(date +%s) - t0 )) -ge $1 ] && { echo 'TIMEOUT: mempool never quiet'; exit 1; }
  sleep 10
done" $(($1 + 120)) | tail -2
}

fingerprint() { # -> key=value lines
  ssm_sh "$DS_INSTANCE" "
n=\$(docker ps -q --filter name=sqc-node-1)
echo node_id=\$(docker inspect -f '{{.Id}}' \$n | cut -c1-12)
echo node_started=\$(docker inspect -f '{{.State.StartedAt}}' \$n)
echo node_restarts=\$(docker inspect -f '{{.RestartCount}}' \$n)
echo node_oom=\$(docker inspect -f '{{.State.OOMKilled}}' \$n)
st=\$(curl -s -m5 http://127.0.0.1:$DS_PROXY_PORT/status)
echo valset=\$(echo \"\$st\" | jq -r .validator_set_size)
echo quorum_required=\$(echo \"\$st\" | jq -r .head_quorum_required)
echo qc_votes=\$(echo \"\$st\" | jq -r .head_qc_votes)
echo peers=\$(echo \"\$st\" | jq -r .peer_count)
echo genesis=\$(echo \"\$st\" | jq -r .genesis_hash | cut -c1-8)
echo valcontainers=\$(docker ps --format '{{.Names}}' | grep -c sqc-val || true)" 90
}

case "${1:-}" in
check)
  ds_log "host + chain readiness"
  ssm_sh "$DS_INSTANCE" "
test -x $DS_HOST_ROOT/bin/txload && echo txload=ok || echo txload=MISSING
echo seeds=\$(ls $DS_HOST_ROOT/keys/*.seed 2>/dev/null | wc -l)
echo drivers_running=\$(docker ps -q --filter name=sqc-load-c | wc -l)
echo watch_units=\$(systemctl list-units --no-legend 'sqc-load-watch-*' | wc -l)
docker network inspect $DS_NODE_NET >/dev/null 2>&1 && echo network=ok || echo network=MISSING
if curl -fsS -m 5 http://127.0.0.1:$DS_PROXY_PORT/status >/dev/null 2>&1; then echo proxy=ok; else
  echo proxy=DOWN; docker rm -f sqc-load-rpcproxy >/dev/null 2>&1 || true
  docker run -d --name sqc-load-rpcproxy --restart unless-stopped --network $DS_NODE_NET \\
    --security-opt no-new-privileges:true -p 127.0.0.1:$DS_PROXY_PORT:$DS_PROXY_PORT \\
    --log-opt max-size=5m --log-opt max-file=1 \\
    $DS_SOCAT_IMAGE TCP-LISTEN:$DS_PROXY_PORT,fork,reuseaddr TCP:$DS_NODE_SVC:8080 >/dev/null && sleep 3 && \\
    curl -fsS -m 5 http://127.0.0.1:$DS_PROXY_PORT/status >/dev/null && echo proxy=recreated
fi
docker run --rm --network $DS_NODE_NET $DS_DRIVER_IMAGE sh -c 'wget -qO- -T5 http://$DS_NODE_SVC:8080/status' 2>/dev/null | jq -c '{chain_id,height,mempool_size,valset:.validator_set_size,genesis:.genesis_hash[0:8]}' || echo driver_network=UNREACHABLE" 180
  fingerprint
  ;;
fund)
  ds_log "one-time funding of the perf sender block: $K x $N senders at offset $OFF ($((K*N)) accounts, 1 SPLD each)"
  RUN="perf-fund-$(date -u +%Y%m%dT%H%M%SZ)"
  start_pass "$RUN" fund "--rate 0.001 --duration $DS_FUND_DURATION --fund-deadline $DS_FUND_DEADLINE"
  wait_drivers "$RUN" fund "$DS_FUND_WAIT_S" 'waiting for funding to confirm ok|no funding needed|^funding:' | tail -6
  ds_log "funding pass finished; verifying balances"
  "$0" balances
  ;;
balances)
  # Sample senders across the block from three funders; every sender in the
  # block was funded by the same pass, so a sample is enough to confirm.
  TX="$DS_WORK/bin/txload-local"
  for i in 00 09 19; do
    addrs=$("$TX" addrs --rpc "$DS_RPC" --chain-id "$DS_CHAIN_ID" --seed "$DS_WORK/keys/funder-$i.seed" \
            --senders 3 --sender-offset "$OFF" | awk '/^s/ {print $2}')
    for a in $addrs; do printf 'funder-%s %s %s SPLD\n' "$i" "${a:0:16}" "$(ds_balance_spld "$a")"; done
  done
  ;;
fire)
  LABEL="${2:?label required, e.g. perf-n1}"
  RUN="$LABEL-$(date -u +%Y%m%dT%H%M%SZ)"
  LOCAL="$DS_WORK/runs/$RUN"; mkdir -p "$LOCAL"
  st=$(curl -sS -m 15 "$DS_RPC/status")
  printf '%s' "$st" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["chain_id"]=="splendor-quantum-devnet-1", d["chain_id"]'
  fingerprint > "$LOCAL/precheck.txt"; cat "$LOCAL/precheck.txt"
  grep -q '^proxy=DOWN' "$LOCAL/precheck.txt" && ds_die "rpc proxy down — run: 60-perf-burst.sh check"
  T_run_start=$(date +%s)
  ds_log "[$LABEL] watcher up"
  "$DS_HERE/35-watch.sh" start "$RUN" >/dev/null
  T_burst_start=$(date +%s)
  ds_log "[$LABEL] FIRE: $((K*N)) one-shot senders at $K x $RATE tx/s"
  start_pass "$RUN" burst "--rate $RATE --one-shot --duration 20m"
  wait_drivers "$RUN" burst 1260 | tail -5
  T_burst_end=$(date +%s)
  ds_log "[$LABEL] drivers exited; draining"
  wait_quiet 1200 || ds_warn "mempool did not drain in 20 min — recorded"
  T_drain_end=$(date +%s)
  "$DS_HERE/35-watch.sh" stop "$RUN" >/dev/null || true
  T_run_end=$(date +%s)
  fingerprint > "$LOCAL/postcheck.txt"
  python3 - "$LOCAL/run.json" "$RUN" "$LABEL" "$N" "$K" "$OFF" "$RATE" \
    "$T_run_start" "$T_burst_start" "$T_burst_end" "$T_drain_end" "$T_run_end" \
    "$DS_DRIVER_CPUS" "$DS_DRIVER_MEM" "$LOCAL/precheck.txt" "$LOCAL/postcheck.txt" <<'PY'
import json, sys
a = sys.argv
def kv(p):
    d = {}
    for l in open(p):
        if "=" in l: k, v = l.strip().split("=", 1); d[k] = v
    return d
pre, post = kv(a[15]), kv(a[16])
changed = [f"{k} {pre[k]}->{post[k]}" for k in ("node_id", "node_started", "valset")
           if pre.get(k) and post.get(k) and pre[k] != post[k]]
json.dump({"run": a[2], "stage": a[3], "senders_per_container": int(a[4]), "containers": int(a[5]),
           "accounts": int(a[4]) * int(a[5]), "sender_offset": int(a[6]), "rate_per_container": float(a[7]),
           "image_tag": "v1.1.1", "skip_fund": True, "mode": "burst",
           "t": {"run_start": int(a[8]), "fund_start": 0, "fund_end": 0, "quiet_end": 0,
                 "burst_start": int(a[9]), "burst_end": int(a[10]), "drain_end": int(a[11]), "run_end": int(a[12])},
           "driver_cpus": a[13], "driver_mem": a[14],
           "topology": {"pre": pre, "post": post, "changed": changed,
                        "external_interference": bool(changed)},
           "validators": {"valset": post.get("valset"), "quorum_required": post.get("quorum_required"),
                          "qc_votes": post.get("qc_votes"), "peers": post.get("peers")}},
          open(a[1], "w"), indent=2)
print("valset=%s quorum=%s votes=%s peers=%s | changed mid-run: %s" % (
    post.get("valset"), post.get("quorum_required"), post.get("qc_votes"), post.get("peers"), changed or "no"))
PY
  ds_log "[$LABEL] collecting"
  "$DS_HERE/40-collect.sh" "$RUN"
  ds_log "[$LABEL] done -> $LOCAL"
  ;;
*) sed -n '2,20p' "$0"; exit 2 ;;
esac
