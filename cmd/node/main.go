// Splendor Quantum Chain — Full Node (v2)
//
// 100% quantum-native blockchain node.
// All crypto: ML-DSA-65 (FIPS-204) + SHA3-256.
// Zero ECDSA. Zero SHA-256. Zero keccak. Zero secp256k1.
//
// Usage:
//
//	sqc-node --data-dir /var/sqc/data --rpc-addr :8080
//	sqc-node --data-dir /var/sqc/data --rpc-addr :8080 --validator --wallet validator.sqwallet
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/consensus"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/rpc"
)

var (
	flagDataDir           string
	flagRPCAddr           string
	flagMetricsAddr       string
	flagWalletPath        string
	flagWalletPass        string
	flagWalletPassFile    string
	flagPeers             []string
	flagBlockInterval     int
	flagIsValidator       bool
	flagLogLevel          string
	flagGenesisPath       string
	flagValidatorPKHex    string
	flagGenesisHashHex    string
	flagRPCAuthToken      string
	flagAllowPrivatePeers bool
	flagCORSOrigins       []string
	flagRateLimit         float64
	flagRateBurst         float64
	flagTrustProxy        bool
	flagTLSCert           string
	flagTLSKey            string
	flagPublicURL         string
	flagAllowPlaintextP2P bool
	flagPeerPins          []string
)

func main() {
	root := &cobra.Command{
		Use:   "sqc-node",
		Short: "Splendor Quantum Chain full node",
		Long: `sqc-node runs a Splendor Quantum Chain full node.
Cryptography: ML-DSA-65 (FIPS-204) + SHA3-256.
No ECDSA. No SHA-256. No conventional cryptography.`,
		RunE: runNode,
	}

	root.Flags().StringVar(&flagDataDir, "data-dir", "./sqc-data", "Directory for chain data")
	// Loopback by default: a node should not become internet-reachable merely by
	// being started. Operators opt into exposure explicitly, ideally behind a
	// terminating proxy.
	root.Flags().StringVar(&flagRPCAddr, "rpc-addr", "127.0.0.1:8080", "RPC server listen address (use 0.0.0.0:8080 to expose publicly)")
	root.Flags().StringVar(&flagMetricsAddr, "metrics-addr", "127.0.0.1:9100", "Prometheus metrics listen address (metrics leak node internals — keep private)")
	root.Flags().StringVar(&flagWalletPath, "wallet", "", "Path to validator .sqwallet file")
	root.Flags().StringVar(&flagWalletPass, "wallet-pass", "", "Wallet password. Visible to any local user via 'ps' — prefer --wallet-pass-file or SQC_WALLET_PASS.")
	root.Flags().StringVar(&flagWalletPassFile, "wallet-pass-file", "", "Read the wallet password from a file (must not be readable by other users)")
	root.Flags().StringArrayVar(&flagPeers, "peer", nil, "Bootstrap peer RPC addresses")
	root.Flags().IntVar(&flagBlockInterval, "block-interval", 2, "Block interval in seconds (validator only)")
	root.Flags().BoolVar(&flagIsValidator, "validator", false, "Run as block-producing validator")
	root.Flags().StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	root.Flags().StringVar(&flagGenesisPath, "genesis", "", "Path to genesis.json (allocations applied once on an empty chain)")
	root.Flags().StringVar(&flagValidatorPKHex, "validator-pubkey-hex", "", "Trusted genesis validator ML-DSA-65 public key (hex). Full nodes MUST set this to authenticate pushed blocks on POST /block.")
	root.Flags().StringVar(&flagGenesisHashHex, "genesis-hash", "", "Pinned hash of block 0 (hex). Required to bootstrap the validator pubkey from peers when --validator-pubkey-hex is not supplied; without one, a node would trust whatever key its first peer serves.")
	root.Flags().StringVar(&flagRPCAuthToken, "rpc-auth-token", "", "Shared secret for the write plane: POST /block and POST /peers/announce. All nodes in a cluster must share it. (or SQC_RPC_AUTH_TOKEN env)")
	root.Flags().BoolVar(&flagAllowPrivatePeers, "allow-private-peers", false, "Allow loopback/private-IP peers in discovery + /peers/announce (private-network or local clusters; default off = public SSRF-safe)")
	root.Flags().StringArrayVar(&flagCORSOrigins, "cors-origin", nil, "Browser origin allowed to call this node (repeatable). Default: none. Use '*' to allow any origin.")
	root.Flags().Float64Var(&flagRateLimit, "rpc-rate-limit", 25, "Sustained requests/second per client IP on tx submission and x402 routes (0 disables)")
	root.Flags().Float64Var(&flagRateBurst, "rpc-rate-burst", 50, "Burst allowance per client IP for rate-limited routes")
	root.Flags().BoolVar(&flagTrustProxy, "trust-proxy", false, "Read client IP from X-Forwarded-For. ONLY enable behind a proxy you control — otherwise clients can spoof it and bypass rate limits.")
	root.Flags().StringVar(&flagTLSCert, "tls-cert", "", "PEM certificate for HTTPS. Enables TLS 1.3 with post-quantum hybrid key exchange (X25519MLKEM768).")
	root.Flags().StringVar(&flagTLSKey, "tls-key", "", "PEM private key matching --tls-cert")
	root.Flags().StringVar(&flagPublicURL, "public-url", "", "Externally reachable base URL advertised to peers (default: derived from --rpc-addr)")
	root.Flags().BoolVar(&flagAllowPlaintextP2P, "allow-plaintext-p2p", false, "Allow unencrypted block push and catch-up sync when a peer has no ML-KEM sealed transport. Off by default so the channel cannot be downgraded.")
	root.Flags().StringArrayVar(&flagPeerPins, "peer-pin", nil, "Pin a peer's node identity as URL=PUBKEY_HEX (repeatable). Without a pin, peer identities are trusted on first use.")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runNode(cmd *cobra.Command, args []string) error {
	nodeStartTime := time.Now()
	setupLogging(flagLogLevel)

	// Opt into private/loopback peers for private-network or local multi-node clusters.
	network.AllowPrivatePeers = flagAllowPrivatePeers

	if (flagTLSCert == "") != (flagTLSKey == "") {
		return fmt.Errorf("--tls-cert and --tls-key must be provided together")
	}
	tlsEnabled := flagTLSCert != ""

	log.Info().
		Str("chain_id", types.ChainID).
		Str("data_dir", flagDataDir).
		Str("rpc_addr", flagRPCAddr).
		Bool("validator", flagIsValidator).
		Msg("Splendor Quantum Chain node starting")

	// ── Ensure data directory exists ──────────────────────────────────────
	if err := os.MkdirAll(flagDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// ── Open databases ─────────────────────────────────────────────────────
	stateDB, err := state.NewStateDB(flagDataDir + "/state")
	if err != nil {
		return fmt.Errorf("state DB: %w", err)
	}
	defer stateDB.Close()

	executor, err := block.NewExecutor(stateDB, flagDataDir+"/chain")
	if err != nil {
		return fmt.Errorf("chain DB: %w", err)
	}
	defer executor.Close()

	// Optional: TX index + receipt store
	txIndex, txErr := state.NewTxIndexStore(flagDataDir + "/txindex")
	if txErr != nil {
		log.Warn().Err(txErr).Msg("TX index unavailable — receipts will not be stored")
	} else {
		executor.WithTxIndex(txIndex)
		defer txIndex.Close()
	}

	// ── Hook registry ──────────────────────────────────────────────────────
	hooks := block.NewHookRegistry()
	hooks.OnPreTx("min_fee", block.MinFeeHook(1000))
	hooks.OnPreBlock("block_size", block.BlockSizeHook(block.MaxTxPerBlock))
	hooks.OnPostBlock("logging", block.LoggingHook())
	executor.WithHooks(hooks)

	// ── Mempool ────────────────────────────────────────────────────────────
	pool := mempool.New(mempool.DefaultMaxSize, stateDB)

	// ── AI Scheduler ───────────────────────────────────────────────────────
	scheduler := ai.NewScheduler()
	scheduler.StartObservationLoop(func() ai.Metrics {
		return ai.Metrics{MempoolDepth: pool.Size()}
	})

	// ── Metrics server ─────────────────────────────────────────────────────
	nodeMetrics := rpc.NewNodeMetrics()
	nodeMetrics.SetLabel("chain_id", types.ChainID)
	nodeMetrics.SetLabel("version", "1.0.0-quantum")
	metricsServer := rpc.NewMetricsServer(flagMetricsAddr, nodeMetrics)
	if flagTLSCert != "" && flagTLSKey != "" {
		if err := metricsServer.EnableTLS(flagTLSCert, flagTLSKey); err != nil {
			return fmt.Errorf("metrics TLS: %w", err)
		}
	}
	metricsServer.Start()
	defer metricsServer.Stop()

	// ── Load validator key ─────────────────────────────────────────────────
	var validatorPub *qcrypto.PublicKey
	var validatorPriv *qcrypto.PrivateKey
	var validatorAddr qcrypto.Address
	var validatorPKBytes []byte

	if flagIsValidator {
		if flagWalletPath == "" {
			return fmt.Errorf("--wallet is required when running as validator")
		}
		pass, passErr := validatorPassphrase()
		if passErr != nil {
			return passErr
		}
		validatorPub, validatorPriv, err = qcrypto.LoadWallet(flagWalletPath, []byte(pass))
		if err != nil {
			return fmt.Errorf("failed to load validator wallet: %w", err)
		}
		// The signing key stays in memory for the life of the process, but do
		// not leave it there after shutdown alongside a core dump.
		defer validatorPriv.Zeroize()

		validatorAddr = qcrypto.DeriveAddress(validatorPub)
		validatorPKBytes = validatorPub.Bytes()
		log.Info().Str("validator_addr", validatorAddr.String()).Msg("Validator key loaded")
	}

	// Full nodes: pin the trusted genesis validator pubkey so inbound (POST /block)
	// and sync blocks are ML-DSA-65-verified. Without it a full node cannot
	// authenticate pushed blocks and fails closed on the push path (relying on
	// pull-sync, which bootstraps the key from the genesis block).
	if !flagIsValidator && flagValidatorPKHex != "" {
		pkBytes, decErr := hex.DecodeString(strings.TrimPrefix(flagValidatorPKHex, "0x"))
		if decErr != nil {
			return fmt.Errorf("invalid --validator-pubkey-hex: %w", decErr)
		}
		if _, perr := qcrypto.PublicKeyFromBytes(pkBytes); perr != nil {
			return fmt.Errorf("invalid --validator-pubkey-hex: %w", perr)
		}
		validatorPKBytes = pkBytes
		log.Info().Str("pubkey_prefix", fmt.Sprintf("%x", pkBytes[:8])).
			Msg("Trusted validator pubkey pinned (full node block verification enabled)")
	}

	// Optional genesis pin. This is what lets a node with no pinned validator
	// key learn one safely: block 0 is authenticated by hash before the key it
	// carries is trusted for every subsequent block.
	var genesisHashPin *[32]byte
	if flagGenesisHashHex != "" {
		raw, decErr := hex.DecodeString(strings.TrimPrefix(flagGenesisHashHex, "0x"))
		if decErr != nil {
			return fmt.Errorf("invalid --genesis-hash: %w", decErr)
		}
		if len(raw) != 32 {
			return fmt.Errorf("invalid --genesis-hash: want 32 bytes (64 hex chars), got %d", len(raw))
		}
		var pin [32]byte
		copy(pin[:], raw)
		genesisHashPin = &pin
		log.Info().Str("genesis_hash", fmt.Sprintf("%x", pin[:8])).Msg("Genesis hash pinned")
	}

	// Write-plane token for block ingest + peer announce (env fallback).
	authToken := flagRPCAuthToken
	if authToken == "" {
		authToken = os.Getenv("SQC_RPC_AUTH_TOKEN")
	}
	if authToken == "" {
		log.Warn().Msg("No --rpc-auth-token set: the write plane (POST /block, POST /peers/announce) is DISABLED. " +
			"Push-based propagation will not work; full nodes will fall back to pull-sync.")
	}

	// ── P2P peer pool + discovery ──────────────────────────────────────────
	// selfURL is the address we advertise to peers. Derived from --rpc-addr for
	// local use; on a real deployment set --public-url to the reachable name,
	// since a loopback default would advertise an address no peer can dial.
	selfURL := flagPublicURL
	if selfURL == "" {
		selfURL = defaultSelfURL(flagRPCAddr, tlsEnabled)
	}

	peerPool := network.NewPeerPool(selfURL, 50)
	broadcaster := network.NewBroadcaster(validatorPKBytes, authToken, flagAllowPlaintextP2P)
	discoverer := network.NewDiscoverer(peerPool, selfURL, 60*time.Second)

	// ── Node identity for the encrypted P2P transport ──────────────────────
	// A validator reuses its wallet key; other nodes get a persistent identity
	// so peers that pinned them on first contact keep matching across restarts.
	nodeIdentity, err := loadNodeIdentity(flagDataDir, validatorPub, validatorPriv)
	if err != nil {
		return fmt.Errorf("node identity: %w", err)
	}

	// Pin peers whose node key the operator supplied, so the sealed channel is
	// authenticated against configuration rather than trust-on-first-use.
	for _, pin := range flagPeerPins {
		peerURL, pkHex, found := strings.Cut(pin, "=")
		if !found {
			return fmt.Errorf("invalid --peer-pin %q: want URL=PUBKEY_HEX", pin)
		}
		pk, decErr := hex.DecodeString(strings.TrimPrefix(pkHex, "0x"))
		if decErr != nil {
			return fmt.Errorf("invalid --peer-pin pubkey for %s: %w", peerURL, decErr)
		}
		if _, perr := qcrypto.PublicKeyFromBytes(pk); perr != nil {
			return fmt.Errorf("invalid --peer-pin pubkey for %s: %w", peerURL, perr)
		}
		broadcaster.Identities().Pin(peerURL, pk)
		log.Info().Str("peer", peerURL).Msg("pinned peer node identity")
	}

	for _, peer := range flagPeers {
		peerPool.Add(&network.PeerInfo{Addr: peer, LastSeen: time.Now().Unix()})
		// Validator nodes: add RPC peers to broadcaster so blocks get pushed
		if flagIsValidator {
			broadcaster.AddPeer(peer)
		}
	}
	discoverer.Bootstrap(flagPeers)
	discoverer.Start()
	defer discoverer.Stop()

	// Announce ourselves to peers so they can push blocks back to us (RPC nodes)
	// and so the validator knows about us (bidirectional peer tracking).
	if len(flagPeers) > 0 {
		go func() {
			time.Sleep(3 * time.Second) // wait for node to be ready
			h, _ := executor.LatestHeight()
			broadcaster.AnnounceSelf(selfURL, h)
		}()
	}

	// ── Block sync ─────────────────────────────────────────────────────────
	// Catch-up pulls ride the same sealed channel and the same pinned peer
	// identities as block push, so falling behind does not drop the node back
	// onto cleartext HTTP.
	syncCfg := network.DefaultSyncConfig(validatorPKBytes)
	syncCfg.Identities = broadcaster.Identities()
	syncCfg.AllowPlaintext = flagAllowPlaintextP2P
	if genesisHashPin != nil {
		syncCfg.GenesisHash = *genesisHashPin
	}
	if len(validatorPKBytes) == 0 && genesisHashPin == nil {
		log.Warn().Msg("Neither --validator-pubkey-hex nor --genesis-hash is set: this node cannot " +
			"authenticate blocks from peers and will not sync. Pin one of them.")
	}

	syncer := network.NewSyncer(
		syncCfg,
		peerPool,
		func() uint64 { h, _ := executor.LatestHeight(); return h },
		func(b *types.Block) error { return executor.ExecuteBlock(b) },
	)

	// ── Context + signal handling ──────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info().Msg("Shutdown signal received")
		cancel()
	}()

	// ── Block channel (for WebSocket broadcasts + metrics) ─────────────────
	blockCh := make(chan *types.Block, 64)

	// ── WebSocket hub ──────────────────────────────────────────────────────
	wsHub := rpc.NewHub()
	go func() {
		for b := range blockCh {
			wsHub.BroadcastNewBlock(b)
			nodeMetrics.SetChainHeight(b.Header.Height)
			nodeMetrics.IncrBlocksProduced()
			nodeMetrics.IncrTxsProcessed(uint64(b.Header.TxCount))
		}
	}()

	// ── Crash recovery ─────────────────────────────────────────────────────
	// The chain head advances (atomically) before state is committed, so a crash
	// can leave the head ahead of the state. Replay any such blocks before we
	// serve or seal. No-op on a fresh DB or a cleanly-shut-down node.
	if err := executor.ReconcileState(); err != nil {
		return fmt.Errorf("state reconciliation failed: %w", err)
	}

	// ── Genesis (if chain is empty) ────────────────────────────────────────
	if latestBlock, _ := executor.LatestBlock(); latestBlock == nil {
		// Credit genesis allocations before block 0 so every node — validator
		// and sync — starts from the same funded state root. Without this the
		// total_supply declared in genesis.json is never minted, the chain has
		// zero balance, and no transaction can meet the minimum fee.
		if flagGenesisPath != "" {
			g, err := genesiscfg.LoadFile(flagGenesisPath)
			if err != nil {
				return fmt.Errorf("load genesis %q: %w", flagGenesisPath, err)
			}
			if _, err := g.ApplyToState(stateDB); err != nil {
				return fmt.Errorf("apply genesis allocations: %w", err)
			}
			log.Info().Int("allocations", len(g.Allocations)).Str("total_supply", g.TotalSupply).Msg("Applied genesis allocations")
		}

		// The validator builds and signs block 0 over the resulting state root.
		if flagIsValidator && validatorPriv != nil {
			log.Info().Msg("Chain is empty — creating genesis block")
			stateRoot, _ := stateDB.StateRoot()
			genesisBlock := types.GenesisBlock(validatorAddr, stateRoot, time.Now().Unix())
			genesisBlock.ValidatorPubKey = validatorPKBytes
			if hash, hashErr := genesisBlock.ComputeHash(); hashErr == nil {
				genesisBlock.Hash = hash
				if signErr := genesisBlock.SignBlock(validatorPriv); signErr == nil {
					if execErr := executor.ExecuteBlock(genesisBlock); execErr == nil {
						log.Info().Str("hash", fmt.Sprintf("%x", genesisBlock.Hash[:8])).Msg("Genesis block committed")
					} else {
						log.Warn().Err(execErr).Msg("Genesis block execution failed")
					}
				}
			}
		}
	}

	// ── Validator consensus ────────────────────────────────────────────────
	if flagIsValidator && validatorPriv != nil {
		vCfg := consensus.Config{
			BlockInterval: time.Duration(flagBlockInterval) * time.Second,
			MaxTxPerBlock: block.MaxTxPerBlock,
			Address:       validatorAddr,
			PublicKey:     validatorPKBytes,
			PrivateKey:    validatorPriv,
		}
		v := consensus.New(vCfg, executor, pool, scheduler)

		// Validator: subscribe to block channel to push blocks to peers immediately.
		// This provides realtime push-based sync (like geth's block propagation).
		// The channel fan-out: blockCh → wsHub + broadcaster.
		broadcastCh := make(chan *types.Block, 64)
		v.Subscribe(broadcastCh)

		go func() {
			for b := range broadcastCh {
				// Send to WebSocket hub
				select {
				case blockCh <- b:
				default:
				}
				// Push to all registered RPC peers immediately
				broadcaster.Broadcast(b)
			}
		}()

		go func() {
			if err := v.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("Validator exited with error")
			}
		}()
	}

	// ── RPC node: bootstrap genesis state from validator peer ──────────────
	// The RPC node starts with empty state. The validator applied genesis.json
	// allocations before creating block 0, so its state root ≠ empty state root.
	// Before syncing blocks, the RPC must fetch and apply the same genesis
	// allocations so its state root matches the validator's.
	if !flagIsValidator && len(flagPeers) > 0 {
		go func() {
			time.Sleep(2 * time.Second) // wait for HTTP server to start
			for _, peer := range flagPeers {
				if err := bootstrapGenesisFromPeer(peer, stateDB, executor, flagGenesisPath); err != nil {
					log.Warn().Err(err).Str("peer", peer).Msg("genesis bootstrap from peer failed")
					continue
				}
				break // success
			}
		}()
	}

	// ── Periodic sync (full nodes) ─────────────────────────────────────────
	// Sync every 1 second so an RPC node stays within 1-2 blocks of the validator
	// (the explorer reads from RPC nodes, so their freshness is user-visible).
	//
	// Each tick we first refresh peer heights by polling their /status. Without
	// this, BestPeerHeight only updated on the 60s peer-exchange cycle, so a full
	// node believed it was already synced between refreshes and lagged the
	// validator by up to a minute, catching up in bursts. Polling here makes the
	// pull path genuinely real-time on any topology, without needing push/announce
	// or an advertised self-URL.
	if !flagIsValidator {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					for _, addr := range peerPool.Addrs() {
						if h, derr := network.DialPeer(addr); derr == nil {
							peerPool.UpdatePeerHeight(addr, h)
						}
					}
					if n, err := syncer.SyncOnce(); err != nil {
						log.Warn().Err(err).Msg("sync failed")
					} else if n > 0 {
						log.Debug().Int("blocks", n).Msg("synced blocks")
					}
				}
			}
		}()
	}

	// ── Metrics update loop ────────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h, _ := executor.LatestHeight()
				nodeMetrics.SetChainHeight(h)
				nodeMetrics.SetMempoolSize(pool.Size())
				nodeMetrics.SetPeerCount(peerPool.Count())
				nodeMetrics.SetWSClientCount(wsHub.ClientCount())
				if scheduler != nil {
					stats := scheduler.GetStats()
					nodeMetrics.SetAIBatchSize(stats.BatchSize)
					nodeMetrics.SetAIAnomaly(stats.AnomalyActive)
				}
			}
		}
	}()

	// ── Build HTTP router ──────────────────────────────────────────────────
	router := mux.NewRouter()

	isReady := func() bool { h, _ := executor.LatestHeight(); return h > 0 }

	// Core RPC server (handles /tx/submit, /block/*, /balance/*, /status, etc.)
	coreRPC := rpc.New(
		rpc.Config{
			ListenAddr:       flagRPCAddr,
			ValidatorPK:      validatorPKBytes,
			PeerCount:        peerPool.Count, // wire live peer count from peerPool
			AuthToken:        authToken,
			CORSOrigins:      flagCORSOrigins,
			SubmitRatePerSec: flagRateLimit,
			SubmitBurst:      flagRateBurst,
			TrustProxy:       flagTrustProxy,
		},
		stateDB, executor, pool, nil, scheduler,
	)

	// VM routes (/vm/disasm, /vm/validate, /healthz, /readyz)
	rpc.RegisterVMRoutes(router, nodeStartTime, isReady)

	// Explorer routes (/tx/{hash}/receipt, /contract/*, /sync, /peers).
	// Pass broadcaster.AddPeer so when a peer announces via POST /peers/announce,
	// the validator immediately adds it to the broadcast list (push-based realtime sync).
	// Note: RegisterExplorerRoutes handles nil txIndex gracefully.
	rpc.RegisterExplorerRoutes(router, txIndex, stateDB, peerPool, func() uint64 {
		h, _ := executor.LatestHeight()
		return h
	}, coreRPC.WriteAuth(), coreRPC.SimGuard(), broadcaster.AddPeer)

	// Post-quantum sealed P2P transport (ML-KEM-768 + AES-256-GCM). Carries the
	// same block-ingest and peer-announce operations as the plaintext write
	// plane, but encrypted and with the write token inside the envelope.
	rpc.RegisterP2PRoutes(router, rpc.P2PConfig{
		Identity: nodeIdentity,
		Token:    authToken,
		HandleBlock: func(body []byte) error {
			return network.HandleIncomingBlock(body, validatorPKBytes, executor.ExecuteBlock)
		},
		HandleAnnounce: func(body []byte) error {
			var peer network.PeerInfo
			if err := json.Unmarshal(body, &peer); err != nil {
				return fmt.Errorf("invalid peer info: %w", err)
			}
			if peer.Addr == "" {
				return fmt.Errorf("peer addr required")
			}
			if err := network.IsSafePeerURL(peer.Addr); err != nil {
				return fmt.Errorf("rejected peer addr: %w", err)
			}
			peerPool.Add(&peer)
			broadcaster.AddPeer(peer.Addr)
			return nil
		},
		// Read side of the sealed channel: lets a peer catch up without
		// dropping to plaintext GET /block/{height}.
		ServeBlock: func(height uint64) ([]byte, error) {
			b, err := executor.GetBlockByHeight(height)
			if err != nil {
				return nil, fmt.Errorf("block %d not available: %w", height, err)
			}
			return json.Marshal(b)
		},
		ServeLatestBlock: func() ([]byte, error) {
			b, err := executor.LatestBlock()
			if err != nil {
				return nil, fmt.Errorf("latest block not available: %w", err)
			}
			return json.Marshal(b)
		},
	})

	// Governance + staking routes
	rpc.RegisterGovRoutes(router, stateDB)

	// DEX routes
	rpc.RegisterDEXRoutes(router, stateDB)

	// Contract-interaction routes (/call view calls, /estimate gas+fee) — issue #4
	rpc.RegisterCallRoutes(router, stateDB, executor, pool, coreRPC.SimGuard())

	// Lending routes
	rpc.RegisterLendingRoutes(router, stateDB)

	// GET /genesis — serve genesis.json so RPC nodes can bootstrap genesis state.
	// Only served if the validator has a --genesis file configured.
	if flagGenesisPath != "" {
		router.HandleFunc("/genesis", func(w http.ResponseWriter, r *http.Request) {
			data, err := os.ReadFile(flagGenesisPath)
			if err != nil {
				http.Error(w, "genesis.json not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		}).Methods(http.MethodGet)
	}

	// WebSocket / SSE
	router.HandleFunc("/ws", wsHub.HandleWebSocket).Methods(http.MethodGet)

	// Fallback: route everything else to core RPC
	router.PathPrefix("/").HandlerFunc(coreRPC.ServeHTTP)

	// ── Start HTTP server ──────────────────────────────────────────────────
	// The origin policy is applied by the core RPC router; wrapping the outer
	// router with the same policy means explorer/gov/DEX routes get identical
	// treatment instead of silently defaulting to permissive.
	httpServer := &http.Server{
		Addr:         flagRPCAddr,
		Handler:      coreRPC.CORS().Middleware(router),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if tlsEnabled {
		tlsCfg, tlsErr := rpc.PQTLSConfig(flagTLSCert, flagTLSKey)
		if tlsErr != nil {
			return tlsErr
		}
		httpServer.TLSConfig = tlsCfg
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		httpServer.Shutdown(shutCtx)
	}()

	log.Info().
		Str("rpc_addr", flagRPCAddr).
		Str("metrics_addr", flagMetricsAddr).
		Str("self_url", selfURL).
		Bool("validator", flagIsValidator).
		Bool("tls", tlsEnabled).
		Bool("write_plane", authToken != "").
		Int("cors_origins", len(flagCORSOrigins)).
		Msg("Node ready")

	var serveErr error
	if tlsEnabled {
		log.Info().Msg("TLS 1.3 enabled with post-quantum hybrid key exchange (X25519MLKEM768)")
		serveErr = httpServer.ListenAndServeTLS(flagTLSCert, flagTLSKey)
	} else {
		serveErr = httpServer.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		if ctx.Err() == nil {
			return fmt.Errorf("HTTP server: %w", serveErr)
		}
	}

	log.Info().Msg("Node stopped")
	return nil
}

// nodeIdentityFile is where a non-validator node persists its P2P identity key.
const nodeIdentityFile = "node_identity.key"

// loadNodeIdentity returns the ML-DSA key material backing this node's
// encrypted P2P transport.
//
// A validator reuses its wallet key, so its P2P identity is the same key peers
// already trust for block signatures. Other nodes persist a generated key: an
// ephemeral one would change on every restart, and peers that pinned the old
// identity would (correctly) refuse to talk to it.
func loadNodeIdentity(dataDir string, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey) (*network.NodeIdentity, error) {
	if pub != nil && priv != nil {
		return network.NewNodeIdentity(pub, priv)
	}

	path := filepath.Join(dataDir, nodeIdentityFile)
	if raw, err := os.ReadFile(path); err == nil {
		loaded, perr := qcrypto.PrivateKeyFromBytes(raw)
		if perr != nil {
			return nil, fmt.Errorf("stored node identity at %s is corrupt: %w", path, perr)
		}
		return network.NewNodeIdentity(loaded.Public(), loaded)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read node identity: %w", err)
	}

	newPub, newPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("generate node identity: %w", err)
	}
	// 0600: this key authenticates the node to its peers.
	if err := os.WriteFile(path, newPriv.Bytes(), 0600); err != nil {
		return nil, fmt.Errorf("persist node identity: %w", err)
	}
	log.Info().Str("path", path).Msg("Generated persistent node P2P identity")
	return network.NewNodeIdentity(newPub, newPriv)
}

// validatorPassphrase resolves the wallet passphrase from, in order, a file,
// the environment, or the command line.
//
// The command line is last and warns: arguments are world-readable through
// /proc and 'ps', so passing the passphrase there leaks the secret that
// protects the block-signing key to every local user on the machine.
func validatorPassphrase() (string, error) {
	if flagWalletPassFile != "" {
		if err := qcrypto.CheckKeyFilePermissions(flagWalletPassFile); err != nil {
			return "", err
		}
		b, err := os.ReadFile(flagWalletPassFile)
		if err != nil {
			return "", fmt.Errorf("read --wallet-pass-file: %w", err)
		}
		pass := strings.TrimRight(string(b), "\r\n")
		if pass == "" {
			return "", fmt.Errorf("--wallet-pass-file %s is empty", flagWalletPassFile)
		}
		return pass, nil
	}
	if pass := os.Getenv("SQC_WALLET_PASS"); pass != "" {
		return pass, nil
	}
	if flagWalletPass != "" {
		log.Warn().Msg("--wallet-pass is visible to other local users via 'ps'; " +
			"prefer --wallet-pass-file or SQC_WALLET_PASS")
		return flagWalletPass, nil
	}
	return "", fmt.Errorf("wallet password required: use --wallet-pass-file, " +
		"SQC_WALLET_PASS, or --wallet-pass")
}

// defaultSelfURL derives the address advertised to peers from the listen
// address. A bare or wildcard host is reported as localhost, which is only
// correct for single-host development — real deployments must set --public-url.
func defaultSelfURL(listenAddr string, useTLS bool) string {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return scheme + "://localhost" + listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// bootstrapGenesisFromPeer fetches genesis.json from a peer validator and applies
// allocations to the local state. This is called on RPC nodes with an empty chain
// so their state root matches the validator's before block sync starts.
func bootstrapGenesisFromPeer(peerURL string, stateDB *state.StateDB, executor *block.Executor, localGenesisPath string) error {
	// If chain already has blocks, no need to bootstrap
	latest, _ := executor.LatestBlock()
	if latest != nil {
		return nil
	}

	// If local genesis.json is specified, use it directly
	if localGenesisPath != "" {
		g, err := genesiscfg.LoadFile(localGenesisPath)
		if err != nil {
			return fmt.Errorf("load local genesis: %w", err)
		}
		if _, err := g.ApplyToState(stateDB); err != nil {
			return fmt.Errorf("apply local genesis: %w", err)
		}
		log.Info().Str("path", localGenesisPath).Msg("RPC: applied local genesis.json allocations")
		return nil
	}

	// Fetch genesis.json from the validator peer
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(peerURL + "/genesis")
	if err != nil {
		return fmt.Errorf("fetch /genesis from peer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Warn().Str("peer", peerURL).Msg("RPC: peer has no /genesis endpoint — state root mismatch may occur")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer /genesis returned %d", resp.StatusCode)
	}

	g, err := genesiscfg.LoadFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("parse peer genesis.json: %w", err)
	}

	if _, err := g.ApplyToState(stateDB); err != nil {
		return fmt.Errorf("apply peer genesis allocations: %w", err)
	}

	stateRoot, _ := stateDB.StateRoot()
	log.Info().
		Str("peer", peerURL).
		Int("allocations", len(g.Allocations)).
		Str("state_root", fmt.Sprintf("%x", stateRoot[:8])).
		Msg("RPC: bootstrapped genesis allocations from validator peer")
	return nil
}

func setupLogging(level string) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}
