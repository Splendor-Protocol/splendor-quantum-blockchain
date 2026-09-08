#!/usr/bin/env bash
# Remove everything 10-launch.sh created: instances, security group, key pair.
# Finds them by the Project tag, so it is safe to run twice. Local keys and
# genesis under $SQC_TEST_DIR are kept unless --purge-local.
set -euo pipefail
source "$(dirname "$0")/env.sh"
PURGE=0; [ "${1:-}" = "--purge-local" ] && PURGE=1

ids="$(aws ec2 describe-instances --filters "Name=tag:Project,Values=$SQC_TEST_TAG" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[].Instances[].InstanceId' --output text)"
if [ -n "$ids" ]; then
  sqc_log "terminating: $ids"
  aws ec2 terminate-instances --instance-ids $ids >/dev/null
  aws ec2 wait instance-terminated --instance-ids $ids
fi
sg="$(aws ec2 describe-security-groups --filters "Name=tag:Project,Values=$SQC_TEST_TAG" --query 'SecurityGroups[].GroupId' --output text)"
for g in $sg; do sqc_log "deleting security group $g"; aws ec2 delete-security-group --group-id "$g"; done
if aws ec2 describe-key-pairs --key-names "$SQC_KEY_NAME" >/dev/null 2>&1; then
  sqc_log "deleting key pair $SQC_KEY_NAME"; aws ec2 delete-key-pair --key-name "$SQC_KEY_NAME"
fi
rm -f "$SQC_TEST_DIR/instances.txt" "$SQC_TEST_DIR/instances.full.txt"
[ "$PURGE" = 1 ] && { sqc_log "purging $SQC_TEST_DIR"; rm -rf "$SQC_TEST_DIR"; }
sqc_log "torn down"
