# Local multi-validator

Do **not** edit `genesis-production.json` (that would change the live genesis
hash). Use `genesis-local.json` or `--allow-fork-extension` on a running node
to schedule `multi_validator`, `validators_v1`, `verify_v1`, and `min_fee_v1`.

1. Start two (or more) `sqc-node` processes with `--validator`, shared
   `--rpc-auth-token`, and `genesis-local.json`.
2. Fund each validator with at least 2,500 SPLD plus 0.001 SPLD fee.
3. Each submits a `validators.register` TxCall with its ML-DSA-65 pubkey.
   Bond is locked; `unregister` + 32 blocks + `finalize_exit` returns it.
4. When two or more keys are active, BFT is live: leader
   `(height+round) % n`, 2/3+1 votes, QCRoot on the block, fees 100% to
   `ValidatorAddr`. A timed-out leader increments the round (view change).
5. `/status` reports `multi_validator`, `validator_set_size`, `local_validator`,
   and the live `min_fee` (congestion 2× / 8× / 32× on top of the floor).

## Joining a running chain

A new validator joins in ONE launch, without the producer stopping and
without a second process on the same data directory. Every joiner needs the
genesis validator's key (`--validator-pubkey-hex`, or `--genesis-hash` to
learn it from block 0), the chain's genesis document (`--genesis` plus
`--genesis-doc-hash`), and the cluster's `--rpc-auth-token`; without the
token the producer never accepts its announce and never pushes proposals or
votes to it.

1. Fund the joiner's address from the running chain: 2,500 SPLD bond plus
   fee.
2. Start it with a fresh `--data-dir`, `--peer` at the producer, and
   `--validator --wallet <joiner.sqwallet>`. Because the genesis document
   names someone else and a peer is configured, the node joins rather than
   minting its own block 0 — it used to exit here, which is why the procedure
   used to say "start a full node, then restart it as a validator". It
   pull-syncs from block 1 (blocks sealed by validators other than the genesis
   one are authenticated against the active validator set), holds production
   until it has a stored head and has caught up, and votes on nothing until it
   is in the registry.

   On a chain whose history cannot be replayed (a QC-less block sealed while
   two or more validators were active), add `--state-sync`: the node takes the
   fork schedule, chain id and minted supply from the genesis document,
   imports the producer's live state snapshot at its head — the head block is
   authenticated against the genesis validator key — and follows from there.
   `--state-sync` and `--validator` combine; it needs at least one `--peer`.
3. When its height tracks the producer's, submit a `validators.register` from
   its own key. It can be submitted to any node in the cluster, including the
   joiner itself: a node relays what it admits to its peers one hop, so the
   transaction reaches whichever validator leads next. Register the producer's
   own key first if it never registered itself.

From the block after the registration the set has two members, the leader
alternates `(height+round) % 2`, and every block carries two votes and a
`qc_root`.

Both flows are exercised end to end, against real processes, by
`scripts/validator-join/run-live-join.sh` and
`scripts/validator-join/run-state-sync-join.sh`; the same scenario runs
in-process in a few seconds under `go test -race ./consensus`.

### What keeps the two nodes in step

- A proposal carries the round it was made in, signed by the proposer (its own
  vote rides with the block). A node whose round timer fired once adopts the
  proposer's round instead of rejecting it as a non-leader — before this, one
  view change on one node was permanent, because nothing ever resynchronised
  the two.
- Advancing to a new height resets the round, so a node that takes blocks by
  push or pull-sync rather than by its own commit does not stay a round ahead.
- A vote that overtakes its proposal is held until the proposal arrives
  instead of being dropped.
- The engine re-reads the validator registry before it sizes a quorum, so the
  set it counts against is the one the executor will judge the certificate
  against — the window a registration lands in used to produce a quorum of one
  where the chain wanted two.
- A commit that fails releases the height instead of locking it: every retry
  carries a fresh timestamp, so a lock left behind refused all of them.
- A node that is not in the active set follows and counts votes but does not
  cast one. An unregistered joiner used to vote, reach a quorum of one, and
  store its own vote as the certificate of the producer's block.

The producer moves onto BFT by itself, in-process. It seals on the single
path while the block it is about to seal predates `multi_validator`, and from
the first block under it — at boot when the activation is already behind its
head, otherwise at the tick that reaches the activation height — it proposes
through BFT: alone it self-commits with one vote and a `qc_root`; once a
second validator is registered it needs that validator's vote, and the
leader alternates. A producer on this binary therefore never stops on
`ErrQuorumRequired`; that error is now only a backstop for a caller driving
`ProduceBlock` directly. The one restart left is the operator's: adopting a
schedule change with `--allow-fork-extension` is a start-up decision. A
producer on an older binary is still single-path for the life of its process
and does stop at the first block that needs a certificate; restart it onto
this binary before registering the second validator.
