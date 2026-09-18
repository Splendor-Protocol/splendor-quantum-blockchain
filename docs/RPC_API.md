# RPC API (native routes)

This node speaks a native REST API. It is **not** Ethereum JSON-RPC.
There are no `/eth_*` methods, no `eth_blockNumber`, and no web3 namespace.
Clients that assume geth/erigon/nethermind will not work.

Listen address defaults to `127.0.0.1:8080` (`--rpc-addr`). Prometheus
metrics are a separate listener, `--metrics-addr` (default
`127.0.0.1:9100`), serving `/metrics` and `/health`. They leak node
internals; keep them private.

## Units and status codes

Amounts, fees, and bonds are integers in the **smallest SPLD unit**.
`1 SPLD = 10^18` units (`/status.decimals` is `18`; `/status.fee_unit` is
`smallest_spld`). Encoding differs by route. Block, tx, and receipt bodies
emit `amount` and `fee` as JSON **numbers** by default; `?amounts=string`
on those routes quotes them so JavaScript clients do not round values above
2^53. `/balance.balance` is always a decimal **string**, as are the `bond` and
`total_staked` fields the `validators` module returns through `POST /call`.
`/status.min_fee` is a number. `POST /tx/submit` accepts `amount` and
`fee` as either numbers or decimal strings.

| Code | Where | Meaning |
| --- | --- | --- |
| 200 | GET reads, including `/tx/{hash}/receipt` | Body is the object |
| 202 | `POST /tx/submit` | Admitted to **this** node's mempool (`status: accepted`) |
| 400 | Submit / decode | Rejected before the mempool |
| 401 | Write plane | Token present but wrong (`X-Node-Auth` or inside the sealed envelope) |
| 404 | Missing block, tx, or receipt | Not indexed on this node |
| 410 | `POST /block` | Plaintext P2P is off (default); use the sealed transport |
| 429 | Both limiters | Per-IP rate exceeded; `Retry-After: 1` |
| 503 | Sim / P2P inflight caps | Too busy (`Retry-After: 1`); sealed writes also 503 when the write plane is disabled |

`202` is not a committed receipt. Poll `GET /tx/{hash}/receipt` until it
returns 200.

## Read plane

Anonymous `GET`s, outside both limiters below. Wire encoding of byte
fields in block, tx, and receipt bodies (Go `encoding/json` defaults):

- Fixed 32-byte fields are **arrays of 32 integers** 0–255: `hash`,
  `prev_hash`, `tx_merkle_root`, `state_root`, `tx_hash`, `block_hash`,
  and every address (`from`, `to`, `validator_addr`).
- Variable-length byte fields are **base64 strings**: `validator_signature`,
  `validator_pubkey`, `public_key`, `signature`, `data`, `qc_root`.
- `/status.genesis_hash` and every field of `GET /tx/{hash}` are **hex**
  strings. `/validators*`, `/balance`, and `/nonce` render addresses as
  `SQ:<hex>` strings.

| Method | Route | Notes |
| --- | --- | --- |
| GET | `/status` | Chain id, height, forks, genesis hash, node version, peers |
| GET | `/block/latest` | Head block |
| GET | `/block/{height}` | Block by height |
| GET | `/block/hash/{hash}` | Block by hash |
| GET | `/tx/{hash}` | Transaction location — `hash`, `block_hash` (hex strings), `block_height`, `tx_index` — plus `receipt` and `status` when the receipt is indexed |
| GET | `/tx/{hash}/receipt` | Execution receipt (`status` 1 = success, 0 = failed) |
| GET | `/balance/{address}` | Account balance (`balance` decimal string, `unit`) |
| GET | `/nonce/{address}` | Next nonce |
| GET | `/state/root` | Head state root (`state_root` hex, `block_height`, `block_hash`) |
| GET | `/state/account/{address}` | Account state (`balance` string, `nonce`, `has_public_key`, `state_root` hex) |
| GET | `/state/proof/{address}` | Merkle proof for an account |
| GET | `/contract/{address}` | Contract metadata |
| GET | `/contract/{address}/code` | Bytecode |
| GET | `/contract/{address}/storage/{slot}` | Storage slot |
| GET | `/verify/proof/{id}` | Verify-module proof |
| GET | `/bridge/{addr}` | Bridge instance |
| GET | `/bridge/{addr}/claim/{id}` | Claim status |
| GET | `/vm/opcodes` | Opcode catalog |
| GET | `/p2p/identity` | This node's signed ML-DSA-65 node key plus its rotating ML-KEM-768 key |
| GET | `/peers` | Known peers |
| GET | `/sync` | Sync view |
| GET | `/genesis` | The genesis document. Registered only on a node started with `--genesis` — a producer, or a follower given the document locally. A follower that bootstrapped the document from a peer answers 404 |
| GET | `/ws` | Server-sent event stream (`text/event-stream`, `?subscribe=` filters) — not a WebSocket upgrade despite the path |
| GET | `/healthz` | Liveness with build identity (`status`, `chain_id`, `commit`, `image_tag`, `node_version`, `uptime_seconds`) |
| GET | `/readyz` | Readiness: `{"status":"ready"}` once height is above 0 |
| GET | `/ai/stats` | AI-stats snapshot |

`/dex/*`, `/exchange/*`, `/lending/*`, and `/gov/*` are **not registered**.
Those names are not routed native modules.

## Examples

Shapes below use the real field names from `rpc/server.go` and the
block/tx/receipt types, checked against the public mainnet RPC on
2026-09-10. 32-byte arrays are shortened to `[186, 116, …]`; base64
strings to `"gJgu…"`. `/status.genesis_hash` is hex. A follower does not
store block 0, so `/block/0` is 404 on it — read that from a node that has
the block (a producer, or the published RPC).

### `GET /status`

```http
GET /status
```

```json
{
  "chain_id": "splendor-quantum-1",
  "height": 130001,
  "mempool_size": 0,
  "peer_count": 1,
  "node_version": "unknown",
  "commit": "unknown",
  "image_tag": "unknown",
  "timestamp": 1778000000,
  "forks": {
    "exchange_v1": 1, "module_revert_v1": 1, "modules_v2": 79202,
    "multi_validator": 1, "qrc1155_v1": 1, "qrc20_v2": 1, "validators_v1": 1
  },
  "min_fee": 1000,
  "fee_unit": "smallest_spld",
  "decimals": 18,
  "multi_validator": true,
  "validator_set_size": 1,
  "consensus_mode": "follower",
  "head_qc_present": true,
  "head_qc_valid": true,
  "head_qc_votes": 1,
  "head_quorum_required": 1,
  "validator_set_height": 130001,
  "genesis_hash": "ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949"
}
```

`forks` is the mainnet schedule observed 2026-09-10 and is omitted on a
chain that schedules none. `min_fee` is the pool's current floor: `1000`
units on the public tiers (none schedules `min_fee_v1`), raised by the
congestion multiplier as the pool fills. `genesis_hash` is omitted when
this node has no block 0. `local_validator` appears when the process
loaded a validator wallet **or** was given `--validator-pubkey-hex`
(a full node reports the pinned key's address there). `head_qc_reason`
appears only when the head QC is not valid. `node_version` / `commit` /
`image_tag` are `unknown` on an unstamped local build; a released image
stamps `core/buildinfo`.

### `GET /block/0`

```http
GET /block/0
```

```json
{
  "header": {
    "height": 0,
    "timestamp": 1786618272,
    "chain_id": "splendor-quantum-1",
    "alg_id": "ML-DSA-65",
    "prev_hash": [0, 0, …],
    "tx_merkle_root": [0, 0, …],
    "state_root": [183, 41, …],
    "validator_addr": [68, 46, 144, 86, …],
    "tx_count": 0
  },
  "hash": [186, 116, 130, 99, …],
  "validator_signature": "gJgu…",
  "validator_pubkey": "gJgu…",
  "transactions": []
}
```

`header.validator_addr` is the 32-byte address as an array, not an
`SQ:` string; `SQ:` + hex of those bytes is the form `/validators` uses.
`header.qc_root` (base64) is omitted while it is empty, and `votes` (the
quorum certificate, an array of `{alg_id, height, round, block_hash,
validator_addr, signature, timestamp}`) is omitted when there is none —
block 0 carries neither. `validator_pubkey` is `omitempty`: block 0
always carries it, and on the public tiers every BFT-sealed block was
observed to carry it too, so do not assume it is absent above height 0.
Compare `hash` and `header.state_root` to a second published RPC; do not
trust height alone.

### `POST /tx/submit`

```http
POST /tx/submit
Content-Type: application/json
```

```json
{
  "version": 1,
  "chain_id": "splendor-quantum-1",
  "type": 1,
  "alg_id": "ML-DSA-65",
  "from": [68, 46, 144, 86, …],
  "to": [224, 139, 40, 140, …],
  "amount": 1000000000000000000,
  "fee": 1000,
  "nonce": 0,
  "timestamp": 1778000000,
  "public_key": "gJgu…",
  "signature": "kR3a…"
}
```

`from` and `to` are 32-byte arrays — an `SQ:` string here is a decode
error. `public_key` and `signature` are base64. `public_key` is required
while the sender has no key registered in state (its first transaction;
the CLIs attach it when `nonce` is `0`) and may be omitted afterwards —
the node then reads the key from state. `data` (base64) is omitted when
empty. `type` is `1` transfer, `2` deploy, `3` call, `5`
x402. On admission:

```http
HTTP/1.1 202 Accepted
```

```json
{ "status": "accepted", "hash": "ab…64 hex chars…" }
```

A 400 body is `{ "error": "rejected: …" }` (or `invalid transaction JSON`).
202 is mempool only. Relaying that tx to a producer still needs the
write-plane token; see [`PEER_AUTH.md`](PEER_AUTH.md).

### `GET /tx/{hash}/receipt`

```http
GET /tx/ab…/receipt
```

```json
{
  "tx_hash": [38, 59, 134, 195, …],
  "tx_index": 0,
  "block_height": 130010,
  "block_hash": [50, 105, 88, 246, …],
  "status": 1,
  "gas_used": 0,
  "from": [9, 244, 76, 65, …],
  "to": [224, 139, 40, 140, …],
  "amount": 1000000000000000000,
  "fee": 1000,
  "logs": [],
  "type": 1
}
```

`status` is `1` on success and `0` on execution failure (`error` is then
set). `contract_address` appears on a successful deploy. HTTP 200 here
means the receipt exists; look at `status` for execution. HTTP 404 means
this node has not indexed that hash yet.

## Metered

Two different limiters. Neither applies to the read plane above.

**Simulation guard**: routes that run VM work. Per-IP rate limit plus a
global in-flight cap and a wall-time cap. Flag value `0` selects the
built-in default.

| Flag | CLI default | Built-in when 0 |
| --- | --- | --- |
| `--sim-rate-limit` | `0` | `100` req/s per client IP |
| `--sim-rate-burst` | `0` | `200` |
| `--sim-max-inflight` | `0` | `8` concurrent simulations |
| `--sim-timeout` | `0` | `5s` |

Also `--cors-origin` (repeatable, default none; `*` allows any origin).
Over the per-IP rate: 429. Over the in-flight cap: 503. Both carry
`Retry-After: 1`.

| Method | Route | Notes |
| --- | --- | --- |
| POST | `/call` | Read-only call / module read |
| POST | `/estimate` | Fee / gas quote |
| GET/POST | `/logs` | Bounded log query |
| POST | `/contract/verify` | Source verification |

**Per-IP rate limit**: `--rpc-rate-limit` / `--rpc-rate-burst` (defaults
25 rps, burst 50; `0` disables). Over the limit is 429 with
`Retry-After: 1`. Behind a proxy you control, `--trust-proxy` reads
`X-Forwarded-For`; anywhere else it lets clients spoof their IP.

| Method | Route | Notes |
| --- | --- | --- |
| POST | `/tx/submit` | Accept a signed native transaction (202 = mempool) |
| POST | `/x402/verify` | x402 verify |
| POST | `/x402/settle` | x402 settle |
| POST | `/verify/check` | Verify-module check |
| POST | `/verify/attest` | Verify-module attest |
| POST | `/verify/settle` | Verify-module settle |

**Not metered**: bytecode helpers with a 1 MB body cap and no guard:
`POST /vm/disasm`, `POST /vm/validate`, `POST /vm/estimate-gas`.

`POST /tx/submit` accepts a tx into **this** node's mempool. Relaying it to
a producer still requires the write-plane token today. See
[`PEER_AUTH.md`](PEER_AUTH.md).

## Write plane (cluster, not public)

| Method | Route | Auth |
| --- | --- | --- |
| POST | `/p2p/sealed` | ML-KEM envelope. Write kinds (`block`, `announce`, `vote`, `proposal`, `tx`) need `--rpc-auth-token` inside the envelope; a node with no token answers them 503 `write plane disabled`, a wrong token is 401. Read kinds (`get_block`, `get_latest`, `get_state_snapshot`) need no token. Decryption work is capped by `--p2p-max-inflight` (default 64; 503 + `Retry-After`). |
| POST | `/block` | Token (`X-Node-Auth`) + plaintext P2P. Default build answers **410** unless `--allow-plaintext-p2p`. |
| POST | `/peers/announce` | `--rpc-auth-token` (`X-Node-Auth`); 503 with no token, 401 with a wrong one |

A follower used only to pull blocks does not need `--rpc-auth-token`.
A node that must push blocks, announce peers, or relay `KindTx` does.

## Receipts

`GET /tx/{hash}/receipt` returns the native receipt object. `status` is `1`
on success and `0` on execution failure, with `error` set when failed.
`sqc-modcall write --require-success` treats only `status: 1` as success.
Do not treat HTTP 202 from `/tx/submit` as `status: 1`.

## Finality (`/status`)

This API does not expose Ethereum `finalized` / `safe` tags. What
`/status` actually reports:

| Field | Meaning |
| --- | --- |
| `consensus_mode` | Engine this process is running: `single` (no certificate), `bft` (proposes/commits with a QC), or `follower` (applies peer blocks) |
| `multi_validator` | Schedule bit at the head — not a QC |
| `head_qc_present` | Head block carries a certificate |
| `head_qc_valid` | That certificate verifies against the committed validator set |
| `head_qc_votes` | Vote count on the head QC |
| `head_quorum_required` | Votes required for quorum |
| `validator_set_height` | Height the set was taken from |
| `head_qc_reason` | Set only when the head QC is not valid |

A healthy HTTP `/status` and a climbing `height` are not a valid QC.

## Not present

- `/eth_*`, `net_*`, `web3_*`
- Ethereum-style JSON-RPC envelopes (`{"jsonrpc":"2.0","method":...}`)
- Engine API / authenticated JWT
