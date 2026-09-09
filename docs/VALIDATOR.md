# Validator lifecycle

> **Public admission has not launched.**
> Do not register on live mainnet.
> This page documents the current CLI and the 2,500 SPLD registry.
> Rehearse only on a scratch chain. Do not treat this as an invitation
> to join production.

Permissionless registration, once admission is open: one bond, no tiers.
Anyone who can fund **2,500 SPLD** can `register`. The bond locks existing
native supply. It does not mint SPLD. After `unregister`, the bond returns
at `finalize_exit` once **32 blocks** have passed.

Authoritative module table: [`modules/validators.md`](modules/validators.md).

## Rehearsal (do this instead of live registration)

The join proofs live in [`scripts/validator-join/`](../scripts/validator-join/).
They start real `sqc-node` processes on a scratch chain and never contact
devnet, mainnet, or `genesis-production.json`. Run those scripts; do not
edit them from this track.

```sh
scripts/validator-join/run-live-join.sh
scripts/validator-join/run-state-sync-join.sh
```

See that directory's README for environment variables and what each script
proves.

## Wallet (rehearsal machine)

```bash
umask 077
install -m 600 /dev/null /var/sqc/wallet.pass
# write the password into wallet.pass yourself; do not put it on the command line
./sqc-wallet generate --out /var/sqc/validator.sqwallet --pass-file /var/sqc/wallet.pass
./sqc-wallet address --wallet /var/sqc/validator.sqwallet
```

`--pass-file` must not be readable by other users (`chmod 600`). The node
and `sqc-modcall` refuse a world-readable secret file.

## Writes

All three actions are `TxCall` through the module router (`validators_v1`).
Operator automation should wait for a successful receipt:

```bash
NODE=http://127.0.0.1:8080
CHAIN_ID="$SQC_CHAIN_ID"   # published or lab chain id; do not invent one
WALLET=/var/sqc/validator.sqwallet
PASS=/var/sqc/wallet.pass

# register — locks 2,500 SPLD and records this wallet's ML-DSA-65 pubkey
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action register \
  --params '{"pubkey":"{{PUBKEY}}"}' \
  --wait --require-success

# unregister — marks exiting; starts the 32-block wait
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action unregister \
  --params '{}' \
  --wait --require-success

# finalize_exit — after 32 blocks, returns the bond
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action finalize_exit \
  --params '{}' \
  --wait --require-success
```

`--wait` alone still exits 0 on a `status: 0` receipt (lab/test default).
`--require-success` prints `receipt-status` (`success` / `execution_failed` /
`pending` / `rejected`) and exits 1 unless execution succeeded.

`register` params: `{ "pubkey": "<hex or base64 ML-DSA-65 public key>" }`.
`{{PUBKEY}}` expands to the signing wallet's key.

## Reads

| Route | What |
| --- | --- |
| `GET /validators` | Active set |
| `GET /validators/{addr}` | One record (bond, status, exit height) |
| module read `bond` | The 2,500 SPLD figure in smallest units |

## Exiting-validator re-register (not this track)

`RegisterV2` (fork-gated by `modules_v2`) is the corrected re-register rule
for a validator that already exited. That change is owned by the modules/vm
track. Do not expect it from this document, and do not edit `vm/**` to get
it. Until that fork is active on a given network, treat a second `register`
from an exiting key as the pre-`modules_v2` rule.

## Do not

- Register on live mainnet while public admission is closed.
- Copy hardware minimums, APY, or unpublished bootnode pins from elsewhere.
- Use [`FEAT_VALIDATOR.md`](FEAT_VALIDATOR.md), [`VALIDATOR_FAQ.md`](VALIDATOR_FAQ.md),
  or [`modules/staking.md`](modules/staking.md) as the live model — those are
  the archived pass/tier lab.
