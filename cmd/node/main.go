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
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
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
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
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
	flagNodeKeyPassFile   string
	flagPeers             []string
	flagBlockInterval     int
	flagIsValidator       bool
	flagLogLevel          string
	flagGenesisPath       string
	flagValidatorPKHex    string
	flagGenesisHashHex    string
	flagGenesisDocHashHex string
	flagTrustPeerGenesis  bool
	flagChainID           string
	flagTLSRequireHybrid  bool
	flagRPCAuthToken      string
	flagAllowPrivatePeers bool
	flagCORSOrigins       []string
	flagRateLimit         float64
	flagRateBurst         float64
	flagTrustProxy        bool
	flagSimRate           float64
	flagSimBurst          float64
	flagSimMaxInFlight    int
	flagSimTimeout        time.Duration
	flagTLSCert           string
	flagTLSKey            string
	flagPublicURL         string
	flagAllowPlaintextP2P bool
	flagPeerPins          []string
	flagRequirePeerPins   bool
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
	root.Flags().BoolVar(&flagIsValidator, "validator", false, "Run as block-producing validator")
	root.Flags().StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	root.Flags().StringVar(&flagGenesisPath, "genesis", "", "Path to genesis.json (allocations applied once on an empty chain)")
	root.Flags().StringVar(&flagValidatorPKHex, "validator-pubkey-hex", "", "Trusted genesis validator ML-DSA-65 public key (hex). Full nodes MUST set this to authenticate pushed blocks on POST /block.")
	root.Flags().StringVar(&flagGenesisHashHex, "genesis-hash", "", "Pinned hash of block 0 (hex). Required to bootstrap the validator pubkey from peers when --validator-pubkey-hex is not supplied; without one, a node would trust whatever key its first peer serves.")
	root.Flags().StringVar(&flagGenesisDocHashHex, "genesis-doc-hash", "", "Pinned hash of the genesis DOCUMENT (hex) — print it with 'sqc-node genesis-hash <genesis.json>'. A node that has no local --genesis credits a peer-served genesis.json only when it hashes to this. Distinct from --genesis-hash, which pins block 0's header.")
	root.Flags().StringVar(&flagChainID, "chain-id", "", "Chain id this node is expected to be on (e.g. splendor-quantum-devnet-1). When set, a genesis document naming a different chain is refused instead of adopted. Leave empty to take whatever the verified genesis says. (or SQC_CHAIN_ID env)")
	root.Flags().BoolVar(&flagTrustPeerGenesis, "trust-peer-genesis", false, "Credit a peer-served genesis.json with no --genesis-doc-hash pin. The peer can then mint any balance it likes into this node's state. Lab networks only.")
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
	root.Flags().StringVar(&flagTLSCert, "tls-cert", "", "PEM certificate for HTTPS. Enables TLS 1.3 and OFFERS post-quantum hybrid key exchange (X25519MLKEM768), falling back to classical X25519 for clients that cannot do it — see --tls-require-hybrid.")
	root.Flags().BoolVar(&flagTLSRequireHybrid, "tls-require-hybrid", false, "Refuse TLS connections that cannot negotiate X25519MLKEM768, instead of falling back to classical X25519. Set this before describing the transport as post-quantum: without it a deployment can offer the hybrid group and still serve nothing but classical.")
	root.Flags().StringVar(&flagTLSKey, "tls-key", "", "PEM private key matching --tls-cert")
	root.Flags().StringVar(&flagPublicURL, "public-url", "", "Externally reachable base URL advertised to peers (default: derived from --rpc-addr)")
	root.Flags().BoolVar(&flagAllowPlaintextP2P, "allow-plaintext-p2p", false, "Allow unencrypted block push and catch-up sync when a peer has no ML-KEM sealed transport. Off by default so the channel cannot be downgraded.")
	root.Flags().StringArrayVar(&flagPeerPins, "peer-pin", nil, "Pin a peer's node identity as URL=PUBKEY_HEX (repeatable). Without a pin, peer identities are trusted on first use — set --require-peer-pins to refuse unpinned peers instead.")
	root.Flags().BoolVar(&flagRequirePeerPins, "require-peer-pins", false, "Refuse to talk to any peer that has no --peer-pin, instead of trusting its identity on first contact. Governs who this node dials — block push, announce and catch-up pull; inbound blocks stay authenticated by the validator signature and the write-plane token. Recommended once a cluster's peers are known: trust-on-first-use hands the relationship to whoever answers first at bootstrap.")

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
	// which rules that chain switches to and when.
	forkSchedule, err := loadForkSchedule(flagGenesisPath)
	if err != nil {
		return err
	}
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
	broadcaster := network.NewBroadcaster(validatorPKBytes, authToken, flagAllowPlaintextP2P)
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

	// ── Chain identity ─────────────────────────────────────────────────────
	// Settle which network this is BEFORE anything reads types.ChainID. On an
	// empty chain the genesis document decides (applyLocalGenesis, below); on a
	// chain that already has blocks the blocks themselves decide, because they
	// were sealed under an id and the executor checks every header against it.
	// Without this a restarted node would silently fall back to the compiled
	// default and then reject its own history as "wrong chain id".
	if err := establishChainIDFromChain(executor); err != nil {
		return err
	}

	// ── Genesis (if chain is empty) ────────────────────────────────────────
	if latestBlock, _ := executor.LatestBlock(); latestBlock == nil {
		// Credit genesis allocations before block 0 so every node — validator
		// and sync — starts from the same funded state root. Without this the
		// total_supply declared in genesis.json is never minted, the chain has
		// zero balance, and no transaction can meet the minimum fee.
		if flagGenesisPath != "" {
			if err := applyLocalGenesis(flagGenesisPath, stateDB, genesisPolicy{docHash: genesisDocPin}); err != nil {
				return err
			}
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
	if bootstrapsGenesisFromPeers(flagIsValidator, flagPeers, flagGenesisPath) {
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
			SimRatePerSec:    flagSimRate,
			SimBurst:         flagSimBurst,
			SimMaxInFlight:   flagSimMaxInFlight,
			SimTimeout:       flagSimTimeout,
			Forks:            forkSchedule,
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

// applyLocalGenesis credits the allocations of a genesis.json held on this host.
//
// A local document is not a peer's, but it is still money: ApplyToState commits
// balances to LevelDB before any block exists to contradict them, and nothing
// later removes an account it wrote. So it goes through the same gate — a
// document for another chain, or one the operator pinned and that no longer
// matches, stops the node instead of minting a supply nobody meant.
// loadForkSchedule reads the rule-change schedule out of the local genesis
// document.
//
// A node with no local --genesis returns an empty schedule rather than an
// error. That node takes its genesis from a peer, gated by --genesis-doc-hash,
// and the schedule arrives with it — but that path runs later in startup, so
// there is nothing to read here yet. An empty schedule means "no rule changes",
// which is the correct reading of a chain that has not scheduled any.
//
// A malformed or unknown-upgrade schedule is fatal. Starting anyway would mean
// running a node that silently ignores a rule change the rest of the network
// applies, and the symptom of that is a fork at the activation height rather
// than an error now.
func loadForkSchedule(genesisPath string) (forks.Schedule, error) {
	if genesisPath == "" {
		return nil, nil
	}
	g, err := genesiscfg.LoadFile(genesisPath)
	if err != nil {
		// Not fatal here: the genesis path is validated properly by the code that
		// applies it, which reports a far better error than this would.
		log.Debug().Err(err).Str("path", genesisPath).Msg("no fork schedule read from genesis")
		return nil, nil
	}
	if err := g.Forks.Validate(); err != nil {
		return nil, fmt.Errorf("genesis %q: %w", genesisPath, err)
	}
	return g.Forks, nil
}

func applyLocalGenesis(path string, stateDB *state.StateDB, policy genesisPolicy) error {
	g, err := genesiscfg.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load genesis %q: %w", path, err)
	}
	if err := verifyGenesisDocument(g, policy.docHash, path); err != nil {
		return err
	}
	if _, err := g.ApplyToState(stateDB); err != nil {
		return fmt.Errorf("apply genesis allocations: %w", err)
	}
	log.Info().Int("allocations", len(g.Allocations)).Str("total_supply", g.TotalSupply).
		Str("genesis_doc_hash", g.HashHex()).Msg("Applied genesis allocations")
	return nil
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

	// Fail closed before the request is even made. Nothing downstream can
	// establish that an arbitrary peer's genesis is this network's genesis, so
	// an operator who has not said what to expect gets told how to say it.
	if policy.docHash == nil && !policy.trustPeer {
		return fmt.Errorf("%w: refusing to credit balances from %s because nothing says what that document should be. "+
			"Either copy the network's genesis.json to this host and pass --genesis <path>, "+
			"or pin it with --genesis-doc-hash <hex> (print the pin with 'sqc-node genesis-hash <path>'), "+
			"or pass --trust-peer-genesis if you control that peer and accept it can mint balances here",
			errGenesisUnpinned, peerURL)
	}

	// Fetch genesis.json from the validator peer
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(peerURL + "/genesis")
	if err != nil {
		return fmt.Errorf("fetch /genesis from peer: %w", err)
	}
	defer resp.Body.Close()

	// Returning nil here would count as success and stop the caller trying the
	// remaining peers, leaving the node unfunded on the word of the one peer that
	// happened to answer first without the endpoint.
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("peer %s serves no /genesis (it was started without --genesis)", peerURL)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer /genesis returned %d", resp.StatusCode)
	}

	g, err := genesiscfg.LoadFromReader(io.LimitReader(resp.Body, maxGenesisDocBytes))
	if err != nil {
		return fmt.Errorf("parse peer genesis.json: %w", err)
	}

	if err := verifyGenesisDocument(g, policy.docHash, "peer "+peerURL); err != nil {
		return err
	}
	if policy.docHash == nil {
		log.Warn().Str("peer", peerURL).Str("genesis_doc_hash", g.HashHex()).
			Msg("--trust-peer-genesis: crediting an unverified peer genesis. Pin this hash with --genesis-doc-hash.")
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
