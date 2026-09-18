#!/usr/bin/env bash
#
# run-fault-lab.sh — a LOCAL four-validator fault lab for Splendor Quantum Chain.
#
# It builds a fresh scratch chain that schedules the two-phase BFT fork
# (bft_rounds_v1) alongside devnet's rules (modules_v2, validators_admission_v1),
# brings four real sqc-node validators up in a known order A,B,C,D, and then
# proves, against real processes and the real network stack:
#
#   1. add    — A self-registers, then B, C and D each join a RUNNING chain and
#               are activated (A approves, they register, the set widens).
#   2. remove — D unregisters, the set falls to three, the n=3 window is clean,
#               D's bond is returned after unbonding, and D still follows.
#   3. re-add — D registers again (its approval persisted) and the set is four.
#   4. faulty — at n=4 a bad D cannot halt the chain:
#                 (a) D crashed (SIGSTOP): A/B/C seal a full window (quorum 3);
#                 (b) D leads a height and withholds: a round change commits it;
#                 (c) D forges a block: every honest node refuses it (non-200);
#                 (d) D is restarted with a cleared vote journal: honest nodes
#                     drop any conflicting vote and no QC double-counts.
#
# The leader schedule is ACTIVE[(height+round) % n] with ACTIVE in registration
# order, so the leader of every height is computable and the withholding case
# can be aimed at D exactly.
#
# FORK_CONTROL=1 runs the pre-fork control on a chain WITHOUT bft_rounds_v1: it
# documents that n=3 has no fault tolerance (2/3+1 of three is three) and shows
# what the single-vote path does when a leader withholds at n=4. That is
# documentation of a limitation, not a failure of the lab.
#
# Everything lives under a scratch directory (default /tmp/sqc-fault-lab/<run>)
# on loopback ports, on a chain id generated per run. It never contacts devnet,
# testnet, mainnet or any deployed service, and never reads genesis-production.
#
# Usage:
#   scripts/validator-join/run-fault-lab.sh
#   FORK_CONTROL=1 scripts/validator-join/run-fault-lab.sh
#   KEEP=1 LAB=/tmp/sqc-fault-lab/dbg scripts/validator-join/run-fault-lab.sh
#
# Env:
#   LAB            scratch dir       (default /tmp/sqc-fault-lab/<UTC timestamp>)
#   PORT_BASE      first RPC port to scan from            (default 22600)
#   BLOCK_INTERVAL seconds per block                      (default 2)
#   OBSERVE_BLOCKS heights sampled per clean window       (default 8)
#   FORK_CONTROL=1 run the pre-fork control instead of the fault suite
#   KEEP=1         leave the nodes running on exit
#   LAB_LOG_LEVEL  node log level                         (default info)

set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$REPO/scripts/validator-join/lib.sh"

FORK_CONTROL=${FORK_CONTROL:-}
LAB=${LAB:-/tmp/sqc-fault-lab/$(date -u +%Y%m%dT%H%M%SZ)${FORK_CONTROL:+-forkctl}}
BIN=$LAB/bin
PORT_BASE=${PORT_BASE:-22600}
BLOCK_INTERVAL=${BLOCK_INTERVAL:-2}
OBSERVE_BLOCKS=${OBSERVE_BLOCKS:-8}
WALLET_PASS=${WALLET_PASS:-labpass}
TREASURY=A

# RoundTimeout defaults to 3s in the engine and proposalWait = BLOCK_INTERVAL,
# so a withholding remote leader is rotated away after ~RoundTimeout+interval
# (~5s) and a commit after a round change lands inside ~2*(5)+interval.
RECOVER_BUDGET=$((2 * (3 + BLOCK_INTERVAL) + BLOCK_INTERVAL + 6)) # seconds

PORT_BASE=$(pick_port_base "$PORT_BASE" 4) ||
	{ echo "no free run of 4 ports from $PORT_BASE" >&2; exit 1; }
PORT_A=$PORT_BASE PORT_B=$((PORT_BASE + 1)) PORT_C=$((PORT_BASE + 2)) PORT_D=$((PORT_BASE + 3))
URL_A=http://127.0.0.1:$PORT_A
URL_B=http://127.0.0.1:$PORT_B
URL_C=http://127.0.0.1:$PORT_C
URL_D=http://127.0.0.1:$PORT_D
# node_url NAME -> its RPC URL. A function (not an associative array) so this
# runs under the macOS system bash (3.2). lib.sh forge and the helpers call it.
node_url() { case "$1" in A) echo "$URL_A";; B) echo "$URL_B";; C) echo "$URL_C";; D) echo "$URL_D";; *) return 1;; esac; }
# addr_of NAME -> its SQ: address, from the per-name ADDR_<NAME> variable.
addr_of() { local v="ADDR_$1"; printf '%s' "${!v}"; }

CHAIN_ID=${CHAIN_ID:-splendor-quantum-faultlab-$(date +%Y%m%d%H%M%S)}
TOKEN=$(head -c 24 /dev/urandom | xxd -p | tr -d '\n')

trap 'code=$?; if [[ -z "${KEEP:-}" ]]; then lab_teardown; fi; exit $code' EXIT

RESULTS="$LAB/logs/RESULTS.txt"
pass() { ok "$1"; printf 'PASS  %s\n' "$1" >>"$RESULTS"; }
fail() { bad "$1"; printf 'FAIL  %s\n' "$1" >>"$RESULTS"; die "$1"; }
note() { say "$1"; printf 'NOTE  %s\n' "$1" >>"$RESULTS"; }

honest_logs() { # honest_logs NAME... -> the log paths for those nodes
	local n; for n in "$@"; do printf '%s\n' "$LAB/logs/$n.log"; done
}

# clean_window LABEL FROM TO MINPROD MINVOTES NODE... — sample the window with
# observe.py (agreement, contiguity, qc_root, >= MINVOTES distinct votes bound
# to the block's own hash/height, >= MINPROD distinct producers) and record it.
clean_window() {
	local label="$1" from="$2" to="$3" minp="$4" minv="$5"; shift 5
	local urls=() n
	for n in "$@"; do urls+=("$(node_url $n)"); done
	say "observe $label: heights $from..$to over [$*] (min-producers $minp, min-votes $minv)"
	# The fault lab runs a set that reaches four (> the quorum of three), so
	# honest nodes may certify the same block with different valid vote subsets:
	# hash/state_root/producer must agree, qc_root need not (see observe.py).
	if python3 "$REPO/scripts/validator-join/observe.py" \
		--from "$from" --to "$to" --min-producers "$minp" --min-votes "$minv" \
		--allow-qc-root-divergence \
		--json-out "$LAB/logs/$label.json" "${urls[@]}" >"$LAB/logs/$label.txt" 2>&1; then
		ok "window $label held ($from..$to)"
		return 0
	fi
	tail -20 "$LAB/logs/$label.txt" >&2
	return 1
}

# await_climb DELTA TIMEOUT NODE... — every listed node must gain DELTA blocks
# from its own current height within TIMEOUT. Returns the lowest observed base.
AWAIT_BASE=0
await_climb() {
	local delta="$1" timeout="$2"; shift 2
	local n base target lo=99999999
	for n in "$@"; do
		base=$(status_field "$(node_url $n)" height) || { bad "$n unreachable"; return 1; }
		((base < lo)) && lo=$base
	done
	AWAIT_BASE=$lo
	target=$((lo + delta))
	for n in "$@"; do
		wait_for "$timeout" "$n climbed to $target" height_at_least "$(node_url $n)" "$target" || return 1
	done
	return 0
}

# ── binaries + wallets + genesis ─────────────────────────────────────────────

step "build the binaries and prepare a scratch chain"
rm -rf "$LAB"
mkdir -p "$LAB"/{bin,data,logs,run,keys}
: >"$RESULTS"
printf '%s' "$WALLET_PASS" >"$LAB/run/wallet.pass"; chmod 600 "$LAB/run/wallet.pass"

(cd "$REPO" && go build -o "$BIN/sqc-node" ./cmd/node) || die "build sqc-node"
(cd "$REPO" && go build -o "$BIN/sqc-wallet" ./cmd/wallet) || die "build sqc-wallet"
(cd "$REPO" && go build -o "$BIN/sqc-modcall" ./cmd/sqc-modcall) || die "build sqc-modcall"
ok "binaries in $BIN"
say "chain id: $CHAIN_ID   ports: $PORT_A/$PORT_B/$PORT_C/$PORT_D   mode: ${FORK_CONTROL:+PRE-FORK CONTROL}${FORK_CONTROL:-fault suite}"

for name in A B C D; do wallet_new "$name"; done
for name in A B C D; do printf -v "ADDR_$name" '%s' "$(wallet_addr "$name")"; done
PK_A=$(wallet_pubkey A)
[[ ${#PK_A} -eq 3904 ]] || die "validator A pubkey is ${#PK_A} hex chars, want 3904"
for name in A B C D; do ok "$name=$(addr_of $name)"; done

# The two-phase fork on top of devnet's rules. FORK_CONTROL drops bft_rounds_v1
# so the same chain runs the single-vote path — the pre-fork control.
BFT_FORK='"bft_rounds_v1": 1,'
[[ -n "$FORK_CONTROL" ]] && BFT_FORK=''
cat >"$LAB/genesis.json" <<EOF
{
  "chain_id": "$CHAIN_ID",
  "network_name": "Splendor Quantum Chain — four-validator fault lab",
  "timestamp": 1741545600,
  "validator_pubkey_hex": "$PK_A",
  "allocations": [
    { "address": "$(addr_of A)", "balance": "26000000000000000000000000000", "comment": "lab treasury and genesis validator" }
  ],
  "total_supply": "26000000000000000000000000000",
  "forks": {
    "module_revert_v1": 1, "qrc20_v2": 1, "qrc1155_v1": 1, "exchange_v1": 1,
    "bridge_v1": 1, "verify_v1": 1, "validators_v1": 1, "lending_v1": 1,
    "oracle_v1": 1, "multisig_v1": 1, "governance_v1": 1, "qrc721_v1": 1,
    "qrc4626_v1": 1, "min_fee_v1": 1, "multi_validator": 1,
    "modules_v2": 1, "validators_admission_v1": 1, $BFT_FORK
    "verify_identity_v1": 1
  }
}
EOF
DOC_HASH=$("$BIN/sqc-node" genesis-hash "$LAB/genesis.json")
ok "genesis document hash $DOC_HASH"
GENESIS_FLAGS=(--genesis "$LAB/genesis.json" --genesis-doc-hash "$DOC_HASH")

# start_validator NAME PORT [peer NAMEs...] [-- extra sqc-node flags...] — start
# a --validator with the join flag set (trusts A's genesis key, peers the given
# nodes). A is started separately as the genesis minter. --allow-plaintext-p2p
# registers the cleartext POST /block route so the forged-block prober reaches
# the real producer gate rather than a transport 410.
start_validator() {
	local name="$1" port="$2"; shift 2
	local peers=() extra=()
	while (($#)); do
		if [[ "$1" == "--" ]]; then shift; extra=("$@"); break; fi
		peers+=(--peer "$(node_url $1)"); shift
	done
	node_start "$name" "$port" --validator \
		--wallet "$LAB/keys/$name.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
		--allow-plaintext-p2p \
		"${GENESIS_FLAGS[@]}" \
		--genesis-hash "$GENESIS_HASH" --validator-pubkey-hex "$PK_A" \
		${peers[@]+"${peers[@]}"} ${extra[@]+"${extra[@]}"}
}

# join_and_activate NAME PORT peer NAMEs... — fund, launch, catch up, approve,
# register, and wait the set to widen everywhere. Appends NAME to ACTIVE.
join_and_activate() {
	local name="$1" port="$2"; shift 2
	local want=$((${#ACTIVE[@]} + 1))
	step "add $name: fund, launch against the running chain, approve, register"
	fund "$(addr_of $name)" 6000 "$URL_A" || fail "$name: funding failed"
	wait_for 60 "$name funded" funded "$(addr_of $name)" "$URL_A" || fail "$name never funded"
	start_validator "$name" "$port" "$@"
	wait_for 60 "$name serving /status" serving "$(node_url $name)" || fail "$name never came up"
	wait_for 150 "$name caught up to the tip" tracks_tip "$(node_url $name)" "$URL_A" 2 ||
		fail "$name never reached the tip"
	# validators_admission_v1: an active validator (A) must approve the joiner
	# before it may register. The producer clause enrols A itself; everyone else
	# is approved.
	approve A "$(addr_of $name)" "$URL_A" || fail "$name: approve failed"
	# Submitted to the joiner itself: it is not in the set, so it never leads a
	# height and can only get the tx included by relaying it one hop to a peer.
	register "$name" "$(node_url $name)" || fail "$name: register submit failed"
	local n
	for n in "${ACTIVE[@]}" "$name"; do
		wait_for 120 "$(node_url $n) sees $want validators" set_size_is "$(node_url $n)" "$want" ||
			fail "$name did not activate on $n"
	done
	ACTIVE+=("$name")
	pass "add $name — active set is now ${want} (${ACTIVE[*]})"
}

# ── step 1: genesis producer A, then add B, C, D ─────────────────────────────

step "start the genesis producer A and prove it advances"
node_start A "$PORT_A" --validator \
	--wallet "$LAB/keys/A.sqwallet" --wallet-pass-file "$LAB/run/wallet.pass" \
	--allow-plaintext-p2p "${GENESIS_FLAGS[@]}"
wait_for 60 "A serving /status on $CHAIN_ID" serving "$URL_A" || die "A never came up"
wait_for 90 "A reached height 3 by itself" height_at_least "$URL_A" 3 || die "A is not producing"
PID_A=$(node_pid A)
GENESIS_HASH=$(hex32 "$URL_A/block/0" "d['hash']") || die "cannot read A's block 0"
ok "A pid=$PID_A genesis block hash=$GENESIS_HASH"

step "register the genesis producer A (producer clause, single-key quorum)"
register A "$URL_A" || die "A register failed"
wait_for 60 "registry lists 1 active validator" set_size_is "$URL_A" 1 || die "A did not register"
ACTIVE=(A)
H_SOLO=$(status_field "$URL_A" height)
wait_for 60 "A kept producing after registering" height_at_least "$URL_A" $((H_SOLO + 2))
pass "add A — genesis producer self-registered, chain live at n=1"

join_and_activate B "$PORT_B" A
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) A B || fail "n=2 stalled"
clean_window s1-add-n2 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 2 2 A B ||
	fail "n=2 window did not hold"
no_refusals $(honest_logs A B) || fail "refusal in an honest log at n=2"

join_and_activate C "$PORT_C" A B
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) A B C || fail "n=3 stalled"
clean_window s1-add-n3 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 3 3 A B C ||
	fail "n=3 window did not hold"

join_and_activate D "$PORT_D" A B C
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 5 + 90)) A B C D || fail "n=4 stalled"
clean_window s1-add-n4 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 4 3 A B C D ||
	fail "n=4 window did not hold"
no_refusals $(honest_logs A B C D) || fail "refusal in an honest log at n=4"
pass "scenario 1 — add B, C, D: set widened 1->2->3->4, every window clean"

# ── the pre-fork control forks off here ──────────────────────────────────────
if [[ -n "$FORK_CONTROL" ]]; then
	source "$REPO/scripts/validator-join/fault-lab-forkctl.inc"
	fork_control_body
	control_summary   # the control leaves the chain deliberately wedged; no strict final_report
	exit 0
fi

# ── scenario 2: remove D ─────────────────────────────────────────────────────

step "scenario 2 — remove D: unregister, unbond, finalize, prove D still follows"
BAL_D_BEFORE=$(balance_units "$(addr_of D)" "$URL_A")
unregister D "$URL_D" || fail "D: unregister failed"
for n in A B C; do
	wait_for 90 "$(node_url $n) sees 3 validators" set_size_is "$(node_url $n)" 3 ||
		fail "D did not leave the set on $n"
done
ACTIVE=(A B C)
H_UNREG=$(status_field "$URL_A" height)
# D's record is Exiting until UnbondingBlocks (32) pass; read its exit height
# from the registry, falling back to unregister-height + 32 if the read shape
# ever changes.
EXIT_HEIGHT=$("$BIN/sqc-modcall" read --node "$URL_A" --module validators --action validator \
	--params "{\"address\":\"$(addr_of D)\"}" 2>/dev/null | python3 -c \
	'import sys,json
try:
    d=json.load(sys.stdin)
    print(int(d.get("exit_height") or d.get("ExitHeight") or 0))
except Exception:
    print(0)' 2>/dev/null || echo 0)
((EXIT_HEIGHT > 0)) || EXIT_HEIGHT=$((H_UNREG + 33))
say "D exit height (bond returnable at) = $EXIT_HEIGHT (unregistered near height $H_UNREG)"

await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) A B C || fail "n=3 (post-remove) stalled"
clean_window s2-remove-n3 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 3 3 A B C ||
	fail "n=3 post-remove window did not hold"
no_refusals $(honest_logs A B C) || fail "refusal in an honest log after removing D"

# D keeps running as a follower — it must still track the tip while out of the set.
wait_for 60 "D still follows the chain as a non-validator" tracks_tip "$URL_D" "$URL_A" 3 ||
	fail "D stopped following after it left the set"

step "wait past D's unbonding height, then finalize the exit"
if ((EXIT_HEIGHT > 0)); then
	wait_for $(((32 + OBSERVE_BLOCKS) * BLOCK_INTERVAL + 60)) "chain reached D's exit height $EXIT_HEIGHT" \
		height_at_least "$URL_A" "$EXIT_HEIGHT" || fail "chain never reached D's exit height"
fi
finalize_exit D "$URL_D" || fail "D: finalize_exit failed"
wait_for 60 "D's bond returned" bond_returned "$(addr_of D)" "$URL_A" "$BAL_D_BEFORE" ||
	fail "D's bond was not returned"
BAL_D_AFTER=$(balance_units "$(addr_of D)" "$URL_A")
pass "scenario 2 — remove D: set fell to 3, clean n=3 window, bond returned ($BAL_D_BEFORE -> $BAL_D_AFTER), D still follows"

# ── scenario 3: re-add D ─────────────────────────────────────────────────────

step "scenario 3 — re-add D: register again (approval persisted), set back to 4"
# No fresh approve: an approval stands until Revoke, so an exited-and-returning
# validator re-registers on the approval it already holds.
register D "$URL_D" || fail "D: re-register submit failed"
for n in A B C D; do
	wait_for 120 "$(node_url $n) sees 4 validators again" set_size_is "$(node_url $n)" 4 ||
		fail "D did not re-activate on $n"
done
ACTIVE=(A B C D)
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 5 + 90)) A B C D || fail "n=4 (re-add) stalled"
clean_window s3-readd-n4 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 4 3 A B C D ||
	fail "n=4 re-add window did not hold"
no_refusals $(honest_logs A B C D) || fail "refusal in an honest log after re-adding D"
pass "scenario 3 — re-add D: approval persisted, set back to 4, clean window"

# ── scenario 4: a faulty D cannot halt the chain at n=4 ──────────────────────

# 4(a) — D crashed (SIGSTOP). The honest three are exactly the quorum (3 of 4).
step "scenario 4(a) — D crashed: A/B/C seal a full window under quorum 3"
H4A_BEFORE=$(status_field "$URL_A" height)
node_pause D
# One in four heights is D's turn to lead; each incurs one round change (~5s),
# so give the window generous time.
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 3 + RECOVER_BUDGET * 3 + 60)) A B C ||
	fail "4(a): chain stalled with D down — quorum 3 should carry it"
clean_window s4a-Ddown-n4 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 3 3 A B C ||
	fail "4(a): window with D down did not hold"
no_refusals $(honest_logs A B C) || fail "4(a): refusal in an honest log with D down"
node_resume D
wait_for 120 "D caught up after resuming" tracks_tip "$URL_D" "$URL_A" 3 ||
	fail "4(a): D never caught up after resume"
H4A_AFTER=$(status_field "$URL_A" height)
pass "scenario 4(a) — D crashed: chain climbed $H4A_BEFORE->$H4A_AFTER on quorum 3, D re-synced"

# 4(b) — D leads a height and withholds. Aim at a height D leads, pause D just
# before it, expect a round change to commit it, then resume.
step "scenario 4(b) — D leads and withholds: a round change must commit the height"
TIP=$(status_field "$URL_A" height)
# Pause D now: the heights before H0 are led by A/B/C and commit on quorum 3
# with D a paused voter; H0 is the first height D leads, and there D withholds.
node_pause D
H0=$(next_leader_height "$TIP" D) || fail "4(b): could not find a height D leads"
say "first height D leads at round 0 after $TIP is $H0 (leader_at $H0 = $(leader_at "$H0"), round-1 leader $(leader_at "$H0" 1))"
# The chain climbs to H0-1 on quorum 3, then holds at H0-1 until the honest
# three round-change past the withholding leader.
wait_for $(((H0 - TIP + 2) * BLOCK_INTERVAL + 30)) "chain reached H0-1=$((H0 - 1))" \
	height_at_least "$URL_A" $((H0 - 1)) || fail "4(b): chain did not reach H0-1"
t0=$SECONDS
wait_for "$((RECOVER_BUDGET + 20))" "H0=$H0 committed despite D withholding" \
	height_at_least "$URL_A" "$H0" || fail "4(b): H0 never committed — a withholding leader stalled the chain"
recover_s=$((SECONDS - t0))
# Prove it took a round change: the committed H0 must be led by the round-1
# leader (not D), and its votes carry round >= 1. The window is only three
# heights with D down, so producer diversity is not the point here (min-producers
# 1); agreement + certification + the round check below are.
clean_window s4b-withhold-n4 "$H0" $((H0 + 2)) 1 3 A B C || fail "4(b): post-withhold window did not hold"
H0_ROUND=$(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
b=[x for x in d["blocks"] if x["height"]==int(sys.argv[2])][0]
print(max([v["round"] for v in b["votes"]] or [0]))' "$LAB/logs/s4b-withhold-n4.json" "$H0")
H0_PROD=$(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
b=[x for x in d["blocks"] if x["height"]==int(sys.argv[2])][0]
print(b["producer"])' "$LAB/logs/s4b-withhold-n4.json" "$H0")
node_resume D
wait_for 120 "D caught up after the withheld height" tracks_tip "$URL_D" "$URL_A" 3 ||
	fail "4(b): D never caught up after resume"
if ((H0_ROUND >= 1)); then
	pass "scenario 4(b) — D withheld H0=$H0; round change committed it at round $H0_ROUND (producer ${H0_PROD:0:14}…) in ${recover_s}s"
else
	fail "4(b): H0=$H0 committed at round $H0_ROUND — expected a round change (>=1) after D withheld"
fi

# 4(c) — D forges blocks and pushes them at honest peers. Each must be refused.
step "scenario 4(c) — D forges blocks at A and B: every honest node must refuse"
H4C_BEFORE=$(status_field "$URL_A" height)
forge_refused() { # forge_refused TARGET_NAME
	local tgt="$1" out rc
	# cmdForge exits 0 on refusal, non-zero only if the target accepted the
	# forged block. The `if` guards set -e so a non-zero exit is captured, not
	# fatal.
	if out=$(forge D "$(node_url $tgt)"); then rc=0; else rc=$?; fi
	printf '%s\n' "$out" >>"$LAB/logs/forge.log"
	if ((rc != 0)); then
		bad "4(c): $tgt ACCEPTED a forged block (forge exit $rc):"; printf '%s\n' "$out" >&2; return 1
	fi
	# Refused. Confirm the HTTP body cmdForge printed shows a non-200 refusal.
	if grep -qiE 'HTTP (4[0-9][0-9]|5[0-9][0-9])|block rejected|refusing inbound block|certificate rejected|linkage' <<<"$out"; then
		ok "4(c): $tgt refused the forged block (non-200): $(grep -oiE 'HTTP [0-9]+.*' <<<"$out" | head -1)"
		return 0
	fi
	bad "4(c): $tgt did not answer a clear refusal:"; printf '%s\n' "$out" >&2; return 1
}
forge_refused A || fail "4(c): A did not refuse the forge"
forge_refused B || fail "4(c): B did not refuse the forge"
await_climb 3 $((RECOVER_BUDGET + 30)) A B C D || fail "4(c): chain did not keep climbing after the forge"
no_refusals $(honest_logs A B C D) || fail "4(c): a forge left a consensus refusal in an honest log"
H4C_AFTER=$(status_field "$URL_A" height)
pass "scenario 4(c) — D forged at A and B: both refused (non-200), chain climbed $H4C_BEFORE->$H4C_AFTER, honest logs clean"

# 4(d) — D restarted with a cleared vote journal may sign a conflicting body.
# Honest nodes must drop any conflicting vote; no QC may double-count; the chain
# must stay unbroken. The equivocation LOG line is recorded as observed / not.
step "scenario 4(d) — D restarted with a cleared vote journal: no double-count, chain unbroken"
H4D_BEFORE=$(status_field "$URL_A" height)
# Stop D mid-height (it is a member and voting), then restart it with the vote
# journal cleared so its in-process double-sign guard is bypassed.
node_pause D; node_resume D   # ensure not frozen before we kill it
node_stop D
start_validator D "$PORT_D" A B C -- --clear-vote-journal
wait_for 60 "D back up with a cleared journal" serving "$URL_D" || fail "4(d): D did not restart"
wait_for 150 "D caught up after the journal clear" tracks_tip "$URL_D" "$URL_A" 3 ||
	fail "4(d): D never caught up after the clear"
# Watch a full window while D re-participates.
await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + RECOVER_BUDGET * 2 + 90)) A B C ||
	fail "4(d): chain stalled after D's journal clear"
clean_window s4d-clearjournal-n4 $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 3 3 A B C ||
	fail "4(d): window after the journal clear did not hold"
no_refusals $(honest_logs A B C) || fail "4(d): a CRITICAL/refusal appeared after the journal clear"
# Safety: no QC in ANY recorded window may list a validator twice (observe.py
# already enforces distinct voters; this is the belt-and-braces scan).
python3 - "$LAB"/logs/s*.json <<'PY' || fail "4(d): a QC double-counted a validator"
import json,sys
for path in sys.argv[1:]:
    try: d=json.load(open(path))
    except Exception: continue
    for b in d.get("blocks",[]):
        seen=set()
        for v in b["votes"]:
            if v["by"] in seen:
                print("DOUBLE-COUNT",path,b["height"],v["by"]); sys.exit(1)
            seen.add(v["by"])
PY
EQUIV=$(grep -l "equivocation — conflicting votes dropped" $(honest_logs A B C) 2>/dev/null || true)
if [[ -n "$EQUIV" ]]; then
	note "4(d): honest node(s) logged 'equivocation — conflicting votes dropped': $(basename -a $EQUIV | tr '\n' ' ')"
else
	note "4(d): no live 'equivocation' line — the honest quorum of 3 committed each height without D, so the cleared-journal D re-synced forward rather than re-signing a still-open view; the drop path is covered in-process by consensus tests. Safety invariants (no double-count, chain unbroken) held."
fi
H4D_AFTER=$(status_field "$URL_A" height)
pass "scenario 4(d) — journal-clear restart: chain climbed $H4D_BEFORE->$H4D_AFTER, no QC double-count, honest logs clean"

# ── final checks (run-live-join's final_checks, widened to four nodes) ────────

final_report() {
	step "let every alive node converge on the tip before comparing histories"
	local n
	for n in A B C D; do
		node_alive "$n" || continue
		wait_for 90 "$n converged on the tip" tracks_tip "$(node_url $n)" "$URL_A" 2 ||
			fail "$n never converged before the final history check"
	done
	step "the whole history agrees on every honest node, block 1 to the tip"
	local head nodes=()
	for n in A B C D; do node_alive "$n" && nodes+=("$(node_url $n)"); done
	head=$(status_field "$URL_A" height)
	if python3 "$REPO/scripts/validator-join/observe.py" \
		--from 1 --to $((head - 1)) --min-producers 1 --min-votes 1 \
		--allow-qc-root-divergence \
		--json-out "$LAB/logs/history.json" "${nodes[@]}" >"$LAB/logs/history.txt" 2>&1; then
		pass "history — blocks 1..$((head - 1)) identical on ${#nodes[@]} honest nodes"
	else
		tail -20 "$LAB/logs/history.txt" >&2
		fail "the nodes disagree somewhere in blocks 1..$((head - 1))"
	fi
	step "final invariants"
	[[ "$(node_pid A)" == "$PID_A" ]] || fail "producer A's pid changed: $PID_A -> $(node_pid A)"
	node_alive A || fail "producer A is not running"
	no_refusals "$LAB"/logs/[ABCD].log || fail "a consensus refusal is present in an honest log"
	pass "final — A ran unbroken as pid $PID_A; no consensus refusals in any honest log"
	printf '\n\033[1;32mPASS\033[0m — four-validator fault lab held on %s\n' "$CHAIN_ID" >&2
	printf '       results: %s   windows: %s/logs/s*.json\n' "$RESULTS" "$LAB" >&2
	cat "$RESULTS" >&2
}

final_report
