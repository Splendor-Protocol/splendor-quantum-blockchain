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

## Files

- `run-live-join.sh` — the pull-sync scenario.
- `run-state-sync-join.sh` — the snapshot scenario.
- `lib.sh` — process, wallet, polling and chain-action helpers.
- `observe.py` — the window checker. Takes a height range and a list of node
  URLs, and asserts agreement, certificates, vote/height binding, parent
  linkage and a minimum number of distinct producers.

## The fast equivalent

`go test ./consensus -run 'TestSecondValidatorJoinsARunningProducer|TestThirdValidatorJoinsAndWidensTheQuorum'`
runs the same scenario in-process, over a link that reorders and delays every
message, in a few seconds and under `-race`. Use it in the edit loop; use the
scripts to prove it against real processes and the real network stack.
