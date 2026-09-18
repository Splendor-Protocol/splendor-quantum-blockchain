#!/usr/bin/env bash
# Pull the metrics for a time window from the obs box (source after env.sh + lib/ssm.sh).
#
#   prom_range <start-epoch> <end-epoch> <out.json>
#
# Runs lib/prom-range.py ON quantum-obs (Prometheus and Loki only listen on
# localhost there and on the three RPC elastic IPs), which writes one JSON file
# holding every Prometheus series (step 15 s) and the parsed Loki "block
# committed" lines for the window; the file is then paged back over SSM.
# Expressions mirror infra/observability/grafana/dashboards/sqc-host-health.json
# with tier="devnet".

prom_range() {
  local start="$1" end="$2" out="$3" f remote="/tmp/prom-range-$$.json"
  f=$(mktemp)
  {
    echo '#!/bin/bash'
    echo 'set -u'
    echo "cat > /tmp/prom-range.py <<'PYEOF'"
    cat "$DS_HERE/lib/prom-range.py"
    echo 'PYEOF'
    echo "python3 /tmp/prom-range.py '$start' '$end' '$DS_TIER' '$remote'"
  } > "$f"
  ssm_run "$DS_OBS_INSTANCE" "$f" 900 || { rm -f "$f"; return 1; }
  rm -f "$f"
  ssm_fetch_file "$DS_OBS_INSTANCE" "$remote" "$out" || return 1
  ssm_sh "$DS_OBS_INSTANCE" "rm -f '$remote'" 60 >/dev/null || true
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print("metrics:", {k: len(v) for k, v in d["prom"].items()}, "blocks:", len(d["blocks"]), "notable:", len(d["notable"]))' "$out"
}
