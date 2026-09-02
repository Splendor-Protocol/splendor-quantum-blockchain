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
`DispatchModuleCall` (`vm/module_router.go`) routes five modules; the
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

A fifth name, `module_revert_v1`, is not a module but the failure rule: under
it a module call that errors is included with a failed receipt (fee kept,
nonce consumed) instead of being dropped, so a relayer whose call fails cannot
jam its own account. The wire formats are in `docs/modules/qrc20-permit.md`
and `docs/modules/exchange.md`.

`vm/lending.go`, `vm/staking.go`, `vm/governance.go`, `vm/oracle.go`,
`vm/multisig.go`, `vm/qrc721.go` and `vm/qrc4626.go` are written and tested,
and their `/lending/*`, `/staking/*` and `/gov/*` RPC routes read their state,
but no transaction reaches them: until the router learns those module names,
they are library code, not a live surface.

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

### Phase 2 and Beyond: Expanding Validators

The intended future design would add:

- New validators register by staking SPLD and registering their ML-DSA-65 public key
- Consensus upgrades to a BFT protocol (all signatures still ML-DSA-65)
- Governance is handled by token holders (voting via ML-DSA-65-signed transactions)

None of that is wired into the running node. `consensus/bft.go` is explicitly
incomplete, there is no production path for a second independent validator to
join, and a passed governance proposal cannot change consensus: `Execute` in
`vm/governance.go` marks a proposal Executed and applies nothing else.

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
- Fork-gated QRC-1155, exchange, and migration-bridge actions
- X402 authorization and payer-signed settlement
- Contract events and current-state reads

The live availability of a fork-gated action depends on the tier's persisted
fork schedule. Lending, staking, governance, oracle, multisig, QRC-721, and
QRC-4626 files exist, but the transaction router does not expose them. They are
not product features merely because library code and read routes exist.

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

At `2026-09-02T08:47:25Z`, the public devnet, testnet, and mainnet status
endpoints responded and their explorers returned HTTP 200. All three nodes
reported commit `2982523195d00d04d45d0ddf829b4c0af13c4826`, which is older than
this README commit, and `peer_count: 0`. Mainnet reported no optional fork
schedule; testnet reported QRC-20 v2, QRC-1155, exchange, module-revert, and
bridge forks; devnet reported those except bridge plus VM-revert. This is a
dated observation, not a continuous-availability guarantee. URLs, genesis
hashes, validator fingerprints, heights, and methods are recorded in the
[claim–evidence matrix](docs/readme-claim-evidence.md).

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
  consistency, release reproducibility, and complete CI coverage. In the
  reviewed checkout, the canonical SDK's committed `wasm_exec.js` digest is
  stale and its prebuild step rewrites the config to the actual artifact hash;
  that generated correction must be committed in a separate code PR

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

*Splendor Quantum Chain is a new Layer-1 with native ML-DSA-65 ledger
identities, SHA3-256 commitments, ML-KEM-sealed peer transport, SQC-VM, a
prototype QSol compiler, and an ML-DSA-authorized X402 settlement path. It is
also an early-stage, single-validator system requiring decentralization,
hardening, audits, tooling, and certification before institutional production
use.*
