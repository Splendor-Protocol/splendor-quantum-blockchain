# New validator joins a running producer — design (hypothesis)

Date: 2026-09-09. Base: `origin/main @ a74757c`. Status: **not implemented,
not built, not tested.** The authoring session could not invoke the Go
toolchain (every `go`/`gofmt`/`make` call was refused by the harness
permission mode), and the brief said to stop rather than write unbuilt code.
Every line number below refers to `a74757c`.

## 1. What is broken, confirmed from code

### 1.1 Why a fresh node cannot replay QC-less history

- Follower rule: `core/block/executor.go:1087-1142` (`verifyProducerAndQC`).
  With `multi_validator` active and the staking registry listing **2 or more**
  active validators, a block must carry `qc_root` and at least `2n/3+1` votes
  from active validators. With fewer than 2 active, no certificate is needed.
- The registry is read at the top of `ExecuteBlock` (line 242), before the
  block's transactions apply, so it is the parent's state. `vm/staking.go:82-133`
  (`Register`) sets the record Active immediately. The block **after** the one
  containing a second `validators.register` is the first one that needs a QC.
  Mainnet: register in 5785, QC demanded from 5786 on.
- Producer rule: none. `assembleBlock(persist=true)` (`executor.go:868-1045`,
  the `ProduceBlock` path used by `consensus.Validator`) never evaluates the
  certificate rule and stores the block directly. A single-path producer keeps
  sealing QC-less blocks after the set reaches 2. Each is canonical on the
  producer and unappliable everywhere else.
- Why the producer is on the single path although `genesis-production.json`
  schedules `multi_validator` at height 1: `cmd/node/main.go:747-748` decides
  `useBFT := Forks().Active(MultiValidator, head)` **once at boot** with the
  current head. On the boot that minted the chain, head is 0, and an activation
  at 1 is not active at 0 (`core/forks/forks.go:112-121`, `>=`). Nothing
  re-evaluates it. Only a restart at head >= 1 flips the node to BFT.
- Sync: `network/sync.go:372-434` (`SyncOnce`) applies `localH+1 ..` through
  `ExecuteBlock`. The first QC-less block with 2+ active returns an error and
  the 1 s loop retries the same height forever. There is no skip, override or
  checkpoint. Independently, `FetchBlock` (`sync.go:264-270`) and
  `FetchLatestBlock` (`300-306`) verify every block against the **single**
  pinned genesis key, so under a rotating BFT leader, blocks sealed by any other
  validator are refused before `ExecuteBlock` runs.
- A fresh `--validator` node cannot join at all today. On an empty chain it
  either mints its own block 0 (`main.go:681-723`, no `--genesis`) or refuses
  to start when `--genesis` names another validator (`main.go:687-697`). It
  never runs the pull-sync loop (`main.go:856`). Pushed blocks are verified
  against its **own** key (`main.go:427` ignores `--validator-pubkey-hex` for
  validators; `network/p2p.go:306-311`).
- BFT fallback: `consensus/multivalidator.go:56-71` (`RefreshSet`) uses the
  **local** node as the validator set when the registry is empty. Two BFT nodes
  on a chain whose genesis validator never registered each believe they are the
  sole leader and fork from each other.

### 1.2 Why a live LevelDB copy is unsafe

- Three independent goleveldb databases under `--data-dir` (`state`, `chain`,
  `txindex`; `main.go:340-353`) with no cross-database atomicity. The executor
  deliberately writes the chain head before the state commit
  (`executor.go:322-332`), so a copy taken between the two has a head ahead of
  state. `ReconcileState` repairs that only from an internally consistent chain
  DB.
- Inside one database, writes go to a journal (WAL), a MANIFEST and SST files
  that compaction rewrites and deletes concurrently. A file copy of an open
  database can reference SSTs deleted mid-copy or carry a half-written journal
  record. Open may fail, or silently drop the tail. `export-allocations`
  already documents the torn-view hazard (`main.go:194-196`,
  `core/state/statedb.go:283-289`).
- The state root is rebuilt from a full scan at open (`statedb.go:178-201`). A
  torn state DB yields a root that matches no header. The first applied block
  fails "state root mismatch" and the node wedges with nothing pointing at the
  copy.
- The data dir also holds `node_identity.key` (`main.go:1115`) and, on a
  validator, the wallet. A wholesale copy clones the P2P identity peers pin, and
  possibly the signing key.
- goleveldb `DB.GetSnapshot()` gives exactly the consistent point-in-time view a
  file copy cannot. State sync (3.4) is built on it.

## 2. Goal and non-goals

Goal: a fresh node joins a running producer **without stopping it**, reaches
the tip, then registers, votes and produces under BFT.

Non-goals: BFT liveness (round resynchronisation between nodes), slashing,
historical snapshots, paged snapshots for very large states, any change to
mainnet or to `genesis-production.json`.

## 3. Design: the smallest change, in five parts

Parts 3.1 to 3.3 are required for any join. Part 3.4 is required only when the
chain's history is unreplayable (mainnet today; devnet if it ever had 2+ active
validators while on the single path). Part 3.5 is the test list.

### 3.1 Producer picks BFT for the block it will produce

`cmd/node/main.go`, `core/block/executor.go`, `consensus/multivalidator.go`.

- `useBFT := Forks().Active(MultiValidator, head+1)`. A producer at head 0 on
  a chain with `multi_validator: 1` now runs BFT from block 1. With one active
  validator the engine self-commits: `NewVoteSet` needs `(1*2)/3+1 = 1` vote,
  `handleProposal` adds the local vote and commits. Blocks carry one vote and a
  `qc_root`. Followers with fewer than 2 active skip the certificate check and
  `Block.Validate()` still checks `qc_root` against the votes.
- Fail-closed guard on the single path. At the top of `assembleBlock`, when
  `persist` is true, `schedule().Active(MultiValidator, height)` holds and
  `ListActive()` has 2 or more entries, return `ErrQuorumRequired` before any
  state mutation. Message: "multi_validator: N active validators at height H,
  this node must run in BFT mode". The chain halts instead of forking, which is
  the mainnet-5786 failure mode made visible.
- `RefreshSet` fallback for an empty registry becomes the **genesis validator**
  (block 0's `validator_addr` and `validator_pubkey`, read once via
  `executor.GetBlockByHeight(0)`), not the local node. `consensus.Config` gains
  `GenesisValidator ValidatorInfo`.

### 3.2 Authenticate blocks against the active set, not one key

`network/sync.go`, `network/p2p.go`, `cmd/node/main.go`.

- `SyncConfig.LookupProducerKey func(addr qcrypto.Address) []byte` (nil keeps
  today's behaviour). `FetchBlock` and `FetchLatestBlock`: height 0 is always
  checked against the pinned key. Above 0: pinned key first; if that fails, the
  resolver's key for `Header.ValidatorAddr`, bound by
  `DeriveAddress(key) == addr`; no key means refuse.
- `HandleIncomingBlock(body, validatorPK, lookup, apply)` applies the same
  rule. Two call sites (`rpc/server.go:425`, `main.go:947`) and the tests in
  `network/p2p_security_test.go` change accordingly.
- Resolver in `main.go`:
  `vm.NewStakingContract(stateDB, vm.DeployStaking(nil)).GetValidator(addr)`,
  returning `PublicKey` only when `Status == Active`. It reads committed state,
  which for the block being fetched is the parent state, the same set
  `verifyProducerAndQC` uses. `ExecuteBlock` remains the authority: it still
  verifies the certificate.
- `--validator-pubkey-hex` is honoured for validators too. It names the trusted
  genesis key, which is not the local signing key. `trustedPK` is the flag when
  set, else the local key (today's behaviour). It feeds the broadcaster, the
  syncer and `network.HandleIncomingBlock`. `rpc.Config.ValidatorPK` keeps the
  local key so `/status.local_validator` is unchanged.
- Prior art: local branch `harness/poa-multivalidator-proof` adds
  `VerifyProducerBound` (verify against the embedded key bound to the header,
  leaving admission to the gate in `ExecuteBlock`). The registry resolver is
  preferred: it refuses unknown producers before apply and keeps pinned-key
  semantics for block 0 and single-producer chains.

### 3.3 Validators catch up before they propose

`cmd/node/main.go`, `consensus/multivalidator.go`.

- The 1 s peer-height poll plus `SyncOnce` loop also runs on validators, but
  only when `useBFT` is true. A single-path validator that pulled blocks would
  be a second producer.
- `consensus.Config.Ready func() bool` (nil means always ready).
  `MultiValidator.Run` skips `propose()` while `!Ready()`. Wired to
  `syncer.IsSynced` (best peer height <= local + 1; with no peers it reports
  synced). The engine still votes on peers' proposals; `VoteSet.AddVote`
  already rejects votes for another height.

### 3.4 State sync served live over the sealed read plane

Server side: `core/state/statedb.go`, `core/block/executor.go`,
`network/p2p_secure.go`, `rpc/p2p_routes.go`, `cmd/node/main.go`.

- `StateDB.ExportSnapshot(maxBytes int) (height uint64, leaves []KV, err error)`.
  Take `snap := db.GetSnapshot()`; read `meta:height` from `snap` (absent means
  "chain predates height markers, cannot serve a snapshot"); iterate every key
  except the `meta:` prefix; abort with `ErrSnapshotTooLarge` past `maxBytes`
  (64 MiB). `KV{Key, Value []byte}` marshals as base64 JSON.
- `Executor.ExportStateSnapshot() (*StateSnapshot, error)` returns
  `{Height, Block, Leaves}` with `Block = GetBlockByHeight(Height)`. The block
  is always present: the head is stored before the state commit.
- New read kind `KindGetStateSnapshot = "get_state_snapshot"` added to
  `IsReadKind`; `P2PConfig.ServeStateSnapshot func() ([]byte, error)`; a case
  in `serveSealedRead`. Reads are not token-gated, like `get_block`. The
  existing reply cap `maxSyncBodyBytes` (128 MiB) covers 64 MiB plus base64.

Client side: `network/sync.go`, `core/state/statedb.go`,
`core/block/executor.go`, `cmd/node/main.go`.

- `BlockFetcher.FetchStateSnapshot(peer) (*StateSnapshot, error)`: sealed
  query; `Block.Validate()`; signature by the **pinned trusted key**. The
  resolver cannot help here because local state is empty. If the head block was
  sealed by another validator, return `ErrSnapshotNotAnchored`; the caller
  retries next second, and leader rotation makes a later head the genesis
  validator's.
- `Syncer.StateSync(importer func(*StateSnapshot) error) error` tries peers in
  order.
- `StateDB.ImportSnapshot(leaves []KV, height uint64, wantRoot [32]byte) error`.
  Refuse unless the state is fresh (`len(s.leaves) == 0`, `!heightKnown`,
  empty pending layer). Compute `merkleRoot` over the leaves with the same
  construction `commit` uses; on mismatch write nothing. Otherwise one batch:
  every leaf plus `meta:height`; then update the in-memory index.
- `Executor.ImportStateSnapshot(snap *StateSnapshot) error`. Refuse unless the
  chain DB is empty. `storeBlock(snap.Block)` first (head before state, the
  executor's invariant), then `state.ImportSnapshot(...)` against
  `snap.Block.Header.StateRoot`, then `verifyProducerAndQC(snap.Block)` against
  the now-loaded validator set as defence in depth. A crash between the two
  writes leaves a head with no state marker; `ReconcileState` returns nil for
  that shape, so the documented recovery is "wipe the data dir and rerun state
  sync".
- Node flag `--state-sync` (bool). On an empty chain it replaces genesis
  allocation apply with: fork schedule and chain id from the pinned genesis
  document (local `--genesis` checked against `--genesis-doc-hash`, or the
  peer's `/genesis` with the same pin) through `persistForkSchedule`;
  `meta:native_minted` from `g.SumAllocations()`; then a loop that waits for
  the trusted key (`bootstrapValidatorPubKey` fills it when only
  `--genesis-hash` is given), calls `syncer.StateSync`, and retries every
  `RetryDelay`. The periodic `SyncOnce` loop is held off by an `atomic.Bool`
  until the import lands, then resumes from `H+1` under the normal rule.
  `--state-sync` without `--validator-pubkey-hex` or `--genesis-hash` fails at
  startup.

Trust model, unchanged in kind: the pinned genesis validator key authenticates
the head block; the header's `state_root` authenticates the state; the
certificate check re-verifies the head against the set the state carries. A
node with neither pin is refused, exactly as block sync is today.

### 3.5 Tests (write first, per package)

- `core/block`: `TestSinglePathRefusesWhenQuorumRequired` (registry with two
  active, fork at 1, `ProduceBlock` returns `ErrQuorumRequired`, state and head
  untouched). `TestExecuteBlockRejectsQCLessBlockWithTwoActive` (the hole a
  fresh node hits). `TestExecuteBlockAcceptsOneVoteQCFromSoleValidator`.
  `TestImportStateSnapshotRoundTrip` (export from A after three blocks, import
  into fresh B, B applies A's block 4). `TestImportStateSnapshotRefusesRootMismatch`,
  `...RefusesNonEmptyState`, `...RefusesNonEmptyChain`.
- `core/state`: `TestExportSnapshotIsConsistentUnderConcurrentCommit` (a commit
  races the export; the exported root equals the header at the exported height).
- `network`: `TestFetchBlockAcceptsActiveSetProducer`,
  `TestFetchBlockRejectsUnknownProducer`, `TestHandleIncomingBlockUsesResolver`,
  plus the existing fail-closed tests kept green.
- `rpc`: `TestSealedStateSnapshotRead` (mirrors `p2p_sealed_sync_test.go`).
- `consensus`: `TestRefreshSetFallsBackToGenesisValidator`,
  `TestMultiValidatorDoesNotProposeUntilReady`.
- `cmd/node`: `TestBFTChosenForNextHeight` for a small pure helper
  `bftForNextBlock(schedule forks.Schedule, head uint64) bool`.

Verification before any commit: `go build ./... && go vet ./... && gofmt -l .
&& go test -count=1 ./...`, then the local two-node run from
`docs/multi-validator-local.md` with `genesis-local.json`.

## 4. How to prove it on devnet

Devnet: chain id `splendor-quantum-devnet-1`, RPC
`https://quantum-rpc-devnet.splendor.org` (readme.md §tiers). Host access,
the write-plane token and the treasury key are in the workspace `infra/`
docs (`SERVICES.md`, `SECRETS.md`), which this session could not read.

### Step 0: read-only inspection, no restarts

```sh
R=https://quantum-rpc-devnet.splendor.org
curl -fsS $R/status | python3 -c 'import json,sys; s=json.load(sys.stdin); print({k:s.get(k) for k in ("height","multi_validator","validator_set_size","local_validator","node_version","commit","forks")})'
curl -fsS $R/block/latest | python3 -c 'import json,sys; b=json.load(sys.stdin); h=b["header"]; print("height",h["height"],"producer","SQ:"+bytes(h["validator_addr"]).hex(),"votes",len(b.get("votes") or []),"qc_root",bool(h.get("qc_root")))'
curl -fsS $R/validators
curl -fsS $R/block/0 | python3 -c 'import json,sys,base64; b=json.load(sys.stdin); print("genesis_hash",bytes(b["hash"]).hex()); print("validator_pubkey_hex",base64.b64decode(b["validator_pubkey"]).hex())'
```

Read the result against this table.

| Observation | Meaning | Consequence |
|---|---|---|
| `votes` is 0 at the tip | producer is on the single path | **producer restart required** (step 2); history is replayable only if `validator_set_size` never reached 2 |
| `votes` is 0 and `validator_set_size` >= 2 | mainnet-shaped: history unreplayable from the first block after the second register | new node must use `--state-sync` (3.4); a restart on the new binary halts production until a second voter is online, so resolve the registry first |
| `votes` >= 1 at the tip | producer already runs BFT | restart not strictly needed for the join; still recommended so both nodes run one binary |
| `/validators` empty | genesis validator never registered | with the old `RefreshSet` fallback every BFT node is its own sole leader: the genesis validator must `register` first, or the new node must run the 3.1 fallback fix |
| `/validators` lists a key nobody runs | quorum needs a voter that will never vote | that key's owner must `unregister` (32 blocks, then `finalize_exit`) before the producer restarts on BFT |

### Step 1: start the new node N as a follower

Fresh `--data-dir`. Reachable public URL (or `--allow-private-peers` on both
sides). Shared `--rpc-auth-token`, otherwise the producer never accepts N's
announce and never pushes proposals or votes to it.

```sh
sqc-node --data-dir /var/sqc/n2 --rpc-addr 0.0.0.0:8080 --public-url http://<N-host>:8080 \
  --peer https://quantum-rpc-devnet.splendor.org --chain-id splendor-quantum-devnet-1 \
  --genesis /etc/sqc/genesis-devnet.json --genesis-doc-hash <sqc-node genesis-hash /etc/sqc/genesis-devnet.json> \
  --genesis-hash <genesis_hash from step 0> --validator-pubkey-hex <validator_pubkey_hex from step 0> \
  --rpc-auth-token <token> [--state-sync]
```

Use `--state-sync` only in the mainnet-shaped row. Wait until N's `/status`
height tracks the producer's. Without `--genesis` on N, the producer must
serve `/genesis` (it was started with `--genesis`), and the pin is still
mandatory.

### Step 2: restart the devnet producer on the new binary (operator action)

Required when step 0 showed `votes` = 0. Not done by the agent. Same flags as
today; the new binary picks BFT for `head+1` and self-commits with one vote.
Verify: `/block/latest` now shows `votes` = 1 and `qc_root` = true, and N keeps
applying blocks (fewer than 2 active, so no certificate is demanded).

If the registry already lists 2 or more keys, production halts on restart
until a second voter is online. Resolve the registry (table above) before this
step.

### Step 3: restart N as a validator

Same data dir, add `--validator --wallet /etc/sqc/n2.sqwallet
--wallet-pass-file ...`, keep `--validator-pubkey-hex <genesis key>`. N runs
BFT (fork active at `head+1`), its set is `[genesis validator]`, it votes on
the producer's proposals and does not propose while behind.

### Step 4: fund and register N

From the devnet treasury, send N at least 2,500 SPLD plus fee, then:

```sh
SQC_WALLET_PASS=... sqc-call register --wallet /etc/sqc/n2.sqwallet --node https://quantum-rpc-devnet.splendor.org --chain-id splendor-quantum-devnet-1
```

(`sqc-call` is `/Users/nick/Workspace/splendor/sqc-become-validator/calltool`.)
From the next block the set is 2, quorum is 2, the leader alternates
`(height+round) % 2`.

### Step 5: pass criteria

For 50 consecutive blocks, sampled from both nodes every 2 s:

- both nodes return the same hash for every height;
- `validator_addr` alternates between the producer and N;
- every block carries 2 votes and a `qc_root`;
- neither log contains `block certificate rejected`, `proposal from non-leader
  rejected` or `commit callback failed`;
- N's `/status` reports `validator_set_size: 2` and `local_validator` = N.

### Step 6: fresh-join re-test

Start a third fresh node with the step 1 flags (with `--state-sync` if the
chain is mainnet-shaped) and confirm it reaches the tip and follows the
2-validator chain. This is the real proof that a newcomer can join without
anyone stopping.

## 5. What this design does not do

- It does not relax the certificate rule for history. QC-less blocks with 2+
  active validators stay unappliable by design; state sync is the join path for
  such chains, and a mainnet decision about its own history is separate.
- Deploying 3.1 to mainnet makes the producer run BFT with 2 active keys and
  halts it until the second key's node joins or that key unregisters. That is a
  mainnet operations decision, not part of this work.
- BFT round drift: rounds advance independently on timeout and only resync on
  commit, so a long stall with 2 validators can leave the nodes disagreeing on
  the leader. The proof sequence above (N synced and idle before the restart,
  register only after both run BFT) avoids it; it is not fixed here.
- Snapshots are single-shot and capped at 64 MiB. Paging is a follow-up.
