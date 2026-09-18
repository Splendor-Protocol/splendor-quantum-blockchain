#!/usr/bin/env bash
# Keys. The faucet seed stays on the laptop; only the 20 FUNDER seeds go to the host.
#
#   10-keys.sh
#
# funder i = txload sender (100000+i) of the faucet: seed = SHA3-256("sqc-txload/v1/"
# + faucet address + "/" + index) — exactly cmd/txload deriveSender/senderSeed, so
# `txload addrs --seed faucet.seed --senders 20 --sender-offset 100000` and
# `txload addrs --seed funder-NN.seed --senders 1` must agree; this script checks
# all 20 before anything is uploaded. Files: $DS_WORK/keys/{faucet-devnet,funder-NN}.seed (600).
set -euo pipefail
source "$(dirname "$0")/env.sh"; source "$DS_HERE/lib/ssm.sh"
umask 077
KEYS="$DS_WORK/keys"
TXLOAD="$DS_WORK/bin/txload-local"
[ -x "$TXLOAD" ] || (cd "$DS_REPO_ROOT" && go build -trimpath -o "$TXLOAD" ./cmd/txload)

# qkey refuses a TTY without --show; a command substitution is not a TTY.
[ -x "$DS_QKEY" ] || ds_die "qkey not found at $DS_QKEY (set DS_QKEY)"
SEED_HEX="$("$DS_QKEY" seed "$DS_FAUCET_KEY")"
[ ${#SEED_HEX} -eq 64 ] || ds_die "faucet seed is not 64 hex chars"
printf '%s\n' "$SEED_HEX" > "$KEYS/faucet-devnet.seed"; chmod 600 "$KEYS/faucet-devnet.seed"

python3 - "$DS_FAUCET_ADDR" "$DS_K" "$DS_FUNDER_OFFSET" "$KEYS" <<'PY'
import hashlib, os, sys
addr, k, off, keys = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]
os.umask(0o077)
for i in range(k):
    seed = hashlib.sha3_256(("sqc-txload/v1/%s/%d" % (addr, off + i)).encode()).hexdigest()
    open(os.path.join(keys, "funder-%02d.seed" % i), "w").write(seed + "\n")
print("derived %d funder seeds" % k)
PY

"$TXLOAD" addrs --rpc "$DS_RPC" --chain-id "$DS_CHAIN_ID" --seed "$KEYS/faucet-devnet.seed" \
  --senders "$DS_K" --sender-offset "$DS_FUNDER_OFFSET" > "$KEYS/funders-from-faucet.txt"
grep -q "^funder $DS_FAUCET_ADDR" "$KEYS/funders-from-faucet.txt" || ds_die "txload does not derive the faucet address from the qkey seed"
ok=0
for i in $(seq -w 0 $((DS_K - 1))); do
  a=$("$TXLOAD" addrs --rpc x --chain-id "$DS_CHAIN_ID" --seed "$KEYS/funder-$i.seed" --senders 1 | awk '/^funder/ {print $2}')
  b=$(awk -v n="s$((10#$i + DS_FUNDER_OFFSET))" '$1==n {print $2}' "$KEYS/funders-from-faucet.txt")
  [ "$a" = "$b" ] && ok=$((ok + 1)) || ds_warn "funder $i: $a != $b"
  echo "$a" > "$KEYS/funder-$i.address"
done
[ "$ok" -eq "$DS_K" ] || ds_die "only $ok/$DS_K funder seeds match txload's derivation"
ds_log "faucet address matches qkey; $ok/$DS_K funder seeds match txload"

# Upload the funder seeds (and nothing else) to the host.
f=$(mktemp); {
  echo '#!/bin/bash'; echo 'set -euo pipefail'; echo "umask 077; mkdir -p $DS_HOST_ROOT/keys"
  for i in $(seq -w 0 $((DS_K - 1))); do
    echo "echo '$(tr -d '\n' < "$KEYS/funder-$i.seed")' > $DS_HOST_ROOT/keys/funder-$i.seed"
  done
  echo "chmod 600 $DS_HOST_ROOT/keys/*.seed; ls $DS_HOST_ROOT/keys | wc -l"
} > "$f"
ssm_run "$DS_INSTANCE" "$f" 120 >/dev/null; rm -f "$f"
ds_log "uploaded $DS_K funder seeds to $DS_INSTANCE:$DS_HOST_ROOT/keys (host holds no faucet material)"
