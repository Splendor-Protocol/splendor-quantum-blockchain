#!/usr/bin/env bash
# Real transaction traffic against the feat/validator AWS lab (sqc-featval-1).
#
# Funds a deterministic sender pool from the lab's rogue wallet (not the
# faucet: another driver may be spending that key) and streams ML-DSA-65
# transfers at a fixed rate across ALL nodes, so every producer's blocks carry
# real transactions and the explorer's Recent Transactions panel stays
# populated. Per-interval stats (JSONL) and an end-of-run summary land in
# $SQC_TEST_DIR/txload/<name>/. Driver: cmd/txload (see its package comment).
#
#   30-txload.sh build                          # build $SQC_TEST_DIR/bin/txload-local
#   30-txload.sh start [name] [driver flags]    # background run; pid/log/stats under txload/<name>/
#   30-txload.sh stop  [name]                   # SIGTERM; the driver writes summary.json on the way out
#   30-txload.sh status [name]                  # pid, last stats line, node heights
#   30-txload.sh run   [driver flags]           # foreground run (Ctrl-C stops)
#   30-txload.sh addrs [driver flags]           # print the sender addresses a config would use
#
# Defaults: --senders 8 --rate 1.2 (≈100k tx/day) --duration 0 (until stopped)
# --sample 5 --interval 30s. Any driver flag given after the name overrides.
# A second concurrent run MUST use its own --sender-offset (e.g. 100).
set -euo pipefail
source "$(dirname "$0")/env.sh"

DRIVER="$SQC_TEST_DIR/bin/txload-local"
KEYS="$SQC_TEST_DIR/keys"
ROOT="$SQC_TEST_DIR/txload"

build() {
  cd "$SQC_REPO_ROOT" && GOTOOLCHAIN=auto go build -trimpath -o "$DRIVER" ./cmd/txload
  sqc_log "built $DRIVER"
}

rpcs() {
  if [ -n "${SQC_TXLOAD_RPC:-}" ]; then echo "$SQC_TXLOAD_RPC"; return; fi
  [ -f "$SQC_TEST_DIR/instances.full.txt" ] || sqc_die "no instances.full.txt; set SQC_TXLOAD_RPC=http://host:8080[,...]"
  awk '{printf "%shttp://%s:8080", (NR>1?",":""), $4}' "$SQC_TEST_DIR/instances.full.txt"
}

common_flags() {
  local name="$1"
  echo --rpc "$(rpcs)" --chain-id "$SQC_TEST_CHAIN_ID" \
       --wallet "$KEYS/rogue.sqwallet" --pass-file "$KEYS/pass.txt" \
       --senders 8 --rate 1.2 --duration 0 --sample 5 --interval 30s \
       --stats "$ROOT/$name/stats.jsonl" --summary "$ROOT/$name/summary.json" --pid "$ROOT/$name/txload.pid"
}

cmd="${1:-}"; shift || true
case "$cmd" in
build) build ;;
run)
  [ -x "$DRIVER" ] || build
  mkdir -p "$ROOT/fg"
  # shellcheck disable=SC2046
  exec "$DRIVER" run $(common_flags fg) "$@"
  ;;
addrs)
  [ -x "$DRIVER" ] || build
  exec "$DRIVER" addrs --rpc "$(rpcs)" --chain-id "$SQC_TEST_CHAIN_ID" --wallet "$KEYS/rogue.sqwallet" --pass-file "$KEYS/pass.txt" "$@"
  ;;
start)
  name="${1:-steady}"; shift || true
  [ -x "$DRIVER" ] || build
  mkdir -p "$ROOT/$name"
  if [ -f "$ROOT/$name/txload.pid" ] && kill -0 "$(cat "$ROOT/$name/txload.pid")" 2>/dev/null; then
    sqc_die "run '$name' already up (pid $(cat "$ROOT/$name/txload.pid")); stop it first"
  fi
  # shellcheck disable=SC2046
  nohup "$DRIVER" run $(common_flags "$name") "$@" >> "$ROOT/$name/txload.log" 2>&1 &
  sqc_log "started '$name' (pid $!): log $ROOT/$name/txload.log, stats $ROOT/$name/stats.jsonl"
  ;;
stop)
  name="${1:-steady}"
  pidf="$ROOT/$name/txload.pid"
  [ -f "$pidf" ] || sqc_die "no pid file for '$name'"
  pid="$(cat "$pidf")"
  kill -TERM "$pid" 2>/dev/null || sqc_die "pid $pid not running"
  for _ in $(seq 1 30); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
  sqc_log "stopped '$name'; summary: $ROOT/$name/summary.json"
  tail -n 3 "$ROOT/$name/txload.log"
  ;;
status)
  name="${1:-steady}"
  pidf="$ROOT/$name/txload.pid"
  if [ -f "$pidf" ] && kill -0 "$(cat "$pidf")" 2>/dev/null; then
    echo "run '$name': UP (pid $(cat "$pidf"))"
  else
    echo "run '$name': not running"
  fi
  [ -f "$ROOT/$name/txload.log" ] && { echo "-- last log lines"; tail -n 3 "$ROOT/$name/txload.log"; }
  echo "-- node heights / mempool"
  IFS=',' read -ra urls <<< "$(rpcs)"
  for u in "${urls[@]}"; do
    printf '  %-28s ' "$u"
    curl -fsS --max-time 5 "$u/status" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("height",d["height"],"mempool",d["mempool_size"])' 2>/dev/null || echo '?'
  done
  ;;
*)
  echo "usage: 30-txload.sh {build|start [name] [flags]|stop [name]|status [name]|run [flags]|addrs [flags]}"
  exit 2
  ;;
esac
