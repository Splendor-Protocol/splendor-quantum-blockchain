#!/usr/bin/env bash
# Pre-flight for the devnet stress test. Read-only: no traffic.
#
#   00-preflight.sh [--wait-for-tag]
#
# Checks: STS account, both instances Online in SSM, CPUCreditBalance, faucet
# balance, node /status + /healthz (image_tag, chain id, height, mempool), host
# docker ps / free -m / node RestartCount+OOMKilled, no other sqc-load driver,
# background tx rate, and the baseline block interval over the last 10 minutes
# from Prometheus. --wait-for-tag polls /status.image_tag every 60 s (up to
# 90 min) until it reports $DS_REQUIRED_TAG. Writes $DS_WORK/preflight-<ts>.json.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"; source "$DS_HERE/lib/prom-range.sh"

WAIT=0; [ "${1:-}" = "--wait-for-tag" ] && WAIT=1
OUT="$DS_WORK/preflight-$(date -u +%Y%m%dT%H%M%SZ).json"

acct=$(aws sts get-caller-identity --query Account --output text)
[ "$acct" = "$DS_ACCOUNT" ] || ds_die "wrong AWS account $acct (want $DS_ACCOUNT)"
ds_log "STS account $acct ($(aws sts get-caller-identity --query Arn --output text))"

for iid in "$DS_INSTANCE" "$DS_OBS_INSTANCE"; do
  ping=$(aws ssm describe-instance-information --filters "Key=InstanceIds,Values=$iid" --query 'InstanceInformationList[0].PingStatus' --output text)
  [ "$ping" = "Online" ] || ds_die "$iid is $ping in SSM"
done
itype=$(aws ec2 describe-instances --instance-ids "$DS_INSTANCE" --query 'Reservations[0].Instances[0].InstanceType' --output text)
ds_log "SSM Online: $DS_INSTANCE ($itype), $DS_OBS_INSTANCE"

credits=$(aws cloudwatch get-metric-statistics --namespace AWS/EC2 --metric-name CPUCreditBalance \
  --dimensions Name=InstanceId,Value="$DS_INSTANCE" --start-time "$(date -u -v-2H +%FT%TZ 2>/dev/null || date -u -d '2 hours ago' +%FT%TZ)" \
  --end-time "$(date -u +%FT%TZ)" --period 300 --statistics Average \
  --query 'sort_by(Datapoints,&Timestamp)[-1].Average' --output text)
ds_log "CPUCreditBalance: $credits"

fbal=$(curl -sS -m 15 "$DS_RPC/balance/$DS_FAUCET_ADDR" | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["balance"]))')
ds_log "faucet $DS_FAUCET_ADDR: $(python3 -c "print(f'{$fbal/10**18:,.2f}')") SPLD"
[ "$fbal" -ge $((50000 * 10**18)) ] 2>/dev/null || python3 -c "import sys; sys.exit(0 if $fbal >= 50000*10**18 else 1)" || ds_die "faucet holds < 50k SPLD"

status() { curl -sS -m 15 "$DS_RPC/status"; }
st=$(status)
python3 - "$st" "$DS_CHAIN_ID" <<'PY'
import json, sys
d = json.loads(sys.argv[1])
assert d["chain_id"] == sys.argv[2], d["chain_id"]
print("node: image_tag=%s version=%s commit=%s height=%s mempool=%s validators=%s" % (d["image_tag"], d["node_version"], d["commit"][:8], d["height"], d["mempool_size"], d["head_quorum_required"]))
PY
hz=$(curl -sS -m 15 "$DS_RPC/healthz")
ds_log "healthz: $hz"

if [ $WAIT = 1 ]; then
  for i in $(seq 1 90); do
    tag=$(status | python3 -c 'import json,sys; print(json.load(sys.stdin)["image_tag"])' 2>/dev/null || echo "?")
    [ "$tag" = "$DS_REQUIRED_TAG" ] && break
    ds_log "$(date -u +%T) image_tag=$tag, waiting for $DS_REQUIRED_TAG ($i/90)"
    sleep 60
  done
fi
tag=$(status | python3 -c 'import json,sys; print(json.load(sys.stdin)["image_tag"])')
[ "$tag" = "$DS_REQUIRED_TAG" ] || ds_die "devnet reports image_tag=$tag, traffic needs $DS_REQUIRED_TAG"
ds_log "image_tag $tag OK"

ds_log "host baseline via SSM"
f=$(mktemp); cat > "$f" <<EOS
#!/bin/bash
set -u
echo "instance-type: \$(curl -s -m 2 -H "X-aws-ec2-metadata-token: \$(curl -s -m 2 -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')" http://169.254.169.254/latest/meta-data/instance-type) nproc=\$(nproc) arch=\$(uname -m)"
free -m | head -2
echo "loadavg: \$(cat /proc/loadavg)"
echo "disk: \$(df -h / | tail -1)"
echo "== docker ps"; docker ps --format '{{.Names}}\t{{.Image}}\t{{.Status}}'
n=\$(docker ps -q --filter name=$DS_NODE_SVC | head -1)
echo "== node: \$(docker inspect -f 'restarts={{.RestartCount}} oom={{.State.OOMKilled}} started={{.State.StartedAt}} memlimit={{.HostConfig.Memory}}' "\$n")"
echo "== other drivers: \$(docker ps -q --filter name=sqc-load-c | wc -l) sqc-load containers running"
echo "== leftovers: \$(ls -d $DS_HOST_ROOT 2>/dev/null || echo none)"
echo "== dmesg killed: \$(dmesg -T 2>/dev/null | grep -ci 'killed process' || true)"
EOS
ssm_run "$DS_INSTANCE" "$f" 120; rm -f "$f"
echo "$SSM_OUT" | grep -q "0 sqc-load containers running" || ds_die "another sqc-load driver is running on the host"

ds_log "baseline block interval + background tx rate over the last 10 min (Prometheus on obs)"
now=$(date +%s)
prom_range $((now - 600)) "$now" "$DS_WORK/baseline-metrics.json" >/dev/null
python3 - "$DS_WORK/baseline-metrics.json" "$OUT" "$st" "$itype" "$credits" "$fbal" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
h = m["prom"]["height"]; tx = m["prom"]["txs_total"]
bi = (h[-1][0]-h[0][0])/(h[-1][1]-h[0][1]) if len(h) > 1 and h[-1][1] > h[0][1] else None
txr = (tx[-1][1]-tx[0][1])/(tx[-1][0]-tx[0][0]) if len(tx) > 1 else None
blocks = m["blocks"]
bt = [blocks[i+1][1]-blocks[i][1] for i in range(len(blocks)-1)]
bt.sort()
pool = [v for _, v in m["prom"]["mempool"]]
out = {
  "instance_type": sys.argv[4], "cpu_credit_balance": sys.argv[5], "faucet_balance_units": sys.argv[6],
  "status": json.loads(sys.argv[3]),
  "block_interval_s_prom": bi, "background_tx_per_s": txr,
  "block_time_loki_p50": bt[len(bt)//2] if bt else None, "block_time_loki_p95": bt[int(len(bt)*0.95)] if bt else None,
  "block_time_loki_max": bt[-1] if bt else None, "blocks_seen_10m": len(blocks),
  "mempool_max_10m": max(pool) if pool else None,
  "cpu_pct_max_10m": max((v for _, v in m["prom"]["cpu_pct"]), default=None),
}
json.dump(out, open(sys.argv[2], "w"), indent=2)
print("block interval (prom) %.3fs | loki p50 %ss p95 %ss max %ss over %d blocks | background %.3f tx/s | mempool max %s | cpu max %s%%" % (
  bi or 0, out["block_time_loki_p50"], out["block_time_loki_p95"], out["block_time_loki_max"], len(blocks), txr or 0, out["mempool_max_10m"], out["cpu_pct_max_10m"]))
if txr and txr > 1:
    print("WARNING: background traffic above 1 tx/s — someone else may be driving devnet", file=sys.stderr)
PY
ds_log "wrote $OUT"
