# Splendor Quantum Chain — Build System
# Requires: Go 1.25+ or Docker
# All crypto: ML-DSA-65 (NIST FIPS 204) + SHA-3-256 (NIST FIPS 202)
# ML-DSA-65 is the standardised form of what CRYSTALS-Dilithium called Mode 3.
# The two are NOT interchangeable — signatures do not cross-verify — so the old
# name is gone from here to stop anyone reaching for a Mode-3 library.

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

.PHONY: all build node wallet docker docker-run docker-validator test test-crypto test-race clean deps tidy fmt lint vet vulncheck fuzz install-go setup-validator setup-rpc run-node run-validator gen-validator-wallet help

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
		-v $(PWD)/genesis.json:/sqc/genesis.json:ro \
		$(DOCKER_IMAGE):latest \
		--data-dir /sqc/data \
		--rpc-addr 0.0.0.0:8080 \
		--genesis /sqc/genesis.json \
		--validator \
		--wallet /sqc/validator.sqwallet \
		--log-level info

# ── Run (local) ───────────────────────────────────────────────────────────
run-node: build
	./$(BINARY_NODE) \
		--data-dir ./sqc-data \
		--rpc-addr 127.0.0.1:8080 \
		--log-level debug

# --genesis is required on a fresh chain: without it the allocations are never
# applied, every balance is zero, and no tx can meet the minimum fee.
run-validator: build
	@echo "Running as validator. Set SQC_WALLET_PASS env var first."
	./$(BINARY_NODE) \
		--data-dir ./sqc-data \
		--rpc-addr 127.0.0.1:8080 \
		--genesis ./genesis.json \
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

# ── Fuzzing ───────────────────────────────────────────────────────────────
# Bounded fuzzing of the peer-supplied SQCO wire decoders (object manifests,
# symbol proofs, frame probes). Each target runs for FUZZTIME (override:
# make fuzz FUZZTIME=60s). The committed seed corpus in
# network/objects/testdata/fuzz is also replayed by `make test`; this spends
# fresh inputs on top so a regression surfaces before a release.
FUZZTIME ?= 15s

fuzz:
	@set -e; for t in FuzzDecodeObjectManifest FuzzDecodeSymbolProof FuzzDecodeManifest FuzzFrameType FuzzObjectReceiverAbsorb; do \
		echo "== fuzz $$t ($(FUZZTIME))"; \
		$(GO) test ./network/objects/ -run "^$$t$$" -fuzz "$$t" -fuzztime $(FUZZTIME); \
	done
	@set -e; for t in FuzzValidateBlockBatchRange FuzzBlockBatchRequestJSON FuzzObjectSymbolRequestJSON FuzzObjectManifestRequestJSON; do \
		echo "== fuzz $$t ($(FUZZTIME))"; \
		$(GO) test ./network/ -run "^$$t$$" -fuzz "$$t" -fuzztime $(FUZZTIME); \
	done

# ── Code quality ──────────────────────────────────────────────────────────
fmt:
	gofmt -w .

lint:
	@which golangci-lint > /dev/null || (echo "Install golangci-lint first"; exit 1)
	golangci-lint run ./...

vet:
	$(GO) vet ./...

# Scans the REACHABLE dependency graph against the Go vulnerability database
# (the same check CI runs on every push). Pinned: v1.8+ requires Go 1.26 and
# would force a toolchain switch on the Go 1.25 toolchain in go.mod.
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...

# ── Install Go 1.25 (if not present) ─────────────────────────────────────
# Must match the `go` directive in go.mod: circl v1.6.5 (ML-DSA-65) needs 1.25.
install-go:
	@echo "Installing Go 1.25.0..."
	wget -q https://go.dev/dl/go1.25.0.linux-amd64.tar.gz -O /tmp/go.tar.gz
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

# ── Genesis ───────────────────────────────────────────────────────────────────
# There is no genesis generator: the old `sqc-node genesis init` target invoked a
# subcommand that does not exist, so it never worked. Write genesis.json by hand
# — the recipe (validator pubkey hex, allocations, total_supply) is in DEPLOY.md
# section 6. `sqc-node genesis-hash <genesis.json>` prints the document pin.

# ── Config ────────────────────────────────────────────────────────────────────
# There is no config file either: cmd/node never calls config.LoadFile, so a
# generated config.json would be read by nothing. Everything is flags.

# ── Help ──────────────────────────────────────────────────────────────────────
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
	@echo "  make test              Run all tests"
	@echo "  make test-race         Run all tests under the race detector"
	@echo "  make fuzz              Fuzz the SQCO wire decoders (FUZZTIME ?= 15s)"
	@echo "  make vulncheck         Scan reachable deps against the Go vuln DB"
	@echo "  make lint / vet / fmt  Code quality"
	@echo "  make clean             Remove binaries and data"
	@echo ""
	@echo "Quick start (validator):"
	@echo "  make gen-validator-wallet"
	@echo "  # write genesis.json by hand — see DEPLOY.md section 6"
	@echo "  export SQC_WALLET_PASS=yourpassword"
	@echo "  make run-validator"
	@echo ""
	@echo "Shipped and wired into the node:"
	@echo "  Receipts + Event Logs · TX Index Store · SSE subscriptions on /ws"
	@echo "  QRC-20/721/1155 · DEX · lending · staking (native Go modules)"
	@echo "  Peer discovery (PEX) · ML-KEM-768 sealed P2P · Prometheus :9100"
	@echo "  JavaScript SDK (sdk/sqc-sdk.js) + @splendor/pq-bridge WASM signer"
	@echo ""
	@echo "Present in the tree but NOT wired into the binary:"
	@echo "  consensus/bft.go  — nothing constructs it; Phase 1 is one validator"
	@echo "  config/config.go  — cmd/node never calls config.LoadFile"
