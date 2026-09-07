# Splendor Quantum Chain

### Post-quantum account and validator cryptography from genesis.

> **Splendor Quantum Chain is a new Layer-1 whose native accounts,
> transactions, blocks, and protocol authorizations use ML-DSA-65 with
> purpose-specific signing contexts. Addresses and ledger commitments use
> SHA3-256. Native peer traffic can be sealed using ML-KEM-768-established
> secrets and AES-256-GCM. This is not an ECDSA-ledger migration, but it is also
> not a guarantee against every future attack: security depends on the current
> assumptions of those algorithms, this implementation, and its operation.**

---

## Start Here — The Stack in One Page

This repository is the chain, and it is the entry point for everything built
around the chain. If you only read one page, read this one; the detail is in
[§19](#19-the-splendor-quantum-stack--every-repository) (every repository),
[§20](#20-live-networks-and-endpoints) (live networks and endpoints),
[§21](#21-how-a-release-reaches-a-tier) (deployment) and
[§22](#22-repository-layout-and-developer-quick-start) (this repository's
layout and quick start).

### The repository map

Eight repositories, all under `github.com/Splendor-Protocol`. In plain words:

```
Splendor-Protocol/
│
├── splendor-quantum-blockchain        ← YOU ARE HERE. The chain itself: the rulebook and
│   │                                    the ledger, written in Go. Everything else is
│   │                                    built around it.
│   ├── cmd/                            the node, the command-line wallet, the QSol
│   │                                   compiler, the contract deploy tool, the bridge
│   ├── sdk/                            the JavaScript/TypeScript SDK and the WASM signer
│   │                                   that websites and the browser wallet use
│   └── scripts/release.mjs             the one command that ships a new node version
│
├── splendor-infra                     The building the chain runs in: the AWS servers,
│                                      the deploy pipeline, backups. Nobody logs in by
│                                      hand; GitHub Actions does it.
│
├── splendor-quantum-explorer          The public window: every block and transaction,
│                                      with each block's signature re-checked before it
│                                      is shown.
│
├── splendor-quantum-wallet-extension  The browser wallet (Chrome). Holds your keys and
│                                      lets a website ask you to sign, like MetaMask.
│
├── wallet                             The phone app (iOS and Android). Holds your keys.
│                                      Also hosts the QR-login service the explorer,
│                                      faucet and dapps use ("Splendor Connect").
│
├── splendor-quantum-dapps             The front door at dapps.splendor.org: the portal
│                                      and the bridge that moves old SPLD/SPLV onto the
│                                      quantum chain.
│
├── splendor-quantum-faucet            Free test coins on testnet and devnet.
│
└── splendor-smartcard                 The NFC card that stores an encrypted backup of
                                       your key. It stores; it does not sign.
```

Who holds keys: only the phone app and the browser extension hold *your* keys,
on your device. The faucet holds its own test-coin key. The explorer, the
portal, the bridge page and the login service hold no keys at all and can never
move funds. The chain is the only place the ledger lives.

If you want to…

| …do this | go here |
|---|---|
| understand what the chain is and is not | the rest of this file, starting with [What Is and Is Not Post-Quantum](#what-is-and-is-not-post-quantum) |
| run your own node or validator | [`DEPLOY.md`](DEPLOY.md) |
| ship a new node version to devnet, testnet or mainnet | [§21](#21-how-a-release-reaches-a-tier) |
| build a website or app that talks to the chain | [`sdk/sqc-sdk/README.md`](sdk/sqc-sdk/README.md), then [§19](#19-the-splendor-quantum-stack--every-repository) |
| write a smart contract | [§8](#8-smart-contracts--sqc-vm-and-qsol-prototype) and [`compiler/README.md`](compiler/README.md) |
| look at blocks and transactions | the explorers in [§20](#20-live-networks-and-endpoints) |
| get test coins | `https://quantum-faucet.splendor.org` |
| move old SPLD or SPLV to the quantum chain | `https://dapps.splendor.org/bridge` (testnet destination today) |
| find every URL and endpoint that is live | [§20](#20-live-networks-and-endpoints) |
| know what is built, tested, measured, deployed, or only planned | [§16](#16-what-is-built-vs-what-comes-next) |

Related but separate: the original Splendor chain (`explorer.splendor.org`,
`mainnet-rpc.splendor.org`, chain 2691) is a different network; the bridge is
the only place the two touch. `quantum.splendor.org` is the marketing site.

### How the pieces connect

```
     HOLD KEYS AND SIGN (on the user's device)         HOLD NO USER KEYS (read, relay, drip)
   ┌──────────────────────┐ ┌──────────────────────┐   ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐
   │ Mobile wallet        │ │ Browser extension    │   │ Explorer   │ │ Dapps +    │ │ Faucet     │ │ Connect    │
   │ `wallet`             │ │ `…-wallet-extension` │   │`…-explorer`│ │ bridge     │ │ `…-faucet` │ │ gateway    │
   │ iOS / Android        │ │ Chrome MV3           │   │ verifies   │ │ `…-dapps`  │ │ test SPLD  │ │ `wallet/   │
   │ + `@splendor/sqc-sdk`│ │ Go WASM signer from  │   │ every      │ │ reads      │ │ from its   │ │ server`    │
   │   (`sdk/` here)      │ │ `sdk/` here          │   │ block      │ │ only       │ │ own key    │ │ QR login   │
   └──────────┬───────────┘ └──────────┬───────────┘   └─────┬──────┘ └─────┬──────┘ └─────┬──────┘ └────────────┘
              └────────────┬───────────┘                     └─────────────┼─────────────┘
                           │ signed transactions                           │ reads
   ┌───────────────────────▼───────────────────────────────────────────────▼─────────────────────────────────────┐
   │  SPLENDOR QUANTUM CHAIN — this repository — `sqc-node`, one ledger per tier                                 │
   │  devnet · testnet · mainnet · an ML-DSA-65-signed block about every 2 s                                     │
   └──────────────────────────────────────────────┬──────────────────────────────────────────────────────────────┘
                                                  │ runs on
   ┌──────────────────────────────────────────────▼──────────────────────────────────────────────────────────────┐
   │  `splendor-infra` — one AWS Graviton host per tier: node + indexer + Postgres, behind a                     │
   │  Cloudflare Tunnel · GitHub Actions + OIDC · shipped by `npm run deploy:<tier>` from this repo              │
   └─────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```
### The repositories, in detail

| Repository | What it is | Built with | Live at | Ships by |
|---|---|---|---|---|
| [`splendor-quantum-blockchain`](https://github.com/Splendor-Protocol/splendor-quantum-blockchain) — **this repo** | The chain: node, CLI wallet, SQC-VM and native modules, QSol compiler, X402, sealed P2P, bridge relayer, TypeScript SDK and WASM signer | Go 1.25, TypeScript | `quantum-rpc.splendor.org`, `-testnet`, `-devnet` | `npm run deploy:<tier>` (§21) |
| [`splendor-infra`](https://github.com/Splendor-Protocol/splendor-infra) | Infrastructure as code and the release pipeline for the chain hosts and the indexer | OpenTofu, GitHub Actions (OIDC), docker compose, Cloudflare Tunnel | the three RPC hosts above | `Build sqc-node image` → `Deploy quantum infrastructure` workflows, driven by this repo's release script |
| [`splendor-quantum-explorer`](https://github.com/Splendor-Protocol/splendor-quantum-explorer) | Block explorer for native `SQ:`/SHA3/ML-DSA-65 types; re-verifies every block signature in its Worker | React 19 on Vite (vinext), Cloudflare Workers | `quantum-explorer.splendor.org`, `quantum-testnet-explorer…`, `quantum-devnet-explorer…`, `splendor-quantum-simnet…` | git tag `vX.Y.Z-{prod,test,devnet,dev}` via `npm run deploy:<env>` |
| [`splendor-quantum-wallet-extension`](https://github.com/Splendor-Protocol/splendor-quantum-wallet-extension) | Chrome MV3 wallet `v1.3.0`: ML-DSA-65 keys from a 32-byte seed, multi-account, `window.splendor` provider for dapps, QRC-20 | Vanilla JS + this repo's Go WASM signer | not on a store: load unpacked or `npm run zip` | `npm run build` (syncs the signer from this repo) |
| [`wallet`](https://github.com/Splendor-Protocol/wallet) | Splendor mobile wallet `4.17.8` (legacy chain 2691) with a Quantum Chain section; also hosts the Splendor Connect gateway | React Native + `@noble/post-quantum`; Node/Hono server | gateway at `wallet-api.splendor.org`; app via TestFlight / Play Console | `npm run ios:testflight`; gateway via Coolify |
| [`splendor-quantum-dapps`](https://github.com/Splendor-Protocol/splendor-quantum-dapps) | Dapps portal and the SPLD/SPLV → Quantum migration bridge UI and read-only API | TypeScript Cloudflare Worker | `dapps.splendor.org`, `…/bridge` | `npm run deploy` or the `Deploy` workflow |
| [`splendor-quantum-faucet`](https://github.com/Splendor-Protocol/splendor-quantum-faucet) | Test-SPLD faucet for testnet and devnet; signs real ML-DSA-65 transfers in the Worker | TypeScript Cloudflare Worker, KV, Turnstile | `quantum-faucet.splendor.org` | `npm run deploy` (by hand) |
| [`splendor-smartcard`](https://github.com/Splendor-Protocol/splendor-smartcard) | JavaCard applet and tooling for the NFC Splendor Card: encrypted seed backup, no on-card signing | JavaCard, Java, GlobalPlatform | hardware | `mvn package`, `./gp-card --install` |

### The networks

Three chains, three ledgers, one codebase. New releases land on devnet first,
are promoted unchanged to testnet, then to mainnet (§21).

| | Devnet | Testnet | Mainnet |
|---|---|---|---|
| Chain id | `splendor-quantum-devnet-1` | `splendor-quantum-testnet-1` | `splendor-quantum-1` |
| RPC | `https://quantum-rpc-devnet.splendor.org` | `https://quantum-rpc-testnet.splendor.org` | `https://quantum-rpc.splendor.org` |
| Explorer | `https://quantum-devnet-explorer.splendor.org` | `https://quantum-testnet-explorer.splendor.org` | `https://quantum-explorer.splendor.org` |
| Faucet | yes | yes | none |
| Migration bridge | — | live (`bridge_v1` at height 11,000) | not activated |
| Node observed 2026-09-02 | `v0.6.2` | `v0.6.1` | `v0.6.1` |

`curl -s <rpc>/status` answers with chain id, height, commit, image tag and
fork schedule; §20 has the full endpoint list and the dated snapshot.

### Shipping the node

```bash
npm run release:status       # what each tier runs, from the tier's own /status
npm run deploy:devnet        # tag → build arm64 image → set the tier's image variable
                             # → SSM rollout → verify /status reports the new commit
npm run deploy v0.6.2 test   # promote that exact image to testnet, then prod
```

The release script never resets a chain and never applies infrastructure; it
refuses a dirty tree, an unpushed commit, a no-op rebuild, and a
non-interactive mainnet release. §21 walks through one real run.

---

## What Is and Is Not Post-Quantum

Read this before the rest of the document. The strongest defensible claim is
specific: every native on-chain public-key signature uses ML-DSA-65, native
ledger commitments use SHA3-256, and the implemented peer protocol uses
ML-KEM-768 plus AES-256-GCM. Deployment, frontend, identity-pinning, and
certification boundaries are separate claims.

### Post-quantum today

| Surface | Mechanism | Where in the code |
|---|---|---|
| Transaction, block, X402, vote, oracle, permit and multisig signatures | ML-DSA-65 ([NIST FIPS 204](https://csrc.nist.gov/pubs/fips/204/final)) via CIRCL, each with its own domain-separation context | `core/crypto/mldsa.go` |
| Addresses | `SQ:` + hex(SHA3-256(ML-DSA-65 public key)) | `core/crypto/` |
| Transaction hashes, block hashes, Merkle nodes, contract storage slot keys | SHA3-256 ([NIST FIPS 202](https://csrc.nist.gov/pubs/fips/202/final)) | native ledger paths |
| Node-to-node block push, peer announce, **and catch-up sync** | ML-KEM-768 ([NIST FIPS 203](https://csrc.nist.gov/pubs/fips/203/final)) + AES-256-GCM; plaintext refused unless `--allow-plaintext-p2p` | `network/pqchannel.go`, `network/sync.go` |
| RPC/WebSocket transport **on the hop the node terminates itself** | TLS 1.3 can offer `X25519MLKEM768`; `--tls-require-hybrid` is required to refuse classical fallback | `rpc/tls.go`, `cmd/node/main.go` |
| Wallet file at rest | AES-256-GCM with an Argon2id-derived key, minimum parameters enforced at startup | `core/crypto/wallet_store.go` |

### Not post-quantum

- **TLS the node does not terminate.** A reverse proxy in front of the node —
  nginx with Let's Encrypt, a CDN, a tunnel — negotiates its own connection with
  the client, which today means classical ECDHE. The hybrid group applies only
  to a connection the node serves directly via `--tls-cert`. Every deployment
  that terminates TLS at a proxy has a classical key exchange in front of it.
- **An unpinned first P2P contact.** P2P payloads are sealed by default, but an
  unpinned peer identity is initially accepted using trust on first use.
  Authenticated production clusters should supply `--peer-pin` values and set
  `--require-peer-pins`.
- **The browser SDK's MetaMask derivation path.** `sdk/sqc-sdk/src/derive.ts`
  derives the PQ key from a secp256k1 signature. The key's *use* is
  post-quantum; its *origin* is not, because anyone who can break secp256k1 can
  reproduce it. The SDK says so at the call site. It is a convenience, not a
  security boundary.
- **WASM integrity pinning in the browser.** The signer bundle is pinned by
  SHA-256 digest, because that is what Web Crypto offers. Not a break, but not
  part of the NIST PQ suite the rest of the chain sticks to.
- **Everything outside this repository.** DNS, the operator's SSH, the CI that
  builds the binary, the browser that reaches the RPC endpoint.

### Conformance, stated precisely

`core/crypto/acvp_test.go` runs a subset of published NIST ACVP vectors
for ML-DSA-65 and ML-KEM-768, so the implementation matches what NIST specified
rather than merely matching its own past output. That is a conformance test, not
a validation certificate: there is no CAVP/CMVP submission, no FIPS 140-3 module
boundary, and no third-party audit of this codebase. "Passes the published
vectors" and "certified" are different claims, and only the first is made here.

---

## Table of Contents

1. [What We Are Building and Why](#1-what-we-are-building-and-why)
2. [Migration Tradeoffs for Existing Blockchains](#2-migration-tradeoffs-for-existing-blockchains)
3. [The New Architecture — Quantum Native From Block Zero](#3-the-new-architecture--quantum-native-from-block-zero)
4. [Addresses — How They Work in a Quantum Chain](#4-addresses--how-they-work-in-a-quantum-chain)
5. [Wallets — Creating One, Using One, Securing One](#5-wallets--creating-one-using-one-securing-one)
6. [Full User Journey — From Zero to Sending Funds](#6-full-user-journey--from-zero-to-sending-funds)
7. [Transactions — How They Are Created, Signed, Broadcast, Recorded](#7-transactions--how-they-are-created-signed-broadcast-recorded)
8. [Smart Contracts — SQC-VM and QSol Prototype](#8-smart-contracts--sqc-vm-and-qsol-prototype)
9. [Consensus — Phase 1 Single-Authority Finality](#9-consensus--phase-1-single-authority-finality)
10. [Record Keeping — The Quantum Ledger](#10-record-keeping--the-quantum-ledger)
11. [X402 — Native Payment Protocol](#11-x402--native-payment-protocol)
12. [The Ecosystem — DApps, DeFi, NFTs, AI Agents](#12-the-ecosystem--dapps-defi-nfts-ai-agents)
13. [Cryptographic Primitives Used (Technical Reference)](#13-cryptographic-primitives-used-technical-reference)
14. [Architecture Comparison — Not a Finality Benchmark](#14-architecture-comparison--not-a-finality-benchmark)
15. [Business Claims and Evidence Boundary](#15-business-claims-and-evidence-boundary)
16. [What Is Built vs What Comes Next](#16-what-is-built-vs-what-comes-next)
17. [Adaptive Scheduler](#17-adaptive-scheduler)
18. [Banking, Settlement, and Compliance Boundary](#18-banking-settlement-and-compliance-boundary)
19. [The Splendor Quantum Stack — Every Repository](#19-the-splendor-quantum-stack--every-repository)
20. [Live Networks and Endpoints](#20-live-networks-and-endpoints)
21. [How a Release Reaches a Tier](#21-how-a-release-reaches-a-tier)
22. [Repository Layout and Developer Quick Start](#22-repository-layout-and-developer-quick-start)

---

## 1. What We Are Building and Why

Splendor Quantum Chain is a new Layer-1 built around post-quantum account and
validator keys from genesis. It avoids the legacy-account migration problem of
an ECDSA-ledger retrofit, while retaining conventional symmetric cryptography
where it is appropriate.

The native design uses:

- **Accounts** are derived from post-quantum keys, not ECDSA
- **Signatures** use ML-DSA-65 (NIST FIPS 204), not ECDSA
- **Hashing** uses SHA-3-256 (SHA3-256), not SHA-256, SHAKE-256, or Keccak-256
- **Native P2P session establishment** uses ML-KEM-768 (NIST FIPS 203)
- **Contract deploy and call transactions** use native ML-DSA-65 account
  signatures, while contract code and storage participate in SHA3-256 state
  commitments
- **Implemented validator protocol messages** use purpose-specific ML-DSA-65
  signatures
- **Block headers** are hashed with SHA-3
- **Node-to-node block push, peer announce, and catch-up sync** are sealed with
  ML-KEM-768 + AES-256-GCM by default. Plaintext requires
  `--allow-plaintext-p2p`; configured pins plus `--require-peer-pins` are needed
  to replace first-contact trust with operator-supplied peer identity

The chain is designed to resist cryptographically relevant quantum attacks
under the current security assumptions of ML-DSA, ML-KEM, SHA3-256, AES-256,
and the implementation surrounding them. No claim is made that Splendor is the
first post-quantum blockchain.

---

## 2. Migration Tradeoffs for Existing Blockchains

### Why retrofitting a mature ledger is difficult

People often ask: "Can't Ethereum just upgrade to quantum-safe cryptography?"

It can, but the migration is a large protocol, custody, application, and
governance project.

**Every Ethereum address is derived like this:**
```
address = last_20_bytes(keccak256(ecdsa_public_key))
```

Mature chains can introduce new signature schemes, account abstraction,
migration contracts, or protocol upgrades. They still have to coordinate users,
custodians, applications, and assets that depend on existing keys and account
semantics. Splendor makes a different tradeoff: post-quantum account keys are
native, so it has no legacy ECDSA account base to migrate. Splendor is not
EVM-compatible, and avoiding that migration cost also means giving up Ethereum
compatibility.

---

## 3. The New Architecture — Quantum Native From Block Zero

The chain is structured in four layers. Native public-key operations use the
post-quantum mechanisms described below; symmetric encryption, password KDFs,
TLS certificates, browser integrity hashes, and optional compatibility routes
remain within their stated boundaries.

```
┌──────────────────────────────────────────────────────────────────┐
│  APPLICATION LAYER                                                │
│  DApps · DeFi · NFTs · AI Agents · X402 Payments                │
│  (native transactions use ML-DSA-65 signatures)                 │
└─────────────────────────────┬────────────────────────────────────┘
                              │ JSON-RPC (transport depends on deployment)
┌─────────────────────────────▼────────────────────────────────────┐
│  EXECUTION LAYER                                                  │
│  SQC-VM bytecode execution                                       │
│  Quantum Transaction Processor                                    │
│  All state reads/writes hashed with SHA-3                        │
│  All signatures verified with ML-DSA-65                          │
└─────────────────────────────┬────────────────────────────────────┘
                              │
┌─────────────────────────────▼────────────────────────────────────┐
│  CONSENSUS LAYER                                                  │
│  Single authorized validator (Phase 1)                           │
│  Blocks signed with ML-DSA-65                                    │
│  Approximately 2-second configured block interval                │
└─────────────────────────────┬────────────────────────────────────┘
                              │
┌─────────────────────────────▼────────────────────────────────────┐
│  NETWORK & STORAGE LAYER                                          │
│  P2P sealed with ML-KEM-768 + AES-256-GCM (push, announce, sync)  │
│  Ledger state committed via a binary SHA-3-256 Merkle tree over   │
│  accounts, contract code, storage slots, and X402 nonces          │
└──────────────────────────────────────────────────────────────────┘
```

### What "quantum native from block zero" means in practice

The genesis block — block 0, the very first block — is:
- Hashed with SHA-3, not Keccak/SHA-256
- Signed by the genesis validator using ML-DSA-65, not ECDSA
- Contains the first account entries whose addresses are derived from ML-DSA-65 public keys

The native ledger did not begin with ECDSA accounts and later receive a
post-quantum upgrade. That narrower statement does not make browsers, TLS
frontends, build systems, operator hosts, or future cryptanalysis disappear;
those boundaries must be assessed separately.

---

## 4. Addresses — How They Work in a Quantum Chain

This is one of the most important design decisions in the whole chain. Getting it right matters.

### How conventional blockchains make addresses

In Ethereum:
```
private_key (32 bytes, random)
    → ECDSA public key (64 bytes)
        → keccak256(public_key)
            → take last 20 bytes
                → 0x address
```

The address is 20 bytes. It is short because the underlying key is small (32 bytes) and the hashing collapses it.

### How Splendor Quantum Chain makes addresses

In Splendor Quantum Chain:
```
ML-DSA-65 private key (4032 bytes, random)
    → ML-DSA-65 public key (1952 bytes)
        → SHA3-256(public_key)
            → take last 32 bytes
                → SQ address (32 bytes, hex-encoded)
```

**Key differences:**
- **32-byte address** instead of 20-byte — the full SHA3-256 output is retained
- **SHA-3** instead of Keccak-256 — SHA3-256 is standardized in FIPS 202 and
  is used under its current collision- and preimage-resistance assumptions
- **ML-DSA-65 public key** as the root — the key pair is post-quantum from the start

**What an address looks like:**
```
SQ:a7f3c9d2e1b48f6a3c7d9e2f1b48a6c3d7e9f2a1b3c8d4e7f6a2c9d3e8f1b4
```

The `SQ:` prefix and 32-byte payload distinguish this format from an Ethereum
address. It is not EVM-compatible and is not a native MetaMask account format.

### Address generation is completely offline

No network connection is needed to create an address. The user generates the ML-DSA-65 key pair on their device. The address is computed locally. The address is broadcast to the chain only when the first transaction is sent.

---

## 5. Wallets — Creating One, Using One, Securing One

### What a wallet is

In blockchain, a "wallet" is not a place that holds money. The money (tokens) lives on the blockchain. A wallet is a **set of cryptographic keys** that proves you have the right to move those tokens.

On Splendor Quantum Chain, a wallet consists of:

| Component | Size | What it does |
|-----------|------|-------------|
| ML-DSA-65 private key | 4,032 bytes | Signs transactions. Must be kept secret forever. |
| ML-DSA-65 public key | 1,952 bytes | Registered on-chain. Anyone can verify your signatures with it. |
| ML-DSA-65 signature | 3,309 bytes | Attached to every transaction and block. |
| SQ Address | 32 bytes | Your identity on the chain. Like your account number. |
| ML-KEM-768 node key | 1,184 bytes (public) | Per-node, rotating. Encrypts node-to-node traffic. Not an account key. |

### Creating a wallet

**Step 1: Key generation**

The wallet software runs the ML-DSA-65 key generation algorithm:
```
(public_key, private_key) = ML_DSA_65.KeyGen(entropy_source)
```

The entropy source is a cryptographically secure random number generator. This happens entirely on the user's device. Nothing is sent to the network.

**Step 2: Address computation**

```
address = "SQ:" + hex(SHA3-256(public_key)[-32:])
```

**Step 3: Public key registration (first transaction)**

The first time the user sends a transaction, the network automatically records:
- Their address
- Their ML-DSA-65 public key
- The block number of first appearance

After this point, all future transactions from this address are verified against the stored public key.

### The key size challenge and how we solve it

An expanded ML-DSA-65 private key is 4,032 bytes — 126 times larger than an
Ethereum private key (32 bytes). The CLI stores that expanded key in an
encrypted file. The separate browser extension instead derives the key from a
32-byte seed that can be represented by a 24-word BIP-39 mnemonic. One CLI
storage option ships in this repository; the two hardware options below it are
designs:

---

**Option A: Encrypted File Storage** (for everyday users)

The private key is stored in an encrypted file on the user's device. The file
is encrypted using a password that the user sets. The implementation uses
AES-256-GCM after Argon2id derivation; an idealized Grover search would reduce
the symmetric brute-force margin, while password strength, KDF parameters,
device security, and implementation quality remain practical boundaries.

```
wallet.sqwallet — fields exactly as core/crypto/wallet_store.go writes them:
{
  "version": 2,
  "alg": "ML-DSA-65",
  "address": "SQ:a7f3c9...",
  "public_key": "base64_encoded_ml_dsa_65_pubkey",
  "encrypted_private_key": {
    "ciphertext": "base64_aes_gcm_ciphertext",
    "nonce": "base64_nonce",
    "salt": "base64_argon2_salt",
    "time": 3,
    "memory": 65536,
    "threads": 4
  }
}
```

This build reads version 1 and version 2 files, and decides what a file holds by
testing the key rather than by reading a label. Neither label is trustworthy: the
ML-DSA-65 migration never bumped the format version, so a v1 file may hold either
scheme, and `alg` is unauthenticated and absent from older files. So `LoadWallet`
signs and verifies with the loaded key and refuses it if that round trip fails,
which also catches a mismatched keypair that no version number could.

(An earlier revision of this section claimed the two schemes' key sizes were
identical. They are not — mode3 secret keys are 4000 bytes against ML-DSA-65's
4032 — so `PrivateKeyFromBytes` already rejects a genuine pre-standard file on
length alone. The self-test is what covers the case a length check cannot: a file
that is the right shape but the wrong key.) The Argon2id parameters are
stored per file so they can be raised later, and floored on load (time ≥ 1,
memory ≥ 32 MB, salt ≥ 16 bytes) so nobody can weaken a file into an easy
offline guess.

The password is not stored in the wallet file. To open the wallet, the user
enters it and the software decrypts the private key in memory.

---

**Option B: Physical NFC Smart Card** — designed, not built. No hardware
integration exists in this repository; what follows is the intended flow.

The ML-DSA-65 private key would be written to the secure element of a physical NFC card — a dedicated chip that:
- Stores the key in hardware-protected non-extractable memory
- Would perform signing operations internally so the intended flow does not
  export the key
- Requires a PIN to authorise each signing operation

**How signing works with the NFC card:**

1. User opens the wallet app on their phone and creates a transaction
2. The app computes the transaction hash (SHA-3 of the transaction data)
3. The app sends: `SIGN_REQUEST { hash: "sha3_hash_bytes" }` to the card via NFC
4. The card asks the user to enter their PIN (on a card with a small keypad, or via the phone with secure PIN entry)
5. The card's secure element runs `sig = ML_DSA_65.Sign(private_key, hash, ctx)` internally
6. The card sends back: `{ signature: "3309_bytes_of_mldsa_sig" }` via NFC
7. The app attaches the signature to the transaction and broadcasts it

The intended security boundary is that the private key remains inside the
card. No card integration exists here, so that property has not been tested.

A 3,309-byte signature is small enough to cross NFC in a fraction of a second, which is what makes the flow plausible. No timing is quoted here, because no card exists yet to measure.

---

**Option C: QR Airgapped Signing** — designed, not built. No signer in this
repository implements it.

For maximum security — where not even an NFC communication channel is trusted:

1. Wallet app on the online device builds the transaction and encodes the hash as a QR code
2. A deliberately offline device scans the QR
3. The offline device has the ML-DSA-65 private key stored locally
4. The offline device signs the hash and encodes the signature as multiple QR
   frames; the framing format and frame count are not implemented yet
5. The online device scans the QR codes, reconstructs the signature, and broadcasts the transaction

The intended flow transfers data optically and keeps the signing device
offline. That boundary cannot be claimed as tested until an application and
operational procedure exist.

---

### Recovering a wallet

If a user loses their device:

- **For file storage:** They need their `.sqwallet` file (which should be backed up to a secure location like an encrypted USB drive) and their password
- **For NFC card:** They need the physical card and their PIN. The card IS the wallet.
- **For airgapped:** They need the offline device (or a backup of the private key file on another offline device)

The CLI `.sqwallet` flow backs up the encrypted wallet file and password; it
does not expose a mnemonic. A separate prototype
[browser-extension wallet](https://github.com/Splendor-Protocol/splendor-quantum-wallet-extension)
derives ML-DSA-65 keys from 32 bytes of entropy and can encode that entropy as a
24-word BIP39 recovery phrase. A recovery representation is a custody choice,
not evidence that one wallet is inherently more secure than another.

---

## 6. Full User Journey — From Zero to Sending Funds

### Scenario: Alice wants to get on Splendor Quantum Chain and send money to Bob

---

**Step 1: Alice gets a Splendor QC wallet**

Available today:
- CLI wallet source (`sqc-wallet`, encrypted `.sqwallet` file)
- Canonical JavaScript/TypeScript SDK and browser Connect flow, with signing in
  WASM memory
- A prototype Chrome-compatible wallet extension in the separate repository
  linked above; verify its release and audit status before production use

Packaged desktop and mobile (iOS, Android) apps are not built. The walkthrough
below describes the CLI file-wallet flow; the extension uses a seed-derived
encrypted browser vault instead of the `.sqwallet` format.

---

**Step 2: Alice creates her wallet**

Alice clicks "Create New Wallet". The app:
1. Generates an ML-DSA-65 key pair using the device's secure random number generator
2. Computes her SQ address
3. Asks Alice to set a strong password (used to encrypt the private key file)
4. Saves the encrypted `.sqwallet` file
5. Shows Alice her address: `SQ:a7f3c9d2...`

The app also shows:
- A reminder to back up the `.sqwallet` file
- (Designed, not built) an option to write the private key to an NFC card
- Her current balance: 0 SPLD

Key generation and address derivation are local operations. Access to tokens,
exchanges, faucets, or regulated applications is outside this repository and
may impose its own account or compliance requirements.

---

**Step 3: Alice gets some SPLD tokens**

She can:
- Receive SPLD from someone who already has it
- Use a faucet on a network where an operator provides one
- Not today: earn SPLD by running a validator node. Phase 1 has one validator, whose key is pinned in genesis, and no path exists for a second one to join

---

**Step 4: Alice opens her wallet and sees her balance**

The wallet app connects to a Splendor QC node via the RPC endpoint. It queries:
```
GET /balance/SQ:a7f3c9d2...
```

The node returns Alice's current SPLD balance, which the app displays.

---

**Step 5: Alice wants to send 50 SPLD to Bob**

Bob gives Alice his address: `SQ:b8d4e1f3...`

Alice opens the Send screen, enters:
- Recipient: `SQ:b8d4e1f3...`
- Amount: `50 SPLD`
- (Optional) Message: `"For the coffee"`

---

**Step 6: The wallet builds the transaction**

Under the hood, the wallet creates a transaction object:

```json
{
  "version": 1,
  "chain_id": "splendor-quantum-1",
  "type": 1,
  "alg_id": "ML-DSA-65",
  "from": "SQ:a7f3c9d2...",
  "to": "SQ:b8d4e1f3...",
  "amount": "50000000000000000000",
  "fee": "100000000000000",
  "nonce": 3,
  "timestamp": 1753147200,
  "data": "Rm9yIHRoZSBjb2ZmZWU="
}
```

Notes:
- `amount` is in the smallest unit (like wei in Ethereum — 1 SPLD = 10^18 units)
- `nonce` is a counter that prevents replay attacks (each transaction from Alice must have a higher nonce than the last)
- `fee` is the transaction fee paid to the validator
- `type` is `1` for a transfer (see the type table in section 7)
- `alg_id` is `"ML-DSA-65"` and sits *inside* the signed body, so the signature scheme cannot be swapped without invalidating the signature
- `data` is the optional memo / calldata field; it is `[]byte` in Go, so it is base64 in JSON. There is no separate `memo` field
- Field order matters: the body is hashed as Go `json.Marshal` output, which emits struct order. Re-ordering or re-indenting the JSON produces a different hash and a signature that will not verify

⚠️ **The block above is a human-readable rendering, not the wire format.** The
bytes that actually get hashed come from `json.Marshal` of `TransactionBody`
(`core/types/transaction.go`), and Go's encoder does not produce the pretty form:

- `from` and `to` are `[32]byte` with no `MarshalJSON`, so they serialise as
  **JSON arrays of 32 numbers** — `[167,168,169,…]` — not as `"SQ:…"` strings
- `amount` and `fee` are `*big.Int`, so they serialise as **bare JSON numbers**,
  not quoted strings. On the way *in*, the node also accepts them as decimal
  strings (`"amount":"50000000000000000000"`) on `/tx/submit` and the x402
  bodies — JavaScript cannot hold an integer above 2^53 in a Number, and
  1 SPLD is 10^18 units. The string is decoded to the same integer and the
  canonical body is still hashed in the numeric form, so what you sign does not
  change. On the way *out*, `/tx/{hash}`, `/tx/{hash}/receipt` and `/block/*`
  emit numbers unless you ask for `?amounts=string`
- `data` is `[]byte`, so it is base64, and it is omitted entirely when empty

Do not hand-author a signed transaction from this document. Use the WASM signer
in `@splendor/sqc-sdk`, or an implementation that reproduces Go's
`encoding/json` output byte for byte — anything else hashes differently and the
node rejects the signature.

---

**Step 7: The transaction is signed**

The wallet computes:
```
tx_bytes = JSON_encode(transaction_object)   // canonical Go json.Marshal of the tx body
tx_hash  = SHA3-256(tx_bytes)
signature = ML_DSA_65.Sign(alice_private_key, tx_hash, "SPLENDOR-QC-TX-v1")
```

The signed transaction is:
```json
{
  "transaction": { ...the transaction object... },
  "public_key": "base64_encoded_ml_dsa_65_public_key",
  "signature": "base64_encoded_3309_byte_mldsa_signature"
}
```

Total signed transaction size depends on payload size; an otherwise small
transaction is several kilobytes because it carries a 1,952-byte public key and
a 3,309-byte signature. Capacity implications should be evaluated against the
actual workload and network limits.

---

**Step 8: The transaction is broadcast**

The wallet sends the signed transaction to the node:
```
POST /tx/submit
Body: { signed_transaction_data }
```

The node:
1. Decodes the transaction
2. Looks up Alice's ML-DSA-65 public key (from when she first registered)
3. Runs `ML_DSA_65.Verify(alice_pubkey, tx_hash, "SPLENDOR-QC-TX-v1", signature)` — **this is the only signature verification needed. No ECDSA.**
4. Checks Alice's nonce is not below her committed nonce — a stale nonce can
   never execute, so it is refused with `400 nonce too low` rather than queued
5. Checks Alice has sufficient balance
6. Adds the transaction to the mempool (queue of pending transactions)

---

**Step 9: The validator picks it up**

In Phase 1, there is one validator. The validator:
1. Collects transactions from the mempool
2. Orders them (by fee, timestamp)
3. Executes them: deducts from Alice's balance, adds to Bob's balance
4. Builds a new block containing the transactions
5. Signs the block with its own ML-DSA-65 validator key
6. Broadcasts the signed block to connected peers

The configured production interval is approximately two seconds by default.
Load, execution time, failures, and deployment conditions can affect when a
particular transaction is included.

---

**Step 10: Transaction confirmed**

Once the block is broadcast and accepted by the network:
- Alice's balance decreases by 50 SPLD + fee
- Bob's balance increases by 50 SPLD
- The transaction is recorded permanently in the quantum ledger

The wallet app shows Alice: ✅ **Transaction confirmed in block #1847**

Bob checks his wallet app and sees the 50 SPLD. Done.

---

## 7. Transactions — How They Are Created, Signed, Broadcast, Recorded

### Transaction types

Splendor Quantum Chain supports four types of transactions:

| Type | `type` code | What it does |
|------|------|-------------|
| **Transfer** | `0x01` | Send SPLD from one account to another |
| **Deploy** | `0x02` | Deploy a new smart contract |
| **Call** | `0x03` | Interact with an existing smart contract |
| **X402** | `0x05` | Settle a signed off-chain micropayment on-chain |

All four types use the same signing mechanism: ML-DSA-65 signature over SHA-3 hash of the transaction body.

A fifth code, `Register` (`0x04`), is reserved in the type enum but unused: a public key is registered automatically by the sender's first transaction, so no separate registration transaction is ever built or executed.

### Transaction lifecycle

```
User creates transaction
        │
        ▼
Wallet signs with ML-DSA-65
        │
        ▼
Signed tx broadcast to node via RPC
        │
        ▼
Node validates: signature, nonce, balance
        │
        ├── Invalid → Rejected, error returned to user
        │
        └── Valid → Added to mempool
                        │
                        ▼
                Validator picks up tx from mempool
                        │
                        ▼
                Validator executes tx (state change)
                        │
                        ▼
                Validator includes tx in new block
                        │
                        ▼
                Block signed by validator (ML-DSA-65)
                        │
                        ▼
                Block broadcast to connected peers
                        │
                        ▼
                Nodes verify block signature (ML-DSA-65)
                        │
                        ▼
                Block appended to chain
                        │
                        ▼
                Committed by the authorized validator
                (single-authority finality)
```

### Transaction fees

Fees are paid in SPLD. The fee is set by the sender, and the validator drains the mempool highest-fee-first (`core/mempool/mempool.go`). The minimum fee is **not** a governable parameter: 1,000 units is a compile-time constant, checked twice — by the mempool on admission and by the `min_fee` pre-transaction hook — so raising it means editing and rebuilding. The pool does lift its own floor above that as it fills, which is what makes flooding expensive.

Fee distribution:
- 100% to the validator, credited in the same block that carries the transaction
- Splitting fees across a validator set is a Phase-2 intention. No code implements it

### Anti-replay protection

Every account has a nonce — a counter starting at 0. Each transaction must use the next nonce in sequence. A transaction with a nonce that has already been used, or is too far ahead, is rejected.

This means even if someone intercepts your signed transaction, they cannot reuse it — the nonce would be wrong.

---

## 8. Smart Contracts — SQC-VM and QSol Prototype

Smart contracts on Splendor Quantum Chain work similarly to Ethereum's, but with quantum-safe foundations.

### What a smart contract is (plain English)

A smart contract is bytecode stored on the blockchain and executed when called.
Its behavior follows that bytecode and the chain rules active at the block that
executes it.

**Example:** A simple token contract. The rules say: "If you call `transfer(to, amount)` and you have enough tokens, deduct from your balance and add to theirs." This executes automatically. No bank. No intermediary. The code runs.

### How smart contracts work on Splendor QC

**Deploying a contract:**

1. Developer produces SQC-VM bytecode, directly or with the prototype
   `sqc-solc` compiler described below
2. Bytecode is checked with `POST /vm/validate`, read back with `POST /vm/disasm`, and costed with `POST /vm/estimate-gas`
3. Developer creates a Deploy transaction containing the bytecode
4. Signs it with their ML-DSA-65 key
5. Broadcasts to the network
6. Validator executes the deployment — the contract gets an SQ address
7. Contract is now live and callable

**Calling a contract:**

1. User creates a Call transaction specifying:
   - Contract address
   - Function name
   - Parameters
2. Signs with ML-DSA-65 key
3. Broadcasts to network
4. Validator executes the contract function, which may:
   - Read/write state
   - Transfer SPLD
   - Call other contracts
   - Emit events (logged permanently)
5. Results recorded in the block

### QSol-subset compiler

A prototype QSol-subset compiler is built and working. `compiler/` parses a
Solidity-flavored subset, emits deployable SQC-VM v4 bytecode, and produces ABI
metadata containing function selectors and event topics. `cmd/sqc-solc` exposes
it as a CLI. Compiler tests compile, deploy, and call generated bytecode against
the real VM.

The implemented subset supports scalar `uint256`, `address`, and `bool` state;
mappings; functions and parameters; local variables; checked arithmetic;
conditions; gas-bounded `for` and `while` loops; events; `msg.sender`;
`msg.value`; and common control flow. It does **not** yet provide constructors,
internal function calls, arrays, structs, strings, inheritance, package
management, source-verification integration, IDE tooling, audits, or full
Solidity compatibility. Use a guarded `init()` in place of a constructor.

One integration mismatch remains before this can be called a complete
toolchain: the compiler currently emits selectors using the Solidity type name
`address`, while the canonical SDK's QSol ABI guard expects `sqaddress`.

The execution surface has two layers:

**1. SQC-VM bytecode (`vm/`).** A stack VM with SHA-3-256 storage slots, `SQ:`
addresses, gas metering, and a disassembler. A Deploy transaction carries raw
SQC-VM bytecode in its `data` field. `POST /vm/disasm`, `POST /vm/validate`,
`POST /vm/estimate-gas` and `GET /vm/opcodes` let you inspect and check that
bytecode.

**2. Native Go modules (`vm/`).** Token standards, a DEX, lending, staking,
multisig and an oracle are implemented in Go inside the node rather than
compiled from user source. This is how the fork-gated token, exchange, and
bridge actions are implemented.

Be precise about which of them a transaction can actually drive.
`DispatchModuleCall` (`vm/module_router.go`) routes six modules; the
authoritative action list is `vm.KnownModuleActions`
(`vm/module_router_read.go`), and every write that arrived after a module
first shipped is gated by a fork the genesis document schedules (`forks` in
`genesis.json`, see `DEPLOY.md` — "Fork Schedule"). Before that height the
action is a hard "fork not active" error: refused at `/tx/submit`,
unappliable in a block.

| Module | Writes | Fork gate | Reads (`POST /call`) |
|---|---|---|---|
| `qrc20` | `deploy`, `mint`, `transfer`, `approve`, `burn` | — (since launch) | `balance_of`, `total_supply`, `allowance`, `token_info`, `permit_nonce`, `paused`, `owner` |
| `qrc20` | `permit`, `transfer_from`, `increase_allowance`, `decrease_allowance`, `burn_from`, `transfer_ownership`, `pause`, `unpause` | `qrc20_v2` | (as above) |
| `qrc1155` | `deploy`, `mint`, `mint_batch`, `transfer`, `batch_transfer`, `set_approval_for_all`, `burn`, `burn_batch`, `transfer_ownership` | `qrc1155_v1` | `balance_of`, `balance_of_batch`, `is_approved_for_all`, `uri`, `total_supply`, `token_info` |
| `dex` | `deploy_pool`, `add_liquidity`, `swap` | — (since launch) | `reserves`, `pool_info` (also `GET /dex/pool/...`) |
| `exchange` | `deploy`, `create_condition`, `split`, `merge`, `match`, `cancel_order`, `increment_nonce`, `resolve`, `redeem`, `redeem_for`, `pause`, `unpause`, `set_redeem_fee_bps`, `set_fee_recipient`, `set_operator`, `transfer_ownership` | `exchange_v1` (requires `module_revert_v1`) | `exchange_info`, `condition_info`, `order_status`, `order_remaining`, `order_hash`, `nonce`, `is_operator`, `position`, `position_id`, `simulate_match` (also `GET /exchange/...`) |
| `bridge` | `deploy`, `claim` (M-of-N attested), `set_operator`, `set_threshold`, `pause`, `unpause`, `transfer_ownership` | `bridge_v1` (requires `module_revert_v1`) | `bridge_info`, `claim_status` (also `GET /bridge/...`) — see `docs/modules/bridge.md` |
| `staking` | `register`, `add_stake`, `unstake`, `withdraw`, `unjail` (open to anyone); `slash`, `set_pass`, `set_admin` (admin only) | `validator_gates_v1` (requires `module_revert_v1`) | `validator`, `pass`, `can_produce`, `staking_info`, `validator_set` (also `GET /staking/...`) — see `docs/modules/staking.md` |
| `verify` | `deploy`, `register` (dual-signed ML-DSA-65 + SLH-DSA instruction), `attest`, `finalize`, `spend`, `set_operator`, `set_threshold`, `pause`, `unpause` | `verify_v1` (requires `module_revert_v1`) | `proof`, `instance` (also `POST /verify/check`, `GET /verify/proof/...`) — see `docs/modules/verify.md` |
| `qrc721` | `deploy`, `mint` (collection owner only), `transfer`, `transfer_from`, `approve`, `set_approval_for_all`, `burn` | `qrc721_v1` (requires `module_revert_v1`) | `owner_of`, `balance_of`, `token_uri`, `get_approved`, `is_approved_for_all`, `token_info` — see `docs/modules/qrc721.md` |

One more name, `module_revert_v1`, is not a module but the failure rule: under
it a module call that errors is included with a failed receipt (fee kept,
nonce consumed) instead of being dropped, so a relayer whose call fails cannot
jam its own account. The wire formats are in `docs/modules/qrc20-permit.md`
and `docs/modules/exchange.md`.

`vm/lending.go`, `vm/governance.go`, `vm/oracle.go`, `vm/multisig.go` and
`vm/qrc4626.go` are written and tested, and their `/lending/*` and `/gov/*`
RPC routes read their state, but no transaction reaches them: until the
router learns those module names, they are library code, not a live surface.
(`vm/staking.go` was in that list until `validator_gates_v1` and
`vm/qrc721.go` until `qrc721_v1`; both are routed now, and `/staking/*` reads
the module's real singleton rather than an address nothing ever wrote to.)
Two of the unrouted ones were hardened ahead of routing: the lending market
binds a price oracle (`SetOracle` / `BorrowFromOracle`) so a routed borrow
can never quote its own collateral, and the oracle keeps a feeder index so
`GetPrice` discovers its feeders.

The source tree proves that these modules are implemented and reachable when
their fork gates are active; it does not prove that every live tier has
activated them. The dated deployment snapshot in
[`docs/readme-claim-evidence.md`](docs/readme-claim-evidence.md) records the
fork schedules observed on the public tiers.

### Contract state commitment

State is committed with a binary SHA3-256 Merkle root over the **entire** state
— account records, contract code, contract storage slots, and X402 nonces — and
that root is included in every block header. Leaves are length-prefixed and
domain-separated to avoid concatenation ambiguity.

Committing storage and code (not just balances) is what stops a silent fork: if the root covered accounts only, two nodes with byte-different token balances or DEX reserves could hash to the same root and "agree" while diverging.

A full Sparse Merkle Tree, adding per-account inclusion proofs so a light client can verify one account without the whole state, is planned but not yet implemented.

The native ledger commitment path uses SHA3-256. Browser integrity pins and
other off-chain surfaces may use SHA-256 as described at the top of this file.

---

## 9. Consensus — Phase 1 Single-Authority Finality

### Phase 1: Single Validator

Phase 1 runs with one authorized validator. This provides deterministic
single-authority block production, not decentralized economic or Byzantine
finality. Compromise of the validator key or host is therefore a critical risk;
the absence of a 51% voting threshold does not make the network impossible to
attack.

**What the single validator does:**
1. Receives transactions from the mempool
2. Orders and executes them
3. Builds a block on a fixed timer — `--block-interval`, approximately 2 seconds
   by default. A full mempool does not trigger an early block; it only makes the
   next one larger
4. Signs the block with its ML-DSA-65 key
5. Broadcasts to all full nodes

**Who operates it?** Splendor Protocol operates the genesis validator. The validator's ML-DSA-65 public key is hardcoded in the genesis block, so every node in the network knows exactly whose signatures to accept.

### How blocks are signed

```
block_header = {
  height: 1847,
  timestamp: 1753147200,
  previous_hash: SHA3-256(previous_block_header),
  tx_merkle_root: SHA3-256_merkle_root(transactions),
  state_root: SHA3-256_merkle_root(state),        // binary tree — see section 10
  validator: "SQ:validator_address..."
}

block_hash   = SHA3-256(JSON_encode(block_header))   // Go json.Marshal of the header
block_sig    = ML_DSA_65.Sign(validator_private_key, block_hash, "SPLENDOR-QC-BLOCK-v1")
```

Every full node verifies:
```
ML_DSA_65.Verify(validator_public_key, block_hash, "SPLENDOR-QC-BLOCK-v1", block_sig) == true
```

If a node receives a block with an invalid ML-DSA-65 signature, it rejects it.
Under the current assumptions of ML-DSA and this implementation, a party
without the authorized validator key should not be able to forge that
signature. A party controlling the authorized key can sign blocks, which is why
validator custody remains a central security boundary.

### Who may produce: the validator gates (`validator_gates_v1`)

Syncing is open to anyone, and so is staking. Producing is not.

On a chain that schedules `validator_gates_v1`, a key may seal a block only if
it passes both gates:

1. **Admission** — an entry in the on-chain pass registry, held against that
   key's ML-DSA-65 public key. An admin grants and revokes it; the genesis
   validator is admitted implicitly, because it has to seal block 1 before
   anything can be staked.
2. **Bond** — a registered stake at or above the Bronze floor (3,947 SPLD),
   status `active`. The SPLD really moves: into the staking module's escrow
   account on `register`, out again only through `unstake` + `withdraw` after
   a ~7-day unbonding delay.

One predicate (`vm.StakingContract.CanProduce`) answers this for both block
production and block acceptance, so a producer cannot build a block its own
followers refuse. `POST /call` with
`{"module":"staking","action":"can_produce",…}` asks it directly.

Anyone can register a key and stake behind it today — `docs/modules/staking.md`
has the full action surface. Before the upgrade activates, the staking module
is unreachable and any key the chain accepts signatures from may produce, which
is what Phase 1 has always been.

### Phase 2 and Beyond: Expanding Validators

The intended future design would add:

- Consensus upgrades to a BFT protocol (all signatures still ML-DSA-65)
- Governance is handled by token holders (voting via ML-DSA-65-signed transactions)

**Multi-validator consensus is still not built.** `validator_gates_v1` decides
who is *allowed* to produce; it does not give a second eligible validator a
turn. `consensus/bft.go` is explicitly incomplete — vote gossip, equivocation
handling, timeouts and fork choice are not written, `qc_root` stays nil, and
the `multi_validator` upgrade remains reserved and unimplemented. There is no
production path for a second independent validator to join, and a passed
governance proposal cannot change consensus: `Execute` in `vm/governance.go`
marks a proposal Executed and applies nothing else.

---

## 10. Record Keeping — The Quantum Ledger

### How the blockchain stores data

Blocks, transactions and the whole of state are committed with SHA-3-256 and signed with ML-DSA-65. Underneath, LevelDB stores bytes the way LevelDB always has: there is no per-record MAC on the database files. The integrity guarantee is the commitment chain described below, not the storage engine.

**The block structure:**

```
Block {
    header: {
        height:           uint64
        timestamp:        uint64
        prev_block_hash:  SHA3-256_hash (32 bytes)
        tx_merkle_root:   SHA3-256_hash (32 bytes)
        state_root:       SHA3-256_hash (32 bytes)
        validator_addr:   SQ address (32 bytes)
    }
    block_hash:    SHA3-256(encoded_header)   // 32 bytes
    validator_sig: ML-DSA-65_signature         // 3309 bytes
    transactions:  [signed_transaction, ...]
}
```

**Why SHA-3 and not SHA-256 or Keccak-256?**

SHA-256 is generally considered quantum-safe for hashing (Grover's algorithm provides only a square-root speedup, leaving SHA-256 with ~128-bit security against quantum). However, Keccak-256 (used by Ethereum) is not NIST-standardised. SHA-3 (also based on Keccak but differently parameterised) IS the NIST standard (FIPS 202).

Splendor QC uses SHA-3 because:
- It is NIST FIPS 202 standardised — the same authority that standardised ML-DSA-65
- It provides 256-bit output → 128-bit quantum security (Grover's algorithm)
- It is consistent with NIST's overall post-quantum migration framework
- Using the same standards family makes the implementation boundary easier to
  document; it does not itself establish compliance or audit approval

**State storage — binary SHA-3-256 Merkle commitment over the whole state:**

Accounts, contract code, contract storage slots, and X402 nonces are all hashed
into a binary Merkle tree whose root is in every block header. Leaves are
length-prefixed and domain-separated (leaf tag `0x00`, internal tag `0x01`) to
avoid structural ambiguity. Subject to SHA3-256's collision-resistance
assumption, this means:

- Two nodes that disagree on any balance, storage slot, or contract byte produce different roots, so divergence is detected instead of silently tolerated
- The commitment chain: state leaf → SHA-3 leaf hash → SHA-3 parent hashes → state root in block header → block signed with ML-DSA-65
- Every native signature and commitment step uses the algorithms and security
  assumptions stated at the top of this file
- Blocks and receipts are queryable at any height; **state is not**. `/balance`, `/nonce` and the contract routes read current state only, and no endpoint accepts a height, so “the balance at block N” cannot be asked of a node today

Not yet built: a **proof API**. The tree would support per-account inclusion proofs, but no endpoint serves them, so a light client cannot today verify one account without trusting a full node. That is what the planned Sparse Merkle Tree work is for.

**Transaction Merkle tree:**

All transactions in a block are hashed into a Merkle tree (SHA-3) whose root sits in the block header. That is the structure a light client would use to check inclusion without the full block — but as with state proofs, no endpoint serves a branch, so a client fetches the block and recomputes the root itself.

---

## 11. X402 — Native Payment Protocol

Splendor implements a dedicated `TxX402` authorization and settlement path.
The node verifies an ML-DSA-65 payment authorization, binds it to a
payer-signed settlement transaction, applies validity-window, receiver, amount,
balance, chain-ID, and nonce checks, and admits the settlement through the
normal mempool and block-execution pipeline.

### How it works end-to-end

**Server-side access gate:**

The canonical package exports a framework-agnostic access middleware:
```javascript
import { Connection, createX402Middleware } from '@splendor/sqc-sdk';

const connection = new Connection({
  rpcUrl: 'https://quantum-rpc.splendor.org',
  chainId: 'splendor-quantum-1',
});

app.use('/api/data', createX402Middleware({
  connection,
  payTo: 'SQ:operator_address...',
  price: '0.001',
}));
```

This middleware issues requirements and verifies the `X-Payment` authorization,
but it does **not** itself submit a settlement transaction. Applications that
must enforce payment before access should require the payer-side settlement
result or add an application-level confirmation policy rather than treating a
reusable verification result as proof of settlement.

**Client side (user or AI agent):**

```
1. Client requests the resource and receives HTTP 402 requirements
2. Client signs the X402 authorization with ML-DSA-65
3. Client signs a `TxX402` settlement using the same payment nonce
4. Client submits requirements, authorization, and `settlement_tx` to
   `POST /x402/settle`
5. Node validates and admits the transaction to the mempool
6. Application applies its own access and confirmation policy
```

**Payment on chain:**

`POST /x402/settle` takes three things: the requirements, the signed payload,
and a `settlement_tx` — a `TxX402` signed by the *payer*. The node:
1. Verifies the ML-DSA-65 signature on the payment payload
2. Rejects the call with `400` if `settlement_tx` is missing, and checks that it
   is bound to that payload
3. Puts the settlement transaction into the mempool, where the payer's
   signature, nonce, fee floor, and balance are checked like any other
   transaction
4. Re-checks the X402 replay registry during block execution

The server cannot debit the payer on its own; only the payer can produce the
settlement transaction, because it consumes the payer's nonce. That is
deliberate — an earlier design settled inside the HTTP handler, which meant the
transfer existed on the node that received the call and nowhere else. Settlement
goes through consensus now, so it lands in a block and in the state root.

Use `payX402` from `@splendor/sqc-sdk` for the current payer-side
authorization-and-settlement flow. The deprecated top-level `sdk/sqc-sdk.js`
calls settlement without the required `settlement_tx` and is unsafe for current
production submit flows. The current TypeScript middleware verifies access but
also deliberately leaves settlement to the payer/application.

A valid admitted transaction is expected in the next produced block under
normal operation, not guaranteed within two seconds. The normal chain
transaction fee is required. The reviewed code imposes no mandatory payment-
processor percentage.

### X402 implementation status

| Capability | Status |
|---|---|
| Dedicated native transaction type | Built in code |
| ML-DSA-65 payment authorization | Built and tested |
| Payer-signed settlement transaction | Built and tested |
| On-chain nonce replay protection | Built and tested |
| AI-agent-compatible wallet automation | Supported by the protocol; application integration and key custody are required |
| Mandatory processor percentage | None in the reviewed code |
| Chain transaction fee | Required |
| Expected settlement | Next produced block under normal operation; not a latency or inclusion guarantee |
| Deprecated `sdk/sqc-sdk.js` automatic settlement | Incomplete; omits `settlement_tx` |
| Current `createX402Middleware` automatic settlement | Not implemented; verification/access gate only |

---

## 12. The Ecosystem — DApps, DeFi, NFTs, AI Agents

This section separates reachable code from product direction.

### Reachable application surfaces in the source tree

- SQC-VM deployment and calls
- QRC-20 and DEX actions available from the launch rules
- Fork-gated QRC-1155, exchange, migration-bridge, and validator-staking actions
- X402 authorization and payer-signed settlement
- Contract events and current-state reads

The live availability of a fork-gated action depends on the tier's persisted
fork schedule. Lending, governance, oracle, multisig, QRC-721, and QRC-4626
files exist, but the transaction router does not expose them. They are not
product features merely because library code and read routes exist.

### Product directions, not current deployments

Tokens, marketplaces, games, machine payments, credentials, regulated-asset
workflows, and financial applications are possible uses of the VM and native
modules. The repository does not establish that such applications are deployed,
audited, legally compliant, liquid, or suitable for production.

The planned AI layer is a decentralized model, storage, inference, licensing,
and creator-payment network. Model weights and computation would remain
off-chain while Splendor records post-quantum identities, model manifests,
usage receipts, licenses, and settlement. That product is **not implemented** by
the current adaptive node scheduler. Today, an application can automate the
`payX402` flow, but it must still provide secure key custody, funding, error
handling, access policy, and settlement confirmation.

No KYC attestation registry, sanctions-screening system, credential contract,
or regulatory-reporting product exists. `vm/governance.go` is not routed for
writes, and executing a proposal changes no consensus parameter.

---

## 13. Cryptographic Primitives Used (Technical Reference)

This section summarizes native ledger and node operations. It does not classify
the browser, reverse proxies, CI, operator access, or third-party applications.

| Operation | Algorithm | Standard | Security characterization |
|-----------|-----------|----------|----------------|
| Account, transaction, block, and X402 signing | ML-DSA-65 | [NIST FIPS 204](https://csrc.nist.gov/pubs/fips/204/final) | Designed to resist known classical and quantum attacks under current assumptions |
| Address derivation, block/transaction hashes, Merkle and state roots | SHA3-256 | [NIST FIPS 202](https://csrc.nist.gov/pubs/fips/202/final) | 256-bit hash; Grover-style search is accounted for in the security assessment |
| CLI wallet private-key encryption | AES-256-GCM | NIST FIPS 197 | Symmetric authenticated encryption; password strength and implementation remain security boundaries |
| CLI wallet password KDF | Argon2id | RFC 9106 | Memory-hard password derivation, not a post-quantum public-key algorithm |
| Native P2P session and traffic | ML-KEM-768 + AES-256-GCM | [NIST FIPS 203](https://csrc.nist.gov/pubs/fips/203/final) | Sealed by default; explicit pins plus `--require-peer-pins` are needed for preconfigured peer identity |
| Node-terminated RPC / WebSocket TLS | X25519MLKEM768 hybrid | TLS 1.3 hybrid key exchange | Offered when configured; `--tls-require-hybrid` is needed to refuse classical X25519 fallback |
| Smart-contract storage slots and selectors | SHA3-256 | [NIST FIPS 202](https://csrc.nist.gov/pubs/fips/202/final) | Native SQC-VM convention |

**Native-ledger boundary:** ECDSA, RSA, SHA-256, and Keccak-256 are not used for
native account signatures or ledger commitments. This is not a repository-wide
ban: the optional MetaMask derivation route uses secp256k1, browser WASM pins use
SHA-256, TLS certificates and frontends have their own algorithms, and the
hybrid TLS group contains X25519 alongside ML-KEM-768.

---

## 14. Architecture Comparison — Not a Finality Benchmark

| Feature | Ethereum | Solana | Splendor Phase 1 |
|---|---|---|---|
| Native account signature | Classical elliptic-curve key | Ed25519 | ML-DSA-65 |
| Smart-contract/program execution | EVM | sBPF/SVM | SQC-VM plus native Go modules |
| High-level compiler/tooling | Mature Solidity toolchain | Mature Rust/LLVM and Anchor tooling | Prototype QSol-subset compiler and `sqc-solc` |
| Consensus authority | Distributed proof of stake | Distributed proof of stake | One authorized validator |
| Configured block/slot interval | 12-second slots | Approximately 400–600 ms slots | Approximately 2-second block interval |
| Decentralized Byzantine/economic finality | Yes | Yes | No |
| EVM compatibility | Native | Not native | No |

The interval row compares production cadence, not finality. Ethereum references:
[account and validator keys](https://ethereum.org/developers/docs/consensus-mechanisms/pos/keys/),
[proof of stake and 12-second slots](https://ethereum.org/developers/docs/consensus-mechanisms/pos/),
and the [Solidity compiler](https://docs.soliditylang.org/en/latest/using-the-compiler.html).
Solana references: [transaction-signature fees](https://solana.com/docs/core/fees/fee-structure),
[slot duration](https://solana.com/developers/cookbook/transactions/confirmation),
and [program execution](https://solana.com/docs/core/programs/program-execution).
This table does not claim that Ethereum or Solana cannot support automated
wallets, AI agents, or micropayments.

---

## 15. Business Claims and Evidence Boundary

The code implements transaction fees, and Phase 1 credits those fees to the
authorized validator. That is a protocol behavior, not a revenue forecast.

Future X402 protocol charges, validator-marketplace revenue, enterprise
licensing, patent licensing, market share, and adoption timing are business
hypotheses or plans; this repository does not verify them. No patent statement
should be published without a filing or application number, jurisdiction,
filing date, applicant/owner, status, and the exact scope being referenced.

No CRQC arrival date, market-size estimate, institutional-spending estimate, or
claim that every regulation benefits Splendor is asserted here. Those claims
require separately maintained evidence and owner review.

---

## 16. What Is Built vs What Comes Next

### Built in code

- Go full node; mempool; block production; execution; receipts; chain storage;
  and current-state RPC
- ML-DSA-65 account, transaction, block, and protocol signing with separate
  contexts
- SHA3-256 transaction, block, Merkle, storage-slot, and whole-state commitments
- Signed height-zero genesis builder
- ML-KEM-768 + AES-256-GCM sealed native P2P push, announce, and catch-up paths
- Optional node-terminated hybrid TLS and hybrid-only policy
- AES-256-GCM + Argon2id `.sqwallet` CLI wallet format
- SQC-VM bytecode interpreter
- Reachable QRC-20, QRC-1155, DEX, exchange, and bridge router paths, subject to
  fork activation
- X402 payment authorization and payer-signed settlement transaction
- Canonical `@splendor/sqc-sdk`, WASM signer, and browser Connect components
- Prototype QSol-subset compiler and `sqc-solc` CLI
- Adaptive mempool-depth scheduler

### Tested or measured, with a narrower claim

- Repository tests cover compiler compile/deploy/call behavior; P2P sealing,
  downgrade refusal, and peer pins; X402 verify/bind/settle/replay behavior;
  genesis signing; and state-root coverage
- ACVP subset tests provide conformance evidence, not a certificate
- A dated single-host experiment measured approximately 2,605–2,901
  transactions per second through the tested block-production pipeline,
  depending on batch size. It is not a sustained public-network or
  multi-validator benchmark; see
  [`docs/performance/2026-08-25-tps-gpu-experiment.md`](docs/performance/2026-08-25-tps-gpu-experiment.md)

### Observed deployments, not inferred from source

The public devnet, testnet and mainnet nodes, their indexers and their
explorers were re-observed between `2026-09-02T13:03Z` and `13:06Z` while this
revision was written. The tier table in [§20](#20-live-networks-and-endpoints)
records the chain ids, heights, images, commits, fork schedules, genesis hashes
and validator addresses seen, and
[`docs/readme-claim-evidence.md`](docs/readme-claim-evidence.md) keeps every
dated snapshot. Devnet was running `v0.6.2`, built from the README-audit commit
`d2ed9cfa9f66`; testnet and mainnet were running `v0.6.1` from
`2982523195d0`; every node reported `peer_count: 0`. This is a dated
observation, not a continuous-availability guarantee.

The separate
[quantum explorer](https://github.com/Splendor-Protocol/splendor-quantum-explorer)
has a deployed web UI. The separate
[browser wallet extension](https://github.com/Splendor-Protocol/splendor-quantum-wallet-extension)
is a prototype; repository existence is not a store release, security audit, or
production-readiness claim.

### Built but requiring integration or hardening

- X402 middleware settlement policy and confirmation; the deprecated JS path
  omits `settlement_tx`, while the current middleware verifies but does not
  automatically settle
- QSol language completeness and `address`/`sqaddress` ABI alignment
- Per-tier fork activation and migration-bridge deployment
- Explicit peer pinning where peers are deployed
- Hybrid-only TLS where the node terminates TLS; proxy-fronted deployments have
  their own cryptographic boundary
- Native-PQ-first wallet UX, signer artifact provenance, deployment
  consistency, release reproducibility, and complete CI coverage

### Not built or not production-ready

- Multi-validator consensus in the running node, permissionless validator
  onboarding, or decentralized Byzantine/economic finality
- Historical-state queries or a Merkle inclusion-proof API
- Sanctions screening, KYC attestation, AML monitoring, or regulator dashboards
- Packaged desktop or mobile wallet applications, NFC hardware signing, or a QR
  air-gapped signing application
- HSM or remote validator signer
- Published Python or Go application SDK
- Independent security or cryptographic audit
- CAVP/CMVP validation or a FIPS 140-3 module certificate
- Production RTGS/CBDC deployment or national-scale validation
- Decentralized AI model storage or inference network

---

## 17. Adaptive Scheduler

Every node runs a small adaptive scheduler in-process, with no outbound API calls, no cloud dependency, and no data leaving the machine.

### What "local AI" means here — and what it does not

Be precise about this, because "AI" is an overloaded word. There is **no neural network, no ONNX runtime, and no trained model** in the node today. What ships in `ai/scheduler.go` is a compact statistical control loop:

- **Exponential moving average** over observed mempool depth
- **A reinforcement-learning-style greedy policy** that tunes block batch size up or down based on whether the last adjustment helped
- **Rule-based anomaly detection** that flags a mempool spike above a fixed multiplier

That is genuinely useful and genuinely local, but it is closer to adaptive control than to machine learning, and it should not be described as an inference engine.

### What the scheduler actually does

The validator asks the scheduler for a batch size each round, and the scheduler adapts that number from the mempool depth it has observed:

- Recommends how many transactions to pack into the next block, between a floor and a ceiling
- Smooths that depth over time rather than reacting to a single observation
- Raises an anomaly flag when pending transactions spike sharply. Nothing in the node consumes the flag — see below

Not implemented, despite appearing in earlier drafts of this document: grouping transactions by contract state, re-prioritising the queue by fee, and reading CPU, memory, or I/O load. The only signal currently fed to the scheduler is mempool depth.

**On throughput:** an earlier version of this section claimed a measured 40–60% improvement over static scheduling. That figure is not substantiated by any benchmark in this repository and has been withdrawn. What has been measured, on a single AWS host, is roughly 2,600–2,900 TPS through the block-production pipeline, with ML-DSA-65 verification well above that; see [`docs/performance/2026-08-25-tps-gpu-experiment.md`](docs/performance/2026-08-25-tps-gpu-experiment.md).

**The anomaly flag, precisely.** `ai/scheduler.go` raises one boolean when the
mempool grows by 5× or more between observation windows *and* holds more than
100 pending transactions. It is reported in three places and acted on in none:
`GET /ai/stats`, the `sqc_ai_anomaly_active` Prometheus gauge, and one
`log.Warn` line the first time it flips. No code path in the node reads it
back, so it throttles nothing, quarantines nothing, and rate-limits nothing —
an operator can alert on the gauge, but the node will not act on it. Rate
limiting and the simulation-plane caps are separate, ordinary middleware, not
driven by this flag.

Not implemented, and previously claimed here: detecting coordinated spam from
address clusters, spotting suspicious contract-deployment patterns, identifying
fee manipulation, watching transaction failure rates, monitoring CPU/RAM/disk/
network, predicting load 10–30 seconds ahead, pre-allocating threads, and
validator bid optimisation. None of these exist in any form. The only input the
scheduler receives is mempool depth.

### Why the scheduler runs in-process

**No external dependency.** The scheduler is roughly 200 lines of Go in the node
binary. There is no API key, no cloud account, no outbound call, and nothing to
fail independently of the node.

**No data leaves the machine.** Mempool depth and fee trends stay in process
memory. This matters for an operator who cannot ship transaction telemetry to a
third party, and it is true because there is no third party involved, not
because of any privacy engineering.

**Nothing to intercept.** A cloud inference call would be one more TLS
connection carrying operational data. There is no such connection here.

---

## 18. Banking, Settlement, and Compliance Boundary

Splendor's architecture may be evaluated for future real-time settlement,
tokenization, or institutional use cases. The current Phase 1 network is not a
production RTGS, CBDC, interbank, or national-payment system.

NIST standardized ML-KEM, ML-DSA, and SHA3 in the publications linked above.
Those standards do not certify this implementation and do not create a
universal legal deadline for every bank or blockchain. Any jurisdictional or
regulatory statement published elsewhere should cite the primary source and
preserve its exact scope.

X402 provides a native payer-authorized settlement transaction. It does not
eliminate chain fees, operational intermediaries, counterparty risk, legal
obligations, application integration, or confirmation policy. Under Phase 1,
the sole authorized validator remains a central operator and security boundary;
an independent bank cannot join the validator set through a production path.

### Compliance and institutional capabilities

| Capability | Current status |
|---|---|
| ML-DSA-65, ML-KEM-768, and SHA3-256 implementation | Built and covered by ACVP-subset tests; no CAVP/CMVP validation, FIPS 140-3 module certificate, or independent audit |
| Transaction and state commitments | Built; blocks carry SHA3-256 transaction and whole-state roots and ML-DSA-65 signatures |
| Historical audit queries | Blocks and receipts are queryable; node state APIs return current state only. The separate indexer stores additional history, but is a distinct deployment component |
| Contract events | Built as ledger data that future reporting tools may consume; not a regulatory-reporting product |
| Sanctions screening / AML monitoring | Not built |
| KYC attestation registry | Not built |
| Regulator dashboards and formal reports | Not built |
| Historical-state API | Not built |
| Merkle inclusion-proof API | Not built |
| HSM or remote validator signer | Not built |
| Multi-validator fault tolerance | Not built in the running node |
| Institutional scale and resilience | Not established by the single-host benchmark or the current single-validator deployments |

An institutional evaluation must assess validator custody, host compromise,
proxy-fronted TLS, peer identity policy, release provenance, disaster recovery,
legal controls, operational staffing, audits, and certification in addition to
the native cryptographic design.

---

## 19. The Splendor Quantum Stack — Every Repository

Seven repositories make up the stack. This one holds the ledger; two hold
keys on the user's device and sign; three hold no keys and only read or relay;
one runs the servers. Every fact below was checked against the repository
named, at the commits recorded in
[`docs/readme-claim-evidence.md`](docs/readme-claim-evidence.md), on
2026-09-02. All seven are private repositories in the `Splendor-Protocol`
GitHub organisation.

### `splendor-quantum-blockchain` — this repository

The chain. Go 1.25. `sqc-node` (full node and validator), `sqc-wallet` (CLI),
SQC-VM and the native modules, the QSol-subset compiler and `sqc-solc`, the
X402 protocol, ML-KEM-sealed P2P, the migration-bridge relayer, the
`QuantumMigrationBurn` Solidity contract for the legacy chain, and the
JavaScript/TypeScript SDK with the Go WASM signer that the browser extension
ships. Sections 1–18 describe what it does; §22 describes where each part
lives. Releases are cut and rolled from here with `npm run deploy:<tier>`
(§21), and CI (`go test`, signer-drift check) runs on every push to `main` and
every pull request.

### `splendor-infra` — where the chain runs

Infrastructure as code and the release pipeline for the chain nodes and the
indexer. OpenTofu 1.12 describes one AWS environment per tier (`devnet`,
`testnet`, `production` under `environments/`, us-east-1): a VPC, one Graviton
host (`t4g.small` for devnet and testnet, `t4g.medium` for mainnet), an
encrypted EBS data volume with daily AWS Backup (7, 14 and 35 days), IMDSv2,
SSM Session Manager for admin, and no SSH and no public ports.

On each host `deploy/node/docker-compose.yml` runs five containers:

| Container | Role |
|---|---|
| `sqc-node` | The validator, built from this repository's Dockerfile; REST on `:8080` inside the compose network only |
| `cloudflared` | Outbound Cloudflare Tunnel that publishes the node as `quantum-rpc[-testnet\|-devnet].splendor.org` and path-routes `/idx/*` to the indexer |
| `indexer` | `sqc-indexer` (Go, `deploy/indexer/`): ingests blocks into Postgres, serves `/idx/v1/*` (§20) and an SSE stream |
| `postgres` | Postgres 17 for the indexer |
| `heartbeat` | Sends a periodic demo transfer with `sqc-wallet send --chain-id <tier>` where `HEARTBEAT_ENABLED` is set. This is why devnet and testnet show steady transfer counts |

`entrypoint-validator.sh` generates the validator wallet on first boot, writes
`genesis.json` from the tier's variables (`SQC_CHAIN_ID`, supply, allocations,
`SQC_GENESIS_FORKS`) and starts `sqc-node --validator`. A tier's identity is
therefore that wallet file on that volume: losing it means a new chain.

Five GitHub Actions workflows. The one that touches AWS,
`Deploy quantum infrastructure`, assumes a per-tier role over OIDC; there are
no long-lived cloud keys anywhere:

| Workflow | Trigger | Does |
|---|---|---|
| `Build sqc-node image` | dispatch (`chain_ref`, `image_tag`) | Checks this repository out at the ref with a read-only deploy key, builds its Dockerfile for `linux/arm64`, stamps commit and tag, pushes to GHCR |
| `Build sqc-indexer image` | dispatch (`image_tag`) | Same for `deploy/indexer` |
| `Deploy quantum infrastructure` | dispatch (`environment`, `apply_infrastructure`, `rollout_node`, `allow_fork_extension`, `reset_chain`, `reset_confirm`) | `tofu apply`, then the SSM rollout described in §21 |
| `Quantum explorer release policy` | `workflow_call` from the explorer | Maps `vX.Y.Z-<env>` tags to environments and enforces that an environment tag points at its base tag's commit |
| `Infrastructure CI` | PR, push | `tofu fmt`, `tofu validate` and `tofu test` for testnet and production (devnet is not validated in CI), the release-contract script test, TFLint, Trivy config scan, actionlint |

What runs on a tier is chosen by GitHub environment **variables**, not by
anything in git: `SQC_NODE_IMAGE` and `SQC_INDEXER_IMAGE` select the images,
and the genesis variables only take effect when a chain is first minted or
deliberately reset. The runbooks are `docs/chain-redeploy-runbook.md` (build,
set, roll, verify; the `reset_chain` decision rule) and
`docs/bootstrap-and-release.md`. Scripts worth knowing:
`scripts/verify-indexer.sh <rpc>`, `scripts/compare-parity.sh` (testnet vs
mainnet surface diff), `scripts/snapshot-balances.sh` (every address with its
balance, as a genesis allocation string, so a reset need not destroy
balances), `scripts/release-indexer.sh`.

Known gaps in that repository at the reviewed commit: its README still says
two environments where there are three; CI validates only testnet and
production; the Cloudflare Tunnel ingress and Access policy are managed by hand
(`scripts/cf-add-idx-route.sh`), not by OpenTofu; GitHub environment protection
rules are not in force on the current plan, so the mainnet reset guard is the
`reset_confirm` phrase; and two build artefacts are committed as files.

### `splendor-quantum-explorer` — the public window

A block explorer written for this chain's native types: `SQ:` addresses,
SHA3-256, ML-DSA-65. It is not Blockscout and speaks no Ethereum JSON-RPC.
React 19 server components on Vite (vinext), deployed as one Cloudflare Worker
per tier. Pages: home, blocks, block, transactions, transaction, address,
tokens, token, contracts (with QSol source verification and a whitelisted
read-only call panel), stats, search, API docs.

**Verification.** For every block page on a live tier the Worker fetches the
block from the node, hashes the header bytes exactly as received with
SHA3-256, requires the digest to equal the reported block hash, fetches block 0
to obtain the validator's 1,952-byte public key, requires that key to hash to
the header's validator address, and verifies the 3,309-byte signature under
the `SPLENDOR-QC-BLOCK-v1` context with `@noble/post-quantum`, which is
byte-compatible with the CIRCL implementation the node signs with. Known-answer
tests in `tests/pq-kat.test.mjs` use real captured testnet blocks. This runs
**in the Worker, server-side**; nothing in the visitor's browser verifies
anything, and the simnet tier reports `simulated` instead of a verdict.

**Data.** Live reads of current state (`/status`, `/block/*`, `/tx/*`,
`/balance`, `/nonce`, `/contract/*`, `POST /call`, `POST /contract/verify`) go
to the node; history (per-address transactions, tokens, contracts,
leaderboards, verified sources) comes from the indexer's `/idx/v1/*`. The
footer shows the **node's** version, image and commit, not the explorer's
release tag.

**Sign-in.** The "Connect wallet" control is Splendor Connect, not
WalletConnect: a QR session against `wallet-api.splendor.org` that the mobile
wallet signs with its ML-DSA-65 key under the `SPLENDOR-QC-MSG-v1` context. The
explorer checks that the returned public key hashes to the returned address and
fails closed. It is sign-in only; the explorer can never move funds.

**Deployment.** Git tags. `npm run version:patch` cuts `vX.Y.Z`;
`npm run deploy:{dev,devnet,test,prod}` pushes `vX.Y.Z-<env>`; the
`Release quantum explorer` workflow lints, runs the tests, validates the tag
against `splendor-infra`'s release policy, builds with `CLOUDFLARE_ENV=<env>`
and runs `wrangler deploy`. All four tiers ran `v0.0.31` at the snapshot. CI on
`main` is green with 90 tests. Its README's tier table omits the devnet tier
that its config, scripts and workflow all support.

### `splendor-quantum-wallet-extension` — the browser wallet

A Chrome Manifest V3 extension, `v1.3.0`, vanilla JavaScript. Keys are
ML-DSA-65, derived from a 32-byte seed the extension generates, and can be
backed up as a 24-word BIP-39 phrase or 64 hex characters. Up to 50 accounts
live under one master password (AES-GCM, PBKDF2) with a per-account
"backed up" flag. Signing happens in a byte-for-byte copy of this repository's
Go WASM signer (`sdk/sqc-sdk/wasm/dist`), pinned at load time by SHA-256; a
mismatch refuses to sign. Networks: mainnet, testnet, devnet and local, each
with its chain id asserted against the node's `/status` before a transaction is
signed.

It injects `window.splendor`, an EIP-1193-style provider announced through
EIP-6963, with `sqc_requestAccounts`, `sqc_accounts`, `sqc_getBalance`,
`sqc_sendTransaction`, `sqc_sign` (raw ML-DSA-65 message signing with its own
approval screen), `sqc_moduleCall` (QRC-20 deploy and transfer), `sqc_chainId`,
`sqc_blockNumber`, `sqc_getBlock`, `sqc_getTransaction`, `sqc_nonce`, and
per-origin `wallet_*` permission methods. Access control is derived from the
Chrome `MessageSender`, never from message fields. `REVIEW.md` records the
security fixes made while merging the multi-account and provider work.

**Distribution.** There is no Chrome Web Store listing. `npm run build` copies
the signer from a sibling checkout of this repository and re-pins it; the
result is loaded unpacked in `chrome://extensions`, or packaged with
`npm run zip`. CI checks that the shipped WASM matches its pins and matches
what this repository publishes; a scheduled `Sync signer from chain` workflow
opens a pull request, never a push, when the signer changes upstream.
157 unit tests passed and one was skipped at the reviewed commit; the seven
Playwright end-to-end tests in `e2e/extension.spec.js` are not run in CI.

**Caveats.** The Tokens tab asks the node for `GET /tokens/{address}`, which
no node serves, and shows an empty list. No HD derivation: each account is an
independent seed. No hardware or external signer. The repository has no README,
only `REVIEW.md`.

### `wallet` — the mobile wallet, and the Splendor Connect gateway

The Splendor mobile wallet is a React Native app (`4.17.8`, iOS and Android)
for the legacy Splendor mainnet (EVM chain 2691) that carries a **Quantum
Chain** section for this chain. The two halves are deliberately independent:
the quantum account is an ML-DSA-65 key generated from fresh device entropy,
never derived from the EVM phrase or key, and only *labelled* with the EVM
address it sits beside. Signing is `@noble/post-quantum` in pure TypeScript
under Hermes, with the Go wire JSON assembled by hand and locked by a Go golden
vector; no WebView and no WASM.

What the quantum section does, by file:

| Capability | Where |
|---|---|
| Create, import and unlock an independent ML-DSA-65 account; several per device | `src/quantum/quantumChainAccount.ts`, `src/quantum/quantumAccounts.ts` |
| Send and receive native SPLD, balance, nonce, history and tokens per tier | `src/quantum/quantumChain.ts`, `src/quantum/quantumNetworks.ts`, `src/screens/Quantum*.tsx` |
| Splendor Connect QR sign-in (EVM and quantum) and approval of remote signing requests: `sq_signMessage`, `sq_signOrder`, `sq_signPermit` | `src/utils/splendorConnectService.ts`, `src/utils/connectRequestListener.ts` |
| QRC-20 permits and exchange orders under `SPLENDOR-QC-PERMIT-v1` / `SPLENDOR-QC-ORDER-v1`, with the exchange address pinned per tier | `src/quantum/exchangeSigner.ts`, `src/quantum/pilotConfig.ts` |
| Encrypted backup of the seed to the NFC Splendor Card, and to a portable armored file, share sheet, QR, iCloud Keychain or Android Auto Backup | `src/quantum/cardRecord.ts`, `src/quantum/quantumChainCard.ts`, `src/quantum/backupBlob.ts`, `src/quantum/quantumCloudBackup.ts` |

Fresh installs default to **testnet**; quantum mainnet is a hidden override.
Releases are `npm run ios:testflight` (EAS build and TestFlight submit) and a
Play Console upload; no file in the repository states that the app is listed
on either store, so this README does not either. The mobile app has no CI; the
quantum, ML-DSA and Connect test subset (163 Jest tests; 182 with the Blockpeak
Credits suite) passed at the reviewed commit.
The app's own README still describes only the EVM wallet.

The same repository's `server/` is **`wallet-api.splendor.org`**, the Splendor
Connect gateway (Node 22, Hono, MongoDB, deployed with Coolify). It hosts
`splendor://connect` sessions (`POST /connect/session`, `POST /connect/verify`,
`GET /connect/me`, `POST /connect/logout`) and the remote signing-request
channel the wallet polls. Quantum sign-ins are verified with `@noble/post-quantum`
and the address is re-derived from the supplied public key. Browser origins are
allow-listed: the three explorers and the faucet are built in, and any other
origin (the dapps portal, for instance) must be added to
`CONNECT_ALLOWED_ORIGINS`. `GET /health` returns `{"ok":true}`; everything else
answers 401 without a session.

### `splendor-quantum-dapps` — the portal and the migration bridge

One TypeScript Cloudflare Worker at `dapps.splendor.org`: a landing page with
Bridge, Explorer and Faucet cards, and the bridge dapp under `/bridge`. The
bridge is the user-facing half of the one-way SPLD/SPLV migration designed in
`docs/plans/2026-08-31-quantum-migration-bridge.md`: the holder burns on the
legacy Splendor mainnet (chain 2691) through `QuantumMigrationBurn`
(`contracts/legacy-burn/`, deployed at
`0xe822e805dd83a6c0e6761cb3c743bb3ff272f1de`), naming an `SQ:` recipient; the
relayer in `cmd/bridge-relayer` waits for 120 confirmations and submits a
`bridge_v1` claim; the recipient is credited native SPLD 1:1. The relayer is an
operator-run process with the bridge operator key: `cmd/bridge-relayer/deploy/`
describes how to host it, and nothing in `splendor-infra` runs it today. The
Worker holds no keys and signs nothing. It reads the legacy chain over JSON-RPC for
confirmations and the quantum tier over `GET /bridge/{addr}` and
`GET /bridge/{addr}/claim/{id}` for pool and claim state, and exposes that as
`GET /bridge/api/info` and `GET /bridge/api/status/{txHash}`.

The production destination is the quantum **testnet** (`bridge_v1` at height
11,000; instance `SQ:643d2c15…aabee6`), and the page says so in a banner.
Mainnet has no `bridge_v1` fork. At the snapshot the bridge was configured,
unpaused, had credited nothing yet, and its pool held 5 SPLD; the production
Worker has no KV binding, so the per-IP cooldown on status polling fails open.
Deploys: `npm run deploy` or the `Deploy` workflow (`staging` or `production`),
gated on `tsc` and 95 Vitest tests.

### `splendor-quantum-faucet` — free test SPLD

One TypeScript Cloudflare Worker at `quantum-faucet.splendor.org`. `POST
/api/request {network, address, token}` validates the address shape, verifies
a Cloudflare Turnstile token server-side and IP-bound, reserves a slot in KV
(30-minute cooldown per address and per IP, 500 drips per network per UTC
day), then signs a real ML-DSA-65 transfer of 10 SPLD with
`@noble/post-quantum` over a hand-assembled Go-canonical body and posts it to
the tier's `/tx/submit`. A slot is released only on an explicit refusal by the
node, never on a timeout, so an induced timeout cannot buy a second drip.
Testnet and devnet only; mainnet is not selectable. One faucet key per tier,
and every drip is refused if the two seeds are identical. An optional
Splendor Connect sign-in prefills the address and buys nothing else. Deploys
are `npm run deploy` by a person with Cloudflare access; there is no CI. 151
Vitest tests, including a Go golden vector for the encoder, passed at the
reviewed commit. Its README still says 79 tests and a one-day cooldown; the
deployed configuration is 30 minutes.

### `splendor-smartcard` — the NFC Splendor Card

The JavaCard applet, personalisation tools and producer kit for the physical
card the mobile wallet backs a seed up to over NFC (NXP JCOP4 J3R150, contact
and NFC). The card **stores** an encrypted 4,096-byte record; it does not sign.
`docs/signing-card-feasibility.md` records why: the J3R150 has about 4 KB of
transient memory and no SHA-3, while ML-DSA-65 signing needs tens of kilobytes
and SHAKE, so on-card post-quantum signing waits for next-generation secure
elements. The hardware-verified applet is the plain storage `Blob4KApplet`, removed
from `main` in `a8287f7` and now found in git history and in the wallet's NFC
code (read and write over NFC from Android and iOS, about 1.6 s per direction); the
current `main` builds a passphrase-gated, retry-locked `VaultApplet` proven in
a jCardSim simulation (61 checks) but not yet recorded as flashed to hardware,
and the wallet still targets the older applet. The current card batch ships
with publicly known GlobalPlatform keys that must be rotated before any
production run. This is the "Option B" of §5, and it remains designed, not a
signing device.

### What is deliberately not in this list

- `splendor-quantum` (the marketing site at `quantum.splendor.org`) and the
  legacy Splendor mainnet stack (`explorer.splendor.org`,
  `mainnet-rpc.splendor.org`, chain 2691) are separate products; the bridge is
  the only place this chain touches them.
- `blockpeak-credits` and the Blockpeak settlement plan in `docs/plans/` are
  applications being built on the exchange module, not part of the chain.

---

## 20. Live Networks and Endpoints

Three chains run today, each with its own genesis, validator key, chain id and
fork schedule, and each with its own explorer and indexer. Everything in this
section was observed from the public endpoints on **2026-09-02 between
13:03 and 13:06 UTC**; heights climb, everything else should not change without
a release. Reachability at that instant is not an availability guarantee, and
an HTTP 200 does not establish correctness, freshness or production readiness.

### The tiers

| | Devnet | Testnet | Mainnet |
|---|---|---|---|
| Purpose | Sandbox; new releases land here first | Rehearsal; mainnet's base rules plus the four optional forks mainnet has not adopted, plus the bridge | Real balances; promoted images only |
| Chain id | `splendor-quantum-devnet-1` | `splendor-quantum-testnet-1` | `splendor-quantum-1` |
| Node RPC | `https://quantum-rpc-devnet.splendor.org` | `https://quantum-rpc-testnet.splendor.org` | `https://quantum-rpc.splendor.org` |
| Indexer read API | `https://quantum-rpc-devnet.splendor.org/idx/v1` | `https://quantum-rpc-testnet.splendor.org/idx/v1` | `https://quantum-rpc.splendor.org/idx/v1` |
| Explorer | `https://quantum-devnet-explorer.splendor.org` | `https://quantum-testnet-explorer.splendor.org` | `https://quantum-explorer.splendor.org` |
| Faucet | `https://quantum-faucet.splendor.org` | `https://quantum-faucet.splendor.org` | none, by design |
| Node image / commit observed | `sqc-node:v0.6.2` / `d2ed9cfa9f66` | `sqc-node:v0.6.1` / `2982523195d0` | `sqc-node:v0.6.1` / `2982523195d0` |
| Indexer image | `sqc-indexer:v0.2.14` | `sqc-indexer:v0.2.12` | `sqc-indexer:v0.2.11` |
| Height observed | 202,554 (13:05Z, after the `v0.6.2` rollout; 202,534 at 13:03Z still on `v0.6.1`) | 88,913 | 332,398 |
| Fork schedule (`/status.forks`) | `module_revert_v1`, `qrc20_v2`, `qrc1155_v1`, `exchange_v1` at 1; `vm_revert_v1` at 130,000 | `module_revert_v1`, `qrc20_v2`, `qrc1155_v1`, `exchange_v1` at 1; `bridge_v1` at 11,000 | none reported (`exchange_v1` deliberately omitted) |
| Genesis block hash | `fc83aa50c5eb011af9b72e7a61007b96af2034492020a0ae0b55bc865dc1e300` | `a52af7f86e77a50d3707452e7def8c7f4c46de9952597acaabfa8f1a5d76d775` | `80eb31e60b689f5dbb82ad3178b6f0bab9d768cb987dafc69607d05a25b083fc` |
| Genesis validator | `SQ:4fd3394ce33d1e2e7787b74028126181a22b9e68ffd7ba5511bcc321a99d4b09` | `SQ:09f44c419a48c70579b1a532c60e5786ecacf4de76e606d1901dfaf21e9745a9` | `SQ:442e905612311035b942f3362ea9d4efa632c1d2581d36356afd2f281afacf7e` |
| Peers reported | 0 (single validator) | 0 (single validator) | 0 (single validator) |

A fourth explorer, `https://splendor-quantum-simnet.splendor.org`, is a
deterministic in-process simulator, visibly labelled as such. It has no chain
behind it and nothing here can deploy to it.

Two things the public `/status` cannot tell you, and this README therefore does
not claim: the effective TLS key-exchange group (every tier is fronted by a
Cloudflare Tunnel, so the client-facing TLS is Cloudflare's), and the node's
plaintext-P2P and peer-pin flags. With a single validator per tier there is no
second peer for either to apply to.

Re-check any of it:

```bash
for base in https://quantum-rpc.splendor.org \
            https://quantum-rpc-testnet.splendor.org \
            https://quantum-rpc-devnet.splendor.org; do
  curl -fsS "$base/healthz"
  curl -fsS "$base/status" | jq '{chain_id,height,commit,image_tag,forks,peer_count}'
  curl -fsS "$base/block/0" | jq '{hash, validator: .header.validator_addr}'
  curl -fsS "$base/idx/v1/health"
done
npm run release:status        # the same, from the release tool's point of view
```

### The node's RPC surface

One HTTP listener per node (`--rpc-addr`), REST plus one server-sent-event
stream. There is no Ethereum JSON-RPC and no `eth_*` method. Full request and
response shapes are in `rpc/` and the SDK; the wire gotchas at the end of this
section matter for anyone not using the SDK.

| Route | Purpose |
|---|---|
| **Chain and state (current only)** | |
| `GET /status` | `chain_id`, `height`, `commit`, `image_tag`, `node_version`, `forks`, `mempool_size`, `peer_count` |
| `GET /healthz`, `GET /readyz` | Liveness with build identity; readiness |
| `GET /block/latest`, `GET /block/{height}`, `GET /block/hash/{hash}` | Signed block with header, hash, validator signature and transactions |
| `GET /tx/{hash}`, `GET /tx/{hash}/receipt` | Transaction by SHA3-256 hash; execution receipt. Append `?amounts=string` for quoted big integers |
| `GET /balance/{address}`, `GET /nonce/{address}` | Current state; no historical query exists |
| `GET /logs` | Contract event logs for a block range (VM-executing route, separately rate-limited) |
| `GET /ws` | Server-sent events: new heads, for wallets and the explorer |
| `GET /peers`, `GET /ai/stats` | Known peers; the scheduler's batch size, moving averages and anomaly flag (§17) |
| **Submitting** | |
| `POST /tx/submit` | A signed transaction. Rate-limited per IP; stale nonces are refused with `400 nonce too low` |
| `POST /x402/verify`, `POST /x402/settle` | X402 payment authorization check; settlement with the payer-signed `settlement_tx` (§11) |
| **Contracts and the VM** | |
| `POST /call` | Read-only call: a native-module envelope or SQC-VM bytecode, executed against current state |
| `POST /estimate` | Gas and fee estimate for a call or deploy |
| `GET /contract/{address}`, `…/code`, `…/storage/{slot}` | Contract record, bytecode, one storage slot |
| `POST /contract/verify` | Compile a QSol source and compare byte-for-byte with deployed code |
| `POST /vm/validate`, `POST /vm/disasm`, `POST /vm/estimate-gas`, `GET /vm/opcodes` | Bytecode inspection; 133 opcodes today |
| **Native modules with a write path (fork-gated, §8)** | |
| `GET /dex/pool/{t0}/{t1}`, `…/quote`, `…/lp/{addr}`, `GET /dex/price/{t0}/{t1}`, `GET /dex/amount-in`, `GET /dex/amount-out` | DEX pools, reserves and quotes |
| `GET /exchange/{addr}`, `…/condition/{id}`, `…/order/{hash}`, `…/nonce/{maker}`, `…/position/{condition}/{holder}` | Exchange instance, conditions, orders, positions |
| `GET /bridge/{addr}`, `GET /bridge/{addr}/claim/{id}` | Migration-bridge instance and claim status |
| **Library-only reads** | |
| `GET /gov/*`, `GET /lending/*` | These read module state that no transaction can currently write: the router does not expose governance or lending (§8). They return empty or default values on every public tier |
| `GET /staking/validators/{addr}`, `…/total-staked`, `…/count`, `…/tiers` | Validator bonds. Writable only on a tier that schedules `validator_gates_v1`; before that they read a registry nothing can write. The richer views (`pass`, `can_produce`, `validator_set`) are `POST /call` module reads |
| **Node-to-node, not for clients** | |
| `POST /block`, `POST /peers/announce`, `GET /sync`, `GET /p2p/identity` | The sealed write plane. `POST` routes require the `X-Node-Auth` shared token (401 without it); a node with no `--rpc-auth-token` answers 503; with plaintext P2P off, as on the public tiers, `POST /block` answers 410 and only the sealed transport is accepted |

`/metrics` (Prometheus) is served by a separate listener, `--metrics-addr`,
loopback by default. It is not exposed on the public hostnames, and asking for
it there returns 404.

### The indexer's read API

Each tier's host also runs `sqc-indexer` (Go, in `splendor-infra/deploy/indexer`),
which polls the node, writes every block, transaction, receipt and event log to
Postgres, and serves the history the node itself cannot: per-address
transaction lists, token and contract listings, search. The Cloudflare Tunnel
path-routes `/idx/*` on the same hostname to the indexer, so explorer read
traffic never touches the validator process.

| Route (under `/idx/v1`) | Purpose |
|---|---|
| `/health` | `ok`, `checkpoint` (last indexed height), `version`, and on newer builds the indexer's own commit and image tag |
| `/stats` | Head, totals, blocks per second, transactions by kind, daily and hourly counts, contract and token counts |
| `/transactions?limit=`, `/tx/{hash}` | Recent transactions; one transaction with receipt |
| `/address/{addr}?limit=` | Transaction history for one address |
| `/search?q=` | Height, hash or address lookup |
| `/tokens`, `/token/{addr}`, `/contracts`, `/contract/{addr}` | QRC-20 tokens, deployed contracts seen on chain, and one contract's interface descriptor with its verified source |
| `/logs?address=&topic0=&from_block=&to_block=` | Event-log query |
| `/top-addresses`, `/largest-transfers` | Leaderboards |
| `/stream` | Server-sent events on every newly indexed head |
| `/accounts?limit=&after=&balances=true`, `/accounts/alloc` | Every address ever seen, optionally with live balances from the node; the same list as a genesis allocation, used to make a chain reset non-destructive |

The indexer stores no balances: address sets come from Postgres, every balance
comes live from the node, because current state is authoritative there.

### The other live services

| Service | URL | What it is | Observed |
|---|---|---|---|
| Faucet | `https://quantum-faucet.splendor.org` | Test SPLD on testnet and devnet; Turnstile plus per-address, per-IP and per-day limits; signs a real ML-DSA-65 transaction inside a Cloudflare Worker. Mainnet is not selectable | 200 |
| Dapps portal | `https://dapps.splendor.org` | Entry page linking Bridge, Explorer and Faucet; `GET /health` returns `{"ok":true,"service":"splendor-dapps"}` | 200 |
| Migration bridge | `https://dapps.splendor.org/bridge` and `…/bridge/api/info`, `…/bridge/api/status/{txHash}` | Burn SPLD or SPLV on the legacy Splendor mainnet (chain 2691, burn contract `0xe822e805dd83a6c0e6761cb3c743bb3ff272f1de`), get native SPLD 1:1 on the quantum **testnet** (bridge instance `SQ:643d2c15cbc0cf922c2054243d0724afd0073d0c1169b981967d81b4c9aabee6`, 120 confirmations). The Worker holds no keys; the relayer in `cmd/bridge-relayer` makes the claim. The UI labels the destination as TESTNET; mainnet `bridge_v1` is not activated | 200, configured |
| Splendor Connect gateway | `https://wallet-api.splendor.org` | QR sign-in and signing-request relay between the mobile wallet and the explorer, faucet and dapps (`/health` → `{"ok":true}`; everything else needs a session). Served from the `wallet` repository's `server/` | 200 |
| Explorers | the four hostnames above | React app on Cloudflare Workers that re-verifies each block's ML-DSA-65 signature server-side in the Worker | 200 on all four |
| Website | `https://quantum.splendor.org` | Marketing site (`splendor-quantum` repository, Coolify) | 200 |

### Wire format gotchas

The node hashes and signs `json.Marshal` output from Go, and the REST bodies
are that same encoding. Anyone not using `@splendor/sqc-sdk` or the extension
must reproduce it exactly:

- `from`, `to`, `validator_addr`, hashes and roots are `[32]byte` and arrive
  as **arrays of 32 numbers**, not `"SQ:…"` or hex strings.
- `amount` and `fee` are `*big.Int` and arrive as **bare JSON numbers** above
  2⁵³; parse with a big-integer-aware decoder or request `?amounts=string`.
  On the way in, `/tx/submit` and the X402 routes accept decimal strings.
- `data` is `[]byte`: base64, and absent entirely when empty.
- Field order is struct order. Re-ordering or re-indenting the JSON changes the
  hash and invalidates the signature.
- Every signature carries a domain-separation context: `SPLENDOR-QC-TX-v1`
  for transactions, `SPLENDOR-QC-BLOCK-v1` for blocks, `SPLENDOR-QC-MSG-v1`
  for Splendor Connect sign-in messages, so a login signature can never be
  replayed as a transfer.

---

## 21. How a Release Reaches a Tier

One command ships the node. Everything else in this section explains what that
command does, why each step exists, and what it refuses to do.

```bash
npm run deploy:devnet          # cut the next patch version and roll it to devnet
npm run deploy:test            # …to testnet   (GitHub environment "test")
npm run deploy:prod            # …to mainnet   (GitHub environment "prod", asks first)
npm run deploy v0.6.2 prod     # promote an image that already exists — no rebuild
npm run release:status         # what every tier is running right now
```

`npm run deploy:<tier>` is `node scripts/release.mjs patch <tier>`. The script
lives in this repository, but the workflows it drives live in
[`splendor-infra`](https://github.com/Splendor-Protocol/splendor-infra). It
needs a clean checkout of `main` that matches `origin/main`, and a `gh` login
that belongs to the `Splendor-Protocol` organisation with the `workflow` scope
(`gh auth refresh -h github.com -s workflow`). Nothing else: no AWS keys, no SSH,
no Docker on the laptop.

### The names

The GitHub environment names do not match the hostnames, and the script accepts
the words people actually type:

| You type | GitHub environment | Tier | RPC it verifies against |
|---|---|---|---|
| `devnet`, `dn` | `devnet` | devnet | `https://quantum-rpc-devnet.splendor.org` |
| `test`, `testnet`, `tn` | `test` | **testnet** | `https://quantum-rpc-testnet.splendor.org` |
| `prod`, `mainnet`, `main`, `production` | `prod` | **mainnet** | `https://quantum-rpc.splendor.org` |

`dev`/simnet is deliberately not a target: it is a simulated explorer with no
chain behind it, so there is nothing to roll.

### What one run does, in order

This is the run that produced devnet `v0.6.2` on 2026-09-02, as the script
prints it. The build progress line is abridged, and the two heights are the
ones recorded just before and just after the run:

```
$ npm run deploy:devnet

  version    v0.6.2
  commit     d2ed9cfa9f66b6c0151c0e204b0588e8372a10ef
             Merge pull request #23 from Splendor-Protocol/docs/readme-evidence-a
  tier       devnet  (GitHub environment "devnet")
  rpc        https://quantum-rpc-devnet.splendor.org
  currently  ghcr.io/splendor-protocol/sqc-node:v0.6.1

  indexer    ghcr.io/splendor-protocol/sqc-indexer:v0.2.14  (applied by this rollout too)

  ✓ tagged v0.6.2 -> d2ed9cfa9f66
  … building v0.6.2
    [04:00]  44%  Build image (linux/arm64)
    done in 06:40
  ✓ image ghcr.io/splendor-protocol/sqc-node:v0.6.2 built from d2ed9cfa9f66
  ✓ published release v0.6.2
  ✓ devnet -> v0.6.2   (was ghcr.io/splendor-protocol/sqc-node:v0.6.1)
  … rolling out to devnet
    done in 00:50
  ✓ height 202534 -> 202554, chain splendor-quantum-devnet-1

✓ devnet is running v0.6.2 (d2ed9cfa9f66)
  verified against https://quantum-rpc-devnet.splendor.org/status — not merely dispatched.
```

Step by step:

1. **Preflight.** The working tree must be clean, the branch must be `main`, and
   `HEAD` must equal `origin/main` after a fetch. On 27 August 2026 `main` was
   force-pushed backwards and the tiers kept serving code that no longer existed
   in the repository; refusing to release an unpushed commit is what prevents
   creating that state on purpose. `gh api user` must succeed.
2. **Find out what the tier runs.** The script reads the `SQC_NODE_IMAGE`
   variable from the tier's GitHub environment in `splendor-infra`. That
   variable is the single source of truth for "which image is this tier on":
   the deploy workflow reads nothing else.
3. **Pick the version.** `patch`/`minor`/`major` bumps the highest version seen
   across *both* the three tier variables and the local `v*` tags, because
   the early image tags `v0.5.0` and `v0.5.1` were chosen by hand and have no
   git tag (`v0.4.8`–`v0.4.12` were tagged retroactively on 27 August). A literal `vX.Y.Z` means *promote*: reuse that image and skip the build.
4. **Resolve the deployed commit.** First by git tag; failing that, by asking
   the tier's `/status`, which reports the commit stamped into the binary. If
   neither works the change checks below are skipped, and the script says so.
5. **Guards.**
   - *Is there anything to ship?* If no `*.go`, `go.mod`, `go.sum` or
     `Dockerfile` changed between the deployed commit and `HEAD`, the release is
     refused. A rebuild is not provably a no-op — the Dockerfile runs
     `go get ./... && go mod tidy` on a floating `golang:1.25-alpine` base — so
     a pointless rebuild is the one way to change a consensus binary with no
     source change behind it. `--allow-no-op` overrides.
   - *Is this a rollback?* Promoting a lower version than the tier runs is
     allowed but never silent: the script prints the Go files that differ (the
     first twelve), because the older binary now has to read what the newer one
     wrote.
   - *Does something else have to ship?* Changes to
     `core/types/transaction.go`, `core/types/block.go`,
     `core/crypto/mldsa.go` or `genesis/genesis.go` are named with the
     downstream owner (wallet signer, faucet, SDK, explorer, infra entrypoint).
     The tool ships the node only.
   - *Is the indexer variable set?* The rollout applies `SQC_INDEXER_IMAGE`
     too, whether or not anyone meant it to, so it must exist.
6. **Confirm production.** `prod` asks `Release vX.Y.Z to PRODUCTION? (y/N)`
   on a TTY; `--yes` skips it; a non-interactive prod release without `--yes`
   is refused. `--dry-run` stops here having mutated nothing.
7. **Tag.** An annotated `vX.Y.Z` tag on `HEAD`, pushed as one ref. For a
   promotion there is no tag and no build; instead the script requires that a
   GitHub Release or another tier proves the image exists (`--dry-run` stops
   before this check). An atomic
   multi-ref push where one ref already existed produced *no* workflow run on
   26 August while reporting success, so it is one ref per push.
8. **Build.** Dispatches the `Build sqc-node image` workflow in
   `splendor-infra` with `chain_ref=vX.Y.Z` and `image_tag=vX.Y.Z`. That
   workflow checks this repository out at the tag (read-only deploy key),
   cross-builds the Dockerfile for `linux/arm64` under QEMU (the hosts are
   Graviton), stamps the full commit and tag into the binary via
   `core/buildinfo` (which `/status` and `/healthz` report), checks the image
   contains `sqc-node`, `sqc-wallet` and `wget`, and pushes
   `ghcr.io/splendor-protocol/sqc-node:vX.Y.Z`. Seven to nine minutes (6:40 in
   this run). The script
   identifies its own run by snapshotting run ids before dispatch and taking the
   difference, and then reads the build log to assert the full commit SHA
   appears in it.
9. **Publish a GitHub Release** on this repository with the commit, the image,
   and the commit subjects since the previous version tag. A failure here is
   reported and does not abort the release.
10. **Record the height** the tier is at before anything changes.
11. **Set `SQC_NODE_IMAGE`** in the tier's GitHub environment to the new image
    and read it back. This is the step that used to be forgotten: build and
    deploy without it is a successful-looking no-op that re-pulls the old image.
12. **Open a GitHub Deployment** on this repository for the tier, so "which
    version is on which tier" is answerable where the version lives.
13. **Roll out.** Dispatches `Deploy quantum infrastructure` with
    `environment=<env> rollout_node=true apply_infrastructure=false
    reset_chain=false`. That workflow assumes the tier's AWS role over OIDC,
    skips `tofu apply`, and runs `scripts/rollout-node.sh`: it writes the
    runtime env (image tags, wallet passphrase, tunnel token, Postgres password,
    genesis variables) to an SSM SecureString, ships `deploy/node/` to
    `/srv/splendor` on the host with SSM Run Command, and runs
    `bootstrap-host.sh`, which is `docker compose pull && up -d` over the whole
    stack. Compose recreates whichever containers' image or environment
    changed: normally just `sqc-node` and `heartbeat` (both run
    `SQC_NODE_IMAGE`), plus `indexer` if `SQC_INDEXER_IMAGE` moved and
    `cloudflared` if a newer `:latest` was pulled; `postgres` keeps running.
    About one minute, with a few seconds of block-production pause during the
    container swap.
14. **Verify live.** Polls `/status` for up to 100 seconds until it reports the
    released commit (a matching `image_tag` only shortens the wait), and
    requires the height to be above the value recorded in step 10. A wrong
    commit or a non-climbing height fails the release. A green workflow only
    means the container was replaced; this is what means the tier is running
    the release and still sealing blocks. The Deployment record is closed as
    `success` only after this passes, `failure` otherwise.

### What it never does

- **It never resets a chain.** `reset_chain` is always sent `false`. A reset
  wipes state, wallet and genesis, and destroys every balance not in the
  tier's `SQC_GENESIS_ALLOC_ADDRESS` list. That is a separate, deliberate
  procedure in `splendor-infra/docs/chain-redeploy-runbook.md`, and the deploy
  workflow additionally refuses a prod reset unless `reset_confirm` is the
  exact phrase `reset prod chain`.
- **It never applies infrastructure.** `apply_infrastructure=false`. Changing
  the AWS stack (`stacks/quantum`, `environments/<tier>/`) is done from
  `splendor-infra` directly.
- **It never adopts a fork.** Activating a new consensus rule on a running chain
  (§8 of this README, and "Fork Schedule" under §6 of `DEPLOY.md`) is the deploy workflow's
  `allow_fork_extension` input together with the tier's `SQC_GENESIS_FORKS`
  variable. It is dispatched by hand, once, and turned off again.
- **It never rolls the indexer on its own.** A new `sqc-indexer` is
  `Build sqc-indexer image` plus `SQC_INDEXER_IMAGE`, and the next node rollout
  carries it (`splendor-infra/scripts/release-indexer.sh`).

### Promotion, rollback, and testing a branch

The usual order is devnet, then testnet, then mainnet, promoting the same image
rather than rebuilding:

```bash
npm run deploy:devnet            # builds v0.6.3, rolls devnet
npm run deploy v0.6.3 test       # same image to testnet
npm run deploy v0.6.3 prod       # same image to mainnet, after the prompt
```

A rollback is a promotion to a lower version and is printed as such. If no Go
source differs between the two versions it is safe; if it does, the older
binary must be able to read a chain the newer one has been writing, and the
script lists up to twelve of the files to check. Storage-format, state-transition or header
changes make a rollback unsafe without a reset.

To prove a pull request on the live testnet chain *without* merging it, build a
throwaway image straight from the branch and point testnet at it by hand
(`splendor-infra` README, "Test a PR on testnet before merging"). Never run a
branch image on mainnet.

### The other deployables

| What | How it ships | Where the truth lives |
|---|---|---|
| Explorer | `npm run version:patch` cuts `vX.Y.Z`; `npm run deploy:{dev,devnet,test,prod}` pushes `vX.Y.Z-<env>`; the `Release quantum explorer` workflow lints, tests, validates the tag against `splendor-infra`'s `release-policy.yml` (an environment tag must point at its base tag's commit), builds with `CLOUDFLARE_ENV=<env>` and runs `wrangler deploy` | `splendor-quantum-explorer/scripts/deploy.mjs`, `wrangler.jsonc` |
| Indexer | `Build sqc-indexer image` workflow → set `SQC_INDEXER_IMAGE` → next node rollout | `splendor-infra/deploy/indexer`, `scripts/release-indexer.sh` |
| Faucet | `npm run deploy` (`wrangler deploy --env production`), run by a person with Cloudflare access; secrets via `wrangler secret put` | `splendor-quantum-faucet/README.md`, "Deploy" |
| Dapps portal + bridge UI | `npm run deploy`, or the `Deploy` workflow (`workflow_dispatch`, `staging`/`production`) | `splendor-quantum-dapps/.github/workflows/deploy.yml` |
| Browser extension | `npm run build` copies the signer WASM from this repository's `sdk/sqc-sdk/wasm/dist` and re-pins its digest; load unpacked in `chrome://extensions`, or `npm run zip`. No store listing. A scheduled `Sync signer from chain` workflow opens a PR when this repository publishes a different signer | `splendor-quantum-wallet-extension/scripts/sync-signer.mjs` |
| Mobile wallet | `npm run ios:testflight` (EAS build + TestFlight submit); `build-apk.sh` for Android | `wallet/README.md`, "Releasing iOS to TestFlight with EAS" |
| Splendor Connect gateway | Coolify, from `wallet@main` (`server/` + `admin/` compose) | `splendor/infra/SERVICES.md` (internal) |

### Fork activation on a running chain

A tier's rules are its persisted fork schedule (`forks` in `/status`). Adding a
rule to a running chain is an extension, never an edit: the new upgrade must
activate above the current head, the deploy workflow must be dispatched with
`allow_fork_extension=true` once, and the tier's `SQC_GENESIS_FORKS` variable
must carry the full schedule. Testnet's `bridge_v1` at height 11,000 was
activated this way. Mainnet reports no optional forks and, by decision, omits
`exchange_v1`.

---

## 22. Repository Layout and Developer Quick Start

### What lives where

| Path | What it is |
|---|---|
| `cmd/node` | `sqc-node` — the full node and validator. Flags, not a config file; the reference is `DEPLOY.md` §5 |
| `cmd/wallet` | `sqc-wallet` — CLI wallet: generate, address, balance, send (`--chain-id` per tier), encrypted `.sqwallet` files |
| `cmd/sqc-solc` | QSol-subset compiler CLI: `.qsol` → SQC-VM v4 hex + ABI JSON |
| `cmd/sqc-deploy` | Deploy, call, view and source-verify QSol contracts against a live node over RPC |
| `cmd/bridge-relayer`, `cmd/bridge-admin` | The migration bridge: relayer that watches the legacy-chain burn contract and claims on the quantum tier; turnkey instance bring-up |
| `cmd/loadtest`, `cmd/auditprobe` | Throughput and security probe harnesses; not shipped in the image |
| `core/crypto` | ML-DSA-65, SHA3-256, addresses, ACVP vectors, `.sqwallet` store (Argon2id + AES-256-GCM) |
| `core/types`, `core/block`, `core/state`, `core/mempool`, `core/forks`, `core/buildinfo` | Transactions and blocks, execution and receipts, whole-state Merkle commitment, fee-ordered mempool, fork schedule, the commit/tag stamp `/status` reports |
| `consensus` | Single-validator block production, gated on the producer's admission pass and bond under `validator_gates_v1`; `bft.go` is explicitly incomplete and unwired |
| `network` | ML-KEM-768 + AES-256-GCM sealed P2P, peer pins, announce signatures, SSRF guards |
| `rpc` | The REST/SSE surface in §20, TLS with the `X25519MLKEM768` hybrid group, rate limits, metrics |
| `vm` | SQC-VM interpreter and disassembler, plus the native Go modules (QRC-20, QRC-1155, DEX, exchange, bridge, staking; lending/governance/oracle/multisig/QRC-721/QRC-4626 as unrouted library code) |
| `compiler` | The QSol-subset compiler (lexer, parser, codegen, assembler) and `examples/*.qsol` |
| `x402` | X402 payment authorization and settlement binding |
| `genesis` | Signed height-zero genesis builder and fork-schedule validation |
| `ai` | The adaptive batch-size scheduler (§17) |
| `sdk/sqc-sdk` | `@splendor/sqc-sdk` v2: Node/browser SDK, the Go signer package (`signer/`), the WASM build and its single committed artifact (`wasm/`), gateway, SQDP-1 protocol docs |
| `sdk/connect-react` | `@splendor/connect-react`: React connect UI kit |
| `sdk/bridge` | `@splendor/pq-bridge`: deprecated re-export shim, nothing else |
| `sdk/sqc-sdk.js` | Legacy single-file SDK, deprecated; its X402 middleware omits `settlement_tx` (§11) |
| `contracts/legacy-burn` | `QuantumMigrationBurn`, the Solidity burn gate on the legacy Splendor mainnet (chain 2691) |
| `docs/modules` | Wire formats: `qrc20-permit.md`, `exchange.md`, `bridge.md` |
| `docs/performance` | The dated TPS/GPU experiment behind every throughput number in this file |
| `docs/plans` | Design documents: compiler, migration bridge, Blockpeak settlement module |
| `docs/readme-claim-evidence.md` | The claim–evidence matrix and dated deployment snapshots behind this README |
| `scripts/release.mjs` | The release tool described in §21 |
| `DEPLOY.md` | Bare-metal operator guide: build, validator setup, every node flag, genesis and fork schedule, systemd, firewall |
| `HARDENING-REPORT.md` | The 25 August 2026 hardening pass: what was asked, what was done, what was found |
| `Dockerfile`, `install.sh`, `Makefile`, `sqc` | The image the tiers run; a fresh-Ubuntu installer; build/test targets; a node management wrapper (`sqc status\|logs\|console\|update`) |

### Build, test, run

```bash
# toolchain: Go 1.25 (circl v1.6.5 requires it); Node 20+ for the release tool, Node 22.20+ for the SDK
make build                      # ./sqc-node and ./sqc-wallet
make test                       # go test -v -count=1 ./...
make test-crypto                # only core/crypto (ML-DSA, ML-KEM, ACVP vectors, wallet store)

# a local single-validator chain
./sqc-wallet generate --out validator.sqwallet          # prompts for a password
SQC_WALLET_PASS=… ./sqc-node --validator --wallet validator.sqwallet \
    --genesis genesis.json --data-dir ./sqc-data        # DEPLOY.md §6 for genesis.json
curl -s http://127.0.0.1:8080/status

# contracts
go run ./cmd/sqc-solc compiler/examples/token.qsol      # bytecode hex + ABI
go run ./cmd/sqc-deploy -rpc https://quantum-rpc-devnet.splendor.org -seed seed.hex \
    deploy compiler/examples/counter.qsol

# SDK
cd sdk/sqc-sdk && npm ci && npm test && npm run build   # 109 vitest tests at the reviewed commit
```

`DEPLOY.md` is the operator document for running a node outside the managed
tiers: prerequisites, validator wallet, genesis, every flag, systemd units,
firewall rules and troubleshooting.

### Continuous integration

Two workflows run on every push to `main` and every pull request
(`.github/workflows/`):

- **CI** — `go build`, `go vet`, `gofmt` on changed files, `go test -count=1
  ./...`; then a **signer-drift** job that rebuilds the WASM signer from source
  under the pinned Go toolchain and fails if the committed
  `sdk/sqc-sdk/wasm/dist/sqc_signer.wasm`, any per-network digest pin, or the
  golden address vector disagrees with the rebuild. That file is the only
  committed copy of the signer; the browser extension syncs from it and checks
  its own copy against it in its own CI. The signer has drifted from
  its source twice before this job existed.
- **Force-push alarm** — detects a non-fast-forward rewrite of `main`, the
  failure that on 27 August 2026 left the tiers serving commits the repository
  no longer contained.

CI at the commit this README was written against
(`d2ed9cfa9f66b6c0151c0e204b0588e8372a10ef`) is green. A green CI run is
evidence for that commit only; it is not an external audit.

---

*Splendor Quantum Chain is a new Layer-1 with native ML-DSA-65 ledger
identities, SHA3-256 commitments, ML-KEM-sealed peer transport, SQC-VM, a
prototype QSol compiler, and an ML-DSA-authorized X402 settlement path. It is
also an early-stage, single-validator system requiring decentralization,
hardening, audits, tooling, and certification before institutional production
use.*
