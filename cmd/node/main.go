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
	"errors"
	"fmt"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
	"github.com/splendor-protocol/splendor-quantum-blockchain/rpc"
)

var (
	flagDataDir            string
	flagRPCAddr            string
	flagMetricsAddr        string
	flagWalletPath         string
	flagWalletPass         string
	flagWalletPassFile     string
	flagNodeKeyPassFile    string
	flagPeers              []string
	flagBlockInterval      int
	flagRoundTimeout       time.Duration
	flagVoteTimeout        time.Duration
	flagIsValidator        bool
	flagLogLevel           string
	flagGenesisPath        string
	flagValidatorPKHex     string
	flagGenesisHashHex     string
	flagGenesisDocHashHex  string
	flagTrustPeerGenesis   bool
	flagAllowForkExtension bool
	flagChainID            string
	flagTLSRequireHybrid   bool
	flagRPCAuthToken       string
	flagAllowPrivatePeers  bool
	flagCORSOrigins        []string
	flagRateLimit          float64
	flagRateBurst          float64
	flagTrustProxy         bool
	flagSimRate            float64
	flagSimBurst           float64
	flagSimMaxInFlight     int
	flagSimTimeout         time.Duration
	flagP2PMaxInFlight     int
	flagTLSCert            string
	flagTLSKey             string
	flagPublicURL          string
	flagAllowPlaintextP2P  bool
	flagPeerPins           []string
	flagRequirePeerPins    bool
	flagStateSync          bool
	flagClearVoteJournal   bool
	flagClearVoteJournalAt uint64
	flagVoteOnly           bool
	flagObjectCodec        string
)

func main() {
	root := &cobra.Command{
		Use:   "sqc-node",
		Short: "Splendor Quantum Chain full node",
		Long: `sqc-node runs a Splendor Quantum Chain full node.
Cryptography: ML-DSA-65 (FIPS-204) + SHA3-256.
No ECDSA. No SHA-256. No conventional cryptography.`,
		RunE: runNode,
		// Explicit, because adding the genesis-hash subcommand below would
		// otherwise flip cobra's default and turn a stray positional argument
		// from "ignored" into "unknown command" for every running deployment.
		Args: cobra.ArbitraryArgs,
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
	root.Flags().StringVar(&flagNodeKeyPassFile, "node-key-pass-file", "", "Encrypt the persisted P2P node identity key with the passphrase in this file (also SQC_NODE_KEY_PASS). Without it the key is stored unencrypted at mode 0600.")
	root.Flags().StringArrayVar(&flagPeers, "peer", nil, "Bootstrap peer RPC addresses")
	root.Flags().IntVar(&flagBlockInterval, "block-interval", 2, "Block interval in seconds (validator only)")
	root.Flags().DurationVar(&flagRoundTimeout, "round-timeout", consensus.DefaultRoundTimeout,
		"How long a BFT view waits before rotating the leader away. Raise it for validators "+
			"spread across regions: the round-0 leader must receive the previous block, build, "+
			"sign and get its proposal back across the link inside this window, or its peers "+
			"time out and discard the proposal when it arrives (validator only)")
	root.Flags().DurationVar(&flagVoteTimeout, "vote-timeout", consensus.DefaultVoteTimeout,
		"BFT vote timeout (validator only)")
	root.Flags().BoolVar(&flagIsValidator, "validator", false, "Run as block-producing validator")
	root.Flags().StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	root.Flags().StringVar(&flagGenesisPath, "genesis", "", "Path to genesis.json (allocations applied once on an empty chain)")
	root.Flags().StringVar(&flagValidatorPKHex, "validator-pubkey-hex", "", "Trusted genesis validator ML-DSA-65 public key (hex). Full nodes MUST set this to authenticate pushed blocks on POST /block.")
	root.Flags().StringVar(&flagGenesisHashHex, "genesis-hash", "", "Pinned hash of block 0 (hex). Required to bootstrap the validator pubkey from peers when --validator-pubkey-hex is not supplied; without one, a node would trust whatever key its first peer serves.")
	root.Flags().StringVar(&flagGenesisDocHashHex, "genesis-doc-hash", "", "Pinned hash of the genesis DOCUMENT (hex) — print it with 'sqc-node genesis-hash <genesis.json>'. A node that has no local --genesis credits a peer-served genesis.json only when it hashes to this. Distinct from --genesis-hash, which pins block 0's header.")
	root.Flags().StringVar(&flagChainID, "chain-id", "", "Chain id this node is expected to be on (e.g. splendor-quantum-devnet-1). When set, a genesis document naming a different chain is refused instead of adopted. Leave empty to take whatever the verified genesis says. (or SQC_CHAIN_ID env)")
	root.Flags().BoolVar(&flagTrustPeerGenesis, "trust-peer-genesis", false, "Credit a peer-served genesis.json with no --genesis-doc-hash pin. The peer can then mint any balance it likes into this node's state. Lab networks only.")
	root.Flags().BoolVar(&flagAllowForkExtension, "allow-fork-extension", false, "Adopt upgrades that --genesis schedules but this chain was not started under, when they activate above the current head. Adopting a rule is a network-wide decision: run this only when every validator is on the same genesis document, or this node forks at the activation height.")
	root.Flags().StringVar(&flagRPCAuthToken, "rpc-auth-token", "", "Shared secret for the write plane: POST /block and POST /peers/announce. All nodes in a cluster must share it. (or SQC_RPC_AUTH_TOKEN env)")
	root.Flags().BoolVar(&flagAllowPrivatePeers, "allow-private-peers", false, "Allow loopback/private-IP peers in discovery + /peers/announce (private-network or local clusters; default off = public SSRF-safe)")
	root.Flags().StringArrayVar(&flagCORSOrigins, "cors-origin", nil, "Browser origin allowed to call this node (repeatable). Default: none. Use '*' to allow any origin.")
	root.Flags().Float64Var(&flagRateLimit, "rpc-rate-limit", 25, "Sustained requests/second per client IP on tx submission and x402 routes (0 disables)")
	root.Flags().Float64Var(&flagRateBurst, "rpc-rate-burst", 50, "Burst allowance per client IP for rate-limited routes")
	root.Flags().BoolVar(&flagTrustProxy, "trust-proxy", false, "Read client IP from X-Forwarded-For. ONLY enable behind a proxy you control — otherwise clients can spoof it and bypass rate limits.")
	root.Flags().Float64Var(&flagSimRate, "sim-rate-limit", 0, "Sustained requests/second per client IP on VM-executing routes (/call, /estimate, /logs). 0 uses the built-in default.")
	root.Flags().Float64Var(&flagSimBurst, "sim-rate-burst", 0, "Burst allowance for VM-executing routes. 0 uses the built-in default.")
	root.Flags().IntVar(&flagSimMaxInFlight, "sim-max-inflight", 0, "Maximum concurrent VM simulations across all callers; excess requests get 503. This, not the rate limit, bounds how much CPU anonymous callers can occupy. 0 uses the built-in default.")
	root.Flags().DurationVar(&flagSimTimeout, "sim-timeout", 0, "Maximum wall time a single simulation may hold a slot. 0 uses the built-in default.")
	root.Flags().IntVar(&flagP2PMaxInFlight, "p2p-max-inflight", 0, "Maximum concurrent sealed P2P requests being read and decrypted; excess requests get 503. The write token sits inside the envelope, so this is what bounds unauthenticated decryption work. 0 uses the built-in default.")
	root.Flags().StringVar(&flagTLSCert, "tls-cert", "", "PEM certificate for HTTPS. Enables TLS 1.3 and OFFERS post-quantum hybrid key exchange (X25519MLKEM768), falling back to classical X25519 for clients that cannot do it — see --tls-require-hybrid.")
	root.Flags().BoolVar(&flagTLSRequireHybrid, "tls-require-hybrid", false, "Refuse TLS connections that cannot negotiate X25519MLKEM768, instead of falling back to classical X25519. Set this before describing the transport as post-quantum: without it a deployment can offer the hybrid group and still serve nothing but classical.")
	root.Flags().StringVar(&flagTLSKey, "tls-key", "", "PEM private key matching --tls-cert")
	root.Flags().StringVar(&flagPublicURL, "public-url", "", "Externally reachable base URL advertised to peers (default: derived from --rpc-addr)")
	root.Flags().BoolVar(&flagAllowPlaintextP2P, "allow-plaintext-p2p", false, "Accept AND send cleartext peer traffic: registers the plaintext POST /block and /peers/announce routes and permits outbound fallback when a peer serves no sealed transport. Off by default, so the sealed ML-KEM channel is the only way in or out.")
	root.Flags().StringArrayVar(&flagPeerPins, "peer-pin", nil, "Pin a peer's node identity as URL=PUBKEY_HEX (repeatable). Without a pin, peer identities are trusted on first use — set --require-peer-pins to refuse unpinned peers instead.")
	root.Flags().BoolVar(&flagRequirePeerPins, "require-peer-pins", false, "Refuse to talk to any peer that has no --peer-pin, instead of trusting its identity on first contact. Governs who this node dials — block push, announce and catch-up pull; inbound blocks stay authenticated by the validator signature and the write-plane token. Recommended once a cluster's peers are known: trust-on-first-use hands the relationship to whoever answers first at bootstrap.")
	root.Flags().BoolVar(&flagStateSync, "state-sync", false, "Join a chain whose history cannot be replayed. On an empty data dir: take the fork schedule, chain id and minted supply from the pinned genesis document (--genesis, or --peer with --genesis-doc-hash), then import a peer's live state snapshot — its head block authenticated by the genesis validator key — and follow from the next block. Needs --validator-pubkey-hex or --genesis-hash. A --validator may use it too, and needs at least one --peer: it holds production until the snapshot has landed and it has caught up.")
	root.Flags().BoolVar(&flagClearVoteJournal, "clear-vote-journal", false, "Backup and clear dataDir/vote-journal.json at startup. Ops recovery for a split durable vote-lock after peers are healthy; coordinate across conflicting validators. Misuse can enable conflicting votes if a prior vote already left the node.")
	root.Flags().Uint64Var(&flagClearVoteJournalAt, "clear-vote-journal-at-height", 0, "Like --clear-vote-journal, but only if the journal's last vote is at this height; refuse to start if it holds a different height (0 disables).")
	root.Flags().BoolVar(&flagVoteOnly, "vote-only", false, "Participate in BFT voting but never propose or seal. Ops recovery after unlocking a split vote-journal at set≥3: start non-leader validators with this so they cannot race a divergent next-height proposal while the scheduled leader cannot solo-seal. Remove once tip advances.")
	root.Flags().StringVar(&flagObjectCodec, "object-codec", "", "Optional coded-object live path (uncoded-v1, erasure-rs-v1, or rlnc-gf256-v1). Empty keeps the default JSON/chunked snapshot and block sync. When set, this node may serve get_object_manifest_v1 / get_object_symbol_v1 over the sealed channel; it does not replace snapshot sync.")

	// Operators need a way to produce the value they pin, and to check that a
	// genesis.json they were handed is the one they expect, without starting a node.
	root.AddCommand(&cobra.Command{
		Use:   "genesis-hash <genesis.json>",
		Short: "Print the canonical hash of a genesis.json (the --genesis-doc-hash pin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := genesiscfg.LoadFile(args[0])
			if err != nil {
				return err
			}
			if err := g.Validate(); err != nil {
				return err
			}
			fmt.Println(g.HashHex())
			return nil
		},
	})

	root.AddCommand(newExportAllocationsCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newExportAllocationsCmd turns the account balances of an existing chain into
// a genesis allocation list, for re-minting a chain whose history cannot be
// replayed.
//
// Only balances cross over, and that is the point rather than a shortcoming. A
// genesis document can express nothing else, so the export cannot smuggle
// contract storage into the new chain — which matters here, because the leaf
// that made the earlier chains unfollowable was contract storage written outside
// a block. Copying state wholesale would carry that artifact into the very chain
// created to be rid of it.
//
// The same limit is a real loss elsewhere: deployed contracts, their storage,
// and every account nonce are dropped. Dropped nonces are safe only because the
// chain id is signed into every transaction, so an old signed transaction cannot
// be replayed onto a chain minted under a different id — give the new chain a
// new id, or that protection is not there.
func newExportAllocationsCmd() *cobra.Command {
	var minBalance string
	var includeZero bool

	cmd := &cobra.Command{
		Use:   "export-allocations <data-dir>",
		Short: "Export account balances from a stopped chain as a genesis allocation list",
		Long: "Reads committed account state and prints a genesis \"allocations\" array on stdout.\n\n" +
			"Run this against a STOPPED node. Reading a running one gives a torn view spanning\n" +
			"whatever block was in flight. Contract code, contract storage and account nonces are\n" +
			"NOT exported — a genesis document cannot express them.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			floor := new(big.Int)
			if minBalance != "" {
				if _, ok := floor.SetString(minBalance, 10); !ok {
					return fmt.Errorf("--min-balance %q is not a base-10 integer", minBalance)
				}
			}

			// Same value the node is given, so an operator does not have to know
			// that state lives in a subdirectory.
			statePath := filepath.Join(args[0], "state")

			// NewStateDB creates a database when the path is empty, so a mistyped
			// data directory would otherwise produce a clean, confident, entirely
			// empty allocation list. This tool's output becomes a chain's money
			// supply; the one failure it must never have is looking successful.
			if fi, err := os.Stat(statePath); err != nil || !fi.IsDir() {
				return fmt.Errorf(
					"no chain state at %s — pass the same --data-dir the node was given, "+
						"not the state directory itself", statePath)
			}

			stateDB, err := state.NewStateDB(statePath)
			if err != nil {
				return fmt.Errorf("open state at %q: %w", statePath, err)
			}
			defer stateDB.Close()

			type alloc struct {
				Address string `json:"address"`
				Balance string `json:"balance"`
			}
			var out []alloc
			total := new(big.Int)
			var skipped int

			err = stateDB.ForEachAccount(func(acc *types.Account) error {
				bal := acc.Balance
				if bal == nil {
					bal = new(big.Int)
				}
				// A zero-balance account is a genesis entry that funds nothing. It
				// still costs a line in the document operators have to review, and
				// review is the only thing standing between a bad allocation list
				// and a wrong money supply.
				if !includeZero && bal.Sign() == 0 {
					skipped++
					return nil
				}
				if bal.Cmp(floor) < 0 {
					skipped++
					return nil
				}
				out = append(out, alloc{Address: acc.Address.String(), Balance: bal.String()})
				total.Add(total, bal)
				return nil
			})
			if err != nil {
				return err
			}
			if len(out) == 0 {
				return fmt.Errorf(
					"%s holds no funded accounts — refusing to emit an empty allocation list, "+
						"which would mint a chain with no supply", statePath)
			}

			// Allocation ORDER is part of the genesis identity, because ApplyToState
			// overwrites an account it has already written rather than adding to it.
			// Sorting makes the document — and therefore the hash operators pin —
			// reproducible from the same chain rather than dependent on scan order.
			sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(out); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "exported %d allocations, total %s (skipped %d)\n",
				len(out), total.String(), skipped)
			fmt.Fprintf(os.Stderr,
				"NOT exported: contract code, contract storage, account nonces. "+
					"Give the new chain a distinct chain id — dropped nonces are only safe "+
					"because the chain id is signed into every transaction.\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&minBalance, "min-balance", "",
		"Skip accounts below this balance in smallest SPLD units (dust left by test traffic)")
	cmd.Flags().BoolVar(&includeZero, "include-zero", false,
		"Include zero-balance accounts, which fund nothing but must still be reviewed")
	return cmd
}

func runNode(cmd *cobra.Command, args []string) error {
	nodeStartTime := time.Now()
	setupLogging(flagLogLevel)

	// Opt into private/loopback peers for private-network or local multi-node clusters.
	network.AllowPrivatePeers = flagAllowPrivatePeers

	// The key-file permission check is a no-op where POSIX mode bits do not
	// describe access, so say so rather than let its silence read as a pass.
	if !qcrypto.KeyFilePermissionsEnforced() {
		log.Warn().Str("os", runtime.GOOS).Msg(
			"Key file permission checks do not run on this platform: POSIX mode bits do not " +
				"describe its ACLs. Restrict the wallet and node identity files to this user " +
				"account yourself — the node cannot verify it.")
	}

	if (flagTLSCert == "") != (flagTLSKey == "") {
		return fmt.Errorf("--tls-cert and --tls-key must be provided together")
	}
	tlsEnabled := flagTLSCert != ""

	// --require-peer-pins demands a verified ML-DSA-65 identity for every peer;
	// --allow-plaintext-p2p permits a channel that carries no identity at all,
	// which no pin can ever match. Taken together the fallback would readmit
	// exactly the peers the pin requirement just refused, so refuse the pair
	// here instead of shipping a guarantee that quietly does not hold.
	if flagRequirePeerPins && flagAllowPlaintextP2P {
		return fmt.Errorf("--require-peer-pins cannot be combined with --allow-plaintext-p2p: " +
			"the plaintext fallback carries no peer identity, so it would readmit the unpinned " +
			"peers that the pin requirement exists to refuse")
	}
	if flagObjectCodec != "" {
		if _, err := coding.Resolve(flagObjectCodec); err != nil {
			return fmt.Errorf("--object-codec: %w", err)
		}
	}

	log.Info().
		Str("chain_id", types.ChainID).
		Str("commit", buildinfo.ShortCommit()).
		Str("image_tag", buildinfo.ImageTag).
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

	// Attach the tx index before snapshot reconcile so a completed import
	// can mark coverage (R6). Receipts are not consensus-critical.
	txIndex, txErr := state.NewTxIndexStore(flagDataDir + "/txindex")
	if txErr != nil {
		log.Warn().Err(txErr).Msg("TX index unavailable — receipts will not be stored")
	} else {
		executor.WithTxIndex(txIndex)
		defer txIndex.Close()
	}

	// A prior --state-sync import that was interrupted mid-flight leaves a
	// recovery marker on disk; resolve it before anything else touches the
	// chain or state DB so the node never runs consensus on a half-imported
	// snapshot. A retryable incomplete import is not fatal when --state-sync
	// is set: the retry path below can resume. See core/block/state_snapshot.go.
	if err := reconcileSnapshotImportAtStartup(executor, flagStateSync); err != nil {
		return err
	}

	// ── Hook registry ──────────────────────────────────────────────────────
	hooks := block.NewHookRegistry()
	hooks.OnPreTx("min_fee", func(tx *types.Transaction, height uint64) error {
		min := int64(mempool.MinFee)
		if executor.Forks().Active(forks.MinFeeV1, height) {
			min = mempool.MinFeeV1Amount
		}
		return block.MinFeeHook(min)(tx, height)
	})
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
	nodeMetrics.SetLabel("version", buildinfo.Version())
	// Failed executions are counted where the executor settles a block, so
	// sqc_txs_failed_total moves alongside sqc_txs_processed_total (driven
	// from blockCh below) instead of staying at zero.
	executor.WithTxFailedHook(func(n int) { nodeMetrics.IncrTxsFailed(uint64(n)) })
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

	// trustedPK is the genesis validator key this node authenticates blocks
	// with on every ingress (push, pull-sync, proposals): --validator-pubkey-hex
	// when set — on a validator joining a running chain its own key is NOT the
	// chain's trust anchor — else a validator's own key (the producer that
	// minted the chain), else nothing until a pinned genesis block supplies
	// one. Blocks sealed by other registered validators are authenticated
	// against the active set (network.VerifyBlockProducer).
	trustedPK, err := resolveTrustedProducerKey(flagValidatorPKHex, flagIsValidator, validatorPKBytes)
	if err != nil {
		return err
	}
	if !flagIsValidator && len(trustedPK) > 0 {
		// Full nodes report the pinned key as /status.local_validator, as before.
		validatorPKBytes = trustedPK
		log.Info().Str("pubkey_prefix", fmt.Sprintf("%x", trustedPK[:8])).
			Msg("Trusted validator pubkey pinned (full node block verification enabled)")
	} else if flagIsValidator && flagValidatorPKHex != "" {
		log.Info().Str("pubkey_prefix", fmt.Sprintf("%x", trustedPK[:8])).
			Msg("Trusted genesis validator pubkey pinned: blocks are authenticated against it and the active set, not this node's own key")
	}

	// Soft warn early: a joiner that omitted --validator-pubkey-hex will reject
	// the real producer. Hard errors for known join roles land after genesis
	// role detection below (and via checkJoinerValidatorPubkey with the doc).
	if warn, err := checkJoinerValidatorPubkey(flagValidatorPKHex, flagIsValidator, false, flagPeers, nil, validatorPKBytes); err != nil {
		return err
	} else if warn != "" {
		log.Warn().Msg(strings.TrimSpace(warn))
	}

	// Optional genesis pin. This is what lets a node with no pinned validator
	// key learn one safely: block 0 is authenticated by hash before the key it
	// carries is trusted for every subsequent block.
	genesisHashPin, err := parsePinnedHash("--genesis-hash", flagGenesisHashHex)
	if err != nil {
		return err
	}
	if genesisHashPin != nil {
		// The same pin anchors the bottom of the chain locally: block 1 applied
		// to an empty chain has no stored parent, so without this it could be
		// built on any genesis at all.
		executor.WithGenesisHash(*genesisHashPin)
		log.Info().Str("genesis_hash", fmt.Sprintf("%x", genesisHashPin[:8])).Msg("Genesis hash pinned")
	}

	// The genesis DOCUMENT pin is a different object from the one above:
	// --genesis-hash covers block 0's header, this covers the genesis.json whose
	// allocations become spendable balances the instant they are applied.
	genesisDocPin, err := parsePinnedHash("--genesis-doc-hash", flagGenesisDocHashHex)
	if err != nil {
		return err
	}
	if genesisDocPin != nil {
		log.Info().Str("genesis_doc_hash", fmt.Sprintf("%x", genesisDocPin[:8])).Msg("Genesis document hash pinned")
	}

	// The rule-change schedule is read on every start, not only on the run that
	// applies genesis: a node restarting onto an existing chain still has to know
	// which rules that chain switches to and when. It is installed on the
	// executor here, before crash recovery below, so a block replayed by
	// ReconcileState is gated exactly as it was when first applied.
	headBlock, _ := executor.LatestBlock()
	var headHeight uint64
	if headBlock != nil {
		headHeight = headBlock.Header.Height
	}
	forkSchedule, err := loadForkSchedule(flagGenesisPath, stateDB, headBlock == nil, forkPolicy{
		Head:           headHeight,
		AllowExtension: flagAllowForkExtension,
		DocHash:        genesisDocPin,
	})
	if err != nil {
		return err
	}
	executor.WithForks(forkSchedule)
	for _, name := range forkSchedule.Names() {
		at, _ := forkSchedule.At(name)
		log.Info().Str("upgrade", name).Uint64("at_height", at).Msg("Scheduled consensus rule change")
	}

	// Chain id assertion (env fallback), so a container can be told which network
	// it is on without editing its command line.
	if flagChainID == "" {
		flagChainID = os.Getenv("SQC_CHAIN_ID")
	}
	if flagChainID != "" {
		if err := types.ValidateChainID(flagChainID); err != nil {
			return fmt.Errorf("--chain-id: %w", err)
		}
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
	broadcaster := network.NewBroadcaster(trustedPK, authToken, flagAllowPlaintextP2P)
	discoverer := network.NewDiscoverer(peerPool, selfURL, 60*time.Second)

	// ── Node identity for the encrypted P2P transport ──────────────────────
	// A validator reuses its wallet key; other nodes get a persistent identity
	// so peers that pinned them on first contact keep matching across restarts.
	nodeIdentity, err := loadNodeIdentity(flagDataDir, validatorPub, validatorPriv)
	if err != nil {
		return fmt.Errorf("node identity: %w", err)
	}
	// Announcements are signed with the same key peers pin for the sealed
	// transport, so a peer entry is attributable to one node instead of to
	// anyone holding the shared write-plane token.
	discoverer.SetIdentity(nodeIdentity)

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
		if perr := broadcaster.Identities().Pin(peerURL, pk); perr != nil {
			return fmt.Errorf("invalid --peer-pin for %s: %w", peerURL, perr)
		}
		log.Info().Str("peer", peerURL).Msg("pinned peer node identity")
	}

	// Enforcement itself lives in the identity cache, which every sealed path
	// (block push, announce, catch-up pull) funnels through — the flag only
	// switches the policy on. Default off, so a node with no peers, or one still
	// bootstrapping a cluster, behaves exactly as before.
	if flagRequirePeerPins {
		broadcaster.Identities().RequirePins(true)
		log.Info().Int("pinned_peers", len(flagPeerPins)).
			Msg("Peer identity pins REQUIRED: peers without a --peer-pin will be refused, not trusted on first use")
		if len(flagPeerPins) == 0 && len(flagPeers) > 0 {
			log.Warn().Msg("--require-peer-pins is set but no --peer-pin was supplied: " +
				"every configured --peer will be refused")
		}
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
	syncCfg := network.DefaultSyncConfig(trustedPK)
	syncCfg.Identities = broadcaster.Identities()
	syncCfg.AllowPlaintext = flagAllowPlaintextP2P
	syncCfg.SnapshotStagingDir = filepath.Join(flagDataDir, "chain", "snapshot_download")
	syncCfg.AdoptSnapshotDownload = executor.ImportSnapshotDownload
	// Under a rotating BFT leader most blocks are sealed by a registered
	// validator other than the genesis one; the registry is the second key
	// ring. ExecuteBlock still verifies the certificate.
	syncCfg.LookupProducerKey = executor.ActiveValidatorKey
	if genesisHashPin != nil {
		syncCfg.GenesisHash = *genesisHashPin
	}
	if len(trustedPK) == 0 && genesisHashPin == nil {
		log.Warn().Msg("Neither --validator-pubkey-hex nor --genesis-hash is set: this node cannot " +
			"authenticate blocks from peers and will not sync. Pin one of them.")
	}

	syncer := network.NewSyncer(
		syncCfg,
		peerPool,
		func() uint64 { h, _ := executor.LatestHeight(); return h },
		func(b *types.Block) error { return executor.ExecuteBlock(b) },
	)
	// Below two registered validators the executor accepts a block from the
	// unregistered genesis validator; it must know WHICH key that is rather
	// than trust the one the block carries. Block 0 in the chain DB is
	// consulted first; a joiner that never stored it uses the trusted key,
	// read at call time because a --genesis-hash pin learns it from block 0
	// after start-up.
	executor.WithGenesisValidatorKey(syncer.TrustedPubKey)

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

	// ── Chain identity ─────────────────────────────────────────────────────
	// Settle which network this is BEFORE anything reads types.ChainID. On an
	// empty chain the genesis document decides (applyLocalGenesis, below); on a
	// chain that already has blocks the blocks themselves decide, because they
	// were sealed under an id and the executor checks every header against it.
	// Without this a restarted node would silently fall back to the compiled
	// default and then reject its own history as "wrong chain id".
	//
	// It runs BEFORE crash recovery deliberately. ReconcileState replays the
	// blocks a crash left behind, and replay checks each header's chain id
	// against types.ChainID — so recovering first made a node on a non-default
	// chain validate its own stored blocks against the compiled default and
	// reject them. Safe in this order: adoption reads only the chain DB, and on
	// an empty chain (where the document decides instead) recovery is a no-op.
	if err := establishChainIDFromChain(executor); err != nil {
		return err
	}

	// ── Crash recovery ─────────────────────────────────────────────────────
	// The chain head advances (atomically) before state is committed, so a crash
	// can leave the head ahead of the state. Replay any such blocks before we
	// serve or seal. No-op on a fresh DB or a cleanly-shut-down node.
	if executor.HasIncompleteSnapshotImport() {
		log.Warn().Msg("skipping ReconcileState until snapshot import completes; the snapshot head is not a replayable chain prefix")
	} else if err := executor.ReconcileState(); err != nil {
		return fmt.Errorf("state reconciliation failed: %w", err)
	} else {
		// The tx-index walk is not a precondition for serving or sealing: it
		// only moves /readyz, and on a node upgraded from a build with no
		// index watermark it reads the whole chain once. It runs beside the
		// node rather than in front of it.
		indexWalkStarted := time.Now()
		executor.StartTxIndexReconcile(func(err error) {
			if err != nil {
				log.Warn().Err(err).Dur("took", time.Since(indexWalkStarted)).
					Msg("tx index backfill failed; /readyz will stay not-ready until it catches up")
				return
			}
			log.Info().Dur("took", time.Since(indexWalkStarted)).Msg("tx index reconciled")
		})
	}

	// ── Genesis (if chain is empty) ────────────────────────────────────────
	// stateSyncPending holds the periodic block-sync loop off until a
	// --state-sync import has landed: with an empty state, block 1 would be
	// fetched, fail on state root, and be retried forever.
	var stateSyncPending atomic.Bool
	// joiningValidator is a validator that was started against a chain it did
	// not mint. It must take a block from the network before it produces one,
	// and it pull-syncs on every path — see the Ready gate and pullsBlocks.
	var joiningValidator bool
	latestBlock, _ := executor.LatestBlock()
	if shouldRunStateSync(flagStateSync, latestBlock, executor.HasIncompleteSnapshotImport()) {
		if err := validateStateSyncFlags(flagIsValidator, trustedPK, genesisHashPin, flagGenesisPath, flagPeers); err != nil {
			return err
		}
		deps := stateSyncDeps{
			peers:    flagPeers,
			policy:   genesisPolicy{docHash: genesisDocPin, trustPeer: flagTrustPeerGenesis},
			stateDB:  stateDB,
			executor: executor,
			syncer:   syncer,
			retry:    syncCfg.RetryDelay,
		}
		if latestBlock != nil {
			// A previous attempt already stored the snapshot head (and the
			// genesis bookkeeping). Do not re-prepare; Import resumes state.
			deps.genesisPrepared = true
		} else if flagGenesisPath != "" {
			g, err := genesiscfg.LoadFile(flagGenesisPath)
			if err != nil {
				return fmt.Errorf("load genesis %q: %w", flagGenesisPath, err)
			}
			if err := prepareStateSyncGenesis(g, genesisDocPin, flagGenesisPath, stateDB, executor); err != nil {
				return err
			}
			deps.genesisPrepared = true
		}
		stateSyncPending.Store(true)
		// A state-syncing validator is a joiner by definition: the chain it is
		// about to adopt is someone else's, and it must not seal anything until
		// the snapshot has landed and it has caught up.
		joiningValidator = flagIsValidator
		log.Info().Msg("--state-sync: genesis allocations are not applied here; waiting for a peer's state snapshot")
		go func() {
			if err := runStateSync(ctx, deps); err != nil {
				if ctx.Err() == nil {
					log.Error().Err(err).Msg("state sync failed: this node cannot follow the chain — fix the configuration and restart")
					cancel()
				}
				return
			}
			stateSyncPending.Store(false)
		}()
	} else if latestBlock == nil {
		// Credit genesis allocations before block 0 so every node — validator
		// and sync — starts from the same funded state root. Without this the
		// total_supply declared in genesis.json is never minted, the chain has
		// zero balance, and no transaction can meet the minimum fee.
		var genesisDoc *genesiscfg.Genesis
		if flagGenesisPath != "" {
			var genErr error
			genesisDoc, genErr = applyLocalGenesis(flagGenesisPath, stateDB, executor, genesisPolicy{docHash: genesisDocPin})
			if genErr != nil {
				return genErr
			}
		}

		// The validator either starts the chain or joins one that already
		// exists. Minting block 0 under a key the genesis document does not
		// name would start a SECOND chain under the same document — a network
		// nobody else can join, discovered later — so that is refused when
		// there is no peer to take the real chain from, and turned into a join
		// when there is.
		if flagIsValidator && validatorPriv != nil {
			role, roleErr := validatorStartRole(genesisDoc, validatorPKBytes, flagPeers)
			if roleErr != nil {
				return roleErr
			}
			joiningValidator = role == roleJoin
			if joiningValidator {
				log.Info().Strs("peers", flagPeers).Msg(
					"Chain is empty and this validator is not the one the genesis document names: " +
						"joining the running chain instead of minting block 0 — it will pull-sync " +
						"first and produce only once it has a head and has caught up")
			}
			if _, err := checkJoinerValidatorPubkey(flagValidatorPKHex, true, joiningValidator, flagPeers, genesisDoc, validatorPKBytes); err != nil {
				return err
			}
		}

		if flagIsValidator && validatorPriv != nil && !joiningValidator {
			log.Info().Msg("Chain is empty — creating genesis block")
			stateRoot, _ := stateDB.StateRoot()

			// Block 0's timestamp comes from the document, not the clock. Two nodes
			// minting from the same genesis then produce the SAME block 0, so its
			// hash is a property of the document rather than of when someone
			// happened to start the process — which is what lets --genesis-hash be
			// published once instead of re-read after every reset.
			genesisTime := time.Now().Unix()
			if genesisDoc != nil && genesisDoc.Timestamp != 0 {
				genesisTime = genesisDoc.Timestamp
			}
			genesisBlock := types.GenesisBlock(validatorAddr, stateRoot, genesisTime)
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
	// producer is nil on a full node. On a validator it runs the single path
	// until the block about to be sealed falls under multi_validator, then the
	// BFT path — in-process, at the activation height, with the same head.
	// The path used to be chosen once at boot, so a producer that booted
	// below the activation stayed single-path and stopped with
	// ErrQuorumRequired when the height arrived with two validators
	// registered, until it was restarted.
	var producer *consensus.Producer
	// readyToPropose gates block production on both paths. A validator that
	// joined someone else's chain additionally has to have a stored head:
	// IsSynced answers "synced" while no peer height is known, so without this
	// a joiner would seal its own block 1 over genesis in its first second.
	readyToPropose := syncer.IsSynced
	if joiningValidator {
		readyToPropose = joiningValidatorReady(executor, syncer.IsSynced)
	}
	readyToPropose = applyStoresReady(executor, readyToPropose)
	readyToPropose = applyVoteOnly(readyToPropose, flagVoteOnly)
	if flagVoteOnly {
		log.Warn().Msg("--vote-only: this validator will vote but never propose; remove the flag once tip advances past the unlocked height")
	}
	// bftEngine reports the engine while the producer is on the BFT path and
	// nil otherwise — read per message, because the answer changes mid-run.
	bftEngine := func() *consensus.BFTEngine {
		if producer == nil {
			return nil
		}
		return producer.Engine()
	}
	// consensusMode is what /status.consensus_mode reports: the path this
	// process is on NOW, derived from the same pointer the ingress reads, so
	// the answer flips at the activation height without a restart — unlike
	// /status.multi_validator, which is the schedule bit.
	consensusMode := func() string {
		switch {
		case producer == nil:
			return rpc.ConsensusModeFollower
		case producer.Engine() != nil:
			return rpc.ConsensusModeBFT
		default:
			return rpc.ConsensusModeSingle
		}
	}
	if flagIsValidator && validatorPriv != nil {
		vCfg := consensus.Config{
			BlockInterval: time.Duration(flagBlockInterval) * time.Second,
			MaxTxPerBlock: block.MaxTxPerBlock,
			Address:       validatorAddr,
			PublicKey:     validatorPKBytes,
			PrivateKey:    validatorPriv,
			// A validator behind its peers votes but does not propose.
			Ready: readyToPropose,
		}
		if gv, ok := genesisValidatorInfo(executor, trustedPK); ok {
			vCfg.GenesisValidator = gv
		} else {
			log.Warn().Msg("No genesis validator known (no block 0 stored, no --validator-pubkey-hex): " +
				"with an empty registry this node would treat itself as the sole BFT leader")
		}
		broadcastCh := make(chan *types.Block, 64)
		go func() {
			for b := range broadcastCh {
				select {
				case blockCh <- b:
				default:
				}
				broadcaster.Broadcast(b)
			}
		}()

		// The last vote this validator signed survives restarts here, so a
		// process that comes back mid-height cannot sign a second, conflicting
		// vote for the same height and round. It lives beside the chain data
		// because it is part of this validator's consensus state.
		voteJournalPath := filepath.Join(flagDataDir, "vote-journal.json")
		if flagClearVoteJournal || flagClearVoteJournalAt > 0 {
			pre, openErr := consensus.OpenVoteJournal(voteJournalPath)
			if openErr != nil {
				return fmt.Errorf("vote journal: %w", openErr)
			}
			h, r, hash, ok := pre.Last()
			var backup string
			var clearErr error
			if flagClearVoteJournalAt > 0 {
				backup, clearErr = pre.ClearIfHeight(flagClearVoteJournalAt)
			} else {
				backup, clearErr = pre.Clear()
			}
			if clearErr != nil {
				return fmt.Errorf("clear vote journal: %w", clearErr)
			}
			ev := log.Warn().Str("backup", backup)
			if ok {
				ev = ev.Uint64("cleared_height", h).Uint32("cleared_round", r).Hex("cleared_hash", hash[:8])
			}
			ev.Msg("OPERATOR: vote journal cleared at startup — only safe after peer mesh is healthy and conflicting validators clear together")
		}
		voteJournal, err := consensus.OpenVoteJournal(voteJournalPath)
		if err != nil {
			return fmt.Errorf("vote journal: %w", err)
		}
		if h, r, _, ok := voteJournal.Last(); ok {
			log.Info().Uint64("height", h).Uint32("round", r).Msg("vote journal: last signed vote restored")
		}

		// The BFT half is configured now and used whenever the producer needs
		// it — at boot when multi_validator is already active for the next
		// block, otherwise at the tick that reaches the activation height.
		producer = consensus.NewProducer(vCfg, consensus.BFTConfig{
			RoundTimeout: flagRoundTimeout,
			VoteTimeout:  flagVoteTimeout,
			LocalAddr:    validatorAddr,
			LocalPubKey:  validatorPKBytes,
			LocalPrivKey: validatorPriv,
			VoteJournal:  voteJournal,
			// The pull-sync loop and the block push race this commit for
			// the same block; if one of them landed it first it is still
			// committed (applyCommittedBlock).
			OnCommit: func(b *types.Block) error {
				return applyCommittedBlock(executor, b)
			},
			CanProgress: executor.CanProgress,
			// A peer's proposal is applied to a discarded pending layer
			// before this node votes on it, so a quorum cannot form on a
			// block this node could never commit.
			ValidateProposal: executor.ValidateProposal,
			RoundsV1: func(h uint64) bool {
				return executor.Forks().Active(forks.BFTRoundsV1, h)
			},
			Broadcast: func(vote *consensus.Vote) {
				data, err := json.Marshal(vote)
				if err == nil {
					broadcaster.BroadcastSealed(network.KindVote, data)
				}
			},
			BroadcastRoundChange: func(rc *consensus.RoundChange) {
				data, err := json.Marshal(rc)
				if err == nil {
					broadcaster.BroadcastSealed(network.KindRoundChange, data)
				}
			},
			// The envelope, not a bare block: it carries the round the
			// proposal was made in and the proposer's own vote for it, so a
			// peer whose round has drifted resynchronises instead of
			// rejecting the leader for ever.
			BroadcastProposal: func(p *consensus.ProposalMsg) {
				data, err := p.Encode()
				if err == nil {
					broadcaster.BroadcastSealed(network.KindProposal, data)
				}
			},
		}, executor, pool, scheduler, stateDB)
		producer.Subscribe(broadcastCh)
		go func() {
			if err := producer.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("Validator exited with error")
			}
		}()

		// Keep the mempool floor aligned with the live fork.
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					h, _ := executor.LatestHeight()
					if executor.Forks().Active(forks.MinFeeV1, h) {
						pool.SetBaseFee(mempool.MinFeeV1Amount)
					} else {
						pool.SetBaseFee(mempool.MinFee)
					}
				}
			}
		}()
	}

	// ── RPC node: bootstrap genesis state from validator peer ──────────────
	// The RPC node starts with empty state. The validator applied genesis.json
	// allocations before creating block 0, so its state root ≠ empty state root.
	// Before syncing blocks, the RPC must fetch and apply the same genesis
	// allocations so its state root matches the validator's.
	// A --state-sync node takes the document without its allocations (above).
	if bootstrapsGenesisFromPeers(flagIsValidator, flagPeers, flagGenesisPath) && !flagStateSync {
		policy := genesisPolicy{docHash: genesisDocPin, trustPeer: flagTrustPeerGenesis}
		go func() {
			time.Sleep(2 * time.Second) // wait for HTTP server to start
			for _, peer := range flagPeers {
				err := bootstrapGenesisFromPeer(peer, stateDB, executor, policy)
				if errors.Is(err, errGenesisUnpinned) {
					// Configuration, not a peer fault: no other peer is any more
					// trustworthy, so say it once and stop.
					log.Error().Err(err).Msg("genesis bootstrap refused")
					return
				}
				if err != nil {
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
	//
	// BFT validators run it too: a validator that joins a running chain has to
	// reach the tip before it may propose (consensus.Config.Ready), and a
	// missed push must not leave it voting on proposals it cannot apply. A
	// single-path validator must NOT pull — it would be a second producer.
	// Which of the two a validator is changes mid-run (the producer moves
	// onto BFT at the activation height), so it is judged on every tick
	// rather than once here.
	//
	// A joining validator pulls on every path, BFT or not: it has no chain at
	// all until the first pull lands, and it never seals anything of its own
	// (readyToPropose refuses until it has a head), so there is no second
	// producer to create.
	pullsBlocks := func() bool { return !flagIsValidator || joiningValidator || bftEngine() != nil }
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if stateSyncPending.Load() || !pullsBlocks() {
					continue
				}
				// Probe every peer at once, not one after another. DialPeer
				// waits up to 5s on an unreachable peer, so a serial pass cost
				// 5s per unreachable peer and a one-second ticker turned into a
				// 5s x (peers) cycle: measured on devnet as a 10.3s block
				// cadence at three validators and 15.0s at four, with one
				// validator — no peers to dial — unaffected at 1.0s. Both the
				// pull below and readyToPropose (IsSynced reads BestPeerHeight,
				// which only this loop refreshes) sit behind it. A pass now
				// costs one timeout however many peers are down.
				refreshPeerHeights(peerPool)
				if n, err := syncer.SyncOnceContext(ctx); err != nil {
					log.Warn().Err(err).Msg("sync failed")
				} else if n > 0 {
					log.Debug().Int("blocks", n).Msg("synced blocks")
				}
			}
		}
	}()

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

	isReady := func() rpc.ReadyStatus {
		return nodeReadyStatus(executor, stateDB, syncer, stateSyncPending.Load())
	}

	// Core RPC server (handles /tx/submit, /block/*, /balance/*, /status, etc.)
	coreRPC := rpc.New(
		rpc.Config{
			ListenAddr:         flagRPCAddr,
			ValidatorPK:        validatorPKBytes,
			TrustedProducerKey: syncer.TrustedPubKey,
			LookupProducerKey:  executor.ActiveValidatorKey,
			ConsensusMode:      consensusMode,
			// Only the leader of a height puts transactions into a block, and
			// nothing carried one mempool to another: a transaction submitted
			// to any node that was not about to lead simply never landed —
			// which is what an operator registering a validator through an RPC
			// node saw. Relaying is one hop: peers admit but do not re-relay.
			RelayTx: func(tx *types.Transaction) {
				data, err := json.Marshal(tx)
				if err != nil {
					return
				}
				broadcaster.BroadcastSealed(network.KindTx, data)
			},
			PeerCount:         peerPool.Count, // wire live peer count from peerPool
			AuthToken:         authToken,
			AllowPlaintextP2P: flagAllowPlaintextP2P,
			CORSOrigins:       flagCORSOrigins,
			SubmitRatePerSec:  flagRateLimit,
			SubmitBurst:       flagRateBurst,
			TrustProxy:        flagTrustProxy,
			SimRatePerSec:     flagSimRate,
			SimBurst:          flagSimBurst,
			SimMaxInFlight:    flagSimMaxInFlight,
			SimTimeout:        flagSimTimeout,
			LookupReceipt: func(hash [32]byte) (*types.Receipt, error) {
				if txIndex == nil {
					return nil, nil
				}
				return txIndex.GetReceipt(hash)
			},
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
	}, coreRPC.WriteAuth(), coreRPC.SimGuard(), executor.GetBlockByHeight, broadcaster.AddPeer)

	// Post-quantum sealed P2P transport (ML-KEM-768 + AES-256-GCM). Carries the
	// same block-ingest and peer-announce operations as the plaintext write
	// plane, but encrypted and with the write token inside the envelope.
	var snapshotInFlight atomic.Bool
	objectHub := objects.NewHub()
	var objectCodec coding.Codec
	if flagObjectCodec != "" {
		var err error
		objectCodec, err = coding.Resolve(flagObjectCodec)
		if err != nil {
			return fmt.Errorf("--object-codec: %w", err)
		}
	}
	var objectPublish atomic.Bool
	rpc.RegisterP2PRoutes(router, rpc.P2PConfig{
		Identity:    nodeIdentity,
		Token:       authToken,
		MaxInFlight: flagP2PMaxInFlight,
		HandleBlock: func(body []byte) error {
			// The trusted key is read at call time: a node pinning only
			// --genesis-hash learns it from block 0 after start-up. A pushed
			// block this node already committed through BFT is accepted, not
			// refused — every BFT peer pushes every block it commits.
			return network.HandleIncomingBlock(body, syncer.TrustedPubKey(), executor.ActiveValidatorKey,
				func(b *types.Block) error { return applyCommittedBlock(executor, b) })
		},
		// Both BFT ingresses resolve the engine per message: a producer that
		// booted on the single path has one from the activation height on.
		HandleVote: func(body []byte) error {
			eng := bftEngine()
			if eng == nil {
				return fmt.Errorf("this node is not running BFT")
			}
			var vote consensus.Vote
			if err := json.Unmarshal(body, &vote); err != nil {
				return fmt.Errorf("invalid vote: %w", err)
			}
			eng.SubmitVote(&vote)
			return nil
		},
		HandleProposal: func(body []byte) error {
			eng := bftEngine()
			if eng == nil {
				return fmt.Errorf("this node is not running BFT")
			}
			msg, err := consensus.DecodeProposal(body)
			if err != nil {
				return err
			}
			// A proposal is voted on before it is ever executed, so it is
			// authenticated here with the rule pushed blocks get: the pinned
			// genesis key or the active set. The write-plane token alone only
			// says the sender is in the cluster. The round the envelope
			// carries is authenticated separately, inside the engine, by the
			// proposer's own signed vote.
			if err := msg.Block.Validate(); err != nil {
				return fmt.Errorf("invalid proposal: %w", err)
			}
			if err := network.VerifyBlockProducer(msg.Block, syncer.TrustedPubKey(), executor.ActiveValidatorKey); err != nil {
				return fmt.Errorf("proposal rejected: %w", err)
			}
			eng.SubmitProposal(msg)
			return nil
		},
		HandleRoundChange: func(body []byte) error {
			eng := bftEngine()
			if eng == nil {
				return fmt.Errorf("this node is not running BFT")
			}
			var rc consensus.RoundChange
			if err := json.Unmarshal(body, &rc); err != nil {
				return fmt.Errorf("invalid round-change: %w", err)
			}
			eng.SubmitRoundChange(&rc)
			return nil
		},
		// A transaction relayed by a peer. It is admitted under exactly the
		// checks the public submit route applies — the envelope signature, the
		// chain id and the mempool's own admission rules — and is NOT relayed
		// on, so the fan-out stays one hop whatever the peer topology.
		HandleTx: func(body []byte) error {
			var tx types.Transaction
			if err := json.Unmarshal(body, &tx); err != nil {
				return fmt.Errorf("invalid transaction: %w", err)
			}
			if err := mempool.ValidateEnvelope(&tx); err != nil {
				return fmt.Errorf("relayed transaction rejected: %w", err)
			}
			if err := pool.Add(&tx); err != nil {
				// Already held, or below the sender's committed nonce: normal
				// on a mesh where several peers relay the same transaction.
				log.Debug().Err(err).Msg("p2p: relayed transaction not admitted")
			}
			return nil
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
		// A snapshot is a full state scan on a public, token-free read, so
		// one at a time: a second requester is told to come back rather than
		// allowed to multiply the work.
		ServeStateSnapshot: func() ([]byte, error) {
			if !snapshotInFlight.CompareAndSwap(false, true) {
				return nil, errors.New("a state snapshot export is already in progress, retry shortly")
			}
			defer snapshotInFlight.Store(false)
			snap, err := executor.ExportStateSnapshot()
			if err != nil {
				return nil, fmt.Errorf("state snapshot not available: %w", err)
			}
			return json.Marshal(snap)
		},
		// Chunked counterpart of ServeStateSnapshot. The first page (empty
		// sessionID and cursor) pins one immutable snapshot session and returns
		// its id; later pages echo it back so every page reads the same state.
		// Guarded by the same single-flight gate as the single-shot form.
		ServeStateSnapshotChunk: func(sessionID string, afterKey []byte, maxLeaves int, source string) ([]byte, error) {
			if !snapshotInFlight.CompareAndSwap(false, true) {
				return nil, errors.New("a state snapshot export is already in progress, retry shortly")
			}
			defer snapshotInFlight.Store(false)
			var (
				chunk *types.StateSnapshotChunkResult
				err   error
			)
			if sessionID == "" {
				_, chunk, err = executor.StartSnapshotChunkSessionFromAt(source, afterKey, maxLeaves)
			} else {
				chunk, err = executor.PageSnapshotChunkSession(sessionID, afterKey, maxLeaves)
			}
			if err != nil {
				return nil, fmt.Errorf("state snapshot chunk not available: %w", err)
			}
			return json.Marshal(chunk)
		},
		ServeObjectManifest: func(descriptorID []byte) ([]byte, error) {
			if objectCodec == nil {
				return nil, errors.New("coded-object live path is not enabled")
			}
			if err := ensurePublishedSnapshot(objectHub, objectCodec, executor, nodeIdentity, &objectPublish); err != nil {
				return nil, err
			}
			return objectHub.ServeManifest(descriptorID)
		},
		ServeObjectSymbol: func(descriptorID []byte, index uint32) ([]byte, error) {
			if objectCodec == nil {
				return nil, errors.New("coded-object live path is not enabled")
			}
			if err := ensurePublishedSnapshot(objectHub, objectCodec, executor, nodeIdentity, &objectPublish); err != nil {
				return nil, err
			}
			return objectHub.ServeSymbol(descriptorID, index)
		},
	})

	// State proof routes (GET /state/root, /state/account/:addr, /state/proof/:addr)
	rpc.RegisterStateRoutes(router, stateDB, executor, coreRPC.SimGuard())

	// Migration bridge read routes — the GET mirrors of the bridge /call reads.
	rpc.RegisterBridgeRoutes(router, stateDB)

	// Contract-interaction routes (/call view calls, /estimate gas+fee) — issue #4
	rpc.RegisterCallRoutes(router, stateDB, executor, pool, coreRPC.SimGuard())
	// Source verification shares the metered plane: it compiles submitted
	// source, which is CPU spent on an anonymous caller's say-so.
	rpc.RegisterVerifyRoutes(router, stateDB, coreRPC.SimGuard())

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
	// router with the same policy means explorer/bridge/call routes get identical
	// treatment instead of silently defaulting to permissive. The request
	// counter sits outside even that, so sqc_rpc_requests_total sees every
	// request the listener takes, explorer routes and preflights included.
	httpServer := &http.Server{
		Addr:        flagRPCAddr,
		Handler:     nodeMetrics.Middleware(coreRPC.CORS().Middleware(router)),
		ReadTimeout: 10 * time.Second,
		// Ordinary RPC writes must finish in 60s. /ws slides this deadline
		// on every event and heartbeat via ResponseController.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if tlsEnabled {
		tlsCfg, tlsErr := rpc.PQTLSConfigWithPolicy(flagTLSCert, flagTLSKey, flagTLSRequireHybrid)
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
		// Say what is actually configured. The unconditional "post-quantum key
		// exchange" line here was true of what the node OFFERED and silently
		// false about what it negotiated, so an operator reading it had no way
		// to discover the connection was classical.
		if flagTLSRequireHybrid {
			log.Info().Msg("TLS 1.3 enabled, post-quantum hybrid key exchange REQUIRED (X25519MLKEM768); " +
				"clients without ML-KEM will be refused")
		} else {
			log.Info().Msg("TLS 1.3 enabled, post-quantum hybrid key exchange OFFERED (X25519MLKEM768) " +
				"with classical X25519 fallback — each handshake logs which was used; " +
				"pass --tls-require-hybrid to refuse the fallback")
		}
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
	pass, err := nodeKeyPassphrase()
	if err != nil {
		return nil, err
	}

	raw, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		return openNodeIdentity(path, raw, pass)
	case !os.IsNotExist(readErr):
		return nil, fmt.Errorf("read node identity: %w", readErr)
	}

	newPub, newPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("generate node identity: %w", err)
	}
	if err := writeNodeIdentity(path, newPub, newPriv, pass); err != nil {
		return nil, err
	}
	log.Info().Str("path", path).Bool("encrypted", pass != "").
		Msg("Generated persistent node P2P identity")
	return network.NewNodeIdentity(newPub, newPriv)
}

// openNodeIdentity reads an existing identity in whichever of the two on-disk
// forms it happens to be in, and upgrades a plaintext one if a passphrase has
// since been configured.
//
// The encrypted form is the wallet file format, which is JSON; the plaintext
// form is raw key bytes. Sniffing the leading brace tells them apart without a
// version field, which matters because the plaintext form never had one and
// nodes in the field are already running with it.
func openNodeIdentity(path string, raw []byte, pass string) (*network.NodeIdentity, error) {
	if len(raw) > 0 && raw[0] == '{' {
		if pass == "" {
			return nil, fmt.Errorf(
				"node identity at %s is encrypted but no passphrase was given: "+
					"set --node-key-pass-file or SQC_NODE_KEY_PASS", path)
		}
		loadedPub, loadedPriv, err := qcrypto.LoadWallet(path, []byte(pass))
		if err != nil {
			return nil, fmt.Errorf("decrypt node identity at %s: %w", path, err)
		}
		return network.NewNodeIdentity(loadedPub, loadedPriv)
	}

	loaded, err := qcrypto.PrivateKeyFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("stored node identity at %s is corrupt: %w", path, err)
	}

	if pass != "" {
		// Turning on encryption should take effect on the key that already
		// exists. Rewriting it in place keeps the node's peer identity stable,
		// which pinning peers depend on.
		if err := writeNodeIdentity(path, loaded.Public(), loaded, pass); err != nil {
			return nil, fmt.Errorf("encrypt existing node identity: %w", err)
		}
		log.Info().Str("path", path).Msg("Encrypted the existing node P2P identity key at rest")
	} else {
		log.Warn().Str("path", path).Msg(
			"Node P2P identity key is stored unencrypted (mode 0600). Anyone who can read " +
				"this file can impersonate this node to its peers. Set --node-key-pass-file " +
				"or SQC_NODE_KEY_PASS to encrypt it.")
	}
	return network.NewNodeIdentity(loaded.Public(), loaded)
}

// writeNodeIdentity persists the identity, encrypted when a passphrase is
// available and mode-0600 plaintext when one is not.
func writeNodeIdentity(path string, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, pass string) error {
	if pass != "" {
		if err := qcrypto.SaveWallet(path, pub, priv, []byte(pass)); err != nil {
			return fmt.Errorf("persist encrypted node identity: %w", err)
		}
		return nil
	}
	// 0600: this key authenticates the node to its peers.
	if err := os.WriteFile(path, priv.Bytes(), 0600); err != nil {
		return fmt.Errorf("persist node identity: %w", err)
	}
	return nil
}

// nodeKeyPassphrase resolves the node identity passphrase from a file or the
// environment. Unlike the validator passphrase there is no command-line form:
// nothing needs one, and an argument is readable by every local user through
// 'ps', which would undo the encryption it is meant to enable.
//
// An empty result is not an error. A node identity is a transport credential,
// not a funds key — losing it means peer impersonation, not theft — so
// requiring a secret to start a plain RPC node would be a poor trade. The
// caller warns instead.
func nodeKeyPassphrase() (string, error) {
	if flagNodeKeyPassFile != "" {
		if err := qcrypto.CheckKeyFilePermissions(flagNodeKeyPassFile); err != nil {
			return "", err
		}
		b, err := os.ReadFile(flagNodeKeyPassFile)
		if err != nil {
			return "", fmt.Errorf("read --node-key-pass-file: %w", err)
		}
		pass := strings.TrimRight(string(b), "\r\n")
		if pass == "" {
			return "", fmt.Errorf("--node-key-pass-file %s is empty", flagNodeKeyPassFile)
		}
		return pass, nil
	}
	return os.Getenv("SQC_NODE_KEY_PASS"), nil
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

// maxGenesisDocBytes bounds a genesis.json read from a peer. The response is
// unauthenticated at the point it is read, so an unbounded JSON decode of it is
// a one-request OOM. 8 MiB holds tens of thousands of allocations.
const maxGenesisDocBytes = 8 << 20

// errGenesisUnpinned is returned when a node is asked to take its genesis from a
// peer with nothing to check that document against. It is a configuration fault,
// so callers report it once instead of retrying it against every peer.
var errGenesisUnpinned = errors.New("peer genesis is unpinned")

// genesisPolicy is the operator's decision about which genesis documents this
// node may credit. The zero value fails closed.
type genesisPolicy struct {
	// docHash pins the expected Genesis.Hash(). nil = unpinned.
	docHash *[32]byte

	// trustPeer applies an unpinned peer-served document anyway.
	trustPeer bool
}

// bootstrapsGenesisFromPeers reports whether a node's configuration takes the
// peer-genesis path at all.
//
// A validator never does: it mints genesis from its own --genesis file before
// sealing block 0, which is why none of the pinning below can affect one.
//
// Nor does any node holding a local --genesis, validator or not: runNode already
// applied that document on the empty chain before sync started. Asking again
// later would re-apply it on top of whatever blocks have since landed, and
// ApplyToState overwrites an account rather than adding to it — so a full node
// on the documented --genesis --peer shape could reset a synced balance and
// nonce back to their genesis values and then diverge on state root.
func bootstrapsGenesisFromPeers(isValidator bool, peers []string, localGenesisPath string) bool {
	return !isValidator && len(peers) > 0 && localGenesisPath == ""
}

// metaForksKey is where the schedule a chain was started under is persisted, in
// the state DB's node-local meta: namespace (next to meta:height, outside the
// state root).
const metaForksKey = "forks"

// loadForkSchedule decides which rule-change schedule this node runs under.
//
// A running chain's schedule is the one persisted under meta:forks when its
// genesis was applied — from --genesis on this host, or fetched from a peer by
// a node started without one. That stored copy is authoritative: a
// peer-bootstrapped follower has no file to re-read on restart, and a
// validator's file can be edited after the fact. So when a stored map exists, a
// --genesis file that is missing, unreadable, or differs is fatal — it is
// either the wrong document or a schedule this chain was not started under,
// and a node that booted anyway would apply rules the rest of the network does
// not. The --genesis-doc-hash pin protects the document at apply time, so the
// stored map and the pinned document never disagree on a healthy node.
//
// With no stored map, the file is consulted. On a chain that already has blocks
// (one started before schedules were persisted) an unreadable file is fatal
// for the same reason. Only an empty chain may proceed past a file it cannot
// read: the apply path reports the better error, and persists the schedule
// there, before the allocations land. A node with neither returns nil — no
// rule changes, which is the correct reading of a chain that scheduled none,
// and the state a peer-bootstrapped follower is in until its genesis arrives.
//
// A malformed or unknown-upgrade schedule is fatal wherever it comes from.
// Starting anyway would mean running a node that silently ignores a rule change
// the rest of the network applies, and the symptom of that is a fork at the
// activation height rather than an error now.
func loadForkSchedule(genesisPath string, stateDB *state.StateDB, chainEmpty bool, policy forkPolicy) (forks.Schedule, error) {
	stored, ok, err := readStoredForkSchedule(stateDB)
	if err != nil {
		return nil, err
	}
	if ok {
		if err := stored.Validate(); err != nil {
			return nil, fmt.Errorf("this chain's stored fork schedule: %w (is this binary older than the one that started the chain?)", err)
		}
		if genesisPath == "" {
			return stored, nil
		}
		g, err := genesiscfg.LoadFile(genesisPath)
		if err != nil {
			return nil, fmt.Errorf("this chain was started under fork schedule %s but --genesis %q cannot be read: %w",
				formatForkSchedule(stored), genesisPath, err)
		}
		if sameForkSchedule(g.Forks, stored) {
			return stored, nil
		}
		// The file differs. The one legitimate reason is a rule decided AFTER
		// this chain launched — the case that otherwise costs the chain its
		// history, which is what the schedule exists to prevent.
		merged, added, err := extendForkSchedule(stored, g.Forks, genesisPath, policy)
		if err != nil {
			return nil, err
		}
		if len(added) > 0 && policy.DocHash != nil {
			// The document changed, so its hash did too. An operator who
			// pinned one must say "yes, this one" by re-pinning, making a
			// consensus rule change take two deliberate acts rather than one.
			if got := g.Hash(); got != *policy.DocHash {
				return nil, fmt.Errorf("--genesis %q hashes to %x but --genesis-doc-hash pins %x — "+
					"extending the schedule changes the document, so re-pin with "+
					"'sqc-node genesis-hash %s' once you have checked the change is the one you intend",
					genesisPath, got[:8], (*policy.DocHash)[:8], genesisPath)
			}
		}
		if len(added) > 0 {
			// Persisted now, so the merged schedule is what this chain was
			// started under from here on: the next restart matches the file
			// without the flag, and a crash between here and the first block
			// re-reads the schedule it adopted rather than the older one.
			if err := storeForkSchedule(stateDB, merged); err != nil {
				return nil, err
			}
			for _, name := range added {
				log.Warn().
					Str("upgrade", name).
					Uint64("at_height", merged[name]).
					Uint64("head", policy.Head).
					Msg("adopting a consensus rule this chain was NOT started under — every validator must run this schedule before the activation height")
			}
		}
		return merged, nil
	}
	if genesisPath == "" {
		return nil, nil
	}
	g, err := genesiscfg.LoadFile(genesisPath)
	if err != nil {
		if !chainEmpty {
			return nil, fmt.Errorf("this chain has no stored fork schedule and --genesis %q cannot be read: %w",
				genesisPath, err)
		}
		// Not fatal on an empty chain: the genesis path is validated properly by
		// the code that applies it, which reports a far better error than this.
		log.Debug().Err(err).Str("path", genesisPath).Msg("no fork schedule read from genesis")
		return nil, nil
	}
	if err := g.Forks.Validate(); err != nil {
		return nil, fmt.Errorf("genesis %q: %w", genesisPath, err)
	}
	return g.Forks, nil
}

// readStoredForkSchedule returns the schedule persisted by persistForkSchedule,
// and whether one exists. A stored nil is read back as an empty schedule, so
// "started with no rule changes" is distinguishable from "never recorded".
func readStoredForkSchedule(stateDB *state.StateDB) (forks.Schedule, bool, error) {
	raw, ok, err := stateDB.GetMeta(metaForksKey)
	if err != nil {
		return nil, false, fmt.Errorf("read stored fork schedule: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	s := forks.Schedule{}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false, fmt.Errorf("stored fork schedule is corrupt: %w", err)
	}
	if s == nil {
		s = forks.Schedule{}
	}
	return s, true, nil
}

// persistForkSchedule records the schedule a chain is being started under and
// installs it on the executor.
//
// Both genesis apply paths call it BEFORE ApplyToState. The order matters on a
// follower: its genesis arrives from a peer while the 1 s syncer is already
// running, and the first block the syncer applies must be gated by the same
// schedule the validator applied it under — never by a nil one. Persisting
// first also means a restart between the two writes re-runs the apply path
// under the schedule it recorded, rather than under none.
func persistForkSchedule(stateDB *state.StateDB, executor *block.Executor, s forks.Schedule) error {
	if s == nil {
		s = forks.Schedule{}
	}
	if err := storeForkSchedule(stateDB, s); err != nil {
		return err
	}
	executor.SetForks(s)
	return nil
}

// forkPolicy carries what loadForkSchedule needs to judge a --genesis file
// scheduling more than the chain was started under.
type forkPolicy struct {
	// Head is the height of this chain's newest applied block. An upgrade may
	// only be ADDED above it: at or below, blocks already applied would have
	// been judged under a rule the node is only now adopting, so the schedule
	// and the chain would disagree about history.
	Head uint64
	// AllowExtension permits adopting upgrades the stored schedule lacks
	// (--allow-fork-extension). Off by default: a genesis file can be edited
	// after the fact, and a node that adopted a rule on its own would fork at
	// the activation height.
	AllowExtension bool
	// DocHash is the operator's --genesis-doc-hash pin, when set. The fork
	// schedule is part of the genesis hash, so an EXTENDED document is by
	// construction not the pinned one: adopting it without re-pinning would
	// undercut what the pin asserts, and would let anyone who can write
	// genesis.json on a host where the flag was left on schedule a rule
	// change. Checked only on the extension path — an unchanged document
	// hashes to its pin already.
	DocHash *[32]byte
}

// extendForkSchedule reconciles a --genesis schedule with the one a running
// chain was started under, returning the merged schedule and the names it adds.
//
// Exactly one kind of difference is legitimate: a NEW upgrade activating above
// the current head. A moved activation height, a dropped upgrade, or an
// activation at or below the head describe a different chain rather than a
// later decision about this one, and are refused whatever the flag says.
//
// An entry at height 0 is "never" (see package forks), so adding one reserves a
// name without changing any rule. That is the documented way to name an upgrade
// before the code that honours it exists, so it needs no flag.
func extendForkSchedule(stored, want forks.Schedule, genesisPath string, policy forkPolicy) (forks.Schedule, []string, error) {
	merged := forks.Schedule{}
	for name, at := range stored {
		merged[name] = at
	}

	// Everything the chain already runs under must survive untouched. Names are
	// walked in sorted order so two runs over the same pair of documents report
	// the same offending upgrade.
	for _, name := range stored.Names() {
		at := stored[name]
		got, present := want[name]
		if !present {
			return nil, nil, fmt.Errorf("--genesis %q drops %s, which this chain was started under (%s) — "+
				"a node that stopped applying a rule its peers still apply would fork",
				genesisPath, name, formatForkSchedule(stored))
		}
		if got != at {
			return nil, nil, fmt.Errorf("--genesis %q moves %s from height %d to %d, but this chain was started under %s — "+
				"an activation height is part of what this chain agreed on and cannot be edited afterwards",
				genesisPath, name, at, got, formatForkSchedule(stored))
		}
	}

	var added []string
	for _, name := range want.Names() {
		if _, present := stored[name]; present {
			continue
		}
		at := want[name]
		if at == 0 {
			merged[name] = 0 // a reserved name, inert until a height is set
			continue
		}
		if at <= policy.Head {
			return nil, nil, fmt.Errorf("--genesis %q schedules %s at height %d but this chain is already at %d — "+
				"an upgrade can only be added ABOVE the head, or blocks already applied would have been judged "+
				"under a rule being adopted only now",
				genesisPath, name, at, policy.Head)
		}
		merged[name] = at
		added = append(added, name)
	}

	if len(added) > 0 && !policy.AllowExtension {
		return nil, nil, fmt.Errorf("--genesis %q schedules %s, which this chain was not started under (%s). "+
			"Adopting a consensus rule is a network-wide decision: every validator must run the same schedule "+
			"before the activation height, and a node adopting one alone would fork there. "+
			"Restart with --allow-fork-extension once the whole network is on this document",
			genesisPath, strings.Join(added, ", "), formatForkSchedule(stored))
	}
	if err := merged.Validate(); err != nil {
		return nil, nil, fmt.Errorf("--genesis %q would extend this chain to a schedule it cannot run: %w", genesisPath, err)
	}
	return merged, added, nil
}

// storeForkSchedule records the schedule a chain runs under, without touching
// the executor: the caller installs it (loadForkSchedule runs before the
// executor is given its schedule).
func storeForkSchedule(stateDB *state.StateDB, s forks.Schedule) error {
	if s == nil {
		s = forks.Schedule{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode fork schedule: %w", err)
	}
	if err := stateDB.PutMeta(metaForksKey, raw); err != nil {
		return fmt.Errorf("persist fork schedule: %w", err)
	}
	return nil
}

func sameForkSchedule(a, b forks.Schedule) bool {
	if len(a) != len(b) {
		return false
	}
	for name, at := range a {
		if b[name] != at {
			return false
		}
	}
	return true
}

// formatForkSchedule renders a schedule for an error message. encoding/json
// sorts map keys, so the text is the same on every run.
func formatForkSchedule(s forks.Schedule) string {
	if s == nil {
		s = forks.Schedule{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%v", s)
	}
	return string(raw)
}

// applyLocalGenesis credits the allocations of a genesis.json held on this host.
//
// A local document is not a peer's, but it is still money: ApplyToState commits
// balances to LevelDB before any block exists to contradict them, and nothing
// later removes an account it wrote. So it goes through the same gate — a
// document for another chain, or one the operator pinned and that no longer
// matches, stops the node instead of minting a supply nobody meant.
func applyLocalGenesis(path string, stateDB *state.StateDB, executor *block.Executor, policy genesisPolicy) (*genesiscfg.Genesis, error) {
	g, err := genesiscfg.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load genesis %q: %w", path, err)
	}
	if err := verifyGenesisDocument(g, policy.docHash, path); err != nil {
		return nil, err
	}
	if err := persistForkSchedule(stateDB, executor, g.Forks); err != nil {
		return nil, err
	}
	if _, err := g.ApplyToState(stateDB); err != nil {
		return nil, fmt.Errorf("apply genesis allocations: %w", err)
	}
	log.Info().Int("allocations", len(g.Allocations)).Str("total_supply", g.TotalSupply).
		Str("genesis_doc_hash", g.HashHex()).Msg("Applied genesis allocations")
	return g, nil
}

// establishChainIDFromChain adopts the chain id recorded in this node's own
// blocks.
//
// For a chain that already has history the blocks are the authority, not the
// genesis file sitting next to them: they were sealed under an id, every header
// carries it, and core/block.Executor rejects any header whose id differs from
// types.ChainID. A node that restarted without adopting it would come up on the
// compiled-in default and refuse to replay its own chain.
//
// An empty chain has nothing to adopt from and is left alone — the genesis
// document names the network in that case.
func establishChainIDFromChain(executor *block.Executor) error {
	head, err := executor.LatestBlock()
	if err != nil || head == nil {
		return nil
	}
	id := head.Header.ChainID
	if id == "" {
		// Blocks predating per-tier ids carry no id of their own; they belong to
		// the single shared chain that existed when they were sealed.
		id = types.DefaultChainID
	}
	if flagChainID != "" && id != flagChainID {
		return fmt.Errorf("this node's chain is %q (from block %d) but --chain-id says %q — "+
			"pointing a node at a different network's data directory would fork it",
			id, head.Header.Height, flagChainID)
	}
	if err := types.SetChainID(id); err != nil {
		return fmt.Errorf("adopt chain id from stored chain: %w", err)
	}
	log.Info().Str("chain_id", id).Uint64("from_height", head.Header.Height).
		Msg("Chain identity adopted from existing blocks")
	return nil
}

// verifyGenesisDocument gates a genesis document before ApplyToState writes it.
//
// Past this point the allocations are money: they are committed to LevelDB
// before any block exists to contradict them, and /balance reports them to every
// caller as real. So a document must be well-formed, be for THIS chain, and —
// when the operator said what it should be — be exactly that document.
func verifyGenesisDocument(g *genesiscfg.Genesis, pin *[32]byte, source string) error {
	if err := g.Validate(); err != nil {
		return fmt.Errorf("genesis from %s is invalid: %w", source, err)
	}
	// Pin first. Everything below decides what network this node joins based on
	// what the document says, so the document has to be the right one before its
	// claims are worth reading.
	if pin != nil {
		if got := g.Hash(); got != *pin {
			return fmt.Errorf("genesis from %s hashes to %x, pinned --genesis-doc-hash is %x — wrong network or hostile peer",
				source, got[:8], pin[:8])
		}
	}
	// The chain id comes FROM the genesis document rather than from a constant
	// compiled into this binary, so one image can serve devnet, testnet and
	// mainnet as separate networks. That is the whole point: while every tier
	// shared "splendor-quantum-1", a transaction signed on devnet was byte-for-
	// byte valid on mainnet the moment the sender's nonce matched there.
	//
	// Adopting an id from a document is only safe because of what has already
	// happened above it: a peer-served genesis reaches this function only when
	// the operator pinned it with --genesis-doc-hash or explicitly accepted the
	// risk with --trust-peer-genesis, and a local --genesis file is the
	// operator's own. --chain-id is the belt-and-braces form: when the operator
	// says which network this is meant to be, a document that disagrees is
	// refused rather than adopted.
	if flagChainID != "" && g.ChainID != flagChainID {
		return fmt.Errorf("genesis from %s is for chain %q but --chain-id says this node is %q",
			source, g.ChainID, flagChainID)
	}
	if err := types.SetChainID(g.ChainID); err != nil {
		return fmt.Errorf("genesis from %s: %w", source, err)
	}
	return nil
}

// bootstrapGenesisFromPeer applies the genesis allocations an RPC node needs
// before block sync starts, so its state root matches the validator's. It is
// reached only by a node with no local --genesis to credit instead.
//
// A peer's /genesis is an unauthenticated response over plain HTTP from whatever
// currently answers at --peer — and its allocations land in state directly, so
// serving a doctored one is how an impersonated bootstrap peer mints itself a
// balance this node will then swear to.
func bootstrapGenesisFromPeer(peerURL string, stateDB *state.StateDB, executor *block.Executor, policy genesisPolicy) error {
	// If chain already has blocks, no need to bootstrap
	latest, _ := executor.LatestBlock()
	if latest != nil {
		return nil
	}

	// Fail closed before the request is even made (fetchPeerGenesis): nothing
	// downstream can establish that an arbitrary peer's genesis is this
	// network's genesis, so an operator who has not said what to expect gets
	// told how to say it.
	g, err := fetchPeerGenesis(peerURL, policy)
	if err != nil {
		return err
	}

	if err := verifyGenesisDocument(g, policy.docHash, "peer "+peerURL); err != nil {
		return err
	}
	if policy.docHash == nil {
		log.Warn().Str("peer", peerURL).Str("genesis_doc_hash", g.HashHex()).
			Msg("--trust-peer-genesis: crediting an unverified peer genesis. Pin this hash with --genesis-doc-hash.")
	}

	// The schedule lands before the allocations: the syncer is already running
	// on this node, and the first block it applies must be gated by the
	// schedule the validator applied it under, never by a nil one.
	if err := persistForkSchedule(stateDB, executor, g.Forks); err != nil {
		return err
	}
	if _, err := g.ApplyToState(stateDB); err != nil {
		return fmt.Errorf("apply peer genesis allocations: %w", err)
	}

	stateRoot, _ := stateDB.StateRoot()
	log.Info().
		Str("peer", peerURL).
		Int("allocations", len(g.Allocations)).
		Str("genesis_doc_hash", g.HashHex()).
		Str("state_root", fmt.Sprintf("%x", stateRoot[:8])).
		Msg("RPC: bootstrapped genesis allocations from validator peer")
	return nil
}

// parsePinnedHash decodes a 32-byte hex pin from a flag, with or without 0x.
// A nil result means the flag was not set.
func parsePinnedHash(flagName, value string) (*[32]byte, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", flagName, err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("invalid %s: want 32 bytes (64 hex chars), got %d", flagName, len(raw))
	}
	var pin [32]byte
	copy(pin[:], raw)
	return &pin, nil
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

// refreshPeerHeights polls every known peer's /status concurrently and records
// the heights that answer. Peers that do not answer are left as they were;
// DialPeer's own 5s timeout bounds the pass.
//
// maxPeerProbeFanout caps how many probes are in flight at once so a large
// peer set cannot open a socket per peer every second.
const maxPeerProbeFanout = 16

func refreshPeerHeights(peerPool *network.PeerPool) {
	addrs := peerPool.Addrs()
	if len(addrs) == 0 {
		return
	}
	sem := make(chan struct{}, maxPeerProbeFanout)
	var wg sync.WaitGroup
	for _, addr := range addrs {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if h, err := network.DialPeer(addr); err == nil {
				peerPool.UpdatePeerHeight(addr, h)
			}
		}(addr)
	}
	wg.Wait()
}
