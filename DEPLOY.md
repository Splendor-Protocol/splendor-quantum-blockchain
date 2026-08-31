# Splendor Quantum Chain — Ubuntu Server Deployment Guide

**Version:** 1.0 | **OS:** Ubuntu 22.04 LTS (recommended) / 20.04 LTS  
**Architecture:** x86_64 | **Min RAM:** 4 GB | **Min Disk:** 50 GB SSD

---

## Table of Contents

1. [Architecture & Data Flow](#1-architecture--data-flow)
2. [Prerequisites](#2-prerequisites)
3. [Installation — Full Node](#3-installation--full-node)
4. [Installation — Validator Node](#4-installation--validator-node)
5. [Configuration Reference](#5-configuration-reference)
6. [Genesis Setup](#6-genesis-setup)
7. [Running as a System Service](#7-running-as-a-system-service)
8. [Monitoring & Health Checks](#8-monitoring--health-checks)
9. [Firewall & Security](#9-firewall--security)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. Architecture & Data Flow

### System Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              EXTERNAL WORLD                  │
                    │  Wallets · DApps · Block Explorer · Bots    │
                    └──────────────────┬──────────────────────────┘
                                       │ HTTPS / REST / WebSocket
                    ┌──────────────────▼──────────────────────────┐
                    │           RPC LAYER  (:8080)                 │
                    │                                              │
                    │  /tx/submit    /block/{h}   /balance/{addr} │
                    │  /vm/disasm    /healthz      /readyz         │
                    │  /gov/*        /staking/*    /dex/*          │
                    │  /ws           /metrics      /peers          │
                    └──────────────────┬──────────────────────────┘
                                       │ internal Go calls
          ┌──────────────────┬─────────▼─────────┬────────────────┐
          │                  │                   │                 │
   ┌──────▼──────┐  ┌────────▼──────┐  ┌────────▼────────┐  ┌────▼──────┐
   │   MEMPOOL   │  │  BLOCK EXEC   │  │   STATE DB      │  │   CHAIN   │
   │             │  │  (executor)   │  │   (LevelDB)     │  │   DB      │
   │ Pending txs │  │               │  │                 │  │ (LevelDB) │
   │ fee-sorted  │  │ sig verify    │  │  accounts       │  │           │
   │ sig checked │  │ nonce check   │  │  contracts      │  │  blocks   │
   │ 50K cap     │  │ balance check │  │  snapshots      │  │  receipts │
   └──────┬──────┘  │ VM execution  │  │  x402 nonces    │  │  tx index │
          │         └──────┬────────┘  └─────────────────┘  └───────────┘
          │                │
   ┌──────▼────────────────▼───────────────────────────────────────────┐
   │                    CONSENSUS LAYER                                 │
   │                                                                    │
   │  Phase 1:  Single Validator                                        │
   │    - Pulls pending txs from mempool every 2s                      │
   │    - Asks AI scheduler for batch size recommendation              │
   │    - Executes transactions → generates receipts                   │
   │    - Signs block with ML-DSA-65 key                        │
   │    - Broadcasts block to all peers via P2P                        │
   │                                                                    │
   │  Phase 2:  BFT Multi-Validator (future)                           │
   │    - All validators sign votes with ML-DSA-65                     │
   │    - 2/3+1 quorum required to commit                             │
   └───────────────────────────────────────────────────────────────────┘
          │
   ┌──────▼───────────────────────────────────────────────────────────┐
   │                    AI SCHEDULER                                   │
   │  - EMA of mempool depth → recommends batch size                  │
   │  - Anomaly detection (spike = potential spam attack)             │
   │  - Runs every 500ms, adjusts batch 100–10,000 txs               │
   └───────────────────────────────────────────────────────────────────┘
          │
   ┌──────▼───────────────────────────────────────────────────────────┐
   │            P2P  (same RPC port — there is no separate listener)  │
   │  - Broadcasts new blocks to peers over the ML-KEM-768 channel     │
   │  - Peer Exchange (PEX): discovers new peers                       │
   │  - Block Sync: pulls missing blocks over the same sealed channel  │
   │  - All blocks ML-DSA-65-verified before applying                  │
   └──────────────────────────────────────────────────────────────────┘
```

### Transaction Data Flow (Step by Step)

```
Step 1: User creates transaction
  wallet.buildTransfer(from, to, amount, fee, nonce)
  → unsigned transaction object

Step 2: User signs with ML-DSA-65 key
  sig = ML_DSA_65.Sign(private_key, SHA3-256(tx_body), "SPLENDOR-QC-TX-v1")
  tx.signature = sig      (3,309 bytes)
  tx.public_key = pub_key (1,952 bytes, first tx only)

Step 3: Submit to node
  POST /tx/submit  { signed transaction JSON }
  
Step 4: Mempool validation
  ✓ Chain ID check
  ✓ ML-DSA-65 signature verification
  ✓ Nonce valid (matches account nonce)
  ✓ Balance sufficient (amount + fee)
  ✓ Fee ≥ minimum (1,000 units)
  ✓ Not duplicate
  → Added to fee-sorted priority queue

Step 5: Block production (every 2 seconds)
  AI scheduler → recommended batch size
  Pull top N txs from mempool by fee
  For each tx:
    ✓ Re-verify signature
    ✓ Execute (transfer / deploy / call / X402)
    ✓ Generate receipt (status, gas_used, logs)
    ✓ Collect fees

Step 6: Block finalisation
  Compute SHA3-256 Merkle root of tx hashes
  Compute SHA3-256 state root (all account states)
  Build block header
  Sign block header with validator ML-DSA-65 key
  Persist block to LevelDB

Step 7: Broadcast
  POST /block → all known peers
  Peers verify ML-DSA-65 block signature
  Peers apply block to their state
  Peers index txs and store receipts

Step 8: Confirmation
  GET /tx/{hash}/receipt → returns:
    { status: 1, block_height: N, gas_used: ..., logs: [...] }
```

### Cryptographic Primitives

| Operation | Algorithm | Standard | Key Size |
|-----------|-----------|----------|----------|
| Transaction signing | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | 4,032 byte private key |
| Signature verification | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | 1,952 byte public key |
| Block signing (validator) | ML-DSA-65 | ML-DSA-65 (NIST FIPS 204) | same |
| Address derivation | SHA3-256(pubkey) | NIST FIPS 202 | 32 byte output |
| Block hash | SHA3-256(header) | NIST FIPS 202 | 32 byte output |
| State root | SHA3-256 binary Merkle tree over accounts, contract code, storage slots, X402 nonces | NIST FIPS 202 | 32 byte output |
| Wallet encryption | AES-256-GCM | NIST FIPS 197 | 256 bit key |
| Key derivation | Argon2id | RFC 9106 | — |
| P2P channel (push, announce, sync) | ML-KEM-768 + AES-256-GCM | NIST FIPS 203 | 1,184 byte public key |
| Node-terminated TLS 1.3 | X25519MLKEM768 hybrid group | TLS 1.3 | — |

**On-chain: zero ECDSA, zero SHA-256, zero keccak256, zero secp256k1.** A
TLS-terminating proxy in front of the node is a separate matter — see section 9.

---

## 2. Prerequisites

### Hardware Requirements

| Role | CPU | RAM | Disk | Network |
|------|-----|-----|------|---------|
| Full node | 2 cores | 4 GB | 50 GB SSD | 10 Mbps |
| Validator | 4+ cores | 8 GB | 100 GB SSD | 100 Mbps |

### System Update

```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y curl wget git build-essential jq ufw
```

### Install Go 1.25

`go.mod` declares `go 1.25.0`. An older toolchain will not build this repository.

```bash
# Download Go
wget -q https://go.dev/dl/go1.25.0.linux-amd64.tar.gz -O /tmp/go.tar.gz

# Install
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz

# Add to PATH (add to ~/.bashrc for persistence)
export PATH=$PATH:/usr/local/go/bin
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# Verify
go version
# Expected: go version go1.25.0 linux/amd64
```

### Install Docker (optional — for containerised deployment)

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
newgrp docker
docker --version
```

---

## 3. Installation — Full Node

### Clone and Build

```bash
# Clone the repository
git clone https://github.com/Splendor-Protocol/splendor-quantum-blockchain.git
cd splendor-quantum-blockchain

# Build binaries
make build

# Verify
./sqc-node --help
./sqc-wallet --help
```

### Or Build with Docker

```bash
docker build -t splendor-quantum-chain:latest .
```

### Create data directory

```bash
sudo mkdir -p /var/sqc/data
sudo chown $USER:$USER /var/sqc/data
```

### There is no config file

`cmd/node` is flag-driven only. It registers no `config` subcommand and never
calls `config.LoadFile`, so a `config.json` on disk is read by nothing. The
`config/` package exists and parses such a file, but nothing in the shipped
binary is wired to it. Earlier revisions of this guide printed a `config.json`
to create; that step has been removed because following it did nothing.

Everything below is set with flags or environment variables. `./sqc-node --help`
is the authoritative list.

### Run Full Node

```bash
./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --rpc-auth-token "$SQC_RPC_AUTH_TOKEN" \
  --log-level info
```

**Secure defaults you should know about:**

| Default | Why | Override |
|---|---|---|
| Binds `127.0.0.1:8080` | A node should not become internet-reachable just by starting. Put a TLS-terminating proxy in front. | `--rpc-addr 0.0.0.0:8080` |
| Metrics bind `127.0.0.1:9100` | Metrics expose peer counts, mempool depth, and height — useful to an attacker. | `--metrics-addr` |
| No CORS headers | The old `Access-Control-Allow-Origin: *` let any web page script a visitor's node. | `--cors-origin https://app.example` (repeatable), or `--cors-origin '*'` |
| Write plane disabled without a token | `POST /block` applies a block to local state; anonymous access is a chain-integrity hole. Every node in a cluster must share the token. | `--rpc-auth-token` / `SQC_RPC_AUTH_TOKEN` |
| Encrypted P2P required | Block push, peer announce, and catch-up sync are all sealed with ML-KEM-768. Plaintext fallback is off so an attacker cannot force a downgrade by blocking `/p2p/identity`. | `--allow-plaintext-p2p` |
| 25 req/s per IP on submission | Each `/tx/submit` costs an ML-DSA verification. | `--rpc-rate-limit`, `--rpc-rate-burst` |

**Post-quantum TLS.** With `--tls-cert` and `--tls-key`, the node serves TLS 1.3
and offers `X25519MLKEM768`, a hybrid group that stays secure unless *both*
X25519 and ML-KEM-768 are broken. Without it, RPC traffic is recordable today
and decryptable once a CRQC exists.

```bash
./sqc-node --data-dir /var/sqc/data --rpc-addr 0.0.0.0:8443 \
  --tls-cert /etc/sqc/fullchain.pem --tls-key /etc/sqc/privkey.pem \
  --public-url https://rpc.example.org \
  --rpc-auth-token "$SQC_RPC_AUTH_TOKEN"
```

Behind a reverse proxy, add `--trust-proxy` so rate limits key off the real
client IP — but **only** when the proxy is yours, since otherwise anyone can
spoof `X-Forwarded-For` and mint a fresh rate-limit bucket per request.

### Verify Node is Running

```bash
# Health check
curl http://localhost:8080/healthz

# Expected:
# {"status":"ok","uptime_seconds":5,"chain_id":"splendor-quantum-1","node_version":"1.0.0-quantum","timestamp":...}

# Check status
curl http://localhost:8080/status

# Check latest block
curl http://localhost:8080/block/latest
```

---

## 4. Installation — Validator Node

### Generate Validator Wallet

```bash
# Generate a new ML-DSA-65 keypair for the validator
./sqc-wallet generate --out /var/sqc/validator.sqwallet

# You will be prompted for a password.
# SAVE THIS PASSWORD. It is never stored.
# BACK UP /var/sqc/validator.sqwallet securely (e.g., encrypted USB).

# Show your validator address
./sqc-wallet address --wallet /var/sqc/validator.sqwallet
# Output: SQ:a7f3c9d2...  ← your validator SQ: address
```

### Get Your Validator Public Key (for genesis/config)

```bash
# The validator pubkey hex is needed in genesis.json
# You can get it from your wallet file:
cat /var/sqc/validator.sqwallet | python3 -c "
import json, sys, base64
w = json.load(sys.stdin)
# Public key is stored in base64; convert to hex for config
pk = w.get('public_key', '')
if pk:
    print(base64.b64decode(pk).hex())
"
```

### Create Genesis File

Create `/var/sqc/genesis.json`:

```json
{
  "chain_id": "splendor-quantum-1",
  "network_name": "Splendor Quantum Chain Mainnet",
  "timestamp": 1754000000,
  "validator_pubkey_hex": "YOUR_VALIDATOR_PUBKEY_HEX_HERE",
  "allocations": [
    {
      "address": "SQ:YOUR_VALIDATOR_ADDRESS_HERE",
      "balance": "26000000000000000000000000000",
      "comment": "Genesis validator allocation — 26 billion SPLD"
    }
  ],
  "total_supply": "26000000000000000000000000000"
}
```

Replace:
- `YOUR_VALIDATOR_PUBKEY_HEX_HERE` with the hex from the step above
- `YOUR_VALIDATOR_ADDRESS_HERE` with your validator SQ: address

### Run Validator Node

```bash
# Set password as environment variable (more secure than --wallet-pass flag)
export SQC_WALLET_PASS="your-secure-password-here"

# The write-plane token: needed to push blocks to the full nodes in the cluster.
# Every node that accepts this validator's blocks must carry the same value.
export SQC_RPC_AUTH_TOKEN="$(openssl rand -hex 32)"

# Run validator. 127.0.0.1 is the node's own default: a validator has no reason
# to answer the internet directly — put a proxy in front, or scope a firewall
# rule to the peers that need it (section 9).
./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr 127.0.0.1:8080 \
  --genesis /var/sqc/genesis.json \
  --validator \
  --wallet /var/sqc/validator.sqwallet \
  --block-interval 2 \
  --log-level info
```

`--genesis` is what mints the allocations, and only on an empty chain. Section 6
explains what goes wrong without it.

### Verify Validator is Producing Blocks

```bash
# Watch logs for block production
journalctl -u sqc-validator -f | grep "block produced"

# Check latest block height (should increment every 2 seconds)
watch -n 2 "curl -s http://localhost:8080/block/latest | python3 -m json.tool | grep height"
```

---

## 5. Configuration Reference

`sqc-node` takes flags, not a config file (see section 3). This table is
generated from `cmd/node/main.go`; `./sqc-node --help` is the authority if the
two ever disagree.

### Core

| Flag | Default | Notes |
|---|---|---|
| `--data-dir` | `./sqc-data` | Chain, state, and tx-index LevelDBs live here |
| `--rpc-addr` | `127.0.0.1:8080` | Loopback on purpose. P2P routes share this port |
| `--metrics-addr` | `127.0.0.1:9100` | Prometheus. Leaks height, mempool depth, peer count — keep it private |
| `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `--validator` | `false` | Produce blocks |
| `--block-interval` | `2` | Seconds between blocks (validator only) |
| `--genesis` | — | Path to `genesis.json`. Allocations are applied once, on an empty chain |
| `--chain-id` / `SQC_CHAIN_ID` | — | Chain id this node expects (e.g. `splendor-quantum-devnet-1`). Set, a genesis document naming another chain is refused; empty, the node adopts whatever the verified genesis says |

### Validator key

| Flag | Notes |
|---|---|
| `--wallet` | Path to the validator `.sqwallet`. Must be mode `600` or startup fails |
| `--wallet-pass-file` | Read the passphrase from an owner-only file |
| `SQC_WALLET_PASS` | Environment variable alternative |
| `--wallet-pass` | **Avoid.** Visible to every local user through `ps`. The node warns |

### Peering and trust

| Flag | Default | Notes |
|---|---|---|
| `--peer` | — | Bootstrap peer RPC address, repeatable |
| `--validator-pubkey-hex` | — | Trusted genesis validator key. A full node MUST set this (or `--genesis-hash`) to authenticate pushed blocks |
| `--genesis-hash` | — | Pinned hash of block 0. Without one of these two, a node adopts whatever key its first peer serves |
| `--peer-pin` | — | `URL=PUBKEY_HEX`, repeatable. Without a pin, peer identities are trust-on-first-use. See **Obtaining a peer pin** below — the endpoint serves base64, not hex |
| `--require-peer-pins` | `false` | Refuse any unpinned peer instead of trusting it on first contact. Worth turning on once the cluster's peers are known |
| `--genesis-doc-hash` | — | Pin for a peer-served `genesis.json`, printed by `sqc-node genesis-hash <path>`. A node with no local `--genesis` credits a peer's genesis only if it hashes to this |
| `--trust-peer-genesis` | `false` | Credit a peer-served genesis with no pin. The peer can then mint any balance it likes into this node's state. Lab networks only |
| `--allow-private-peers` | `false` | Permit loopback/private-IP peers. Needed for a local multi-node cluster; off in public deployments so `/peers/announce` cannot be used for SSRF |
| `--allow-plaintext-p2p` | `false` | Fall back to cleartext when a peer serves no sealed transport. Leave off: the default is what stops a downgrade attack. Cannot be combined with `--require-peer-pins` — the node refuses to start, because a plaintext channel carries no identity to pin |

#### Obtaining a peer pin

`--peer-pin` takes hex, and `/p2p/identity` serves base64. Copying the field
straight out of the response produces a pin that can never match, so decode it:

```bash
PEER=https://validator.example:8080
curl -fsS "$PEER/p2p/identity" | python3 -c \
  "import base64,json,sys;print(base64.b64decode(json.load(sys.stdin)['node_pubkey']).hex())"
```

Pass the result as `--peer-pin $PEER=<hex>`, and add `--require-peer-pins` so an
unpinned peer is refused rather than trusted. Two things worth knowing before
you rely on it:

- **A validator's node identity is its wallet key.** A non-validator generates a
  separate identity at `<data-dir>/node_identity.key`, so wiping a follower's
  data directory changes its identity and invalidates anyone's pin of it.
- **Take the pin over a channel you trust.** Reading it from the peer you are
  about to pin is trust-on-first-use with extra steps; it is only better than
  TOFU if the value is confirmed out of band, or if you fetch it over TLS from a
  host you already authenticate.

`install.sh` does this automatically when provisioning an RPC node, and fails
the install rather than writing an unpinned unit if the identity cannot be read.

### Write plane and exposure

| Flag | Default | Notes |
|---|---|---|
| `--rpc-auth-token` / `SQC_RPC_AUTH_TOKEN` | — | Shared secret for `POST /block` and `POST /peers/announce`, sent as the `X-Node-Auth` header. Without it those routes return `503`, not open access |
| `--cors-origin` | none | Repeatable. `'*'` allows any origin — that means any web page a user visits can drive their node |
| `--trust-proxy` | `false` | Read the client IP from `X-Forwarded-For`. Only behind a proxy you control, or callers spoof it and bypass rate limits |
| `--tls-cert` / `--tls-key` | — | Serve TLS 1.3 directly, offering the `X25519MLKEM768` hybrid group |
| `--public-url` | derived | Base URL advertised to peers |
| `--node-key-pass-file` / `SQC_NODE_KEY_PASS` | — | Encrypt `node_identity.key` at rest (Argon2id + AES-256-GCM). Without it the key is plaintext at mode 0600, and anyone who can read the file can impersonate this node to its peers. Setting it on a node that already has a plaintext key re-encrypts that key in place, so the node's peer identity — and any pins on it — stay valid. There is no command-line form: an argument is visible to every local user through `ps` |

### Rate limits

| Flag | Default | Notes |
|---|---|---|
| `--rpc-rate-limit` | `25` | Requests/second per IP on tx submission and x402 routes; `0` disables |
| `--rpc-rate-burst` | `50` | Burst allowance for the above |
| `--sim-rate-limit`, `--sim-rate-burst` | built-in | VM-executing routes: `/call`, `/estimate`, `/logs` |
| `--sim-max-inflight` | built-in | Concurrent VM simulations across all callers; excess gets `503`. This, not the rate limit, is what bounds anonymous CPU use |
| `--sim-timeout` | built-in | Wall-clock ceiling on one simulation |

### Fixed at compile time

Some values in this document read like settings but are constants: the
mempool cap is 50,000 transactions (`core/mempool/mempool.go`), the minimum fee
hook is 1,000 units, and the AI scheduler's batch bounds live in
`ai/scheduler.go`. Changing any of them means editing and rebuilding.

`chain_id` is **not** one of them. The node takes it from the verified genesis
document (`core/types.DefaultChainID`, `splendor-quantum-1`, is only what a
binary assumes before it has seen one) and binds it into every transaction,
permit and order signature; `--chain-id` / `SQC_CHAIN_ID` pins the expected
value so a node refuses a genesis for another tier instead of adopting it.

---

## 6. Genesis Setup

### Understanding the Genesis Block

The genesis block (block 0) is the first block on the chain. It:
- Records the initial SPLD balances for all pre-funded accounts
- Registers the genesis validator's ML-DSA-65 public key
- Is signed by the genesis validator
- Cannot be changed after creation — it is the chain's root of trust

### Step-by-Step Genesis Creation

```bash
# Step 1: Generate validator wallet
./sqc-wallet generate --out /var/sqc/validator.sqwallet

# Step 2: Get validator address
VALIDATOR_ADDR=$(./sqc-wallet address --wallet /var/sqc/validator.sqwallet)
echo "Validator address: $VALIDATOR_ADDR"

# Step 3: Get validator public key in hex
VALIDATOR_PUBKEY=$(cat /var/sqc/validator.sqwallet | python3 -c "
import json, sys, base64
w = json.load(sys.stdin)
pk_b64 = w.get('public_key', '')
if pk_b64:
    print(base64.b64decode(pk_b64 + '==').hex())
" 2>/dev/null)
echo "Validator pubkey (hex): ${VALIDATOR_PUBKEY:0:32}..."

# Step 4: Create genesis.json
cat > /var/sqc/genesis.json << EOF
{
  "chain_id": "splendor-quantum-1",
  "network_name": "Splendor Quantum Chain Mainnet",
  "timestamp": $(date +%s),
  "validator_pubkey_hex": "$VALIDATOR_PUBKEY",
  "allocations": [
    {
      "address": "$VALIDATOR_ADDR",
      "balance": "26000000000000000000000000000",
      "comment": "Genesis: 26 billion SPLD"
    }
  ],
  "total_supply": "26000000000000000000000000000"
}
EOF

echo "Genesis file created: /var/sqc/genesis.json"
cat /var/sqc/genesis.json

# Step 5: Start validator. --genesis is NOT optional here.
export SQC_WALLET_PASS="your-password"
./sqc-node \
  --data-dir /var/sqc/data \
  --genesis /var/sqc/genesis.json \
  --validator \
  --wallet /var/sqc/validator.sqwallet

# Step 6: Verify genesis block AND that the allocation actually landed
sleep 3
curl http://localhost:8080/block/0
curl "http://localhost:8080/balance/$VALIDATOR_ADDR"    # must not be 0
```

⚠️ **Omitting `--genesis` produces a chain with no money in it.** Allocations are
applied by `Genesis.ApplyToState`, which the node calls only when `--genesis` is
set *and* the chain is empty. Start the validator without it and block 0 is
signed over an empty state: `total_supply` is never minted, every balance is
zero, and no transaction can meet the 1,000-unit minimum fee. The node logs
`Applied genesis allocations` when it works — if that line is absent from the
first startup, stop, delete `/var/sqc/data`, and start again with the flag.
There is no way to apply allocations to a chain that has already produced
block 0.

### Fork Schedule

`genesis.json` may carry a `forks` object that schedules consensus rule changes
by block height. It is part of the document's identity: `sqc-node genesis-hash`
covers it, so two documents that differ only in a schedule get different
`--genesis-doc-hash` pins. A document without one hashes exactly as it did
before the field existed.

```json
"forks": {
  "module_revert_v1": 1,
  "qrc20_v2": 1,
  "qrc1155_v1": 1,
  "exchange_v1": 1
}
```

- **A height is the first block produced under the new rules.** `0` or an absent
  name means *never* — genesis is block 0, so `0` cannot mean "from genesis".
  The convention for a fresh chain is to schedule every rule it starts with at
  height **1**.
- **Names.** `multi_validator` is reserved today. The settlement upgrades are
  `module_revert_v1`, `qrc20_v2`, `qrc1155_v1` and `exchange_v1`; each becomes
  schedulable only in the release that implements it. `qrc20_v2`, `qrc1155_v1`
  and `exchange_v1` each require `module_revert_v1` at the same or an earlier
  height (a failed module call must be a failed receipt, not a dropped
  transaction that pins the sender's nonce). Devnet and testnet
  schedule all four at 1; **mainnet omits `exchange_v1`** (the real-money gate
  stays closed).
- **A name the binary does not implement is refused at boot**, not ignored: a
  node cannot start on a document it does not understand. Deploy the binary that
  knows a name everywhere *before* using a document that schedules it, and
  compute the document's pin with that binary — an older one refuses the file.
- **The schedule is persisted with the chain.** When genesis is applied (from
  `--genesis`, or from a peer by a node started without one) the node records
  the schedule in its state DB before it credits a single allocation. On every
  later boot that stored copy is authoritative: a follower restarted without
  `--genesis` keeps its schedule, and a node whose `--genesis` file is missing,
  unreadable, or schedules something different **refuses to boot** rather than
  run under rules the rest of the network does not.
- **Check it before the height arrives:** `curl http://localhost:8080/status`
  reports `forks` on every node that has one. Nodes disagreeing about an
  activation height do not fail at startup — they split at the height.

---

## 7. Running as a System Service

### Create systemd service

```bash
# Create service file
sudo tee /etc/systemd/system/sqc-node.service << 'EOF'
[Unit]
Description=Splendor Quantum Chain Node
Documentation=https://github.com/Splendor-Protocol/splendor-quantum-blockchain
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sqc
Group=sqc
WorkingDirectory=/opt/sqc
Environment="SQC_RPC_AUTH_TOKEN=YOUR_SHARED_WRITE_PLANE_TOKEN"
ExecStart=/opt/sqc/sqc-node \
    --data-dir /var/sqc/data \
    --rpc-addr 127.0.0.1:8080 \
    --genesis /var/sqc/genesis.json \
    --validator-pubkey-hex GENESIS_VALIDATOR_PUBKEY_HEX \
    --peer http://VALIDATOR_HOST:8080 \
    --log-level info
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sqc-node
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/var/sqc/data

[Install]
WantedBy=multi-user.target
EOF
```

### For validator, use this instead:

```bash
sudo tee /etc/systemd/system/sqc-validator.service << 'EOF'
[Unit]
Description=Splendor Quantum Chain Validator Node
After=network-online.target

[Service]
Type=simple
User=sqc
Group=sqc
WorkingDirectory=/opt/sqc
Environment="SQC_WALLET_PASS=YOUR_PASSWORD_HERE"
# Must match the token on every full node this validator pushes blocks to.
Environment="SQC_RPC_AUTH_TOKEN=YOUR_SHARED_WRITE_PLANE_TOKEN"
ExecStart=/opt/sqc/sqc-node \
    --data-dir /var/sqc/data \
    --rpc-addr 127.0.0.1:8080 \
    --genesis /var/sqc/genesis.json \
    --validator \
    --wallet /var/sqc/validator.sqwallet \
    --block-interval 2 \
    --log-level info
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sqc-validator
LimitNOFILE=65535
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/var/sqc/data /var/sqc

[Install]
WantedBy=multi-user.target
EOF
```

A full node started with neither `--validator-pubkey-hex` nor `--genesis-hash`
cannot authenticate blocks from peers and **will not sync** — the node logs a
warning and then sits at height 0. Pin one of them.

### Create system user and install

```bash
# Create dedicated user
sudo useradd -r -m -s /bin/false sqc

# Install binaries
sudo mkdir -p /opt/sqc
sudo cp sqc-node sqc-wallet /opt/sqc/
sudo chmod +x /opt/sqc/sqc-node /opt/sqc/sqc-wallet

# Fix ownership
sudo chown -R sqc:sqc /opt/sqc
sudo mkdir -p /var/sqc/data
sudo chown -R sqc:sqc /var/sqc

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable sqc-node        # or sqc-validator
sudo systemctl start sqc-node

# Check status
sudo systemctl status sqc-node
sudo journalctl -u sqc-node -f        # follow logs
```

---

## 8. Monitoring & Health Checks

### Health Endpoints

```bash
# Liveness probe (always 200 if process is running)
curl http://localhost:8080/healthz

# Readiness probe (200 when chain has synced)
curl http://localhost:8080/readyz

# Full node status
curl http://localhost:8080/status | python3 -m json.tool

# Prometheus metrics
curl http://localhost:9100/metrics

# AI scheduler stats
curl http://localhost:8080/ai/stats

# Sync status
curl http://localhost:8080/sync
```

### Key Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `sqc_chain_height` | gauge | Latest committed block height |
| `sqc_mempool_size` | gauge | Pending transactions in mempool |
| `sqc_peer_count` | gauge | Connected peers |
| `sqc_blocks_produced_total` | counter | Blocks produced (validator only) |
| `sqc_txs_processed_total` | counter | Transactions executed |
| `sqc_txs_failed_total` | counter | Transactions that failed |
| `sqc_ai_batch_size` | gauge | Current AI batch size recommendation |
| `sqc_ai_anomaly_active` | gauge | 1 while the scheduler's mempool-spike flag is up. Nothing in the node acts on it — alert on it here or not at all |
| `sqc_ws_client_count` | gauge | Connected WebSocket clients |
| `sqc_rpc_requests_total` | counter | RPC requests served |
| `sqc_uptime_seconds` | gauge | Node uptime |

### Grafana Dashboard (quick setup)

```bash
# Install Prometheus + Grafana via Docker
cat > /tmp/monitor-compose.yml << 'EOF'
version: '3'
services:
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - /tmp/prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"
  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
EOF

cat > /tmp/prometheus.yml << 'EOF'
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: 'sqc-node'
    static_configs:
      - targets: ['host.docker.internal:9100']
EOF

docker compose -f /tmp/monitor-compose.yml up -d
echo "Grafana: http://localhost:3000  (admin/admin)"
echo "Prometheus: http://localhost:9090"
```

---

## 9. Firewall & Security

### UFW Firewall Rules

The node binds `127.0.0.1:8080` by default and serves its P2P routes on that
same port — there is no separate P2P listener, so there is no second port to
open. The default posture is therefore: expose the proxy, not the node.

```bash
# Enable UFW
sudo ufw enable

# Allow SSH (always do this first!)
sudo ufw allow 22/tcp comment 'SSH'

# Public entry point: the TLS terminator, not the node
sudo ufw allow 443/tcp comment 'HTTPS'
sudo ufw allow 80/tcp comment 'HTTP (ACME challenge only)'

# Metrics — restrict to the monitoring server only
sudo ufw allow from YOUR_MONITORING_SERVER_IP to any port 9100 comment 'Prometheus'

# Check rules
sudo ufw status verbose
```

Open 8080 only if peers must reach this node directly — a validator pushing
blocks to it, or a node it syncs from — and then scope the rule to those peers
rather than the internet:

```bash
sudo ufw allow from PEER_IP to any port 8080 proto tcp comment 'SQC peer'
```

A node reachable on 8080 must also be started with `--rpc-addr 0.0.0.0:8080` and
a `--rpc-auth-token`; without the token, `POST /block` and `POST /peers/announce`
return `503` and the node cannot take part in a cluster at all.

### Nginx Reverse Proxy (recommended for production)

```bash
sudo apt install -y nginx certbot python3-certbot-nginx

sudo tee /etc/nginx/sites-available/sqc-node << 'EOF'
server {
    listen 80;
    server_name YOUR_DOMAIN_HERE;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        # WebSocket support (for /ws endpoint)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;

        # Don't buffer the upgraded /ws connection, or events arrive in batches
        proxy_buffering off;
        proxy_cache off;
    }
}
EOF

sudo ln -s /etc/nginx/sites-available/sqc-node /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

# Add TLS with Let's Encrypt
sudo certbot --nginx -d YOUR_DOMAIN_HERE
```

**This TLS is not post-quantum, and saying otherwise would be false.** Once
nginx terminates the connection, the client negotiates with nginx, and a stock
nginx/OpenSSL build offers classical ECDHE (X25519 or P-256). The certificate
Let's Encrypt issues is ECDSA or RSA and is signed by a classical CA chain.
`--tls-cert` on the node changes nothing about that hop; it only applies where
the node itself answers the TLS handshake.

So pick one, deliberately:

- **Proxy in front (this recipe).** Operationally easy, and what most
  deployments run. The public hop is classically encrypted, which means it is
  recordable today and decryptable by a future CRQC — "harvest now, decrypt
  later" applies to pre-broadcast transactions, addresses, and any operator
  token that crosses it. Signatures and consensus are unaffected; the chain's
  integrity does not depend on this hop's confidentiality.
- **Node terminates TLS.** Point `--tls-cert`/`--tls-key` at the certificate and
  expose the node directly. Clients that support `X25519MLKEM768` get a hybrid
  key exchange all the way to the node. Clients that do not fall back to
  classical, so this raises the ceiling without guaranteeing every client.

A proxy can carry the hybrid group only if its own TLS stack supports the group
(OpenSSL 3.5+ and a build configured to offer it). That is not what
`apt install nginx` gives you today, so do not assume it — check with
`openssl s_client -groups X25519MLKEM768 -connect YOUR_DOMAIN:443` before
claiming post-quantum transport for a proxied endpoint.

### Wallet Security

In Phase 1 there is one validator, so this file is the chain's root of trust:
whoever holds it and its passphrase can sign an alternative history. Treat it
like a root CA key.

The node enforces two of these rules rather than trusting the operator:

- A wallet readable by group or other is **refused** at startup. Fix with `chmod 600`.
  **On Windows this check does not run.** `CheckKeyFilePermissions` returns
  success unconditionally there, because POSIX mode bits do not describe a
  Windows ACL. A Windows operator therefore gets none of this protection and
  must restrict the file themselves — the sentence above is true of Linux and
  macOS only.
- A wallet whose Argon2id parameters have been weakened below the floor
  (time ≥ 1, memory ≥ 32 MB, salt ≥ 16 bytes) is **refused**, so rewriting the
  file cannot make an offline guess at the passphrase cheap.

```bash
# Restrict wallet file permissions (validator only) — enforced at startup
chmod 600 /var/sqc/validator.sqwallet

# Never store the password in config files — use environment variable
# Add to /etc/environment or use a secrets manager
echo 'SQC_WALLET_PASS=your-password' | sudo tee -a /etc/environment

# Or read it from an owner-only file
chmod 600 /var/sqc/wallet.pass
./sqc-node --wallet /var/sqc/validator.sqwallet --wallet-pass-file /var/sqc/wallet.pass

# Or use systemd credentials (systemd 250+)
sudo systemd-creds encrypt --name=sqc-wallet-pass - /etc/systemd/system/sqc-validator.service.d/wallet-creds.conf
```

Avoid `--wallet-pass`: command-line arguments are readable by every local user
through `ps`, so it leaks the passphrase protecting the signing key. The node
logs a warning if you use it.

**Offline custody.** Nothing above prevents a compromise of the running host
from reaching the in-memory key. Removing that exposure needs either an HSM or
a remote signer holding the key off the node, and neither is implemented; until
BFT is wired there is also no second validator to bound the damage. Plan the
validator host accordingly: dedicated machine, no shared shells, no co-tenants.

---

## 10. Troubleshooting

### Common Issues

**Node won't start: "failed to open chain database"**
```bash
# Check disk space
df -h /var/sqc/data

# Check permissions
ls -la /var/sqc/data
sudo chown -R $USER:$USER /var/sqc/data
```

**Validator: "wallet password required"**
```bash
# Ensure environment variable is set
echo $SQC_WALLET_PASS

# Or pass directly (less secure)
./sqc-node --wallet /var/sqc/validator.sqwallet --wallet-pass "yourpass"
```

**"state root mismatch" on block execution**
```bash
# This means your local state diverged. Reset chain data:
sudo systemctl stop sqc-node        # or Ctrl-C / kill the process
rm -rf /var/sqc/data/chain /var/sqc/data/state /var/sqc/data/txindex
# Restart — node will resync from peers
```

> Wipe `txindex` too, not just `chain` and `state`. It is a separate store
> (`<data-dir>/txindex`) holding transaction receipts and logs. Leaving it while
> the chain is rebuilt keeps the OLD chain's receipts queryable at heights the
> NEW chain has reused, so `/logs` and transaction lookups answer confidently
> with results from a chain that no longer exists. Nothing detects this: the
> index has no notion of which chain it indexed.

**No peers connecting**
```bash
# P2P shares the RPC port — there is no separate P2P listener
nc -zv localhost 8080

# Which peers does this node know about?
curl http://localhost:8080/peers

# Add one manually. /peers/announce is write-plane: it needs the shared token
# in X-Node-Auth, and returns 503 if the node was started without one.
curl http://localhost:8080/peers/announce \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Node-Auth: $SQC_RPC_AUTH_TOKEN" \
  -d '{"addr":"http://PEER_IP:8080","height":0}'
```

If the peer is on a private IP or loopback (a local cluster), both nodes also
need `--allow-private-peers`; otherwise the SSRF guard drops the announcement.
That guard now runs twice — once when the address is accepted, and again when
the node actually dials it, so a hostname cannot resolve to a public address at
announce time and an internal one at connect time. Only hostnames are re-checked;
an IP literal has nothing to re-resolve.

Announcements nodes send each other are signed with the sender's ML-DSA-65 node
identity — the same key the sealed transport pins — and carry a timestamp and
nonce, so an announcement names one node and cannot be replayed. The hand-rolled
form above is unsigned; it is still accepted, because announce has to keep
working across a version upgrade, and the shared token still gates the route
either way. Once every node in a deployment is on a build that signs, that
fallback can be dropped.

**RPC returns "no blocks yet"**
```bash
# Validator hasn't produced a genesis block yet
# Make sure you're running with --validator flag and correct wallet
./sqc-node --validator --wallet /var/sqc/validator.sqwallet ...

# Check if genesis block was committed
curl http://localhost:8080/block/0
```

**High memory usage**
```bash
# Check Go runtime stats
curl http://localhost:9100/metrics | grep go_memstats

# LevelDB state cache is typically the largest consumer.
# Per-block transaction count is a compile-time constant (block.MaxTxPerBlock),
# not a setting — lowering it means editing and rebuilding.
```

### Log Levels for Debugging

```bash
# Enable debug logging
./sqc-node --log-level debug

# Watch for specific events
journalctl -u sqc-node -f | grep -E "(block produced|tx failed|peer added|error)"

# Check block production rate
journalctl -u sqc-node | grep "block produced" | tail -20
```

### Quick Diagnostic Script

```bash
#!/bin/bash
echo "=== SQC Node Diagnostics ==="
echo ""
echo "--- Health ---"
curl -s http://localhost:8080/healthz | python3 -m json.tool 2>/dev/null || echo "Node not responding"
echo ""
echo "--- Status ---"
curl -s http://localhost:8080/status | python3 -m json.tool 2>/dev/null
echo ""
echo "--- Latest Block ---"
curl -s http://localhost:8080/block/latest | python3 -c "
import json, sys
b = json.load(sys.stdin)
h = b.get('header', {})
print(f'Height: {h.get(\"height\")}  TxCount: {h.get(\"tx_count\")}  Hash: {b.get(\"hash\", \"\")[:16]}...')
" 2>/dev/null || echo "No blocks yet"
echo ""
echo "--- Peers ---"
curl -s http://localhost:8080/peers | python3 -c "
import json, sys
peers = json.load(sys.stdin)
print(f'Connected peers: {len(peers)}')
for p in peers[:5]:
    print(f'  {p.get(\"addr\")}  height={p.get(\"height\")}')
" 2>/dev/null
echo ""
echo "--- Sync ---"
curl -s http://localhost:8080/sync | python3 -m json.tool 2>/dev/null
echo ""
echo "--- Mempool ---"
curl -s http://localhost:8080/status | python3 -c "
import json, sys
s = json.load(sys.stdin)
print(f'Mempool size: {s.get(\"mempool_size\")}')
" 2>/dev/null
```

Save as `diagnostics.sh`, `chmod +x diagnostics.sh`, run with `./diagnostics.sh`.

---

## Quick Reference — API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe |
| `/status` | GET | Node status (height, mempool, peers) |
| `/sync` | GET | Sync status vs peers |
| `/block/latest` | GET | Latest block |
| `/block/{height}` | GET | Block by height |
| `/balance/{SQ:addr}` | GET | Account balance |
| `/nonce/{SQ:addr}` | GET | Account nonce |
| `/tx/submit` | POST | Submit signed transaction |
| `/tx/{hash}/receipt` | GET | Transaction receipt |
| `/contract/{SQ:addr}` | GET | Contract info |
| `/peers` | GET | Known peers |
| `/peers/announce` | POST | Announce a peer |
| `/vm/disasm` | POST | Disassemble bytecode |
| `/vm/validate` | POST | Validate bytecode |
| `/vm/estimate-gas` | POST | Estimate gas |
| `/vm/opcodes` | GET | All SQC-VM opcodes |
| `/gov/proposals` | GET | List proposals |
| `/staking/validators/{addr}` | GET | Validator info |
| `/staking/tiers` | GET | Staking tier info |
| `/dex/pool/{t0}/{t1}` | GET | Pool info |
| `/dex/pool/{t0}/{t1}/quote` | GET | Swap quote |
| `/call` | POST | Module read (`{"module","action","params"}`) or bytecode view call, at the head block |
| `/exchange/{SQ:addr}` | GET | Exchange instance info (`exchange_info`) |
| `/exchange/{SQ:addr}/condition/{id}` | GET | Condition state (`condition_info`) |
| `/exchange/{SQ:addr}/order/{hash}` | GET | Order fill/cancel status (`order_status`) |
| `/exchange/{SQ:addr}/nonce/{SQ:maker}` | GET | Maker's order nonce (`nonce`) |
| `/exchange/{SQ:addr}/position/{condition}/{SQ:holder}` | GET | Holder's YES/NO balances (`position`) |
| `/ws?subscribe=new_block,new_tx` | GET | WebSocket stream (`rpc/websocket.go`) |
| `/metrics` | GET | Prometheus metrics (:9100) |
| `/ai/stats` | GET | AI scheduler stats |

---

*Splendor Quantum Chain — Built for the world that's coming.*  
*Every signature. Every hash. Every block. Quantum-safe from day one.*
