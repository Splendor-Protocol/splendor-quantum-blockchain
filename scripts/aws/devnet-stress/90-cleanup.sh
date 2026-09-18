#!/usr/bin/env bash
# Tear down everything the stress test put in place. The node is not touched.
#
#   90-cleanup.sh --yes
#
# Host: stop watcher units, docker rm -f sqc-load-c* and sqc-load-rpcproxy,
#       rm -rf /srv/sqc-load (binary, runs, the 20 funder seeds).
# AWS:  delete the staged object and the bucket.
# Laptop: overwrite-and-delete every seed under $DS_WORK/keys (rm -P on macOS,
#         shred -u elsewhere). Collected runs/ and figures/ are kept.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"
[ "${1:-}" = "--yes" ] || ds_die "pass --yes"

ds_log "host cleanup on $DS_INSTANCE"
ssm_sh "$DS_INSTANCE" "
for u in \$(systemctl list-units --no-legend --plain 'sqc-load-watch-*' | awk '{print \$1}'); do systemctl stop \$u || true; systemctl reset-failed \$u 2>/dev/null || true; done
docker ps -aq --filter name=sqc-load | xargs -r docker rm -f >/dev/null 2>&1 || true
rm -rf $DS_HOST_ROOT
echo containers_left=\$(docker ps -aq --filter name=sqc-load | wc -l) dir_left=\$(ls -d $DS_HOST_ROOT 2>/dev/null || echo none) units_left=\$(systemctl list-units --no-legend 'sqc-load-watch-*' | wc -l)
docker ps --format '{{.Names}}' | grep -E 'sqc-node|heartbeat|alloy|metrics-proxy' | tr '\n' ' '; echo ' (node stack untouched)'
" 120

ds_log "S3 cleanup"
aws s3 rm "s3://$DS_BUCKET/txload-linux-arm64" --only-show-errors 2>/dev/null || true
aws s3 rb "s3://$DS_BUCKET" 2>&1 || ds_warn "bucket $DS_BUCKET not removed"
aws s3api head-bucket --bucket "$DS_BUCKET" 2>/dev/null && ds_warn "bucket still exists" || ds_log "bucket $DS_BUCKET gone"

ds_log "local keys"
for f in "$DS_WORK"/keys/*.seed "$DS_WORK"/presigned.url; do
  [ -f "$f" ] || continue
  if rm -P "$f" 2>/dev/null; then :; elif command -v shred >/dev/null; then shred -u "$f"; else python3 -c 'import os,sys; p=sys.argv[1]; n=os.path.getsize(p); open(p,"r+b").write(os.urandom(n)); os.remove(p)' "$f"; fi
done
ls "$DS_WORK/keys" 2>/dev/null | grep -c seed | sed 's/^/seed files left: /' || true
ds_log "done; runs/ figures/ report/ kept under $DS_WORK"
