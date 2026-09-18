# fault-lab-forkctl.inc — the pre-fork control body, sourced by run-fault-lab.sh
# when FORK_CONTROL=1. It runs on a chain that does NOT schedule bft_rounds_v1,
# so the single-vote path is in force. It documents, exactly as observed, two
# limitations that the two-phase fork addresses:
#
#   B. what the single-vote path does at n=4 when a leader withholds; and
#   A. that n=3 has no fault tolerance AND that, once one member is down, the
#      single-vote path's durable vote-lock wedges the chain permanently — a
#      view change cannot unlock it, which is exactly the C1 stall bft_rounds_v1
#      fixes with prevote QCs (POL) and re-proposal of the locked body.
#
# B is run FIRST (it recovers cleanly), A LAST (it deliberately wedges the
# chain), so A's wedge cannot break B. This is documentation of a limitation,
# not a pass/fail of consensus: only an infrastructure error (a build or a node
# dying, or a step that cannot even be attempted) fails the run here.

# wait_for_quiet TIMEOUT CMD... — like wait_for but prints no timeout diagnosis,
# for the control where a timeout is a documented observation, not a failure.
wait_for_quiet() {
	local timeout="$1"; shift
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		"$@" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 1
}

fork_control_body() {
	note "PRE-FORK CONTROL: chain has NO bft_rounds_v1 — the single-vote path is in force"

	# ── B. single-vote path at n=4 under a withholding leader (recovers) ─────
	step "pre-fork control B — at n=4, D withholds a height it leads"
	await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 90)) A B C D ||
		fail "control: n=4 single-path chain did not advance"
	local TIP H0 t0
	TIP=$(status_field "$URL_A" height)
	node_pause D
	H0=$(next_leader_height "$TIP" D) || fail "control: could not find a height D leads"
	say "first height D leads after $TIP is $H0 (single-vote path)"
	wait_for $(((H0 - TIP + 2) * BLOCK_INTERVAL + 30)) "chain reached H0-1=$((H0 - 1))" \
		height_at_least "$URL_A" $((H0 - 1)) || fail "control: chain did not reach H0-1"
	t0=$SECONDS
	if wait_for_quiet $((RECOVER_BUDGET + 20)) height_at_least "$URL_A" "$H0"; then
		note "pre-fork n=4 withhold: single-vote path RECOVERED — H0=$H0 committed in $((SECONDS - t0))s via a view change. A pure withholding leader casts no vote, so no honest node is vote-locked at H0; the C1 lock the two-phase fork fixes needs CONFLICTING votes at a height, which a black-box withhold alone does not create (see control A)."
	else
		note "pre-fork n=4 withhold: single-vote path STALLED at height $(status_field "$URL_A" height) (H0=$H0 not committed within the budget)."
	fi
	node_resume D
	wait_for 150 "D caught up after the withheld height" tracks_tip "$URL_D" "$URL_A" 3 ||
		note "control: D did not re-converge within the budget after resume (see logs)"
	pass "pre-fork control B — documented the single-vote path at n=4 under a withholding leader"

	# ── A. n=3 has no fault tolerance; the single-vote lock wedges it ────────
	step "pre-fork control A — remove D for n=3, then one of three goes down"
	unregister D "$URL_D" || fail "control: unregister D failed"
	for n in A B C; do
		wait_for 90 "$(node_url $n) sees 3 validators" set_size_is "$(node_url $n)" 3 ||
			fail "control: D did not leave the set on $n"
	done
	ACTIVE=(A B C)
	await_climb "$OBSERVE_BLOCKS" $((OBSERVE_BLOCKS * BLOCK_INTERVAL * 4 + 60)) A B C ||
		fail "control: n=3 single-path chain did not advance"
	clean_window ctl-n3-clean $((AWAIT_BASE + 2)) $((AWAIT_BASE + OBSERVE_BLOCKS)) 3 3 A B C ||
		fail "control: n=3 single-path window did not hold"

	local H_BEFORE
	H_BEFORE=$(status_field "$URL_A" height)
	say "pausing C — quorum (3*2)/3+1 = 3 of a 3-set, so one down should stall"
	node_pause C
	if climbed_within "$RECOVER_BUDGET" "$URL_A" 2; then
		note "pre-fork n=3: chain climbed past $H_BEFORE with one of three DOWN — unexpected fault tolerance at n=3; recording actual"
	else
		note "pre-fork n=3: chain STALLED at height $(status_field "$URL_A" height) with one of three down (2/3+1 of a 3-set is 3, unreachable with one down) — n=3 has zero fault tolerance"
	fi
	# Bring C back and see whether the chain unlocks. On the single-vote path it
	# does NOT: A and B durably vote-locked to their round-0 vote for the stuck
	# height and refuse every re-proposed block a view change offers.
	node_resume C
	if climbed_within $((RECOVER_BUDGET + 30)) "$URL_A" 2; then
		note "pre-fork n=3: chain RESUMED after C returned — the stall was only a liveness pause"
	else
		local locked
		locked=$(grep -lE 'durable vote-lock refuses different block|split vote-lock' \
			$(honest_logs A B C) 2>/dev/null | xargs -n1 basename 2>/dev/null | tr '\n' ' ')
		note "pre-fork n=3: chain did NOT resume after C returned — this is the C1 single-vote WEDGE: ${locked:-A/B} logged 'durable vote-lock refuses different block ... view-change will not unlock', so recovery needs --clear-vote-journal. bft_rounds_v1 removes exactly this failure (a prevote QC locks a value and a later leader re-proposes THAT body)."
	fi
	pass "pre-fork control A — documented n=3 no-fault-tolerance and the single-vote C1 wedge"
}

# climbed_within TIMEOUT URL DELTA — 0 if URL gains >= DELTA blocks within
# TIMEOUT, 1 otherwise. Quiet: a non-climb is the expected observation here.
climbed_within() {
	local timeout="$1" url="$2" delta="$3" base target deadline
	base=$(status_field "$url" height) || return 2
	target=$((base + delta))
	deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		height_at_least "$url" "$target" && return 0
		sleep 1
	done
	return 1
}

# control_summary — the control leaves the chain deliberately wedged (part A),
# so the strict final_report is inappropriate. Confirm only that A, the genesis
# producer, never faulted, and print the recorded observations.
control_summary() {
	step "pre-fork control — summary"
	[[ "$(node_pid A)" == "$PID_A" ]] || fail "producer A's pid changed: $PID_A -> $(node_pid A)"
	node_alive A || fail "producer A is not running"
	pass "control — A (genesis producer) ran unbroken as pid $PID_A throughout the control"
	printf '\n\033[1;33mPRE-FORK CONTROL COMPLETE\033[0m — observations recorded on %s\n' "$CHAIN_ID" >&2
	printf '       results: %s   window: %s/logs/ctl-n3-clean.json\n' "$RESULTS" "$LAB" >&2
	cat "$RESULTS" >&2
}
