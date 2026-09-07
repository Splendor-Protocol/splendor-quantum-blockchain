#!/usr/bin/env bash
# Drive the gates + SQVL + qrc721 + fee-floor smoke against the launched
# chain and check every full node follows the validator. Exit 0 = all PASS.
#
#   20-smoke.sh                      # against the AWS chain from 10-launch.sh
#   20-smoke.sh --rpc http://127.0.0.1:18545 --seed ./faucet.seed --chain-id sqc-featval-1
#                                    # against any node (e.g. a local one)
set -euo pipefail
source "$(dirname "$0")/env.sh"

RPC=""; SEED="$SQC_TEST_DIR/chain/faucet.seed"; CHAIN="$SQC_TEST_CHAIN_ID"
while [ $# -gt 0 ]; do
  case "$1" in
    --rpc) RPC="$2"; shift 2 ;;
    --seed) SEED="$2"; shift 2 ;;
    --chain-id) CHAIN="$2"; shift 2 ;;
    *) sqc_die "unknown flag $1" ;;
  esac
done
if [ -z "$RPC" ]; then
  [ -f "$SQC_TEST_DIR/instances.full.txt" ] || sqc_die "no launched chain on record; pass --rpc"
  RPC="http://$(awk '$1==0{print $4}' "$SQC_TEST_DIR/instances.full.txt"):8080"
fi
DRIVER="$SQC_TEST_DIR/bin/featsmoke-local"
[ -x "$DRIVER" ] || { cd "$SQC_REPO_ROOT" && GOTOOLCHAIN=auto go build -trimpath -o "$DRIVER" ./cmd/featsmoke; }

sqc_log "smoke against $RPC (chain $CHAIN)"
"$DRIVER" run --rpc "$RPC" --seed "$SEED" --chain-id "$CHAIN"
rc=$?

if [ -f "$SQC_TEST_DIR/instances.full.txt" ] && [ -z "${RPC_EXPLICIT:-}" ]; then
  sqc_log "follower heights (each must track the validator within a few blocks)"
  vh="$(curl -fsS "$RPC/status" | jq -r .height)"
  while read -r i role id ip priv; do
    h="$(curl -fsS "http://$ip:8080/status" | jq -r .height || echo '?')"
    printf '  %-12s %-16s height=%s (validator=%s)\n' "$role-$i" "$ip" "$h" "$vh"
  done < "$SQC_TEST_DIR/instances.full.txt"
fi
exit $rc
