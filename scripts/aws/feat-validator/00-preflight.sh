#!/usr/bin/env bash
# Are the credentials live, and are they the right account? Nothing else runs
# until this passes. Exit 0 = ready to launch.
set -euo pipefail
source "$(dirname "$0")/env.sh"

command -v aws >/dev/null || sqc_die "aws CLI v2 is required (brew install awscli)"
command -v go >/dev/null || sqc_die "Go is required to build the node (GOTOOLCHAIN=auto handles 1.25)"

sqc_log "profile=$AWS_PROFILE region=$AWS_REGION account(expected)=$AWS_ACCOUNT_ID"
if ! ident="$(aws sts get-caller-identity --output json 2>&1)"; then
  sqc_warn "STS refused the credentials for profile '$AWS_PROFILE':"
  sqc_warn "  $ident"
  cat >&2 <<MSG

Sign in first (AWS CLI v2.27+ browser login, creates/refreshes the profile):

    aws login --profile $AWS_PROFILE
    # or, for the console identity that administers this account:
    aws configure sso --profile $AWS_PROFILE      # if the account is behind IAM Identity Center

then re-run this script. If you keep a different profile for the same
account, export AWS_PROFILE=<that profile> before sourcing env.sh. Do NOT
use the 'splendor' / stardust profile for EC2 work.
MSG
  exit 2
fi
account="$(printf '%s' "$ident" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Account"])')"
arn="$(printf '%s' "$ident" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Arn"])')"
if [ "$account" != "$AWS_ACCOUNT_ID" ]; then
  sqc_die "profile '$AWS_PROFILE' is account $account ($arn), not $AWS_ACCOUNT_ID — refusing to launch anywhere else"
fi
sqc_log "credentials live: $arn"

ami="$(aws ssm get-parameter --name "$SQC_AMI_PARAM" --query 'Parameter.Value' --output text)"
sqc_log "ubuntu 24.04 arm64 AMI in $AWS_REGION: $ami"
if [ -n "${SQC_VPC_ID:-}" ] && [ -n "${SQC_SUBNET_ID:-}" ]; then
  vpc="$SQC_VPC_ID"
  aws ec2 describe-vpcs --vpc-ids "$vpc" --query 'Vpcs[0].VpcId' --output text >/dev/null     || sqc_die "SQC_VPC_ID=$vpc not found in $AWS_REGION"
  aws ec2 describe-subnets --subnet-ids "$SQC_SUBNET_ID" --query 'Subnets[0].SubnetId' --output text >/dev/null     || sqc_die "SQC_SUBNET_ID=$SQC_SUBNET_ID not found in $AWS_REGION"
  sqc_log "VPC: $vpc (override) subnet: $SQC_SUBNET_ID"
else
  vpc="$(aws ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)"
  [ "$vpc" != "None" ] || sqc_die "no default VPC in $AWS_REGION; set SQC_VPC_ID / SQC_SUBNET_ID in env.sh"
  sqc_log "default VPC: $vpc"
fi
sqc_log "READY: run 10-launch.sh"
