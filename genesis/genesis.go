// Package genesis handles the creation and loading of the Splendor Quantum Chain genesis state.
// The genesis file defines the initial account balances and the validator public key.
// All addresses use Dilithium-derived SQ: format. No ECDSA.
package genesis

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Allocation defines an initial account balance in the genesis state.
type Allocation struct {
	// Address in SQ: format.
	Address string `json:"address"`

	// Balance in smallest SPLD units (1 SPLD = 10^18).
	Balance string `json:"balance"`

	// Comment is an optional human-readable label.
	Comment string `json:"comment,omitempty"`
}

// Genesis defines the initial state of the chain.
type Genesis struct {
	// ChainID must match types.ChainID.
	ChainID string `json:"chain_id"`

	// NetworkName is a human-readable name shown in logs and the explorer.
	NetworkName string `json:"network_name"`

	// Timestamp is the unix timestamp of block 0.
	Timestamp int64 `json:"timestamp"`

	// ValidatorPubKeyHex is the hex-encoded 1952-byte Dilithium public key
	// of the genesis validator. This key's address is the validator address
	// used in the genesis block header.
	ValidatorPubKeyHex string `json:"validator_pubkey_hex"`

	// Allocations are the initial account balances.
	Allocations []Allocation `json:"allocations"`

	// TotalSupply is informational (sum of all allocations).
	TotalSupply string `json:"total_supply"`
}

// DefaultGenesis returns a genesis configuration for local development / testnet.
// It creates a single funded "faucet" account. Replace with real allocations for mainnet.
func DefaultGenesis(validatorPubKeyHex string) *Genesis {
	return &Genesis{
		ChainID:            types.ChainID,
		NetworkName:        "Splendor Quantum Chain Mainnet",
		Timestamp:          time.Now().Unix(),
		ValidatorPubKeyHex: validatorPubKeyHex,
		Allocations: []Allocation{
			{
				Address: "SQ:0000000000000000000000000000000000000000000000000000000000000001",
				Balance: "1000000000000000000000000000", // 1 billion SPLD
				Comment: "Development faucet — replace on mainnet",
			},
		},
		TotalSupply: "26000000000000000000000000000", // 26 billion SPLD
	}
}

// LoadFile reads a genesis.json file.
func LoadFile(path string) (*Genesis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read genesis file %s: %w", path, err)
	}
	var g Genesis
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("invalid genesis file %s: %w", path, err)
	}
	return &g, nil
}

// LoadFromReader parses a genesis.json from any io.Reader.
// Used by RPC nodes to fetch genesis from a validator peer's /genesis endpoint.
func LoadFromReader(r io.Reader) (*Genesis, error) {
	var g Genesis
	if err := json.NewDecoder(r).Decode(&g); err != nil {
		return nil, fmt.Errorf("invalid genesis JSON: %w", err)
	}
	return &g, nil
}

// Save writes the genesis to a JSON file.
func (g *Genesis) Save(path string) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal genesis: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ValidatorAddress derives the SQ: address of the genesis validator
// from the ValidatorPubKeyHex field.
func (g *Genesis) ValidatorAddress() (qcrypto.Address, error) {
	pubKeyBytes, err := hex.DecodeString(g.ValidatorPubKeyHex)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("invalid validator pubkey hex: %w", err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("invalid validator pubkey: %w", err)
	}
	return qcrypto.DeriveAddress(pub), nil
}

// ValidatorPublicKey returns the parsed Dilithium public key of the genesis validator.
func (g *Genesis) ValidatorPublicKey() (*qcrypto.PublicKey, error) {
	pubKeyBytes, err := hex.DecodeString(g.ValidatorPubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid validator pubkey hex: %w", err)
	}
	return qcrypto.PublicKeyFromBytes(pubKeyBytes)
}

// ApplyToState writes all genesis allocations into the provided StateDB.
// It also registers the validator's public key against its address.
// Returns the initial state root (SHA3-256 of all genesis accounts).
func (g *Genesis) ApplyToState(stateDB *state.StateDB) ([32]byte, error) {
	// Apply all account allocations
	for _, alloc := range g.Allocations {
		addr, err := qcrypto.AddressFromString(alloc.Address)
		if err != nil {
			return [32]byte{}, fmt.Errorf("invalid genesis address %q: %w", alloc.Address, err)
		}

		balance, ok := new(big.Int).SetString(alloc.Balance, 10)
		if !ok {
			return [32]byte{}, fmt.Errorf("invalid genesis balance for %s: %q", alloc.Address, alloc.Balance)
		}

		acc := types.NewAccount(addr)
		acc.AddBalance(balance)
		stateDB.SetAccount(acc)
	}

	// Register validator public key
	if g.ValidatorPubKeyHex != "" {
		pubKeyBytes, err := hex.DecodeString(g.ValidatorPubKeyHex)
		if err != nil {
			return [32]byte{}, fmt.Errorf("validator pubkey hex invalid: %w", err)
		}
		pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
		if err != nil {
			return [32]byte{}, fmt.Errorf("validator pubkey parse failed: %w", err)
		}
		validatorAddr := qcrypto.DeriveAddress(pub)
		validatorAcc, err := stateDB.GetAccount(validatorAddr)
		if err != nil {
			validatorAcc = types.NewAccount(validatorAddr)
		}
		validatorAcc.PublicKey = pubKeyBytes
		stateDB.SetAccount(validatorAcc)
	}

	// Commit all genesis accounts and return the initial state root
	stateRoot, err := stateDB.Commit()
	if err != nil {
		return [32]byte{}, fmt.Errorf("genesis state commit failed: %w", err)
	}

	return stateRoot, nil
}

// BuildGenesisBlock constructs and signs the genesis block (height 0).
// The genesis block has no transactions and the initial state root.
func BuildGenesisBlock(g *Genesis, stateRoot [32]byte, validatorPriv *qcrypto.PrivateKey) (*types.Block, error) {
	validatorAddr, err := g.ValidatorAddress()
	if err != nil {
		return nil, err
	}

	genesis := types.GenesisBlock(validatorAddr, stateRoot, g.Timestamp)

	// Compute block hash
	hash, err := genesis.ComputeHash()
	if err != nil {
		return nil, fmt.Errorf("genesis hash failed: %w", err)
	}
	genesis.Hash = hash

	// Sign with validator Dilithium key
	if err := genesis.SignBlock(validatorPriv); err != nil {
		return nil, fmt.Errorf("genesis signing failed: %w", err)
	}

	return genesis, nil
}

// Validate checks that the genesis configuration is consistent.
func (g *Genesis) Validate() error {
	if g.ChainID == "" {
		return fmt.Errorf("genesis: chain_id is required")
	}
	if g.ValidatorPubKeyHex == "" {
		return fmt.Errorf("genesis: validator_pubkey_hex is required")
	}
	if len(g.Allocations) == 0 {
		return fmt.Errorf("genesis: at least one allocation is required")
	}
	// Verify all addresses are parseable
	for i, alloc := range g.Allocations {
		if _, err := qcrypto.AddressFromString(alloc.Address); err != nil {
			return fmt.Errorf("genesis allocation[%d]: invalid address: %w", i, err)
		}
		if b, ok := new(big.Int).SetString(alloc.Balance, 10); !ok || b.Sign() < 0 {
			return fmt.Errorf("genesis allocation[%d]: invalid balance %q", i, alloc.Balance)
		}
	}
	return nil
}
