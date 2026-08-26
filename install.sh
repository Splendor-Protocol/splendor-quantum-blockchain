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

# Must satisfy the `go` directive in go.mod. circl v1.6.5 (FIPS-204 ML-DSA-65)
# declares go 1.25.0, so anything older cannot build the node at all — this
# script used to install 1.23.0 and every fresh server failed at `make build`.
GO_MIN_VERSION="1.25.0"
GO_VERSION="1.25.0"
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

# generate_token emits a 256-bit hex secret for the write plane. openssl is the
# common case; /dev/urandom is the fallback so this never silently produces a
# weak or empty token on a minimal image.
generate_token() {
  if command -v openssl &>/dev/null; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# peer_node_pubkey_hex fetches a peer's ML-DSA-65 node identity, which is the
# value --peer-pin takes. Pinning is what turns trust-on-first-use into trust in
# a key you actually checked: without it the first host to answer at that URL
# becomes the peer this node believes for the life of the chain.
peer_node_pubkey_hex() {
  curl -fsS --max-time 15 "$1/p2p/identity" 2>/dev/null | python3 -c "
import base64, json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
pk = d.get('node_pubkey')
if not pk:
    sys.exit(1)
sys.stdout.write(base64.b64decode(pk).hex())
" 2>/dev/null
}

# ── Step 2: Install Go ─────────────────────────────────────────────────────────
install_go() {
  # Accept any toolchain at or above the minimum. Matching an exact version
  # string would downgrade a server that already has something newer.
  if command -v go &>/dev/null; then
    local have
    have="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
    if [ -n "$have" ] && \
       [ "$(printf '%s\n%s\n' "$GO_MIN_VERSION" "$have" | sort -V | head -1)" = "$GO_MIN_VERSION" ]; then
      log "Go ${have} already installed (>= ${GO_MIN_VERSION} required)"
      return
    fi
    warn "Go ${have:-unknown} is older than the required ${GO_MIN_VERSION}; upgrading"
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

  # A node's RPC surface carries a write plane (POST /block, /peers/announce) and
  # reveals everything the node knows. Binding it to every interface with no token
  # is how a lab node becomes a public one by accident, so the default is loopback
  # and a generated token. Put a TLS reverse proxy in front to expose it, or set
  # SQC_BIND_ADDR deliberately.
  SQC_BIND_ADDR="${SQC_BIND_ADDR:-127.0.0.1:8080}"
  SQC_RPC_TOKEN="$(generate_token)"
  printf '%s\n' "$SQC_RPC_TOKEN" > /var/sqc/rpc-auth-token
  chmod 600 /var/sqc/rpc-auth-token
  GENESIS_DOC_HASH="$($SQC_DIR/sqc-node genesis-hash /var/sqc/genesis.json 2>/dev/null || true)"
  if [ -z "$GENESIS_DOC_HASH" ]; then
    err "could not compute the genesis document hash from /var/sqc/genesis.json"
    exit 1
  fi
  log "genesis document pinned: $GENESIS_DOC_HASH"

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
Environment="SQC_RPC_AUTH_TOKEN=$SQC_RPC_TOKEN"
ExecStart=$SQC_DIR/sqc-node \\
    --data-dir /var/sqc/data \\
    --rpc-addr $SQC_BIND_ADDR \\
    --validator \\
    --wallet /var/sqc/validator.sqwallet \\
    --genesis /var/sqc/genesis.json \\
    --genesis-doc-hash $GENESIS_DOC_HASH \\
    --block-interval 2 \\
    --log-level info
Restart=always
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/sqc
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
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

  # An RPC node has to authenticate two things it cannot see: which chain this is,
  # and which peer it is talking to. Fetch the peer's identity now and pin it --
  # trust-on-first-use means whoever answers first at that URL owns this node's
  # view of the chain for good.
  SQC_BIND_ADDR="${SQC_BIND_ADDR:-127.0.0.1:8080}"
  PEER_PIN="$(peer_node_pubkey_hex "$VALIDATOR_URL" || true)"
  if [ -z "$PEER_PIN" ]; then
    err "could not read the node identity of $VALIDATOR_URL"
    echo "  Tried: GET $VALIDATOR_URL/p2p/identity" >&2
    echo "  An RPC node installed without a peer pin trusts whatever answers at that" >&2
    echo "  URL, so this install stops rather than writing an unpinned unit. Check the" >&2
    echo "  validator is reachable, then re-run." >&2
    exit 1
  fi
  log "peer identity pinned: ${PEER_PIN:0:16}..."

  GENESIS_DOC_HASH="$($SQC_DIR/sqc-node genesis-hash /var/sqc/genesis.json 2>/dev/null || true)"
  if [ -z "$GENESIS_DOC_HASH" ]; then
    err "could not compute the genesis document hash from /var/sqc/genesis.json"
    exit 1
  fi
  log "genesis document pinned: $GENESIS_DOC_HASH"

  # A full node authenticates every block against the validator's ML-DSA-65 key.
  # It does NOT learn that key from --genesis: the flag credits allocations only.
  # Without --validator-pubkey-hex (or --genesis-hash to bootstrap it from block 0)
  # the node starts, stays up, and never syncs -- "this node cannot authenticate
  # any block". The key is already in the genesis document, so read it from there.
  VALIDATOR_PK="$(python3 -c "
import json, sys
try:
    print(json.load(open('/var/sqc/genesis.json')).get('validator_pubkey_hex', ''))
except Exception:
    pass
" 2>/dev/null)"
  if [ -z "$VALIDATOR_PK" ]; then
    err "no validator_pubkey_hex in /var/sqc/genesis.json"
    echo "  Without it this node cannot authenticate blocks and would never sync." >&2
    exit 1
  fi
  log "validator key pinned: ${VALIDATOR_PK:0:16}..."

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
    --rpc-addr $SQC_BIND_ADDR \\
    --peer $VALIDATOR_URL \\
    --peer-pin $VALIDATOR_URL=$PEER_PIN \\
    --require-peer-pins \\
    --validator-pubkey-hex $VALIDATOR_PK \\
    --genesis /var/sqc/genesis.json \\
    --genesis-doc-hash $GENESIS_DOC_HASH \\
    --log-level info
Restart=always
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/sqc
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
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
  # 8080 is NOT opened. The node binds loopback by default and P2P shares that
  # same port -- there is no separate P2P listener -- so opening either published
  # the write plane to the internet while looking like ordinary firewall setup.
  # To serve RPC publicly, terminate TLS in a reverse proxy on 443 and forward to
  # 127.0.0.1:8080; the node's own token still gates the write plane.
  ufw allow 443/tcp  comment 'HTTPS reverse proxy to SQC RPC' 2>/dev/null || true
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
