package types

import (
	"encoding/json"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// BlockHeader contains the metadata for a block.
// All hashes use SHA-3-256. The block is signed by the validator using Dilithium.
type BlockHeader struct {
	// Chain metadata
	Height    uint64 `json:"height"`
	Timestamp int64  `json:"timestamp"`
	ChainID   string `json:"chain_id"`

	// Hash of the previous block header (SHA3-256).
	// All zeros for genesis block.
	PrevHash [32]byte `json:"prev_hash"`

	// SHA3-256 Merkle root of all transactions in this block.
	TxMerkleRoot [32]byte `json:"tx_merkle_root"`

	// State commitment after applying this block: a flat SHA3-256 hash over the
	// sorted set of account records (NOT a Sparse Merkle Tree — an SMT is future
	// work). NOTE: this covers accounts only; contract code and storage slots are
	// not yet included in the commitment.
	StateRoot [32]byte `json:"state_root"`

	// Address of the validator that produced this block.
	ValidatorAddr qcrypto.Address `json:"validator_addr"`

	// Number of transactions in this block.
	TxCount uint32 `json:"tx_count"`
}

// Block is a complete signed block on the Splendor Quantum Chain.
type Block struct {
	Header BlockHeader `json:"header"`

	// SHA3-256 of the serialised BlockHeader. This is the canonical block ID.
	Hash [32]byte `json:"hash"`

	// ML-DSA-65 signature (3309 bytes) by the validator over the block hash.
	ValidatorSignature []byte `json:"validator_signature"`

	// ValidatorPubKey is the 1952-byte Dilithium public key of the validator.
	// Only populated on the genesis block (height 0) so that full nodes and
	// RPC nodes can bootstrap signature verification without a local genesis.json.
	// For all other blocks this is nil (pubkey is looked up from state).
	ValidatorPubKey []byte `json:"validator_pubkey,omitempty"`

	// Ordered list of transactions included in this block.
	Transactions []*Transaction `json:"transactions"`
}

// ComputeHash computes SHA3-256 of the serialised block header.
// The result is stored in Block.Hash when the block is finalised.
func (b *Block) ComputeHash() ([32]byte, error) {
	data, err := json.Marshal(b.Header)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal block header: %w", err)
	}
	return qcrypto.Hash32(data), nil
}

// ComputeTxMerkleRoot builds a SHA3-256 Merkle tree over the transaction hashes
// and returns the root hash.
func ComputeTxMerkleRoot(txs []*Transaction) ([32]byte, error) {
	if len(txs) == 0 {
		// Empty block: root is SHA3-256 of empty string
		return qcrypto.Hash32([]byte{}), nil
	}

	// Compute leaf hashes (full hash of each signed transaction)
	leaves := make([][32]byte, len(txs))
	for i, tx := range txs {
		h, err := tx.FullHash()
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to hash tx %d: %w", i, err)
		}
		leaves[i] = h
	}

	return merkleRoot(leaves), nil
}

// merkleRoot recursively computes the Merkle root of a list of SHA3-256 hashes.
func merkleRoot(hashes [][32]byte) [32]byte {
	if len(hashes) == 1 {
		return hashes[0]
	}
	// If odd number, duplicate the last hash
	if len(hashes)%2 != 0 {
		hashes = append(hashes, hashes[len(hashes)-1])
	}
	var parents [][32]byte
	for i := 0; i < len(hashes); i += 2 {
		combined := append(hashes[i][:], hashes[i+1][:]...)
		parent := qcrypto.Hash32(combined)
		parents = append(parents, parent)
	}
	return merkleRoot(parents)
}

// SignBlock signs the block hash using the given Dilithium private key.
// The Block.Hash must be set before calling this.
func (b *Block) SignBlock(priv *qcrypto.PrivateKey) error {
	sig, err := qcrypto.Sign(priv, b.Hash[:])
	if err != nil {
		return fmt.Errorf("failed to sign block: %w", err)
	}
	b.ValidatorSignature = sig
	return nil
}

// VerifySignature verifies the validator's Dilithium signature on this block.
func (b *Block) VerifySignature(validatorPubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(validatorPubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid validator public key: %w", err)
	}
	if !qcrypto.Verify(pub, b.Hash[:], b.ValidatorSignature) {
		return fmt.Errorf("invalid block validator signature at height %d", b.Header.Height)
	}
	return nil
}

// Validate performs structural validation of a block.
// It verifies: hash integrity, merkle root, and basic header consistency.
// It does NOT verify transaction signatures (done during block execution).
func (b *Block) Validate() error {
	// Recompute and check the block hash
	computed, err := b.ComputeHash()
	if err != nil {
		return fmt.Errorf("failed to compute block hash: %w", err)
	}
	if computed != b.Hash {
		return fmt.Errorf("block hash mismatch at height %d", b.Header.Height)
	}

	// Verify tx merkle root
	merkleRoot, err := ComputeTxMerkleRoot(b.Transactions)
	if err != nil {
		return fmt.Errorf("failed to compute merkle root: %w", err)
	}
	if merkleRoot != b.Header.TxMerkleRoot {
		return fmt.Errorf("transaction merkle root mismatch at height %d", b.Header.Height)
	}

	// Verify tx count matches
	if uint32(len(b.Transactions)) != b.Header.TxCount {
		return fmt.Errorf("tx count mismatch: header says %d, got %d",
			b.Header.TxCount, len(b.Transactions))
	}

	// Signature must be present
	if len(b.ValidatorSignature) != qcrypto.SignatureSize {
		return fmt.Errorf("missing or invalid validator signature")
	}

	return nil
}

// GenesisBlock creates the genesis block (block 0) for the chain.
// The genesis block has an all-zero PrevHash and contains no transactions.
func GenesisBlock(validatorAddr qcrypto.Address, stateRoot [32]byte, timestamp int64) *Block {
	header := BlockHeader{
		Height:        0,
		Timestamp:     timestamp,
		ChainID:       ChainID,
		PrevHash:      [32]byte{}, // all zeros
		TxMerkleRoot:  qcrypto.Hash32([]byte{}),
		StateRoot:     stateRoot,
		ValidatorAddr: validatorAddr,
		TxCount:       0,
	}
	b := &Block{
		Header:       header,
		Transactions: nil,
	}
	return b
}
