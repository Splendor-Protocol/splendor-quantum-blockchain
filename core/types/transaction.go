// Package types defines the core data structures for Splendor Quantum Chain.
// All hashing uses SHA-3-256. All signing uses ML-DSA-65.
package types

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// TxType identifies the kind of transaction.
type TxType uint8

const (
	TxTransfer TxType = 0x01 // Transfer SPLD between accounts
	TxDeploy   TxType = 0x02 // Deploy a new smart contract
	TxCall     TxType = 0x03 // Call a smart contract function
	TxRegister TxType = 0x04 // Register public key on first use (auto on first Transfer)
	TxX402     TxType = 0x05 // X402 micropayment settlement
)

// DefaultChainID is the id this binary assumes when no genesis document names
// one. It is the id every tier shared before per-tier ids existed, so an
// unconfigured node still speaks to the network it always spoke to.
const DefaultChainID = "splendor-quantum-1"

// ChainID is the chain this node belongs to. It is signed into every
// transaction body (see TransactionBody) and stamped into every block header,
// so it is what separates one network's transactions from another's: a
// transaction is only valid on the chain whose id it was signed for.
//
// It is a variable rather than a constant because one binary has to serve
// devnet, testnet and mainnet, and because the id these tiers ran on was
// identical — which made a transaction signed on devnet byte-for-byte valid on
// mainnet as soon as the nonces lined up. Sourcing it from the genesis document
// is what lets the tiers be told apart without shipping three binaries.
//
// Write it exactly once, through SetChainID, during startup and before any
// goroutine that reads it exists. It is deliberately not mutex-guarded: after
// startup it is read on every transaction admission and block apply, and a lock
// on that path would cost more than it protects. Never assign to it directly.
var ChainID = DefaultChainID

// chainIDAdopted records that SetChainID has run, so a second, contradictory
// call is an error rather than a silent switch of networks mid-process.
var chainIDAdopted bool

// SetChainID adopts the chain id named by a validated genesis document.
//
// Calling it twice with the same id is a no-op, so a node that verifies its
// genesis on more than one path does not trip over itself. Calling it twice
// with DIFFERENT ids is refused: that would mean two genesis documents
// disagreed about which network this is, and continuing would let blocks from
// one chain be admitted under the identity of another.
func SetChainID(id string) error {
	if err := ValidateChainID(id); err != nil {
		return err
	}
	if chainIDAdopted && ChainID != id {
		return fmt.Errorf("chain id is already %q and cannot be changed to %q: "+
			"two genesis documents disagree about which network this node is on", ChainID, id)
	}
	ChainID = id
	chainIDAdopted = true
	return nil
}

// ValidateChainID rejects ids that could not have come from a genuine genesis.
//
// The id is signed into every transaction and stamped into every header, so it
// travels far and is compared byte-for-byte in a lot of places. Keeping it to a
// short, boring alphabet means it cannot smuggle whitespace, a newline or a
// homoglyph past a log line or a config file and be mistaken for another tier.
func ValidateChainID(id string) error {
	if id == "" {
		return fmt.Errorf("chain id is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("chain id %q is longer than 64 characters", id)
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("chain id %q contains %q; only letters, digits, '-', '_' and '.' are allowed", id, r)
		}
	}
	return nil
}

// Transaction is the fundamental unit of state change on Splendor Quantum Chain.
// All transactions are signed with ML-DSA-65 (NIST FIPS 204).
// The hash is computed as SHA3-256(JSON-serialised transaction body).
type Transaction struct {
	// Header
	Version uint8  `json:"version"`
	ChainID string `json:"chain_id"`
	Type    TxType `json:"type"`

	// AlgID names the signature scheme this transaction is signed with.
	// Part of the signed body: a verifier that does not implement the named
	// scheme rejects the tx rather than guessing, and an attacker cannot
	// downgrade a tx to a weaker scheme without invalidating the signature.
	AlgID string `json:"alg_id"`

	// Sender / Receiver
	From qcrypto.Address `json:"from"`
	To   qcrypto.Address `json:"to"`

	// Value in smallest SPLD unit (1 SPLD = 10^18 units)
	Amount *big.Int `json:"amount"`

	// Fee in smallest unit
	Fee *big.Int `json:"fee"`

	// Replay protection: monotonically increasing per-account counter
	Nonce uint64 `json:"nonce"`

	// Unix timestamp in seconds
	Timestamp int64 `json:"timestamp"`

	// Optional memo / data payload
	// For TxDeploy: contains compiled QSol bytecode
	// For TxCall:   contains ABI-encoded function call
	// For TxTransfer/TxX402: optional memo string
	Data []byte `json:"data,omitempty"`

	// Authentication
	// The sender's ML-DSA-65 public key (1952 bytes).
	// Included on first transaction; subsequent transactions look it up from state.
	PublicKey []byte `json:"public_key,omitempty"`

	// ML-DSA-65 signature (3309 bytes) over SHA3-256(canonical_body_bytes).
	Signature []byte `json:"signature,omitempty"`
}

// TransactionBody is the serialisable body that is hashed and signed.
// It excludes PublicKey and Signature fields.
type TransactionBody struct {
	Version   uint8           `json:"version"`
	ChainID   string          `json:"chain_id"`
	Type      TxType          `json:"type"`
	AlgID     string          `json:"alg_id"`
	From      qcrypto.Address `json:"from"`
	To        qcrypto.Address `json:"to"`
	Amount    *big.Int        `json:"amount"`
	Fee       *big.Int        `json:"fee"`
	Nonce     uint64          `json:"nonce"`
	Timestamp int64           `json:"timestamp"`
	Data      []byte          `json:"data,omitempty"`
}

// Hash computes SHA3-256 of the canonical transaction body.
// This is the value that must be signed by the sender.
func (tx *Transaction) Hash() ([32]byte, error) {
	body := TransactionBody{
		Version:   tx.Version,
		ChainID:   tx.ChainID,
		Type:      tx.Type,
		AlgID:     tx.AlgID,
		From:      tx.From,
		To:        tx.To,
		Amount:    tx.Amount,
		Fee:       tx.Fee,
		Nonce:     tx.Nonce,
		Timestamp: tx.Timestamp,
		Data:      tx.Data,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal transaction body: %w", err)
	}
	return qcrypto.Hash32(b), nil
}

// FullHash computes SHA3-256 of the complete signed transaction (including signature).
// This is used as the transaction ID stored on chain.
func (tx *Transaction) FullHash() ([32]byte, error) {
	b, err := json.Marshal(tx)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal transaction: %w", err)
	}
	return qcrypto.Hash32(b), nil
}

// Validate performs basic structural validation of a transaction.
// It does NOT verify the signature (use VerifySignature for that).
func (tx *Transaction) Validate() error {
	if tx.Version == 0 {
		return fmt.Errorf("invalid version 0")
	}
	if tx.ChainID != ChainID {
		return fmt.Errorf("wrong chain ID: got %q, want %q", tx.ChainID, ChainID)
	}
	if tx.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported signature algorithm %q: this node only verifies %q",
			tx.AlgID, qcrypto.AlgMLDSA65)
	}
	if tx.From.IsZero() {
		return fmt.Errorf("zero from address")
	}
	if tx.Amount == nil || tx.Amount.Sign() < 0 {
		return fmt.Errorf("invalid amount")
	}
	if tx.Fee == nil || tx.Fee.Sign() < 0 {
		return fmt.Errorf("invalid fee")
	}
	if len(tx.Signature) != qcrypto.SignatureSize {
		return fmt.Errorf("wrong signature length: got %d, want %d", len(tx.Signature), qcrypto.SignatureSize)
	}
	return nil
}

// VerifySignature verifies the ML-DSA-65 signature on a transaction.
// pubKeyBytes must be the sender's registered ML-DSA-65 public key.
func (tx *Transaction) VerifySignature(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	hash, err := tx.Hash()
	if err != nil {
		return fmt.Errorf("failed to compute tx hash: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxTransaction, tx.Signature) {
		return fmt.Errorf("invalid ML-DSA signature")
	}
	return nil
}

// Sign signs the transaction using the given ML-DSA-65 private key.
// It sets the Signature field and optionally the PublicKey field.
func (tx *Transaction) Sign(priv *qcrypto.PrivateKey, includePublicKey bool) error {
	if tx.AlgID == "" {
		tx.AlgID = qcrypto.AlgMLDSA65
	}
	hash, err := tx.Hash()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, hash[:], qcrypto.CtxTransaction)
	if err != nil {
		return fmt.Errorf("signing failed: %w", err)
	}
	tx.Signature = sig
	if includePublicKey {
		tx.PublicKey = priv.Public().Bytes()
	}
	return nil
}

// NewTransfer creates an unsigned transfer transaction.
func NewTransfer(from, to qcrypto.Address, amount, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxTransfer,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		To:        to,
		Amount:    new(big.Int).Set(amount),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
	}
}

// NewDeploy creates an unsigned contract deployment transaction.
func NewDeploy(from qcrypto.Address, bytecode []byte, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxDeploy,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		Amount:    big.NewInt(0),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
		Data:      bytecode,
	}
}

// NewCall creates an unsigned smart contract call transaction.
func NewCall(from, contract qcrypto.Address, calldata []byte, value, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxCall,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		To:        contract,
		Amount:    new(big.Int).Set(value),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
		Data:      calldata,
	}
}
