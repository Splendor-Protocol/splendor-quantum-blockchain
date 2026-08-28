// Package vm — QRC-20: Splendor Quantum Chain fungible token standard (v2).
//
// Improvements in v2:
//   - Ownable: contract owner stored on-chain, transferable
//   - Pausable: owner can pause/unpause all transfers
//   - Capped: optional max supply enforced at mint time
//   - Events: Transfer, Approval, Mint, Burn emitted as *types.Log
//   - Permit (EIP-2612 equivalent): off-chain ML-DSA-65-signed approvals
//   - IncreaseAllowance / DecreaseAllowance: safer than plain Approve
//   - All slot keys: SHA3-256, no keccak
package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// QRC-20 storage slot constants (SHA3-256 of descriptive strings)
var (
	slotTotalSupply = qcrypto.Hash32([]byte("qrc20:total_supply"))
	slotName        = qcrypto.Hash32([]byte("qrc20:name"))
	slotSymbol      = qcrypto.Hash32([]byte("qrc20:symbol"))
	slotDecimals    = qcrypto.Hash32([]byte("qrc20:decimals"))
	slotOwner       = qcrypto.Hash32([]byte("qrc20:owner"))
	slotPaused      = qcrypto.Hash32([]byte("qrc20:paused"))
	slotMaxSupply   = qcrypto.Hash32([]byte("qrc20:max_supply"))
)

// balanceSlot = SHA3-256("qrc20:balance:" + addr)
func balanceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc20:balance:"), addr[:]...))
}

// allowanceSlot = SHA3-256("qrc20:allowance:" + owner + spender)
func allowanceSlot(owner, spender qcrypto.Address) [32]byte {
	key := append([]byte("qrc20:allowance:"), owner[:]...)
	key = append(key, spender[:]...)
	return qcrypto.Hash32(key)
}

// permitNonceSlot = SHA3-256("qrc20:permit_nonce:" + addr)
func permitNonceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc20:permit_nonce:"), addr[:]...))
}

// ── QRC20Token ────────────────────────────────────────────────────────────────

// QRC20Token is the v2 native implementation of the QRC-20 standard.
type QRC20Token struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewQRC20Token creates a handle to an existing QRC-20 contract.
func NewQRC20Token(stateDB *state.StateDB, contract qcrypto.Address) *QRC20Token {
	return &QRC20Token{stateDB: stateDB, contract: contract}
}

// DeployQRC20 deploys a new QRC-20 token contract.
// maxSupply = 0 means uncapped.
func DeployQRC20(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name, symbol string,
	decimals uint8,
	totalSupply *big.Int,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	return DeployQRC20Capped(stateDB, deployer, name, symbol, decimals, totalSupply, nil, deployerNonce)
}

// DeployQRC20Capped deploys a QRC-20 with optional maxSupply cap.
func DeployQRC20Capped(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name, symbol string,
	decimals uint8,
	initialSupply *big.Int,
	maxSupply *big.Int,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	// Deterministic address
	nonceBytes := []byte(fmt.Sprintf("%d", deployerNonce))
	contractInput := append(deployer[:], nonceBytes...)
	contractInput = append(contractInput, []byte("qrc20")...)
	hash := qcrypto.Hash32(contractInput)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	token := NewQRC20Token(stateDB, contractAddr)

	// Validate max supply
	if maxSupply != nil && maxSupply.Sign() > 0 && initialSupply.Cmp(maxSupply) > 0 {
		return qcrypto.Address{}, fmt.Errorf("QRC20 deploy: initialSupply exceeds maxSupply")
	}

	// Store metadata
	if err := token.setString(slotName, name); err != nil {
		return qcrypto.Address{}, err
	}
	if err := token.setString(slotSymbol, symbol); err != nil {
		return qcrypto.Address{}, err
	}
	if err := token.setUint8(slotDecimals, decimals); err != nil {
		return qcrypto.Address{}, err
	}
	if err := token.setBigInt(slotTotalSupply, initialSupply); err != nil {
		return qcrypto.Address{}, err
	}

	// Store owner
	if err := stateDB.SetStorageValue(contractAddr, slotOwner, deployer[:]); err != nil {
		return qcrypto.Address{}, err
	}

	// Store max supply (0 = uncapped)
	cap := big.NewInt(0)
	if maxSupply != nil {
		cap = maxSupply
	}
	if err := token.setBigInt(slotMaxSupply, cap); err != nil {
		return qcrypto.Address{}, err
	}

	// Credit initial supply to deployer
	if initialSupply.Sign() > 0 {
		if err := token.setBalance(deployer, initialSupply); err != nil {
			return qcrypto.Address{}, err
		}
	}

	return contractAddr, nil
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func (t *QRC20Token) Name() (string, error)          { return t.getString(slotName) }
func (t *QRC20Token) Symbol() (string, error)        { return t.getString(slotSymbol) }
func (t *QRC20Token) Decimals() (uint8, error)       { return t.getUint8(slotDecimals) }
func (t *QRC20Token) TotalSupply() (*big.Int, error) { return t.getBigInt(slotTotalSupply) }
func (t *QRC20Token) MaxSupply() (*big.Int, error)   { return t.getBigInt(slotMaxSupply) }

// Owner returns the contract owner address.
func (t *QRC20Token) Owner() (qcrypto.Address, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slotOwner)
	if err != nil {
		return qcrypto.Address{}, err
	}
	if len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

// IsPaused returns true if the contract is paused.
func (t *QRC20Token) IsPaused() (bool, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slotPaused)
	if err != nil {
		return false, err
	}
	if len(b) == 0 {
		return false, nil
	}
	return b[0] == 1, nil
}

// ── Access control ────────────────────────────────────────────────────────────

// requireOwner returns an error if caller is not the contract owner.
func (t *QRC20Token) requireOwner(caller qcrypto.Address) error {
	owner, err := t.Owner()
	if err != nil {
		return err
	}
	if caller != owner {
		return fmt.Errorf("QRC20: caller %s is not owner", caller)
	}
	return nil
}

// requireNotPaused returns an error if the contract is paused.
func (t *QRC20Token) requireNotPaused() error {
	paused, err := t.IsPaused()
	if err != nil {
		return err
	}
	if paused {
		return fmt.Errorf("QRC20: contract is paused")
	}
	return nil
}

// TransferOwnership transfers contract ownership to newOwner.
func (t *QRC20Token) TransferOwnership(caller, newOwner qcrypto.Address) error {
	return t.TransferOwnershipWithEvents(caller, newOwner, nil)
}

// TransferOwnershipWithEvents transfers ownership and records the
// OwnershipTransferred event.
func (t *QRC20Token) TransferOwnershipWithEvents(caller, newOwner qcrypto.Address, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if newOwner.IsZero() {
		return fmt.Errorf("QRC20: new owner is zero address")
	}
	if err := t.stateDB.SetStorageValue(t.contract, slotOwner, newOwner[:]); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(OwnershipTransferredEvent(t.contract, caller, newOwner))
	}
	return nil
}

// Pause halts all transfers. Only callable by owner.
func (t *QRC20Token) Pause(caller qcrypto.Address) error {
	return t.PauseWithEvents(caller, nil)
}

// PauseWithEvents pauses the contract and records the Paused event.
func (t *QRC20Token) PauseWithEvents(caller qcrypto.Address, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if err := t.stateDB.SetStorageValue(t.contract, slotPaused, []byte{1}); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(PausedEvent(t.contract, caller))
	}
	return nil
}

// Unpause resumes all transfers. Only callable by owner.
func (t *QRC20Token) Unpause(caller qcrypto.Address) error {
	return t.UnpauseWithEvents(caller, nil)
}

// UnpauseWithEvents unpauses the contract and records the Unpaused event.
func (t *QRC20Token) UnpauseWithEvents(caller qcrypto.Address, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if err := t.stateDB.SetStorageValue(t.contract, slotPaused, []byte{0}); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(UnpausedEvent(t.contract, caller))
	}
	return nil
}

// ── Balances & allowances ─────────────────────────────────────────────────────

// BalanceOf returns the token balance of addr.
func (t *QRC20Token) BalanceOf(addr qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(balanceSlot(addr))
}

// Allowance returns the spending allowance of spender over owner's tokens.
func (t *QRC20Token) Allowance(owner, spender qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(allowanceSlot(owner, spender))
}

// ── Core ERC-20 operations ────────────────────────────────────────────────────

// Transfer moves amount tokens from sender to recipient.
func (t *QRC20Token) Transfer(sender, recipient qcrypto.Address, amount *big.Int) error {
	return t.transferWithEvents(sender, recipient, amount, nil)
}

// TransferWithEvents moves tokens and appends the Transfer event to `logs`.
func (t *QRC20Token) TransferWithEvents(sender, recipient qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	return t.transferWithEvents(sender, recipient, amount, receipt)
}

func (t *QRC20Token) transferWithEvents(sender, recipient qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if err := t.requireNotPaused(); err != nil {
		return err
	}
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 transfer: negative amount")
	}
	if amount.Sign() == 0 {
		return nil
	}
	if recipient.IsZero() {
		return fmt.Errorf("QRC20 transfer: cannot transfer to zero address")
	}

	senderBal, err := t.BalanceOf(sender)
	if err != nil {
		return err
	}
	if senderBal.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 transfer: insufficient balance (%s < %s)", senderBal, amount)
	}

	// sender == recipient writes the SAME storage slot twice. Reading both
	// balances up front gives B for each, so the debit (B-amount) is overwritten
	// by the credit (B+amount) and the holder gains `amount` while totalSupply
	// stays put. Settle the balance check and emit the event, but move nothing.
	if sender != recipient {
		recipBal, err := t.BalanceOf(recipient)
		if err != nil {
			return err
		}
		if err := t.setBalance(sender, new(big.Int).Sub(senderBal, amount)); err != nil {
			return err
		}
		if err := t.setBalance(recipient, new(big.Int).Add(recipBal, amount)); err != nil {
			return err
		}
	}

	// Emit Transfer event
	if receipt != nil {
		receipt.AddLog(TransferEvent(t.contract, sender, recipient, amount))
	}
	return nil
}

// Approve sets spender's allowance over caller's tokens.
// Use IncreaseAllowance/DecreaseAllowance when possible to avoid race conditions.
func (t *QRC20Token) Approve(owner, spender qcrypto.Address, amount *big.Int) error {
	return t.approveWithEvents(owner, spender, amount, nil)
}

// ApproveWithEvents sets allowance and records the Approval event.
func (t *QRC20Token) ApproveWithEvents(owner, spender qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	return t.approveWithEvents(owner, spender, amount, receipt)
}

func (t *QRC20Token) approveWithEvents(owner, spender qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 approve: negative amount")
	}
	if spender.IsZero() {
		return fmt.Errorf("QRC20 approve: zero spender")
	}
	if err := t.setBigInt(allowanceSlot(owner, spender), amount); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(ApprovalEvent(t.contract, owner, spender, amount))
	}
	return nil
}

// IncreaseAllowance adds amount to spender's current allowance.
func (t *QRC20Token) IncreaseAllowance(owner, spender qcrypto.Address, amount *big.Int) error {
	return t.IncreaseAllowanceWithEvents(owner, spender, amount, nil)
}

// IncreaseAllowanceWithEvents raises the allowance and records the Approval
// event carrying the new total. The total must still fit a 256-bit word.
func (t *QRC20Token) IncreaseAllowanceWithEvents(owner, spender qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 increaseAllowance: negative amount")
	}
	current, err := t.Allowance(owner, spender)
	if err != nil {
		return err
	}
	next := new(big.Int).Add(current, amount)
	if next.BitLen() > 256 {
		return fmt.Errorf("QRC20 increaseAllowance: allowance overflow")
	}
	return t.approveWithEvents(owner, spender, next, receipt)
}

// DecreaseAllowance subtracts amount from spender's allowance. Reverts on underflow.
func (t *QRC20Token) DecreaseAllowance(owner, spender qcrypto.Address, amount *big.Int) error {
	return t.DecreaseAllowanceWithEvents(owner, spender, amount, nil)
}

// DecreaseAllowanceWithEvents lowers the allowance and records the Approval
// event carrying the new total.
func (t *QRC20Token) DecreaseAllowanceWithEvents(owner, spender qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC20 decreaseAllowance: negative amount")
	}
	current, err := t.Allowance(owner, spender)
	if err != nil {
		return err
	}
	if current.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 decreaseAllowance: allowance below zero")
	}
	return t.approveWithEvents(owner, spender, new(big.Int).Sub(current, amount), receipt)
}

// TransferFrom moves tokens using an allowance.
func (t *QRC20Token) TransferFrom(spender, from, to qcrypto.Address, amount *big.Int) error {
	return t.TransferFromWithEvents(spender, from, to, amount, nil)
}

// TransferFromWithEvents moves tokens via allowance and records events.
func (t *QRC20Token) TransferFromWithEvents(spender, from, to qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	allowance, err := t.Allowance(from, spender)
	if err != nil {
		return err
	}
	if allowance.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 transferFrom: allowance insufficient (%s < %s)", allowance, amount)
	}

	if err := t.transferWithEvents(from, to, amount, receipt); err != nil {
		return err
	}

	// Reduce allowance (unless it's MaxUint256 — infinite approval). The
	// decrement is an Approval in its own right, so it lands in the receipt
	// next to the Transfer.
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if allowance.Cmp(maxUint256) != 0 {
		if err := t.approveWithEvents(from, spender, new(big.Int).Sub(allowance, amount), receipt); err != nil {
			return err
		}
	}
	return nil
}

// ── Mint & Burn ───────────────────────────────────────────────────────────────

// Mint creates amount new tokens for recipient. Only callable by owner.
func (t *QRC20Token) Mint(caller, recipient qcrypto.Address, amount *big.Int) error {
	return t.MintWithEvents(caller, recipient, amount, nil)
}

// MintWithEvents mints tokens and records the Mint event.
func (t *QRC20Token) MintWithEvents(caller, recipient qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if err := t.requireNotPaused(); err != nil {
		return err
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("QRC20 mint: amount must be positive")
	}
	if recipient.IsZero() {
		return fmt.Errorf("QRC20 mint: cannot mint to zero address")
	}

	// Check cap
	maxSupply, _ := t.MaxSupply()
	if maxSupply.Sign() > 0 {
		supply, _ := t.TotalSupply()
		if new(big.Int).Add(supply, amount).Cmp(maxSupply) > 0 {
			return fmt.Errorf("QRC20 mint: would exceed max supply (%s)", maxSupply)
		}
	}

	supply, _ := t.TotalSupply()
	bal, _ := t.BalanceOf(recipient)

	if err := t.setBigInt(slotTotalSupply, new(big.Int).Add(supply, amount)); err != nil {
		return err
	}
	if err := t.setBalance(recipient, new(big.Int).Add(bal, amount)); err != nil {
		return err
	}

	if receipt != nil {
		receipt.AddLog(MintEvent(t.contract, recipient, big.NewInt(0), amount))
	}
	return nil
}

// Burn destroys amount tokens from holder's balance.
func (t *QRC20Token) Burn(holder qcrypto.Address, amount *big.Int) error {
	return t.BurnWithEvents(holder, amount, nil)
}

// BurnWithEvents burns tokens and records the Burn event.
func (t *QRC20Token) BurnWithEvents(holder qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	if err := t.requireNotPaused(); err != nil {
		return err
	}
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

	supply, _ := t.TotalSupply()
	if err := t.setBigInt(slotTotalSupply, new(big.Int).Sub(supply, amount)); err != nil {
		return err
	}
	if err := t.setBalance(holder, new(big.Int).Sub(bal, amount)); err != nil {
		return err
	}

	if receipt != nil {
		receipt.AddLog(BurnEvent(t.contract, holder, big.NewInt(0), amount))
	}
	return nil
}

// BurnFrom burns amount from holder using caller's allowance.
func (t *QRC20Token) BurnFrom(caller, holder qcrypto.Address, amount *big.Int) error {
	return t.BurnFromWithEvents(caller, holder, amount, nil)
}

// BurnFromWithEvents burns via allowance and records the Burn event and the
// Approval event for the decremented allowance.
func (t *QRC20Token) BurnFromWithEvents(caller, holder qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	allowance, err := t.Allowance(holder, caller)
	if err != nil {
		return err
	}
	if allowance.Cmp(amount) < 0 {
		return fmt.Errorf("QRC20 burnFrom: allowance insufficient")
	}
	if err := t.BurnWithEvents(holder, amount, receipt); err != nil {
		return err
	}
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if allowance.Cmp(maxUint256) != 0 {
		return t.approveWithEvents(holder, caller, new(big.Int).Sub(allowance, amount), receipt)
	}
	return nil
}

// ── Permit (off-chain approval) ────────────────────────────────────────────────

// PermitNonce returns the current permit nonce for addr (prevents replay).
func (t *QRC20Token) PermitNonce(addr qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(permitNonceSlot(addr))
}

// PermitMessage is the digest an owner signs under crypto.CtxPermit to grant a
// permit on this chain: types.PermitDigest bound to types.ChainID (the id the
// node adopted from its genesis). See PermitDigest for the layout and for why
// the chain id is part of it.
func PermitMessage(contract, owner, spender qcrypto.Address, amount *big.Int, nonce uint64, deadline int64) ([32]byte, error) {
	return types.PermitDigest(types.ChainID, contract, owner, spender, amount, nonce, deadline)
}

// Permit executes an off-chain-signed approval: it sets spender's allowance
// over owner's tokens to amount on the strength of owner's ML-DSA-65 signature
// over PermitMessage(...), so the spender (or any relayer) pays the fee and
// the owner never sends a transaction.
//
// blockTime is the timestamp of the block the permit executes in, handed down
// through vm.ModuleContext — never the wall clock, so every node judges the
// deadline identically. deadline must be strictly positive (an open-ended
// permit is not a thing) and the permit is valid while blockTime <= deadline.
//
// Key resolution is registry-first: if owner has a registered public key it
// is the only key that verifies, and ownerPubKey is ignored. Only an owner
// with no registered key may present ownerPubKey, and then only a key whose
// derived address is owner — otherwise anyone could hand in a key they hold
// and "prove" an approval for an address they do not own. Permit never
// registers the presented key; registration stays with the transaction path.
//
// Like Approve, Permit is not blocked by pause: it only records an allowance,
// and the transfer_from that spends it is what the pause stops. The permit
// nonce advances on success, so a signature can be used once. The Approval
// event is appended to receipt when one is given.
func (t *QRC20Token) Permit(
	owner, spender qcrypto.Address,
	amount *big.Int,
	deadline int64,
	sig []byte,
	ownerPubKey []byte,
	blockTime int64,
	receipt *types.Receipt,
) error {
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("QRC20 permit: amount must be non-negative")
	}
	if amount.BitLen() > 256 {
		return fmt.Errorf("QRC20 permit: amount does not fit in 256 bits")
	}
	if deadline <= 0 {
		return fmt.Errorf("QRC20 permit: deadline must be positive")
	}
	if blockTime > deadline {
		return fmt.Errorf("QRC20 permit: expired (deadline %d, block time %d)", deadline, blockTime)
	}

	var pub *qcrypto.PublicKey
	registered, err := t.stateDB.GetPublicKey(owner)
	switch {
	case err == nil:
		if pub, err = qcrypto.PublicKeyFromBytes(registered); err != nil {
			return fmt.Errorf("QRC20 permit: registered owner key: %w", err)
		}
	case errors.Is(err, types.ErrNoPublicKey):
		if pub, err = qcrypto.PublicKeyFromBytes(ownerPubKey); err != nil {
			return fmt.Errorf("QRC20 permit: owner has no registered key and owner_pubkey is invalid: %w", err)
		}
		if qcrypto.DeriveAddress(pub) != owner {
			return fmt.Errorf("QRC20 permit: owner_pubkey does not derive owner %s", owner)
		}
	default:
		return fmt.Errorf("QRC20 permit: owner key lookup: %w", err)
	}

	nonce, err := t.PermitNonce(owner)
	if err != nil {
		return err
	}
	if nonce.BitLen() > 64 {
		return fmt.Errorf("QRC20 permit: nonce overflow")
	}
	msg, err := PermitMessage(t.contract, owner, spender, amount, nonce.Uint64(), deadline)
	if err != nil {
		return fmt.Errorf("QRC20 permit: %w", err)
	}
	if !qcrypto.Verify(pub, msg[:], qcrypto.CtxPermit, sig) {
		return fmt.Errorf("QRC20 permit: invalid signature")
	}

	// Increment nonce (prevents replay)
	if err := t.setBigInt(permitNonceSlot(owner), new(big.Int).Add(nonce, big.NewInt(1))); err != nil {
		return err
	}

	return t.approveWithEvents(owner, spender, amount, receipt)
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (t *QRC20Token) setBalance(addr qcrypto.Address, bal *big.Int) error {
	return t.setBigInt(balanceSlot(addr), bal)
}

func (t *QRC20Token) setBigInt(slot [32]byte, v *big.Int) error {
	if v == nil || v.Sign() == 0 {
		return t.stateDB.SetStorageValue(t.contract, slot, []byte{0})
	}
	return t.stateDB.SetStorageValue(t.contract, slot, v.Bytes())
}

// The readers below return a StateDB read ERROR as an error rather than the
// empty-slot zero: an I/O fault on one node must fail the call, not yield a
// different successful result than every other node computes. An empty slot
// is still the zero value.
func (t *QRC20Token) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil {
		return big.NewInt(0), err
	}
	if len(b) == 0 || (len(b) == 1 && b[0] == 0) {
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
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", nil
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
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, nil
	}
	return b[0], nil
}
