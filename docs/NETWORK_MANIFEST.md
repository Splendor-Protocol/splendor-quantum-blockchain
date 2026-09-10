# Network identity manifest

This file separates **immutable genesis identity** from the **mutable fork
schedule**. It records only values that are already independently verified
in-repo. Fields a public join still needs, but which this branch does not
publish as a release pin, are listed as required and unpublished.

Follower start: [`RPC_NODE.md`](RPC_NODE.md). API field names:
[`RPC_API.md`](RPC_API.md).

## Immutable genesis (verified)

These identify the current pre-public mainnet. They change only on a
reset. Update them through a verified network-identity decision, not by
copying whatever an untrusted peer says.

| Field | Value | Source |
| --- | --- | --- |
| Chain id | `splendor-quantum-1` | `/status.chain_id`; readme §20 |
| Genesis block hash (`--genesis-hash`) | `ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949` | `/status.genesis_hash` on a node that stores block 0; readme §20 |

Genesis **document** hashing and block-zero hashing are different
operations. This table pins block 0's header only.

Testnet's chain id (`splendor-quantum-testnet-1`) and block-0 hash live in
readme §20. They are a different chain. Devnet is not a join target for
this guide.

`genesis-production.json` in this repository is **not** mainnet's
document. Do not derive any pin from it.

## Required, unpublished

A verifying follower also needs the values below. This manifest does
**not** pin them. Fill them from a signed release / owner-published
manifest, then cross-check as in [`RPC_NODE.md`](RPC_NODE.md). Do not
invent a value here.

| Field | Flag / check | Why it is unpublished here |
| --- | --- | --- |
| Genesis document hash | `--genesis-doc-hash` (`sqc-node genesis-hash <file>`) | The live document is not the repo template; publishing a guessed hash would pin the wrong allocations |
| Bootnode URL | `--peer` | Public bootstrap endpoints are an operator/release decision |
| Bootnode identity hex | `--peer-pin URL=HEX` | The ML-DSA-65 `node_pubkey` (hex on the flag; base64 on `/p2p/identity`) must come from the same release decision |
| Image digest | container/runtime pin | `/status.image_tag` and `commit` name a build; a content digest is not recorded in this tree |

`--require-peer-pins` refuses trust-on-first-use once the identity hex is
known. Do not use `--trust-peer-genesis` for a mainnet join.

## Mutable: fork schedule

The fork schedule is **not** part of immutable genesis. It is the map
`/status.forks` (`name → activation height`) on a live node. Nodes that
disagree about an activation height do not fail at start-up — they split
at that height.

Read the schedule from `/status.forks` on an independently verified RPC
(readme §20 snapshots it). Do not copy a schedule from
`genesis-production.json`. Adopting a new fork on an existing chain is
`--allow-fork-extension` and is a network-wide decision, not a join-time
pin.

## What this file is not

- Not a hardware spec.
- Not a validator-admission announcement.
- Not a substitute for comparing a shared height's block `hash` and
  `header.state_root` after sync.
