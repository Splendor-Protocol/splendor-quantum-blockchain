package types

import "errors"

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrInvalidNonce        = errors.New("invalid nonce")
	ErrInvalidSignature    = errors.New("invalid signature")
	ErrUnknownAccount      = errors.New("account not found")
	ErrContractNotFound    = errors.New("contract not found")
	ErrInvalidTransaction  = errors.New("invalid transaction")
	ErrBlockNotFound       = errors.New("block not found")
	ErrDuplicateTx         = errors.New("duplicate transaction")
	ErrMempoolFull         = errors.New("mempool is full")
	ErrWrongChainID        = errors.New("wrong chain ID")
	ErrNoPublicKey         = errors.New("no public key registered for account")
	ErrX402InvalidPayment  = errors.New("invalid X402 payment")
	ErrX402NonceUsed       = errors.New("X402 payment nonce already used")
	ErrX402Expired         = errors.New("X402 payment has expired")
)
