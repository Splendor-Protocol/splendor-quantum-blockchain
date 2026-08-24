// Package types defines the core data structures for Splendor Quantum Chain.
// All hashing uses SHA-3-256. All signing uses Dilithium Mode 3.
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
	TxTransfer  TxType = 0x01 // Transfer SPLD between accounts
	TxDeploy    TxType = 0x02 // Deploy a new smart contract
	TxCall      TxType = 0x03 // Call a smart contract function
	TxRegister  TxType = 0x04 // Register public key on first use (auto on first Transfer)
	TxX402      TxType = 0x05 // X402 micropayment settlement
)

// ChainID is the unique identifier for Splendor Quantum Chain.
const ChainID = "splendor-quantum-1"

// Transaction is the fundamental unit of state change on Splendor Quantum Chain.
// All transactions are signed with Dilithium Mode 3 (round-3, not FIPS-204 ML-DSA).
// The hash is computed as SHA3-256(JSON-serialised transaction body).
type Transaction struct {
	// Header
	Version   uint8  `json:"version"`
	ChainID   string `json:"chain_id"`
	Type      TxType `json:"type"`

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
	// The sender's Dilithium public key (1952 bytes).
	// Included on first transaction; subsequent transactions look it up from state.
	PublicKey []byte `json:"public_key,omitempty"`

	// Dilithium Mode 3 signature (3293 bytes) over SHA3-256(canonical_body_bytes).
	Signature []byte `json:"signature,omitempty"`
}

// TransactionBody is the serialisable body that is hashed and signed.
// It excludes PublicKey and Signature fields.
type TransactionBody struct {
	Version   uint8           `json:"version"`
	ChainID   string          `json:"chain_id"`
	Type      TxType          `json:"type"`
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

// VerifySignature verifies the Dilithium signature on a transaction.
// pubKeyBytes must be the sender's registered Dilithium public key.
func (tx *Transaction) VerifySignature(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	hash, err := tx.Hash()
	if err != nil {
		return fmt.Errorf("failed to compute tx hash: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], tx.Signature) {
		return fmt.Errorf("invalid Dilithium signature")
	}
	return nil
}

// Sign signs the transaction using the given Dilithium private key.
// It sets the Signature field and optionally the PublicKey field.
func (tx *Transaction) Sign(priv *qcrypto.PrivateKey, includePublicKey bool) error {
	hash, err := tx.Hash()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, hash[:])
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
		From:      from,
		To:        contract,
		Amount:    new(big.Int).Set(value),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
		Data:      calldata,
	}
}
