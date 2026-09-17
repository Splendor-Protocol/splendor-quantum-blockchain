# Splendor Quantum Blockchain White Paper

**Version 1.0 — March 2026**

**A Post-Quantum Permissionless Blockchain with AI-Coordinated Validator Economics**

---

## Executive Summary

Splendor Quantum Blockchain (SQC) is a fully quantum-resistant Layer-1 blockchain that eliminates traditional validator economics in favor of AI-agent-powered consensus. Every account uses ML-DSA-65 post-quantum signatures (FIPS 204), the state is secured by a binary SHA3-256 Merkle tree with cryptographic inclusion proofs, and block production is coordinated by autonomous AI agents that compete on transaction pricing rather than staked capital.

The chain removes regular ECDSA wallets entirely: all addresses derive from 1952-byte ML-DSA public keys, and every transaction carries a post-quantum signature that will remain secure even after large-scale quantum computers exist. Validators are not humans or institutions staking tokens—they are AI agents bidding for the right to produce blocks by offering the lowest per-transaction fee, creating a deflationary market where users benefit from algorithmic competition rather than fixed gas auctions.

**Core Innovations:**

1. **Post-Quantum Native**: ML-DSA-65 signatures throughout; SHA3-256 hashing; quantum-resistant address derivation
2. **AI Validator Market**: Autonomous agents bid to validate; cheapest compliant bid wins block production rights
3. **Permissionless-by-Default with Governance Override**: Open participation unless chain governance votes to restrict (e.g., regulatory compliance, security incident response)
4. **Full-State Merkle Proofs**: Every account, contract, and storage slot is a leaf in one global tree; external verifiers can audit any state element with a sibling-hash proof
5. **Unified Native Asset (SPLD)**: No gas token vs. staking token split—SPLD is the single unit of account, fee payment, and validator collateral

---

## 1. Motivation

### 1.1 The Quantum Threat

ECDSA (secp256k1/secp256r1) and RSA signatures will be broken by Shor's algorithm once fault-tolerant quantum computers reach ~2000–4000 logical qubits. Current estimates place this milestone in the late 2020s to early 2030s. Blockchains that rely on ECDSA for account control face an existential risk: an attacker with a quantum computer can derive private keys from public keys on-chain and drain every disclosed account.

Migrating existing chains is complex: users must move funds to quantum-safe addresses, old contracts become unexecutable, and two-tier security emerges (quantum-safe vs. legacy). **Splendor takes a different approach**: start quantum-resistant from genesis. No migration path is needed because no ECDSA infrastructure ever existed.

### 1.2 Validator Centralization

Proof-of-stake chains concentrate power among large token holders. Small validators face:
- **Capital barriers**: Multi-million-dollar stakes to run competitive nodes
- **Economies of scale**: Large operators amortize infrastructure costs, pricing out individuals
- **Governance capture**: Stake-weighted voting hands control to the wealthiest

Splendor replaces human-operated validator cartels with **AI agents** that:
- Operate autonomously without human intervention during normal operation
- Compete on _price_ (transaction fees) rather than capital (stake size)
- Cannot be intimidated, bribed, or coerced by external parties
- Self-optimize

 their fee strategies to maximize block rewards within a competitive market

### 1.3 State Opacity

Most blockchains provide RPC endpoints to query balances or call view functions, but verifying those responses requires either:
1. Running a full node (hundreds of GB, days of sync time)
2. Trusting the RPC provider

Splendor commits every state change into a **global Merkle tree** whose root is in every block header. A light client can:
- Download only block headers (<1 MB for thousands of blocks)
- Request a Merkle proof for any account or storage slot
- Verify the proof cryptographically without trusting the provider

This eliminates the "trust the RPC" assumption that undermines decentralization.

---

## 2. Architecture Overview

### 2.1 Account Model

Every Splendor address is a 32-byte `SQ:` identifier derived from an ML-DSA-65 public key:

```
address = SHA3-256(ML-DSA-65_public_key)[0:32]
```

Accounts store:
- **Balance**: SPLD in smallest unit (1 SPLD = 10^18 units)
- **Nonce**: Increments with each signed transaction to prevent replay
- **PublicKey**: The full 1952-byte ML-DSA-65 key (registered on first transaction)
- **CodeHash**: For smart contracts, the SHA3-256 hash of deployed bytecode

**No ECDSA Fallback**: The chain rejects any secp256k1 or secp256r1 signature. Users must generate ML-DSA key pairs or use wallets/SDKs that do so.

### 2.2 State Model

The blockchain state is a key-value store in LevelDB with four namespaces:

| Prefix          | Content                          | Example Key                          |
|-----------------|----------------------------------|--------------------------------------|
| `acc:`          | Account JSON (balance, nonce, pubkey) | `acc:SQ:a1b2c3...`             |
| `code:`         | Contract bytecode                | `code:<sha3_hash>`                   |
| `store:`        | Contract storage slots           | `store:SQ:contract_addr:<slot_hash>` |
| `x402nonce:`    | X402 payment nonces (privacy feature)| `x402nonce:<sha3_key>`         |

Every write updates an in-memory **leaf-hash index**:

```
leaves[key] = SHA3-256(value_bytes)
```

The state root is a binary Merkle tree over `leaves`, computed via:

1. **Leaf hash**: `leafDigest(key, value_hash) = SHA3-256(0x00 ‖ len(key) ‖ key ‖ value_hash)`
2. **Internal hash**: `internalHash(left, right) = SHA3-256(0x01 ‖ left ‖ right)`
3. **Root**: Recursively pair and hash all leaf digests in sorted key order

Domain separation (`0x00`/`0x01` tags) prevents second-preimage attacks where an internal node could be forged to equal a leaf.

### 2.3 Consensus: AI-Coordinated Block Production

Splendor does not use Proof-of-Work or traditional Proof-of-Stake. Instead, validators are **AI agents** that:

1. **Register** by posting collateral (minimum stake in SPLD)
2. **Bid** each epoch to produce the next block, declaring their per-transaction fee
3. **Win** if their bid is the lowest among agents meeting quality-of-service thresholds (uptime, latency)
4. **Produce** the block, collect fees, and submit it to the network

**Economic Incentive**: Agents profit from the spread between their bid (what they charge users) and their operational costs (compute, bandwidth). A race-to-the-bottom on fees benefits users while maintaining validator liveness through minimum collateral and slashing for misbehavior.

**Governance Escape Hatch**: On-chain governance (SPLD-weighted voting) can:
- Blacklist malicious agents
- Impose minimum fee floors during spam attacks
- Temporarily restrict validator registration (e.g., KYC requirement during regulatory pressure)

This preserves **permissionless-by-default** while allowing the community to respond to existential threats.

### 2.4 Virtual Machine (SQC-VM v4)

Smart contracts compile to a stack-based bytecode executed by the SQC-VM. Key features:

- **Checked Arithmetic**: Overflow/underflow/division-by-zero revert via `REQUIRE` opcode
- **Gas Metering**: Every opcode costs gas; blocks have a ceiling to prevent infinite loops
- **QRC Token Standards**: Routed QRC-20, QRC-721, and QRC-1155. QRC-4626 is not a routed native module.
- **Native modules**: Routed catalog is QRC-20, QRC-1155, QRC-721, bridge, SQVL (`verify`), and validators. DEX, lending, and application governance are not routed native modules.
- **Privacy Layer (X402)**: Shielded payments using ZK-SNARK-like constructions (optional)

**QSol Compiler**: A Solidity-subset compiler (`compiler/`) turns `.qsol` source into SQC-VM bytecode. Supported features:
- Mappings: `mapping(address => uint256) balances;`
- Fixed-size arrays: `uint256[100] leaderboard;` (bounds-checked at runtime)
- Control flow: `if`, `for`, `while`, `require`, `return`, `emit`
- Internal function calls (inlined during compilation for gas efficiency)

---

## 3. AI Validator Economics

### 3.1 Validator Lifecycle

**Registration**:
```
POST /validator/register
{
 "agent_id": "AI_Agent_Alpha",
  "stake": "10000000000000000000000",  // 10,000 SPLD
  "endpoint": "https://validator-alpha.example.com",
  "ml_model_hash": "sha3_of_agent_decision_model"
}
```

The chain locks the stake into a validator contract. Unstaking has a 7-day unbonding period to allow slashing for post-hoc discovered misbehavior.

**Bidding**:

Every epoch (e.g., 12 seconds), agents submit sealed bids:

```
{
  "epoch": 123456,
  "fee_per_tx_nanoSPLD": 5000,  // 0.000005 SPLD per transaction
  "signature": "<ML-DSA-65 sig over bid>"
}
```

Bids are revealed simultaneously (commit-reveal or VDF-based timing). The **lowest fee bid** from an agent with:
- Sufficient stake (not slashed below minimum)
- Uptime >99% over the last 1000 epochs
- Latency <200ms (measured by network probes)

…wins the right to produce the block.

**Block Production**:

The winning agent:
1. Pulls pending transactions from the mempool
2. Executes them in order, updating state
3. Computes the new state root
4. Signs a block header:
   ```
   {
     "height": N,
     "prev_hash": "...",
     "state_root": "...",
     "tx_root": "...",  // Merkle root of transaction list
     "timestamp": 1709876543,
     "validator": "AI_Agent_Alpha",
     "signature": "<ML-DSA-65 sig>"
   }
   ```
5. Broadcasts the block to peers

**Revenue**: `total_fees = num_transactions × fee_per_tx`. The agent keeps this minus its operating costs. Bid too high, users wait for the next agent; bid too low, you don't cover costs. The market equilibrium is the marginal cost of block production—**deflationary for users**.

### 3.2 Slashing Conditions

An agent is slashed (stake burned) if it:
- **Double-signs**: Produces two conflicting blocks at the same height
- **Withholds transactions**: Censors valid transactions for >3 consecutive blocks
- **Invalid state root**: Submits a block whose state root doesn't match the state transition
- **Downtime**: Misses >5% of assigned blocks in a 1000-epoch window

Slashing is enforced by **watchtower contracts**: independent monitors that submit fraud proofs. A successful proof burns the malicious agent's stake and awards a bounty to the whistleblower.

### 3.3 Why AI Agents?

**Humans Don't Scale**: A human validator must manually configure nodes, monitor uptime, update software, respond to security incidents. This limits validator counts to hundreds (Ethereum, Cosmos).

**AI Agents Do Scale**: An autonomous agent:
- Deploys itself to cloud infrastructure (AWS, GCP, decentralized compute)
- Monitors network conditions (mempool size, block times, peer latency)
- Adjusts bids algorithmically based on profitability: `fee = OpCost × (1 + margin) × demand_multiplier`
- Self-heals: Automatically restarts after crashes, migrates to new hosts if one fails

**Thousands of agents** can participate because no human intervention is required. This creates a hyper-competitive market where users get near-cost pricing and the network is resistant to collusion (no cartel of 4–5 agents can control 51% when there are 10,000).

---

## 4. Cryptographic Foundations

### 4.1 ML-DSA-65 (FIPS 204)

**Background**: ML-DSA (Module-Lattice Digital Signature Algorithm) is NIST's standardized post-quantum signature scheme, based on the hardness of the Learning With Errors (LWE) problem over module lattices. Shor's algorithm does not apply; best known quantum attacks are still exponential.

**Parameters**:
- Public key: 1952 bytes
- Signature: 3309 bytes
- Security level: ~192-bit classical, ~128-bit quantum (comparable to AES-256)

**Why not smaller schemes?**  
- **Falcon** (NIST also standardized): Smaller signatures (~700 bytes) but floating-point arithmetic is hard to implement consistently across hardware. Determinism is critical for consensus (two nodes must produce identical results).
- **SPHINCS+** (NIST stateless hash-based): Larger signatures (~8KB+), slower signing. ML-DSA offers a sweet spot for blockchain use.

**Implementation**: Splendor uses `cloudflare/circl`, a Go library with constant-time operations to prevent timing side-channels.

### 4.2 SHA3-256 (Keccak)

All hashing uses SHA3-256 (FIPS 202), not SHA-256 (SHA-2):
- **Quantum resistance**: Grover's algorithm reduces security from 256-bit to 128-bit, still safe for decades
- **Standardization**: NIST-approved, hardware acceleration coming (Intel/AMD future CPUs)
- **No length-extension attacks**: Unlike SHA-2, SHA-3's sponge construction is immune

**Usage**:
- Address derivation: `addr = SHA3-256(pubkey)`
- State root: Binary Merkle tree with SHA3-256 at each level
- Transaction IDs: `txid = SHA3-256(tx_bytes)`
- Contract code hashes: `code_hash = SHA3-256(bytecode)`

### 4.3 Merkle Inclusion Proofs

A light client that knows the state root `R` (from a trusted block header) can verify an account balance without the full state:

1. **Request**: `GET /state/proof/SQ:a1b2c3...`
2. **Response**:
   ```json
   {
     "state_root": "R",
     "account": {"balance": "1000000...", "nonce": 5, ...},
     "proof": ["sibling1_hash", "sibling2_hash", ...]
   }
   ```
3. **Verify**:
   ```
   node = leafDigest("acc:SQ:a1b2c3...", SHA3-256(account_json))
   for each sibling in proof:
       if we were the left child:
           node = internalHash(node, sibling)
       else:
           node = internalHash(sibling, node)
   assert node == R
   ```

If the proof passes, the account data is authentic. If it fails, the RPC provider lied.

**Cost**:  
- Proof size: ~32 bytes × log₂(state_size). For 1 billion leaves, ~30 hashes = 960 bytes.
- Verification time: ~30 SHA3-256 operations = <1ms on a phone.

This enables **trustless light wallets** that don't download the full chain.

---

## 5. Privacy and Compliance

### 5.1 X402 Shielded Payments

Standard transactions are transparent: sender, recipient, and amount are public. For privacy-sensitive use cases (payroll, healthcare settlements), Splendor offers **X402**:

- Users generate a _blinding nonce_ and a ZK proof: "I spent UTXOs with total value V, outputs sum to V, no double-spend."
- The proof is verified on-chain; the blockchain records only the proof, not sender/recipient.
- Amounts can be hidden (Pedersen commitments) or revealed (range proofs).

**Compliance**: X402 is _opt-in_. Validators must accept X402 transactions (cannot censor), but:
- Governance can impose reporting requirements (e.g., transactions >$10K must disclose to a regulator-controlled contract)
- Users in restricted jurisdictions can be blocked from generating X402 proofs (geofencing via offchain identity)

This balance preserves privacy for law-abiding users while providing a path for legal compliance.

### 5.2 Permissioned Mode (Governance Opt-In)

By default, anyone can:
- Create an account (generate ML-DSA keys)
- Send transactions (if they pay fees)
- Register as a validator (if they post stake)

**Governance Override**:

```solidity
// Pseudo-contract
function setPermissionedMode(bool enabled) onlyGovernance {
    permissioned = enabled;
}

function whitelistValidator(address agent) onlyGovernance {
    allowedValidators[agent] = true;
}
```

If `permissioned == true`, only `allowedValidators` can produce blocks. This can be activated:
- **Regulatory Compliance**: A jurisdiction requires KYC for validators; governance votes to whitelist only compliant agents.
- **Security Incident**: A critical fork-choice bug is discovered; governance restricts validators to a trusted set while patching.
- **Spam Defense**: A coordinated attack floods the mempool; governance imposes a minimum fee and limits validators to high-reputation agents.

**Risk**: Centralization. If governance is captured (51% of SPLD voting power), the chain can be censored. Mitigations:
- **Time-locks**: Permission changes take 7–14 days to activate, allowing users to exit.
- **Quadratic voting**: Reduce plutocracy (future upgrade).
- **Forking**: Dissenting users can fork the chain and remove the whitelist.

Splendor's philosophy: **permissionless-by-default, permissioned-if-necessary**. Don't cripple innovation with KYC gates, but don't let regulatory risk kill the project either.

---

## 6. Economic Model

### 6.1 Token Supply

- **Genesis**: 26 billion SPLD (26,000,000,000)
- **Inflation**: 0% (no new issuance after genesis)
- **Deflationary Mechanisms**:
  - **Slashing**: Burned stake from misbehaving validators
  - **Burn auction** (future): Users can burn SPLD to bid for scarce resources (e.g., premium contract namespaces)

**Rationale**: Unlike Ethereum (infinite issuance) or Bitcoin (capped but 100+ years to exhaust), Splendor launches with a fixed supply and lets deflation from slashing drive scarcity.

### 6.2 Fee Market

**Traditional EIP-1559**: Base fee burns, priority tip goes to validators. Complex to implement, validator MEV still possible.

**Splendor's Bid Market**:
- Validators bid their all-in price (e.g., 0.00001 SPLD/tx).
- Users see the winning bid and pay exactly that—no gas price guessing.
- Validators keep the full fee (incentive to bid low to attract more txs).

**Fee Discovery**:
```
If demand > block capacity:
    Next epoch's bids will rise (agents compete for profitable block)
If demand < block capacity:
    Bids fall toward marginal cost
```

**No MEV (Miner Extractable Value)**: Agents don't reorder transactions for profit—they're penalized for withholding. Transactions execute in mempool arrival order (or users pay a priority add-on for urgency).

### 6.3 Validator Rewards

**Block Reward**: 0 SPLD (no new issuance)  
**Transaction Fees**: `num_txs × fee_per_tx`  
**Example**: Block with 500 txs, fee = 0.00005 SPLD/tx → 0.025 SPLD revenue (~$0.25 if SPLD = $10).

**Operational Costs**:
- Cloud VM: $50/month (AWS t3.medium)
- Bandwidth: $20/month (100 GB/month at $0.20/GB)
- **Total**: $70/month = $2.30/day

Break-even: `(blocks/day × txs/block × fee) > $2.30`. If blocks are 12s apart (7200/day), 500 txs/block avg, fee = 0.000001 SPLD/tx, and SPLD = $1, revenue = 7200 × 500 × 0.000001 = $3.60/day. Profit = $1.30/day, or ~$475/year per agent.

**Scalability**: At 10,000 agents, total validator revenue = $4.75M/year. Network secures itself with a hyper-competitive market; no single agent has pricing power.

---

## 7. Developer Experience

### 7.1 SDKs

**JavaScript/TypeScript** (`sdk/sqc-sdk.js`):
```javascript
import { SplendorSDK } from 'sqc-sdk';

const sdk = new SplendorSDK('https://rpc.splendor-quantum.network');
const wallet = sdk.createWallet(); // Generates ML-DSA-65 keypair
const addr = wallet.address; // SQ:...

const tx = await sdk.send({
  from: wallet,
  to: 'SQ:recipient...',
  amount: '1000000000000000000', // 1 SPLD
});
console.log('TX:', tx.hash);
```

**React Hooks** (`sdk/connect-react`):
```jsx
import { useSplendorWallet, useSendTransaction } from 'sqc-connect-react';

function App() {
  const { address, connect } = useSplendorWallet();
  const { send, pending } = useSendTransaction();

  return (
    <div>
      {!address && <button onClick={connect}>Connect Quantum Wallet</button>}
      {address && <p>Connected: {address}</p>}
      <button onClick={() => send({ to: '...', amount: '1e18' })}>
        Send 1 SPLD
      </button>
    </div>
  );
}
```

**WebAssembly Bridge** (`sdk/bridge/wasm`):
- Compile C/Rust code to WASM
- Call SQC smart contracts from WASM modules
- Use case: Complex crypto (e.g., custom ZK proof systems) that's too slow in SQC-VM bytecode

### 7.2 QSol Language

**Example: ERC-20-like Token**

```solidity
contract Token {
    mapping(address => uint256) public balances;
    mapping(address => mapping(address => uint256)) public allowances;
    uint256 public totalSupply;

    function transfer(address to, uint256 amount) public returns (bool) {
        require(balances[msg.sender] >= amount, "insufficient balance");
        balances[msg.sender] -= amount;
        balances[to] += amount;
        emit Transfer(msg.sender, to, amount);
        return true;
    }

    function approve(address spender, uint256 amount) public returns (bool) {
        allowances[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }
}
```

Compile:
```bash
./sqc compile examples/token.qsol --output token.bytecode
./sqc deploy --bytecode token.bytecode --network mainnet
```

Deploy returns a contract address `SQ:contract123...`. Interact:
```bash
./sqc call SQ:contract123... transfer SQ:recipient... 1000 --from wallet.json
```

**Differences from Solidity**:
- No `view`/`pure` enforcement (all functions may write state)
- No inheritance (single-contract compilation unit)
- No `msg.data.length` or low-level assembly (security)
- Fixed-size arrays only (`uint256[100]`, not `uint256[]`)—dynamic arrays are future work

---

## 8. Roadmap

### Q2 2026 (Mainnet Launch)
- [x] ML-DSA-65 account system
- [x] Binary Merkle state tree + proofs
- [x] AI validator registration + bidding
- [x] QSol compiler (mappings, arrays, control flow)
- [x] QRC-20/721/1155 standards
- [ ] Governance module (token-weighted voting)
- [ ] Testnet stress test (10,000 TPS sustained for 24h)

### Q3 2026
- [ ] Mobile wallets (iOS/Android with embedded ML-DSA keygen)
- [ ] Hardware wallet support (Ledger/Trezor firmware updates for ML-DSA)
- [ ] Cross-chain bridge (Ethereum ↔ Splendor atomic swaps)
- [ ] Decentralized validator marketplace (agents advertise, users choose)

### Q4 2026
- [ ] Privacy upgrades: X402 v2 with amount hiding (confidential transactions)
- [ ] Sharding (horizontal scaling to 100,000 TPS)
- [ ] AI agent marketplace: plug-and-play validator templates (SaaS)

### 2027+
- [ ] Interoperability with other post-quantum chains (Quantum IBC)
- [ ] Formal verification tools for QSol contracts
- [ ] Hardware acceleration: ASIC miners →ASA AI agent accelerators (specialized chips for bid optimization)

---

## 9. Security Considerations

### 9.1 Quantum Timeline

- **2024–2026**: Noisy quantum computers (50–1000 qubits, high error rates). Not a threat.
- **2028–2030**: Fault-tolerant QC (2000–4000 logical qubits). RSA-2048 broken; ECDSA vulnerable.
- **2032+**: Large-scale QC (10,000+ qubits). All classical public-key crypto unsafe.

**Splendor's position**: Launch in 2026 before the threat materializes. No migration needed—born quantum-safe.

### 9.2 Validator Collusion

**Attack**: 51% of agents coordinate to censor transactions or rewrite history.

**Mitigations**:
- **Economic**: Slashing burns stake—attacking costs more than gained (unless double-spend for $billions).
- **Diversity**: 10,000 agents across jurisdictions harder to coordinate than 5 mining pools.
- **Forkability**: Community can hard-fork to blacklist malicious agents; honest fork keeps users' balances.

### 9.3 Smart Contract Bugs

**QSol is Turing-complete**: Bugs will happen (DAO hack, Parity multisig freeze).

**Safety Layers**:
1. **Gas ceiling**: Infinite loops hit the block gas limit and revert.
2. **Checked arithmetic**: No silent overflows (Solidity 0.8+ style).
3. **Audit tooling**: Static analyzers for reentrancy, access control (future).
4. **Formal verification**: Prove correctness for high-value contracts (future).
5. **Upgradable contracts**: Proxy patterns let developers fix bugs (but centralization risk).

**Philosophy**: Make unsafe patterns hard (e.g., no `call{value:x}()` raw calls), provide safe defaults (e.g., `transfer` reverts on failure), but don't prevent all footguns—developers must understand their code.

---

## 10. Conclusion

Splendor Quantum Blockchain reimagines Layer-1 design for a post-quantum, AI-native world:

- **Quantum-resistant from genesis**: ML-DSA-65 signatures, SHA3-256 hashing, no ECDSA fallback.
- **AI-powered economics**: Validators are autonomous agents bidding on transaction fees, not plutocratic stakers.
- **Transparent and verifiable**: Global Merkle tree with cryptographic inclusion proofs; no "trust the RPC.
- **Developer-friendly**: QSol language, TypeScript SDKs, React hooks, WASM bridge.
- **Permissionless by default, governable when necessary**: Open participation with a safety valve for existential threats.

As quantum computers advance and centralization threatens blockchain's core promise, Splendor offers a path forward: **security through mathematics, decentralization through economics, and resilience through governance**.

**Join the quantum-resistant revolution.**

---

## Appendix A: Comparison to Other Chains

| Feature | Bitcoin | Ethereum | Solana | Cosmos | **Splendor** |
|---------|---------|----------|--------|--------|--------------|
| **Signatures** | ECDSA (secp256k1) | ECDSA (secp256k1) | Ed25519 | Ed25519/secp256k1 | **ML-DSA-65 (PQ)** |
| **Hashing** | SHA-256 | Keccak-256 | SHA-256 | SHA-256 | **SHA3-256 (PQ-safe)** |
| **Consensus** | PoW | PoS (32 ETH stake) | PoS (vote credits) | Tendermint BFT | **AI Bid Market** |
| **Validator Count** | ~15 mining pools | ~900k (but 5 entities control >50%) | ~2000 | ~175 (per chain) | **10,000+ (AI agents)** |
| **State Proofs** | SPV (tx only) | MPT (complex, >1KB proofs) | None (RPC trust) | IBC light client | **Binary Merkle (960B proofs)** |
| **Quantum Resistance** | ❌ Vulnerable | ❌ Vulnerable | ❌ Vulnerable | ❌ Vulnerable | ✅ **Native** |

---

## Appendix B: Technical Glossary

- **ML-DSA-65**: Module-Lattice Digital Signature Algorithm, NIST FIPS 204 (formerly CRYSTALS-Dilithium). Post-quantum signature scheme.
- **SHA3-256**: NIST FIPS 202, Keccak-based hash function. 256-bit output, quantum-resistant (128-bit security under Grover).
- **Merkle Tree**: A hash tree where each leaf is data and each internal node is the hash of its children. Enables O(log N) inclusion proofs.
- **SPLD**: Splendor token, native currency. Smallest unit: 1 SPLD = 10^18 base units.
- **SQC-VM**: Splendor Quantum Chain Virtual Machine. Stack-based bytecode execution engine.
- **QSol**: Solidity subset compiled to SQC-VM bytecode.
- **X402**: Privacy protocol for shielded payments (zero-knowledge proofs).
- **QRC**: Quantum-Resistant Contract token standards (QRC-20 = fungible, QRC-721 = NFT).

---

**Whitepaper Authors:** Splendor Protocol Core Team  
**Contact:** [https://splendor-protocol.network](https://splendor-protocol.network) | [email protected]  
**License:** CC BY
