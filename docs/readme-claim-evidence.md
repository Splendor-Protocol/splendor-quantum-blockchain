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
| Five native module names are routed: QRC-20, QRC-1155, DEX, exchange, and bridge | Implemented and tested | `vm/module_router.go`, `vm/module_router_read.go`, `core/forks/forks.go`, `rpc/module_admission_test.go`, module tests under `vm/` | Each tier's live write surface depends on its persisted fork schedule. Other module files are not writable merely because they exist. |
| A QSol-subset compiler and `sqc-solc` CLI produce SQC-VM v4 bytecode and ABI/event metadata | Implemented and tested | `compiler/`, `cmd/sqc-solc/main.go`, `compiler/compiler_test.go`, `compiler/lang_test.go`, `compiler/loops_test.go`, `compiler/fleet_test.go` | Prototype subset, not a production-complete Solidity toolchain. The compiler currently emits `address` selectors while the SDK guard expects `sqaddress`. |
| X402 has ML-DSA authorization, transaction binding, payer-signed settlement, validity and balance checks, and on-chain nonce replay protection | Implemented and tested | `x402/x402.go`, `x402/x402_test.go`, `rpc/server.go`, `rpc/security_test.go`, `sdk/sqc-sdk/src/splendor-wallet.ts`, `sdk/sqc-sdk/src/x402/client.ts` | The current middleware verifies access but does not automatically settle. Deprecated `sdk/sqc-sdk.js` omits the required `settlement_tx`. Normal chain fees apply. |
| Phase 1 uses one authorized validator with an approximately two-second default production interval | Implemented; observed deployed | `consensus/validator.go`, `cmd/node/main.go`, genesis trust setup, public status snapshot below | This is single-authority finality, not decentralized economic or Byzantine finality. Validator key/host compromise is critical. |
| Multi-validator BFT is not wired into the running node | Source review | `consensus/bft.go`, `consensus/validator.go`, `cmd/node/main.go` | Future design work is not a current consensus capability. |
| The node scheduler is an adaptive statistical controller driven by mempool depth | Implemented and tested | `ai/scheduler.go`, scheduler tests under `ai/`, validator integration | It is not a neural model, general inference engine, or implemented decentralized AI network. The anomaly flag is reported but does not itself throttle traffic. |
| Approximately 2,605–2,901 TPS was measured through the tested block-production pipeline | Measured | [`docs/performance/2026-08-25-tps-gpu-experiment.md`](performance/2026-08-25-tps-gpu-experiment.md), `core/block/throughput_test.go` | One AWS host and specified batches. Not sustained public-network TPS, a multi-validator result, national-scale evidence, or the isolated GPU verification rate. |
| ACVP-vector subset tests exist for ML-DSA-65 and ML-KEM-768 | Tested | `core/crypto/acvp_test.go`, `core/crypto/testdata/acvp-vectors.json` | Passing selected vectors is not CAVP/CMVP validation or a FIPS 140-3 certificate. |
| Canonical SDK signer pins are release-consistent | Known issue | `sdk/sqc-sdk/src/config/sqc.networks.json`, `sdk/sqc-sdk/scripts/sync-config.mjs`, `sdk/sqc-sdk/wasm/dist/wasm_exec.js` | The committed `execSha256` is stale. A clean prebuild rewrites it from `4703f2…` to the artifact's `0c949f…`; this docs-only PR deliberately does not commit that code/config change. |
| The implementation has a published independent audit or formal cryptographic certificate | No evidence | No audit report, certificate identifier, CAVP/CMVP record, or FIPS 140-3 module boundary was found in the reviewed repositories | The README therefore states that these do not currently exist. |

## Public deployment snapshot

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
