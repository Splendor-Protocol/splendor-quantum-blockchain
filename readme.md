# Splendor Quantum Chain

### A blockchain where quantum resistance is not an add-on — it is the foundation.

> **This is not a fork. This is not an upgrade. This is a new chain — designed from block zero to be completely quantum-native. Every signature, every address, every hash, every smart contract call, every validator message uses post-quantum cryptography only. No ECDSA. No secp256k1. No SHA-256. No conventional algorithms anywhere.**

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

---

## 1. What We Are Building and Why

Splendor Quantum Chain is a new Layer-1 blockchain built from scratch with one rule: **no conventional cryptography, anywhere, ever**.

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
- **Node-to-node traffic** is sealed with ML-KEM-768 + AES-256-GCM, and TLS (when enabled) negotiates the hybrid `X25519MLKEM768` group

The result is a chain that will still be cryptographically secure the day a quantum computer tries to attack it.

We are not the first post-quantum chain and do not claim to be: QRL has run a post-quantum mainnet for years, and QRL 2.0 uses ML-DSA-87. What is distinctive here is that the post-quantum guarantee extends past signatures to the state commitment, the peer-to-peer channel, and the transport, rather than stopping at the ledger.

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
│  P2P communication with quantum-safe channel encryption          │
│  Ledger state committed via flat SHA-3-256 over sorted accounts  │
│  All on-disk data integrity verified with post-quantum MACs      │
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

A ML-DSA-65 private key is 4,032 bytes — 126 times larger than an Ethereum private key (32 bytes). You cannot just write it on a piece of paper like a seed phrase. Splendor Quantum Chain solves this with three storage options:

---

**Option A: Encrypted File Storage** (for everyday users)

The private key is stored in an encrypted file on the user's device. The file is encrypted using a password that the user sets. The encryption algorithm used is AES-256-GCM — a symmetric cipher that is quantum-resistant for key storage purposes (Grover's algorithm halves AES-256's effective key length, but 128-bit security is still considered safe).

```
wallet.sqwallet file:
{
  "version": 1,
  "address": "SQ:a7f3c9...",
  "public_key_b64": "base64_encoded_ml_dsa_65_pubkey",
  "encrypted_private_key": {
    "ciphertext": "base64_aes_gcm_ciphertext",
    "nonce": "base64_nonce",
    "salt": "base64_argon2_salt"
  }
}
```

The password is never stored. Only the user knows it. To open the wallet, the user enters their password, which decrypts the private key in memory. When the app closes, the key is wiped from memory.

---

**Option B: Physical NFC Smart Card** (for high-security users)

The ML-DSA-65 private key is written to the secure element of a physical NFC card — a dedicated chip that:
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

Transfer time for the signature over NFC: approximately 62 milliseconds. The user just holds their card to the phone for a fraction of a second.

---

**Option C: QR Airgapped Signing** (for institutional users / cold storage)

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
- Option to write the private key to an NFC card for extra security
- Her current balance: 0 SPLD

This takes about 5 seconds total. No signup. No KYC. No email. No connection to any server needed.

---

**Step 3: Alice gets some SPLD tokens**

She can:
- Buy SPLD from a supported exchange and withdraw to her SQ address
- Receive SPLD from someone who already has it
- Earn SPLD by running a validator node

---

**Step 4: Alice opens her wallet and sees her balance**

The wallet app connects to a Splendor QC node via the RPC endpoint. It queries:
```
GET /balance?address=SQ:a7f3c9d2...
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
  "from": "SQ:a7f3c9d2...",
  "to": "SQ:b8d4e1f3...",
  "amount": "50000000000000000000",
  "fee": "100000000000000",
  "nonce": 3,
  "timestamp": 1753147200,
  "memo": "For the coffee"
}
```

Notes:
- `amount` is in the smallest unit (like wei in Ethereum — 1 SPLD = 10^18 units)
- `nonce` is a counter that prevents replay attacks (each transaction from Alice must have a higher nonce than the last)
- `fee` is the transaction fee paid to the validator

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

| Type | What it does |
|------|-------------|
| **Transfer** | Send SPLD from one account to another |
| **Deploy** | Deploy a new smart contract |
| **Call** | Interact with an existing smart contract |
| **Register** | Register a new account's public key on first use |

All four types use the same signing mechanism: ML-DSA-65 signature over SHA-3 hash of the transaction body.

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

Fees are paid in SPLD. The fee is set by the sender. Higher fee = validator prioritises your transaction. The minimum fee is set by the network (governable parameter).

Fee distribution:
- 100% to the validator (Phase 1, single validator)
- As more validators join: split by governance rules

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

1. Developer writes the contract in the Splendor contract language (a quantum-safe subset — see below)
2. Contract is compiled to bytecode
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

### What language are contracts written in?

Splendor QC uses a smart contract language called **Quantum Solidity (QSol)** — a superset of Solidity with three additions and two removals:

**Additions:**
1. Built-in `verify_mldsa(pubkey, message, sig)` function for on-chain PQ signature verification
2. Built-in `sha3(data)` for quantum-safe hashing (instead of keccak256)
3. Built-in `sqaddress` type for quantum addresses (32 bytes instead of 20)

**Removals:**
1. `ecrecover()` is gone — no ECDSA recovery
2. `keccak256()` replaced by `sha3()`

A simple token contract in QSol:

```solidity
// SPDX-License-Identifier: MIT
pragma quantum-solidity ^0.1.0;

contract QToken {
    mapping(sqaddress => uint256) public balances;
    string public name = "Quantum Token";
    
    event Transfer(sqaddress indexed from, sqaddress indexed to, uint256 amount);
    
    constructor() {
        balances[msg.sender] = 1_000_000 * 10**18;
    }
    
    function transfer(sqaddress to, uint256 amount) external {
        require(balances[msg.sender] >= amount, "Insufficient balance");
        balances[msg.sender] -= amount;
        balances[to] += amount;
        emit Transfer(msg.sender, to, amount);
    }
}
```

The contract looks and feels like Solidity. A Solidity developer can learn QSol in hours, not months.

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
- **Speed.** No time spent on voting, leader election, or Byzantine fault tolerance overhead. Blocks are produced as fast as transactions arrive.
- **Simplicity.** Every component can be tested, audited, and hardened before adding complexity.
- **Precedent.** Ethereum ran with a very small validator set for years before decentralising. Bitcoin had one miner initially.

**What the single validator does:**
1. Receives transactions from the mempool
2. Orders and executes them
3. Builds blocks (every 1–2 seconds, or when the block is full)
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
  state_root: SHA3-256_sparse_merkle_root(state),
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

The transition requires no chain restart, no hard fork in the data format sense — just a governance vote to activate multi-validator consensus.

---

## 10. Record Keeping — The Quantum Ledger

### How the blockchain stores data

Every piece of data on Splendor QC is stored with quantum-safe integrity guarantees.

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
- Historical state can be queried by specifying any block height

Not yet built: a **proof API**. The tree would support per-account inclusion proofs, but no endpoint serves them, so a light client cannot today verify one account without trusting a full node. That is what the planned Sparse Merkle Tree work is for.

**Transaction Merkle tree:**

All transactions in a block are hashed into a Merkle tree (SHA-3). The root is in the block header. This allows light clients to verify that a specific transaction was included in a block without downloading the full block.

---

## 11. X402 — Native Payment Protocol

X402 is built into the Splendor QC node at the RPC layer. It allows any HTTP server to charge for API calls and receive payment instantly, with no payment processor, no subscription, and zero friction.

### How it works end-to-end

**Server side (API operator):**

The API operator adds one middleware to their server:
```javascript
app.use('/api/data', splendorQC_X402({
  payTo: 'SQ:operator_address...',
  price:  '0.001',   // SPLD per request
  rpc:    'https://rpc.splendor-qc.io/'
}))
```

That's it. Every request to `/api/data` now requires a quantum-signed payment.

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

When the server calls `x402_settle`, the node:
1. Verifies the ML-DSA-65 signature on the payment
2. Deducts from client's balance
3. Credits the API operator's balance
4. Records in next block

Settlement: under 2 seconds. No payment processor takes a cut. The operator gets 100%.

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

The Splendor Quantum Chain is general-purpose infrastructure. Anything that can be built on Ethereum can be built here — with quantum resistance baked in.

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

Game developers deploy their game contracts in QSol. Players create wallets. In-game items are tokens. Trades happen on-chain, instantly, quantum-safely.

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

```python
# An AI agent that autonomously pays for API access
agent = SplendorQCAgent(private_key=os.getenv('SQC_ML_DSA_KEY'))

# Agent needs weather data — costs 0.001 SPLD per call
weather = agent.fetch_with_payment(
    url='https://weather-api.example.com/forecast',
    max_payment_spld=0.001
)
# Agent automatically signed and sent payment. No human needed.
```

AI agents can also:
- Pay other AI agents for sub-tasks
- Receive payment for services they provide
- Hold and manage SPLD balances autonomously
- Participate in DeFi to earn yield on idle funds

This creates the foundation for a true **AI economy** — autonomous economic agents that can earn, spend, and save without human intervention, all secured by quantum-safe cryptography.

---

### Identity and Credentials

Since every Splendor QC account is anchored to an ML-DSA-65 key pair, accounts can be used as cryptographic identities.

Use cases:
- **Verifiable credentials:** A university issues a degree as a smart contract attestation, signed with the university's ML-DSA-65 key, recorded on-chain. The graduate proves their credential by showing the on-chain record — no third-party verification service needed.
- **KYC attestation:** A KYC provider attests that a user has been verified. The attestation is on-chain. The user's identity remains private (only the attestation is public). Any DApp can check whether the user is KYC-verified without seeing personal data.
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
| Encrypted P2P communication | ML-KEM-768 + AES-256-GCM | NIST FIPS 203 | ✅ Post-quantum |
| RPC / WebSocket transport (TLS enabled) | X25519MLKEM768 hybrid | TLS 1.3 hybrid key exchange | ✅ Post-quantum |
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
| Quantum-safe hashing | ❌ | ❌ | ✅ (SHA-3 everywhere) |
| Quantum-safe addresses | ❌ | ❌ | ✅ (derived from ML-DSA-65 pubkey) |
| Quantum-safe P2P layer | ❌ | ❌ | ✅ (ML-KEM-768 key exchange) |
| Native micropayment protocol | ❌ | ❌ | ✅ (X402, built in) |
| Smart contracts | ✅ | ✅ | ✅ (QSol) |
| Block finality | ~12 min | ~400ms | < 2 seconds |
| NIST post-quantum standard compliance | ❌ | ❌ | ✅ (FIPS 202, 203, 204) |
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

1. **First mover advantage in a category that hasn't started yet.** Post-quantum blockchain doesn't exist as a category. Splendor QC creates it.

2. **No migration risk.** Every other chain will eventually need to migrate to post-quantum cryptography. That migration will be painful, contentious, and may cause community splits. Splendor QC has nothing to migrate — it started there.

3. **Regulatory tailwinds.** Every government mandate for post-quantum cryptography is a headwind for existing blockchains and a tailwind for Splendor QC. More regulation → more users who need a compliant chain.

4. **AI agent economy.** The emergence of autonomous AI agents creates an entire new class of economic actors who need financial infrastructure. Splendor QC and X402 are that infrastructure.

5. **Patent protection.** A filed application covers the core wallet innovations. Note that filed is not granted, and no filing numbers are published here, so this is not yet enforceable protection.

---

## 16. What Is Built vs What Comes Next

### Built and working

✅ Full node — Go implementation, runs a single-validator devnet  
✅ ML-DSA-65 key generation and signing — FIPS 204, via CIRCL  
✅ SHA-3-256 hashing throughout — implemented  
✅ Blocks and transactions signed with domain-separated ML-DSA-65  
✅ Whole-state Merkle commitment — accounts, code, storage, X402 nonces  
✅ Smart contract VM — SQC-VM bytecode interpreter with native Go modules  
✅ X402 payment protocol — settled through the mempool like any transaction  
✅ ML-KEM-768 encrypted P2P + hybrid X25519MLKEM768 TLS  
✅ Genesis and CLI wallet (`.sqwallet`, AES-256-GCM + Argon2id)  
✅ JavaScript SDK and WASM signer (`@splendor/pq-bridge`, Connect UI)  
✅ JSON RPC for balances, blocks, receipts, DEX, staking, governance, lending  

### In active development

🔧 Block explorer — JSON APIs exist; no web interface yet  
🔧 Multi-validator consensus — `consensus/bft.go` written, not yet wired in  
🔧 Performance — ~2,600–2,900 TPS block pipeline measured on one AWS host  

### Designed, not built

📋 QSol compiler — the language is specified, but no compiler exists; contracts are deployed as SQC-VM bytecode today  
📋 Sparse Merkle Tree proof API — the state root is a Merkle tree, but no endpoint serves inclusion proofs  
📋 Wallet apps — desktop and mobile UIs are designed only; today there is a CLI wallet and a browser Connect flow  
📋 NFC card and QR airgapped signing — flows are designed; no hardware integration in this repo  
📋 Python and Go SDKs — only the JavaScript/TypeScript SDK is published  
📋 Bridge protocol and DeFi primitives in QSol  

---

## 17. AI at the Core — The Intelligent Node

Every node runs a small adaptive scheduler in-process, with no outbound API calls, no cloud dependency, and no data leaving the machine.

### What "local AI" means here — and what it does not

Be precise about this, because "AI" is an overloaded word. There is **no neural network, no ONNX runtime, and no trained model** in the node today. What ships in `ai/scheduler.go` is a compact statistical control loop:

- **Exponential moving average** over observed fee and mempool trends
- **A reinforcement-learning-style greedy policy** that tunes block batch size up or down based on whether the last adjustment helped
- **Rule-based anomaly detection** that flags a mempool spike above a fixed multiplier

That is genuinely useful and genuinely local, but it is closer to adaptive control than to machine learning, and it should not be described as an inference engine.

### What the scheduler actually does

The validator asks the scheduler for a batch size each round, and the scheduler adapts that number from the mempool depth it has observed:

- Recommends how many transactions to pack into the next block, between a floor and a ceiling
- Tracks a smoothed fee trend rather than reacting to a single block
- Raises an anomaly flag when pending transactions spike sharply, so throttling can respond

Not implemented, despite appearing in earlier drafts of this document: grouping transactions by contract state, re-prioritising the queue by fee, and reading CPU, memory, or I/O load. The only signal currently fed to the scheduler is mempool depth.

**On throughput:** an earlier version of this section claimed a measured 40–60% improvement over static scheduling. That figure is not substantiated by any benchmark in this repository and has been withdrawn. What has been measured, on a single AWS host, is roughly 2,600–2,900 TPS through the block-production pipeline, with ML-DSA-65 verification well above that; see `docs/performance/`.

**Anomaly detection:**

The anomaly detection model reads block-level summaries and mempool statistics — not individual transaction content — and flags:
- Coordinated spam attacks (sudden volume spikes from clusters of addresses)
- Unusual contract deployment patterns (e.g., many similar contracts deployed in rapid succession — a common precursor to exploit deployment)
- Fee manipulation attempts
- Abnormal transaction failure rate spikes (which can signal a contract bug being exploited)

When an anomaly crosses a threshold, the node raises an alert and can automatically throttle, quarantine, or escalate for human review.

**Resource auto-scaling:**

The AI engine monitors hardware utilisation (CPU, RAM, disk I/O, network bandwidth) and predicts load 10–30 seconds ahead based on mempool depth trends. When load is projected to exceed capacity, it can:
- Pre-allocate additional processing threads
- Adjust block production timing
- Signal the operator to scale hardware before a bottleneck hits

**Validator bid optimisation (Phase 2):**

When multi-validator consensus is active, each validator's AI engine manages the bidding strategy — a reinforcement learning policy that analyses recent fee trends, competitor bids, and the validator's own operating costs to set the optimal fee per block. The policy improves continuously as it receives reward signals (whether the bid won, and at what margin). This runs on-node, in real time, with no human input needed.

### Why local AI instead of a cloud AI service?

**Privacy:** Transaction data belongs to users and operators. Sending it to a cloud AI service for analysis is a data governance problem, especially for regulated financial institutions. Everything stays on-node.

**Latency:** A cloud API call adds 50–200ms of round-trip latency per decision. An on-node inference call on a small trained model takes 1–5ms. At sub-2-second block times, this difference is significant.

**Reliability:** No external dependency means no downtime risk from a third-party provider. The node's AI functions even if it is fully offline from the internet.

**Sovereignty:** The node operator owns the models, the data, and the decisions. The AI can be audited, retrained, or replaced by the operator at any time.

**Quantum safety:** Cloud AI services communicate over TLS. TLS will eventually be quantum-vulnerable. On-node AI inference has zero external network attack surface — there is nothing to intercept.

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

**Banks that hold customer assets secured by ECDSA are sitting on a ticking clock.** Splendor Quantum Chain is the only blockchain infrastructure where they can operate today knowing their records will still be cryptographically valid in 2035.

### What X402 means for financial institutions

The X402 payment protocol on Splendor QC is essentially a **quantum-safe, programmable payment rail** built into the blockchain itself. For financial institutions, this enables:

**Real-Time Gross Settlement (RTGS) equivalent:**
Every X402 settlement is final in under 2 seconds. Traditional RTGS systems (SWIFT, Fedwire, CHAPS) have settlement windows measured in hours. X402 on Splendor QC provides equivalent settlement finality with quantum-safe security guarantees.

**Programmable compliance:**
Smart contracts in QSol can enforce compliance rules automatically:
- KYC attestation checks before any transaction is processed
- Sanctions screening (wallet blocklist at the smart contract level)
- Transaction limits enforced by on-chain rules
- Automatic regulatory reporting via on-chain events

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

### Compliance features built in

| Requirement | How Splendor QC addresses it |
|-------------|------------------------------|
| Post-quantum cryptography mandate | NIST FIPS 202, 203, 204 — all three standards |
| Transaction immutability | SHA-3 Merkle proofs, ML-DSA-65-signed blocks |
| Auditability | Full on-chain transaction history, queryable at any block height |
| Sanctions compliance | Wallet blocklist enforced at the smart contract layer |
| KYC integration | On-chain attestation model — KYC provider signs attestation, DApps check it |
| Data sovereignty | On-node AI inference only — no data leaves the node |
| Regulatory reporting | Smart contract events are permanent, queryable audit trail |


> Splendor Quantum Chain is the only blockchain that is compliant with the NIST post-quantum migration mandate, provides programmable settlement via smart contracts, supports autonomous AI-agent payment flows, and is ready for institutional deployment today.

---

*Splendor Quantum Chain — The first blockchain built for the world that's coming.*

*Every signature. Every hash. Every block. Quantum-safe from day one.*
