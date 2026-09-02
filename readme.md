# Splendor Quantum Chain

Splendor Quantum Chain is a new Layer-1 designed around post-quantum account and validator cryptography from genesis. Native accounts, transactions, blocks, and protocol authorizations use ML-DSA-65 (FIPS 204) with purpose-specific signing contexts. Addresses and ledger commitments use SHA3-256 (FIPS 202). Native peer communication is sealed using ML-KEM-768 (FIPS 203)-established secrets and AES-256-GCM, with explicit peer pinning available for authenticated production clusters.

Splendor is not a post-quantum retrofit of an ECDSA ledger. It began with ML-DSA-65 keys as the native identity model. The current Phase 1 implementation uses one authorized validator and should be evaluated as an early-stage chain requiring additional decentralization, operational hardening, audits, tooling, and certification before institutional production use.

---

## What Is Built

### Native ML-DSA-65 Identity

Every account address on Splendor derives from an ML-DSA-65 public key:

```
address = "SQ:" + SHA3-256(dilithium_public_key)
```

Signature operations use purpose-specific domain contexts (`SPLENDOR-QC-TX-v1`, `BLOCK-v1`, etc.). ML-DSA-65 key sizes: 1,952-byte public keys, 4,032-byte private keys, 3,309-byte signatures.

### ML-KEM-768 Sealed P2P

Native node-to-node communication is sealed by default. Peers establish a session secret using ML-KEM-768 and protect all transported data with AES-256-GCM. Plaintext P2P is disabled and requires the explicit `--allow-plaintext-p2p` flag to enable.

For authenticated production clusters, operators should configure explicit peer identity pins and enable `--require-peer-pins`. Without this flag, first contact uses trust-on-first-use.

### Optional Hybrid TLS

When the node terminates TLS directly (`--tls-cert` / `--tls-key`), it offers the TLS 1.3 `X25519MLKEM768` hybrid group. Operators requiring every accepted connection to use the hybrid group must enable `--tls-require-hybrid`. Without that flag, classical X25519 fallback remains available for compatibility.

A reverse proxy, CDN, or load balancer negotiates its own client-facing cryptography. The node cannot make an upstream proxy connection post-quantum merely because the inner node TLS offers a hybrid group.

### SHA3-256 Merkle State

The chain maintains a binary SHA3-256 Merkle commitment covering accounts, contract code, contract storage, and X402 nonce state. Every block header includes this state root, computed and signed by the validator.

### Transaction Types

| Type | Description |
|------|-------------|
| `TxTransfer` | SPLD transfer between accounts |
| `TxDeploy` | Deploy SQC-VM bytecode contract |
| `TxCall` | Call a deployed contract or native module |
| `TxX402` | ML-DSA-authorized machine/API payment settlement |
| `Register` | Reserved, unused |

### Native Module Router

The module router exposes built-in contract modules without requiring bytecode deployment:

- **QRC-20** — fungible tokens
- **QRC-721** — NFTs
- **QRC-1155** — multi-token
- **QRC-4626** — yield vaults
- **DEX** — decentralized exchange
- **Lending** — lending protocol
- **Governance** — on-chain voting
- **Staking** — validator stake management
- **Oracle** — price data
- **Multisig** — multi-signature accounts
- **Exchange** — advanced exchange routing
- **Bridge** — cross-chain bridge (fork-gated)

### X402 Machine Payment Protocol

Splendor includes a native X402 transaction and settlement path:

- ML-DSA-65 payment authorization
- ML-DSA-65 payer-signed settlement transactions
- payment-to-transaction binding
- amount and receiver validation
- balance validation
- validity windows and chain ID binding
- on-chain nonce replay protection
- settlement through the normal mempool and block pipeline

**Current limitation:** The legacy `sdk/sqc-sdk.js` middleware verifies X402 access but does not automatically complete on-chain settlement because it omits the required payer-signed settlement transaction. The newer TypeScript SDK (`sdk/bridge/`) handles this correctly. Applications should use the current payer-side `payX402`/settlement flow until the legacy middleware is fully corrected.

### SQC-VM and QSol Compiler

**SQC-VM** is a custom bytecode virtual machine. Contracts deploy as SQC-VM bytecode and execute in isolation. The VM supports arithmetic, storage (SLOAD/SSTORE), memory, events (LOG), SHA3-256, call dispatch, and revert/require.

**QSol compiler** (`compiler/`, `cmd/sqc-solc/`) is a working prototype:

```bash
go run ./cmd/sqc-solc compiler/examples/token.qsol
```

The compiler accepts a Solidity-flavored QSol subset and emits deployable SQC-VM v4 bytecode with ABI/selector metadata. **Current subset supports:**

- Scalar state: `uint256`, `address`, `bool`, `string`/`bytes32`
- Mappings: `mapping(K => V)`
- Functions with value-type parameters
- Local variables, checked arithmetic (overflow reverts)
- Control flow: `if`/`else if`/`else`, `for`, `while`
- Loop control: **`break`**, **`continue`**
- Internal function calls (`private`/`internal` functions calling each other)
- String literals (stored as `bytes32`, max 32 chars)
- Events (`emit E(args)`)
- `msg.sender`, `msg.value`
- `require(cond, "reason")`

**Not yet supported:** dynamic arrays (`uint256[]`), structs, inheritance, constructor arguments, full Solidity ABI encoding, or import statements.

This is not a production-complete Solidity toolchain. Production language completeness, audits, developer tooling, package management, source verification, and IDE integration remain future work.

---

## Native Account Signature Comparison

| Feature | Ethereum | Solana | Splendor Phase 1 |
|---------|----------|--------|-----------------|
| Native account signature scheme | secp256k1 / ECDSA | Ed25519 | ML-DSA-65 |
| Native ledger built with PQ account keys | No | No | Yes |
| Dedicated ML-DSA X402 settlement transaction | No | No | Yes |
| Native P2P ML-KEM sealed transport | No | No | Yes |
| Smart-contract support | Yes (EVM) | Yes (Sealevel) | Yes (SQC-VM + native modules) |
| High-level compiler | Mature Solidity toolchain | Mature Rust/Anchor | Prototype QSol-subset compiler |
| Consensus model | Distributed proof of stake | Distributed proof of stake | Single authorized validator |
| Block/slot interval | ~12-second slots | ~400 ms slots | 2-second configured default |
| Decentralized/economic finality | Yes | Yes | No — Phase 1 is single authorized validator |
| EVM compatibility | Native | No | No |

---

## Cryptography

The chain uses algorithms standardized in FIPS 203 (ML-KEM), FIPS 204 (ML-DSA), and FIPS 202 (SHA-3). The repository runs a subset of published NIST ACVP test vectors. Passing those vectors is not a NIST validation certificate. Splendor does not currently have CAVP/CMVP validation, a FIPS 140-3 module boundary, or an independent cryptographic audit.

The chain is designed to resist attacks from cryptographically relevant quantum computers under the current security assumptions of ML-DSA, ML-KEM, SHA3-256, and AES-256-GCM. No implementation provides absolute guarantees of future cryptographic security.

---

## Phase 1 Consensus

Phase 1 provides single-authority block production: one authorized validator produces and signs blocks approximately every two seconds under the default configuration. This is fast and deterministic, but it is **not** decentralized economic or Byzantine finality. A compromised validator key or host is a critical risk under Phase 1.

Multi-validator BFT (`consensus/bft.go`) exists in the codebase but is not wired into the running node. Permissionless validator onboarding and decentralized finality remain future work.

---

## Performance

A dated single-host experiment (see `docs/performance/2026-08-25-tps-gpu-experiment.md`) measured approximately **2,605–2,901 transactions per second** through the tested block-production pipeline, depending on batch size. This was measured on a specific AWS host configuration, not a public multi-node network.

This is not a public-network sustained-throughput result, not a multi-validator benchmark, and not evidence of national payment scale. Every performance number must be read in context of the exact host, batch size, transaction type, and test methodology in that document.

---

## Wallet and Key Management

- Wallet files (`.sqwallet`) use **version 2** format: AES-256-GCM encryption, Argon2id password derivation
- Version 1 (legacy Dilithium3) files are deliberately rejected with a "regenerate wallet" error
- The Chrome browser extension (`wallet-extension/`) provides in-browser ML-DSA-65 key management with WASM-based signing

---

## Running a Node

```bash
# Clone and install (Go 1.25+ required)
git clone https://github.com/Splendor-Protocol/splendor-quantum-blockchain
cd splendor-quantum-blockchain
bash install.sh --validator   # or --rpc for sync-only node
```

**Validator service flags:**
```bash
sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \      # loopback by default
  --validator \
  --wallet /var/sqc/validator.sqwallet \
  --genesis /var/sqc/genesis.json \
  --genesis-doc-hash <hash>
```

**RPC node:**
```bash
sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --peer http://VALIDATOR_IP:8080 \
  --peer-pin http://VALIDATOR_IP:8080=<pubkey_hex> \
  --require-peer-pins \
  --validator-pubkey-hex <hex>
```

Management: `sqc status` | `sqc logs` | `sqc update` | `sqc update --clean`

---

## SDK and Integration

```bash
# JavaScript/TypeScript SDK (post-quantum WASM signing)
npm install @splendor-protocol/sdk

# Node.js
import { connectSplendor } from '@splendor-protocol/sdk'
const session = await connectSplendor({ network: 'mainnet', connect: 'seed' })
await session.transfer({ to: 'SQ:...', amount: '1000000000000000000' })
```

Native ML-DSA seed connector is the primary path. MetaMask/EVM derivation is available as a legacy path and is **not quantum-safe** (derives ML-DSA key from an secp256k1 ECDSA signature).

---

## What Is Built vs. What Is Next

### Built in code

- Go full node (`cmd/node/`)
- ML-DSA-65 wallet, transaction, block, and protocol signing
- SHA3-256 native hashing and Merkle commitments
- Signed height-zero genesis builder
- ML-KEM-768 sealed native P2P communication
- Optional node-terminated hybrid TLS (`X25519MLKEM768`)
- Encrypted `.sqwallet` format (v2, Argon2id)
- Mempool, execution, receipts, chain storage, and current-state RPC
- SQC-VM bytecode interpreter
- Native QRC-20, QRC-721, QRC-1155, QRC-4626, DEX, Lending, Governance, Staking, Oracle, Multisig, Exchange, Bridge (fork-gated) module router
- X402 authorization and payer-signed settlement transaction
- JavaScript/TypeScript SDK and WASM signing components (`sdk/bridge/`)
- **Prototype QSol-subset compiler** (`compiler/`) and `sqc-solc` CLI
- Adaptive mempool-depth scheduler (`ai/scheduler.go`)
- Chrome browser wallet extension (`wallet-extension/`)
- Fleet deployment tooling (`cmd/sqc-deploy/`)
- Cross-chain bridge relayer (`cmd/bridge-relayer/`)

### Built but requiring integration or hardening

- Legacy X402 middleware settlement path (in progress)
- QSol language completeness (dynamic arrays, structs, constructor args)
- Bridge tier production activation and deployment
- Native-PQ-first wallet UX (MetaMask still appears; seed connector is available)
- Full CI green run on every PR
- Release reproducibility and binary signing

### Not built or not production-ready

- Multi-validator consensus in the running node
- Permissionless validator onboarding
- Decentralized Byzantine finality
- Sparse Merkle proof API for light clients and auditors
- Historical state queries
- Production block explorer web UI
- Sanctions/KYC/AML compliance applications
- Formal regulatory reporting product
- Desktop or mobile wallet applications
- NFC or air-gapped QR signing hardware integration
- HSM/remote validator signer
- Independent security audit
- CAVP/CMVP or FIPS 140-3 validation
- Production RTGS or CBDC deployment

---

## Positioning

Splendor was designed as a natively post-quantum Layer-1 combining ML-DSA-65 ledger signatures, ML-KEM-768-sealed native P2P communication, and a built-in ML-DSA X402 payment settlement path.

The updated README must be read as: **what the architecture was designed to do** → **what is implemented in code** → **what tests verify** → **what benchmarks measured** → **what remains roadmap**.

If evidence does not exist, the claim is not made here.

---

## AI Network (Planned)

Splendor's next product layer is a decentralized AI model, storage, inference, and creator-payment network. Model weights and computation will remain off-chain, while Splendor provides post-quantum identity, model-manifest registration, usage receipts, licensing records, and automatic settlement. This AI network is a planned product built on the existing chain foundation; it is not implemented by the current adaptive node scheduler (`ai/scheduler.go`).

---

## Repository Structure

```
cmd/node/          — Full node binary
cmd/wallet/        — CLI wallet
cmd/sqc-solc/      — QSol compiler CLI
cmd/bridge-relayer/— EVM↔SQC bridge relayer
cmd/sqc-deploy/    — Fleet deployment tool
compiler/          — QSol compiler (lexer → parser → codegen → bytecode)
core/crypto/       — ML-DSA-65, ML-KEM-768, wallet store
core/block/        — Block execution engine
core/state/        — StateDB, Merkle state
core/types/        — Block, transaction, account types
network/           — ML-KEM P2P, sync, peer pinning
rpc/               — HTTP/WS RPC server, middleware, routes
vm/                — SQC-VM, module router, all native modules
sdk/               — JavaScript/TypeScript SDK, WASM signing
wallet-extension/  — Chrome browser extension
x402/              — X402 payment protocol
```

## License

See [LICENSE](LICENSE).
