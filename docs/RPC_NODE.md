# RPC follower (clean machine)

Run a non-producing node that pull-syncs published blocks. This is not a
validator guide. Do **not** pass `--validator` or `--trust-peer-genesis`.

Public admission has not launched. Use this to follow a published chain, not
to register or produce.

## What you must pin yourself

This document pins only the verified **block-0 hash**. Fill the rest from the
published network manifest — do not invent them:

| Variable | What it is |
| --- | --- |
| `GENESIS_DOC_HASH` | Canonical hash of the published `genesis.json` (`sqc-node genesis-hash <file>`) |
| `BOOTNODE_URL` | Reachable RPC URL of a published bootnode |
| `PEER_IDENTITY_HEX` | That bootnode's ML-DSA-65 identity hex (`GET /p2p/identity`) |
| `SQC_CHAIN_ID` | Published chain id, if the manifest names one |

`--genesis-hash` and `--genesis-doc-hash` are different pins. The first is
block 0's header. The second is the genesis **document**. A peer that can
change the document can mint any allocation it likes unless the document hash
is pinned.

## Build

```bash
go build -mod=readonly -o sqc-node ./cmd/node
go build -mod=readonly -o sqc-wallet ./cmd/wallet
go build -mod=readonly -o sqc-modcall ./cmd/sqc-modcall
```

## Start (follower)

```bash
# Required: published document hash, bootnode URL, and that peer's identity.
: "${GENESIS_DOC_HASH:?set GENESIS_DOC_HASH from the published manifest}"
: "${BOOTNODE_URL:?set BOOTNODE_URL from the published manifest}"
: "${PEER_IDENTITY_HEX:?set PEER_IDENTITY_HEX from GET ${BOOTNODE_URL}/p2p/identity}"

./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --genesis-hash ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949 \
  --genesis-doc-hash "$GENESIS_DOC_HASH" \
  --peer "$BOOTNODE_URL" \
  --peer-pin "${BOOTNODE_URL}=${PEER_IDENTITY_HEX}" \
  --require-peer-pins \
  --log-level info
```

Optional: `--chain-id "$SQC_CHAIN_ID"` if the published manifest names one.
That refuses a genesis document for a different chain.

An example systemd unit is [`contrib/systemd/sqc-rpc.service`](../contrib/systemd/sqc-rpc.service).

## Auth: what a follower does and does not need

Follower **pull-sync** (sealed `get_block` / `get_latest` / `get_state_snapshot`)
is not token-gated. An RPC node can catch up with `--genesis-hash`, `--peer`,
and `--peer-pin` and **no** `--rpc-auth-token`.

**Tx relay to a producer still requires the cluster write-plane token.**
Sealed `KindTx` is on the write plane today. A public RPC that accepts
`POST /tx/submit` cannot propagate that transaction to a producer unless both
sides share `--rpc-auth-token`. That is a current limitation; see
[`PEER_AUTH.md`](PEER_AUTH.md). Do not add `--rpc-auth-token` just to sync.

## Validate (hash compare, not height)

Height climbing is not enough. A peer can serve a tall fork. Check identity
and hashes:

```bash
curl -sS http://127.0.0.1:8080/status
curl -sS http://127.0.0.1:8080/block/0
curl -sS http://127.0.0.1:8080/block/latest
```

1. `/status` — `chain_id` matches the published id (when you set one).
2. `/block/0` — `hash` equals
   `ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949`.
3. `/block/latest` — compare `hash` (and parent linkage) against the same
   height on a second published RPC. Matching height with a different hash
   means you followed the wrong chain.

API inventory: [`RPC_API.md`](RPC_API.md).
