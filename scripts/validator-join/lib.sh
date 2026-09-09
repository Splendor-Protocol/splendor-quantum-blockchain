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
