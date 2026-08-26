// Package genesis handles the creation and loading of the Splendor Quantum Chain genesis state.
// The genesis file defines the initial account balances and the validator public key.
// All addresses use ML-DSA-65-derived SQ: format. No ECDSA.
package genesis

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
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

	// ValidatorPubKeyHex is the hex-encoded 1952-byte ML-DSA-65 public key
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

// ValidatorPublicKey returns the parsed ML-DSA-65 public key of the genesis validator.
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

	// Sign with validator ML-DSA-65 key
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
	// A duplicate address makes the document ambiguous about how much the chain
	// starts with: ApplyToState overwrites, so state ends up holding one of the
	// two balances, while the supply arithmetic below would count both. This is
	// not hypothetical -- the deploy entrypoint always allocates the validator
	// its treasury, and a balance snapshot taken off a live tier naturally
	// contains that same validator address, so the two meet in one document the
	// first time anyone snapshots a tier and resets it.
	seen := make(map[string]int, len(g.Allocations))
	for i, alloc := range g.Allocations {
		key := strings.ToLower(alloc.Address)
		if first, dup := seen[key]; dup {
			return fmt.Errorf("genesis allocation[%d]: address %s is already allocated at [%d]: "+
				"a duplicate makes the starting supply ambiguous", i, alloc.Address, first)
		}
		seen[key] = i
	}
	// total_supply has always been documented as the sum of the allocations, but
	// nothing ever checked it, and so it drifted: every live tier declares less
	// than it actually minted, mainnet by a whole billion SPLD. Since the value
	// is hashed into the genesis identity, a wrong one is not cosmetic — it is a
	// figure every reader of the document is entitled to trust.
	//
	// An empty value means "compute it", which is how the deploy entrypoint now
	// writes the document: the node holds the arithmetic, so a shell script
	// cannot get it wrong, and the alpine image has neither python nor bc nor
	// integers wide enough to add these numbers anyway.
	sum := g.SumAllocations()
	if g.TotalSupply == "" {
		g.TotalSupply = sum.String()
		return nil
	}
	declared, ok := new(big.Int).SetString(g.TotalSupply, 10)
	if !ok || declared.Sign() < 0 {
		return fmt.Errorf("genesis: invalid total_supply %q", g.TotalSupply)
	}
	if declared.Cmp(sum) != 0 {
		return fmt.Errorf("genesis: total_supply is %s but the allocations sum to %s — "+
			"the document would mint %s more than it declares",
			declared, sum, new(big.Int).Sub(sum, declared))
	}
	return nil
}

// SumAllocations totals every allocation in the document.
//
// This is what total_supply means: the amount this chain begins with. Nothing
// else mints at genesis, so the sum is the whole of it.
func (g *Genesis) SumAllocations() *big.Int {
	sum := new(big.Int)
	for _, alloc := range g.Allocations {
		if b, ok := new(big.Int).SetString(alloc.Balance, 10); ok && b.Sign() >= 0 {
			sum.Add(sum, b)
		}
	}
	return sum
}

// genesisHashDomain separates a genesis-document hash from every other SHA3-256
// preimage on this chain, so no value hashed for another purpose can ever be
// replayed as a genesis pin.
const genesisHashDomain = "SPLENDOR-QC-GENESIS-v1"

// Hash returns the canonical SHA3-256 identity of the genesis document — the
// value an operator pins with --genesis-doc-hash so a node can tell the real
// network's genesis from one a bootstrap peer invented.
//
// The preimage is NOT the JSON file. Whitespace, key order and indentation must
// not change a chain's identity, so the hash is taken over a length-prefixed
// encoding of exactly the fields that decide what the chain starts as, in this
// fixed order:
//
//	domain tag, chain_id, network_name, timestamp (8-byte big-endian),
//	validator_pubkey_hex, allocation count (8-byte big-endian), then each
//	allocation's address and balance in document order, then total_supply.
//
// Every field carries its own 8-byte big-endian length, so no two different
// documents can collide by shifting bytes across a field boundary ("ab"+"c"
// and "a"+"bc" hash differently).
//
// Hex-bearing fields are lowercased first: hex decoding is case-insensitive, so
// two documents differing only in case produce byte-identical state and must
// not get different identities. Balances are hashed exactly as written — a
// decimal string is its own canonical form here, and re-encoding it through
// big.Int would give Hash an error path on precisely the documents it exists to
// fingerprint BEFORE they are validated.
//
// Allocation ORDER is part of the identity because it is part of the state:
// ApplyToState replaces an account it has already written rather than adding to
// it, so two orderings of the same allocations can fund different balances.
//
// Comments are excluded. They are operator labels with no consensus weight, and
// a pin that broke when someone fixed a typo in one is a pin operators would
// learn to switch off.
func (g *Genesis) Hash() [32]byte {
	var buf bytes.Buffer

	field := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		buf.Write(n[:])
		buf.Write(b)
	}
	number := func(v uint64) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], v)
		field(n[:])
	}

	field([]byte(genesisHashDomain))
	field([]byte(g.ChainID))
	field([]byte(g.NetworkName))
	number(uint64(g.Timestamp))
	field([]byte(strings.ToLower(g.ValidatorPubKeyHex)))
	number(uint64(len(g.Allocations)))
	for _, alloc := range g.Allocations {
		field([]byte(strings.ToLower(alloc.Address)))
		field([]byte(alloc.Balance))
	}
	field([]byte(g.TotalSupply))

	return qcrypto.Hash32(buf.Bytes())
}

// HashHex returns Hash in the lowercase hex form --genesis-doc-hash takes.
func (g *Genesis) HashHex() string {
	h := g.Hash()
	return hex.EncodeToString(h[:])
}
