// Splendor Quantum Chain — Full Node (v2)
//
// 100% quantum-native blockchain node.
// All crypto: CRYSTALS-Dilithium Mode 3 (NIST FIPS 204) + SHA3-256 (NIST FIPS 202).
// Zero ECDSA. Zero SHA-256. Zero keccak. Zero secp256k1.
//
// Usage:
//   sqc-node --data-dir /var/sqc/data --rpc-addr :8080
//   sqc-node --data-dir /var/sqc/data --rpc-addr :8080 --validator --wallet validator.sqwallet
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/rpc"
)

var (
	flagDataDir       string
	flagRPCAddr       string
	flagMetricsAddr   string
	flagWalletPath    string
	flagWalletPass    string
	flagPeers         []string
	flagBlockInterval int
	flagIsValidator   bool
	flagLogLevel      string
)

func main() {
	root := &cobra.Command{
		Use:   "sqc-node",
		Short: "Splendor Quantum Chain full node",
		Long: `sqc-node runs a Splendor Quantum Chain full node.
Cryptography: CRYSTALS-Dilithium Mode 3 (NIST FIPS 204) + SHA3-256 (NIST FIPS 202).
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

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runNode(cmd *cobra.Command, args []string) error {
	nodeStartTime := time.Now()
	setupLogging(flagLogLevel)

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

	// ── Genesis block (if chain is empty) ──────────────────────────────────
	if flagIsValidator && validatorPriv != nil {
		latestBlock, _ := executor.LatestBlock()
		if latestBlock == nil {
			log.Info().Msg("Chain is empty — creating genesis block")
			stateRoot, _ := stateDB.StateRoot()
			genesis := types.GenesisBlock(validatorAddr, stateRoot, time.Now().Unix())
			genesis.ValidatorPubKey = validatorPKBytes
			if hash, hashErr := genesis.ComputeHash(); hashErr == nil {
				genesis.Hash = hash
				if signErr := genesis.SignBlock(validatorPriv); signErr == nil {
					if execErr := executor.ExecuteBlock(genesis); execErr == nil {
						log.Info().Str("hash", fmt.Sprintf("%x", genesis.Hash[:8])).Msg("Genesis block committed")
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

	// ── Periodic sync (full nodes) ─────────────────────────────────────────
	// Sync every 1 second — realtime sync matching the 2s block interval.
	// RPC node stays within 1-2 blocks of the validator at all times.
	if !flagIsValidator {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
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
			ListenAddr:  flagRPCAddr,
			ValidatorPK: validatorPKBytes,
			PeerCount:   peerPool.Count, // wire live peer count from peerPool
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
		h, _ := executor.LatestHeight(); return h
	}, broadcaster.AddPeer)

	// Governance + staking routes
	rpc.RegisterGovRoutes(router, stateDB)

	// DEX routes
	rpc.RegisterDEXRoutes(router, stateDB)

	// Lending routes
	rpc.RegisterLendingRoutes(router, stateDB)

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
