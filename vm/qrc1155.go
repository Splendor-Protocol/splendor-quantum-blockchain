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
//	mint(to sqaddress, tokenId, amount uint256, uri string)          [owner only]
//	mintBatch(to sqaddress, tokenIds, amounts []uint256, uris []string) [owner only]
//	burn(from sqaddress, tokenId, amount uint256)
//	burnBatch(from sqaddress, tokenIds, amounts []uint256)
//	uri(tokenId uint256) → string
//	totalSupply(tokenId uint256) → uint256
//	owner() → sqaddress
//	transferOwnership(newOwner sqaddress)                            [owner only]
//
// Ownership. A collection has exactly one owner, written at deploy and never
// re-initialised. Minting is owner-only. A collection whose owner is its own
// address is MODULE-MANAGED: no key derives to it, so no transaction can
// satisfy the owner check, and only the native module that lives at that
// address moves its balances (through mintUnchecked/burnUnchecked). The
// router refuses every user-authorised burn or transfer against such a
// collection, so a holder cannot destroy one leg of a position set the
// owning module accounts for.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// MaxQRC1155Batch caps the length of every batch path (mintBatch, burnBatch,
// safeBatchTransferFrom, balanceOfBatch). Module calls are unmetered, so an
// unbounded batch would be free work for every node.
const MaxQRC1155Batch = 64

// ── QRC-1155 storage slot derivation ──────────────────────────────────────────
//
// balance[account][tokenId]   = SHA3-256("qrc1155:bal:" + account + tokenId32)
// approvedAll[owner][operator] = SHA3-256("qrc1155:opall:" + owner + operator)
// uri[tokenId]                = SHA3-256("qrc1155:uri:" + tokenId32)
// supply[tokenId]             = SHA3-256("qrc1155:supply:" + tokenId32)
// name                        = SHA3-256("qrc1155:name")
// uri_prefix                  = SHA3-256("qrc1155:uri_prefix")
// owner                       = SHA3-256("qrc1155:owner")

var (
	slotMTName      = qcrypto.Hash32([]byte("qrc1155:name"))
	slotMTURIPrefix = qcrypto.Hash32([]byte("qrc1155:uri_prefix"))
	slotMTOwner     = qcrypto.Hash32([]byte("qrc1155:owner"))
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

// checkTokenID refuses an id that has no 32-byte representation. padTo32
// would otherwise panic on a value of 2^256 or above; a nil or negative id
// has no slot either.
func checkTokenID(tokenId *big.Int) error {
	if tokenId == nil || tokenId.Sign() < 0 {
		return fmt.Errorf("QRC1155: token id must be a non-negative integer")
	}
	if tokenId.Cmp(maxUint256Plus1) >= 0 {
		return fmt.Errorf("QRC1155: token id does not fit in 256 bits")
	}
	return nil
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

// DeployQRC1155 deploys a new QRC-1155 multi-token contract owned by the
// deployer at SHA3(deployer ‖ decimal(nonce) ‖ "qrc1155").
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

	if err := initQRC1155At(stateDB, contractAddr, deployer, name, uriPrefix); err != nil {
		return qcrypto.Address{}, err
	}
	return contractAddr, nil
}

// initQRC1155At writes a fresh collection's owner and metadata at addr. It is
// the one place a collection is initialised — DeployQRC1155 for user
// collections, and a native module (owner == addr) for the collection it
// manages — and it refuses to run twice: re-initialising would let a second
// deploy rewrite the owner of a collection that already has holders.
func initQRC1155At(stateDB *state.StateDB, addr, owner qcrypto.Address, name, uriPrefix string) error {
	if owner.IsZero() {
		return fmt.Errorf("QRC1155 deploy: owner is zero address")
	}
	mt := NewQRC1155Token(stateDB, addr)
	if existing, _ := mt.Owner(); !existing.IsZero() {
		return fmt.Errorf("QRC1155 deploy: collection %s already initialised", addr)
	}
	if err := stateDB.SetStorageValue(addr, slotMTOwner, owner[:]); err != nil {
		return err
	}
	if err := mt.setString(slotMTName, name); err != nil {
		return err
	}
	return mt.setString(slotMTURIPrefix, uriPrefix)
}

// ── Metadata ──────────────────────────────────────────────────────────────────

// Name returns the collection name.
func (t *QRC1155Token) Name() (string, error) { return t.getString(slotMTName) }

// URIPrefix returns the collection-wide URI prefix.
func (t *QRC1155Token) URIPrefix() (string, error) { return t.getString(slotMTURIPrefix) }

// URI returns the metadata URI for a token.
// If a per-token URI is set, it takes precedence over the prefix.
func (t *QRC1155Token) URI(tokenId *big.Int) (string, error) {
	if err := checkTokenID(tokenId); err != nil {
		return "", err
	}
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
	if err := checkTokenID(tokenId); err != nil {
		return nil, err
	}
	return t.getBigInt(mtSupplySlot(tokenId))
}

// Owner returns the collection owner (zero for a collection nobody deployed).
func (t *QRC1155Token) Owner() (qcrypto.Address, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slotMTOwner)
	if err != nil || len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var owner qcrypto.Address
	copy(owner[:], b)
	return owner, nil
}

// IsModuleManaged reports whether the collection's owner is its own address:
// a native module's position collection, which no transaction may mutate
// directly (see the package comment).
func (t *QRC1155Token) IsModuleManaged() (bool, error) {
	owner, err := t.Owner()
	if err != nil {
		return false, err
	}
	return owner == t.contract, nil
}

// ── Access control ────────────────────────────────────────────────────────────

// requireOwner returns an error if caller is not the collection owner.
func (t *QRC1155Token) requireOwner(caller qcrypto.Address) error {
	owner, err := t.Owner()
	if err != nil {
		return err
	}
	if caller != owner {
		return fmt.Errorf("QRC1155: caller %s is not owner", caller)
	}
	return nil
}

// requireUserManaged refuses a module-managed collection: the router must
// never let a holder burn or move a balance the owning module accounts for.
func (t *QRC1155Token) requireUserManaged() error {
	managed, err := t.IsModuleManaged()
	if err != nil {
		return err
	}
	if managed {
		return fmt.Errorf("QRC1155: collection %s is managed by a native module; only that module may move its balances", t.contract)
	}
	return nil
}

// TransferOwnership hands the collection to newOwner. Only callable by owner.
func (t *QRC1155Token) TransferOwnership(caller, newOwner qcrypto.Address) error {
	return t.TransferOwnershipWithEvents(caller, newOwner, nil)
}

// TransferOwnershipWithEvents transfers ownership and records the
// OwnershipTransferred event.
func (t *QRC1155Token) TransferOwnershipWithEvents(caller, newOwner qcrypto.Address, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if newOwner.IsZero() {
		return fmt.Errorf("QRC1155: new owner is zero address")
	}
	if err := t.stateDB.SetStorageValue(t.contract, slotMTOwner, newOwner[:]); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(OwnershipTransferredEvent(t.contract, caller, newOwner))
	}
	return nil
}

// ── Balances ──────────────────────────────────────────────────────────────────

// BalanceOf returns the balance of `account` for `tokenId`.
func (t *QRC1155Token) BalanceOf(account qcrypto.Address, tokenId *big.Int) (*big.Int, error) {
	if err := checkTokenID(tokenId); err != nil {
		return nil, err
	}
	return t.getBigInt(mtBalanceSlot(account, tokenId))
}

// BalanceOfBatch returns balances for multiple (account, tokenId) pairs.
// len(accounts) must equal len(tokenIds) and be at most MaxQRC1155Batch.
func (t *QRC1155Token) BalanceOfBatch(accounts []qcrypto.Address, tokenIds []*big.Int) ([]*big.Int, error) {
	if len(accounts) != len(tokenIds) {
		return nil, fmt.Errorf("QRC1155 balanceOfBatch: accounts and tokenIds length mismatch")
	}
	if len(accounts) > MaxQRC1155Batch {
		return nil, fmt.Errorf("QRC1155 balanceOfBatch: %d entries exceeds the batch cap of %d", len(accounts), MaxQRC1155Batch)
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
	return t.SetApprovalForAllWithEvents(owner, operator, approved, nil)
}

// SetApprovalForAllWithEvents sets the approval and records the
// ApprovalForAll event.
func (t *QRC1155Token) SetApprovalForAllWithEvents(owner, operator qcrypto.Address, approved bool, receipt *types.Receipt) error {
	if operator.IsZero() {
		return fmt.Errorf("QRC1155 setApprovalForAll: operator is zero address")
	}
	if operator == owner {
		return fmt.Errorf("QRC1155 setApprovalForAll: cannot approve self")
	}
	val := []byte{0}
	if approved {
		val = []byte{1}
	}
	if err := t.stateDB.SetStorageValue(t.contract, mtApprovalSlot(owner, operator), val); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(ApprovalForAllEvent(t.contract, owner, operator, approved))
	}
	return nil
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

// checkBatch validates the common shape of every batch path.
func checkBatch(what string, tokenIds, amounts []*big.Int) error {
	if len(tokenIds) != len(amounts) {
		return fmt.Errorf("QRC1155 %s: tokenIds and amounts length mismatch", what)
	}
	if len(tokenIds) > MaxQRC1155Batch {
		return fmt.Errorf("QRC1155 %s: %d entries exceeds the batch cap of %d", what, len(tokenIds), MaxQRC1155Batch)
	}
	return nil
}

// checkTransferTo refuses the two destinations no transfer may name: the
// zero address, and the collection itself (a balance held by the collection
// address can never be moved again, and for a module-managed collection it
// would break the module's accounting).
func (t *QRC1155Token) checkTransferTo(what string, to qcrypto.Address) error {
	if to.IsZero() {
		return fmt.Errorf("QRC1155 %s: cannot transfer to zero address", what)
	}
	if to == t.contract {
		return fmt.Errorf("QRC1155 %s: cannot transfer to the collection address", what)
	}
	return nil
}

// transferOne moves `amount` of `tokenId` from `from` to `to` with no
// authorisation and no event; the callers own both.
func (t *QRC1155Token) transferOne(from, to qcrypto.Address, tokenId, amount *big.Int) error {
	if err := checkTokenID(tokenId); err != nil {
		return err
	}
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("QRC1155 transfer: negative amount")
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

// SafeTransferFrom transfers `amount` of `tokenId` from `from` to `to`.
func (t *QRC1155Token) SafeTransferFrom(caller, from, to qcrypto.Address, tokenId, amount *big.Int) error {
	return t.SafeTransferFromWithEvents(caller, from, to, tokenId, amount, nil)
}

// SafeTransferFromWithEvents transfers and records the TransferSingle event.
func (t *QRC1155Token) SafeTransferFromWithEvents(caller, from, to qcrypto.Address, tokenId, amount *big.Int, receipt *types.Receipt) error {
	if err := t.checkTransferTo("transfer", to); err != nil {
		return err
	}
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 transfer: caller not approved")
	}
	if err := t.transferOne(from, to, tokenId, amount); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(TransferSingleEvent(t.contract, caller, from, to, tokenId, amount))
	}
	return nil
}

// SafeBatchTransferFrom transfers multiple (tokenId, amount) pairs from `from` to `to`.
func (t *QRC1155Token) SafeBatchTransferFrom(caller, from, to qcrypto.Address, tokenIds, amounts []*big.Int) error {
	return t.SafeBatchTransferFromWithEvents(caller, from, to, tokenIds, amounts, nil)
}

// SafeBatchTransferFromWithEvents transfers a batch and records one
// TransferBatch event.
func (t *QRC1155Token) SafeBatchTransferFromWithEvents(caller, from, to qcrypto.Address, tokenIds, amounts []*big.Int, receipt *types.Receipt) error {
	if err := checkBatch("batchTransfer", tokenIds, amounts); err != nil {
		return err
	}
	if err := t.checkTransferTo("batchTransfer", to); err != nil {
		return err
	}
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 batchTransfer: caller not approved")
	}
	for i, tokenId := range tokenIds {
		if err := t.transferOne(from, to, tokenId, amounts[i]); err != nil {
			return fmt.Errorf("QRC1155 batchTransfer[%d]: %w", i, err)
		}
	}
	if receipt != nil {
		receipt.AddLog(TransferBatchEvent(t.contract, caller, from, to, tokenIds, amounts))
	}
	return nil
}

// ── Minting ───────────────────────────────────────────────────────────────────

// mintOne credits `amount` of `tokenId` to `to` and grows the supply, with no
// authorisation and no event.
func (t *QRC1155Token) mintOne(to qcrypto.Address, tokenId, amount *big.Int, uri string) error {
	if err := checkTokenID(tokenId); err != nil {
		return err
	}
	if to.IsZero() {
		return fmt.Errorf("QRC1155 mint: cannot mint to zero address")
	}
	if amount == nil || amount.Sign() <= 0 {
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
		if err := t.setString(mtURISlot(tokenId), uri); err != nil {
			return err
		}
	}
	return nil
}

// mintUnchecked mints without an owner check and records TransferSingle from
// the zero address. For in-package use by the module that owns the
// collection; `operator` is the address acting (the module itself).
func (t *QRC1155Token) mintUnchecked(operator, to qcrypto.Address, tokenId, amount *big.Int, uri string, receipt *types.Receipt) error {
	if err := t.mintOne(to, tokenId, amount, uri); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(TransferSingleEvent(t.contract, operator, qcrypto.Address{}, to, tokenId, amount))
	}
	return nil
}

// mintBatchUnchecked is the batch form of mintUnchecked: one TransferBatch
// from the zero address.
func (t *QRC1155Token) mintBatchUnchecked(operator, to qcrypto.Address, tokenIds, amounts []*big.Int, uris []string, receipt *types.Receipt) error {
	if err := checkBatch("mintBatch", tokenIds, amounts); err != nil {
		return err
	}
	if len(uris) > len(tokenIds) {
		return fmt.Errorf("QRC1155 mintBatch: more uris than tokenIds")
	}
	for i, tokenId := range tokenIds {
		uri := ""
		if i < len(uris) {
			uri = uris[i]
		}
		if err := t.mintOne(to, tokenId, amounts[i], uri); err != nil {
			return fmt.Errorf("QRC1155 mintBatch[%d]: %w", i, err)
		}
	}
	if receipt != nil {
		receipt.AddLog(TransferBatchEvent(t.contract, operator, qcrypto.Address{}, to, tokenIds, amounts))
	}
	return nil
}

// Mint creates `amount` units of `tokenId` for `to`. Only callable by owner.
func (t *QRC1155Token) Mint(caller, to qcrypto.Address, tokenId, amount *big.Int, uri string) error {
	return t.MintWithEvents(caller, to, tokenId, amount, uri, nil)
}

// MintWithEvents mints and records the TransferSingle event (from zero).
func (t *QRC1155Token) MintWithEvents(caller, to qcrypto.Address, tokenId, amount *big.Int, uri string, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	return t.mintUnchecked(caller, to, tokenId, amount, uri, receipt)
}

// MintBatch mints multiple token types at once. Only callable by owner.
func (t *QRC1155Token) MintBatch(caller, to qcrypto.Address, tokenIds, amounts []*big.Int, uris []string) error {
	return t.MintBatchWithEvents(caller, to, tokenIds, amounts, uris, nil)
}

// MintBatchWithEvents mints a batch and records one TransferBatch event.
func (t *QRC1155Token) MintBatchWithEvents(caller, to qcrypto.Address, tokenIds, amounts []*big.Int, uris []string, receipt *types.Receipt) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	return t.mintBatchUnchecked(caller, to, tokenIds, amounts, uris, receipt)
}

// ── Burning ───────────────────────────────────────────────────────────────────

// burnOne debits `amount` of `tokenId` from `from` and shrinks the supply,
// with no authorisation and no event.
func (t *QRC1155Token) burnOne(from qcrypto.Address, tokenId, amount *big.Int) error {
	if err := checkTokenID(tokenId); err != nil {
		return err
	}
	if amount == nil || amount.Sign() <= 0 {
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

// burnUnchecked burns without an approval check and records TransferSingle to
// the zero address. For in-package use by the module that owns the
// collection; `operator` is the address acting (the module itself).
func (t *QRC1155Token) burnUnchecked(operator, from qcrypto.Address, tokenId, amount *big.Int, receipt *types.Receipt) error {
	if err := t.burnOne(from, tokenId, amount); err != nil {
		return err
	}
	if receipt != nil {
		receipt.AddLog(TransferSingleEvent(t.contract, operator, from, qcrypto.Address{}, tokenId, amount))
	}
	return nil
}

// burnBatchUnchecked is the batch form of burnUnchecked: one TransferBatch to
// the zero address.
func (t *QRC1155Token) burnBatchUnchecked(operator, from qcrypto.Address, tokenIds, amounts []*big.Int, receipt *types.Receipt) error {
	if err := checkBatch("burnBatch", tokenIds, amounts); err != nil {
		return err
	}
	for i, tokenId := range tokenIds {
		if err := t.burnOne(from, tokenId, amounts[i]); err != nil {
			return fmt.Errorf("QRC1155 burnBatch[%d]: %w", i, err)
		}
	}
	if receipt != nil {
		receipt.AddLog(TransferBatchEvent(t.contract, operator, from, qcrypto.Address{}, tokenIds, amounts))
	}
	return nil
}

// Burn destroys `amount` units of `tokenId` from `from`; the caller must be
// `from` or an approved operator.
func (t *QRC1155Token) Burn(caller, from qcrypto.Address, tokenId, amount *big.Int) error {
	return t.BurnWithEvents(caller, from, tokenId, amount, nil)
}

// BurnWithEvents burns and records the TransferSingle event (to zero).
func (t *QRC1155Token) BurnWithEvents(caller, from qcrypto.Address, tokenId, amount *big.Int, receipt *types.Receipt) error {
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 burn: caller not approved")
	}
	return t.burnUnchecked(caller, from, tokenId, amount, receipt)
}

// BurnBatch destroys multiple token types from `from`.
func (t *QRC1155Token) BurnBatch(caller, from qcrypto.Address, tokenIds, amounts []*big.Int) error {
	return t.BurnBatchWithEvents(caller, from, tokenIds, amounts, nil)
}

// BurnBatchWithEvents burns a batch and records one TransferBatch event.
func (t *QRC1155Token) BurnBatchWithEvents(caller, from qcrypto.Address, tokenIds, amounts []*big.Int, receipt *types.Receipt) error {
	ok, err := t.isApprovedOrOwner(caller, from)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC1155 burnBatch: caller not approved")
	}
	return t.burnBatchUnchecked(caller, from, tokenIds, amounts, receipt)
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
