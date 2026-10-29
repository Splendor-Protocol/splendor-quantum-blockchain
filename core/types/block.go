package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// BlockHeader contains the metadata for a block.
// All hashes use SHA-3-256. The block is signed by the validator using ML-DSA-65.
type BlockHeader struct {
	// Chain metadata
	Height    uint64 `json:"height"`
	Timestamp int64  `json:"timestamp"`
	ChainID   string `json:"chain_id"`

	// AlgID names the signature scheme the validator signed this block with.
	// Covered by the block hash, so it cannot be altered after signing.
	AlgID string `json:"alg_id"`

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

	// GenesisHash commits to the hash of block 0, anchoring chain identity.
	// When set (non-zero), any block whose GenesisHash does not match the
	// node's adopted genesis hash is rejected, regardless of ChainID matching.
	// This prevents cross-network replay attacks where a spoofed ChainID
	// could still reference a different chain's genesis.
	GenesisHash [32]byte `json:"genesis_hash,omitempty"`

	// QCRoot commits to the quorum certificate that justified this block, once
	// the chain runs a validator set rather than a single producer. It is nil
	// under single-validator consensus and MUST stay nil until the
	// multi_validator upgrade activates — see Validate.
	//
	// It is reserved here, ahead of the code that will populate it, because a
	// header field cannot be added later without cost. The block hash is taken
	// over this struct, so any field appearing in the JSON changes every block
	// hash on the chain; the only way to introduce one after launch is to start
	// the chain again. Declaring it now is free instead: it is a slice with
	// omitempty, so while it is nil encoding/json omits it entirely and the
	// header serialises byte-for-byte as it did without the field. A [32]byte
	// array would NOT have worked — arrays are never "empty" to encoding/json,
	// so one would serialise from block 0 and change every hash today.
	//
	// Committing the certificate in the header, rather than only alongside the
	// block, is what lets a client verify finality from headers alone.
	QCRoot []byte `json:"qc_root,omitempty"`
}

// Block is a complete signed block on the Splendor Quantum Chain.
type Block struct {
	Header BlockHeader `json:"header"`

	// SHA3-256 of the serialised BlockHeader. This is the canonical block ID.
	Hash [32]byte `json:"hash"`

	// ML-DSA-65 signature (3309 bytes) by the validator over the block hash.
	ValidatorSignature []byte `json:"validator_signature"`

	// ValidatorPubKey is the 1952-byte ML-DSA-65 public key of the validator.
	// Only populated on the genesis block (height 0) so that full nodes and
	// RPC nodes can bootstrap signature verification without a local genesis.json.
	// For all other blocks this is nil (pubkey is looked up from state).
	ValidatorPubKey []byte `json:"validator_pubkey,omitempty"`

	// Ordered list of transactions included in this block.
	Transactions []*Transaction `json:"transactions"`

	// Votes is the quorum certificate justifying this block under
	// multi_validator. Not part of the voted hash (see ComputeVotedHash).
	Votes []QCVote `json:"votes,omitempty"`
}

const (
	// VoteKindPrevote and VoteKindPrecommit are the bft_rounds_v1 ballot
	// kinds. Empty Kind is the pre-fork single vote, and its digest must
	// stay bit-identical so existing certificates still verify.
	VoteKindPrevote   = "prevote"
	VoteKindPrecommit = "precommit"
)

// QCVote is a validator vote stored with a block as a quorum certificate.
type QCVote struct {
	AlgID         string          `json:"alg_id"`
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	BlockHash     [32]byte        `json:"block_hash"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Signature     []byte          `json:"signature"`
	Timestamp     int64           `json:"timestamp"`
	Kind          string          `json:"kind,omitempty"`
}

// VoteDigest is the signed vote body. Empty kind is the legacy body.
func VoteDigest(algID string, height uint64, round uint32, blockHash [32]byte, addr qcrypto.Address, ts int64, kind string) ([32]byte, error) {
	var (
		b   []byte
		err error
	)
	if kind == "" {
		b, err = json.Marshal(struct {
			AlgID         string          `json:"alg_id"`
			Height        uint64          `json:"height"`
			Round         uint32          `json:"round"`
			BlockHash     [32]byte        `json:"block_hash"`
			ValidatorAddr qcrypto.Address `json:"validator_addr"`
			Timestamp     int64           `json:"timestamp"`
		}{algID, height, round, blockHash, addr, ts})
	} else {
		b, err = json.Marshal(struct {
			AlgID         string          `json:"alg_id"`
			Height        uint64          `json:"height"`
			Round         uint32          `json:"round"`
			BlockHash     [32]byte        `json:"block_hash"`
			ValidatorAddr qcrypto.Address `json:"validator_addr"`
			Timestamp     int64           `json:"timestamp"`
			Kind          string          `json:"kind"`
		}{algID, height, round, blockHash, addr, ts, kind})
	}
	if err != nil {
		return [32]byte{}, err
	}
	return qcrypto.Hash32(b), nil
}

// Hash is the signed vote body (excludes Signature), matching consensus.Vote.
func (v QCVote) Hash() ([32]byte, error) {
	return VoteDigest(v.AlgID, v.Height, v.Round, v.BlockHash, v.ValidatorAddr, v.Timestamp, v.Kind)
}

// Verify checks the ML-DSA-65 vote signature against pubKeyBytes.
func (v QCVote) Verify(pubKeyBytes []byte) error {
	if v.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported vote alg_id %q", v.AlgID)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("vote pubkey: %w", err)
	}
	if qcrypto.DeriveAddress(pub) != v.ValidatorAddr {
		return fmt.Errorf("vote claims %s but key belongs to %s", v.ValidatorAddr, qcrypto.DeriveAddress(pub))
	}
	hash, err := v.Hash()
	if err != nil {
		return err
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxVote, v.Signature) {
		return fmt.Errorf("invalid vote signature from %s", v.ValidatorAddr)
	}
	return nil
}

// ComputeQCRoot is SHA3-256 over the canonical vote set (sorted by address).
func ComputeQCRoot(votes []QCVote) [32]byte {
	type body struct {
		AlgID         string          `json:"alg_id"`
		Height        uint64          `json:"height"`
		Round         uint32          `json:"round"`
		BlockHash     [32]byte        `json:"block_hash"`
		ValidatorAddr qcrypto.Address `json:"validator_addr"`
		Timestamp     int64           `json:"timestamp"`
		Signature     []byte          `json:"signature"`
	}
	cp := append([]QCVote(nil), votes...)
	sort.Slice(cp, func(i, j int) bool {
		return bytes.Compare(cp[i].ValidatorAddr[:], cp[j].ValidatorAddr[:]) < 0
	})
	raw, _ := json.Marshal(cp)
	return qcrypto.Hash32(raw)
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

// ComputeVotedHash is the hash validators sign and vote on. QCRoot is omitted
// so a certificate can be attached after the proposal is signed without
// changing the voted identity.
func (b *Block) ComputeVotedHash() ([32]byte, error) {
	h := b.Header
	h.QCRoot = nil
	data, err := json.Marshal(h)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal voted header: %w", err)
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

// SignBlock signs the block hash using the given ML-DSA-65 private key.
// The Block.Hash must be set before calling this.
func (b *Block) SignBlock(priv *qcrypto.PrivateKey) error {
	sig, err := qcrypto.Sign(priv, b.Hash[:], qcrypto.CtxBlock)
	if err != nil {
		return fmt.Errorf("failed to sign block: %w", err)
	}
	b.ValidatorSignature = sig
	return nil
}

// VerifySignature verifies the validator's ML-DSA-65 signature on this block.
func (b *Block) VerifySignature(validatorPubKeyBytes []byte) error {
	if b.Header.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported block signature algorithm %q: this node only verifies %q",
			b.Header.AlgID, qcrypto.AlgMLDSA65)
	}
	pub, err := qcrypto.PublicKeyFromBytes(validatorPubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid validator public key: %w", err)
	}
	if !qcrypto.Verify(pub, b.Hash[:], qcrypto.CtxBlock, b.ValidatorSignature) {
		return fmt.Errorf("invalid block validator signature at height %d", b.Header.Height)
	}
	return nil
}

// Validate performs structural validation of a block.
// It verifies: hash integrity, merkle root, and basic header consistency.
// It does NOT verify transaction signatures (done during block execution).
func (b *Block) Validate() error {
	voted, err := b.ComputeVotedHash()
	if err != nil {
		return fmt.Errorf("failed to compute block hash: %w", err)
	}
	if voted != b.Hash {
		return fmt.Errorf("block hash mismatch at height %d", b.Header.Height)
	}

	if len(b.Header.QCRoot) != 0 {
		if len(b.Votes) == 0 {
			return fmt.Errorf("block %d carries qc_root but no votes", b.Header.Height)
		}
		root := ComputeQCRoot(b.Votes)
		if !bytes.Equal(b.Header.QCRoot, root[:]) {
			return fmt.Errorf("block %d qc_root does not match votes", b.Header.Height)
		}
		for _, v := range b.Votes {
			if v.BlockHash != b.Hash {
				return fmt.Errorf("block %d vote from %s is for a different hash", b.Header.Height, v.ValidatorAddr)
			}
		}
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
		AlgID:         qcrypto.AlgMLDSA65,
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
