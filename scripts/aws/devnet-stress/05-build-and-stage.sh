#!/usr/bin/env bash
# Build the static arm64 txload and put it on the devnet host.
#
#   05-build-and-stage.sh
#
# 1. CGO_ENABLED=0 GOOS=linux GOARCH=arm64 build of ./cmd/txload (this branch).
# 2. Stage through S3: create $DS_BUCKET (private, public access blocked), copy
#    the binary, presign a 1-hour GET URL. (The instance role is SSM+CloudWatch
#    only, so the host cannot read S3 by itself; the presigned URL is the one
#    thing that crosses.)
# 3. Over SSM: /srv/sqc-load/{bin,keys,runs}, curl the URL, verify sha256, run
#    the binary once inside alpine:3.20, start the sqc-load-rpcproxy sidecar
#    (host 127.0.0.1:18080 -> sqc-node:8080, same pattern as sqc-metrics-proxy),
#    install lib/watch-host.sh.
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"

BIN="$DS_WORK/bin/txload-linux-arm64"
ds_log "building $BIN from $DS_REPO_ROOT ($(git -C "$DS_REPO_ROOT" rev-parse --short HEAD))"
(cd "$DS_REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "$BIN" ./cmd/txload)
(cd "$DS_REPO_ROOT" && go build -trimpath -o "$DS_WORK/bin/txload-local" ./cmd/txload)
SHA=$(shasum -a 256 "$BIN" | cut -d' ' -f1)
ds_log "sha256 $SHA ($(stat -f %z "$BIN" 2>/dev/null || stat -c %s "$BIN") bytes)"

acct=$(aws sts get-caller-identity --query Account --output text)
[ "$acct" = "$DS_ACCOUNT" ] || ds_die "wrong AWS account $acct"
if ! aws s3api head-bucket --bucket "$DS_BUCKET" 2>/dev/null; then
  aws s3 mb "s3://$DS_BUCKET" --region "$AWS_REGION"
fi
aws s3api put-public-access-block --bucket "$DS_BUCKET" \
  --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
aws s3 cp "$BIN" "s3://$DS_BUCKET/txload-linux-arm64" --only-show-errors
URL=$(aws s3 presign "s3://$DS_BUCKET/txload-linux-arm64" --expires-in 3600)
umask 077; printf '%s\n' "$URL" > "$DS_WORK/presigned.url"
ds_log "staged s3://$DS_BUCKET/txload-linux-arm64 (presigned 1h)"

WATCH_B64=$(base64 < "$DS_HERE/lib/watch-host.sh" | tr -d '\n')
f=$(mktemp); cat > "$f" <<EOS
#!/bin/bash
set -euo pipefail
mkdir -p $DS_HOST_ROOT/bin $DS_HOST_ROOT/keys $DS_HOST_ROOT/runs
chmod 700 $DS_HOST_ROOT/keys
curl -fsS -m 120 -o $DS_HOST_ROOT/bin/txload.new '$URL'
echo "$SHA  $DS_HOST_ROOT/bin/txload.new" | sha256sum -c -
mv $DS_HOST_ROOT/bin/txload.new $DS_HOST_ROOT/bin/txload
chmod 755 $DS_HOST_ROOT/bin/txload
docker pull -q $DS_DRIVER_IMAGE >/dev/null; docker pull -q $DS_SOCAT_IMAGE >/dev/null
# the binary must run inside the driver image (static, no libc): usage text, exit 2
set +e; docker run --rm -v $DS_HOST_ROOT:$DS_HOST_ROOT:ro $DS_DRIVER_IMAGE $DS_HOST_ROOT/bin/txload 2>&1 | head -1; set -e
docker network inspect $DS_NODE_NET >/dev/null
docker rm -f sqc-load-rpcproxy >/dev/null 2>&1 || true
docker run -d --name sqc-load-rpcproxy --restart unless-stopped --network $DS_NODE_NET \\
  --security-opt no-new-privileges:true -p 127.0.0.1:$DS_PROXY_PORT:$DS_PROXY_PORT \\
  --log-opt max-size=5m --log-opt max-file=1 \\
  $DS_SOCAT_IMAGE TCP-LISTEN:$DS_PROXY_PORT,fork,reuseaddr TCP:$DS_NODE_SVC:8080 >/dev/null
sleep 2
curl -fsS -m 5 http://127.0.0.1:$DS_PROXY_PORT/status | jq -r '"proxy OK: chain=\\(.chain_id) tag=\\(.image_tag) height=\\(.height)"'
echo '$WATCH_B64' | base64 -d > $DS_HOST_ROOT/watch.sh; chmod 755 $DS_HOST_ROOT/watch.sh; bash -n $DS_HOST_ROOT/watch.sh
ls -la $DS_HOST_ROOT/bin $DS_HOST_ROOT/watch.sh
EOS
ssm_run "$DS_INSTANCE" "$f" 600; rm -f "$f"
echo "$SSM_OUT" | grep -q "proxy OK: chain=$DS_CHAIN_ID" || ds_die "rpc proxy did not answer with the devnet chain id"
ds_log "host staged: $DS_HOST_ROOT/bin/txload, sqc-load-rpcproxy, watch.sh"
