#!/bin/bash
# Splendor Quantum Chain — Server Setup & Build Script
# Run this once on a fresh Ubuntu 20.04/22.04 server as root or with sudo.
# Usage: curl -fsSL https://raw.githubusercontent.com/... | bash
#    OR: bash install.sh [--validator] [--rpc]

set -e

GREEN='\033[0;32m'
ORANGE='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

GO_VERSION="1.23.0"
GO_ARCH="linux-amd64"
SQC_DIR="$HOME/splendor-quantum-blockchain"

log() { echo -e "${GREEN}[SQC]${NC} $1"; }
warn() { echo -e "${ORANGE}[WARN]${NC} $1"; }
err() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# ── Parse flags ────────────────────────────────────────────────────────────────
IS_VALIDATOR=false
IS_RPC=false
for arg in "$@"; do
  case $arg in
    --validator) IS_VALIDATOR=true ;;
    --rpc)       IS_RPC=true ;;
    --help|-h)
      echo "Usage: $0 --validator | --rpc"
      echo "  --validator   Set up as a block-producing validator node"
      echo "  --rpc         Set up as an RPC (sync) node"
      exit 0 ;;
  esac
done

# ── Validate arguments EARLY — before doing any work ─────────────────────────
if [ "$IS_VALIDATOR" = false ] && [ "$IS_RPC" = false ]; then
  echo ""
  echo -e "${RED}╔══════════════════════════════════════════════════════╗${NC}"
  echo -e "${RED}║  ERROR: You must specify --validator or --rpc         ║${NC}"
  echo -e "${RED}╚══════════════════════════════════════════════════════╝${NC}"
  echo ""
  echo "Usage:"
  echo "  bash install.sh --validator    (first-time validator setup)"
  echo "  bash install.sh --rpc          (first-time RPC node setup)"
  echo ""
  echo "After install, use the 'sqc' tool:"
  echo "  sqc status                     (check node status)"
  echo "  sqc logs                       (live coloured logs)"
  echo "  sqc console                    (interactive console)"
  echo "  sqc update                     (dirty update, keeps data)"
  echo "  sqc update --clean             (clean update, wipes data)"
  echo ""
  exit 1
fi

# ── Step 1: Install system dependencies ────────────────────────────────────────
log "Installing system dependencies..."
apt-get update -qq
apt-get install -y -qq \
  git curl wget gcc musl-dev \
  tmux jq ufw ca-certificates \
  build-essential 2>/dev/null | tail -3

# ── Step 2: Install Go ─────────────────────────────────────────────────────────
install_go() {
  if command -v go &>/dev/null && [[ "$(go version)" == *"go1.23"* ]]; then
    log "Go 1.23 already installed: $(go version)"
    return
  fi

  log "Installing Go ${GO_VERSION}..."
  wget -q "https://go.dev/dl/go${GO_VERSION}.${GO_ARCH}.tar.gz" -O /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz

  # Add to current session AND permanent
  export PATH=$PATH:/usr/local/go/bin
  grep -q '/usr/local/go/bin' /root/.bashrc 2>/dev/null || \
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /root/.bashrc
  grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null || \
    echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh

  log "Go installed: $(go version)"
}

install_go

# Ensure go is in PATH for this script
export PATH=$PATH:/usr/local/go/bin
which go || err "Go installation failed — 'go' still not found in PATH"

# ── Step 3: Clone / update repo ────────────────────────────────────────────────
if [ -d "$SQC_DIR/.git" ]; then
  log "Repo already exists — pulling latest..."
  cd "$SQC_DIR"
  git pull --rebase origin main 2>/dev/null || true
else
  log "Cloning Splendor Quantum Blockchain..."
  git clone https://github.com/Splendor-Protocol/splendor-quantum-blockchain.git "$SQC_DIR"
  cd "$SQC_DIR"
fi

cd "$SQC_DIR"

# ── Step 4: Build binaries ─────────────────────────────────────────────────────
log "Setting up Go module environment..."

# Force Go modules mode (never use GOPATH mode)
export GO111MODULE=on
export GOPATH=/root/go
export GOMODCACHE=/root/go/pkg/mod
mkdir -p "$GOPATH/pkg/mod"

# Verify go.mod exists (required for module mode)
[ -f go.mod ] || err "go.mod not found — is the repo cloned correctly?"
log "go.mod found: $(head -1 go.mod)"

# Download dependencies into module cache
log "Downloading Go module dependencies..."
go mod download
log "Dependencies downloaded ✓"

log "Building sqc-node..."
CGO_ENABLED=1 GO111MODULE=on go build \
  -ldflags="-s -w -X main.version=1.0.0" \
  -o sqc-node ./cmd/node
log "sqc-node built ✓"

log "Building sqc-wallet..."
CGO_ENABLED=1 GO111MODULE=on go build \
  -ldflags="-s -w" \
  -o sqc-wallet ./cmd/wallet
log "sqc-wallet built ✓"

chmod +x sqc-node sqc-wallet
ls -lh sqc-node sqc-wallet

# ── Step 4b: Install sqc management tool globally ─────────────────────────────
if [ -f "$SQC_DIR/sqc" ]; then
  chmod +x "$SQC_DIR/sqc"
  cp "$SQC_DIR/sqc" /usr/local/bin/sqc
  log "sqc management tool installed → /usr/local/bin/sqc"
fi

# ── Step 5: Create data directory ──────────────────────────────────────────────
mkdir -p /var/sqc/data
log "Data directory: /var/sqc/data"

# ── Step 6: Validator setup ────────────────────────────────────────────────────
setup_validator() {
  log "=== VALIDATOR SETUP ==="

  # Generate wallet if not exists
  if [ ! -f /var/sqc/validator.sqwallet ]; then
    log "Generating new ML-DSA-65 validator keypair..."
    echo ""
    echo -e "${ORANGE}Enter a strong password for your validator wallet.${NC}"
    echo -e "${ORANGE}Save it securely — it is never stored on disk.${NC}"
    echo ""
    ./sqc-wallet generate --out /var/sqc/validator.sqwallet
    chmod 600 /var/sqc/validator.sqwallet
  else
    warn "Wallet already exists at /var/sqc/validator.sqwallet — skipping generation"
  fi

  # Get address and pubkey
  echo ""
  log "Your validator address:"
  ./sqc-wallet address --wallet /var/sqc/validator.sqwallet
  echo ""

  # Create genesis.json if not exists
  if [ ! -f /var/sqc/genesis.json ]; then
    log "Creating genesis.json..."
    VALIDATOR_ADDR=$(./sqc-wallet address --wallet /var/sqc/validator.sqwallet)
    VALIDATOR_PUBKEY=$(./sqc-wallet pubkey --wallet /var/sqc/validator.sqwallet)
    TIMESTAMP=$(date +%s)

    cat > /var/sqc/genesis.json << GENESIS
{
  "chain_id": "splendor-quantum-1",
  "network_name": "Splendor Quantum Chain Mainnet",
  "timestamp": $TIMESTAMP,
  "validator_pubkey_hex": "$VALIDATOR_PUBKEY",
  "allocations": [
    {
      "address": "$VALIDATOR_ADDR",
      "balance": "26000000000000000000000000000",
      "comment": "Genesis validator — 26 billion SPLD"
    }
  ],
  "total_supply": "26000000000000000000000000000"
}
GENESIS
    log "genesis.json written to /var/sqc/genesis.json"
    echo ""
    echo -e "${ORANGE}IMPORTANT: Share your validator_pubkey_hex with other nodes${NC}"
    echo -e "${ORANGE}so they can verify your blocks. Your genesis.json:${NC}"
    cat /var/sqc/genesis.json | python3 -c "
import json,sys
g=json.load(sys.stdin)
pk=g['validator_pubkey_hex']
print(f'  validator_pubkey_hex: {pk[:32]}...(truncated)')
print(f'  validator_address: {g[\"allocations\"][0][\"address\"]}')
"
  else
    warn "genesis.json already exists at /var/sqc/genesis.json"
  fi

  # Create systemd service
  cat > /etc/systemd/system/sqc-validator.service << EOF
[Unit]
Description=Splendor Quantum Chain Validator Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=$SQC_DIR
Environment="SQC_WALLET_PASS=REPLACE_WITH_YOUR_PASSWORD"
ExecStart=$SQC_DIR/sqc-node \\
    --data-dir /var/sqc/data \\
    --rpc-addr :8080 \\
    --validator \\
    --wallet /var/sqc/validator.sqwallet \\
    --genesis /var/sqc/genesis.json \\
    --block-interval 2 \\
    --log-level info
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

  log "Systemd service created: /etc/systemd/system/sqc-validator.service"
  echo ""
  echo -e "${RED}NEXT STEP: Edit the service file to set your wallet password:${NC}"
  echo "  nano /etc/systemd/system/sqc-validator.service"
  echo "  (change REPLACE_WITH_YOUR_PASSWORD to your actual password)"
  echo ""
  echo "Then start the validator:"
  echo "  systemctl daemon-reload"
  echo "  systemctl enable sqc-validator"
  echo "  systemctl start sqc-validator"
  echo "  journalctl -u sqc-validator -f"
}

# ── Step 7: RPC node setup ─────────────────────────────────────────────────────
setup_rpc() {
  log "=== RPC NODE SETUP ==="
  echo ""
  echo -e "${ORANGE}The RPC node does NOT generate its own genesis.json.${NC}"
  echo -e "${ORANGE}It syncs the chain (including the genesis block) from the validator peer.${NC}"
  echo -e "${ORANGE}You need: the validator's public IP and its RPC port (default: 8080).${NC}"
  echo ""

  # Prompt for validator URL
  echo -e "${ORANGE}Enter the validator node's RPC URL (e.g. http://1.2.3.4:8080):${NC}"
  read -r VALIDATOR_URL
  VALIDATOR_URL=${VALIDATOR_URL:-"http://VALIDATOR_IP:8080"}

  # Optionally copy genesis.json from validator (for config reference, not required)
  echo ""
  echo -e "${ORANGE}[OPTIONAL] Paste the genesis.json content from your validator server.${NC}"
  echo -e "${ORANGE}Press ENTER to skip (the RPC node will sync it automatically):${NC}"
  read -r SKIP_GENESIS
  if [ -z "$SKIP_GENESIS" ]; then
    log "Skipping genesis.json — will sync from validator peer automatically."
  else
    echo "$SKIP_GENESIS" > /var/sqc/genesis.json
    log "genesis.json saved to /var/sqc/genesis.json"
  fi

  cat > /etc/systemd/system/sqc-rpc.service << EOF
[Unit]
Description=Splendor Quantum Chain RPC Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=$SQC_DIR
ExecStart=$SQC_DIR/sqc-node \\
    --data-dir /var/sqc/data \\
    --rpc-addr :8080 \\
    --peer $VALIDATOR_URL \\
    --log-level info
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

  log "Systemd service created: /etc/systemd/system/sqc-rpc.service"
  echo ""
  echo -e "${GREEN}Summary for RPC node:${NC}"
  echo "  • No .sqwallet needed (RPC nodes don't sign blocks)"
  echo "  • No genesis.json needed (syncs from validator automatically)"
  echo "  • Validator peer: $VALIDATOR_URL"
  echo ""
  echo "Start the RPC node:"
  echo "  systemctl daemon-reload"
  echo "  systemctl enable sqc-rpc"
  echo "  systemctl start sqc-rpc"
  echo "  journalctl -u sqc-rpc -f"
  echo ""
  echo "Verify sync after starting:"
  echo "  curl http://localhost:8080/sync"
  echo "  # {\"is_syncing\":false,\"local_height\":N,\"best_height\":N,\"peer_count\":1}"
}

# ── Step 8: Firewall ───────────────────────────────────────────────────────────
setup_firewall() {
  log "Configuring UFW firewall..."
  ufw --force enable 2>/dev/null || true
  ufw allow 22/tcp   comment 'SSH' 2>/dev/null || true
  ufw allow 8080/tcp comment 'SQC RPC' 2>/dev/null || true
  ufw allow 9090/tcp comment 'SQC P2P' 2>/dev/null || true
  # Restrict metrics to localhost only
  ufw allow from 127.0.0.1 to any port 9100 comment 'SQC Metrics' 2>/dev/null || true
  log "Firewall configured"
}

# ── Validate arguments — MUST have --validator or --rpc ───────────────────────
if [ "$IS_VALIDATOR" = false ] && [ "$IS_RPC" = false ]; then
  echo ""
  echo -e "${RED}╔══════════════════════════════════════════════════════╗${NC}"
  echo -e "${RED}║  ERROR: You must specify --validator or --rpc         ║${NC}"
  echo -e "${RED}╚══════════════════════════════════════════════════════╝${NC}"
  echo ""
  echo -e "${ORANGE}Usage:${NC}"
  echo "  bash install.sh --validator    Set up a block-producing validator"
  echo "  bash install.sh --rpc          Set up an RPC (sync) node"
  echo ""
  echo -e "${ORANGE}Or use the sqc management tool:${NC}"
  echo "  sqc install --validator"
  echo "  sqc install --rpc"
  echo "  sqc update             (dirty update, preserves chain data)"
  echo "  sqc update --clean     (clean update, wipes data)"
  echo ""
  exit 1
fi

# ── Run selected setup ──────────────────────────────────────────────────────────
setup_firewall

if [ "$IS_VALIDATOR" = true ]; then
  setup_validator
elif [ "$IS_RPC" = true ]; then
  setup_rpc
fi

echo ""
log "=== DONE ==="
echo ""
echo -e "${GREEN}Quick verification:${NC}"
echo "  ./sqc-wallet --help"
echo "  ./sqc-node --help"
echo ""
echo -e "${GREEN}After starting, test:${NC}"
echo "  curl http://localhost:8080/healthz"
echo "  curl http://localhost:8080/status"
