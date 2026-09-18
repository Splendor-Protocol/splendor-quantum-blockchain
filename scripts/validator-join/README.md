# Validator-join lab

Two runnable proofs that a validator can join a chain that is already
running, without the producer stopping. Both start real `sqc-node` processes
on a scratch chain in a temporary directory, on loopback ports chosen at run
time. Neither ever contacts devnet, mainnet or any deployed service, and
neither reads `genesis-production.json`.

```sh
scripts/validator-join/run-live-join.sh          # A, then B, then C — pull-sync join
scripts/validator-join/run-state-sync-join.sh    # A, then D — snapshot join
```

Both exit non-zero with a diagnosis on the first property that fails, and
leave everything under `$LAB` (`/tmp/sqc-join-lab`, `/tmp/sqc-statesync-lab`):
node logs, the genesis document, the wallets, and the sampled block windows as
JSON.

## What `run-live-join.sh` proves

1. One genesis producer A starts on a fresh chain and advances on its own.
2. A registers itself; the chain keeps producing under a one-key quorum.
3. B is funded by a signed transfer **from the running chain**, then launched
   **once** as `--validator` against A. It is not in the genesis document, not
   in its allocations, and not in the registry.
4. B pull-syncs to the tip while A keeps producing, then registers with a
   signed `validators.register` **submitted to B itself** — B is not in the
   set yet, so it never leads a height and can only get that transaction
   included by relaying it to its peers.
5. For a sampled window: two DISTINCT addresses seal blocks, every block
   carries a `qc_root` and two distinct votes for its own hash and height, and
   both nodes return the same hash, state root, producer and certificate at
   every height.
6. The same again for C, with a three-member quorum.
7. Blocks 1..tip are compared across every node — the check that catches two
   honest nodes storing different certificates for the same block.
8. A's pid is the same at the end as at the start, and no node logged
   `proposal from non-leader rejected`, `block certificate rejected`,
   `commit callback failed` or `refusing inbound block`.

"Several nodes are connected" would pass none of steps 5 to 7.

## What `run-state-sync-join.sh` proves

The same shape for a chain the joiner does not replay: D is launched **once**
as `--validator --state-sync`, imports A's live state snapshot, is asserted to
hold **no block below the snapshot**, registers from its own RPC, and then
shares the leadership. That flag combination used to be refused at start-up.

## What `run-fault-lab.sh` proves

A **four-validator** lab on a chain that schedules the two-phase BFT fork
(`bft_rounds_v1`) on top of devnet's rules (`modules_v2`,
`validators_admission_v1`). Four validators A, B, C, D are brought up in a known
registration order, so the leader of every height is computable as
`ACTIVE[(height+round) % n]` and a faulty member can be aimed at exactly.

```sh
scripts/validator-join/run-fault-lab.sh              # the fault suite
FORK_CONTROL=1 scripts/validator-join/run-fault-lab.sh   # the pre-fork control
```

1. **Add** — A self-registers (the admission producer clause); then B, C and D
   each fund from the running chain, launch **once** as `--validator` against
   it, catch up, are **approved** by A (`validators_admission_v1`), register
   from their own RPC, and the set widens 1→2→3→4. Each phase's window is clean.
2. **Remove** — D `unregister`s, the set falls to three, the n=3 window holds,
   the chain runs to D's exit height, `finalize_exit` returns D's bond (asserted
   from the balance), and D keeps following the chain as a non-validator.
3. **Re-add** — D `register`s again on the approval it already holds, the set is
   four, the window holds.
4. **A bad D cannot halt the chain at n=4** (quorum is 3 of 4):
   - **(a) crashed** — D is `SIGSTOP`ped; A/B/C seal a full window on quorum 3;
     D `SIGCONT`s and re-syncs.
   - **(b) withholds while leading** — D is paused just before a height it
     leads; the honest three round-change past it and commit that height at
     round ≥ 1 (verified from the window's votes), inside
     `~2·(RoundTimeout+proposalWait)`.
   - **(c) forges** — D seals a syntactically valid, correctly linked block with
     its own key and pushes it at A and B; both refuse it (HTTP 400,
     `block certificate rejected: multi_validator requires a quorum
     certificate`) and the chain keeps climbing. The refusal is in the HTTP
     response, not the node log, so it never masquerades as a consensus fault.
   - **(d) restarted with a cleared vote journal** — D is stopped mid-height and
     restarted with `--clear-vote-journal`, bypassing its own double-sign guard.
     No QC double-counts a validator and the chain stays unbroken. Whether an
     honest node logs `BFT: equivocation — conflicting votes dropped` is
     recorded: at n=4 the honest quorum of three commits each height without D,
     so a cleared-journal D re-syncs forward rather than re-signing a still-open
     view — the drop path is exercised in-process by the consensus tests.

Every scenario is gated the same way: each honest node's height climbs by
`OBSERVE_BLOCKS`, the sampled window agrees on **hash, state root and producer**
at every height with ≥ quorum distinct votes bound to the block, and no honest
log holds a consensus refusal. At n=4 the **certificate can differ** between
honest nodes — quorum is 3 of 4, so each node finalises the same block with its
own valid 3-vote subset and a different `qc_root`; that is correct BFT
behaviour, not a fork (the certificate is not part of the block hash), so the
n=4 windows are observed with `--allow-qc-root-divergence` (hash/state/producer
must still agree, and every node must still carry its own quorum).

`FORK_CONTROL=1` runs the same add flow on a chain **without** `bft_rounds_v1`
(the single-vote path) and then documents two limitations: n=3 has no fault
tolerance (2/3+1 of three is three, so pausing one of three stalls until it
returns), and what the single-vote path does at n=4 under a withholding leader.
This is documentation of a limitation, not a failure of the lab.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `LAB` | `/tmp/sqc-join-lab` | scratch directory (wiped at the start of each run) |
| `VALIDATORS` | `3` | `2` stops after B |
| `PORT_BASE` | `21545` | first port to scan from; a busy run is skipped |
| `BLOCK_INTERVAL` | `2` | seconds per block |
| `OBSERVE_BLOCKS` | `12` | heights sampled per quorum phase |
| `KEEP` | unset | `KEEP=1` leaves the nodes running for inspection |
| `LAB_LOG_LEVEL` | `info` | node log level |

Ports are probed before use. Other labs run on this machine, and a fixed base
would silently attach the harness to somebody else's node; every `/status`
assertion also checks the chain id for the same reason.

The fault lab honours the same environment variables, plus `FORK_CONTROL=1`.
Its scratch directory defaults to `/tmp/sqc-fault-lab/<UTC timestamp>`,
`PORT_BASE` to `22600`, and `OBSERVE_BLOCKS` to `8`. It records a PASS/FAIL (and
NOTE) line per scenario to `$LAB/logs/RESULTS.txt` and one sampled window JSON
per scenario (`$LAB/logs/s*.json`).

## Files

- `run-live-join.sh` — the pull-sync scenario.
- `run-state-sync-join.sh` — the snapshot scenario.
- `run-fault-lab.sh` — the four-validator add/remove/re-add + faulty-member lab.
- `fault-lab-forkctl.inc` — the pre-fork control body, sourced when
  `FORK_CONTROL=1`.
- `lib.sh` — process, wallet, polling and chain-action helpers (register,
  approve, unregister, finalize_exit, forge, leader schedule, pause/resume,
  no_refusals).
- `observe.py` — the window checker. Takes a height range and a list of node
  URLs, and asserts agreement, certificates, vote/height binding, parent
  linkage and a minimum number of distinct producers. `--allow-qc-root-divergence`
  relaxes only the cross-node `qc_root` equality (for a set larger than the
  quorum), still requiring each node to carry its own certificate.

## The fast equivalent

`go test ./consensus -run 'TestSecondValidatorJoinsARunningProducer|TestThirdValidatorJoinsAndWidensTheQuorum|TestTwoPhaseFourValidatorsOneDownABBEventuallyCommits'`
runs the same scenarios in-process, over a link that reorders and delays every
message, in a few seconds and under `-race`. Use it in the edit loop; use the
scripts to prove it against real processes and the real network stack.
