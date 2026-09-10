# Validator lifecycle

> **Public admission has not launched.**
> Do not register on live mainnet.
> The chain does not enforce that. `validators_v1` and `multi_validator`
> are active on all three public tiers (observed 2026-09-10: mainnet and
> testnet since height 1, devnet since 74,075), so the router accepts a
> 2,500 SPLD `register` on each today. Closed means no signed release
> pins, no supported join path, and no promise that today's state
> survives a reset. Every tier has been reset before.
> This page documents the current CLI and the 2,500 SPLD registry.
> Rehearse only on a scratch chain. Do not treat this as an invitation
> to join production.

The protocol rule is one bond, no tiers. Under `validators_v1` alone anyone
who can fund **2,500 SPLD** can `register`. Once a tier schedules
`validators_admission_v1`, `register` is accepted only from the block's own
producer (the genesis validator enrolling itself) or from an address an
active validator has `approve`d; everyone else gets a failed receipt and
keeps the bond. See [`modules/validators.md`](modules/validators.md#admission-validators_admission_v1).
The bond locks existing native supply. It does not mint SPLD. After
`unregister`, the bond returns at `finalize_exit` once **32 blocks** have
passed.

Authoritative module table: [`modules/validators.md`](modules/validators.md).

## 1. Sync and verify as an RPC follower first

A validator is a follower that also signs. Complete
[`RPC_NODE.md`](RPC_NODE.md) on this machine first: pinned chain id,
block-0 hash, genesis document hash, peer pin, hash **and** state-root
compare at a shared height, then a restart that resumes. Do not add
`--validator` until that node is following the intended chain.

Starting with `--validator` does not fund, register, or activate anything.

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

`generate` prints the address **and** the seed hex plus 24-word phrase.
It refuses to overwrite an existing wallet. Only `generate` takes
`--pass-file`; `address` and the other subcommands prompt for the
password. `generate --seed` restores from a phrase or hex and **prints
the seed again**. There is no wallet subcommand that backups or restores
without printing the seed.

Offline recovery that does not print the seed: copy the encrypted
`.sqwallet` and the mode-0600 pass file to offline storage. Restore by
copying those two files back. Do not use `export-key` as a backup (it
writes the raw private key). Do not copy another validator's wallet.
Never run the same signer on two nodes at once.

`--pass-file` must not be readable by other users (`chmod 600`). The node,
`sqc-wallet generate`, and `sqc-modcall` refuse a wallet or pass file with
any group or other permission bit set (mode `& 077 != 0`, so `640` fails
too); not enforced on Windows, which has no POSIX modes.

## Fund the bond (rehearsal)

`register` locks **2,500 SPLD** (`2_500 * 10^18` smallest units) from the
caller. The protocol does not mint that balance and does not pay an APY.
This document does not invent a funding process: the address must already
hold the bond plus fees on the rehearsal chain. Starting the process with
`--validator` does not transfer SPLD.

## Writes

All three actions are `TxCall` through the module router (`validators_v1`).
Operator automation should wait for a successful receipt:

```bash
NODE=http://127.0.0.1:8080
CHAIN_ID="$SQC_CHAIN_ID"   # published or lab chain id; do not invent one
WALLET=/var/sqc/validator.sqwallet
PASS=/var/sqc/wallet.pass
FEE=100000000000000000     # 0.1 SPLD; see the note on fees below

# register — locks 2,500 SPLD and records this wallet's ML-DSA-65 pubkey
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action register --fee "$FEE" \
  --params '{"pubkey":"{{PUBKEY}}"}' \
  --wait --require-success

# unregister — marks exiting; starts the 32-block wait
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action unregister --fee "$FEE" \
  --params '{}' \
  --wait --require-success

# finalize_exit — after 32 blocks, returns the bond
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action finalize_exit --fee "$FEE" \
  --params '{}' \
  --wait --require-success

# approve / revoke — validators_admission_v1 only; the signer must be an
# ACTIVE validator. Run approve from the tier's validator BEFORE a joiner
# submits register, or the joiner's register fails with "not approved".
./sqc-modcall write \
  --node "$NODE" --wallet "$WALLET" --pass-file "$PASS" --chain-id "$CHAIN_ID" \
  --module validators --action approve --fee "$FEE" \
  --params '{"address":"SQ:<joiner>"}' \
  --wait --require-success
```

`sqc-modcall` defaults `--fee` to 100,000 units. That clears the legacy
floor of 1,000 units on the public tiers (none of them schedules
`min_fee_v1`; see `/status.min_fee`) but not the `min_fee_v1` floor of
10^15 units (0.001 SPLD). The validator-join lab genesis activates that
fork at height 1, so without an explicit `--fee` every rehearsal write is
refused at submit. On any chain the pool multiplies its floor by 2/8/32 at
50/75/90 % occupancy, so read `/status.min_fee` before a write.

`--wait` alone still exits 0 on a `status: 0` receipt (lab/test default).
`--require-success` prints `receipt-status` (`success` / `execution_failed` /
`pending` / `rejected`) and exits 1 unless execution succeeded. It implies
`--wait`.

`register` params: `{ "pubkey": "<hex or base64 ML-DSA-65 public key>" }`.
`{{PUBKEY}}` expands to the signing wallet's key in base64. The module
refuses a key whose derived address is not the caller.

HTTP 202 on submit and a row in the registry are not activation.

## Launch (add to the tested follower config)

Take the follower command that already syncs (see [`RPC_NODE.md`](RPC_NODE.md))
and add the validator flags. Use this node's own wallet, not the original
producer's.

```bash
./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --metrics-addr 127.0.0.1:9100 \
  --chain-id "$SQC_CHAIN_ID" \
  --genesis-hash "$GENESIS_BLOCK_HASH" \
  --genesis-doc-hash "$GENESIS_DOC_HASH" \
  --peer "$BOOTNODE_URL" \
  --peer-pin "${BOOTNODE_URL}=${PEER_IDENTITY_HEX}" \
  --require-peer-pins \
  --validator \
  --wallet /var/sqc/validator.sqwallet \
  --wallet-pass-file /var/sqc/wallet.pass \
  --log-level info
```

Prefer `--wallet-pass-file` over `--wallet-pass` (`ps` can see the latter).
Authenticated proposal/vote delivery still needs the cluster write-plane
token today; membership alone does not grant transport. See
[`PEER_AUTH.md`](PEER_AUTH.md). Rehearse that on the join scripts, not by
sharing a production token.

## Confirm activation and QC

Registry presence is not enough. After `register` commits with
`status: 1`, check both the registry and the head certificate.

`GET /validators` returns `{ "validators": [...], "count": N, "bond_spld": 2500 }`
where each active row is `{ "address", "bond", "status": "active", "joined_height" }`.
`address` is an `SQ:` string and `bond` a decimal string in smallest units.

`GET /validators/{addr}` returns `{ "address", "bond", "bond_spld": 2500,
"status", "joined_height", "exit_height" }`. `status` is `inactive`,
`active`, or `exiting`; `exit_height` is `0` until `unregister`. An
unknown address is 404.

`GET /status` fields that matter here (real names):

| Field | What it actually says |
| --- | --- |
| `local_validator` | Address of this process's wallet key when `--validator` loaded one; on a full node given `--validator-pubkey-hex`, the pinned key's address instead. Absent otherwise |
| `validator_set_size` | `ListActive()` count |
| `multi_validator` | Schedule bit: whether the rule is active at the head. Not a QC. |
| `consensus_mode` | Engine this process is running: `follower`, `single`, or `bft` |
| `head_qc_present` | Head block carries a certificate |
| `head_qc_valid` | That certificate verifies against the committed set |
| `head_qc_votes` | Vote count on the head QC |
| `head_quorum_required` | Votes required |
| `validator_set_height` | Height the set was taken from |
| `head_qc_reason` | Present only when the head QC is not valid |

A producing validator on a `multi_validator` chain should show
`consensus_mode` of `bft`, `status` `active` at `/validators/{addr}`, and
a head whose `head_qc_valid` is true with this address among the votes
once it has been selected and a QC formed. `joined_height` is when the
bond locked, not when the node first voted.

## Restart without double-signing

The last vote this validator signed is stored at
`<data-dir>/vote-journal.json` (`height`, `round`, `block_hash`). The
engine writes and fsyncs that file **before** the vote is signed. On
start, the node logs `vote journal: last signed vote restored` and
refuses a contradicting vote (`ErrDoubleSign`: different block at the
same height and round, or any vote for a height/round already passed).
Re-recording the identical vote is allowed.

Keep the journal with the data dir. Do not delete it to “unstick” a
restart. Do not run two processes with the same wallet. This tree has
no validator key-rotation command and `validators_v1` does not slash;
do not promise either. The archived pass/tier lab's `slash` action is
not this registry.

## Reads

| Route | What |
| --- | --- |
| `GET /validators` | Active set (`address`, `bond`, `status`, `joined_height`, `count`, `bond_spld`) |
| `GET /validators/{addr}` | One record (`bond`, `bond_spld`, `status`, `joined_height`, `exit_height`) |
| `GET /staking/validators/{addr}` | Same record |
| `GET /staking/total-staked`, `GET /staking/count` | `total_staked` (decimal string), `validator_count` |
| `GET /status` | `consensus_mode`, `head_qc_*`, `local_validator`, `validator_set_size` |
| module read `bond` | `{ "bond": "<smallest units>", "spld": "2500" }` via `sqc-modcall read --module validators --action bond` |
| module reads `validator`, `list`, `count` | Same registry through `POST /call` |

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
