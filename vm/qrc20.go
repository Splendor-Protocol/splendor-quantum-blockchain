// Package vm — QRC-20: Splendor Quantum Chain native fungible token standard.
//
// QRC-20 is the quantum-native equivalent of ERC-20.
// All addresses use the SQ: format (32 bytes, SHA3-256 derived).
// All state operations go through the StateDB (no ECDSA, no keccak).
//
// A QRC-20 token is a smart contract that implements:
//   - totalSupply() → uint256
//   - balanceOf(address) → uint256
//   - transfer(to, amount) → bool
//   - approve(spender, amount) → bool
//   - allowance(owner, spender) → uint256
//   - transferFrom(from, to, amount) → bool
//
// The QRC20Token struct provides a pure-Go in-process implementation that
// the SQC VM can call natively, without needing compiled bytecode for
// standard token operations. Developers can also deploy QSol bytecode
// for custom logic on top.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// QRC20 slot keys (SHA3-256 of string identifiers — quantum-safe storage layout)
var (
	slotTotalSupply = qcrypto.Hash32([]byte("qrc20:total_supply"))
	slotName        = qcrypto.Hash32([]byte("qrc20:name"))
	slotSymbol      = qcrypto.Hash32([]byte("qrc20:symbol"))
	slotDecimals    = qcrypto.Hash32([]byte("qrc20:decimals"))
)

// balanceSlot computes the storage slot key for an account's QRC-20 balance.
// slot = SHA3-256("qrc20:balance:" + address)
func balanceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc20:balance:"), addr[:]...))
}

// allowanceSlot computes the storage slot for owner→spender allowance.
// slot = SHA3-256("qrc20:allowance:" + owner + spender)
func allowanceSlot(owner, spender qcrypto.Address) [32]byte {
	key := append([]byte("qrc20:allowance:"), owner[:]...)
	key = append(key, spender[:]...)
	return qcrypto.Hash32(key)
}

// QRC20Token is the native implementation of the QRC-20 standard.
// It stores all state in the StateDB under the contract's address.
type QRC20Token struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewQRC20Token creates a handle to an existing QRC-20 contract.
func NewQRC20Token(stateDB *state.StateDB, contract qcrypto.Address) *QRC20Token {
	return &QRC20Token{stateDB: stateDB, contract: contract}
}

// DeployQRC20 deploys a new QRC-20 token contract.
// It writes the initial state (name, symbol, decimals, total supply) to the StateDB
// and credits the entire supply to the deployer's address.
// Returns the contract address.
func DeployQRC20(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name string,
	symbol string,
	decimals uint8,
	totalSupply *big.Int,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	// Derive contract address deterministically: SHA3-256(deployer || nonce || "qrc20")
	nonceBytes := []byte(fmt.Sprintf("%d", deployerNonce))
	contractInput := append(deployer[:], nonceBytes...)
	contractInput = append(contractInput, []byte("qrc20")...)
	hash := qcrypto.Hash32(contractInput)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	token := NewQRC20Token(stateDB, contractAddr)

	// Store metadata
	if err := token.setString(slotName, name); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: failed to set name: %w", err)
	}
	if err := token.setString(slotSymbol, symbol); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: failed to set symbol: %w", err)
	}
	if err := token.setUint8(slotDecimals, decimals); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: failed to set decimals: %w", err)
	}
	if err := token.setBigInt(slotTotalSupply, totalSupply); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: failed to set total supply: %w", err)
	}

	// Credit entire supply to deployer
	if err := token.setBalance(deployer, totalSupply); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: failed to credit deployer: %w", err)
	}

	return contractAddr, nil
}

// ── Standard QRC-20 methods ────────────────────────────────────────────────────

// Name returns the token name.
func (t *QRC20Token) Name() (string, error) {
	return t.getString(slotName)
}

// Symbol returns the token symbol.
func (t *QRC20Token) Symbol() (string, error) {
	return t.getString(slotSymbol)
}

// Decimals returns the number of decimals.
func (t *QRC20Token) Decimals() (uint8, error) {
	return t.getUint8(slotDecimals)
}

// TotalSupply returns the total token supply.
func (t *QRC20Token) TotalSupply() (*big.Int, error) {
	return t.getBigInt(slotTotalSupply)
}

// BalanceOf returns the token balance of an account.
func (t *QRC20Token) BalanceOf(addr qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(balanceSlot(addr))
}

// Transfer transfers amount tokens from sender to recipient.
// Returns an error if the sender has insufficient balance.
func (t *QRC20Token) Transfer(sender, recipient qcrypto.Address, amount *big.Int) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 transfer: negative amount")
	}
	senderBal, err := t.BalanceOf(sender)
	if err != nil {
		return err
	}
	if senderBal.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 transfer: insufficient balance (%s < %s)", senderBal, amount)
	}

	recipBal, err := t.BalanceOf(recipient)
	if err != nil {
		return err
	}

	newSender := new(big.Int).Sub(senderBal, amount)
	newRecip := new(big.Int).Add(recipBal, amount)

	if err := t.setBalance(sender, newSender); err != nil {
		return err
	}
	return t.setBalance(recipient, newRecip)
}

// Approve sets the allowance of spender over owner's tokens.
func (t *QRC20Token) Approve(owner, spender qcrypto.Address, amount *big.Int) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 approve: negative amount")
	}
	return t.setBigInt(allowanceSlot(owner, spender), amount)
}

// Allowance returns how much spender is allowed to spend on behalf of owner.
func (t *QRC20Token) Allowance(owner, spender qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(allowanceSlot(owner, spender))
}

// TransferFrom transfers amount tokens from `from` to `to` using an allowance.
// The caller (spender) must have been approved by `from`.
func (t *QRC20Token) TransferFrom(spender, from, to qcrypto.Address, amount *big.Int) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 transferFrom: negative amount")
	}

	// Check allowance
	allowance, err := t.Allowance(from, spender)
	if err != nil {
		return err
	}
	if allowance.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 transferFrom: allowance insufficient (%s < %s)", allowance, amount)
	}

	// Execute transfer
	if err := t.Transfer(from, to, amount); err != nil {
		return err
	}

	// Reduce allowance
	newAllowance := new(big.Int).Sub(allowance, amount)
	return t.Approve(from, spender, newAllowance)
}

// Mint creates new tokens and credits them to recipient.
// Only callable by the contract owner (enforced by the caller).
func (t *QRC20Token) Mint(recipient qcrypto.Address, amount *big.Int) error {
	if amount.Sign() <= 0 {
		return fmt.Errorf("QRC20 mint: amount must be positive")
	}

	supply, err := t.TotalSupply()
	if err != nil {
		return err
	}
	bal, err := t.BalanceOf(recipient)
	if err != nil {
		return err
	}

	newSupply := new(big.Int).Add(supply, amount)
	newBal := new(big.Int).Add(bal, amount)

	if err := t.setBigInt(slotTotalSupply, newSupply); err != nil {
		return err
	}
	return t.setBalance(recipient, newBal)
}

// Burn destroys tokens from holder's balance.
func (t *QRC20Token) Burn(holder qcrypto.Address, amount *big.Int) error {
	if amount.Sign() <= 0 {
		return fmt.Errorf("QRC20 burn: amount must be positive")
	}

	bal, err := t.BalanceOf(holder)
	if err != nil {
		return err
	}
	if bal.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 burn: insufficient balance")
	}

	supply, err := t.TotalSupply()
	if err != nil {
		return err
	}

	newBal := new(big.Int).Sub(bal, amount)
	newSupply := new(big.Int).Sub(supply, amount)

	if err := t.setBalance(holder, newBal); err != nil {
		return err
	}
	return t.setBigInt(slotTotalSupply, newSupply)
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (t *QRC20Token) setBalance(addr qcrypto.Address, bal *big.Int) error {
	return t.setBigInt(balanceSlot(addr), bal)
}

func (t *QRC20Token) setBigInt(slot [32]byte, v *big.Int) error {
	return t.stateDB.SetStorageValue(t.contract, slot, v.Bytes())
}

func (t *QRC20Token) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil {
		return big.NewInt(0), err
	}
	if len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (t *QRC20Token) setString(slot [32]byte, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return t.stateDB.SetStorageValue(t.contract, slot, b)
}

func (t *QRC20Token) getString(slot [32]byte) (string, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil || len(b) == 0 {
		return "", err
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return string(b), nil
	}
	return s, nil
}

func (t *QRC20Token) setUint8(slot [32]byte, v uint8) error {
	return t.stateDB.SetStorageValue(t.contract, slot, []byte{v})
}

func (t *QRC20Token) getUint8(slot [32]byte) (uint8, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil || len(b) == 0 {
		return 0, err
	}
	return b[0], nil
}
