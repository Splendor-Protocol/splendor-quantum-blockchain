# Peer authentication (current vs public)

Design note only. No protocol change is specified here as implemented.

## Current model

Three planes share one node process:

| Plane | What travels | Who may do it today |
| --- | --- | --- |
| Committed-block **pull** | Sealed `get_block` / `get_latest` / `get_state_snapshot` | Any peer that can complete ML-KEM to a pinned identity. **Not** token-gated. |
| Consensus **push** | Sealed `proposal`, `vote`, `block`, `announce` | Peers that also carry the shared `--rpc-auth-token` inside the envelope. |
| Public **read / tx** | HTTP `GET` reads; `POST /tx/submit` | Reads are public on the RPC bind. Submit lands in the local mempool. Relaying that tx (`KindTx`) is write-plane and still token-gated. |

Peer **identity** is a long-term ML-DSA-65 node key plus a rotating
ML-KEM-768 encapsulation key (`not_after`), served at `GET /p2p/identity`.
`--peer-pin URL=PUBKEY_HEX` pins the ML-DSA-65 key only (base64 on the wire,
hex on the flag). `--require-peer-pins` refuses trust-on-first-use. On the
public tiers the node key is the genesis validator's key: its SHA3-256 is the
genesis validator address in readme §20.

The write-plane token is a **cluster secret**. It is not a validator
identity. Every node that should accept blocks or relay txs from the
producer set must be handed the same value. A stranger who learns it can
push on the write plane. A stranger who does not cannot relay a tx, even
if the tx itself is validly signed.

Inbound produced blocks are still authenticated by the **block seal**
(validator ML-DSA-65) in addition to the token. The token is who may speak
on the write plane; the seal is whose block is admissible.

## Why this is not a public RPC model

A public follower should:

1. Pull committed history from published peers (already possible without a token).
2. Serve reads to wallets (`/status`, `/block/...`, `/call`, …).
3. Accept a signed user transaction and **propagate** it toward a producer.

Step 3 fails today: `KindTx` requires `--rpc-auth-token`. Publishing that
token to every independent RPC publishes write-plane access (proposals,
votes, block ingest, peer announce). Keeping the token private means an
independent RPC cannot relay.

The same token admits `proposal` and `vote` to the transport. The engine
then counts a vote only if it is signed by a key in the active validator set,
and drops equivocations. So the token decides who may *deliver* consensus
messages, not whose vote counts. Membership grants no transport access: a
registered validator outside the token cluster cannot deliver its own vote.

## Required public model (later PR)

Not implemented in this track. After the modules/vm batches that own
`rpc/p2p_routes.go`, `network/*`, and `cmd/node/main.go` are idle:

1. **Token-free tx relay** — authenticate `KindTx` by the signed transaction
   plus the peer pin. A public RPC can propagate without the cluster token.
   Still one hop; still no re-relay storm.
2. **Membership-based consensus auth** — admit `proposal` / `vote` from
   on-chain validator identity, not from a shared `--rpc-auth-token`.
3. Keep committed-block pull as it is: sealed, pin-authenticated, not
   token-gated.
4. Keep anonymous HTTP reads on the RPC bind; do not require a cluster
   secret to query `/status` or `/block/0`.

Until that lands, an independent follower can **sync and serve reads**.
It cannot be a useful public submit gateway to a producer unless it is
inside the token cluster.

## Deferred (not this branch)

Public-peer authentication and token-free `KindTx` are protocol plus
security work. They need changes in `rpc/p2p_routes.go`, `network/*`, and
`cmd/node/main.go`, which this onboarding track must not edit. Removing
the write-plane token without a replacement would let strangers push
proposals, votes, blocks, and announces. This PR therefore documents the
current cluster-token model and does not implement a public relay.

## Related

- Follower bring-up: [`RPC_NODE.md`](RPC_NODE.md)
- Route inventory: [`RPC_API.md`](RPC_API.md)
- Validator rehearsal: [`VALIDATOR.md`](VALIDATOR.md)
