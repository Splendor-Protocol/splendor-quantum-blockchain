# README Claim–Evidence Matrix

This file records the evidence used for the public `readme.md` truth pass. It
keeps source-code, test, benchmark, deployment, and certification claims
separate.

## Review scope

- Canonical repository: `Splendor-Protocol/splendor-quantum-blockchain`
- Code base reviewed: `9f9ffccd1d91a3d8eb5d4ec4aab7331b3e812b12`
- Public-network snapshot: `2026-09-02T08:47:25Z`
- Related repositories reviewed:
  - `Splendor-Protocol/splendor-infra` at `d0cb16c`
  - `Splendor-Protocol/splendor-quantum-explorer` at `204ab9b`
  - `Splendor-Protocol/splendor-quantum-wallet-extension` at `ba7a25a`

The original update brief named `therealdev101/splendor-quantum-blockchain`.
That is not this checkout's canonical remote; the `Splendor-Protocol`
repository above is the source reviewed and updated.

## Evidence levels

| Level | Meaning |
|---|---|
| Implemented | A reachable production code path exists in the reviewed source. |
| Tested | A repository test exercises the stated behavior. A passing test is not a deployment or certification claim. |
| Measured | A dated benchmark records a particular host, workload, and method. |
| Observed deployed | A public endpoint returned the recorded result at the snapshot time. This is not continuous monitoring. |
| Audited or certified | A named independent audit, CAVP/CMVP validation, or FIPS module certificate exists and is cited. |

## Material claim matrix

| Claim | Evidence level | Primary repository evidence | Qualification |
|---|---|---|---|
| Native account, transaction, block, and protocol signatures use ML-DSA-65 with separate contexts | Implemented and tested | `core/crypto/mldsa.go`, `core/crypto/mldsa_kat_test.go`, `core/types/transaction.go`, `core/types/block.go` | Applies to native ledger and implemented protocol signing paths, not every external system around the node. |
| ML-DSA-65 public, private, and signature sizes are 1,952, 4,032, and 3,309 bytes | Implemented and tested | `core/crypto/mldsa.go`, `core/crypto/mldsa_kat_test.go` | Expanded private-key size; a seed-derived wallet may back up the seed rather than the expanded key. |
| Addresses are `SQ:` plus the SHA3-256 digest of an ML-DSA-65 public key | Implemented and tested | `core/crypto/mldsa.go`, `core/crypto/mldsa_kat_test.go` | The digest is represented as 64 lowercase hex characters after the prefix. |
| Native transaction, block, Merkle, storage-slot, and state commitments use SHA3-256 | Implemented and tested | `core/types/transaction.go`, `core/types/block.go`, `core/state/statedb.go`, `core/state/merkle_state_test.go` | Browser WASM integrity pins use SHA-256 and are outside this native-ledger claim. |
| The state root covers accounts, contract code, contract storage, and X402 nonces | Implemented and tested | `core/state/statedb.go`, `core/state/merkle_state_test.go`, `core/state/statedb_security_test.go` | There is no public Merkle-proof endpoint and no historical-state query API. |
| Genesis is height zero, hashed, and validator-signed with ML-DSA-65 | Implemented and tested | `genesis/genesis.go`, `genesis/genesis_test.go`, `core/block/genesis_linkage_test.go`, `cmd/node/genesis_bootstrap_test.go` | A live tier's exact genesis remains deployment data and is recorded separately below. |
| Native P2P block push, announcement, and catch-up paths can be ML-KEM-768 sealed with AES-256-GCM | Implemented and tested | `network/pqchannel.go`, `network/p2p.go`, `network/sync.go`, `network/pqchannel_test.go`, `network/p2p_security_test.go`, `network/sync_security_test.go`, `rpc/p2p_sealed_sync_test.go` | Plaintext is refused by default but can be enabled explicitly with `--allow-plaintext-p2p`. |
| Production peer identity can be pinned | Implemented and tested | `network/p2p_secure.go`, `network/peer_pin_test.go` | Without configured pins and `--require-peer-pins`, first contact uses trust on first use. |
| Node-terminated TLS can offer `X25519MLKEM768` | Implemented and tested | `rpc/tls.go`, `cmd/node/main.go`, `rpc/security_test.go` | Classical X25519 fallback remains unless `--tls-require-hybrid` is set. A Cloudflare tunnel or other proxy negotiates its own client-facing TLS. |
| CLI wallet files use AES-256-GCM with Argon2id and current format version 2 | Implemented and tested | `core/crypto/wallet_store.go`, `core/crypto/wallet_store_test.go`, `cmd/wallet/main.go` | Legacy version 1 is handled deliberately. The browser extension is a separate implementation with seed/mnemonic backup. |
| Transfer, Deploy, Call, and X402 transaction types execute; Register is reserved | Implemented and tested | `core/types/transaction.go`, `core/block/executor.go`, relevant tests under `core/block/` | A type constant alone is not evidence of an application product. |
| SQC-VM bytecode deployment and execution exist | Implemented and tested | `vm/`, `core/block/executor.go`, `rpc/vm_rpc.go`, tests under `vm/` and `rpc/` | This is not EVM compatibility. |
| Six native module names are routed: QRC-20, QRC-1155, bridge, verify, validators, and QRC-721 | Implemented and tested | `vm/module_router.go`, `vm/module_router_read.go`, `core/forks/forks.go`, `rpc/module_admission_test.go`, module tests under `vm/` | Each write still needs its fork. DEX, exchange, lending, oracle, multisig, governance, and QRC-4626 are not routed. A leftover SDK adapter is not a native capability. |
| A QSol-subset compiler and `sqc-solc` CLI produce SQC-VM v4 bytecode and ABI/event metadata | Implemented and tested | `compiler/`, `cmd/sqc-solc/main.go`, `compiler/compiler_test.go`, `compiler/lang_test.go`, `compiler/loops_test.go`, `compiler/fleet_test.go` | Prototype subset, not a production-complete Solidity toolchain. Canonical ABI spelling is `address` (`4b40e901`); legacy `sqaddress` hashes as written (`14cde9ab`). Constructors may parse but do not execute. |
| X402 has ML-DSA authorization, transaction binding, payer-signed settlement, validity and balance checks, and on-chain nonce replay protection | Implemented and tested | `x402/x402.go`, `x402/x402_test.go`, `rpc/server.go`, `rpc/security_test.go`, `sdk/sqc-sdk/src/splendor-wallet.ts`, `sdk/sqc-sdk/src/x402/client.ts` | The current middleware verifies access but does not automatically settle. Deprecated `sdk/sqc-sdk.js` omits the required `settlement_tx`. Normal chain fees apply. |
| Phase 1 uses one authorized validator with an approximately two-second default production interval | Implemented; observed deployed | `consensus/validator.go`, `cmd/node/main.go`, genesis trust setup, public status snapshot below | This is single-authority finality, not decentralized economic or Byzantine finality. Validator key/host compromise is critical. |
| Multi-validator BFT is not wired into the running node | Source review | `consensus/bft.go`, `consensus/validator.go`, `cmd/node/main.go` | Future design work is not a current consensus capability. |
| The node scheduler is an adaptive statistical controller driven by mempool depth | Implemented and tested | `ai/scheduler.go`, scheduler tests under `ai/`, validator integration | It is not a neural model, general inference engine, or implemented decentralized AI network. The anomaly flag is reported but does not itself throttle traffic. |
| Approximately 2,605–2,901 TPS was measured through the tested block-production pipeline | Measured | [`docs/performance/2026-08-25-tps-gpu-experiment.md`](performance/2026-08-25-tps-gpu-experiment.md), `core/block/throughput_test.go` | One AWS host and specified batches. Not sustained public-network TPS, a multi-validator result, national-scale evidence, or the isolated GPU verification rate. |
| ACVP-vector subset tests exist for ML-DSA-65 and ML-KEM-768 | Tested | `core/crypto/acvp_test.go`, `core/crypto/testdata/acvp-vectors.json` | Passing selected vectors is not CAVP/CMVP validation or a FIPS 140-3 certificate. |
| Canonical SDK signer pins are release-consistent | Known issue at `9f9ffcc`; fixed by the single-signer change below | `sdk/sqc-sdk/src/config/sqc.networks.json`, `sdk/sqc-sdk/scripts/sync-config.mjs`, `sdk/sqc-sdk/wasm/dist/wasm_exec.js` | The committed `execSha256` is stale. A clean prebuild rewrites it from `4703f2…` to the artifact's `0c949f…`; this docs-only PR deliberately does not commit that code/config change. |
| The implementation has a published independent audit or formal cryptographic certificate | No evidence | No audit report, certificate identifier, CAVP/CMVP record, or FIPS 140-3 module boundary was found in the reviewed repositories | The README therefore states that these do not currently exist. |

## Public deployment snapshot

> **Historical.** The snapshot below is from `2026-09-02T08:47:25Z` and is kept
> as the record of that date. Every tier has been reset since, so the genesis
> block hashes here name chains that no longer exist; do not use them as
> `--genesis-hash` pins. The current identities (mainnet genesis
> `ba748263713642d619abb7c4b4d596cddc0d9429f296c438beacf3e656510949`, testnet
> `324d7f561493ae6d08062288cb99adfbb3f83297d352b783f611171251a1289f`) and the
> current schedules are in the README's tier table, dated 2026-09-09.

The following read-only checks were performed at
`2026-09-02T08:47:25Z`. Heights are point-in-time values and should increase.
All three nodes reported source commit
`2982523195d00d04d45d0ddf829b4c0af13c4826`, an ancestor of the reviewed
README base and therefore not the exact README revision.

| Tier | RPC and chain ID | Height | Genesis block hash | Genesis validator address | Reported forks | Peers |
|---|---|---:|---|---|---|---:|
| Mainnet | `https://quantum-rpc.splendor.org` — `splendor-quantum-1` | 324,705 | `80eb31e60b689f5dbb82ad3178b6f0bab9d768cb987dafc69607d05a25b083fc` | `SQ:442e905612311035b942f3362ea9d4efa632c1d2581d36356afd2f281afacf7e` | none reported | 0 |
| Testnet | `https://quantum-rpc-testnet.splendor.org` — `splendor-quantum-testnet-1` | 81,222 | `a52af7f86e77a50d3707452e7def8c7f4c46de9952597acaabfa8f1a5d76d775` | `SQ:09f44c419a48c70579b1a532c60e5786ecacf4de76e606d1901dfaf21e9745a9` | `module_revert_v1`, `qrc20_v2`, `qrc1155_v1`, `exchange_v1` at 1; `bridge_v1` at 11,000 | 0 |
| Devnet | `https://quantum-rpc-devnet.splendor.org` — `splendor-quantum-devnet-1` | 194,845 | `fc83aa50c5eb011af9b72e7a61007b96af2034492020a0ae0b55bc865dc1e300` | `SQ:4fd3394ce33d1e2e7787b74028126181a22b9e68ffd7ba5511bcc321a99d4b09` | `module_revert_v1`, `qrc20_v2`, `qrc1155_v1`, `exchange_v1` at 1; `vm_revert_v1` at 130,000 | 0 |

The block-zero responses contained base64 values that decode to a 1,952-byte
validator public key and a 3,309-byte signature. The public status response does
not expose the genesis-document hash, effective TLS key-exchange group,
plaintext-P2P flag, or peer-pin policy. Those properties cannot be inferred
from an HTTP 200 response. The reviewed infrastructure uses Cloudflare in front
of the node, so client-facing TLS is a separate deployment boundary.

At the same snapshot time, these explorer URLs returned HTTP 200:

- `https://quantum-explorer.splendor.org`
- `https://quantum-testnet-explorer.splendor.org`
- `https://quantum-devnet-explorer.splendor.org`
- `https://splendor-quantum-simnet.splendor.org`

HTTP 200 establishes reachability at that instant, not correctness, freshness,
availability SLOs, or production readiness.

### Reproduction commands

```bash
for base in \
  https://quantum-rpc.splendor.org \
  https://quantum-rpc-testnet.splendor.org \
  https://quantum-rpc-devnet.splendor.org
do
  curl -fsS "$base/healthz"
  curl -fsS "$base/status" | jq '{chain_id,height,peer_count,commit,forks}'
  curl -fsS "$base/block/0" | jq \
    '{height:.header.height,hash:.hash,validator:.header.validator_addr}'
done
```

The companion infrastructure repository should own a repeatable evidence
capture that adds HTTP status, timestamps, and decoded fingerprints without
changing network state.

## External primary references

- ML-KEM: [NIST FIPS 203](https://csrc.nist.gov/pubs/fips/203/final)
- ML-DSA: [NIST FIPS 204](https://csrc.nist.gov/pubs/fips/204/final)
- SHA-3: [NIST FIPS 202](https://csrc.nist.gov/pubs/fips/202/final)
- Ethereum account and validator keys:
  [ethereum.org](https://ethereum.org/developers/docs/consensus-mechanisms/pos/keys/)
- Ethereum proof of stake and 12-second slots:
  [ethereum.org](https://ethereum.org/developers/docs/consensus-mechanisms/pos/)
- Solidity compiler:
  [Solidity documentation](https://docs.soliditylang.org/en/latest/using-the-compiler.html)
- Solana signature fees:
  [Solana documentation](https://solana.com/docs/core/fees/fee-structure)
- Solana slot duration:
  [Solana documentation](https://solana.com/developers/cookbook/transactions/confirmation)
- Solana program execution:
  [Solana documentation](https://solana.com/docs/core/programs/program-execution)

The README makes no “first post-quantum blockchain” claim. A narrower “first”
claim also remains unpublished until a dated, maintained competitor matrix is
owned and reviewed.

## Claims removed or downgraded

- Absolute “quantum-proof,” “unbreakable,” and permanent-security guarantees
- “First post-quantum blockchain” and unsupported narrower “first” claims
- Claims that Ethereum cannot migrate or that other chains cannot support AI
  agents or micropayments
- Comparisons of a two-second block interval with decentralized finality
- Claims of 0% fees, guaranteed sub-two-second X402 settlement, or completed
  automatic settlement in either JavaScript middleware
- Claims that the QSol compiler is absent, and claims that it is already a
  production-complete toolchain
- Broad “exclusively post-quantum” wording that ignored AES-GCM, Argon2id,
  browser pins, TLS frontends, and the optional secp256k1-derived wallet route
- Public-network throughput, million-TPS, national-scale, and 40–60% scheduler
  improvement claims unsupported by the dated benchmark
- Production RTGS/CBDC, compliance-ready, audit, certification, universal
  regulatory-deadline, market-size, CRQC-timeline, and patent-protection claims
- The claim that no production explorer web UI exists; the separate explorer
  repository is built and its public tiers were reachable at the snapshot time

## Verification record

The README-only change must not alter chain behavior. Before merge, run these
commands on the exact commit and attach the GitHub Actions result:

```bash
go build ./...
go vet ./...
go test -count=1 ./...

(cd sdk/sqc-sdk && npm ci && npm test && npm run build)
```

The repository CI additionally rebuilds the committed signer WASM, compares it
with source, checks every network pin, and runs the signer golden vectors. The
result is evidence for the tested commit only; it is not an external security
audit or certification.

Review-time local results on `2026-09-02`:

- Go 1.25: `go build ./...`, `go vet ./...`, and
  `go test -count=1 ./...` passed.
- Canonical `sdk/sqc-sdk`: 109 tests passed and one live test was skipped;
  TypeScript build passed. Its prebuild exposed the stale committed
  `execSha256` pin described above, and those generated changes were excluded
  to keep this pull request documentation-only.
- `sdk/connect-react`: a clean `npm ci` did not reach its tests because the
  local-file `@splendor/pq-bridge` prepare step could not resolve its sibling
  `@splendor/sqc-sdk`. This packaging/install-order issue remains separate from
  the README change.
- Separate explorer repository: 90 tests passed and the production build
  completed. `npm audit` reported one existing high-severity dependency
  advisory; no dependency update was included here.
- Separate wallet-extension repair candidate: signer check and build passed;
  157 tests passed and one was skipped.

## Revision 2026-09-02: stack, endpoint and deployment sections

The README gained a "Start Here" page and sections 19–22 (every repository,
live networks and endpoints, how a release reaches a tier, repository layout).
This revision is documentation-only; no chain code changed. Base commit of this
repository: `d2ed9cfa9f66b6c0151c0e204b0588e8372a10ef` (CI green, run of
2026-09-02T12:33Z).

### Sibling repositories reviewed

| Repository | Commit | Date | Evidence used |
|---|---|---|---|
| `Splendor-Protocol/splendor-infra` | `d0cb16c` | 2026-09-01 | `README.md`, `.github/workflows/*`, `deploy/node/*`, `deploy/indexer/*`, `scripts/*`, `docs/chain-redeploy-runbook.md`, `config/environments.json`, GitHub environment variables (read-only) |
| `Splendor-Protocol/splendor-quantum-explorer` | `c44d8f0` | 2026-09-01 | `README.md`, `wrangler.jsonc`, `scripts/deploy.mjs`, `.github/workflows/release.yml`, `lib/chain/*`, `lib/pq/*`, `lib/connect/*`, git tags |
| `Splendor-Protocol/splendor-quantum-wallet-extension` | `0456f6b` | 2026-09-02 | `manifest.json`, `REVIEW.md`, `inject.js`, `lib/networks.js`, `scripts/sync-signer.mjs`, both workflows |
| `Splendor-Protocol/wallet` | `269154d` | 2026-08-31 | `src/quantum/README.md`, `src/quantum/*.ts`, `src/utils/splendorConnectService.ts`, `server/src/connect/*`, `server/src/routes/connect.ts`, `package.json`, `README.md` |
| `Splendor-Protocol/splendor-quantum-dapps` | `6f6dd95` | 2026-09-01 | `README.md`, `wrangler.jsonc`, `src/bridge/*`, `.github/workflows/deploy.yml` |
| `Splendor-Protocol/splendor-quantum-faucet` | `0d48b99` | 2026-08-31 | `README.md`, `wrangler.jsonc`, `src/*` |
| `Splendor-Protocol/splendor-smartcard` | `a8287f7` | 2026-08-31 | `README.md`, `docs/signing-card-feasibility.md`, `applet/`, `sim/` |

### Public deployment snapshot, 2026-09-02T13:03Z–13:06Z

Read-only `GET`s from a laptop. Heights are point-in-time.

| Tier | `/status` | Indexer `/idx/v1/health` and `/stats` |
|---|---|---|
| Mainnet `splendor-quantum-1` | height 332,398 at 13:03:43Z; `image_tag v0.6.1`; commit `2982523195d00d04d45d0ddf829b4c0af13c4826`; no `forks`; `peer_count 0`; `mempool_size 0` | checkpoint 332,460; 0 transactions indexed; no `build` field (image predates the build stamp; variable says `sqc-indexer:v0.2.11`) |
| Testnet `splendor-quantum-testnet-1` | height 88,913 at 13:03:40Z; `v0.6.1`; commit `2982523…`; forks `module_revert_v1`, `qrc20_v2`, `qrc1155_v1`, `exchange_v1` at 1, `bridge_v1` at 11,000; `peer_count 0` | checkpoint 88,979; 21,958 transactions (21,933 transfers, 25 calls); variable says `v0.2.12` |
| Devnet `splendor-quantum-devnet-1` | at 13:03:37Z still `v0.6.1` / `2982523…` at height 202,534 (the `v0.6.2` rollout ran 13:03:42Z–13:04:32Z); at 13:05:56Z `/healthz` reported `v0.6.2`, commit `d2ed9cfa9f66b6c0151c0e204b0588e8372a10ef`, uptime 102 s, and `release:status` read height 202,554 with that commit; forks as testnet minus `bridge_v1`, plus `vm_revert_v1` at 130,000; `peer_count 0` | checkpoint 202,597; 50,254 transactions (49,789 transfers, 284 deploys, 181 calls); `build` `v0.2.14` |

Genesis block hashes and validator addresses re-fetched from `/block/0` at the
same time and unchanged from the 08:47Z snapshot above. The `heartbeat`
container in `splendor-infra/deploy/node` sends periodic demo transfers where
enabled, which accounts for the steady transfer counts on devnet and testnet.

Other services, same window:

| URL | Result |
|---|---|
| `https://quantum-explorer.splendor.org`, `…-testnet-explorer…`, `…-devnet-explorer…`, `https://splendor-quantum-simnet.splendor.org` | HTTP 200; the explorer repository's tags `v0.0.31-{prod,test,devnet,dev}` all point at `c44d8f0`, and each tier's footer reported the node build recorded above |
| `https://quantum-faucet.splendor.org/` and `/health` | HTTP 200; `{"ok":true,"service":"quantum-faucet"}` |
| `https://dapps.splendor.org/health` | HTTP 200; `{"ok":true,"service":"splendor-dapps"}` |
| `https://dapps.splendor.org/bridge/api/info` | HTTP 200; burn contract `0xe822e805dd83a6c0e6761cb3c743bb3ff272f1de` on chain `0xa83` (2691), SPLV `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689`, 120 confirmations, quantum network `testnet`, bridge `SQ:643d2c15cbc0cf922c2054243d0724afd0073d0c1169b981967d81b4c9aabee6`, pool balance 5 SPLD, nothing claimed, not paused |
| `https://wallet-api.splendor.org/health` | HTTP 200; `{"ok":true}`; `/` answers 401 |
| `https://quantum-rpc*.splendor.org/metrics` | HTTP 404 on all three tiers; metrics are on the loopback `--metrics-addr` listener |

Release records consulted: `gh release list` on this repository (`v0.6.2` published
2026-09-02T13:03:32Z), the GitHub Deployments on this repository (`devnet v0.6.2`
2026-09-02T13:03:36Z, `prod v0.6.1` and `test v0.6.1` 2026-09-01), and the
`splendor-infra` Actions history (`Build sqc-node image` 12:56:46Z–13:03:26Z,
`Deploy quantum infrastructure` 13:03:42Z–13:04:32Z on 2026-09-02). The
2026-08-31 deploy run `33418702359` logged
`SQC_ALLOW_FORK_EXTENSION=true` with the schedule that adds `bridge_v1` at
11,000, which is the evidence for the fork-extension statement in §21.

### Sibling test results recorded for this revision

| Repository | Command | Result |
|---|---|---|
| `splendor-quantum-wallet-extension` @ `0456f6b` | `npm ci && npm test` (2026-09-02T13:19Z) | 13 files, 157 passed, 1 skipped |
| `splendor-quantum-dapps` @ `6f6dd95` | `npm test` | 7 files, 95 passed |
| `splendor-quantum-faucet` @ `0d48b99` | `npm test` | 5 files, 151 passed (README says 79; stale) |
| `wallet` @ `269154d` | `npx jest __tests__/quantum __tests__/mldsa __tests__/splendorConnectService` (and `__tests__/blockpeakCredits`) | 16 suites, 163 passed; 182 with the 19 Blockpeak Credits tests (subset; full suite not run) |
| `splendor-infra` @ `d0cb16c` | `scripts/test-release-contract.sh`, `scripts/test-entrypoint-forks.sh`, `go test ./...` in `deploy/indexer` | passed; 24 indexer tests |
| `splendor-smartcard` @ `a8287f7` | `./sim/run.sh` | jCardSim proof, 61/61 checks |
| `splendor-quantum-explorer` @ `c44d8f0` | CI `Verify explorer`, run `33508812173` (2026-09-01) | 90 passed |

### Claims the sibling repositories' own documents make that this README does not repeat

- Explorer README and the internal stack overview describe block verification
  as happening "in your browser". The code runs it in the Cloudflare Worker
  (server-side React server components); the browser bundle deliberately
  excludes the verifier. The README says Worker-side.
- Explorer README's tier table lists three tiers; the config, scripts and
  release workflow support four (devnet included). The README lists four.
- `splendor-infra` README says two environments; there are three.
- Faucet README says 79 tests and a one-day cooldown; the suite has 151 and
  the deployed cooldown is 30 minutes.
- Mobile wallet README describes only the EVM wallet and Android; the quantum
  section and the iOS TestFlight path exist in code and scripts. No document
  states an App Store or Play Store listing, so none is claimed.
- Smartcard README documents `Blob4KApplet`; `main` builds `VaultApplet`,
  proven in simulation only. The README describes the card as encrypted
  storage that does not sign, per `docs/signing-card-feasibility.md`.
- The internal stack overview of 2026-09-01 says the compiler is "designed,
  not built" and the extension is `v1.1.0`; both are stale against the code
  (compiler prototype in `compiler/`, extension `v1.3.0`).

### Repository hygiene noted, not changed in this documentation revision

Compiled binaries are tracked in git at the top level of this repository
(`sqc-node`, `sqc-wallet`, `sqc-deploy` for arm64 macOS; `node`, `wallet` for
x86-64 Linux) together with `qc`, a captured `git log`. `splendor-infra` tracks
`deploy/indexer/sqc-indexer` and `tools/sqvm-kit/sqvm-kit`. The SDK's committed
`execSha256` pin remains stale as recorded above. These are separate,
non-documentation changes.

### Single signer, 2026-09-02

The Go signer package, its WASM build and the committed artifact now live only
under `sdk/sqc-sdk` (`signer/`, `wasm/`); `sdk/bridge` is a re-export shim with
no artifact. Moving the Go import path changes the bytes, so the signer was
rebuilt under the pinned `go1.25.0` toolchain: `sqc_signer.wasm`
`d7cb784749d170af758e8de0a29903797c203d6fa26f50efdda15f9681002ad7` (was
`39c48add…`), `wasm_exec.js` unchanged at `0c949f49…`. A second build produced
the same digest. `npm run sync:config` rewrote every network pin, which also
cleared the stale `execSha256` recorded above. CI's signer-drift job now
rebuilds and checks `sdk/sqc-sdk`. Local results: `go build ./...`,
`go vet ./...`, `go test ./sdk/... ./cmd/loadtest/...` passed; `sdk/sqc-sdk`
109 Vitest tests passed and 1 skipped; the browser extension re-pinned from the
new artifact passed 157 tests with 1 skipped.
