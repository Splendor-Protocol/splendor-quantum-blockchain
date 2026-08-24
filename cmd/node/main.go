// Splendor Quantum Chain — Full Node (v2)
//
// 100% quantum-native blockchain node.
// All crypto: CRYSTALS-Dilithium Mode 3 (round-3, not FIPS-204 ML-DSA) + SHA3-256.
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
	"fmt"
	"net/http"
	"os"
	"os/signal"
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
	flagPeers             []string
	flagBlockInterval     int
	flagIsValidator       bool
	flagLogLevel          string
	flagGenesisPath       string
	flagValidatorPKHex    string
	flagRPCAuthToken      string
	flagEnableX402        bool
	flagAllowPrivatePeers bool
)

func main() {
	root := &cobra.Command{
		Use:   "sqc-node",
		Short: "Splendor Quantum Chain full node",
		Long: `sqc-node runs a Splendor Quantum Chain full node.
Cryptography: CRYSTALS-Dilithium Mode 3 (round-3, not FIPS-204 ML-DSA) + SHA3-256.
No ECDSA. No SHA-256. No conventional cryptography.`,
		RunE: runNode,
	}

	root.Flags().StringVar(&flagDataDir, "data-dir", "./sqc-data", "Directory for chain data")
	root.Flags().StringVar(&flagRPCAddr, "rpc-addr", ":8080", "RPC server listen address")
	root.Flags().StringVar(&flagMetricsAddr, "metrics-addr", ":9100", "Prometheus metrics listen address")
	root.Flags().StringVar(&flagWalletPath, "wallet", "", "Path to validator .sqwallet file")
	root.Flags().StringVar(&flagWalletPass, "wallet-pass", "", "Wallet password (prefer SQC_WALLET_PASS env)")
	root.Flags().StringArrayVar(&flagPeers, "peer", nil, "Bootstrap peer RPC addresses")
	root.Flags().IntVar(&flagBlockInterval, "block-interval", 2, "Block interval in seconds (validator only)")
	root.Flags().BoolVar(&flagIsValidator, "validator", false, "Run as block-producing validator")
	root.Flags().StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	root.Flags().StringVar(&flagGenesisPath, "genesis", "", "Path to genesis.json (allocations applied once on an empty chain)")
	root.Flags().StringVar(&flagValidatorPKHex, "validator-pubkey-hex", "", "Trusted genesis validator Dilithium public key (hex). Full nodes MUST set this to authenticate pushed blocks on POST /block.")
	root.Flags().StringVar(&flagRPCAuthToken, "rpc-auth-token", "", "Shared secret required on admin write routes (or SQC_RPC_AUTH_TOKEN env)")
	root.Flags().BoolVar(&flagEnableX402, "enable-x402-settle", false, "Expose POST /x402/settle (direct, non-consensus settlement; requires --rpc-auth-token)")
	root.Flags().BoolVar(&flagAllowPrivatePeers, "allow-private-peers", false, "Allow loopback/private-IP peers in discovery + /peers/announce (private-network or local clusters; default off = public SSRF-safe)")

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
		pass := flagWalletPass
		if pass == "" {
			pass = os.Getenv("SQC_WALLET_PASS")
		}
		if pass == "" {
			return fmt.Errorf("wallet password required: use --wallet-pass or SQC_WALLET_PASS env var")
		}
		validatorPub, validatorPriv, err = qcrypto.LoadWallet(flagWalletPath, []byte(pass))
		if err != nil {
			return fmt.Errorf("failed to load validator wallet: %w", err)
		}
		validatorAddr = qcrypto.DeriveAddress(validatorPub)
		validatorPKBytes = validatorPub.Bytes()
		log.Info().Str("validator_addr", validatorAddr.String()).Msg("Validator key loaded")
	}

	// Full nodes: pin the trusted genesis validator pubkey so inbound (POST /block)
	// and sync blocks are Dilithium-verified. Without it a full node cannot
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

	// Admin auth token for state-mutating routes (env fallback).
	authToken := flagRPCAuthToken
	if authToken == "" {
		authToken = os.Getenv("SQC_RPC_AUTH_TOKEN")
	}

	// ── P2P peer pool + discovery ──────────────────────────────────────────
	// selfURL is our public-facing RPC address for peer announcements.
	// On a real server this should be the public IP; on localhost it's fine as-is.
	selfURL := "http://localhost" + flagRPCAddr

	peerPool := network.NewPeerPool(selfURL, 50)
	broadcaster := network.NewBroadcaster(validatorPKBytes)
	discoverer := network.NewDiscoverer(peerPool, selfURL, 60*time.Second)

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
	syncer := network.NewSyncer(
		network.DefaultSyncConfig(validatorPKBytes),
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
			ListenAddr:        flagRPCAddr,
			ValidatorPK:       validatorPKBytes,
			PeerCount:         peerPool.Count, // wire live peer count from peerPool
			AuthToken:         authToken,
			X402SettleEnabled: flagEnableX402,
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
	}, broadcaster.AddPeer)

	// Governance + staking routes
	rpc.RegisterGovRoutes(router, stateDB)

	// DEX routes
	rpc.RegisterDEXRoutes(router, stateDB)

	// Contract-interaction routes (/call view calls, /estimate gas+fee) — issue #4
	rpc.RegisterCallRoutes(router, stateDB, executor)

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
	httpServer := &http.Server{
		Addr:         flagRPCAddr,
		Handler:      addCORSHeaders(router),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
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
		Bool("validator", flagIsValidator).
		Msg("Node ready")

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		if ctx.Err() == nil {
			return fmt.Errorf("HTTP server: %w", err)
		}
	}

	log.Info().Msg("Node stopped")
	return nil
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

func addCORSHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Payment")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
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
