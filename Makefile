# Splendor Quantum Chain — Build System
# Requires: Go 1.23+ or Docker
# All crypto: CRYSTALS-Dilithium Mode 3 (NIST FIPS 204) + SHA-3-256 (NIST FIPS 202)

BINARY_NODE   = sqc-node
BINARY_WALLET = sqc-wallet
DOCKER_IMAGE  = splendor-quantum-chain
VERSION       = 1.0.0

# Auto-detect Go binary: prefer /usr/local/go/bin/go (installed by install.sh)
# then fall back to whatever is in PATH.
ifeq ($(wildcard /usr/local/go/bin/go),/usr/local/go/bin/go)
  GO := /usr/local/go/bin/go
else
  GO := $(shell which go 2>/dev/null || echo go)
endif

.PHONY: all build node wallet docker docker-run test clean deps tidy fmt lint run-node run-validator help

# ── Default ───────────────────────────────────────────────────────────────
all: deps build

# ── Dependencies ──────────────────────────────────────────────────────────
deps:
	$(GO) mod download

tidy:
	$(GO) mod tidy

# ── Build ─────────────────────────────────────────────────────────────────
build: node wallet

node:
	@echo "Building sqc-node (Go: $(GO))..."
	CGO_ENABLED=1 $(GO) build -mod=mod -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY_NODE) ./cmd/node
	@echo "  → $(BINARY_NODE)"

wallet:
	@echo "Building sqc-wallet..."
	CGO_ENABLED=1 $(GO) build -mod=mod -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY_WALLET) ./cmd/wallet
	@echo "  → $(BINARY_WALLET)"

# ── Docker ────────────────────────────────────────────────────────────────
docker:
	docker build -t $(DOCKER_IMAGE):$(VERSION) -t $(DOCKER_IMAGE):latest .
	@echo "Docker image built: $(DOCKER_IMAGE):$(VERSION)"

docker-run:
	docker run -it --rm \
		-p 8080:8080 \
		-v $(PWD)/sqc-data:/sqc/data \
		$(DOCKER_IMAGE):latest

docker-validator:
	@echo "Running validator node in Docker..."
	docker run -it --rm \
		-p 8080:8080 \
		-v $(PWD)/sqc-data:/sqc/data \
		-v $(PWD)/validator.sqwallet:/sqc/validator.sqwallet:ro \
		-e SQC_WALLET_PASS \
		$(DOCKER_IMAGE):latest \
		--data-dir /sqc/data \
		--rpc-addr :8080 \
		--validator \
		--wallet /sqc/validator.sqwallet \
		--log-level info

# ── Run (local) ───────────────────────────────────────────────────────────
run-node: build
	./$(BINARY_NODE) \
		--data-dir ./sqc-data \
		--rpc-addr :8080 \
		--log-level debug

run-validator: build
	@echo "Running as validator. Set SQC_WALLET_PASS env var first."
	./$(BINARY_NODE) \
		--data-dir ./sqc-data \
		--rpc-addr :8080 \
		--validator \
		--wallet ./validator.sqwallet \
		--block-interval 2 \
		--log-level debug

# Generate a new validator wallet
gen-validator-wallet: build
	./$(BINARY_WALLET) generate --out validator.sqwallet

# ── Test ──────────────────────────────────────────────────────────────────
test:
	$(GO) test -v -count=1 ./...

test-crypto:
	$(GO) test -v -count=1 ./core/crypto/...

test-race:
	$(GO) test -race -count=1 ./...

# ── Code quality ──────────────────────────────────────────────────────────
fmt:
	gofmt -w .

lint:
	@which golangci-lint > /dev/null || (echo "Install golangci-lint first"; exit 1)
	golangci-lint run ./...

vet:
	$(GO) vet ./...

# ── Install Go 1.23 (if not present) ─────────────────────────────────────
install-go:
	@echo "Installing Go 1.23.0..."
	wget -q https://go.dev/dl/go1.23.0.linux-amd64.tar.gz -O /tmp/go.tar.gz
	sudo rm -rf /usr/local/go
	sudo tar -C /usr/local -xzf /tmp/go.tar.gz
	rm /tmp/go.tar.gz
	echo 'export PATH=$$PATH:/usr/local/go/bin' >> ~/.bashrc
	@echo "Done. Run: source ~/.bashrc  then  make build"

# ── Full server setup (validator or rpc) ──────────────────────────────────
setup-validator:
	bash install.sh --validator

setup-rpc:
	bash install.sh --rpc

# ── Clean ─────────────────────────────────────────────────────────────────
clean:
	rm -f $(BINARY_NODE) $(BINARY_WALLET)
	rm -rf sqc-data/

# ── Help ──────────────────────────────────────────────────────────────────
# ── Genesis ───────────────────────────────────────────────────────────────────
gen-genesis: build
	@echo "Generating genesis.json (requires validator wallet)..."
	./$(BINARY_NODE) genesis init --wallet ./validator.sqwallet --out genesis.json
	@echo "Genesis file created: genesis.json"

# ── Config ────────────────────────────────────────────────────────────────────
gen-config:
	@echo "Generating default config.json..."
	./$(BINARY_NODE) config init --out config.json
	@echo "Config file created: config.json"

# ── SDK ───────────────────────────────────────────────────────────────────────
sdk-example:
	@echo "Running SDK example..."
	node sdk/example.js

help:
	@echo "Splendor Quantum Chain — Build Targets"
	@echo ""
	@echo "  make build             Build node + wallet binaries"
	@echo "  make node              Build sqc-node only"
	@echo "  make wallet            Build sqc-wallet only"
	@echo "  make docker            Build Docker image"
	@echo "  make docker-run        Run full node in Docker"
	@echo "  make docker-validator  Run validator in Docker (needs SQC_WALLET_PASS)"
	@echo "  make run-node          Run full node locally"
	@echo "  make run-validator     Run validator locally (needs SQC_WALLET_PASS)"
	@echo "  make gen-validator-wallet  Generate validator.sqwallet"
	@echo "  make gen-genesis       Generate genesis.json from validator wallet"
	@echo "  make gen-config        Generate default config.json"
	@echo "  make test              Run all tests"
	@echo "  make clean             Remove binaries and data"
	@echo ""
	@echo "Quick start (validator):"
	@echo "  make gen-validator-wallet"
	@echo "  make gen-genesis"
	@echo "  export SQC_WALLET_PASS=yourpassword"
	@echo "  make run-validator"
	@echo ""
	@echo "New in v1.1:"
	@echo "  Receipts + Event Logs · Node Config (JSON) · Genesis Builder"
	@echo "  TX Index Store · SSE WebSocket Subscriptions"
	@echo "  BFT Multi-Validator Consensus (Phase 2)"
	@echo "  QRC-20 Token Standard · Peer Discovery (PEX)"
	@echo "  Prometheus Metrics (:9100/metrics) · JavaScript SDK"
