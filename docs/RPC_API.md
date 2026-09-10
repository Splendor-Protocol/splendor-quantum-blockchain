# RPC API (native routes)

This node speaks a native REST API. It is **not** Ethereum JSON-RPC.
There are no `/eth_*` methods, no `eth_blockNumber`, and no web3 namespace.
Clients that assume geth/erigon/nethermind will not work.

Listen address defaults to `127.0.0.1:8080` (`--rpc-addr`). Prometheus
metrics are a separate listener, `--metrics-addr` (default
`127.0.0.1:9100`), serving `/metrics` and `/health`. They leak node
internals; keep them private.

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

## Metered

Two different limiters. Neither applies to the read plane above.

**Simulation guard**: routes that run VM work. Per-IP rate limit plus a
global in-flight cap and a wall-time cap (`--sim-rate-limit`,
`--sim-max-inflight`, `--sim-timeout`); excess gets 503.

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

## Not present

- `/eth_*`, `net_*`, `web3_*`
- Ethereum-style JSON-RPC envelopes (`{"jsonrpc":"2.0","method":...}`)
- Engine API / authenticated JWT
