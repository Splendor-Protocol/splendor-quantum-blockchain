#!/usr/bin/env bash
# Control the host watcher (lib/watch-host.sh, installed at /srv/sqc-load/watch.sh).
#
#   35-watch.sh start <run-id>     systemd-run --unit sqc-load-watch-<run-id>
#   35-watch.sh stop  <run-id>
#   35-watch.sh status             active watcher units + running drivers
#   35-watch.sh tail  <run-id> [n] last n watch.jsonl lines (default 5)
#   35-watch.sh peek  <run-id>     one-line node state + per-driver progress
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"
cmd="${1:-}"; run="${2:-}"
case "$cmd" in
start)
  [ -n "$run" ] || ds_die "run id required"
  ssm_sh "$DS_INSTANCE" "systemd-run --unit sqc-load-watch-$run --collect --setenv=PROXY_PORT=$DS_PROXY_PORT $DS_HOST_ROOT/watch.sh $run 2>&1; sleep 12; tail -1 $DS_HOST_ROOT/runs/$run/watch.jsonl" 90 ;;
stop)
  [ -n "$run" ] || ds_die "run id required"
  ssm_sh "$DS_INSTANCE" "systemctl stop sqc-load-watch-$run 2>&1 || true; systemctl reset-failed sqc-load-watch-$run 2>/dev/null || true; wc -l < $DS_HOST_ROOT/runs/$run/watch.jsonl | sed 's/^/watch lines: /'" 90 ;;
status)
  ssm_sh "$DS_INSTANCE" "systemctl list-units --no-legend 'sqc-load-watch-*'; echo drivers=\$(docker ps -q --filter name=sqc-load-c | wc -l); curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -c '{height,mempool_size,image_tag}'" 60 ;;
tail)
  [ -n "$run" ] || ds_die "run id required"
  ssm_sh "$DS_INSTANCE" "tail -${3:-5} $DS_HOST_ROOT/runs/$run/watch.jsonl; cat $DS_HOST_ROOT/runs/$run/stop-rule.txt 2>/dev/null | sed 's/^/STOP RULE: /'" 60 ;;
peek)
  [ -n "$run" ] || ds_die "run id required"
  ssm_sh "$DS_INSTANCE" "
tail -1 $DS_HOST_ROOT/runs/$run/watch.jsonl | jq -c '{ts,healthz,height,mempool,restarts,oom,mem_avail_mb,load1,drivers,node_cpu,drivers_cpu_pct,bt_120s,rule}'
for d in $DS_HOST_ROOT/runs/$run/*/c*; do [ -f \$d/stats.jsonl ] && echo \"\$(basename \$(dirname \$d))/\$(basename \$d) \$(tail -1 \$d/stats.jsonl | jq -c '{elapsed_s,accepted,accept_rate_tps,rate_limited_429,cum_accepted,cum_rejected,one_shot_remaining,rtt_ms_p95,incl_s_p95}')\"; done 2>/dev/null | tail -20
grep -h 'txload:' $DS_HOST_ROOT/runs/$run/*/c00/txload.log 2>/dev/null | tail -3" 90 ;;
*) sed -n '2,9p' "$0"; exit 2 ;;
esac
