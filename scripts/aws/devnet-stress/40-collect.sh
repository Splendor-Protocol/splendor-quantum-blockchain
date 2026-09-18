#!/usr/bin/env bash
# Pull everything a run produced back to the laptop.
#
#   40-collect.sh <run-id>
#
# From the validator host: /srv/sqc-load/runs/<run-id>/ (every pass's cNN/
# stats.jsonl + summary.json + txload.log, watch.jsonl, stop-rule.txt) as a
# tarball -> $DS_WORK/runs/<run-id>/host/. From quantum-obs: lib/prom-range.py
# over [fund_start - 3 min, run_end + 3 min] -> $DS_WORK/runs/<run-id>/metrics.json.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"; source "$DS_HERE/lib/prom-range.sh"
RUN="${1:?run id}"
LOCAL="$DS_WORK/runs/$RUN"; mkdir -p "$LOCAL"
[ -f "$LOCAL/run.json" ] || ds_die "$LOCAL/run.json missing (30-run.sh writes it)"

ds_log "fetching $DS_HOST_ROOT/runs/$RUN from the host"
ssm_fetch_tar "$DS_INSTANCE" "$DS_HOST_ROOT/runs/$RUN" "$LOCAL/host.tar.gz"
rm -rf "$LOCAL/host"; mkdir -p "$LOCAL/host"
tar xzf "$LOCAL/host.tar.gz" -C "$LOCAL/host" --strip-components=1
ds_log "host files: $(find "$LOCAL/host" -type f | wc -l | tr -d ' ') ($(du -sh "$LOCAL/host" | cut -f1))"

read -r start end < <(python3 -c '
import json,sys; t=json.load(open(sys.argv[1]))["t"]
s=t["fund_start"] or t["burst_start"]; print(s-180, t["run_end"]+180)' "$LOCAL/run.json")
ds_log "metrics window $(date -u -r "$start" +%T 2>/dev/null || date -u -d @"$start" +%T) .. $(date -u -r "$end" +%T 2>/dev/null || date -u -d @"$end" +%T) UTC from quantum-obs"
prom_range "$start" "$end" "$LOCAL/metrics.json"

python3 - "$LOCAL" <<'PY'
import glob, json, os, sys
root = sys.argv[1]
for pas in sorted(glob.glob(os.path.join(root, "host", "*", ""))):
    name = os.path.basename(os.path.dirname(pas))
    tot = {"submitted": 0, "accepted": 0, "rejected": 0, "rate_limited_429": 0, "gave_up": 0, "n": 0}
    for s in glob.glob(os.path.join(pas, "c*", "summary.json")):
        d = json.load(open(s)); tot["n"] += 1
        for k in ("submitted", "accepted", "rejected", "rate_limited_429"): tot[k] += d.get(k, 0)
        tot["gave_up"] += d.get("one_shot_gave_up", 0)
    print("  %-10s summaries=%d submitted=%d accepted=%d rejected=%d 429=%d gave_up=%d" % (name, tot["n"], tot["submitted"], tot["accepted"], tot["rejected"], tot["rate_limited_429"], tot["gave_up"]))
w = os.path.join(root, "host", "watch.jsonl")
if os.path.exists(w):
    lines = [json.loads(l) for l in open(w) if l.strip()]
    rules = [l["rule"] for l in lines if l.get("rule")]
    print("  watch: %d samples, restarts max %s, oom %s, mem_avail min %s MB, rule: %s" % (
        len(lines), max(l["restarts"] for l in lines), any(l["oom"] == "true" for l in lines), min(l["mem_avail_mb"] for l in lines), rules[0] if rules else "none"))
PY
ds_log "collected -> $LOCAL"
