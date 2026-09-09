#!/usr/bin/env bash
#
# run-live-join.sh — the sequential late-join proof, with real processes.
#
# Starts ONE genesis producer on a scratch chain, lets it advance, then brings
# validator B up against the RUNNING producer, registers B on-chain with a
# signed transaction, and proves the two of them are producing under a quorum.
# Then does the same for C. The producer is never stopped, restarted or reset,
# and no validator other than the genesis one is named or registered in the
# genesis document.
#
# What "activated" means here, and what this script asserts:
#   * blocks keep coming while a joiner is syncing and registering;
#   * after each join, DISTINCT addresses seal blocks in the sampled window —
#     a peer that only follows is not a validator;
#   * every block in the window carries a qc_root and a quorum of distinct
#     votes for that block's own hash and height;
#   * every node returns the same hash, state root and producer for every
#     height in the window;
#   * the producer's pid is the same at the end as at the start.
#
# Everything lives under a scratch directory (default /tmp/sqc-join-lab) on
# loopback ports, on a chain id generated per run. It never contacts devnet,
# mainnet or any deployed service, and it never reads genesis-production.json.
#
# Usage:
#   scripts/validator-join/run-live-join.sh                 # full 3-validator run
#   VALIDATORS=2 scripts/validator-join/run-live-join.sh     # stop after B
#   LAB=/tmp/mylab KEEP=1 scripts/validator-join/run-live-join.sh
#
# Env:
#   LAB              scratch dir                  (default /tmp/sqc-join-lab)
#   VALIDATORS       2 or 3                       (default 3)
#   PORT_BASE        first free RPC port to scan from (default 21545)
#   BLOCK_INTERVAL   seconds per block            (default 2)
#   OBSERVE_BLOCKS   heights sampled per phase    (default 12)
#   KEEP=1           leave the nodes running on exit
#   LAB_LOG_LEVEL    node log level               (default info)

set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$REPO/scripts/validator-join/lib.sh"

LAB=${LAB:-/tmp/sqc-join-lab}
BIN=$LAB/bin
VALIDATORS=${VALIDATORS:-3}
PORT_BASE=${PORT_BASE:-21545}
BLOCK_INTERVAL=${BLOCK_INTERVAL:-2}
OBSERVE_BLOCKS=${OBSERVE_BLOCKS:-12}
WALLET_PASS=${WALLET_PASS:-labpass}
TREASURY=A

PORT_BASE=$(pick_port_base "$PORT_BASE" 3) ||
	{ echo "no free run of 3 ports from $PORT_BASE" >&2; exit 1; }
PORT_A=$PORT_BASE
PORT_B=$((PORT_BASE + 1))
PORT_C=$((PORT_BASE + 2))
URL_A=http://127.0.0.1:$PORT_A
URL_B=http://127.0.0.1:$PORT_B
URL_C=http://127.0.0.1:$PORT_C

# A fresh chain id per run: the wallets, allocations and genesis hash are all
# new, so reusing an id would let a stale data dir be mistaken for this chain.
CHAIN_ID=${CHAIN_ID:-splendor-quantum-joinlab-$(date +%Y%m%d%H%M%S)}
TOKEN=$(head -c 24 /dev/urandom | xxd -p | tr -d '\n')

trap 'code=$?; if [[ -z "${KEEP:-}" ]]; then lab_teardown; fi; exit $code' EXIT

# NODES grows as validators join; the end-of-run checks read every live node.
NODES=("$URL_A" "$URL_B")

# final_checks is what makes the run a proof rather than a demo: the whole
# history has to be identical on every node, the producer has to be the same
# process it was at the start, and no node may have logged a consensus refusal.
final_checks() {
	step "the whole history agrees on every node, block 1 to the tip"
	# The sampled windows only cover the quorum phases. Two nodes disagreeing
	# about the CERTIFICATE of an early block — one storing the producer's vote,
	# the other a vote it had no right to cast — is invisible there and visible
	# here. --to is the tip minus one, so a node one block behind is not a
	# failure of agreement.
	local head
	head=$(status_field "$URL_A" height)
	python3 "$REPO/scripts/validator-join/observe.py" \
		--from 1 --to "$((head - 1))" --min-producers 1 --min-votes 1 \
		--json-out "$LAB/logs/history.json" \
		"${NODES[@]}" >"$LAB/logs/history.txt" ||
		{ tail -20 "$LAB/logs/history.txt" >&2; die "the nodes disagree somewhere in blocks 1..$((head - 1))"; }
	ok "blocks 1..$((head - 1)) identical on all ${#NODES[@]} nodes (hash, state root, producer, certificate)"

	step "final invariants"
	[[ "$(node_pid A)" == "$PID_A" ]] || die "producer A's pid changed: $PID_A -> $(node_pid A)"
	node_alive A || die "producer A is not running"
	ok "producer A ran unbroken as pid $PID_A for the whole run"

	local log
	for log in "$LAB"/logs/[ABC].log; do
		[[ -f "$log" ]] || continue
		if grep -qE "proposal from non-leader rejected|block certificate rejected|commit callback failed|refusing inbound block" "$log"; then
			bad "$(basename "$log") contains consensus rejections:"
			grep -nE "proposal from non-leader rejected|block certificate rejected|commit callback failed|refusing inbound block" "$log" | tail -5 >&2
			die "consensus rejections in $log"
		fi
	done
	ok "no consensus rejections in any node log"
}

# ── step 1: scratch workspace + binaries ─────────────────────────────────────

step "build the binaries and prepare a scratch chain"
rm -rf "$LAB"
mkdir -p "$LAB"/{bin,data,logs,run,keys}
printf '%s' "$WALLET_PASS" >"$LAB/run/wallet.pass"
chmod 600 "$LAB/run/wallet.pass"

(cd "$REPO" && go build -o "$BIN/sqc-node" ./cmd/node) || die "build sqc-node"
(cd "$REPO" && go build -o "$BIN/sqc-wallet" ./cmd/wallet) || die "build sqc-wallet"
(cd "$REPO" && go build -o "$BIN/sqc-modcall" ./cmd/sqc-modcall) || die "build sqc-modcall"
ok "binaries in $BIN"
say "chain id: $CHAIN_ID   ports: $PORT_A/$PORT_B/$PORT_C"

for name in A B C; do wallet_new "$name"; done
ADDR_A=$(wallet_addr A); PK_A=$(wallet_pubkey A)
ADDR_B=$(wallet_addr B)
ADDR_C=$(wallet_addr C)
[[ ${#PK_A} -eq 3904 ]] || die "validator A pubkey is ${#PK_A} hex chars, want 3904"
ok "A=$ADDR_A"
ok "B=$ADDR_B"
ok "C=$ADDR_C"

# The genesis document names ONE validator (A) and funds ONE account (A).
# B and C are strangers to it: they are not in the allocations, not in
# validator_pubkey_hex, and nothing registers them until they are running.
cat >"$LAB/genesis.json" <<EOF
{
  "chain_id": "$CHAIN_ID",
  "network_name": "Splendor Quantum Chain — validator-join lab",
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
ok "genesis document hash $DOC_HASH"

GENESIS_FLAGS=(--genesis "$LAB/genesis.json" --genesis-doc-hash "$DOC_HASH")

# ── step 2: one genesis producer, advancing on its own ───────────────────────

step "start the single genesis producer A and prove it advances"
node_start A "$PORT_A" --validator \
	--wallet "$LAB/keys/A.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	"${GENESIS_FLAGS[@]}"

wait_for 60 "A serving /status on $CHAIN_ID" serving "$URL_A" || die "A never came up"
wait_for 90 "A reached height 3 by itself" height_at_least "$URL_A" 3 || die "A is not producing"

PID_A=$(node_pid A)
GENESIS_HASH=$(hex32 "$URL_A/block/0" "d['hash']") || die "cannot read A's block 0"
ok "A pid=$PID_A genesis block hash=$GENESIS_HASH"

# A is the only key on the chain, so it self-commits: one vote, one qc_root.
step "register the genesis producer A on-chain (single-key quorum)"
register A "$URL_A" || die "A register failed"
wait_for 60 "registry lists 1 active validator" set_size_is "$URL_A" 1 || die "A did not register"
H_SOLO=$(status_field "$URL_A" height)
wait_for 60 "A kept producing after registering" height_at_least "$URL_A" $((H_SOLO + 2))

# ── step 3: fund B from the running chain, then join it ──────────────────────

step "fund B with a signed transfer from the running chain"
fund "$ADDR_B" 3000 "$URL_A" || die "funding B failed"
wait_for 60 "B's account is funded" funded "$ADDR_B" "$URL_A"

H_BEFORE_B=$(status_field "$URL_A" height)
say "A is at height $H_BEFORE_B — B joins a chain that is already running"

step "launch validator B against the RUNNING producer (no restart of A)"
node_start B "$PORT_B" --validator \
	--wallet "$LAB/keys/B.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	"${GENESIS_FLAGS[@]}" \
	--genesis-hash "$GENESIS_HASH" --validator-pubkey-hex "$PK_A" \
	--peer "$URL_A"

wait_for 60 "B serving /status on $CHAIN_ID" serving "$URL_B" || die "B never came up"
wait_for 120 "B caught up to A's tip" tracks_tip "$URL_B" "$URL_A" 2 || die "B never reached the tip"
[[ "$(node_pid A)" == "$PID_A" ]] || die "producer A was restarted during B's join"

step "register B on-chain with a signed transaction, submitted to B itself"
# Submitted to B, not to the producer. B is not in the validator set yet, so it
# never leads a height and can never include its own registration: the
# transaction only lands because the node relays it to its peers. That is the
# flow an operator actually uses — register from the node being brought up.
register B "$URL_B" || die "B register failed"
wait_for 90 "registry lists 2 active validators" set_size_is "$URL_A" 2 || die "B did not register"
wait_for 90 "B's own view of the registry shows 2" set_size_is "$URL_B" 2

H_QUORUM_B=$(status_field "$URL_A" height)
say "two-validator quorum from height ~$H_QUORUM_B"

step "observe $OBSERVE_BLOCKS blocks under the two-validator quorum"
TARGET=$((H_QUORUM_B + OBSERVE_BLOCKS + 2))
wait_for $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) \
	"chain advanced to $TARGET with two validators" height_at_least "$URL_A" "$TARGET" ||
	die "the chain stalled after B joined — see $LAB/logs/"
wait_for 60 "B is at the tip too" height_at_least "$URL_B" "$TARGET"

python3 "$REPO/scripts/validator-join/observe.py" \
	--from $((H_QUORUM_B + 2)) --to "$TARGET" \
	--min-producers 2 --min-votes 2 \
	--json-out "$LAB/logs/window-2v.json" \
	"$URL_A" "$URL_B" || die "the two-validator window did not hold"

[[ "$(node_pid A)" == "$PID_A" ]] || die "producer A was restarted"
node_alive A || die "producer A died"
ok "producer A still running as pid $PID_A"

if ((VALIDATORS < 3)); then
	say "VALIDATORS=$VALIDATORS — stopping after B"
	final_checks
	exit 0
fi
NODES+=("$URL_C")

# ── step 4: the same again, later, for C ─────────────────────────────────────

step "fund C from the running chain"
fund "$ADDR_C" 3000 "$URL_A" || die "funding C failed"
wait_for 60 "C's account is funded" funded "$ADDR_C" "$URL_A"

H_BEFORE_C=$(status_field "$URL_A" height)
say "chain is at height $H_BEFORE_C — C joins two running validators"

step "launch validator C against the running two-validator chain"
node_start C "$PORT_C" --validator \
	--wallet "$LAB/keys/C.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	"${GENESIS_FLAGS[@]}" \
	--genesis-hash "$GENESIS_HASH" --validator-pubkey-hex "$PK_A" \
	--peer "$URL_A" --peer "$URL_B"

wait_for 60 "C serving /status on $CHAIN_ID" serving "$URL_C" || die "C never came up"
wait_for 180 "C caught up to the tip" tracks_tip "$URL_C" "$URL_A" 2 || die "C never reached the tip"

step "register C on-chain with a signed transaction, submitted to C itself"
register C "$URL_C" || die "C register failed"
for u in "$URL_A" "$URL_B" "$URL_C"; do
	wait_for 120 "$u sees 3 active validators" set_size_is "$u" 3 || die "C did not register everywhere"
done

H_QUORUM_C=$(status_field "$URL_A" height)
step "observe $OBSERVE_BLOCKS blocks under the three-validator quorum"
TARGET=$((H_QUORUM_C + OBSERVE_BLOCKS + 2))
wait_for $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 5 + 90)) \
	"chain advanced to $TARGET with three validators" height_at_least "$URL_A" "$TARGET" ||
	die "the chain stalled after C joined — see $LAB/logs/"
for u in "$URL_B" "$URL_C"; do
	wait_for 90 "$u is at the tip" height_at_least "$u" "$TARGET"
done

python3 "$REPO/scripts/validator-join/observe.py" \
	--from $((H_QUORUM_C + 2)) --to "$TARGET" \
	--min-producers 3 --min-votes 3 \
	--json-out "$LAB/logs/window-3v.json" \
	"$URL_A" "$URL_B" "$URL_C" || die "the three-validator window did not hold"

final_checks

printf '\n\033[1;32mPASS\033[0m — sequential late join proven for %d validators on %s\n' "$VALIDATORS" "$CHAIN_ID" >&2
printf '       logs: %s/logs   evidence: %s/logs/window-*.json\n' "$LAB" "$LAB" >&2
