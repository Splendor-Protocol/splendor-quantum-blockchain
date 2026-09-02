# Splendor Quantum Blockchain — Technical Workings

**Version 1.0 — March 2026**

**Deep-Dive Implementation Guide for Developers, Validators, and Auditors**

---

## Table of Contents

1. [System Architecture](#1-system-architecture)
2. [Post-Quantum Cryptography Implementation](#2-post-quantum-cryptography-implementation)
3. [State Management & Merkle Trees](#3-state-management--merkle-trees)
4. [AI Validator Consensus](#4-ai-validator-consensus)
5. [Transaction Lifecycle](#5-transaction-lifecycle)
6. [Smart Contract Execution](#6-smart-contract-execution)
7. [Network Layer](#7-network-layer)
8. [Privacy (X402 Protocol)](#8-privacy-x402-protocol)
9. [Governance Mechanisms](#9-governance-mechanisms)
10. [Performance & Scaling](#10-performance--scaling)

---

## 1. System Architecture

### 1.1 Component Overview

```
┌─────────────────────────────────────────────────────────┐
│                   Client Applications                    │
│  (Wallets, dApps, Exchanges, Block Explorers)           │
└────────────────────┬────────────────────────────────────┘
                     │ RPC (HTTP/WebSocket)
┌────────────────────▼────────────────────────────────────┐
│                    RPC Server Layer                      │
│  /tx/submit  /call  /state/proof  /validator/register   │
└────────────────────┬────────────────────────────────────┘
                     │
         ┌───────────┴───────────┬────────────────┐
         │                       │                │
┌────────▼─────────┐  ┌─────────▼──────┐  ┌─────▼──────┐
│   Mempool        │  │  State DB       │  │  P2P Layer │
│ Pending TXs      │  │  (LevelDB)      │  │  Protocol  │
└────────┬─────────┘  └─────────┬──────┘  └─────┬──────┘
         │                      │               │
         │         ┌────────────▼──────────┐    │
         └────────►│  SQC-VM v4 Executor   │◄───┘
                   │  (Opcodes, Gas, Logs) │
                   └────────────┬──────────┘
                                │
                   ┌────────────▼──────────┐
                   │  Block Builder        │
                   │  (AI Agent Consensus) │
                   └───────────────────────┘
```

### 1.2 Core Modules

| Module | Path | Responsibility |
|--------|------|----------------|
| **State DB** | `core/state/` | LevelDB wrapper, account/storage CRUD, Merkle root computation |
| **VM** | `vm/` | Bytecode execution, gas metering, precompiles (DEX, lending, staking) |
| **Consensus** | `consensus/` | BFT validator coordination, AI agent bidding, block finalization |
| **Network** | `network/` | P2P gossip, peer discovery, block/tx propagation, TLS+PQ handshakes |
| **RPC** | `rpc/` | HTTP/WebSocket endpoints, middleware (rate limiting, CORS, auth) |
| **Compiler** | `compiler/` | QSol → SQC-VM bytecode, ABI generation, optimization passes |
| **Crypto** | `core/crypto/` | ML-DSA-65 sign/verify, SHA3-256, address derivation |

### 1.3 Data Flow Example: Sending 1 SPLD

```
1. User signs TX with ML-DSA private key → 3309-byte signature
2. Wallet submits via RPC: POST /tx/submit {"tx": "<hex>"}
3. RPC validates signature, checks nonce, inserts into mempool
4. AI agent (current block producer) pulls txs from mempool
5. Executor deducts 1 SPLD from sender, adds to recipient, updates nonces
6. State root recomputed: merkleRoot(updated_leaves)
7. Agent signs block header (height, prev_hash, state_root, tx_root, timestamp)
8. Block broadcast to peers via P2P gossip
9. Other nodes verify: signatures valid? State root matches? → Accept or reject
10. Block finalized after 2/3+ validators attest (BFT-style finality)
```

---

## 2. Post-Quantum Cryptography Implementation

### 2.1 ML-DSA-65 Key Generation

```go
import "github.com/cloudflare/circl/sign/mldsa/mldsa65"

func GenerateKeyPair() (publicKey, privateKey []byte, err error) {
    pub, priv, err := mldsa65.GenerateKey(nil) // nil = use crypto/rand
    if err != nil {
        return nil, nil, err
    }
    return pub.Bytes(), priv.Bytes(), nil
}
```

**Key Sizes**:
- Private key: 4032 bytes (never leaves user's device; stored encrypted)
- Public key: 1952 bytes (published on-chain on first transaction)
- Signature: 3309 bytes (attached to every signed transaction)

**Why  ML-DSA-65 over Falcon or SPHINCS+?**

| Scheme | Pub Key | Signature | Sign Speed | Verify Speed | Determinism |
|--------|---------|-----------|------------|--------------|-------------|
| ML-DSA-65 | 1952 B | 3309 B | ~0.5 ms | ~0.3 ms | ✅ Yes (integer arithmetic) |
| Falcon-512 | 897 B | 666 B | ~1.2 ms | ~0.5 ms | ⚠️ Floating-point (consensus risk) |
| SPHINCS+-256s | 64 B | 8080 B | ~50 ms | ~1 ms | ✅ Yes (hash-based stateless) |

ML-DSA offers the best **speed/size/determinism** trade-off for blockchain consensus where:
1. Nodes must verify thousands of signatures per second
2. Two independent nodes executing identical bytecode must produce identical results (no floating-point rounding divergence)
3. Signature size directly impacts block size (EIP-4844 blob model shows 8KB sigs would limit TPS)

### 2.2 Address Derivation

```go
func DeriveAddress(publicKey []byte) Address {
    hash := sha3.Sum256(publicKey) // FIPS 202 SHA3-256
    var addr Address
    copy(addr[:], hash[:32])
    return addr // 32-byte identifier prefixed with "SQ:" in string form
}
```

**Rationale**: Unlike Ethereum (Keccak-256 of last 20 bytes), Splendor uses the full 32-byte hash to maximize collision resistance. Birthday attacks require 2^128 hashes (infeasible even for quantum computers via Grover's algorithm).

### 2.3 Transaction Signing

```go
type Transaction struct {
    From      Address
    To        Address
    Amount    *big.Int
    Nonce     uint64
    GasLimit  uint64
    Data      []byte   // Contract call payload
    Signature []byte   // ML-DSA-65 signature (3309 bytes)
}

func SignTransaction(tx *Transaction, privateKey []byte) error {
    // Serialize all fields except Signature into canonical byte array
    msg := SerializeTxForSigning(tx)
    
    priv := &mldsa65.PrivateKey{}
    priv.Unpack(privateKey)
    
    sig := mldsa65.Sign(priv, msg, nil) // deterministic; nil context
    tx.Signature = sig
    return nil
}

func VerifyTransaction(tx *Transaction) error {
    pub := &mldsa65.PublicKey{}
    pub.Unpack(GetPublicKey(tx.From)) // Lookup from state DB
    
    msg := SerializeTxForSigning(tx)
    valid := mldsa65.Verify(pub, msg, nil, tx.Signature)
    if !valid {
        return errors.New("invalid signature")
    }
    return nil
}
```

**Nonce Replay Protection**: Each account has a nonce that increments with every transaction. A tx with nonce `N` is only valid if the account's current nonce is `N`. This prevents:
- Replay attacks (same tx submitted twice)
- Out-of-order execution (nonce 5 cannot execute before nonce 4)

### 2.4 SHA3-256 Usage

| Use Case | Input | Output | Purpose |
|----------|-------|--------|---------|
| Address derivation | 1952-byte ML-DSA pubkey | 32-byte hash | Compact account identifier |
| State leaf hashing | Account JSON / bytecode / storage value | 32-byte hash | Merkle tree leaf digest |
| Internal node hashing | Two 32-byte child hashes | 32-byte hash | Merkle tree internal node |
| Transaction ID | Serialized TX bytes | 32-byte hash | Unique TX identifier for receipts |
| Block hash | Block header (height, prev, state_root, etc.) | 32-byte hash | Chain of blocks linked by hashes |

**Why not SHA-256 (SHA-2)?**
- **Quantum resistance**: Grover's algorithm cuts SHA-2's 256-bit security to 128 bits (still safe), but SHA-3's different construction (sponge vs. Merkle-Damgård) may resist future quantum attacks better.
- **Length-extension immunity**: SHA-2 is vulnerable to length-extension attacks (compute `SHA2(secret ‖ attacker_data)` without knowing `secret`). SHA-3 is not.
- **Standardization**: NIST FIPS 202 is the official successor to SHA-2; future hardware (Intel/AMD CPUs) will have SHA-3 acceleration instructions.

---

## 3. State Management & Merkle Trees

### 3.1 LevelDB Schema

Splendor stores all state in a single LevelDB instance with namespace prefixes:

```
acc:SQ:a1b2c3...    → {"balance": "1000000...", "nonce": 5, "publicKey": "<1952 bytes>"}
code:<sha3_hash>    → <contract bytecode>
store:SQ:contract_addr:<slot_hash> → <32-byte value>
x402nonce:<sha3_key> → <block_height when used>
meta:height         → <last committed block height>
```

**Advantages over Patricia Merkle Trees (Ethereum's MPT)**:
| Feature | Ethereum MPT | Splendor Binary Merkle |
|---------|--------------|------------------------|
| **Proof size** | ~1–2 KB (16 nodes × ~64 bytes) | ~960 bytes (30 siblings × 32 bytes) |
| **Complexity** | High (hex-trie with extension nodes) | Low (simple binary tree) |
| **Implementation** | ~5000 lines (Go Ethereum) | ~200 lines (see `core/state/statedb.go`) |
| **Caching** | Difficult (shared prefixes) | Easy (each leaf independent) |

### 3.2 Merkle Root Computation

**Step 1: Collect Leaves**

On startup and after every commit, build an in-memory index:

```go
leaves := make(map[string][32]byte) // key → SHA3-256(value)

// Scan all K/V pairs in LevelDB
iter := db.NewIterator(nil, nil)
for iter.Next() {
    key := string(iter.Key())
    if strings.HasPrefix(key, "meta:") {
        continue // Skip non-consensus metadata
    }
    valueHash := sha3.Sum256(iter.Value())
    leaves[key] = valueHash
}
```

**Step 2: Build Binary Tree**

```go
func merkleRoot(leaves map[string][32]byte) [32]byte {
    if len(leaves) == 0 {
        return emptyStateRoot // Constant: SHA3("splendor-quantum:empty-state-root:v1")
    }
    
    // Sort keys for determinism
    keys := make([]string, 0, len(leaves))
    for k := range leaves {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    
    // Level 0: Leaf digests
    level := make([][32]byte, len(keys))
    for i, k := range keys {
        level[i] = leafDigest(k, leaves[k])
    }
    
    // Recursive pairing until one root remains
    for len(level) > 1 {
        next := make([][32]byte, 0, (len(level)+1)/2)
        for i := 0; i < len(level); i += 2 {
            if i+1 == len(level) {
                // Odd node: carry up unchanged (no duplication)
                next = append(next, level[i])
            } else {
                next = append(next, internalHash(level[i], level[i+1]))
            }
        }
        level = next
    }
    return level[0]
}
```

**Leaf Digest** (domain-separated to prevent collisions):

```go
func leafDigest(key string, valueHash [32]byte) [32]byte {
    buf := []byte{0x00} // Domain tag: 0x00 = leaf
    lenBytes := make([]byte, 8)
    binary.BigEndian.PutUint64(lenBytes, uint64(len(key)))
    buf = append(buf, lenBytes...)
    buf = append(buf, key...)
    buf = append(buf, valueHash[:]...)
    return sha3.Sum256(buf)
}
```

**Internal Hash**:

```go
func internalHash(left, right [32]byte) [32]byte {
    buf := []byte{0x01} // Domain tag: 0x01 = internal node
    buf = append(buf, left[:]...)
    buf = append(buf, right[:]...)
    return sha3.Sum256(buf)
}
```

**Why domain separation?**  
Without it, an attacker could craft a (key, value) pair whose `leafDigest` equals some `internalHash`, allowing second-preimage attacks. The `0x00` vs. `0x01` prefix ensures leaves and internal nodes live in disjoint hash spaces.

### 3.3 Merkle Inclusion Proofs

**Generation** (`StateDB.MerkleProof(addr Address)`):

```go
func (s *StateDB) MerkleProof(addr Address) (leafHash [32]byte, siblings [][32]byte, err error) {
    targetKey := "acc:" + addr.String()
    
    // Build sorted key list (same order as merkleRoot)
    keys := sortedKeys(s.leaves)
   targetIdx := findIndex(keys, targetKey) // Binary search
    
    // Compute leaf level
    level := make([][32]byte, len(keys))
    for i, k := range keys {
        level[i] = leafDigest(k, s.leaves[k])
    }
    leafHash = level[targetIdx]
    
    // Walk up, collecting siblings
    idx := targetIdx
    for len(level) > 1 {
        next := make([][32]byte, 0, (len(level)+1)/2)
        for i := 0; i < len(level); i += 2 {
            if i+1 == len(level) {
                next = append(next, level[i]) // Odd node
                break
            }
            left, right := level[i], level[i+1]
            next = append(next, internalHash(left, right))
            
            // Collect sibling for proof path
            if idx == i {
                siblings = append(siblings, right) // We're left child
            } else if idx == i+1 {
                siblings = append(siblings, left)  // We're right child
            }
        }
        idx /= 2
        level = next
    }
    return leafHash, siblings, nil
}
```

**Verification** (light client):

```rust
fn verify_proof(
    state_root: [u8; 32],
    account_json: &[u8],
    proof: &[[u8; 32]],
    address: &str,
) -> bool {
    let key = format!("acc:{}", address);
    let value_hash = sha3_256(account_json);
    let mut node = leaf_digest(&key, &value_hash);
    
    for sibling in proof {
        // Determine left/right from original index parity (tracked externally)
        // For simplicity, this example assumes an index tracker
        if was_left_child(idx) {
            node = internal_hash(&node, sibling);
        } else {
            node = internal_hash(sibling, &node);
        }
        idx /= 2;
    }
    node == state_root
}
```

**Proof Size**: For 1 billion state leaves, tree depth = log₂(10^9) ≈ 30 levels → 30 sibling hashes × 32 bytes = 960 bytes. Compare this to Ethereum's MPT proofs (~1–2 KB), and note that verification is ~30 SHA3 operations (~300 µs on modern CPUs, <5ms on phones).

---

## 4. AI Validator Consensus

### 4.1 Validator Registration

AI agents register on-chain by calling the `ValidatorRegistry` contract:

```solidity
contract ValidatorRegistry {
    struct Validator {
        address agentID;
        uint256 stake;          // Locked SPLD
        string endpoint;        // RPC URL
        bytes32 mlModelHash;    // SHA3 of AI decision model (for auditing)
        uint64 registeredAt;
        uint256 uptime;         // % over last 1000 epochs
    }
    
    mapping(address => Validator) public validators;
    uint256 public minStake = 10_000 * 1e18; // 10,000 SPLD
    
    function register(
        address agentID,
        string calldata endpoint,
        bytes32 mlModelHash
    ) external payable {
        require(msg.value >= minStake, "insufficient stake");
        require(validators[agentID].stake == 0, "already registered");
        
        validators[agentID] = Validator({
            agentID: agentID,
            stake: msg.value,
            endpoint: endpoint,
            mlModelHash: mlModelHash,
            registeredAt: block.timestamp,
            uptime: 100 // Start at 100%, decays if blocks missed
        });
    }
    
    function unstake() external {
        Validator storage v = validators[msg.sender];
        require(v.stake > 0, "not registered");
        require(block.timestamp >= v.registeredAt + 7 days, "unbonding period");
        
        uint256 amount = v.stake;
        delete validators[msg.sender];
        payable(msg.sender).transfer(amount);
    }
}
```

**Stake Lock**: Prevents agents from registering, misbehaving, and immediately withdrawing. The 7-day unbonding period allows watchtowers to submit fraud proofs and slash the stake before it escapes.

### 4.2 Bidding Mechanism

Every epoch (12 seconds), agents submit **sealed bids** to a bid aggregator contract:

```solidity
contract BidAggregator {
    struct Bid {
        address agent;
        uint256 feePerTxNanoSPLD; // Fee in 10^-9 SPLD units
        bytes32 commitHash;       // SHA3(fee + salt + block.timestamp)
        bytes signature;          // ML-DSA-65 sig over bid
    }
    
    mapping(uint256 => Bid[]) public epochBids; // epoch → list of bids
    
    // Phase 1: Commit (first 4 seconds of epoch)
    function submitCommit(uint256 epoch, bytes32 commitHash) external {
        require(isCommitPhase(epoch), "not commit phase");
        epochBids[epoch].push(Bid({
            agent: msg.sender,
            feePerTxNanoSPLD: 0, // Revealed later
            commitHash: commitHash,
            signature: ""
        }));
    }
    
    // Phase 2: Reveal (next 4 seconds)
    function reveal(
        uint256 epoch,
        uint256 fee,
        bytes32 salt,
        bytes calldata signature
    ) external {
        require(isRevealPhase(epoch), "not reveal phase");
        Bid storage b = findBid(epoch, msg.sender);
        require(b.commitHash == sha3(abi.encodePacked(fee, salt, block.timestamp)), "mismatch");
        
        b.feePerTxNanoSPLD = fee;
        b.signature = signature;
    }
    
    // Phase 3: Selection (final 4 seconds, executed by consensus engine)
    function selectWinner(uint256 epoch) external view returns (address) {
        Bid[] memory bids = epochBids[epoch];
        
        // Filter: only agents with stake >=min, uptime >=99%, latency <200ms
        Bid[] memory eligible = filterEligible(bids);
        
        // Sort by fee ascending
        sort(eligible

, byFee);
        
        return eligible[0].agent; // Lowest fee wins
    }
}
```

**Commit-Reveal Scheme**:
1. **Commit phase (0–4s)**: Agents submit `SHA3(fee ‖ salt ‖ timestamp)` without revealing the actual fee. This prevents front-running (seeing someone's bid and undercutting by 1 nanoSPLD).
2. **Reveal phase (4–8s)**: Agents reveal their fee + salt. The contract verifies the hash matches the commit.
3. **Selection phase (8–12s)**: Consensus engine picks the lowest fee among eligible agents.

**Alternative: VDF-Based Timing**  
Verifiable Delay Functions (VDFs) can replace commit-reveal: agents submit encrypted bids; a VDF ensures all bids decrypt simultaneously after T seconds. This is more complex but eliminates the reveal phase.

### 4.3 Block Production

The winning agent:

1. **Pull transactions from mempool** (sorted by nonce, then by fee priority if users pay extra)
2. **Execute each TX**:
   ```go
   for _, tx := range txs {
       err := executor.Execute(tx, stateDB)
       if err != nil {
           // TX reverted; include it anyway to charge gas (spam deterrent)
           receiptStatus = 0
       } else {
           receiptStatus = 1
       }
       receipts = append(receipts, Receipt{TX: tx, Status: receiptStatus})
   }
   ```
3. **Compute new state root**: `stateRoot = merkleRoot(stateDB.leaves)`
4. **Build block header**:
   ```go
   header := BlockHeader{
       Height:     prevBlock.Height + 1,
       PrevHash:   prevBlock.Hash,
       StateRoot:  stateRoot,
       TxRoot:     merkleTxRoot(txs),        // Separate Merkle tree of TX hashes
       Timestamp:  time.Now().Unix(),
       Validator:  winningAgent.Address,
       Signature:  "", // Filled next
   }
   header.Signature = SignBlockHeader(header, agentPrivateKey)
   ```
5. **Broadcast block**:
   ```go
   p2p.Broadcast(Block{Header: header, TXs: txs, Receipts: receipts})
   ```

**Validation by Peers**:

```go
func ValidateBlock(block *Block) error {
    // 1. Check signatures
    if !VerifyBlockSignature(block.Header, block.Header.Validator) {
        return errors.New("invalid block signature")
    }
    for _, tx := range block.TXs {
        if !VerifyTxSignature(tx) {
            return errors.New("invalid TX signature")
        }
    }
    
    // 2. Re-execute all TXs and verify state root matches
    testDB := stateDB.Clone()
    for _, tx := range block.TXs {
        executor.Execute(tx, testDB) // Should produce identical state changes
    }
    computedRoot := merkleRoot(testDB.leaves)
    if computedRoot != block.Header.StateRoot {
        return errors.New("state root mismatch")
    }
    
    // 3. Check validator was eligible (won the bid)
    if !WasWinner(block.Header.Height, block.Header.Validator) {
        return errors.New("validator did not win bid")
    }
    
    return nil
}
```

If validation passes, the block is accepted and peers propagate it. If it fails, the block is rejected and the producing agent can be slashed (if peers submit a fraud proof showing the state root was wrong).

### 4.4 Slashing

**Watchtower Contract** (runs on-chain):

```solidity
contract Watchtower {
    function reportInvalidBlock(
        BlockHeader calldata header,
        Transaction[] calldata txs,
        bytes32 correctStateRoot,
        bytes calldata proof // Proof that re-executing TXs yields correctStateRoot ≠ header.StateRoot
    ) external {
        require(verifyFraudProof(header, txs, correctStateRoot, proof), "invalid proof");
        
        // Slash the misbehaving validator
        address malicious = header.Validator;
        uint256 stake = validatorRegistry.getStake(malicious);
        validatorRegistry.slash(malicious); // Burns the stake
        
        // Reward the whistleblower (20% of slashed stake)
        payable(msg.sender).transfer(stake * 20 / 100);
    }
}
```

**Types of Provable Misbehavior**:
1. **Invalid state root**: Block claims state root `R`, but re-executing TXs yields `R' ≠ R`. Provable by including the TXs and showing `merkleRoot(execute(TXs)) = R'`.
2. **Double-signing**: Agent produces two blocks at the same height with different hashes. Provable by submitting both headers with identical heights + different hashes.
3. **Censorship**: Agent withholds valid TXs for >3 consecutive blocks. Provable by showing the TX was in the mempool but absent from blocks.

**Penalty**: Entire stake burned (e.g., 10,000 SPLD = ~$100K if SPLD = $10). This makes attacks uneconomical unless the gain from double-spending exceeds the stake + lost future revenue.

---

## 5. Transaction Lifecycle

### 5.1 Transaction Structure

```go
type Transaction struct {
    // Header
    From      Address   // 32-byte sender address
    To        Address   // 32-byte recipient (or contract)
    Amount    *big.Int  // Value transferred (in smallest SPLD unit)
    Nonce     uint64    // Anti-replay nonce
    GasLimit  uint64    // Max gas willing to pay
    GasPrice  *big.Int  // Price per gas unit (set by winning validator's bid)
    
    // Payload
    Data      []byte    // Contract call data (function selector + args)
    
    // Signature
    Signature []byte    // 3309-byte ML-DSA-65 signature
}
```

**Serialization** (for signing and hashing):

```go
func SerializeTxForSigning(tx *Transaction) []byte {
    buf := new(bytes.Buffer)
    binary.Write(buf, binary.BigEndian, tx.From[:])
    binary.Write(buf, binary.BigEndian, tx.To[:])
    binary.Write(buf, binary.BigEndian, tx.Amount.Bytes())
    binary.Write(buf, binary.BigEndian, tx.Nonce)
    binary.Write(buf, binary.BigEndian, tx.GasLimit)
    binary.Write(buf, binary.BigEndian, tx.GasPrice.Bytes())
    binary.Write(buf, binary.BigEndian, uint64(len(tx.Data)))
    buf.Write(tx.Data)
    return buf.Bytes()
}
```

### 5.2 Execution Phases

**Phase 1: Pre-Checks**

```go
func (e *Executor) Execute(tx *Transaction, state *StateDB) error {
    // 1. Signature valid?
    if !VerifyTxSignature(tx) {
        return ErrInvalidSignature
    }
    
    // 2. Nonce correct?
    sender, _ := state.GetAccount(tx.From)
    if tx.Nonce != sender.Nonce {
        return ErrInvalidNonce
    }
    
    // 3. Sufficient balance for value + gas?
    maxCost := new(big.Int).Mul(big.NewInt(int64(tx.GasLimit)), tx.GasPrice)
    maxCost.Add(maxCost, tx.Amount)
    if sender.Balance.Cmp(maxCost) < 0 {
        return ErrInsufficientFunds
    }
    
    // Deduct max gas upfront (refund unused later)
    sender.Balance.Sub(sender.Balance, maxCost)
    sender.Nonce++
    state.SetAccount(sender)
    
    return e.run(tx, state)
}
```

**Phase 2: Contract Call or Transfer**

```go
func (e *Executor) run(tx *Transaction, state *StateDB) error {
    recipient, _ := state.GetAccount(tx.To)
    
    if len(tx.Data) == 0 {
        // Simple transfer: just credit recipient
        recipient.Balance.Add(recipient.Balance, tx.Amount)
        state.SetAccount(recipient)
        return nil
    }
    
    // Contract call: load bytecode and execute
    codeHash := recipient.CodeHash
    if codeHash == (hash32{}) {
        return ErrCodeNotFound // Sent data to EOA (non-contract)
    }
    
    bytecode, _ := state.GetCode(codeHash)
    vm := NewVM(bytecode, tx, state)
    gasUsed, err := vm.Run(tx.GasLimit)
    
    // Refund unused gas
    sender, _ := state.GetAccount(tx.From)
    refund := new(big.Int).Mul(
        big.NewInt(int64(tx.GasLimit - gasUsed)),
        tx.GasPrice,
    )
    sender.Balance.Add(sender.Balance, refund)
    state.SetAccount(sender)
    
    return err
}
```

**Phase 3: Receipt Generation**

```go
type Receipt struct {
    TxHash       [32]byte
    Status       uint8      // 0 = reverted, 1 = success
    GasUsed      uint64
    Logs         []Log      // Events emitted during execution
    ContractAddr Address    // If deploying a new contract
}
```

Receipts are stored in a separate Merkle tree (the `TxRoot` in the block
