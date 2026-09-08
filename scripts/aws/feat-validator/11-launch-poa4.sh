#!/usr/bin/env bash
# Launch 1 master + 3 validators on AWS with the PoA round-robin harness
# (--poa-producer) so validator_gates_v1 can be proved across producers.
# Requires the PoA harness files on the working tree (cherry-picked from
# harness/poa-multivalidator-proof onto feat/validator — uncommitted OK).
#
#   11-launch-poa4.sh
#   11-launch-poa4.sh --dry-run
#
# Cost: 1x t4g.large + 3x t4g.small. Tag: Project=$SQC_TEST_TAG.
# Tear down any prior fleet first: bash scripts/aws/feat-validator/90-teardown.sh
set -euo pipefail
source "$(dirname "$0")/env.sh"

DRY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY=1; shift ;;
    *) sqc_die "unknown flag $1" ;;
  esac
done
run() { if [ "$DRY" = 1 ]; then printf '+ %q ' "$@"; echo; else "$@"; fi; }

NODES=4
mkdir -p "$SQC_TEST_DIR/bin" "$SQC_TEST_DIR/chain" "$SQC_TEST_DIR/keys"
cd "$SQC_REPO_ROOT"
[ "$DRY" = 1 ] || bash scripts/aws/feat-validator/00-preflight.sh

# Remind if an old fleet is still running
if [ "$DRY" = 0 ]; then
  old="$(aws ec2 describe-instances --filters "Name=tag:Project,Values=$SQC_TEST_TAG" "Name=instance-state-name,Values=pending,running,stopping,stopped" --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null || true)"
  if [ -n "${old:-}" ]; then
    sqc_warn "existing fleet still tagged $SQC_TEST_TAG: $old"
    sqc_die "run: bash scripts/aws/feat-validator/90-teardown.sh   (then re-run this script)"
  fi
fi

# ── 1. binaries ──────────────────────────────────────────────────────────────
sqc_log "building linux/arm64 sqc-node, sqc-wallet, sqc-modcall, featsmoke"
for pkg in node wallet; do
  run env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-$pkg" "./cmd/$pkg"
done
run env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-modcall" ./cmd/sqc-modcall
run env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/featsmoke" ./cmd/featsmoke
run env GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-wallet-local" ./cmd/wallet
run env GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-modcall-local" ./cmd/sqc-modcall
run env GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/featsmoke-local" ./cmd/featsmoke

# ── 2. keys ──────────────────────────────────────────────────────────────────
cd "$SQC_TEST_DIR/keys"
if [ ! -f pass.txt ]; then
  [ "$DRY" = 1 ] || { openssl rand -hex 16 > pass.txt; chmod 600 pass.txt; }
fi
for name in master v1 v2 v3 rogue; do
  if [ ! -f "$name.sqwallet" ]; then
    sqc_log "generating wallet $name"
    [ "$DRY" = 1 ] || printf '%s\n%s\n' "$(cat pass.txt)" "$(cat pass.txt)" | \
      "$SQC_TEST_DIR/bin/sqc-wallet-local" generate --out "$name.sqwallet" >/dev/null
  fi
  if [ "$DRY" = 0 ]; then
    printf '%s\n' "$(cat pass.txt)" | "$SQC_TEST_DIR/bin/sqc-wallet-local" pubkey --wallet "$name.sqwallet" | tail -1 > "$name.pub.hex"
    printf '%s\n' "$(cat pass.txt)" | "$SQC_TEST_DIR/bin/sqc-wallet-local" address --wallet "$name.sqwallet" | grep -o 'SQ:[0-9a-f]*' > "$name.addr"
  fi
done
if [ ! -f "$SQC_TEST_DIR/chain/faucet.seed" ]; then
  sqc_log "generating faucet seed"
  [ "$DRY" = 1 ] || "$SQC_TEST_DIR/bin/featsmoke-local" keygen --out "$SQC_TEST_DIR/chain/faucet.seed"
fi

if [ "$DRY" = 0 ]; then
  MASTER_PUB="$(cat master.pub.hex)"
  MASTER_ADDR="$(cat master.addr)"
  V1="$(cat v1.addr)"; V2="$(cat v2.addr)"; V3="$(cat v3.addr)"; ROGUE="$(cat rogue.addr)"
  FAUCET="$("$SQC_TEST_DIR/bin/featsmoke-local" keygen --out "$SQC_TEST_DIR/chain/faucet.seed" --print-address)"
  cat > "$SQC_TEST_DIR/chain/genesis.json" <<JSON
{
  "chain_id": "$SQC_TEST_CHAIN_ID",
  "network_name": "feat/validator poa4 demo",
  "timestamp": $(date +%s),
  "validator_pubkey_hex": "$MASTER_PUB",
  "allocations": [
    { "address": "$MASTER_ADDR", "balance": "1000000000000000000000000", "comment": "master / genesis validator" },
    { "address": "$V1", "balance": "1000000000000000000000000", "comment": "validator v1" },
    { "address": "$V2", "balance": "1000000000000000000000000", "comment": "validator v2" },
    { "address": "$V3", "balance": "1000000000000000000000000", "comment": "validator v3" },
    { "address": "$ROGUE", "balance": "1000000000000000000000000", "comment": "rogue (unadmitted)" },
    { "address": "$FAUCET", "balance": "1000000000000000000000000000", "comment": "smoke faucet" }
  ],
  "total_supply": "",
  "forks": {"module_revert_v1":1,"validator_gates_v1":1,"verify_v1":1,"qrc721_v1":1,"min_fee_v1":300}
}
JSON
  [ -f "$SQC_TEST_DIR/chain/rpc.token" ] || { openssl rand -hex 32 > "$SQC_TEST_DIR/chain/rpc.token"; chmod 600 "$SQC_TEST_DIR/chain/rpc.token"; }
  PRODUCERS="$MASTER_ADDR,$V1,$V2,$V3"
  echo "$PRODUCERS" > "$SQC_TEST_DIR/keys/producers.txt"
  sqc_log "genesis: master=$MASTER_ADDR"
  sqc_log "producers=$PRODUCERS"
fi

# ── 3. key pair + SG ─────────────────────────────────────────────────────────
MYIP="$(curl -fsS https://checkip.amazonaws.com | tr -d ' \n')/32"
if [ ! -f "$SQC_KEY_FILE" ]; then
  sqc_log "creating key pair $SQC_KEY_NAME"
  run aws ec2 create-key-pair --key-name "$SQC_KEY_NAME" --key-type ed25519 --key-format pem \
    --tag-specifications "ResourceType=key-pair,Tags=[{Key=Project,Value=$SQC_TEST_TAG},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query KeyMaterial --output text > "$SQC_KEY_FILE"
  chmod 600 "$SQC_KEY_FILE"
fi
VPC="$SQC_VPC_ID"
SUBNET="$SQC_SUBNET_ID"
[ -n "$VPC" ] && [ -n "$SUBNET" ] || sqc_die "set SQC_VPC_ID / SQC_SUBNET_ID"
VPC_CIDR="$(run aws ec2 describe-vpcs --vpc-ids "$VPC" --query 'Vpcs[0].CidrBlock' --output text)"
SG="$(run aws ec2 describe-security-groups --filters Name=group-name,Values="$SQC_SG_NAME" Name=vpc-id,Values="$VPC" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || echo None)"
if [ "$SG" = "None" ] || [ -z "$SG" ]; then
  SG="$(run aws ec2 create-security-group --group-name "$SQC_SG_NAME" --description "feat/validator poa4 throwaway" --vpc-id "$VPC" \
    --tag-specifications "ResourceType=security-group,Tags=[{Key=Project,Value=$SQC_TEST_TAG},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query GroupId --output text)"
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 22 --cidr "$MYIP"
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 8080 --cidr 0.0.0.0/0
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 8080 --cidr "$VPC_CIDR"
fi
if [ "$DRY" = 0 ]; then
  has_open="$(aws ec2 describe-security-groups --group-ids "$SG" --query "SecurityGroups[0].IpPermissions[?ToPort==\`8080\`].IpRanges[?CidrIp==\`0.0.0.0/0\`].CidrIp | [0]" --output text 2>/dev/null || echo None)"
  if [ "$has_open" = "None" ] || [ -z "$has_open" ]; then
    sqc_log "opening 8080/tcp to 0.0.0.0/0 for explorer read-through"
    aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 8080 --cidr 0.0.0.0/0 || true
  fi
fi
sqc_log "SG=$SG VPC=$VPC subnet=$SUBNET"

# ── 4. instances ─────────────────────────────────────────────────────────────
AMI="$(run aws ssm get-parameter --name "$SQC_AMI_PARAM" --query 'Parameter.Value' --output text)"
USERDATA="$(cat <<'CLOUD'
#!/bin/bash
set -e
useradd --system --home /var/sqc --shell /usr/sbin/nologin sqc || true
mkdir -p /opt/sqc /var/sqc/data && chown -R sqc:sqc /var/sqc
apt-get update -qq && apt-get install -y -qq jq curl >/dev/null
CLOUD
)"
: > "$SQC_TEST_DIR/instances.txt"
for i in $(seq 0 $((NODES-1))); do
  if [ "$i" = 0 ]; then role=master; itype="$SQC_VALIDATOR_INSTANCE_TYPE"; wname=master
  else role=validator; itype="$SQC_NODE_INSTANCE_TYPE"; wname="v$i"
  fi
  id="$(run aws ec2 run-instances --image-id "$AMI" --instance-type "$itype" --key-name "$SQC_KEY_NAME" \
    --network-interfaces "DeviceIndex=0,SubnetId=$SUBNET,Groups=$SG,AssociatePublicIpAddress=true" \
    --user-data "$USERDATA" --metadata-options HttpTokens=required \
    --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=40,VolumeType=gp3,Encrypted=true}' \
    --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$SQC_TEST_TAG-$role-$i},{Key=Project,Value=$SQC_TEST_TAG},{Key=Role,Value=$role},{Key=Wallet,Value=$wname},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query 'Instances[0].InstanceId' --output text)"
  echo "$i $role $id $wname" >> "$SQC_TEST_DIR/instances.txt"
  sqc_log "launched $role-$i ($wname): $id ($itype)"
done
[ "$DRY" = 1 ] && { sqc_log "dry run complete"; exit 0; }

sqc_log "waiting for instances to run"
aws ec2 wait instance-running --instance-ids $(awk '{print $3}' "$SQC_TEST_DIR/instances.txt")
: > "$SQC_TEST_DIR/instances.full.txt"
while read -r i role id wname; do
  pub="$(aws ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)"
  priv="$(aws ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)"
  echo "$i $role $id $pub $priv $wname" >> "$SQC_TEST_DIR/instances.full.txt"
done < "$SQC_TEST_DIR/instances.txt"

sqc_pub()  { awk -v i="$1" '$1==i{print $4; exit}' "$SQC_TEST_DIR/instances.full.txt"; }
sqc_priv() { awk -v i="$1" '$1==i{print $5; exit}' "$SQC_TEST_DIR/instances.full.txt"; }

SSH=(ssh -i "$SQC_KEY_FILE" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o BatchMode=yes)
wait_ssh() { for _ in $(seq 1 50); do "${SSH[@]}" "ubuntu@$1" 'test -d /opt/sqc && test -x /usr/bin/jq' 2>/dev/null && return 0; sleep 6; done; sqc_die "ssh to $1 never came up"; }

TOKEN="$(cat "$SQC_TEST_DIR/chain/rpc.token")"
MASTER_PUB="$(cat "$SQC_TEST_DIR/keys/master.pub.hex")"
PRODUCERS="$(cat "$SQC_TEST_DIR/keys/producers.txt")"

# ── 5. ship + start (master first) ───────────────────────────────────────────
while read -r i role id ip priv wname <&3; do
  sqc_log "provisioning $role-$i ($wname) at $ip"
  wait_ssh "$ip"
  scp -i "$SQC_KEY_FILE" -o StrictHostKeyChecking=accept-new \
    "$SQC_TEST_DIR/bin/sqc-node" "$SQC_TEST_DIR/bin/sqc-wallet" "$SQC_TEST_DIR/bin/sqc-modcall" \
    "$SQC_TEST_DIR/bin/featsmoke" "$SQC_TEST_DIR/chain/genesis.json" \
    "$SQC_TEST_DIR/keys/$wname.sqwallet" "$SQC_TEST_DIR/keys/pass.txt" \
    "ubuntu@$ip:/tmp/"
  PEERS=""
  for j in $(seq 0 $((NODES-1))); do
    [ "$j" = "$i" ] && continue
    PEERS="$PEERS --peer http://$(sqc_priv "$j"):8080"
  done
  EXEC="/opt/sqc/sqc-node --data-dir /var/sqc/data --rpc-addr 0.0.0.0:8080 --genesis /var/sqc/genesis.json --chain-id $SQC_TEST_CHAIN_ID --validator --wallet /var/sqc/validator.sqwallet --wallet-pass-file /var/sqc/wallet.pass --block-interval $SQC_BLOCK_INTERVAL --validator-pubkey-hex $MASTER_PUB --poa-producer $PRODUCERS --allow-private-peers --allow-plaintext-p2p --log-level info $PEERS"
  "${SSH[@]}" "ubuntu@$ip" "sudo bash -s" <<REMOTE
set -e
install -m 0755 /tmp/sqc-node /tmp/sqc-wallet /tmp/sqc-modcall /tmp/featsmoke /opt/sqc/
install -m 0644 /tmp/genesis.json /var/sqc/genesis.json
install -m 0600 -o sqc -g sqc /tmp/${wname}.sqwallet /var/sqc/validator.sqwallet
install -m 0600 -o sqc -g sqc /tmp/pass.txt /var/sqc/wallet.pass
rm -f /tmp/${wname}.sqwallet /tmp/pass.txt /tmp/*.sqwallet
cat > /etc/systemd/system/sqc-node.service <<UNIT
[Unit]
Description=SQC feat/validator PoA node ($role $wname)
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=sqc
Group=sqc
WorkingDirectory=/var/sqc
Environment="SQC_RPC_AUTH_TOKEN=$TOKEN"
ExecStart=$EXEC
Restart=always
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/var/sqc
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload && systemctl enable --now sqc-node
sleep 3 && systemctl --no-pager --lines=8 status sqc-node || true
REMOTE
  if [ "$i" = 0 ]; then sqc_log "master up; waiting 8s for block 0"; sleep 8; fi
done 3< "$SQC_TEST_DIR/instances.full.txt"

sqc_log "PoA4 launched. Master RPC: http://$(sqc_pub 0):8080"
sqc_log "next: bash scripts/aws/feat-validator/25-gate-proof.sh all"
cat "$SQC_TEST_DIR/instances.full.txt"
