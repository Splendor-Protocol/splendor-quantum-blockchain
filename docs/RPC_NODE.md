# RPC follower (clean machine)

Run a non-producing node that pull-syncs published blocks. This is not a
validator guide. Do **not** pass `--validator` or `--trust-peer-genesis`.

Public admission has not launched. Use this to follow a published chain, not
to register or produce.

## What you must pin

A follower needs four values. Take none of them from the peer you are about
to trust without a second source. The table below and the tier table in
[readme §20](../readme.md#20-live-networks-and-endpoints) are that source.

| Variable | Flag | What it is |
| --- | --- | --- |
| `SQC_CHAIN_ID` | `--chain-id` | Published chain id. A genesis document naming another chain is refused. |
| `GENESIS_BLOCK_HASH` | `--genesis-hash` | Hash of block 0's header. |
| `GENESIS_DOC_HASH` | `--genesis-doc-hash` | Canonical hash of the genesis **document** (`sqc-node genesis-hash <file>`). |
| `PEER_IDENTITY_HEX` | `--peer-pin URL=HEX` | The bootnode's long-term ML-DSA-65 node key, hex. |

`--genesis-hash` and `--genesis-doc-hash` are different pins. The first is
block 0's header. The second is the genesis document. Block 0 does not
carry the document hash, and a peer that can change the document can mint
any allocation it likes unless the document hash is pinned. Pin both.

`genesis-production.json` in this repository is **not** the document
mainnet runs: it names a different fork list and different allocations.
Do not derive `GENESIS_DOC_HASH` from it.

### Observed public pins (2026-09-10)

Read from the public endpoints on 2026-09-10, node image `sqc-node:v1.0.12`
(commit `a3964a4`) on both tiers. Heights climb; nothing else here should
change without a reset, and every tier has been reset before. Re-derive
before use (next section). If a value differs from this table, stop and
ask; do not follow.

| | Mainnet | Testnet |
| --- | --- | --- |
| Chain id | `splendor-quantum-1` | `splendor-quantum-testnet-1` |
| Bootnode (`--peer`) | `https://quantum-rpc.splendor.org` | `https://quantum-rpc-testnet.splendor.org` |
| Block-0 hash | `ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949` | `324d7f561493ae6d08062288cb99adfbb3f83297d352b783f611171251a1289f` |
| Genesis document hash | `4ff371940b52a3b0ef2d2f5973716956b72ab894363ac780b3b4cb1308688640` | `c7930eb20b110fd3dc261911b14cb7042c9b3352a151e7ed0f12b0d7ecfb6281` |
| Bootnode identity, SHA3-256 of `node_pubkey` | `442e905612311035b942f3362ea9d4efa632c1d2581d36356afd2f281afacf7e` | `09f44c419a48c70579b1a532c60e5786ecacf4de76e606d1901dfaf21e9745a9` |

The identity fingerprint is the tier's genesis validator address in readme
§20: on the public tiers the node identity key is the validator key.

Devnet is two chains behind one hostname (readme §20). Do not follow it
from this guide.

## Derive and cross-check

```bash
BOOTNODE_URL=https://quantum-rpc.splendor.org
SQC_CHAIN_ID=splendor-quantum-1

# Chain id and block-0 hash, as the tier reports them.
curl -fsS "$BOOTNODE_URL/status" | jq -r '.chain_id, .genesis_hash'

# Genesis document hash: hash the document the tier serves, then compare
# with the table above.
curl -fsS "$BOOTNODE_URL/genesis" -o genesis.json
./sqc-node genesis-hash genesis.json

# Bootnode identity: base64 on the wire, hex on the flag. The SHA3-256 of
# the raw key must equal the fingerprint in the table.
curl -fsS "$BOOTNODE_URL/p2p/identity" | jq -r .node_pubkey | base64 -d > node_pubkey.bin
openssl dgst -sha3-256 node_pubkey.bin
PEER_IDENTITY_HEX=$(od -An -v -tx1 node_pubkey.bin | tr -d ' \n')
```

`/p2p/identity` also carries a rotating ML-KEM-768 key (`not_after`). Only
the ML-DSA-65 `node_pubkey` is pinned.

## Build

```bash
go build -mod=readonly -o sqc-node ./cmd/node
go build -mod=readonly -o sqc-wallet ./cmd/wallet
go build -mod=readonly -o sqc-modcall ./cmd/sqc-modcall
```

## Start (follower)

```bash
: "${SQC_CHAIN_ID:?published chain id}"
: "${GENESIS_BLOCK_HASH:?block-0 hash, cross-checked}"
: "${GENESIS_DOC_HASH:?genesis document hash, cross-checked}"
: "${BOOTNODE_URL:?published bootnode URL}"
: "${PEER_IDENTITY_HEX:?bootnode node_pubkey as hex, fingerprint cross-checked}"

./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --chain-id "$SQC_CHAIN_ID" \
  --genesis-hash "$GENESIS_BLOCK_HASH" \
  --genesis-doc-hash "$GENESIS_DOC_HASH" \
  --peer "$BOOTNODE_URL" \
  --peer-pin "${BOOTNODE_URL}=${PEER_IDENTITY_HEX}" \
  --require-peer-pins \
  --log-level info
```

The `--peer-pin` URL must be the exact `--peer` string. Prometheus metrics
listen on `--metrics-addr` (default `127.0.0.1:9100`); keep them private.

### Install as a service

[`contrib/systemd/sqc-follower.service`](../contrib/systemd/sqc-follower.service) is
a mainnet unit with the derived pins left as `REPLACE_*` placeholders. It
needs `curl`. `install.sh` writes its own `sqc-rpc.service` for validators;
that is a different contract, hence the different name.

```bash
sudo useradd --system --home /var/sqc --shell /usr/sbin/nologin sqc
sudo install -d -o sqc -g sqc -m 750 /var/sqc /var/sqc/data
sudo install -d -m 755 /opt/sqc
sudo install -m 755 sqc-node /opt/sqc/sqc-node
sudo install -m 644 contrib/systemd/sqc-follower.service /etc/systemd/system/sqc-follower.service
# fill the REPLACE_* values in /etc/systemd/system/sqc-follower.service
sudo systemctl daemon-reload
sudo systemctl enable --now sqc-follower
journalctl -u sqc-follower -f
```

## Auth: what a follower does and does not need

Follower **pull-sync** (sealed `get_block` / `get_latest` / `get_state_snapshot`)
is not token-gated. An RPC node catches up with `--genesis-hash`,
`--genesis-doc-hash`, `--peer` and `--peer-pin` and **no** `--rpc-auth-token`.
Without a token the node answers sealed write kinds with 503 (`write plane
disabled`) and plaintext `POST /block` with 410. That is the intended state
for a follower.

**Tx relay to a producer still requires the cluster write-plane token.**
Sealed `KindTx` is on the write plane today. A public RPC that accepts
`POST /tx/submit` cannot propagate that transaction to a producer unless both
sides share `--rpc-auth-token`. That is a current limitation; see
[`PEER_AUTH.md`](PEER_AUTH.md). Do not add `--rpc-auth-token` just to sync.

## Validate (hash compare, not height)

Height climbing is not enough. A peer can serve a tall fork. Check the pins
and the hashes.

1. The two pins are enforced at start-up, not by you. The first minute of
   the log must show both lines, or the node did not bootstrap from the
   pinned chain:

   ```
   sync: bootstrapped validator pubkey from pinned genesis block peer=... pubkey_prefix=...
   RPC: bootstrapped genesis allocations from validator peer allocations=... genesis_doc_hash=...
   ```

   A follower does not store block 0: `/block/0` is 404 and
   `/status.genesis_hash` is null on it. Read those from the bootnode, not
   from your node.
2. `chain_id` is the pinned one and the height climbs:

   ```bash
   curl -sS http://127.0.0.1:8080/status | jq '{chain_id, height, peer_count}'
   ```
3. Compare a committed block against a second published RPC, byte for byte
   (`hash` is a JSON byte array in block bodies):

   ```bash
   H=$(curl -sS http://127.0.0.1:8080/status | jq -r .height)
   diff <(curl -sS "http://127.0.0.1:8080/block/$H" | jq -c .hash) \
        <(curl -sS "$BOOTNODE_URL/block/$H" | jq -c .hash) && echo "block $H matches"
   ```

   Matching height with a different hash means you followed the wrong
   chain.

### Start-up is single-shot

The node fetches the pinned block 0 and the genesis document once, shortly
after start, and does not retry. Started while the bootnode is unreachable,
it logs `no peer served a genesis block matching the pinned hash` or
`genesis bootstrap from peer failed` once, stays up, and never syncs.
Restart it. The unit's `ExecStartPre` reachability wait exists for this.

### Expected log noise

- One `sync failed ... state root mismatch` on block 1 right after start:
  the genesis document lands one catch-up attempt late and the sync
  retries. Repeated mismatches mean the wrong document.
- `p2p: REFUSED unpinned peer` for `http://quantum-rpc.splendor.org`: the
  bootnode also advertises itself with an `http://` scheme, which is a
  different pin key. Refusing it is correct; sync continues over the
  pinned `https://` peer.
- `Node P2P identity key is stored unencrypted (mode 0600)`: the follower's
  own identity, generated in the data dir. Encrypt it with
  `--node-key-pass-file` (a mode-0600 file) if the box has other users.

Full replay from block 0 runs at a few blocks per second; a 130,000-block
chain takes hours. Verified 2026-09-10: a fresh follower started with the
mainnet values above synced about 600 blocks in 100 s over the public
endpoint.

API inventory: [`RPC_API.md`](RPC_API.md).
