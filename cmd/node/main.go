// Splendor Quantum Chain — Full Node
// Usage: sqc-node --data-dir /var/sqc --rpc-addr :8080 --wallet /etc/sqc/validator.sqwallet
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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
All cryptography uses CRYSTALS-Dilithium Mode 3 (NIST FIPS 204) and SHA-3-256 (NIST FIPS 202).
No ECDSA. No SHA-256. No conventional cryptography.`,
		RunE: runNode,
	}

	root.Flags().StringVar(&flagDataDir, "data-dir", "./sqc-data", "Directory for chain data and state DB")
	root.Flags().StringVar(&flagRPCAddr, "rpc-addr", ":8080", "RPC server listen address")
	root.Flags().StringVar(&flagWalletPath, "wallet", "", "Path to validator .sqwallet file (required if --validator)")
	root.Flags().StringVar(&flagWalletPass, "wallet-pass", "", "Wallet password (use env SQC_WALLET_PASS for security)")
	root.Flags().StringArrayVar(&flagPeers, "peer", nil, "Peer addresses to connect to (e.g. http://1.2.3.4:8080)")
	root.Flags().IntVar(&flagBlockInterval, "block-interval", 2, "Block production interval in seconds")
	root.Flags().BoolVar(&flagIsValidator, "validator", false, "Run as block-producing validator")
	root.Flags().StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runNode(cmd *cobra.Command, args []string) error {
	// Configure logging
	setupLogging(flagLogLevel)

	log.Info().
		Str("chain_id", types.ChainID).
		Str("data_dir", flagDataDir).
		Str("rpc_addr", flagRPCAddr).
		Bool("validator", flagIsValidator).
		Msg("Starting Splendor Quantum Chain node")

	// ── Open state database ──────────────────────────────────────────────
	stateDB, err := state.NewStateDB(flagDataDir + "/state")
	if err != nil {
		return fmt.Errorf("failed to open state DB: %w", err)
	}
	defer stateDB.Close()

	// ── Open chain database ──────────────────────────────────────────────
	executor, err := block.NewExecutor(stateDB, flagDataDir+"/chain")
	if err != nil {
		return fmt.Errorf("failed to open chain DB: %w", err)
	}
	defer executor.Close()

	// ── Mempool ──────────────────────────────────────────────────────────
	pool := mempool.New(mempool.DefaultMaxSize, stateDB)

	// ── AI Scheduler ─────────────────────────────────────────────────────
	scheduler := ai.NewScheduler()
	scheduler.StartObservationLoop(func() ai.Metrics {
		return ai.Metrics{
			MempoolDepth:   pool.Size(),
			CPULoadPercent: getCPULoad(),
		}
	})

	// ── Load validator key ───────────────────────────────────────────────
	var validatorPub *qcrypto.PublicKey
	var validatorPriv *qcrypto.PrivateKey
	var validatorAddr qcrypto.Address

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
		log.Info().Str("validator_addr", validatorAddr.String()).Msg("Validator key loaded")
	}

	// ── P2P Broadcaster ──────────────────────────────────────────────────
	var validatorPKBytes []byte
	if validatorPub != nil {
		validatorPKBytes = validatorPub.Bytes()
	}
	broadcaster := network.NewBroadcaster(validatorPKBytes)
	for _, peer := range flagPeers {
		broadcaster.AddPeer(peer)
	}

	// ── RPC Server ───────────────────────────────────────────────────────
	rpcServer := rpc.New(
		rpc.Config{
			ListenAddr:  flagRPCAddr,
			ValidatorPK: validatorPKBytes,
		},
		stateDB,
		executor,
		pool,
		broadcaster,
		scheduler,
	)

	// ── Context + signal handling ─────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Info().Msg("Shutdown signal received")
		cancel()
		rpcServer.Stop()
	}()

	// ── Block subscriber channel ──────────────────────────────────────────
	blockCh := make(chan *types.Block, 64)
	go broadcaster.RunBroadcastLoop(blockCh)

	// ── Validator (optional) ──────────────────────────────────────────────
	if flagIsValidator && validatorPriv != nil {
		vCfg := consensus.Config{
			BlockInterval: time.Duration(flagBlockInterval) * time.Second,
			MaxTxPerBlock: block.MaxTxPerBlock,
			Address:       validatorAddr,
			PublicKey:     validatorPKBytes,
			PrivateKey:    validatorPriv,
		}
		v := consensus.New(vCfg, executor, pool, scheduler)
		v.Subscribe(blockCh)

		go func() {
			if err := v.Run(ctx); err != nil {
				log.Error().Err(err).Msg("Validator exited with error")
			}
		}()
	}

	// ── Genesis block (if chain is empty) ────────────────────────────────
	height, _ := executor.LatestHeight()
	if height == 0 && validatorPub != nil {
		log.Info().Msg("Chain is empty — producing genesis block")
		stateRoot, _ := stateDB.StateRoot()
		genesis := types.GenesisBlock(validatorAddr, stateRoot, time.Now().Unix())
		hash, err := genesis.ComputeHash()
		if err == nil {
			genesis.Hash = hash
			if err := genesis.SignBlock(validatorPriv); err == nil {
				if err := executor.ExecuteBlock(genesis); err != nil {
					log.Warn().Err(err).Msg("Genesis block execution skipped (chain already has state)")
				} else {
					log.Info().Str("hash", fmt.Sprintf("%x", hash[:8])).Msg("Genesis block committed")
				}
			}
		}
	}

	// ── Start RPC server ─────────────────────────────────────────────────
	log.Info().Str("addr", flagRPCAddr).Msg("Node ready")
	if err := rpcServer.Start(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("RPC server error: %w", err)
	}

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

// getCPULoad returns a rough CPU utilisation estimate.
// In production, use github.com/shirou/gopsutil for accurate readings.
// This stub returns 0 (no throttling) by default.
func getCPULoad() float64 {
	return 0.0
}
