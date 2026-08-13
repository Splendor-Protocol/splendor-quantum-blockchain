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
      echo "Usage: $0 [--validator] [--rpc]"
      echo "  --validator   Set up as a block-producing validator node"
      echo "  --rpc         Set up as an RPC node (syncs from validator)"
      exit 0 ;;
  esac
done

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
log "Building sqc-node and sqc-wallet..."
CGO_ENABLED=1 go build -mod=mod -ldflags="-s -w -X main.version=1.0.0" \
  -o sqc-node ./cmd/node
CGO_ENABLED=1 go build -mod=mod -ldflags="-s -w" \
  -o sqc-wallet ./cmd/wallet

chmod +x sqc-node sqc-wallet
log "Build complete: $(./sqc-node --help 2>&1 | head -1)"

# ── Step 5: Create data directory ──────────────────────────────────────────────
mkdir -p /var/sqc/data
log "Data directory: /var/sqc/data"

# ── Step 6: Validator setup ────────────────────────────────────────────────────
setup_validator() {
  log "=== VALIDATOR SETUP ==="

  # Generate wallet if not exists
  if [ ! -f /var/sqc/validator.sqwallet ]; then
    log "Generating new Dilithium validator keypair..."
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

  # Prompt for validator IP
  echo ""
  echo -e "${ORANGE}Enter the validator node's IP address or URL (e.g. http://1.2.3.4:8080):${NC}"
  read -r VALIDATOR_URL
  VALIDATOR_URL=${VALIDATOR_URL:-"http://VALIDATOR_IP:8080"}

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
  echo "Start the RPC node:"
  echo "  systemctl daemon-reload"
  echo "  systemctl enable sqc-rpc"
  echo "  systemctl start sqc-rpc"
  echo "  journalctl -u sqc-rpc -f"
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

# ── Run selected setup ──────────────────────────────────────────────────────────
setup_firewall

if [ "$IS_VALIDATOR" = true ]; then
  setup_validator
elif [ "$IS_RPC" = true ]; then
  setup_rpc
else
  log "No --validator or --rpc flag provided."
  log "Build complete. To continue:"
  echo ""
  echo "  For a VALIDATOR node:"
  echo "    bash $0 --validator"
  echo ""
  echo "  For an RPC node:"
  echo "    bash $0 --rpc"
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
