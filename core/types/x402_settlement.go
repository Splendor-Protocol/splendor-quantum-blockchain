package types

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// X402Settlement is the on-chain record of a confirmed X402 payment, written by
// the executor when a TxX402 actually transfers funds. It binds the payment
// nonce (the registry key, SHA3-256(from || nonce)) to the payer, payee, amount,
// chain and the transaction that settled it.
//
// A used nonce can therefore only ever re-confirm the payment that really
// happened: an already-consumed nonce presented with a changed recipient,
// amount or (unverifiable) chain must not be treated as confirmed payment.
type X402Settlement struct {
	// From is the payer.
	From qcrypto.Address `json:"from"`
	// To is the payee that was actually credited.
	To qcrypto.Address `json:"to"`
	// Amount is the value that was actually transferred.
	Amount *big.Int `json:"amount"`
	// ChainID is the chain the settlement transaction committed to.
	ChainID string `json:"chain_id"`
	// TxHash is the full SHA3-256 hash of the signed settlement transaction.
	TxHash [32]byte `json:"tx_hash"`
	// Height is the block height that committed the settlement.
	Height uint64 `json:"height"`
}

// Matches reports whether the settlement record binds exactly the supplied
// payment details. The nonce itself is implicit in the registry key the record
// is stored under.
func (s *X402Settlement) Matches(chainID string, from, to qcrypto.Address, amount *big.Int) bool {
	if s == nil {
		return false
	}
	if s.ChainID != chainID || s.From != from || s.To != to {
		return false
	}
	if s.Amount == nil || amount == nil {
		return s.Amount == nil && amount == nil
	}
	return s.Amount.Cmp(amount) == 0
}
