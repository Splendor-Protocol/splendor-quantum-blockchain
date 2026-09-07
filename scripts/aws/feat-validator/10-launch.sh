#!/usr/bin/env bash
# Launch the feat/validator test chain on AWS: one validator (the genesis
# producer) plus N-1 full nodes that sync from it. Usage:
#
#   10-launch.sh            # single-node gated smoke (validator only)
#   10-launch.sh --nodes 4  # validator + 3 syncing full nodes
#   10-launch.sh --dry-run  # print every aws call, run none
#
# What it does, in order: builds linux/arm64 binaries here (static, no cgo, no
# repo access needed on the hosts), makes the validator wallet + faucet seed +
# genesis, creates a key pair and a security group scoped to YOUR public IP,
# launches the instances with a minimal cloud-init, ships binaries + genesis +
# systemd units over SSH, and starts the services. Everything is tagged
# Project=$SQC_TEST_TAG so 90-teardown.sh can remove it all.
set -euo pipefail
source "$(dirname "$0")/env.sh"

NODES=1; DRY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --nodes) NODES="$2"; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    *) sqc_die "unknown flag $1" ;;
  esac
done
[ "$NODES" -ge 1 ] || sqc_die "--nodes must be >= 1"

run() { if [ "$DRY" = 1 ]; then printf '+ %q ' "$@"; echo; else "$@"; fi; }

mkdir -p "$SQC_TEST_DIR/bin" "$SQC_TEST_DIR/chain"
cd "$SQC_REPO_ROOT"
[ "$DRY" = 1 ] || bash scripts/aws/feat-validator/00-preflight.sh

# ── 1. binaries ──────────────────────────────────────────────────────────────
sqc_log "building linux/arm64 sqc-node, sqc-wallet, featsmoke (static)"
for pkg in node wallet featsmoke; do
  out="$SQC_TEST_DIR/bin/sqc-$pkg"; [ "$pkg" = featsmoke ] && out="$SQC_TEST_DIR/bin/featsmoke"
  run env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=auto go build -trimpath -o "$out" "./cmd/$pkg"
done
# Native copies for generating keys and driving the smoke from this laptop.
run env GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-wallet-local" ./cmd/wallet
run env GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/featsmoke-local" ./cmd/featsmoke

# ── 2. keys + genesis ─────────────────────────────────────────────────────────
cd "$SQC_TEST_DIR/chain"
if [ ! -f validator.sqwallet ]; then
  sqc_log "generating the validator wallet (password in wallet.pass)"
  [ "$DRY" = 1 ] || { openssl rand -hex 16 > wallet.pass; chmod 600 wallet.pass; }
  [ "$DRY" = 1 ] || printf '%s\n%s\n' "$(cat wallet.pass)" "$(cat wallet.pass)" | "$SQC_TEST_DIR/bin/sqc-wallet-local" generate --out validator.sqwallet >/dev/null
fi
if [ ! -f faucet.seed ]; then
  sqc_log "generating the faucet key the smoke driver spends from"
  [ "$DRY" = 1 ] || "$SQC_TEST_DIR/bin/featsmoke-local" keygen --out faucet.seed
fi
if [ "$DRY" = 0 ]; then
  PUB="$(printf '%s\n' "$(cat wallet.pass)" | "$SQC_TEST_DIR/bin/sqc-wallet-local" pubkey --wallet validator.sqwallet | tail -1)"
  VADDR="$(printf '%s\n' "$(cat wallet.pass)" | "$SQC_TEST_DIR/bin/sqc-wallet-local" address --wallet validator.sqwallet | grep -o 'SQ:[0-9a-f]*')"
  FAUCET="$("$SQC_TEST_DIR/bin/featsmoke-local" keygen --out faucet.seed --print-address)"
  cat > genesis.json <<JSON
{
  "chain_id": "$SQC_TEST_CHAIN_ID",
  "network_name": "feat/validator test",
  "timestamp": $(date +%s),
  "validator_pubkey_hex": "$PUB",
  "allocations": [
    { "address": "$VADDR",  "balance": "1000000000000000000000000",    "comment": "genesis validator: 1,000,000 SPLD" },
    { "address": "$FAUCET", "balance": "1000000000000000000000000000", "comment": "smoke faucet: 1,000,000,000 SPLD" }
  ],
  "total_supply": "",
  "forks": $SQC_TEST_FORKS
}
JSON
  [ -f rpc.token ] || { openssl rand -hex 32 > rpc.token; chmod 600 rpc.token; }
  sqc_log "genesis written: validator=$VADDR faucet=$FAUCET forks=$SQC_TEST_FORKS"
fi

# ── 3. key pair + security group ──────────────────────────────────────────────
MYIP="$(curl -fsS https://checkip.amazonaws.com | tr -d ' \n')/32"
if [ ! -f "$SQC_KEY_FILE" ]; then
  sqc_log "creating key pair $SQC_KEY_NAME -> $SQC_KEY_FILE"
  run aws ec2 create-key-pair --key-name "$SQC_KEY_NAME" --key-type ed25519 --key-format pem \
    --tag-specifications "ResourceType=key-pair,Tags=[{Key=Project,Value=$SQC_TEST_TAG},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query KeyMaterial --output text > "$SQC_KEY_FILE"
  chmod 600 "$SQC_KEY_FILE"
fi
VPC="$(run aws ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)"
VPC_CIDR="$(run aws ec2 describe-vpcs --vpc-ids "$VPC" --query 'Vpcs[0].CidrBlock' --output text)"
SG="$(run aws ec2 describe-security-groups --filters Name=group-name,Values="$SQC_SG_NAME" Name=vpc-id,Values="$VPC" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || echo None)"
if [ "$SG" = "None" ] || [ -z "$SG" ]; then
  SG="$(run aws ec2 create-security-group --group-name "$SQC_SG_NAME" --description "feat/validator throwaway test" --vpc-id "$VPC" \
    --tag-specifications "ResourceType=security-group,Tags=[{Key=Project,Value=$SQC_TEST_TAG},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query GroupId --output text)"
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 22 --cidr "$MYIP"
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 8080 --cidr "$MYIP"
  run aws ec2 authorize-security-group-ingress --group-id "$SG" --protocol tcp --port 8080 --cidr "$VPC_CIDR"
fi
sqc_log "security group $SG: 22 + 8080 from $MYIP, 8080 inside $VPC_CIDR"

# ── 4. instances ──────────────────────────────────────────────────────────────
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
  role=node; itype="$SQC_NODE_INSTANCE_TYPE"; [ "$i" = 0 ] && { role=validator; itype="$SQC_VALIDATOR_INSTANCE_TYPE"; }
  id="$(run aws ec2 run-instances --image-id "$AMI" --instance-type "$itype" --key-name "$SQC_KEY_NAME" \
    --security-group-ids "$SG" --user-data "$USERDATA" --metadata-options HttpTokens=required \
    --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=40,VolumeType=gp3,Encrypted=true}' \
    --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$SQC_TEST_TAG-$role-$i},{Key=Project,Value=$SQC_TEST_TAG},{Key=Role,Value=$role},{Key=Owner,Value=$SQC_TEST_OWNER}]" \
    --query 'Instances[0].InstanceId' --output text)"
  echo "$i $role $id" >> "$SQC_TEST_DIR/instances.txt"
  sqc_log "launched $role-$i: $id ($itype)"
done
[ "$DRY" = 1 ] && { sqc_log "dry run complete"; exit 0; }

sqc_log "waiting for the instances to run"
aws ec2 wait instance-running --instance-ids $(awk '{print $3}' "$SQC_TEST_DIR/instances.txt")
declare -A PUB PRIV
while read -r i role id; do
  PUB[$i]="$(aws ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)"
  PRIV[$i]="$(aws ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)"
  echo "$i $role $id ${PUB[$i]} ${PRIV[$i]}" >> "$SQC_TEST_DIR/instances.full.txt"
done < "$SQC_TEST_DIR/instances.txt"

SSH=(ssh -i "$SQC_KEY_FILE" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o BatchMode=yes)
wait_ssh() { for _ in $(seq 1 40); do "${SSH[@]}" "ubuntu@$1" 'test -d /opt/sqc && test -x /usr/bin/jq' 2>/dev/null && return 0; sleep 6; done; sqc_die "ssh to $1 never came up"; }

# ── 5. ship + start ───────────────────────────────────────────────────────────
TOKEN="$(cat "$SQC_TEST_DIR/chain/rpc.token")"
PUBHEX="$(python3 -c 'import json;print(json.load(open("'"$SQC_TEST_DIR"'/chain/genesis.json"))["validator_pubkey_hex"])')"
while read -r i role id ip priv; do
  sqc_log "provisioning $role-$i at $ip"
  wait_ssh "$ip"
  scp -i "$SQC_KEY_FILE" -o StrictHostKeyChecking=accept-new "$SQC_TEST_DIR/bin/sqc-node" "$SQC_TEST_DIR/bin/sqc-wallet" "$SQC_TEST_DIR/bin/featsmoke" "$SQC_TEST_DIR/chain/genesis.json" "ubuntu@$ip:/tmp/"
  if [ "$role" = validator ]; then
    scp -i "$SQC_KEY_FILE" "$SQC_TEST_DIR/chain/validator.sqwallet" "$SQC_TEST_DIR/chain/wallet.pass" "ubuntu@$ip:/tmp/"
    EXEC="/opt/sqc/sqc-node --data-dir /var/sqc/data --rpc-addr 0.0.0.0:8080 --genesis /var/sqc/genesis.json --chain-id $SQC_TEST_CHAIN_ID --validator --wallet /var/sqc/validator.sqwallet --wallet-pass-file /var/sqc/wallet.pass --block-interval $SQC_BLOCK_INTERVAL --allow-private-peers --log-level info"
    for j in $(seq 1 $((NODES-1))); do EXEC="$EXEC --peer http://${PRIV[$j]}:8080"; done
  else
    EXEC="/opt/sqc/sqc-node --data-dir /var/sqc/data --rpc-addr 0.0.0.0:8080 --genesis /var/sqc/genesis.json --chain-id $SQC_TEST_CHAIN_ID --validator-pubkey-hex $PUBHEX --peer http://${PRIV[0]}:8080 --allow-private-peers --log-level info"
  fi
  "${SSH[@]}" "ubuntu@$ip" "sudo bash -s" <<REMOTE
set -e
install -m 0755 /tmp/sqc-node /tmp/sqc-wallet /tmp/featsmoke /opt/sqc/
install -m 0644 /tmp/genesis.json /var/sqc/genesis.json
[ -f /tmp/validator.sqwallet ] && install -m 0600 -o sqc -g sqc /tmp/validator.sqwallet /var/sqc/validator.sqwallet && install -m 0600 -o sqc -g sqc /tmp/wallet.pass /var/sqc/wallet.pass
rm -f /tmp/validator.sqwallet /tmp/wallet.pass
cat > /etc/systemd/system/sqc-node.service <<UNIT
[Unit]
Description=Splendor Quantum Chain node (feat/validator test, $role)
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
sleep 3 && systemctl --no-pager --lines=5 status sqc-node || true
REMOTE
done < "$SQC_TEST_DIR/instances.full.txt"

sqc_log "launched. Validator RPC: http://${PUB[0]}:8080"
sqc_log "next: bash scripts/aws/feat-validator/20-smoke.sh"
