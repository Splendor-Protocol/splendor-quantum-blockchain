# RPC follower (clean machine)

Run a non-producing node that pull-syncs published blocks. This is not a
validator guide. Do **not** pass `--validator` or `--trust-peer-genesis`.

Public admission has not launched. Use this to follow a published chain, not
to register or produce.

## What you must pin

A follower needs four values. Take none of them from the peer you are about
to trust without a second source. Immutable mainnet identity (chain id and
block-0 hash) is also listed in [`NETWORK_MANIFEST.md`](NETWORK_MANIFEST.md).
The table below and the tier table in
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
go mod download
go mod verify
go build -mod=readonly -o sqc-node ./cmd/node
go build -mod=readonly -o sqc-wallet ./cmd/wallet
go build -mod=readonly -o sqc-modcall ./cmd/sqc-modcall
```

An unstamped `go build` does **not** report a release commit. `/status` fields
`commit`, `image_tag`, and `node_version` come from `core/buildinfo`, which is
injected at link time (`-X …/core/buildinfo.Commit=` and `ImageTag=`). A local
build reports `unknown` for all three. Released container images stamp those
values from `SQC_COMMIT` / `SQC_IMAGE_TAG`; when you run a released binary,
compare `/status.commit` and `/status.image_tag` to the published image tag.

## Start (follower)

Install an independently obtained genesis document if you have one, then hash
it. Do not derive the pin from `genesis-production.json` in this repository.

```bash
sudo install -d -m 755 /etc/sqc
# copy the independently obtained document, then:
./sqc-node genesis-hash /etc/sqc/genesis.json
# output must equal $GENESIS_DOC_HASH from the pin table
```

`--genesis` is optional. Without it, a follower still bootstraps allocations
from the peer-served document once that document hashes to
`--genesis-doc-hash`. Pass `--genesis /etc/sqc/genesis.json` only when you
hold that same document locally. Do not use `--trust-peer-genesis`.

```bash
: "${SQC_CHAIN_ID:?published chain id}"
: "${GENESIS_BLOCK_HASH:?block-0 hash, cross-checked}"
: "${GENESIS_DOC_HASH:?genesis document hash, cross-checked}"
: "${BOOTNODE_URL:?published bootnode URL}"
: "${PEER_IDENTITY_HEX:?bootnode node_pubkey as hex, fingerprint cross-checked}"

./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --metrics-addr 127.0.0.1:9100 \
  --chain-id "$SQC_CHAIN_ID" \
  --genesis /etc/sqc/genesis.json \
  --genesis-hash "$GENESIS_BLOCK_HASH" \
  --genesis-doc-hash "$GENESIS_DOC_HASH" \
  --peer "$BOOTNODE_URL" \
  --peer-pin "${BOOTNODE_URL}=${PEER_IDENTITY_HEX}" \
  --require-peer-pins \
  --log-level info
```

Omit `--genesis` if `/etc/sqc/genesis.json` is not present. The `--peer-pin`
URL must be the exact `--peer` string. `--metrics-addr` defaults to
`127.0.0.1:9100` and serves `/metrics` plus `/health`; keep that listener
private. Do not advertise a loopback `--public-url` — see below.

### Service user and data-dir permissions

Run the process as a dedicated unprivileged user. The data directory holds
the chain store (`state/`, `chain/`, `txindex/`), the node's P2P identity
key, and (if you later enable `--validator`) `vote-journal.json`. Other
users on the box must not read or write it.

- Create the system user once. Home `/var/sqc`, shell `nologin`.
- Data dir `/var/sqc/data` owned by that user, mode `750` (owner rwx, group
  rx, other none). The unit's `ExecStartPre` creates it outside the
  systemd sandbox because `ProtectSystem=strict` blocks the node's own
  `mkdir`.
- Binary in `/opt/sqc` (mode `755`). Genesis document in `/etc/sqc`
  (mode `644` is fine: it is not a secret).
- Password / node-key files, if any, stay mode `0600` and are never
  placed in the unit's `ExecStart` line.

### Install as a service

[`contrib/systemd/sqc-follower.service`](../contrib/systemd/sqc-follower.service) is
the existing mainnet unit. Fill its `REPLACE_*` placeholders from your own
derivation; do not embed secrets in the unit. It needs `curl`.
`install.sh` writes its own `sqc-rpc.service` for validators; that is a
different contract, hence the different name.

```bash
sudo useradd --system --home /var/sqc --shell /usr/sbin/nologin sqc
sudo install -d -o sqc -g sqc -m 750 /var/sqc /var/sqc/data
sudo install -d -m 755 /opt/sqc /etc/sqc
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

## Expose client RPC (only when intended)

Keep `--rpc-addr 127.0.0.1:8080` and put TLS plus a reverse proxy in front
when clients should reach this node. The node can terminate TLS itself
(`--tls-cert` / `--tls-key`, TLS 1.3). `--tls-require-hybrid` refuses
clients that cannot negotiate X25519MLKEM768; without it the node offers
the hybrid group and still falls back to classical X25519. Metrics stay
on `--metrics-addr 127.0.0.1:9100` — do not proxy `/metrics` publicly.

Flags that already exist (defaults in parentheses):

| Flag | Default | Role |
| --- | --- | --- |
| `--cors-origin` | none (repeatable; `*` allows any) | Browser origins allowed to call this node |
| `--rpc-rate-limit` | `25` | Sustained requests/second per client IP on `/tx/submit` and x402 |
| `--rpc-rate-burst` | `50` | Burst for those same routes |
| `--sim-rate-limit` | `0` → built-in `100` | Per-IP rate on `/call`, `/estimate`, `/logs` |
| `--sim-rate-burst` | `0` → built-in `200` | Burst for those VM routes |
| `--sim-max-inflight` | `0` → built-in `8` | Global concurrent VM simulations; excess is 503 |
| `--sim-timeout` | `0` → built-in `5s` | Wall time one simulation may hold a slot |
| `--trust-proxy` | `false` | Read client IP from `X-Forwarded-For` |

Enable `--trust-proxy` only behind a proxy you control. Anywhere else,
clients can spoof `X-Forwarded-For` and bypass the per-IP limiters.
Inventory actual routes in [`RPC_API.md`](RPC_API.md); this is not an
Ethereum JSON-RPC API.

When this node should be dialable by peers, set `--public-url` to the
externally reachable base URL (the name the proxy serves). The default
self-URL is derived from `--rpc-addr` and becomes `http://127.0.0.1:8080`
or `http://localhost:…` — no peer can dial that. Do not advertise
loopback. A follower that only pulls and serves local clients does not
need `--public-url`.

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
3. Compare a committed block against a second published RPC, byte for byte.
   Check **both** the block `hash` and `header.state_root` (`hash` and
   `state_root` are JSON byte arrays in block bodies; `/state/root` is hex):

   ```bash
   H=$(curl -sS http://127.0.0.1:8080/status | jq -r .height)
   diff <(curl -sS "http://127.0.0.1:8080/block/$H" | jq -c '{hash, root: .header.state_root}') \
        <(curl -sS "$BOOTNODE_URL/block/$H" | jq -c '{hash, root: .header.state_root}') \
     && echo "block $H hash and state_root match"
   ```

   Matching height with a different hash or root means you followed the
   wrong chain. A moving height alone does not prove the node validates.

4. Restart and replay. Stop the process, start it again against the same
   `--data-dir`, and confirm `/status.height` resumes (does not reset) and
   a later shared height still matches hash and root. The node replays from
   the persisted store; it does not re-download history it already has.

5. Watch lag and error logs (`journalctl -u sqc-follower -f`, or the
   process stderr). Persistent `state root mismatch`, `REFUSED unpinned
   peer` against the pin you intended, or a height that stalls while the
   published RPC moves, are failures — not “still catching up.” Expected
   one-shot noise is listed below.

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

## Backup, restore, and upgrade

Stop the node before copying the data directory. A live copy can tear
`state/`, `chain/`, or `txindex/`.

```bash
sudo systemctl stop sqc-follower
sudo tar -C /var/sqc -czf /secure/sqc-data-$(date +%Y%m%d).tar.gz data
sudo systemctl start sqc-follower
```

Restore onto an empty host by extracting that archive to `/var/sqc/data`
as user `sqc` (mode `750`) and starting the same unit with the same pins.
Do not restore a validator data dir onto a second live signer — see
[`VALIDATOR.md`](VALIDATOR.md).

Upgrade is non-destructive: replace `/opt/sqc/sqc-node` with the new
binary (or the stamped release image), then `systemctl restart
sqc-follower`. The data dir stays. Do **not** run `sqc update --clean`
or any installer path that wipes or re-genesises the store. A public
join is never “mint a new chain.”

API inventory: [`RPC_API.md`](RPC_API.md).
