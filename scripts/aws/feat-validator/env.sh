#!/usr/bin/env bash
# Shared settings for the feat/validator AWS test. Source this; do not run it.
#
# Everything here is a throwaway, non-mainnet test: its own key pair, its own
# security group, its own tag. It never touches the splendor-infra tofu state
# (testnet / production / devnet) and never reads genesis-production.json.

# Account 640168431521 (LatamDX). Prefer a profile that `aws login` created;
# `default` is the fallback. Override: AWS_PROFILE=... before sourcing.
export AWS_ACCOUNT_ID="${AWS_ACCOUNT_ID:-640168431521}"
export AWS_PROFILE="${AWS_PROFILE:-splendor-640}"
# Same region as the live infra (splendor-infra environments/*.tfvars).
export AWS_REGION="${AWS_REGION:-us-east-1}"
export AWS_DEFAULT_REGION="$AWS_REGION"

# One tag on everything, so teardown can find it all.
export SQC_TEST_TAG="${SQC_TEST_TAG:-splendor-feat-validator}"
export SQC_TEST_OWNER="${SQC_TEST_OWNER:-$(whoami)}"

# Graviton, Ubuntu 24.04 arm64 — what splendor-infra runs. t4g.large is the
# infra's validator size; t4g.small is enough for a full node in this test.
export SQC_VALIDATOR_INSTANCE_TYPE="${SQC_VALIDATOR_INSTANCE_TYPE:-t4g.large}"
export SQC_NODE_INSTANCE_TYPE="${SQC_NODE_INSTANCE_TYPE:-t4g.small}"
export SQC_AMI_PARAM="/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id"

# The chain under test. A distinct chain id so nothing signed here could ever
# be replayed against a real tier.
export SQC_TEST_CHAIN_ID="${SQC_TEST_CHAIN_ID:-sqc-featval-1}"
export SQC_BLOCK_INTERVAL="${SQC_BLOCK_INTERVAL:-2}"
# min_fee_v1 activates late so the fee flip is observable mid-run
# (300 blocks ≈ 10 minutes at 2 s).
export SQC_TEST_FORKS="${SQC_TEST_FORKS:-{\"module_revert_v1\":1,\"validator_gates_v1\":1,\"verify_v1\":1,\"qrc721_v1\":1,\"min_fee_v1\":300}}"

# Local working directory for keys, genesis, binaries and instance ids.
export SQC_TEST_DIR="${SQC_TEST_DIR:-$HOME/.sqc-feat-validator}"
export SQC_KEY_NAME="${SQC_KEY_NAME:-sqc-feat-validator}"
export SQC_KEY_FILE="$SQC_TEST_DIR/$SQC_KEY_NAME.pem"
export SQC_SG_NAME="${SQC_SG_NAME:-sqc-feat-validator}"

# Repo root (this file lives in scripts/aws/feat-validator/).
export SQC_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

sqc_log() { printf '\033[0;32m[feat-validator]\033[0m %s\n' "$*"; }
sqc_warn() { printf '\033[0;33m[feat-validator]\033[0m %s\n' "$*" >&2; }
sqc_die() { printf '\033[0;31m[feat-validator]\033[0m %s\n' "$*" >&2; exit 1; }
