# Splendor Quantum Chain

### A blockchain where quantum resistance is not an add-on — it is the foundation.

> **This is not a fork. This is not an upgrade. This is a new chain — designed from block zero so that everything the chain itself signs, hashes, and addresses is post-quantum. Every transaction signature, every address, every block hash, every validator message uses ML-DSA-65 and SHA-3-256. On-chain there is no ECDSA, no secp256k1, no SHA-256. Off-chain — browsers, proxied TLS, the optional MetaMask derivation path — is a different story, and the next section says exactly where that line falls.**

---

## What Is and Is Not Post-Quantum

Read this before the rest of the document. The line is narrower than the
marketing instinct wants it to be, and it is still the strongest thing about
this chain: everything the chain *commits to* is post-quantum, end to end,
including the wire between nodes.

### Post-quantum today

| Surface | Mechanism | Where in the code |
|---|---|---|
| Transaction, block, X402, vote, oracle, permit and multisig signatures | ML-DSA-65 (NIST FIPS 204) via CIRCL, each with its own domain-separation context | `core/crypto/mldsa.go` |
| Addresses | `SQ:` + hex(SHA3-256(ML-DSA-65 public key)) | `core/crypto/` |
| Transaction hashes, block hashes, Merkle nodes, contract storage slot keys | SHA-3-256 (NIST FIPS 202) | throughout |
| Node-to-node block push, peer announce, **and catch-up sync** | ML-KEM-768 (NIST FIPS 203) + AES-256-GCM sealed channel; plaintext refused unless `--allow-plaintext-p2p` | `network/pqchannel.go`, `network/sync.go` |
| RPC/WebSocket transport **on the hop the node terminates itself** | TLS 1.3 offering the `X25519MLKEM768` hybrid group | `rpc/tls.go`, `cmd/node/main.go` |
| Wallet file at rest | AES-256-GCM with an Argon2id-derived key, minimum parameters enforced at startup | `core/crypto/wallet_store.go` |

### Not post-quantum

- **TLS the node does not terminate.** A reverse proxy in front of the node —
  nginx with Let's Encrypt, a CDN, a tunnel — negotiates its own connection with
  the client, which today means classical ECDHE. The hybrid group applies only
  to a connection the node serves directly via `--tls-cert`. Every deployment
  that terminates TLS at a proxy has a classical key exchange in front of it.
- **The browser SDK's MetaMask derivation path.** `sdk/bridge/src/derive.ts`
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

`core/crypto/acvp_test.go` runs an unmodified subset of the NIST ACVP vectors
for ML-DSA-65 and ML-KEM-768, so the implementation matches what NIST specified
rather than merely matching its own past output. That is a conformance test, not
a validation certificate: there is no CAVP/CMVP submission, no FIPS 140-3 module
boundary, and no third-party audit of this codebase. "Passes the published
vectors" and "certified" are different claims, and only the first is made here.

---

## Table of Contents

1. [What We Are Building and Why](#1-what-we-are-building-and-why)
2. [The Problem With Every Other Blockchain](#2-the-problem-with-every-other-blockchain)
3. [The New Architecture — Quantum Native From Block Zero](#3-the-new-architecture--quantum-native-from-block-zero)
4. [Addresses — How They Work in a Quantum Chain](#4-addresses--how-they-work-in-a-quantum-chain)
5. [Wallets — Creating One, Using One, Securing One](#5-wallets--creating-one-using-one-securing-one)
6. [Full User Journey — From Zero to Sending Funds](#6-full-user-journey--from-zero-to-sending-funds)
7. [Transactions — How They Are Created, Signed, Broadcast, Recorded](#7-transactions--how-they-are-created-signed-broadcast-recorded)
8. [Smart Contracts — Fully Quantum-Native](#8-smart-contracts--fully-quantum-native)
9. [Consensus — One Validator, Built to Scale](#9-consensus--one-validator-built-to-scale)
10. [Record Keeping — The Quantum Ledger](#10-record-keeping--the-quantum-ledger)
11. [X402 — Native Payment Protocol](#11-x402--native-payment-protocol)
12. [The Ecosystem — DApps, DeFi, NFTs, AI Agents](#12-the-ecosystem--dapps-defi-nfts-ai-agents)
13. [Cryptographic Primitives Used (Technical Reference)](#13-cryptographic-primitives-used-technical-reference)
14. [What Makes This Different — Comparison Table](#14-what-makes-this-different--comparison-table)
15. [The Business Case for Investors](#15-the-business-case-for-investors)
16. [What Is Built vs What Comes Next](#16-what-is-built-vs-what-comes-next)
17. [AI at the Core — The Intelligent Node](#17-ai-at-the-core--the-intelligent-node)
18. [Banking and Financial Institutions](#18-banking-and-financial-institutions--the-perfect-fit)

---

## 1. What We Are Building and Why

Splendor Quantum Chain is a new Layer-1 blockchain built from scratch with one rule: **no conventional cryptography in anything the chain commits to or sends between nodes**. Where that rule stops — proxied TLS, the browser's MetaMask derivation path — is set out above and not glossed over below.

Every existing blockchain — Ethereum, Bitcoin, Solana, Binance Smart Chain, Cardano, Avalanche — uses the same underlying cryptographic primitives that quantum computers will eventually break. These chains were built in an era when quantum computers were theoretical. That era is ending.

IBM's quantum computers already have over 1,000 qubits. Estimates from NIST, the NSA, and leading academic institutions put cryptographically relevant quantum computers — machines capable of breaking ECDSA and RSA — at 5–10 years away. The financial industry is already spending billions preparing. Governments have issued mandates.

**The blockchain industry is not ready.**

Splendor Quantum Chain is the answer. Not a patch on an existing chain. Not a compatibility layer. A fresh start — where:

- **Accounts** are derived from post-quantum keys, not ECDSA
- **Signatures** use ML-DSA-65 (NIST FIPS 204), not ECDSA
- **Hashing** uses SHA-3-256 (SHA3-256), not SHA-256, SHAKE-256, or Keccak-256
- **Key exchange** uses ML-KEM-768 (NIST FIPS 203), not ECDH
- **Smart contracts** are signed, deployed, and called using quantum-safe operations only
- **Validator messages** are signed with ML-DSA-65
- **Block headers** are hashed with SHA-3
- **Node-to-node block push, peer announce, and catch-up sync** are all sealed with ML-KEM-768 + AES-256-GCM. A node that falls behind does not drop back to cleartext for the bulk of its traffic: plaintext is refused unless the operator passes `--allow-plaintext-p2p`. Encryption authenticates the channel; the ML-DSA-65 signature on each block is what authenticates the chain

The result is a chain that will still be cryptographically secure the day a quantum computer tries to attack it.

We are not the first post-quantum chain and do not claim to be: QRL has run a post-quantum mainnet for years, and QRL 2.0 uses ML-DSA-87. What is distinctive here is that the post-quantum guarantee extends past signatures to the state commitment and the peer-to-peer channel — including catch-up sync — rather than stopping at the ledger.

---

## 2. The Problem With Every Other Blockchain

### Why quantum resistance cannot be "bolted on" to Ethereum

People often ask: "Can't Ethereum just upgrade to quantum-safe cryptography?"

The answer is: not without breaking everything.

Here's why:

**Every Ethereum address is derived like this:**
```
address = last_20_bytes(keccak256(ecdsa_public_key))
```

The address is mathematically bound to the ECDSA public key. Every wallet, every smart contract, every DeFi position, every NFT is owned by an address that was created this way. To change the underlying cryptography, you'd have to:

1. Assign everyone a new address (impossible — the old address is already in millions of contracts)
2. Or add a "shim" layer on top (which means the security is only as strong as the old ECDSA layer underneath it)

Neither option actually solves the problem. The moment a quantum computer can break ECDSA, it can steal any funds sitting in any Ethereum wallet where a transaction has ever been sent — because sending a transaction reveals the public key.

**The only real solution is a new chain where the quantum-safe algorithm is the base layer.**

That is Splendor Quantum Chain.

---

## 3. The New Architecture — Quantum Native From Block Zero

The chain is structured in four layers. Every layer uses exclusively post-quantum algorithms.

```
┌──────────────────────────────────────────────────────────────────┐
│  APPLICATION LAYER                                                │
│  DApps · DeFi · NFTs · AI Agents · X402 Payments                │
│  (all interact via quantum-safe RPC and quantum-signed txns)     │
└─────────────────────────────┬────────────────────────────────────┘
                              │ Quantum-Safe JSON-RPC
┌─────────────────────────────▼────────────────────────────────────┐
│  EXECUTION LAYER                                                  │
│  Smart Contract VM (quantum-native bytecode execution)           │
│  Quantum Transaction Processor                                    │
│  All state reads/writes hashed with SHA-3                        │
│  All signatures verified with ML-DSA-65                          │
└─────────────────────────────┬────────────────────────────────────┘
                              │
┌─────────────────────────────▼────────────────────────────────────┐
│  CONSENSUS LAYER                                                  │
│  Single Validator (Phase 1) → Scalable later                     │
│  All consensus messages signed with ML-DSA-65                    │
│  Block finality in < 2 seconds                                   │
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

There is no moment in this chain's history where conventional cryptography was used. A quantum computer attacking the chain 10 years from now would find no weak link, because there never was one.

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
- **32-byte address** instead of 20-byte — more collision-resistant, more future-proof
- **SHA-3** instead of Keccak-256 — SHA-3 is NIST-standardised and has no known quantum weakness in this context
- **ML-DSA-65 public key** as the root — the key pair is post-quantum from the start

**What an address looks like:**
```
SQ:a7f3c9d2e1b48f6a3c7d9e2f1b48a6c3d7e9f2a1b3c8d4e7f6a2c9d3e8f1b4
```

The `SQ:` prefix immediately identifies this as a Splendor Quantum Chain address. It cannot be confused with an Ethereum address. It is not compatible with MetaMask by default (intentional — this is a quantum-native chain, not an Ethereum clone).

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

A ML-DSA-65 private key is 4,032 bytes — 126 times larger than an Ethereum private key (32 bytes). You cannot just write it on a piece of paper like a seed phrase. One storage option ships today; the two hardware options below it are designs:

---

**Option A: Encrypted File Storage** (for everyday users)

The private key is stored in an encrypted file on the user's device. The file is encrypted using a password that the user sets. The encryption algorithm used is AES-256-GCM — a symmetric cipher that is quantum-resistant for key storage purposes (Grover's algorithm halves AES-256's effective key length, but 128-bit security is still considered safe).

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

The password is never stored. Only the user knows it. To open the wallet, the user enters their password, which decrypts the private key in memory.

---

**Option B: Physical NFC Smart Card** — designed, not built. No hardware
integration exists in this repository; what follows is the intended flow.

The ML-DSA-65 private key would be written to the secure element of a physical NFC card — a dedicated chip that:
- Stores the key in hardware-protected non-extractable memory
- Performs signing operations internally (the key never leaves the chip)
- Requires a PIN to authorise each signing operation

**How signing works with the NFC card:**

1. User opens the wallet app on their phone and creates a transaction
2. The app computes the transaction hash (SHA-3 of the transaction data)
3. The app sends: `SIGN_REQUEST { hash: "sha3_hash_bytes" }` to the card via NFC
4. The card asks the user to enter their PIN (on a card with a small keypad, or via the phone with secure PIN entry)
5. The card's secure element runs `sig = ML_DSA_65.Sign(private_key, hash, ctx)` internally
6. The card sends back: `{ signature: "3309_bytes_of_mldsa_sig" }` via NFC
7. The app attaches the signature to the transaction and broadcasts it

**The private key never touches the internet. Ever.**

A 3,309-byte signature is small enough to cross NFC in a fraction of a second, which is what makes the flow plausible. No timing is quoted here, because no card exists yet to measure.

---

**Option C: QR Airgapped Signing** — designed, not built. No signer in this
repository implements it.

For maximum security — where not even an NFC communication channel is trusted:

1. Wallet app on the online device builds the transaction and encodes the hash as a QR code
2. An offline device (a phone that has NEVER connected to the internet) scans the QR
3. The offline device has the ML-DSA-65 private key stored locally
4. The offline device signs the hash and encodes the signature as a series of QR codes (2 QR codes needed for 3,309-byte signature)
5. The online device scans the QR codes, reconstructs the signature, and broadcasts the transaction

**Zero data ever travels over a network to or from the signing device.** Air-gap maintained throughout.

---

### Recovering a wallet

If a user loses their device:

- **For file storage:** They need their `.sqwallet` file (which should be backed up to a secure location like an encrypted USB drive) and their password
- **For NFC card:** They need the physical card and their PIN. The card IS the wallet.
- **For airgapped:** They need the offline device (or a backup of the private key file on another offline device)

**There is no 12-word seed phrase.** The key is too large for seed phrases. This is a different model from Ethereum wallets — and it is more secure.

---

## 6. Full User Journey — From Zero to Sending Funds

### Scenario: Alice wants to get on Splendor Quantum Chain and send money to Bob

---

**Step 1: Alice gets a Splendor QC wallet**

Available today:
- CLI wallet (`sqc-wallet`, encrypted `.sqwallet` file) — Windows, Mac, Linux
- Browser Connect flow via the JavaScript SDK, with keys held in WASM memory

Designed but not built: packaged desktop and mobile (iOS, Android) apps. The
walkthrough below describes the wallet flow generally; today it is the CLI and
the browser SDK that implement it.

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

This takes about 5 seconds total. No signup. No KYC. No email. No connection to any server needed.

---

**Step 3: Alice gets some SPLD tokens**

She can:
- Buy SPLD from a supported exchange and withdraw to her SQ address
- Receive SPLD from someone who already has it
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
  not quoted strings
- `data` is `[]byte`, so it is base64, and it is omitted entirely when empty

Do not hand-author a signed transaction from this document. Use the WASM signer
in `@splendor/pq-bridge`, or an implementation that reproduces Go's
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

Total signed transaction size: ~8–10 KB (large compared to Ethereum's ~200 bytes, but completely manageable for a modern network).

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
4. Checks Alice's nonce is correct
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
6. Broadcasts the signed block to all nodes

Block time: under 2 seconds.

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
                Block broadcast to all nodes
                        │
                        ▼
                Nodes verify block signature (ML-DSA-65)
                        │
                        ▼
                Block appended to chain
                        │
                        ▼
                Transaction FINAL ✅
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

## 8. Smart Contracts — Fully Quantum-Native

Smart contracts on Splendor Quantum Chain work similarly to Ethereum's, but with quantum-safe foundations.

### What a smart contract is (plain English)

A smart contract is a program that lives on the blockchain. Once deployed, it runs automatically when called. It follows rules coded by the developer — no human can change those rules after deployment.

**Example:** A simple token contract. The rules say: "If you call `transfer(to, amount)` and you have enough tokens, deduct from your balance and add to theirs." This executes automatically. No bank. No intermediary. The code runs.

### How smart contracts work on Splendor QC

**Deploying a contract:**

1. Developer produces SQC-VM bytecode (by hand or with their own tooling — there is no compiler here; see below)
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

### What language are contracts written in? — nothing, yet

There is **no contract compiler in this repository**, and no high-level contract
language you can write and compile today. Anyone telling you otherwise has read
a roadmap, not the code. What exists is two layers below that:

**1. SQC-VM bytecode (`vm/`).** A stack VM with SHA-3-256 storage slots, `SQ:`
addresses, gas metering, and a disassembler. A Deploy transaction carries raw
SQC-VM bytecode in its `data` field. `POST /vm/disasm`, `POST /vm/validate`,
`POST /vm/estimate-gas` and `GET /vm/opcodes` let you inspect and check that
bytecode; nothing generates it from source.

**2. Native Go modules (`vm/`).** Token standards, a DEX, lending, staking,
multisig and an oracle are implemented in Go inside the node rather than
compiled from user source. This is how tokens and DeFi ship on this chain
today, and it is why the absence of a compiler has not blocked anything so far.

Be precise about which of them a transaction can actually drive.
`DispatchModuleCall` (`vm/module_router.go`) routes four modules; the
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

**Roadmap, not shipped: Quantum Solidity (QSol).** The intended language is a
Solidity dialect — `sqaddress` for 32-byte addresses, `sha3()` in place of
`keccak256()`, a built-in `verify_mldsa(...)`, and no `ecrecover()`. It is a
specification and an intention. No parser, no compiler, no toolchain exists in
this repository, so no QSol source has ever been deployed. The illustrative
sample that used to sit here has been removed rather than left where a reader
could mistake it for something they can compile.

### Contract state is quantum-safe

State is committed with a binary Merkle root over the **entire** state — account records, contract code, contract storage slots, and X402 nonces — and that root is included in every block header. Leaves are length-prefixed and domain-separated before hashing, so no two different states can collide by concatenation ambiguity.

Committing storage and code (not just balances) is what stops a silent fork: if the root covered accounts only, two nodes with byte-different token balances or DEX reserves could hash to the same root and "agree" while diverging.

A full Sparse Merkle Tree, adding per-account inclusion proofs so a light client can verify one account without the whole state, is planned but not yet implemented.

There is no SHA-256. No Keccak. SHA-3 everywhere.

---

## 9. Consensus — One Validator, Built to Scale

### Phase 1: Single Validator

In Phase 1, the network runs with a single validator. This is intentional.

**Why start with one validator?**

- **Security.** A single trusted validator eliminates the attack surface of multi-party consensus before the ecosystem is mature. There's no "51% attack" if there's one validator you control.
- **Speed.** No time spent on voting, leader election, or Byzantine fault tolerance overhead. A block costs one signature, not a round of messages.
- **Simplicity.** Every component can be tested, audited, and hardened before adding complexity.
- **Precedent.** Ethereum ran with a very small validator set for years before decentralising. Bitcoin had one miner initially.

**What the single validator does:**
1. Receives transactions from the mempool
2. Orders and executes them
3. Builds a block on a fixed timer — `--block-interval`, 2 seconds by default. A full mempool does not trigger an early block; it only makes the next one larger
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

If any node receives a block with an invalid ML-DSA-65 signature, it rejects it. This makes it impossible for anyone — quantum computer or not — to forge a block.

### Phase 2 and Beyond: Expanding Validators

When the ecosystem is ready, additional validators join:

- New validators register by staking SPLD and registering their ML-DSA-65 public key
- Consensus upgrades to a BFT protocol (all signatures still ML-DSA-65)
- Governance is handled by token holders (voting via ML-DSA-65-signed transactions)

None of that exists yet. There is no BFT protocol, no path for a second validator to join, and no way for a passed proposal to change consensus: `Execute` in `vm/governance.go` marks a proposal Executed and applies nothing else. The block format would not have to change, which is why this is an upgrade rather than a new chain — but it is unwritten code, not a vote away.

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
- Using the same standards body across all primitives simplifies compliance and auditing

**State storage — binary SHA-3-256 Merkle commitment over the whole state:**

Accounts, contract code, contract storage slots, and X402 nonces are all hashed into a binary Merkle tree whose root is in every block header. Leaves are length-prefixed and domain-separated (leaf tag `0x00`, internal tag `0x01`) so distinct states cannot collide. This means:

- Two nodes that disagree on any balance, storage slot, or contract byte produce different roots, so divergence is detected instead of silently tolerated
- The commitment chain: state leaf → SHA-3 leaf hash → SHA-3 parent hashes → state root in block header → block signed with ML-DSA-65
- Every step is quantum-safe
- Blocks and receipts are queryable at any height; **state is not**. `/balance`, `/nonce` and the contract routes read current state only, and no endpoint accepts a height, so “the balance at block N” cannot be asked of a node today

Not yet built: a **proof API**. The tree would support per-account inclusion proofs, but no endpoint serves them, so a light client cannot today verify one account without trusting a full node. That is what the planned Sparse Merkle Tree work is for.

**Transaction Merkle tree:**

All transactions in a block are hashed into a Merkle tree (SHA-3) whose root sits in the block header. That is the structure a light client would use to check inclusion without the full block — but as with state proofs, no endpoint serves a branch, so a client fetches the block and recomputes the root itself.

---

## 11. X402 — Native Payment Protocol

X402 is built into the Splendor QC node at the RPC layer. It lets any HTTP server charge per API call, with no payment processor and no subscription. Access is granted the moment the signed payment verifies; the money moves when the settlement transaction lands in the next block.

### How it works end-to-end

**Server side (API operator):**

The API operator adds one middleware to their server. The helper is
`x402Middleware`, exported from `sdk/sqc-sdk.js`, and it takes an initialised
SDK instance as its first argument:
```javascript
const { SplendorQC, x402Middleware } = require('./sdk/sqc-sdk.js');

const sdk = new SplendorQC('https://quantum-rpc.splendor.org');

app.use('/api/data', x402Middleware(sdk, {
  payTo: 'SQ:operator_address...',
  price: '0.001',   // SPLD per request, or a route-pattern map
}));
```

Every request to `/api/data` now returns `402` with signed payment requirements
until a valid `X-Payment` header arrives.

**Client side (user or AI agent):**

```
1. Client sends:    GET /api/data
2. Server responds: HTTP 402 { "pay_to": "SQ:...", "amount": "0.001" }
3. Client signs payment with their ML-DSA-65 key
4. Client sends:    GET /api/data
                    X-Payment: { signed_payment_payload }
5. Server verifies payment with Splendor QC node
6. Server responds: 200 OK { data }
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
4. Executes it in the next block, where the X402 replay registry is re-checked

The server cannot debit the payer on its own; only the payer can produce the
settlement transaction, because it consumes the payer's nonce. That is
deliberate — an earlier design settled inside the HTTP handler, which meant the
transfer existed on the node that received the call and nowhere else. Settlement
goes through consensus now, so it lands in a block and in the state root.

⚠️ The `x402Middleware` in `sdk/sqc-sdk.js` calls `sdk.x402Settle(requirements,
payload)` **without** a `settlement_tx`, so its settlement leg currently gets a
`400` and only logs. The middleware still gates access correctly; to actually
settle, use `payX402` from `@splendor/pq-bridge`, which signs the settlement
transaction on the payer side.

Settlement lands in the next block. No payment processor takes a cut.

### What makes X402 different from existing payment systems

| Feature | Stripe/PayPal | Lightning Network | Splendor X402 |
|---------|--------------|-------------------|---------------|
| Works for AI agents | ❌ (needs human) | ❌ (complex setup) | ✅ |
| Sub-cent payments | ❌ (fees too high) | ✅ | ✅ |
| Quantum-safe payment | ❌ | ❌ | ✅ |
| Settlement time | 1–3 days | instant | < 2 seconds |
| Intermediary cut | 2.9% + 30¢ | routing fees | 0% |
| One-line integration | ❌ | ❌ | ✅ |
| Works without account | ❌ | ❌ | ✅ |

---

## 12. The Ecosystem — DApps, DeFi, NFTs, AI Agents

### What gets built on this chain

The Splendor Quantum Chain is general-purpose infrastructure. Anything that can be built on Ethereum can be built here — with quantum resistance baked in. This section describes what the chain is *for*, not what is deployed on it: of the modules below, only tokens and the DEX can be driven by a transaction today (section 8).

---

### DeFi (Decentralised Finance)

**Decentralised Exchanges (DEX):**
Users trade tokens directly with each other through smart contracts. No central exchange. No withdrawal limits. No KYC. All transactions signed with ML-DSA-65.

**Lending and Borrowing:**
Users deposit SPLD or other tokens as collateral, borrow other assets. Interest rates are set algorithmically. Liquidations are automatic. No bank involved.

**Yield Farming / Staking:**
Users lock tokens in smart contracts to earn rewards. All automated. All quantum-safe.

**Why DeFi on Splendor QC specifically?**
DeFi manages billions of dollars. Today's DeFi on Ethereum will be vulnerable to quantum attacks within the decade. Users and institutions who move their DeFi positions to Splendor QC now get the same financial tools with a 10-year security advantage. That is a powerful pitch to institutional DeFi users.

---

### NFTs (Non-Fungible Tokens)

NFTs on Splendor QC work the same as on Ethereum — unique digital assets representing art, music, game items, real-world assets.

The key difference: **the ownership signature is ML-DSA-65, not ECDSA**. An NFT on Ethereum proves ownership with ECDSA. A quantum computer could forge that proof. An NFT on Splendor QC proves ownership with ML-DSA-65 — which no known quantum algorithm can forge.

For high-value digital art and real-world asset tokenization, this distinction matters enormously.

---

### Gaming and GameFi

Games with in-game economies — where items, characters, and land are owned by players as tokens — require fast, cheap transactions. Splendor QC's sub-2-second block time and near-zero fees make it suitable for high-frequency game transactions.

Game developers deploy SQC-VM bytecode, or use the native token modules already in the node. Players create wallets. In-game items are tokens. Trades happen on-chain, instantly, quantum-safely.

---

### AI Agents

This is the most forward-looking use case and arguably the most important.

**The problem AI agents have today:**
An autonomous AI agent — a software program that acts on behalf of a user, making decisions and taking actions — needs to pay for things: compute time, data, API calls, storage, other AI services. Currently, this requires:
- A credit card (requires a human to set up)
- A bank account (requires a human and a legal entity)
- API keys pre-purchased by a human
- Manual top-ups when the balance runs low

This is a fundamental bottleneck for autonomous AI. The AI can decide, reason, and act — but it can't pay.

**What Splendor QC enables:**
An AI agent has a Splendor QC wallet. Its ML-DSA-65 private key is stored securely in the agent's environment. When it needs to pay for something, it signs a payment transaction automatically. No human in the loop.

The flow an agent runs is the X402 flow from section 11, with no human at any
step:

```
1. Agent calls the paid endpoint, gets 402 + signed requirements
2. Agent signs an X402 payload with its ML-DSA-65 key
3. Agent signs the settlement TxX402 for the same pinned nonce
4. Agent retries with X-Payment; the server verifies and serves
5. The settlement transaction lands in the next block
```

`payX402` in `@splendor/pq-bridge` (`sdk/bridge/src/wallet.ts`) does steps 2–4
in one call. There is no Python SDK — only the JavaScript/TypeScript one is
published — so an agent in another language speaks the HTTP protocol directly.

AI agents can also:
- Pay other AI agents for sub-tasks
- Receive payment for services they provide
- Hold and manage SPLD balances autonomously
- Participate in DeFi to earn yield on idle funds

This creates the foundation for a true **AI economy** — autonomous economic agents that can earn, spend, and save without human intervention, all secured by quantum-safe cryptography.

---

### Identity and Credentials

Since every Splendor QC account is anchored to an ML-DSA-65 key pair, accounts can be used as cryptographic identities.

These are use cases the primitives would support. The first two are **not
implemented in this repository**: there is no attestation registry and no
credential contract. The third partly is — `vm/governance.go` holds proposals,
votes, quorum and timelock, and `/gov/*` serves them read-only. What it lacks
is a way in and a way out: the module router dispatches `qrc20`, `qrc1155`,
`dex` and `exchange` (see the table under "Native Go modules"), not
`governance`, so no transaction can submit or vote today, and `Execute` marks a proposal
Executed without changing any chain parameter. Read the rest as what the key
material makes possible, not as a feature list:

- **Verifiable credentials:** A university issues a degree as a smart contract attestation, signed with the university's ML-DSA-65 key, recorded on-chain. The graduate proves their credential by showing the on-chain record — no third-party verification service needed.
- **KYC attestation:** A KYC provider attests that a user has been verified. The attestation is on-chain. The user's identity remains private (only the attestation is public). Any DApp could check whether the user is KYC-verified without seeing personal data.
- **DAO voting:** Decentralised Autonomous Organisations use ML-DSA-65-signed votes recorded on-chain. Quantum computers cannot forge votes.

---

### Supply Chain and Real-World Assets

Physical goods, property, and financial instruments can be tokenized on Splendor QC. The quantum-safe security guarantees are particularly important here because:
- Real estate title records may need to remain valid for 30+ years (well within the quantum threat window)
- Supply chain provenance records need to be unforgeable for regulatory and insurance purposes
- Financial instrument records require the highest level of cryptographic assurance

---

## 13. Cryptographic Primitives Used (Technical Reference)

This section lists every cryptographic operation in the chain and what algorithm is used.

| Operation | Algorithm | Standard | Quantum Safety |
|-----------|-----------|----------|----------------|
| Account key generation | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | ✅ Post-quantum |
| Transaction signing | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | ✅ Post-quantum |
| Signature verification | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | ✅ Post-quantum |
| Block signing (validator) | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | ✅ Post-quantum |
| Address derivation hash | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| Block header hash | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| Transaction hash | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| Merkle tree hashing | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| State root hashing | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| Private key encryption at rest | AES-256-GCM | NIST FIPS 197 | ✅ Quantum-safe (128-bit security) |
| Key derivation (from password) | Argon2id | RFC 9106 | ✅ Memory-hard, quantum-safe |
| Encrypted P2P communication | ML-KEM-768 + AES-256-GCM | NIST FIPS 203 | ✅ Post-quantum (block push, peer announce, and catch-up sync) |
| RPC / WebSocket transport, node-terminated TLS | X25519MLKEM768 hybrid | TLS 1.3 hybrid key exchange | ✅ Post-quantum (a TLS-terminating proxy in front of the node is not — see the top of this document) |
| Smart contract hash (keccak replacement) | SHA-3-256 | NIST FIPS 202 | ✅ Quantum-safe |
| Payment signing (X402) | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | ✅ Post-quantum |

**What is NOT used:**
- ❌ ECDSA (secp256k1 or any curve) — no quantum safety
- ❌ RSA — no quantum safety
- ❌ SHA-256 — not NIST post-quantum suite (though generally safe, replaced with SHA-3 for consistency)
- ❌ Keccak-256 — not NIST-standardised (SHA-3 is used instead)
- ❌ ECDH — replaced by ML-KEM-768 for key exchange (TLS uses the X25519MLKEM768 hybrid, which is never weaker than X25519 alone)
- ❌ AES-128 — replaced with AES-256 (Grover halves security; 256-bit gives 128-bit quantum security)

---

## 14. What Makes This Different — Comparison Table

| Feature | Ethereum | Solana | Splendor Quantum Chain |
|---------|----------|--------|------------------------|
| Quantum-safe signatures | ❌ | ❌ | ✅ (ML-DSA-65, from block 0) |
| Quantum-safe hashing | ✅ | ✅ | ✅ (SHA-3 everywhere) |
| Quantum-safe addresses | ❌ | ❌ | ✅ (derived from ML-DSA-65 pubkey) |
| Quantum-safe P2P layer | ❌ | ❌ | ✅ (ML-KEM-768 seals block push, peer announce, and catch-up sync; plaintext refused by default) |
| Native micropayment protocol | ❌ | ❌ | ✅ (X402, built in) |
| Smart contracts | ✅ | ✅ | ✅ (SQC-VM bytecode; QSol compiler not built) |
| Block finality | ~12 min | ~400ms | < 2 seconds |
| Uses NIST post-quantum algorithms | ❌ | ❌ | ✅ (FIPS 202, 203, 204; not independently validated) |
| AI agent payment support | ❌ | ❌ | ✅ |
| Backward compat with Ethereum | ✅ | ❌ | ❌ (intentional — clean break) |
| Single validator Phase 1 | ❌ | ❌ | ✅ (by design) |

---

## 15. The Business Case for Investors

### The market timing is perfect

**Quantum computers are no longer theoretical.**

- IBM's 1,121-qubit quantum computer: operational today
- NIST post-quantum standards: finalised 2024
- US government mandate to migrate federal systems: deadline 2030
- Global financial institutions: beginning PQC migration assessments now
- Academic consensus on "harvest now, decrypt later" attacks: already happening (adversaries collect encrypted data today to decrypt when quantum computers are ready)

**The window to be first is 3–5 years.**

The blockchain industry has not acted. The first quantum-native chain that attracts developers, users, and institutional deposits wins a multi-trillion-dollar market.

### Revenue model

**Transaction fees:** Every transaction pays a fee in SPLD. As transaction volume grows, validator revenue grows. In Phase 1, all fees go to Splendor Protocol.

**X402 protocol fees:** Option to take a small protocol fee from X402 settlements (configurable — could be 0% to start, introduced later).

**Validator marketplace (Phase 2+):** When multi-validator consensus is activated, SPLD staking generates yield. The network earns from securing other parties' stake.

**Enterprise licensing:** Institutions that want private/permissioned deployments of the Splendor QC stack pay licensing fees.

**Patent licensing:** The hybrid quantum wallet system is the subject of a filed patent application, intended to generate licensing revenue from other blockchains implementing similar systems. Filing numbers are not published in this repository; treat this as a stated intention rather than a granted patent.

### Why this wins

1. **Early in a small category.** Post-quantum chains exist — QRL has run one for years (see section 1) — but the category is small and no incumbent has taken it. The bet is on being early and complete, not on being first.

2. **No migration risk.** Every other chain will eventually need to migrate to post-quantum cryptography. That migration will be painful, contentious, and may cause community splits. Splendor QC has nothing to migrate — it started there.

3. **Regulatory tailwinds.** Every government mandate for post-quantum cryptography is a headwind for existing blockchains and a tailwind for Splendor QC. More regulation → more users who need a compliant chain.

4. **AI agent economy.** The emergence of autonomous AI agents creates an entire new class of economic actors who need financial infrastructure. Splendor QC and X402 are that infrastructure.

5. **Patent protection.** A filed application covers the core wallet innovations. Note that filed is not granted, and no filing numbers are published here, so this is not yet enforceable protection.

---

## 16. What Is Built vs What Comes Next

### Built and working

✅ Full node — Go implementation. Three tiers are live (devnet, testnet, mainnet), each a single self-genesis validator that is also its own RPC node, with no follower nodes attached today  
✅ ML-DSA-65 key generation and signing — FIPS 204, via CIRCL  
✅ SHA-3-256 hashing throughout — implemented  
✅ Blocks and transactions signed with domain-separated ML-DSA-65  
✅ Whole-state Merkle commitment — accounts, code, storage, X402 nonces  
✅ Smart contract VM — SQC-VM bytecode interpreter with native Go modules  
✅ X402 payment protocol — settled through the mempool like any transaction  
✅ ML-KEM-768 sealed block push, peer announce, and catch-up sync + hybrid X25519MLKEM768 TLS when the node terminates it  
✅ Genesis and CLI wallet (`.sqwallet`, AES-256-GCM + Argon2id)  
✅ JavaScript SDK and WASM signer (`@splendor/pq-bridge`, Connect UI)  
✅ JSON RPC for balances, blocks, receipts, DEX, staking, governance, lending  

### In active development

🔧 Block explorer — JSON APIs exist; no web interface yet  
🔧 Multi-validator consensus — `consensus/bft.go` written, not yet wired in  
🔧 Performance — ~2,600–2,900 TPS block pipeline measured on one AWS host  

### Designed, not built

📋 QSol compiler — the language is sketched, but there is no parser, compiler, or toolchain; contracts are deployed as SQC-VM bytecode today  
📋 Sparse Merkle Tree proof API — the state root is a Merkle tree, but no endpoint serves inclusion proofs  
📋 Wallet apps — desktop and mobile UIs are designed only; today there is a CLI wallet and a browser Connect flow  
📋 NFC card and QR airgapped signing — flows are designed; no hardware integration in this repo  
📋 Python and Go SDKs — only the JavaScript/TypeScript SDK is published  
🔄 Migration bridge — the one-way legacy→quantum burn-and-claim rail is implemented (`bridge_v1` module, `cmd/bridge-relayer`, `contracts/legacy-burn/`, `docs/modules/bridge.md`); awaiting per-tier fork scheduling and deploys. A general two-way bridge protocol remains unplanned  

---

## 17. AI at the Core — The Intelligent Node

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

**On throughput:** an earlier version of this section claimed a measured 40–60% improvement over static scheduling. That figure is not substantiated by any benchmark in this repository and has been withdrawn. What has been measured, on a single AWS host, is roughly 2,600–2,900 TPS through the block-production pipeline, with ML-DSA-65 verification well above that; see `docs/performance/`.

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

## 18. Banking and Financial Institutions — The Perfect Fit

Splendor Quantum Chain is not just a "crypto project". It is infrastructure-grade financial technology designed to meet the needs of regulated financial institutions.

### Why banks and financial institutions need this now

**The regulatory timeline is not optional.**

- The US government's NIST post-quantum migration mandate: 2030 deadline for federal systems
- The NSA's Commercial National Security Algorithm Suite 2.0 (CNSA 2.0): mandates ML-DSA and ML-KEM (standardised from CRYSTALS-Dilithium and CRYSTALS-Kyber) for all classified systems by 2030
- The Bank for International Settlements (BIS): has published quantum risk guidance for central banks
- The European Union: has included post-quantum cryptography in its digital finance strategy
- Financial regulators in Singapore, UK, and India: actively issuing PQC guidance

**Banks that hold customer assets secured by ECDSA are sitting on a ticking clock.** Splendor Quantum Chain is one of a small number of chains whose signatures should still be sound in 2035 — QRL is the other established one — and it is the one where the peer-to-peer layer and the whole-state commitment are post-quantum too, not just the ledger.

### What X402 means for financial institutions

The X402 payment protocol on Splendor QC is essentially a **quantum-safe, programmable payment rail** built into the blockchain itself. For financial institutions, this enables:

**Real-Time Gross Settlement (RTGS) equivalent:**
An X402 settlement is a transaction in the next block, so it is final within a block interval (2 seconds by default). Traditional RTGS systems (SWIFT, Fedwire, CHAPS) have settlement windows measured in hours. X402 on Splendor QC provides equivalent settlement finality with quantum-safe security guarantees.

**Programmable compliance — none of it written yet:**
The VM and the event log would carry compliance rules, but no such rules exist
in this repository and there is no language to express them in (see section 8).
An institution would be commissioning all of the following, not adopting it:
- KYC attestation checks before a transaction is processed
- Sanctions screening against a blocklist
- Transaction limits enforced on-chain
- Regulatory reporting built on contract events

**Interbank settlement:**
Banks can operate validator nodes on Splendor QC. Interbank transactions settle directly on-chain, in real time, with ML-DSA-65 signatures replacing the correspondent banking system's trust model. No counterparty risk. No settlement lag. No intermediary.

**Trade finance:**
Letters of credit, bills of lading, and trade finance instruments tokenized on Splendor QC:
- Immutable records, quantum-safe signatures
- Automatic execution when conditions are met (smart contracts)
- Real-time visibility to all parties
- No paper, no fax, no 5-day settlement

**Central Bank Digital Currencies (CBDCs):**
A central bank operating a CBDC needs:
- Full control over the validator set ✅ (Phase 1 single-validator model — the central bank IS the validator)
- Quantum-safe cryptography ✅
- Smart contract programmability for policy rules ✅
- Native payment settlement protocol ✅
- Scalability to national payment volumes ❌ — measured throughput is roughly 2,600–2,900 TPS on a single host, which is nowhere near national retail payment volume

Splendor QC's Phase 1 single-validator model maps structurally onto how a CBDC would be operated by a central bank: one trusted issuer, full ledger control, quantum-safe from day one. To be clear about maturity, this is a Phase-1 prototype with a single validator, not a production real-time gross settlement system, and it should be evaluated as an architectural fit rather than a deployable one.

### Compliance features — what exists, and what does not

| Requirement | Status |
|-------------|--------|
| Post-quantum algorithms | **Built.** ML-DSA-65 (FIPS 204), ML-KEM-768 (FIPS 203), SHA-3-256 (FIPS 202), checked against the published NIST ACVP vectors. No CAVP/CMVP validation, no FIPS 140-3 module boundary, no external audit |
| Transaction immutability | **Built.** SHA-3-256 Merkle roots over transactions and over whole state, blocks signed with ML-DSA-65 |
| Auditability | **Built.** Full on-chain history, queryable at any block height |
| Regulatory reporting | **Built,** in the sense that contract events are permanent and queryable. No reporting tooling exists on top of them |
| Sanctions screening | **Not built.** There is no blocklist, no screening hook, and no contract layer enforcing one anywhere in this repository |
| KYC attestation | **Not built.** No attestation registry, no contract, no verifier. The design in section 12 is a described possibility, not shipped code |
| Merkle inclusion proofs for auditors | **Not built.** The state root is a Merkle tree, but no endpoint serves proofs, so a verifier must trust a full node |
| Data sovereignty | Nothing in the node makes an outbound call to a third-party service. That is a property of the current code, not a compliance control |

An institution evaluating this should read the "Not built" rows as work it would
have to fund or build, and read the whole table alongside the maturity note
above: one validator, no HSM or remote signer, no second validator to bound the
damage if the signing host is compromised.

---

*Splendor Quantum Chain — a Layer-1 that is post-quantum where it commits and
where it talks to its peers, and honest about where it is not.*

*Every signature. Every hash. Every block. Post-quantum from block zero. The
browser, the proxy, and the world outside this repository are still catching up
— see "What Is and Is Not Post-Quantum" at the top.*
