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
                                       │ HTTPS / REST / SSE
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
   │    - Signs block with Dilithium Mode 3 key                        │
   │    - Broadcasts block to all peers via P2P                        │
   │                                                                    │
   │  Phase 2:  BFT Multi-Validator (future)                           │
   │    - All validators sign votes with Dilithium                     │
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
   │                    P2P NETWORK  (:9090)                          │
   │  - Broadcasts new blocks to all known peers                      │
   │  - Peer Exchange (PEX): discovers new peers every 60s           │
   │  - Block Sync: downloads missing blocks on startup              │
   │  - All blocks Dilithium-verified before applying                │
   └──────────────────────────────────────────────────────────────────┘
```

### Transaction Data Flow (Step by Step)

```
Step 1: User creates transaction
  wallet.buildTransfer(from, to, amount, fee, nonce)
  → unsigned transaction object

Step 2: User signs with Dilithium key
  dilithium_sig = Dilithium.Sign(private_key, SHA3-256(tx_body))
  tx.signature = dilithium_sig  (3,293 bytes)
  tx.public_key = dilithium_pub (1,952 bytes, first tx only)

Step 3: Submit to node
  POST /tx/submit  { signed transaction JSON }
  
Step 4: Mempool validation
  ✓ Chain ID check
  ✓ Dilithium signature verification
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
  Sign block header with validator Dilithium key
  Persist block to LevelDB

Step 7: Broadcast
  POST /block → all known peers
  Peers verify Dilithium block signature
  Peers apply block to their state
  Peers index txs and store receipts

Step 8: Confirmation
  GET /tx/{hash}/receipt → returns:
    { status: 1, block_height: N, gas_used: ..., logs: [...] }
```

### Cryptographic Primitives

| Operation | Algorithm | Standard | Key Size |
|-----------|-----------|----------|----------|
| Transaction signing | Dilithium Mode 3 | NIST FIPS 204 | 4,032 byte private key |
| Signature verification | Dilithium Mode 3 | NIST FIPS 204 | 1,952 byte public key |
| Block signing (validator) | Dilithium Mode 3 | NIST FIPS 204 | same |
| Address derivation | SHA3-256(pubkey) | NIST FIPS 202 | 32 byte output |
| Block hash | SHA3-256(header) | NIST FIPS 202 | 32 byte output |
| State root | SHA3-256(accounts) | NIST FIPS 202 | 32 byte output |
| Wallet encryption | AES-256-GCM | NIST FIPS 197 | 256 bit key |
| Key derivation | Argon2id | RFC 9106 | — |

**Zero ECDSA. Zero SHA-256. Zero keccak256. Zero secp256k1.**

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

### Install Go 1.23

```bash
# Download Go
wget -q https://go.dev/dl/go1.23.0.linux-amd64.tar.gz -O /tmp/go.tar.gz

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
# Expected: go version go1.23.0 linux/amd64
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

### Create config file

```bash
# Generate default config
cat > /var/sqc/config.json << 'EOF'
{
  "node": {
    "mode": "full",
    "name": "sqc-node-1",
    "log_level": "info",
    "log_format": "console"
  },
  "chain": {
    "chain_id": "splendor-quantum-1",
    "network_name": "Splendor Quantum Chain Mainnet",
    "max_tx_per_block": 10000,
    "min_fee": "1000"
  },
  "rpc": {
    "listen_addr": ":8080",
    "enable_cors": true,
    "enable_websocket": true
  },
  "p2p": {
    "listen_addr": ":9090",
    "bootstrap_peers": [],
    "max_peers": 50,
    "peer_exchange_interval": "60s",
    "validator_pubkey_hex": ""
  },
  "storage": {
    "data_dir": "/var/sqc/data"
  },
  "ai": {
    "enabled": true,
    "min_batch_size": 100,
    "max_batch_size": 10000,
    "observation_interval_ms": 500
  },
  "metrics": {
    "enabled": true,
    "listen_addr": ":9100"
  }
}
EOF
```

### Run Full Node

```bash
./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr :8080 \
  --log-level info
```

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
# Generate a new Dilithium keypair for the validator
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

# Run validator
./sqc-node \
  --data-dir /var/sqc/data \
  --rpc-addr :8080 \
  --validator \
  --wallet /var/sqc/validator.sqwallet \
  --block-interval 2 \
  --log-level info
```

### Verify Validator is Producing Blocks

```bash
# Watch logs for block production
./sqc-node --validator --wallet ... 2>&1 | grep "block produced"

# Check latest block height (should increment every 2 seconds)
watch -n 2 "curl -s http://localhost:8080/block/latest | python3 -m json.tool | grep height"
```

---

## 5. Configuration Reference

### All Configuration Options

```json
{
  "node": {
    "mode": "full",          // "full" (sync only) or "validator" (produce blocks)
    "name": "my-node",       // node identifier shown in peer lists
    "log_level": "info",     // debug | info | warn | error
    "log_format": "console"  // console (human) | json (machine)
  },
  "chain": {
    "chain_id": "splendor-quantum-1",
    "network_name": "Splendor Quantum Chain Mainnet",
    "max_tx_per_block": 10000,
    "min_fee": "1000"         // minimum tx fee in smallest SPLD units
  },
  "validator": {
    "wallet_path": "/var/sqc/validator.sqwallet",
    "wallet_pass_env": "SQC_WALLET_PASS",  // env var holding password
    "block_interval": "2s",                // block production frequency
    "max_block_gas": 500000000000          // 500B gas limit
  },
  "rpc": {
    "listen_addr": ":8080",
    "enable_cors": true,
    "max_request_body_bytes": 67108864,    // 64MB
    "read_timeout": "10s",
    "write_timeout": "30s",
    "enable_websocket": true
  },
  "p2p": {
    "listen_addr": ":9090",
    "bootstrap_peers": [
      "http://seed1.splendor.example:8080",
      "http://seed2.splendor.example:8080"
    ],
    "max_peers": 50,
    "peer_exchange_interval": "60s",
    "validator_pubkey_hex": "abc123..."    // genesis validator's Dilithium pubkey
  },
  "storage": {
    "data_dir": "/var/sqc/data"
  },
  "ai": {
    "enabled": true,
    "min_batch_size": 100,
    "max_batch_size": 10000,
    "observation_interval_ms": 500
  },
  "metrics": {
    "enabled": true,
    "listen_addr": ":9100"              // Prometheus scrape endpoint
  }
}
```

---

## 6. Genesis Setup

### Understanding the Genesis Block

The genesis block (block 0) is the first block on the chain. It:
- Records the initial SPLD balances for all pre-funded accounts
- Registers the genesis validator's Dilithium public key
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

# Step 5: Start validator — genesis block auto-created on first run
export SQC_WALLET_PASS="your-password"
./sqc-node \
  --data-dir /var/sqc/data \
  --validator \
  --wallet /var/sqc/validator.sqwallet

# Step 6: Verify genesis block
sleep 3
curl http://localhost:8080/block/0
```

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
ExecStart=/opt/sqc/sqc-node \
    --data-dir /var/sqc/data \
    --rpc-addr :8080 \
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
ExecStart=/opt/sqc/sqc-node \
    --data-dir /var/sqc/data \
    --rpc-addr :8080 \
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

```bash
# Enable UFW
sudo ufw enable

# Allow SSH (always do this first!)
sudo ufw allow 22/tcp comment 'SSH'

# Allow SQC RPC (public-facing)
sudo ufw allow 8080/tcp comment 'SQC RPC'

# Allow SQC P2P
sudo ufw allow 9090/tcp comment 'SQC P2P'

# Metrics — restrict to monitoring server only
sudo ufw allow from YOUR_MONITORING_SERVER_IP to any port 9100 comment 'Prometheus'

# Check rules
sudo ufw status verbose
```

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

        # SSE support (for /ws?subscribe=...)
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

### Wallet Security

```bash
# Restrict wallet file permissions (validator only)
chmod 600 /var/sqc/validator.sqwallet

# Never store the password in config files — use environment variable
# Add to /etc/environment or use a secrets manager
echo 'SQC_WALLET_PASS=your-password' | sudo tee -a /etc/environment

# Or use systemd credentials (systemd 250+)
sudo systemd-creds encrypt --name=sqc-wallet-pass - /etc/systemd/system/sqc-validator.service.d/wallet-creds.conf
```

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
./sqc-node --stop   # or systemctl stop sqc-node
rm -rf /var/sqc/data/chain /var/sqc/data/state
# Restart — node will resync from peers
```

**No peers connecting**
```bash
# Check if P2P port is open
nc -zv localhost 9090

# Verify bootstrap peers in config
curl http://localhost:8080/peers

# Add bootstrap peers manually
curl http://localhost:8080/peers/announce \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"addr":"http://PEER_IP:8080","height":0}'
```

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
# Reduce max_tx_per_block to lower per-block state footprint.
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
| `/ws?subscribe=new_block,new_tx` | GET | SSE stream |
| `/metrics` | GET | Prometheus metrics (:9100) |
| `/ai/stats` | GET | AI scheduler stats |

---

*Splendor Quantum Chain — Built for the world that's coming.*  
*Every signature. Every hash. Every block. Quantum-safe from day one.*
