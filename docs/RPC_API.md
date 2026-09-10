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
`smallest_spld`). Default JSON encoding emits those integers as numbers;
`?amounts=string` on block/tx/receipt routes quotes `amount` and `fee` so
JavaScript clients do not round values above 2^53.

| Code | Where | Meaning |
| --- | --- | --- |
| 200 | GET reads, including `/tx/{hash}/receipt` | Body is the object |
| 202 | `POST /tx/submit` | Admitted to **this** node's mempool (`status: accepted`) |
| 400 | Submit / decode | Rejected before the mempool |
| 404 | Missing block, tx, or receipt | Not indexed on this node |
| 503 | Sim / P2P inflight caps | Too busy; sealed writes also 503 when the write plane is disabled |

`202` is not a committed receipt. Poll `GET /tx/{hash}/receipt` until it
returns 200.

## Read plane

Anonymous `GET`s, outside both limiters below. Hashes inside block and tx
bodies are JSON byte arrays; `/status.genesis_hash` is hex.

| Method | Route | Notes |
| --- | --- | --- |
| GET | `/status` | Chain id, height, forks, genesis hash, node version, peers |
| GET | `/block/latest` | Head block |
| GET | `/block/{height}` | Block by height |
| GET | `/block/hash/{hash}` | Block by hash |
| GET | `/tx/{hash}` | Transaction location (+ receipt when indexed) |
| GET | `/tx/{hash}/receipt` | Execution receipt (`status` 1 = success, 0 = failed) |
| GET | `/balance/{address}` | Account balance |
| GET | `/nonce/{address}` | Next nonce |
| GET | `/state/root` | Current state root |
| GET | `/state/account/{address}` | Account state |
| GET | `/state/proof/{address}` | Merkle proof for an account |
| GET | `/validators` | Active validator set |
| GET | `/validators/{addr}` | One validator record |
| GET | `/staking/validators/{addr}` | Same record via staking prefix |
| GET | `/staking/total-staked` | Aggregate bond |
| GET | `/staking/count` | Validator count |
| GET | `/gov/proposals` | Governance list |
| GET | `/gov/proposals/{id}` | One proposal |
| GET | `/gov/proposals/{id}/votes/{addr}` | One vote |
| GET | `/gov/power/{addr}` | Voting power |
| GET | `/gov/total-power` | Total voting power |
| GET | `/contract/{address}` | Contract metadata |
| GET | `/contract/{address}/code` | Bytecode |
| GET | `/contract/{address}/storage/{slot}` | Storage slot |
| GET | `/verify/proof/{id}` | Verify-module proof |
| GET | `/bridge/{addr}` | Bridge instance |
| GET | `/bridge/{addr}/claim/{id}` | Claim status |
| GET | `/exchange/{addr}` | Exchange instance |
| GET | `/exchange/{addr}/condition/{id}` | Condition |
| GET | `/exchange/{addr}/order/{hash}` | Order status |
| GET | `/exchange/{addr}/nonce/{maker}` | Maker nonce |
| GET | `/exchange/{addr}/position/{condition}/{holder}` | Position |
| GET | `/dex/pool/{token0}/{token1}` | Pool |
| GET | `/dex/pool/{token0}/{token1}/quote` | Quote |
| GET | `/dex/pool/{token0}/{token1}/lp/{addr}` | LP position |
| GET | `/dex/price/{token0}/{token1}` | Price |
| GET | `/dex/amount-out`, `/dex/amount-in` | Swap math |
| GET | `/lending/pool/{asset}` | Lending pool |
| GET | `/lending/position/{user}/{asset}` | Supply position |
| GET | `/lending/borrow/{user}/{asset}` | Borrow position |
| GET | `/lending/utilisation/{asset}` | Utilisation |
| GET | `/lending/rates/{asset}` | Rates |
| GET | `/vm/opcodes` | Opcode catalog |
| GET | `/p2p/identity` | This node's signed ML-DSA-65 node key plus its rotating ML-KEM-768 key |
| GET | `/peers` | Known peers |
| GET | `/sync` | Sync view |
| GET | `/genesis` | The genesis document. Served only by a node started with `--genesis` (the producers); a follower does not serve it |
| GET | `/ws` | WebSocket event stream |
| GET | `/healthz` | Process liveness |
| GET | `/readyz` | Readiness (has committed at least one block) |
| GET | `/ai/stats` | AI-stats snapshot |

## Examples

Shapes below use the real field names from `rpc/server.go` and the
block/tx/receipt types. Byte arrays (`hash`, `state_root`, signatures) are
JSON arrays of 0–255; they are shortened here. `/status.genesis_hash` is
hex. A follower does not store block 0, so `/block/0` is 404 on it —
read that from a node that has the block (a producer, or the published
RPC).

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
  "forks": { "validators_v1": 1, "multi_validator": 1, "modules_v2": 79202 },
  "min_fee": 1000000000000000,
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

`forks` is omitted on a chain that schedules none. `genesis_hash` is
omitted when this node has no block 0. `local_validator` appears only
when the process loaded a validator wallet. `head_qc_reason` appears only
when the head QC is not valid. `node_version` / `commit` / `image_tag`
are `unknown` on an unstamped local build; a released image stamps
`core/buildinfo`.

### `GET /block/0`

```http
GET /block/0
```

```json
{
  "header": {
    "height": 0,
    "timestamp": 0,
    "chain_id": "splendor-quantum-1",
    "alg_id": "ML-DSA-65",
    "prev_hash": [0, 0, 0],
    "tx_merkle_root": [0, 0, 0],
    "state_root": [0, 0, 0],
    "validator_addr": "SQ:…",
    "tx_count": 0
  },
  "hash": [186, 116, 130, 99],
  "validator_signature": [0],
  "validator_pubkey": [0],
  "transactions": []
}
```

`header.qc_root` is omitted while it is empty. `validator_pubkey` is
populated on height 0 only. Compare `hash` and `header.state_root` to a
second published RPC; do not trust height alone.

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
  "from": "SQ:…",
  "to": "SQ:…",
  "amount": 1000000000000000000,
  "fee": 1000000000000000,
  "nonce": 0,
  "timestamp": 1778000000,
  "public_key": [0],
  "signature": [0]
}
```

`type` is `1` transfer, `2` deploy, `3` call, `5` x402. On admission:

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
  "tx_hash": [0],
  "tx_index": 0,
  "block_height": 130010,
  "block_hash": [0],
  "status": 1,
  "gas_used": 0,
  "from": "SQ:…",
  "to": "SQ:…",
  "amount": 1000000000000000000,
  "fee": 1000000000000000,
  "logs": [],
  "type": 1
}
```

`status` is `1` on success and `0` on execution failure (`error` is then
set). HTTP 200 here means the receipt exists; look at `status` for
execution. HTTP 404 means this node has not indexed that hash yet.

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
Excess sim work returns 503.

| Method | Route | Notes |
| --- | --- | --- |
| POST | `/call` | Read-only call / module read |
| POST | `/estimate` | Fee / gas quote |
| GET/POST | `/logs` | Bounded log query |
| POST | `/contract/verify` | Source verification |

**Per-IP rate limit**: `--rpc-rate-limit` / `--rpc-rate-burst` (defaults
25 rps, burst 50). Behind a proxy you control, `--trust-proxy` reads
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
| POST | `/p2p/sealed` | ML-KEM envelope. Write kinds (`block`, `announce`, `vote`, `proposal`, `tx`) need `--rpc-auth-token` inside the envelope; a node with no token answers them 503 `write plane disabled`. Read kinds (`get_block`, `get_latest`, `get_state_snapshot`) need no token. Decryption work is capped by `--p2p-max-inflight` (503 + `Retry-After`). |
| POST | `/block` | Token + plaintext P2P. Default build answers **410** unless `--allow-plaintext-p2p`. |
| POST | `/peers/announce` | `--rpc-auth-token` (`X-Node-Auth`) |

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
