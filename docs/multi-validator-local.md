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
