#!/bin/bash
# sqc-load watcher — runs ON the devnet validator host (installed by
# 05-build-and-stage.sh, started by 35-watch.sh through systemd-run).
#
#   watch-host.sh <run-id>
#
# Every 10 s it appends one JSON line to /srv/sqc-load/runs/<run-id>/watch.jsonl:
# /healthz (through the sqc-load-rpcproxy sidecar), /status height + mempool,
# the node container's RestartCount / OOMKilled, MemAvailable, load1, dmesg
# OOM kills, docker stats for the node and the driver containers, and the
# average block time over the last 120 s.
#
# Stop rules — they stop the DRIVER containers only, never the node, and the
# rule that fired is recorded in the line (field "rule") and in stop-rule.txt:
#   healthz not 200 for 60 s | height unchanged for 120 s |
#   MemAvailable < 100 MB   | average block time > 5 s over the last 2 min
set -u
RUN="${1:?run id}"
ROOT=/srv/sqc-load
DIR="$ROOT/runs/$RUN"
OUT="$DIR/watch.jsonl"
PORT="${PROXY_PORT:-18080}"
mkdir -p "$DIR"
hz_fail=0
triggered=""
hist=()   # "epoch:height" samples of the last ~130 s

fire() {
  local rule="$1"
  [ -n "$triggered" ] && return
  triggered="$rule"
  echo "$(date -u +%FT%TZ) $rule" >> "$DIR/stop-rule.txt"
  # SIGTERM with a 30 s grace so txload writes its summary.json.
  docker ps -q --filter name=sqc-load-c | xargs -r docker stop -t 30 >/dev/null 2>&1 &
}

while :; do
  tick=$(date +%s)
  t0=$(date +%s%N)
  # curl prints %{http_code} (000 on failure) itself; a `|| echo 000` fallback
  # would concatenate a second 000 and emit invalid JSON ("healthz":000000).
  hz=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/healthz" 2>/dev/null)
  hz=${hz:-000}; hz=$((10#$hz))
  hz_ms=$(( ($(date +%s%N) - t0) / 1000000 ))
  st=$(curl -s -m 5 "http://127.0.0.1:$PORT/status" 2>/dev/null || echo '{}')
  height=$(printf '%s' "$st" | jq -r '.height // 0' 2>/dev/null || echo 0)
  pool=$(printf '%s' "$st" | jq -r '.mempool_size // -1' 2>/dev/null || echo -1)
  nts=$(printf '%s' "$st" | jq -r '.timestamp // 0' 2>/dev/null || echo 0)
  node=$(docker ps -q --filter name=sqc-node | head -1)
  if [ -n "$node" ]; then
    read -r restarts oom nstate < <(docker inspect -f '{{.RestartCount}} {{.State.OOMKilled}} {{.State.Status}}' "$node" 2>/dev/null || echo "-1 unknown gone")
  else
    restarts=-1; oom=unknown; nstate=gone
  fi
  mem_avail=$(awk '/MemAvailable/ {printf "%d", $2/1024}' /proc/meminfo)
  load1=$(cut -d' ' -f1 /proc/loadavg)
  killed=$(dmesg -T 2>/dev/null | grep -ci 'killed process' || true); killed=${killed:-0}
  drivers=$(docker ps -q --filter name=sqc-load-c | wc -l | tr -d ' ')
  stats=$(timeout 8 docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' 2>/dev/null || true)
  node_cpu=$(printf '%s\n' "$stats" | awk '/sqc-node-1/ {print $2}' | head -1); node_cpu=${node_cpu:-"?"}
  node_mem=$(printf '%s\n' "$stats" | awk '/sqc-node-1/ {print $3}' | head -1); node_mem=${node_mem:-"?"}
  drv_cpu=$(printf '%s\n' "$stats" | awk '/sqc-load-c/ {c += $2} END {printf "%.1f", c+0}')
  drv_mem_mb=$(printf '%s\n' "$stats" | awk '/sqc-load-c/ {v=$3; u=substr(v,length(v)-2); n=v+0; if (u ~ /GiB/) n*=1024; if (u ~ /KiB/) n/=1024; m += n} END {printf "%.0f", m+0}')

  # block time over the trailing 120 s
  hist+=("$tick:$height")
  while [ ${#hist[@]} -gt 0 ] && [ $(( tick - ${hist[0]%%:*} )) -gt 130 ]; do hist=("${hist[@]:1}"); done
  bt=""
  oldest="${hist[0]}"; oe=${oldest%%:*}; oh=${oldest##*:}
  span=$(( tick - oe ))
  if [ "$span" -ge 110 ] && [ "$height" -gt 0 ] && [ "$oh" -gt 0 ]; then
    delta=$(( height - oh ))
    if [ "$delta" -le 0 ]; then
      bt="inf"; fire "height stalled for ${span}s at $height"
    else
      bt=$(awk -v s="$span" -v d="$delta" 'BEGIN {printf "%.2f", s/d}')
      awk -v b="$bt" 'BEGIN {exit !(b > 5)}' && fire "average block time ${bt}s > 5s over ${span}s"
    fi
  fi
  if [ "$hz" != "200" ]; then hz_fail=$((hz_fail + 10)); else hz_fail=0; fi
  [ "$hz_fail" -ge 60 ] && fire "healthz not 200 for ${hz_fail}s (last $hz)"
  [ "$mem_avail" -lt 100 ] && fire "MemAvailable ${mem_avail}MB < 100MB"

  printf '{"ts":"%s","epoch":%d,"healthz":%s,"healthz_ms":%d,"height":%s,"mempool":%s,"node_ts":%s,"restarts":%s,"oom":"%s","node_state":"%s","mem_avail_mb":%s,"load1":%s,"killed":%s,"drivers":%s,"node_cpu":"%s","node_mem":"%s","drivers_cpu_pct":%s,"drivers_mem_mb":%s,"bt_120s":"%s","rule":"%s"}\n' \
    "$(date -u +%FT%TZ)" "$tick" "$hz" "$hz_ms" "$height" "$pool" "$nts" "$restarts" "$oom" "$nstate" "$mem_avail" "$load1" "$killed" "$drivers" "$node_cpu" "$node_mem" "$drv_cpu" "$drv_mem_mb" "$bt" "$triggered" >> "$OUT"

  el=$(( $(date +%s) - tick ))
  [ "$el" -lt 10 ] && sleep $(( 10 - el ))
done
