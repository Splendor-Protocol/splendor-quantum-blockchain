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

A new node joins without the producer stopping. Every joiner needs the
genesis validator's key (`--validator-pubkey-hex`, or `--genesis-hash` to
learn it from block 0), the chain's genesis document (`--genesis` plus
`--genesis-doc-hash`), and the cluster's `--rpc-auth-token`; without the
token the producer never accepts its announce and never pushes proposals or
votes to it.

1. Start the joiner as a follower with a fresh `--data-dir` and `--peer` at
   the producer. It pull-syncs from block 1; blocks sealed by validators other
   than the genesis one are authenticated against the active validator set.
   On a chain whose history cannot be replayed (a QC-less block sealed while
   two or more validators were active), add `--state-sync`: the node takes the
   fork schedule, chain id and minted supply from the genesis document,
   imports the producer's live state snapshot at its head — the head block is
   authenticated against the genesis validator key — and follows from there.
2. When its height tracks the producer's, restart it on the same data dir
   with `--validator --wallet <joiner.sqwallet>`, keeping
   `--validator-pubkey-hex <genesis key>`. It runs BFT, votes on the
   producer's proposals, and does not propose until it is synced.
3. Fund the joiner (2,500 SPLD bond plus fee) and register both keys: the
   producer's first, if it never registered itself, then the joiner's. From
   the next block the set has two members, the leader alternates
   `(height+round) % 2`, and every block carries two votes and a `qc_root`.

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
