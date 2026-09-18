#!/usr/bin/env bash
# Shared helpers for the validator-join lab.
#
# Sourced by run-live-join.sh. Nothing here touches a deployed network: every
# function operates on a scratch chain id, scratch wallets and loopback ports
# under $LAB, which the caller creates and owns.

set -o pipefail

# ── logging ──────────────────────────────────────────────────────────────────

LAB_STEP=0

say()  { printf '\033[1;36m[lab]\033[0m %s\n' "$*" >&2; }
step() { LAB_STEP=$((LAB_STEP + 1)); printf '\n\033[1;35m══ step %d: %s\033[0m\n' "$LAB_STEP" "$*" >&2; }
ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*" >&2; }
bad()  { printf '\033[1;31m  ✗\033[0m %s\n' "$*" >&2; }
die()  { bad "$*"; exit 1; }

# ── json ─────────────────────────────────────────────────────────────────────

# jget URL EXPR — fetch JSON and print python EXPR evaluated against `d`.
# Prints nothing and returns non-zero when the node is unreachable or the
# expression does not resolve, so callers can poll with it.
jget() {
	local url="$1" expr="$2" body
	body=$(curl -fsS --max-time 5 "$url" 2>/dev/null) || return 1
	printf '%s' "$body" | python3 -c "
import json,sys
d=json.load(sys.stdin)
try:
    v=($expr)
except Exception:
    sys.exit(1)
if v is None:
    sys.exit(1)
print(v)
" 2>/dev/null
}

# hex32 URL EXPR — the same, for a Go [32]byte serialised as a JSON int array.
hex32() {
	local url="$1" expr="$2" body
	body=$(curl -fsS --max-time 5 "$url" 2>/dev/null) || return 1
	printf '%s' "$body" | python3 -c "
import json,sys
d=json.load(sys.stdin)
v=($expr)
print(bytes(v).hex())
" 2>/dev/null
}

# ── ports ────────────────────────────────────────────────────────────────────

# port_free P — true when nothing is listening on 127.0.0.1:P.
port_free() {
	python3 - "$1" <<'PY'
import socket, sys
s = socket.socket()
try:
    s.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(1)
finally:
    s.close()
PY
}

# pick_port_base FIRST COUNT — echo the first base B >= FIRST for which the
# RPC ports B..B+COUNT-1 and the metrics ports B+1000.. are all free.
#
# Other labs run on this machine (a second session's chain, a load driver), so
# a fixed base silently attaches this harness to somebody else's node — which
# is exactly the failure that makes a "proof" meaningless.
pick_port_base() {
	local first="$1" count="$2" base p all
	for ((base = first; base < first + 500; base += 10)); do
		all=1
		for ((p = 0; p < count; p++)); do
			port_free $((base + p)) || { all=0; break; }
			port_free $((base + p + 1000)) || { all=0; break; }
		done
		((all)) && { echo "$base"; return 0; }
	done
	return 1
}

# ── polling ──────────────────────────────────────────────────────────────────

# status_field URL FIELD — read a /status field, but only from a node that is
# on OUR chain. Without the chain-id guard a stale node left listening on the
# port would answer every assertion in this harness and pass all of them.
status_field() {
	local url="$1" field="$2" body
	body=$(curl -fsS --max-time 5 "$url/status" 2>/dev/null) || return 1
	printf '%s' "$body" | CHAIN_ID="$CHAIN_ID" python3 -c "
import json,os,sys
d=json.load(sys.stdin)
if d.get('chain_id') != os.environ['CHAIN_ID']:
    sys.exit(1)
v=d.get('$field')
if v is None:
    sys.exit(1)
print(v)
" 2>/dev/null
}

# wait_for TIMEOUT_S DESC CMD... — poll CMD until it succeeds.
wait_for() {
	local timeout="$1" desc="$2"
	shift 2
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		if "$@" >/dev/null 2>&1; then
			ok "$desc"
			return 0
		fi
		sleep 1
	done
	bad "timed out after ${timeout}s waiting for: $desc"
	return 1
}

# serving URL — the node answers /status and is on our chain.
serving() { status_field "$1" height >/dev/null; }

# height_at_least URL N
height_at_least() {
	local h
	h=$(status_field "$1" height) || return 1
	((h >= $2))
}

# set_size_is URL N
set_size_is() {
	local n
	n=$(status_field "$1" validator_set_size) || return 1
	((n == $2))
}

# bond_returned ADDR NODE_URL BEFORE_UNITS — the account's balance has grown by
# at least most of a bond (2,500 SPLD) since BEFORE_UNITS, i.e. finalize_exit
# credited the bond back. A tolerance is left for the fees paid meanwhile.
bond_returned() {
	local now
	now=$(balance_units "$1" "$2") || return 1
	BEFORE="$3" NOW="$now" python3 -c '
import os
before=int(os.environ["BEFORE"]); now=int(os.environ["NOW"])
bond=2500*10**18
# credited at least 90% of the bond back (fees paid in between are < 1 SPLD)
import sys; sys.exit(0 if now-before >= bond*9//10 else 1)'
}

# tracks_tip FOLLOWER_URL LEADER_URL SLACK — follower is within SLACK blocks.
tracks_tip() {
	local f l
	f=$(status_field "$1" height) || return 1
	l=$(status_field "$2" height) || return 1
	((f + $3 >= l && f > 0))
}

# funded ADDR NODE_URL — the account has a non-zero balance.
funded() {
	local bal
	bal=$(jget "$2/balance/$1" "d.get('balance')") || return 1
	[[ -n "$bal" && "$bal" != "0" ]]
}

# ── node processes ───────────────────────────────────────────────────────────

# node_start NAME PORT [extra flags...] — background an sqc-node, record its pid.
node_start() {
	local name="$1" port="$2"
	shift 2
	local log="$LAB/logs/$name.log"
	mkdir -p "$LAB/data/$name" "$LAB/logs"
	"$BIN/sqc-node" \
		--data-dir "$LAB/data/$name" \
		--rpc-addr "127.0.0.1:$port" \
		--metrics-addr "127.0.0.1:$((port + 1000))" \
		--public-url "http://127.0.0.1:$port" \
		--chain-id "$CHAIN_ID" \
		--rpc-auth-token "$TOKEN" \
		--allow-private-peers \
		--block-interval "$BLOCK_INTERVAL" \
		--log-level "${LAB_LOG_LEVEL:-info}" \
		"$@" >"$log" 2>&1 &
	local pid=$!
	echo "$pid" >"$LAB/run/$name.pid"
	say "started $name pid=$pid port=$port log=$log"

	# Fail here rather than letting a node that could not bind its port, or
	# refused its flags, be diagnosed twenty seconds later as "never synced".
	sleep 2
	if ! kill -0 "$pid" 2>/dev/null; then
		bad "$name exited immediately:"
		tail -20 "$log" >&2
		return 1
	fi
}

node_pid() { cat "$LAB/run/$1.pid" 2>/dev/null; }

node_alive() {
	local pid
	pid=$(node_pid "$1") || return 1
	[[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

node_stop() {
	local pid
	pid=$(node_pid "$1" 2>/dev/null) || return 0
	[[ -n "$pid" ]] || return 0
	kill "$pid" 2>/dev/null || true
	wait "$pid" 2>/dev/null || true
	rm -f "$LAB/run/$1.pid"
}

lab_teardown() {
	local n
	for n in $(ls "$LAB/run" 2>/dev/null | sed 's/\.pid$//'); do
		node_stop "$n"
	done
}

# ── wallets ──────────────────────────────────────────────────────────────────

# wallet_new NAME — create $LAB/keys/NAME.sqwallet and export NAME_ADDR/_PK.
wallet_new() {
	local name="$1"
	mkdir -p "$LAB/keys"
	local w="$LAB/keys/$name.sqwallet"
	printf '%s\n%s\n' "$WALLET_PASS" "$WALLET_PASS" |
		"$BIN/sqc-wallet" generate -o "$w" >"$LAB/logs/wallet-$name.log" 2>&1 ||
		die "wallet generate $name failed (see $LAB/logs/wallet-$name.log)"
	chmod 600 "$w"
}

wallet_addr() {
	printf '%s\n' "$WALLET_PASS" | "$BIN/sqc-wallet" address -w "$LAB/keys/$1.sqwallet" 2>/dev/null |
		tr -d '[:space:]'
}

wallet_pubkey() {
	printf '%s\n' "$WALLET_PASS" | "$BIN/sqc-wallet" pubkey -w "$LAB/keys/$1.sqwallet" 2>/dev/null |
		tr -d '[:space:]'
}

# ── chain actions ────────────────────────────────────────────────────────────

# LAB_FEE is well above the min_fee_v1 floor (1e15) so a congestion multiplier
# (2x/8x/32x) never turns a lab transaction into a mysterious 400.
LAB_FEE=${LAB_FEE:-100000000000000000}

# fund NAME AMOUNT_SPLD NODE_URL — transfer from the treasury wallet.
fund() {
	local to_addr="$1" amount="$2" node="$3"
	printf '%s\n' "$WALLET_PASS" | "$BIN/sqc-wallet" send \
		-w "$LAB/keys/$TREASURY.sqwallet" \
		--node "$node" --chain-id "$CHAIN_ID" \
		--to "$to_addr" --amount "$amount" --fee "$LAB_FEE" \
		>>"$LAB/logs/fund.log" 2>&1
}

# register NAME NODE_URL — signed validators.register from NAME's own key.
register() {
	local name="$1" node="$2"
	"$BIN/sqc-modcall" write \
		--node "$node" \
		--wallet "$LAB/keys/$name.sqwallet" \
		--pass-file "$LAB/run/wallet.pass" \
		--chain-id "$CHAIN_ID" \
		--fee "$LAB_FEE" \
		--module validators --action register \
		--params '{"pubkey":"{{PUBKEY}}"}' \
		--wait 2>&1 | tee -a "$LAB/logs/register-$name.log"
}

# balance_units ADDR NODE_URL
balance_units() { jget "$2/balance/$1" "d.get('balance')"; }

# ── fault-lab chain actions ──────────────────────────────────────────────────
#
# These mirror register() above. They are what the four-validator fault lab
# (run-fault-lab.sh) adds on top of the join lab: admission approval, the exit
# sequence, the forged-block prober, the leader schedule and process pause.

# approve APPROVER ADDR NODE — an active validator opens admission for ADDR.
# Under validators_admission_v1 a joiner that is not the block producer cannot
# register until an active validator has approved its address. --require-success
# so a soft "not an active validator" is a step failure, not a silent no-op.
approve() {
	local approver="$1" addr="$2" node="$3"
	"$BIN/sqc-modcall" write \
		--node "$node" \
		--wallet "$LAB/keys/$approver.sqwallet" \
		--pass-file "$LAB/run/wallet.pass" \
		--chain-id "$CHAIN_ID" \
		--fee "$LAB_FEE" \
		--module validators --action approve \
		--params "{\"address\":\"$addr\"}" \
		--wait --require-success 2>&1 | tee -a "$LAB/logs/approve-$approver-$addr.log"
}

# unregister NAME NODE — NAME leaves the active set; bond starts unbonding and
# returns after UnbondingBlocks (32) at finalize_exit. The record is Exiting,
# so ListActive drops NAME immediately (validator_set_size falls) even though
# the bond is still owed.
unregister() {
	local name="$1" node="$2"
	"$BIN/sqc-modcall" write \
		--node "$node" \
		--wallet "$LAB/keys/$name.sqwallet" \
		--pass-file "$LAB/run/wallet.pass" \
		--chain-id "$CHAIN_ID" \
		--fee "$LAB_FEE" \
		--module validators --action unregister \
		--params '{}' \
		--wait --require-success 2>&1 | tee -a "$LAB/logs/unregister-$name.log"
}

# finalize_exit NAME NODE — return NAME's bond once the unbonding delay passed.
# Refused (soft) with "unbonding until height N" before then, which is why the
# caller waits height >= exit height first.
finalize_exit() {
	local name="$1" node="$2"
	"$BIN/sqc-modcall" write \
		--node "$node" \
		--wallet "$LAB/keys/$name.sqwallet" \
		--pass-file "$LAB/run/wallet.pass" \
		--chain-id "$CHAIN_ID" \
		--fee "$LAB_FEE" \
		--module validators --action finalize_exit \
		--params '{}' \
		--wait --require-success 2>&1 | tee -a "$LAB/logs/finalize-$name.log"
}

# node_url NAME must be provided by the caller (run-fault-lab defines it): it
# echoes a validator's own RPC URL. forge reads it. It is a function, not an
# associative array, so the lab runs under the macOS system bash (3.2).

# forge NAME TARGET_URL — NAME seals a syntactically valid, correctly linked
# block with its own key and pushes it to TARGET's block-ingest path. Every
# honest target must REFUSE it: below the certificate check the block carries no
# quorum, and its state root is junk. cmdForge exits 0 when the target refuses
# (HTTP != 200 — the expected outcome) and NON-ZERO only when the target
# ACCEPTS it (HTTP 200 — the finding, "the producer gate did not hold"). The
# refusal reason travels back in the HTTP response body cmdForge prints, NOT in
# the target's log: the ingest handler answers 4xx without logging, so this
# probe never trips no_refusals.
forge() {
	local name="$1" target="$2"
	local self; self=$(node_url "$name")
	[[ -n "$self" ]] || { bad "forge: no NODE_URL for $name"; return 2; }
	"$BIN/sqc-modcall" forge \
		--node "$self" --target "$target" \
		--wallet "$LAB/keys/$name.sqwallet" \
		--pass-file "$LAB/run/wallet.pass" \
		--chain-id "$CHAIN_ID" \
		--auth-token "$TOKEN" 2>&1
}

# ACTIVE is the ordered list of validator NAMEs in the on-chain registry index
# (registration order). The leader schedule is ACTIVE[(height+round) % n], so
# with a known order the leader of any height is computable. The caller keeps it
# in step with the registry on every add / remove / re-add.
ACTIVE=()

# leader_at HEIGHT [ROUND] — echo the NAME scheduled to lead HEIGHT at ROUND
# (default 0), from the current ACTIVE order. Mirrors BFTEngine.LeaderAddress.
leader_at() {
	local h="$1" r="${2:-0}" n=${#ACTIVE[@]}
	((n > 0)) || { echo ""; return 1; }
	echo "${ACTIVE[$(((h + r) % n))]}"
}

# next_leader_height AFTER NAME — smallest round-0 height strictly greater than
# AFTER whose leader is NAME. Used to line a scenario up on a chosen leader.
next_leader_height() {
	local after="$1" want="$2" n=${#ACTIVE[@]} h
	for ((h = after + 1; h <= after + n + 1; h++)); do
		[[ "$(leader_at "$h")" == "$want" ]] && { echo "$h"; return 0; }
	done
	return 1
}

# node_pause NAME / node_resume NAME — freeze and thaw a node's whole process
# with SIGSTOP / SIGCONT. A paused node stops answering /status and stops
# voting; the honest quorum must carry the chain without it. kill is the right
# tool: it models a crashed / partitioned member without corrupting its data.
node_pause() {
	local pid
	pid=$(node_pid "$1") && [[ -n "$pid" ]] && kill -STOP "$pid" 2>/dev/null &&
		say "paused $1 (pid $pid, SIGSTOP)"
}
node_resume() {
	local pid
	pid=$(node_pid "$1") && [[ -n "$pid" ]] && kill -CONT "$pid" 2>/dev/null &&
		say "resumed $1 (pid $pid, SIGCONT)"
}

# NO_REFUSALS_RE is the consensus-refusal set: a leader check tripping, a
# certificate refused inside the engine, a commit callback failing, an inbound
# block refused by the producer gate, or any CRITICAL invariant. None may appear
# in an honest node's log during a clean window. Factored from run-live-join's
# final_checks, widened with the inbound-block and CRITICAL guards.
NO_REFUSALS_RE="proposal from non-leader rejected|block certificate rejected|commit callback failed|refusing inbound block|CRITICAL:"

# no_refusals LOG... — non-zero (and a diagnosis) if any log holds a refusal.
no_refusals() {
	local log rc=0
	for log in "$@"; do
		[[ -f "$log" ]] || continue
		if grep -qE "$NO_REFUSALS_RE" "$log"; then
			bad "$(basename "$log") contains consensus refusals:"
			grep -nE "$NO_REFUSALS_RE" "$log" | tail -6 >&2
			rc=1
		fi
	done
	return $rc
}
