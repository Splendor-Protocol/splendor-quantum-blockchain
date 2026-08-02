package types

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// Account represents the on-chain state of a Splendor QC account.
// Balances are stored in the smallest SPLD unit (1 SPLD = 10^18 units).
type Account struct {
	// The account's quantum address (32 bytes, derived from Dilithium pubkey).
	Address qcrypto.Address `json:"address"`

	// Current SPLD balance in smallest unit.
	Balance *big.Int `json:"balance"`

	// Monotonically increasing transaction counter. Prevents replay attacks.
	Nonce uint64 `json:"nonce"`

	// The account's Dilithium public key (1952 bytes).
	// Registered on first outbound transaction.
	// Empty for accounts that have only ever received funds.
	PublicKey []byte `json:"public_key,omitempty"`

	// For contract accounts: the deployed QSol bytecode hash (SHA3-256).
	// Empty for externally owned accounts (EOA).
	CodeHash [32]byte `json:"code_hash,omitempty"`

	// For contract accounts: the SHA3-256 root of the contract's storage trie.
	StorageRoot [32]byte `json:"storage_root,omitempty"`

	// True if this account has deployed contract code.
	IsContract bool `json:"is_contract"`
}

// NewAccount creates a new empty account with zero balance and nonce.
func NewAccount(addr qcrypto.Address) *Account {
	return &Account{
		Address: addr,
		Balance: big.NewInt(0),
		Nonce:   0,
	}
}

// AddBalance adds the given amount to the account balance.
// amount must be non-negative.
func (a *Account) AddBalance(amount *big.Int) {
	if a.Balance == nil {
		a.Balance = big.NewInt(0)
	}
	a.Balance.Add(a.Balance, amount)
}

// SubBalance subtracts the given amount from the account balance.
// Returns an error if the balance would go negative.
func (a *Account) SubBalance(amount *big.Int) error {
	if a.Balance == nil {
		a.Balance = big.NewInt(0)
	}
	if a.Balance.Cmp(amount) < 0 {
		return ErrInsufficientBalance
	}
	a.Balance.Sub(a.Balance, amount)
	return nil
}

// IncrNonce increments the account nonce by 1.
func (a *Account) IncrNonce() {
	a.Nonce++
}

// HasPublicKey returns true if the account has a registered Dilithium public key.
func (a *Account) HasPublicKey() bool {
	return len(a.PublicKey) == qcrypto.PublicKeySize
}

// Copy returns a deep copy of the account.
func (a *Account) Copy() *Account {
	cp := &Account{
		Address:     a.Address,
		Nonce:       a.Nonce,
		IsContract:  a.IsContract,
		CodeHash:    a.CodeHash,
		StorageRoot: a.StorageRoot,
	}
	if a.Balance != nil {
		cp.Balance = new(big.Int).Set(a.Balance)
	} else {
		cp.Balance = big.NewInt(0)
	}
	if len(a.PublicKey) > 0 {
		cp.PublicKey = make([]byte, len(a.PublicKey))
		copy(cp.PublicKey, a.PublicKey)
	}
	return cp
}
