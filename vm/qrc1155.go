// Package vm — QRC-1155: Splendor Quantum Chain Multi-Token Standard.
//
// QRC-1155 is the quantum-native equivalent of ERC-1155.
// A single contract can manage multiple token types:
//   - Fungible tokens (like QRC-20): tokenId with many identical units
//   - Non-fungible tokens (like QRC-721): tokenId with exactly 1 unit
//   - Semi-fungible: limited edition collectibles with N identical units
//
// All addresses use SQ: format. All hashing uses SHA3-256.
// No ECDSA. No keccak. No Ethereum address format.
//
// Interface:
//
//	balanceOf(account sqaddress, tokenId uint256) → uint256
//	balanceOfBatch(accounts []sqaddress, tokenIds []uint256) → []uint256
//	setApprovalForAll(operator sqaddress, approved bool)
//	isApprovedForAll(account sqaddress, operator sqaddress) → bool
//	safeTransferFrom(from, to sqaddress, tokenId, amount uint256, data []byte)
//	safeBatchTransferFrom(from, to sqaddress, tokenIds, amounts []uint256, data []byte)
//	mint(to sqaddress, tokenId, amount uint256, uri string)
//	mintBatch(to sqaddress, tokenIds, amounts []uint256, uris []string)
//	burn(from sqaddress, tokenId, amount uint256)
//	burnBatch(from sqaddress, tokenIds, amounts []uint256)
//	uri(tokenId uint256) → string
//	totalSupply(tokenId uint256) → uint256
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// ── QRC-1155 storage slot derivation ──────────────────────────────────────────
//
// balance[account][tokenId]   = SHA3-256("qrc1155:bal:" + account + tokenId32)
// approvedAll[owner][operator] = SHA3-256("qrc1155:opall:" + owner + operator)
// uri[tokenId]                = SHA3-256("qrc1155:uri:" + tokenId32)
// supply[tokenId]             = SHA3-256("qrc1155:supply:" + tokenId32)
// name                        = SHA3-256("qrc1155:name")
// uri_prefix                  = SHA3-256("qrc1155:uri_prefix")

var (
	slotMTName      = qcrypto.Hash32([]byte("qrc1155:name"))
	slotMTURIPrefix = qcrypto.Hash32([]byte("qrc1155:uri_prefix"))
)

func mtBalanceSlot(account qcrypto.Address, tokenId *big.Int) [32]byte {
	key := append([]byte("qrc1155:bal:"), account[:]...)
	key = append(key, padTo32(tokenId)...)
	return qcrypto.Hash32(key)
}

func mtApprovalSlot(owner, operator qcrypto.Address) [32]byte {
	key := append([]byte("qrc1155:opall:"), owner[:]...)
	key = append(key, operator[:]...)
	return qcrypto.Hash32(key)
}

func mtURISlot(tokenId *big.Int) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc1155:uri:"), padTo32(tokenId)...))
}

func mtSupplySlot(tokenId *big.Int) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc1155:supply:"), padTo32(tokenId)...))
}

// ── QRC1155Token ──────────────────────────────────────────────────────────────

// QRC1155Token is the native Go implementation of the QRC-1155 multi-token standard.
type QRC1155Token struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewQRC1155Token returns a handle to an existing QRC-1155 contract.
func NewQRC1155Token(stateDB *state.StateDB, contract qcrypto.Address) *QRC1155Token {
	return &QRC1155Token{stateDB: stateDB, contract: contract}
}

// DeployQRC1155 deploys a new QRC-1155 multi-token contract.
func DeployQRC1155(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name string,
	uriPrefix string, // e.g. "ipfs://QmBase/{id}.json"
	deployerNonce uint64,
) (qcrypto.Address, error) {
	nonce := []byte(fmt.Sprintf("%d", deployerNonce))
	input := append(deployer[:], nonce...)
	input = append(input, []byte("qrc1155")...)
	hash := qcrypto.Hash32(input)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	mt := NewQRC1155Token(stateDB, contractAddr)
	if err := mt.setString(slotMTName, name); err != nil {
		return qcrypto.Address{}, err
	}
	if err := mt.setString(slotMTURIPrefix, uriPrefix); err != nil {
		return qcrypto.Address{}, err
	}
	return contractAddr, nil
}

// ── Metadata ──────────────────────────────────────────────────────────────────

// Name returns the collection name.
func (t *QRC1155Token) Name() (string, error) { return t.getString(slotMTName) }

// URI returns the metadata URI for a token.
// If a per-token URI is set, it takes precedence over the prefix.
func (t *QRC1155Token) URI(tokenId *big.Int) (string, error) {
	// Try per-token URI first
	perToken, err := t.getString(mtURISlot(tokenId))
	if err == nil && perToken != "" {
		return perToken, nil
	}
	// Fall back to prefix + tokenId
	prefix, err := t.getString(slotMTURIPrefix)
	if err != nil || prefix == "" {
		return "", nil
	}
	return fmt.Sprintf("%s%s", prefix, tokenId.String()), nil
}

// TotalSupply returns the total minted (minus burned) amount for a tokenId.
func (t *QRC1155Token) TotalSupply(tokenId *big.Int) (*big.Int, error) {
	return t.getBigInt(mtSupplySlot(tokenId))
}

// ── Balances ──────────────────────────────────────────────────────────────────

// BalanceOf returns the balance of `account` for `tokenId`.
func (t *QRC1155Token) BalanceOf(account qcrypto.Address, tokenId *big.Int) (*big.Int, error) {
	return t.getBigInt(mtBalanceSlot(account, tokenId))
}

// BalanceOfBatch returns balances for multiple (account, tokenId) pairs.
// len(accounts) must equal len(tokenIds).
func (t *QRC1155Token) BalanceOfBatch(accounts []qcrypto.Address, tokenIds []*big.Int) ([]*big.Int, error) {
	if len(accounts) != len(tokenIds) {
		return nil, fmt.Errorf("QRC1155 balanceOfBatch: accounts and tokenIds length mismatch")
	}
	results := make([]*big.Int, len(accounts))
	for i, acc := range accounts {
		bal, err := t.BalanceOf(acc, tokenIds[i])
		if err != nil {
			return nil, err
		}
		results[i] = bal
	}
	return results, nil
}

// ── Approvals ─────────────────────────────────────────────────────────────────

// SetApprovalForAll grants or revokes operator approval for all tokens of owner.
func (t *QRC1155Token) SetApprovalForAll(owner, operator qcrypto.Address, approved bool) error {
	val := []byte{0}
	if approved {
		val = []byte{1}
	}
	return t.stateDB.SetStorageValue(t.contract, mtApprovalSlot(owner, operator), val)
}

// IsApprovedForAll checks if operator is approved to manage all of owner's tokens.
func (t *QRC1155Token) IsApprovedForAll(owner, operator qcrypto.Address) (bool, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, mtApprovalSlot(owner, operator))
	if err != nil || len(b) == 0 {
		return false, nil
	}
	return b[0] == 1, nil
}

// isApprovedOrOwner checks if caller can operate on from's tokens.
func (t *QRC1155Token) isApprovedOrOwner(caller, from qcrypto.Address) (bool, error) {
	if caller == from {
		return true, nil
	}
	return t.IsApprovedForAll(from, caller)
}

// ── Transfers ─────────────────────────────────────────────────────────────────

// SafeTransferFrom transfers `amount` of `tokenId` from `from` to `to`.
func (t *QRC1155Token) SafeTransferFrom(caller, from, to qcrypto.Address, tokenId, amount *big.Int) error {
	if amount.Sign() < 0 {
		return fmt.Errorf("QRC1155 transfer: negative amount")
	}
	if to.IsZero() {
		return fmt.Errorf("QRC1155 transfer: cannot transfer to zero address")
	}
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 transfer: caller not approved")
	}

	fromBal, err := t.BalanceOf(from, tokenId)
	if err != nil {
		return err
	}
	if fromBal.Cmp(amount) < 0 {
		return fmt.Errorf("QRC1155 transfer: insufficient balance (have %s, want %s)", fromBal, amount)
	}

	// from == to addresses the SAME balance slot: both reads see B, and the
	// credit write (B+amount) lands after the debit write (B-amount), minting
	// `amount` units. Leave the balance untouched instead.
	if from == to {
		return nil
	}

	toBal, err := t.BalanceOf(to, tokenId)
	if err != nil {
		return err
	}

	newFrom := new(big.Int).Sub(fromBal, amount)
	newTo := new(big.Int).Add(toBal, amount)

	if err := t.setBigInt(mtBalanceSlot(from, tokenId), newFrom); err != nil {
		return err
	}
	return t.setBigInt(mtBalanceSlot(to, tokenId), newTo)
}

// SafeBatchTransferFrom transfers multiple (tokenId, amount) pairs from `from` to `to`.
func (t *QRC1155Token) SafeBatchTransferFrom(caller, from, to qcrypto.Address, tokenIds, amounts []*big.Int) error {
	if len(tokenIds) != len(amounts) {
		return fmt.Errorf("QRC1155 batchTransfer: tokenIds and amounts length mismatch")
	}
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 batchTransfer: caller not approved")
	}
	if to.IsZero() {
		return fmt.Errorf("QRC1155 batchTransfer: cannot transfer to zero address")
	}

	for i, tokenId := range tokenIds {
		if err := t.SafeTransferFrom(from, from, to, tokenId, amounts[i]); err != nil {
			return fmt.Errorf("QRC1155 batchTransfer[%d]: %w", i, err)
		}
	}
	return nil
}

// ── Minting ───────────────────────────────────────────────────────────────────

// Mint creates `amount` units of `tokenId` for `to`.
func (t *QRC1155Token) Mint(to qcrypto.Address, tokenId, amount *big.Int, uri string) error {
	if to.IsZero() {
		return fmt.Errorf("QRC1155 mint: cannot mint to zero address")
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("QRC1155 mint: amount must be positive")
	}

	bal, _ := t.BalanceOf(to, tokenId)
	if err := t.setBigInt(mtBalanceSlot(to, tokenId), new(big.Int).Add(bal, amount)); err != nil {
		return err
	}

	supply, _ := t.TotalSupply(tokenId)
	if err := t.setBigInt(mtSupplySlot(tokenId), new(big.Int).Add(supply, amount)); err != nil {
		return err
	}

	if uri != "" {
		t.setString(mtURISlot(tokenId), uri)
	}
	return nil
}

// MintBatch mints multiple token types at once.
func (t *QRC1155Token) MintBatch(to qcrypto.Address, tokenIds, amounts []*big.Int, uris []string) error {
	if len(tokenIds) != len(amounts) {
		return fmt.Errorf("QRC1155 mintBatch: length mismatch")
	}
	for i, tokenId := range tokenIds {
		uri := ""
		if i < len(uris) {
			uri = uris[i]
		}
		if err := t.Mint(to, tokenId, amounts[i], uri); err != nil {
			return fmt.Errorf("QRC1155 mintBatch[%d]: %w", i, err)
		}
	}
	return nil
}

// ── Burning ───────────────────────────────────────────────────────────────────

// Burn destroys `amount` units of `tokenId` from `from`.
func (t *QRC1155Token) Burn(caller, from qcrypto.Address, tokenId, amount *big.Int) error {
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 burn: caller not approved")
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("QRC1155 burn: amount must be positive")
	}

	bal, _ := t.BalanceOf(from, tokenId)
	if bal.Cmp(amount) < 0 {
		return fmt.Errorf("QRC1155 burn: insufficient balance")
	}

	newBal := new(big.Int).Sub(bal, amount)
	if err := t.setBigInt(mtBalanceSlot(from, tokenId), newBal); err != nil {
		return err
	}

	supply, _ := t.TotalSupply(tokenId)
	newSupply := new(big.Int).Sub(supply, amount)
	if newSupply.Sign() < 0 {
		newSupply = big.NewInt(0)
	}
	return t.setBigInt(mtSupplySlot(tokenId), newSupply)
}

// BurnBatch destroys multiple token types from `from`.
func (t *QRC1155Token) BurnBatch(caller, from qcrypto.Address, tokenIds, amounts []*big.Int) error {
	if len(tokenIds) != len(amounts) {
		return fmt.Errorf("QRC1155 burnBatch: length mismatch")
	}
	for i, tokenId := range tokenIds {
		if err := t.Burn(caller, from, tokenId, amounts[i]); err != nil {
			return fmt.Errorf("QRC1155 burnBatch[%d]: %w", i, err)
		}
	}
	return nil
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (t *QRC1155Token) setBigInt(slot [32]byte, v *big.Int) error {
	return t.stateDB.SetStorageValue(t.contract, slot, v.Bytes())
}

func (t *QRC1155Token) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (t *QRC1155Token) setString(slot [32]byte, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return t.stateDB.SetStorageValue(t.contract, slot, b)
}

func (t *QRC1155Token) getString(slot [32]byte) (string, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil || len(b) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return string(b), nil
	}
	return s, nil
}
