#!/usr/bin/env bash
# SQVL /verify/* door harness (check → attest → settle → proof).
# Does not stop load/indexer. Prefer go test offline; this hits a live RPC.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
RPC="${SQC_RPC:-http://3.84.91.28:8080}"
CHAIN_ID="${SQC_CHAIN_ID:-sqc-featval-1}"
SEED="${SQC_FAUCET_SEED:-$HOME/.sqc-feat-validator/chain/faucet.seed}"
BIN="${SQC_FEATSMOKE_BIN:-}"
if [[ -z "$BIN" ]]; then
  if [[ -x "$HOME/.sqc-feat-validator/bin/featsmoke-local" ]]; then
    BIN="$HOME/.sqc-feat-validator/bin/featsmoke-local"
  elif [[ -x "$HOME/.sqc-feat-validator/bin/featsmoke" ]]; then
    BIN="$HOME/.sqc-feat-validator/bin/featsmoke"
  else
    (cd "$ROOT" && go build -o /tmp/featsmoke-sqvl ./cmd/featsmoke)
    BIN=/tmp/featsmoke-sqvl
  fi
fi
echo "SQVL harness: $BIN verify --rpc $RPC --seed $SEED --chain-id $CHAIN_ID"
exec "$BIN" verify --rpc "$RPC" --seed "$SEED" --chain-id "$CHAIN_ID"
