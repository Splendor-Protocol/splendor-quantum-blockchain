#!/usr/bin/env bash
#
# run-state-sync-join.sh — the same late join, but for a chain whose history
# the joiner does not replay.
#
# A validator D is started ONCE, with --validator --state-sync, against a
# producer that keeps running. It takes the fork schedule and minted supply
# from the pinned genesis document, imports the producer's live state snapshot
# at its head, follows from the next block, registers with a signed transaction
# submitted to itself, and then shares the leadership.
#
# This combination used to be refused at start-up ("--state-sync cannot be
# combined with --validator"), so the only route was: join as a full node,
# stop it, restart it as a validator. Nothing about the problem required that
# second process.
#
# Same guarantees as run-live-join.sh: scratch chain id, loopback ports,
# temporary directory, no contact with any deployed network.
#
# Usage: scripts/validator-join/run-state-sync-join.sh
# Env:   LAB, PORT_BASE, BLOCK_INTERVAL, OBSERVE_BLOCKS, KEEP, LAB_LOG_LEVEL

set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$REPO/scripts/validator-join/lib.sh"

LAB=${LAB:-/tmp/sqc-statesync-lab}
BIN=$LAB/bin
PORT_BASE=${PORT_BASE:-22545}
BLOCK_INTERVAL=${BLOCK_INTERVAL:-2}
OBSERVE_BLOCKS=${OBSERVE_BLOCKS:-10}
WALLET_PASS=${WALLET_PASS:-labpass}
TREASURY=A

PORT_BASE=$(pick_port_base "$PORT_BASE" 2) ||
	{ echo "no free run of 2 ports from $PORT_BASE" >&2; exit 1; }
PORT_A=$PORT_BASE
PORT_D=$((PORT_BASE + 1))
URL_A=http://127.0.0.1:$PORT_A
URL_D=http://127.0.0.1:$PORT_D

CHAIN_ID=${CHAIN_ID:-splendor-quantum-snaplab-$(date +%Y%m%d%H%M%S)}
TOKEN=$(head -c 24 /dev/urandom | xxd -p | tr -d '\n')

trap 'code=$?; if [[ -z "${KEEP:-}" ]]; then lab_teardown; fi; exit $code' EXIT

step "build the binaries and prepare a scratch chain"
rm -rf "$LAB"
mkdir -p "$LAB"/{bin,data,logs,run,keys}
printf '%s' "$WALLET_PASS" >"$LAB/run/wallet.pass"
chmod 600 "$LAB/run/wallet.pass"
(cd "$REPO" && go build -o "$BIN/sqc-node" ./cmd/node) || die "build sqc-node"
(cd "$REPO" && go build -o "$BIN/sqc-wallet" ./cmd/wallet) || die "build sqc-wallet"
(cd "$REPO" && go build -o "$BIN/sqc-modcall" ./cmd/sqc-modcall) || die "build sqc-modcall"
say "chain id: $CHAIN_ID   ports: $PORT_A/$PORT_D"

for name in A D; do wallet_new "$name"; done
ADDR_A=$(wallet_addr A); PK_A=$(wallet_pubkey A)
ADDR_D=$(wallet_addr D)
ok "A=$ADDR_A"
ok "D=$ADDR_D"

cat >"$LAB/genesis.json" <<EOF
{
  "chain_id": "$CHAIN_ID",
  "network_name": "Splendor Quantum Chain — state-sync join lab",
  "timestamp": 1741545600,
  "validator_pubkey_hex": "$PK_A",
  "allocations": [
    { "address": "$ADDR_A", "balance": "26000000000000000000000000000", "comment": "lab treasury and genesis validator" }
  ],
  "total_supply": "26000000000000000000000000000",
  "forks": {
    "module_revert_v1": 1, "qrc20_v2": 1, "qrc1155_v1": 1, "exchange_v1": 1,
    "bridge_v1": 1, "verify_v1": 1, "validators_v1": 1, "lending_v1": 1,
    "oracle_v1": 1, "multisig_v1": 1, "governance_v1": 1, "qrc721_v1": 1,
    "qrc4626_v1": 1, "min_fee_v1": 1, "multi_validator": 1
  }
}
EOF
DOC_HASH=$("$BIN/sqc-node" genesis-hash "$LAB/genesis.json")
GENESIS_FLAGS=(--genesis "$LAB/genesis.json" --genesis-doc-hash "$DOC_HASH")

step "start the genesis producer A and let it build some history"
node_start A "$PORT_A" --validator \
	--wallet "$LAB/keys/A.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	"${GENESIS_FLAGS[@]}"
wait_for 60 "A serving /status on $CHAIN_ID" serving "$URL_A" || die "A never came up"
wait_for 90 "A reached height 4" height_at_least "$URL_A" 4 || die "A is not producing"
PID_A=$(node_pid A)
GENESIS_HASH=$(hex32 "$URL_A/block/0" "d['hash']") || die "cannot read A's block 0"

step "register the producer, then fund D"
register A "$URL_A" || die "A register failed"
wait_for 60 "registry lists 1 active validator" set_size_is "$URL_A" 1
fund "$ADDR_D" 3000 "$URL_A" || die "funding D failed"
wait_for 60 "D's account is funded" funded "$ADDR_D" "$URL_A"

H_BEFORE=$(status_field "$URL_A" height)
say "A is at height $H_BEFORE — D will import its state rather than replay it"

step "launch D ONCE as --validator --state-sync against the running producer"
node_start D "$PORT_D" --validator --state-sync \
	--wallet "$LAB/keys/D.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	"${GENESIS_FLAGS[@]}" \
	--genesis-hash "$GENESIS_HASH" --validator-pubkey-hex "$PK_A" \
	--peer "$URL_A"
wait_for 60 "D serving /status on $CHAIN_ID" serving "$URL_D" || die "D never came up"
wait_for 120 "D imported a snapshot and reached the tip" tracks_tip "$URL_D" "$URL_A" 2 ||
	{ tail -30 "$LAB/logs/D.log" >&2; die "D never state-synced"; }

grep -q "state snapshot imported" "$LAB/logs/D.log" ||
	die "D reached the tip without importing a snapshot — it replayed history, so this proves nothing"
SNAP_H=$(grep "state snapshot imported" "$LAB/logs/D.log" | grep -oE 'height=[0-9]+' | head -1 | cut -d= -f2 || true)
ok "D imported the state snapshot at height ${SNAP_H:-?}"

# The point of state sync: D has NO history below the snapshot.
if curl -fsS "$URL_D/block/1" >/dev/null 2>&1; then
	die "D holds block 1 — it replayed the chain instead of importing a snapshot"
fi
ok "D holds no block below the snapshot, as intended"

step "register D with a signed transaction submitted to D itself"
register D "$URL_D" || die "D register failed"
for u in "$URL_A" "$URL_D"; do
	wait_for 120 "$u sees 2 active validators" set_size_is "$u" 2 || die "D did not register"
done

H_QUORUM=$(status_field "$URL_A" height)
step "observe $OBSERVE_BLOCKS blocks under the two-validator quorum"
TARGET=$((H_QUORUM + OBSERVE_BLOCKS + 2))
wait_for $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) \
	"chain advanced to $TARGET" height_at_least "$URL_A" "$TARGET" ||
	die "the chain stalled after D joined — see $LAB/logs/"
wait_for 60 "D is at the tip too" height_at_least "$URL_D" "$TARGET"

python3 "$REPO/scripts/validator-join/observe.py" \
	--from $((H_QUORUM + 2)) --to "$TARGET" \
	--min-producers 2 --min-votes 2 \
	--json-out "$LAB/logs/window.json" \
	"$URL_A" "$URL_D" || die "the two-validator window did not hold"

step "final invariants"
[[ "$(node_pid A)" == "$PID_A" ]] || die "producer A's pid changed"
node_alive A || die "producer A is not running"
ok "producer A ran unbroken as pid $PID_A, never stopped for the join"
for log in "$LAB"/logs/[AD].log; do
	if grep -qE "proposal from non-leader rejected|block certificate rejected|commit callback failed" "$log"; then
		grep -nE "proposal from non-leader rejected|block certificate rejected|commit callback failed" "$log" | tail -5 >&2
		die "consensus rejections in $log"
	fi
done
ok "no consensus rejections in either node log"

printf '\n\033[1;32mPASS\033[0m — a validator joined by state sync in ONE launch on %s\n' "$CHAIN_ID" >&2
