#!/usr/bin/env bash
# Fund the 20 funders from the faucet — from the laptop, through the public
# devnet RPC, in one txload invocation (it refuses a node whose chain id is not
# devnet). 20 transfers of $DS_FUNDER_SPLD SPLD; nothing else is sent (the
# 0.001/s rate never ticks inside the 4-minute window).
#
#   20-fund-funders.sh
set -euo pipefail
source "$(dirname "$0")/env.sh"
KEYS="$DS_WORK/keys"; TXLOAD="$DS_WORK/bin/txload-local"
[ -f "$KEYS/faucet-devnet.seed" ] || ds_die "run 10-keys.sh first"
units=$(python3 -c "print($DS_FUNDER_SPLD * 10**18)")
total=$(python3 -c "print($DS_FUNDER_SPLD * $DS_K)")
[ "$total" -le 50000 ] || ds_die "budget: $total SPLD > 50,000 cap"
out="$DS_WORK/runs/fund-funders-$(date -u +%Y%m%dT%H%M%SZ)"; mkdir -p "$out"
ds_log "funding $DS_K funders x $DS_FUNDER_SPLD SPLD ($total SPLD) -> $out"
"$TXLOAD" run --rpc "$DS_RPC" --chain-id "$DS_CHAIN_ID" --seed "$KEYS/faucet-devnet.seed" \
  --senders "$DS_K" --sender-offset "$DS_FUNDER_OFFSET" --fund "$units" \
  --rate 0.001 --duration 4m --fund-deadline 3m --scan-node -1 --sample 0 --topup-every 0 \
  --amount-min "$DS_TX_AMOUNT_UNITS" --amount-max "$DS_TX_AMOUNT_UNITS" --interval 30s \
  --stats "$out/stats.jsonl" --summary "$out/summary.json" 2>&1 | tee "$out/txload.log" | grep -v '^  s1000'
for i in $(seq -w 0 $((DS_K - 1))); do
  a=$(cat "$KEYS/funder-$i.address")
  printf 'funder-%s %s %s SPLD\n' "$i" "$a" "$(ds_balance_spld "$a")"
done
