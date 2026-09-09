# RPC API (native routes)

This node speaks a native REST API. It is **not** Ethereum JSON-RPC.
There are no `/eth_*` methods, no `eth_blockNumber`, and no web3 namespace.
Clients that assume geth/erigon/nethermind will not work.

Listen address defaults to `127.0.0.1:8080` (`--rpc-addr`).

## Read plane

| Method | Route | Notes |
| --- | --- | --- |
| GET | `/status` | Chain id, height, forks, node version |
| GET | `/block/latest` | Head block |
| GET | `/block/{height}` | Block by height |
| GET | `/block/hash/{hash}` | Block by hash |
| GET | `/tx/{hash}` | Transaction location (+ receipt when indexed) |
| GET | `/tx/{hash}/receipt` | Execution receipt (`status` 1 = success, 0 = failed) |
| GET | `/balance/{address}` | Account balance |
| GET | `/nonce/{address}` | Next nonce |
| GET | `/validators` | Active validator set |
| GET | `/validators/{addr}` | One validator record |
| GET | `/staking/validators/{addr}` | Same record via staking prefix |
| GET | `/staking/total-staked` | Aggregate bond |
| GET | `/staking/count` | Validator count |
| GET | `/p2p/identity` | This node's signed ML-DSA + ML-KEM identity |
| GET | `/peers` | Known peers |
| GET | `/sync` | Sync view |
| GET | `/healthz` | Process liveness |
| GET | `/readyz` | Readiness |
| GET | `/ai/stats` | AI-stats snapshot |
| GET | `/gov/proposals` | Governance list |
| GET | `/gov/proposals/{id}` | One proposal |
| GET | `/gov/proposals/{id}/votes/{addr}` | One vote |
| GET | `/gov/power/{addr}` | Voting power |
| GET | `/gov/total-power` | Total voting power |
| GET | `/contract/{address}` | Contract metadata |
| GET | `/contract/{address}/code` | Bytecode |
| GET | `/contract/{address}/storage/{slot}` | Storage slot |
| GET | `/verify/proof/{id}` | Verify-module proof |
| GET | `/vm/opcodes` | Opcode catalog |

## Metered / simulation

These can run VM work. They are rate-limited and concurrency-capped.

| Method | Route | Notes |
| --- | --- | --- |
| POST | `/call` | Read-only call / module read |
| POST | `/estimate` | Fee / gas quote |
| POST | `/tx/submit` | Accept a signed native transaction (202 = mempool) |
| GET/POST | `/logs` | Bounded log query |
| POST | `/vm/disasm` | Disassemble |
| POST | `/vm/validate` | Validate bytecode |
| POST | `/vm/estimate-gas` | VM gas estimate |
| POST | `/x402/verify` | x402 verify |
| POST | `/x402/settle` | x402 settle |
| POST | `/verify/check` | Verify-module check |
| POST | `/verify/attest` | Verify-module attest |
| POST | `/verify/settle` | Verify-module settle |

`POST /tx/submit` accepts a tx into **this** node's mempool. Relaying it to
a producer still requires the write-plane token today. See
[`PEER_AUTH.md`](PEER_AUTH.md).

## Write plane (cluster, not public)

| Method | Route | Auth |
| --- | --- | --- |
| POST | `/p2p/sealed` | ML-KEM envelope; write kinds need `--rpc-auth-token` inside the envelope. Read kinds (`get_block`, `get_latest`, `get_state_snapshot`) do not. |
| POST | `/block` | Token + plaintext P2P. Default build answers **410** unless `--allow-plaintext-p2p`. |
| POST | `/peers/announce` | `--rpc-auth-token` (`X-Node-Auth`) |

A follower used only to pull blocks does not need `--rpc-auth-token`.
A node that must push blocks, announce peers, or relay `KindTx` does.

## Receipts

`GET /tx/{hash}/receipt` returns the native receipt object. `status` is `1`
on success and `0` on execution failure, with `error` set when failed.
`sqc-modcall write --wait --require-success` treats only `status: 1` as
success.

## Not present

- `/eth_*`, `net_*`, `web3_*`
- Ethereum-style JSON-RPC envelopes (`{"jsonrpc":"2.0","method":...}`)
- Engine API / authenticated JWT
