// Package config provides the Splendor Quantum Chain node configuration.
//
// NOT YET WIRED: nothing imports this package. cmd/node takes its entire runtime
// configuration from CLI flags, so a JSON file written against these structs is
// read by no one and its defaults below are inert. Where a flag is the live
// control, the field says so. Wire this into cmd/node or drop it — leaving it as
// it stands invites operators to tune a file the node never opens.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config is the complete node configuration.
type Config struct {
	// Node identity
	Node NodeConfig `json:"node"`

	// Chain parameters
	Chain ChainConfig `json:"chain"`

	// Validator settings (only used when node.mode == "validator")
	Validator ValidatorConfig `json:"validator"`

	// RPC server settings
	RPC RPCConfig `json:"rpc"`

	// P2P networking
	P2P P2PConfig `json:"p2p"`

	// Storage paths
	Storage StorageConfig `json:"storage"`

	// AI scheduler settings
	AI AIConfig `json:"ai"`

	// Metrics / monitoring
	Metrics MetricsConfig `json:"metrics"`
}

// NodeConfig controls node identity and operation mode.
type NodeConfig struct {
	// Mode: "full" (sync only) or "validator" (produce blocks)
	Mode string `json:"mode"`

	// Human-readable name for this node (shown in logs and peer lists)
	Name string `json:"name"`

	// LogLevel: "debug", "info", "warn", "error"
	LogLevel string `json:"log_level"`

	// LogFormat: "console" (human-readable) or "json"
	LogFormat string `json:"log_format"`
}

// ChainConfig defines chain-level parameters.
type ChainConfig struct {
	// ChainID must match types.ChainID. Do not change after genesis.
	ChainID string `json:"chain_id"`

	// NetworkName is a human-readable network identifier.
	NetworkName string `json:"network_name"`

	// MaxTxPerBlock limits how many transactions fit in one block.
	MaxTxPerBlock int `json:"max_tx_per_block"`

	// MinFee is the minimum transaction fee in smallest SPLD units.
	MinFee string `json:"min_fee"`
}

// ValidatorConfig is used only when the node runs as a block producer.
type ValidatorConfig struct {
	// WalletPath is the path to the .sqwallet file for the validator key.
	WalletPath string `json:"wallet_path"`

	// WalletPassEnv is the environment variable that holds the wallet password.
	// Defaults to SQC_WALLET_PASS.
	WalletPassEnv string `json:"wallet_pass_env"`

	// BlockInterval is how often blocks are produced.
	//
	// Inert (see the package note): the validator's cadence comes from
	// `sqc-node --block-interval` in cmd/node, and the deployed tiers do not all
	// run the same value. `consensus.Config.BlockInterval` is the field that
	// actually reaches the block-production ticker.
	BlockInterval Duration `json:"block_interval"`

	// MaxBlockGas is the maximum gas allowed per block (future use).
	MaxBlockGas uint64 `json:"max_block_gas"`
}

// RPCConfig controls the HTTP JSON-RPC server.
type RPCConfig struct {
	// ListenAddr is the bind address for the RPC server (e.g. ":8080").
	ListenAddr string `json:"listen_addr"`

	// EnableCORS allows cross-origin requests (disable in production).
	EnableCORS bool `json:"enable_cors"`

	// MaxRequestBodyBytes limits incoming request sizes.
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes"`

	// ReadTimeout for HTTP requests.
	ReadTimeout Duration `json:"read_timeout"`

	// WriteTimeout for HTTP responses.
	WriteTimeout Duration `json:"write_timeout"`

	// EnableWebSocket enables the /ws WebSocket subscription endpoint.
	EnableWebSocket bool `json:"enable_websocket"`
}

// P2PConfig controls peer-to-peer networking.
type P2PConfig struct {
	// ListenAddr is the P2P inbound address (e.g. ":9090").
	// Leave empty to disable inbound P2P.
	ListenAddr string `json:"listen_addr"`

	// BootstrapPeers is the initial list of peer RPC addresses to connect to.
	BootstrapPeers []string `json:"bootstrap_peers"`

	// MaxPeers is the maximum number of connected peers.
	MaxPeers int `json:"max_peers"`

	// PeerExchangeInterval is how often the node requests peer lists from known peers.
	PeerExchangeInterval Duration `json:"peer_exchange_interval"`

	// ValidatorPubKeyHex is the hex-encoded ML-DSA-65 public key of the genesis validator.
	// Required for all nodes to verify incoming blocks.
	ValidatorPubKeyHex string `json:"validator_pubkey_hex"`
}

// StorageConfig controls data directory paths.
type StorageConfig struct {
	// DataDir is the root directory for all chain data.
	DataDir string `json:"data_dir"`

	// StateDBPath overrides the state DB path (default: DataDir/state).
	StateDBPath string `json:"state_db_path,omitempty"`

	// ChainDBPath overrides the chain DB path (default: DataDir/chain).
	ChainDBPath string `json:"chain_db_path,omitempty"`

	// ReceiptDBPath overrides the receipt DB path (default: DataDir/receipts).
	ReceiptDBPath string `json:"receipt_db_path,omitempty"`

	// TxIndexDBPath overrides the tx index DB path (default: DataDir/txindex).
	TxIndexDBPath string `json:"tx_index_db_path,omitempty"`
}

// AIConfig controls the on-node AI inference scheduler.
type AIConfig struct {
	// Enabled turns the AI scheduler on or off.
	Enabled bool `json:"enabled"`

	// MinBatchSize is the minimum transactions per block suggested by the scheduler.
	MinBatchSize int `json:"min_batch_size"`

	// MaxBatchSize is the maximum transactions per block suggested by the scheduler.
	MaxBatchSize int `json:"max_batch_size"`

	// ObservationIntervalMs is how often (milliseconds) the scheduler samples metrics.
	ObservationIntervalMs int `json:"observation_interval_ms"`
}

// MetricsConfig controls the Prometheus metrics endpoint.
type MetricsConfig struct {
	// Enabled turns the /metrics endpoint on or off.
	Enabled bool `json:"enabled"`

	// ListenAddr is the bind address for the metrics HTTP server (e.g. ":9100").
	ListenAddr string `json:"listen_addr"`
}

// Duration wraps time.Duration for JSON serialisation as a human-readable string.
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

// Default returns a sensible default configuration.
func Default() *Config {
	return &Config{
		Node: NodeConfig{
			Mode:      "full",
			Name:      "sqc-node",
			LogLevel:  "info",
			LogFormat: "console",
		},
		Chain: ChainConfig{
			ChainID:       "splendor-quantum-1",
			NetworkName:   "Splendor Quantum Chain Mainnet",
			MaxTxPerBlock: 10_000,
			MinFee:        "1000",
		},
		Validator: ValidatorConfig{
			WalletPath:    "./validator.sqwallet",
			WalletPassEnv: "SQC_WALLET_PASS",
			BlockInterval: Duration{2 * time.Second},
			MaxBlockGas:   500_000_000_000,
		},
		RPC: RPCConfig{
			ListenAddr:          ":8080",
			EnableCORS:          true,
			MaxRequestBodyBytes: 64 * 1024 * 1024,
			ReadTimeout:         Duration{10 * time.Second},
			WriteTimeout:        Duration{30 * time.Second},
			EnableWebSocket:     true,
		},
		P2P: P2PConfig{
			ListenAddr:           ":9090",
			BootstrapPeers:       []string{},
			MaxPeers:             50,
			PeerExchangeInterval: Duration{60 * time.Second},
		},
		Storage: StorageConfig{
			DataDir: "./sqc-data",
		},
		AI: AIConfig{
			Enabled:               true,
			MinBatchSize:          100,
			MaxBatchSize:          10_000,
			ObservationIntervalMs: 500,
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			ListenAddr: ":9100",
		},
	}
}

// LoadFile reads a JSON config file and merges it over the defaults.
func LoadFile(path string) (*Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open config file %s: %w", path, err)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config to a JSON file.
func (c *Config) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create config file %s: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

// StateDBPath returns the resolved state DB path.
func (c *Config) ResolveStateDBPath() string {
	if c.Storage.StateDBPath != "" {
		return c.Storage.StateDBPath
	}
	return c.Storage.DataDir + "/state"
}

// ResolveChainDBPath returns the resolved chain DB path.
func (c *Config) ResolveChainDBPath() string {
	if c.Storage.ChainDBPath != "" {
		return c.Storage.ChainDBPath
	}
	return c.Storage.DataDir + "/chain"
}

// ResolveReceiptDBPath returns the resolved receipt DB path.
func (c *Config) ResolveReceiptDBPath() string {
	if c.Storage.ReceiptDBPath != "" {
		return c.Storage.ReceiptDBPath
	}
	return c.Storage.DataDir + "/receipts"
}

// ResolveTxIndexDBPath returns the resolved tx index DB path.
func (c *Config) ResolveTxIndexDBPath() string {
	if c.Storage.TxIndexDBPath != "" {
		return c.Storage.TxIndexDBPath
	}
	return c.Storage.DataDir + "/txindex"
}
