// Package vm — QRC-721: Splendor Quantum Chain Non-Fungible Token standard.
//
// QRC-721 is the quantum-native NFT standard — the SQC equivalent of ERC-721.
// Every token is unique, identified by a uint256 tokenId.
// All addresses use the SQ: format (32 bytes, SHA3-256 derived from ML-DSA-65 public key).
// All state operations go through the StateDB with SHA3-256 slot keys.
// No ECDSA. No keccak. No Ethereum address format.
//
// Interface:
//
//	balanceOf(owner sqaddress) → uint256
//	ownerOf(tokenId uint256) → sqaddress
//	transfer(to sqaddress, tokenId uint256) → bool
//	approve(to sqaddress, tokenId uint256) → bool
//	getApproved(tokenId uint256) → sqaddress
//	setApprovalForAll(operator sqaddress, approved bool) → bool
//	isApprovedForAll(owner sqaddress, operator sqaddress) → bool
//	transferFrom(from sqaddress, to sqaddress, tokenId uint256) → bool
//	mint(to sqaddress, tokenId uint256, uri string) → bool
//	burn(tokenId uint256) → bool
//	tokenURI(tokenId uint256) → string
//	totalSupply() → uint256
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// ── QRC-721 storage slot derivation ───────────────────────────────────────────
//
// All slots are SHA3-256 hashes for quantum-safe, collision-resistant layout.
//
// balanceOf[addr]        = SHA3-256("qrc721:balance:" + addr)
// ownerOf[tokenId]       = SHA3-256("qrc721:owner:" + tokenId_bytes32)
// approved[tokenId]      = SHA3-256("qrc721:approved:" + tokenId_bytes32)
// approvedForAll[o][op]  = SHA3-256("qrc721:approvedall:" + owner + operator)
// tokenURI[tokenId]      = SHA3-256("qrc721:uri:" + tokenId_bytes32)
// totalSupply            = SHA3-256("qrc721:total_supply")
// name                   = SHA3-256("qrc721:name")
// symbol                 = SHA3-256("qrc721:symbol")

var (
	slotNFTTotalSupply = qcrypto.Hash32([]byte("qrc721:total_supply"))
	slotNFTName        = qcrypto.Hash32([]byte("qrc721:name"))
	slotNFTSymbol      = qcrypto.Hash32([]byte("qrc721:symbol"))
	slotNFTOwner       = qcrypto.Hash32([]byte("qrc721:owner"))
)

func nftBalanceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc721:balance:"), addr[:]...))
}

func nftOwnerSlot(tokenId *big.Int) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc721:owner:"), padTo32(tokenId)...))
}

func nftApprovedSlot(tokenId *big.Int) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc721:approved:"), padTo32(tokenId)...))
}

func nftApprovalForAllSlot(owner, operator qcrypto.Address) [32]byte {
	key := append([]byte("qrc721:approvedall:"), owner[:]...)
	key = append(key, operator[:]...)
	return qcrypto.Hash32(key)
}

func nftURISlot(tokenId *big.Int) [32]byte {
	return qcrypto.Hash32(append([]byte("qrc721:uri:"), padTo32(tokenId)...))
}

// padTo32 pads a big.Int to a 32-byte big-endian slice.
func padTo32(v *big.Int) []byte {
	b := make([]byte, 32)
	vb := v.Bytes()
	copy(b[32-len(vb):], vb)
	return b
}

// ── QRC721Token ───────────────────────────────────────────────────────────────

// QRC721Token is the native Go implementation of the QRC-721 NFT standard.
// It stores all state in the StateDB under the contract's address.
type QRC721Token struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewQRC721Token returns a handle to an existing QRC-721 contract.
func NewQRC721Token(stateDB *state.StateDB, contract qcrypto.Address) *QRC721Token {
	return &QRC721Token{stateDB: stateDB, contract: contract}
}

// DeployQRC721 deploys a new QRC-721 NFT contract.
// Returns the contract address.
func DeployQRC721(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name, symbol string,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	// Deterministic contract address: SHA3-256(deployer || nonce || "qrc721")
	nonce := []byte(fmt.Sprintf("%d", deployerNonce))
	input := append(deployer[:], nonce...)
	input = append(input, []byte("qrc721")...)
	hash := qcrypto.Hash32(input)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	nft := NewQRC721Token(stateDB, contractAddr)
	if err := nft.setString(slotNFTName, name); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC721 deploy name: %w", err)
	}
	if err := nft.setString(slotNFTSymbol, symbol); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC721 deploy symbol: %w", err)
	}
	if err := nft.setBigInt(slotNFTTotalSupply, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC721 deploy supply: %w", err)
	}
	if err := stateDB.SetStorageValue(contractAddr, slotNFTOwner, deployer[:]); err != nil {
		return qcrypto.Address{}, fmt.Errorf("QRC721 deploy owner: %w", err)
	}
	return contractAddr, nil
}

// Owner returns the collection owner (the deployer). Zero if never deployed.
func (t *QRC721Token) Owner() (qcrypto.Address, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slotNFTOwner)
	if err != nil {
		return qcrypto.Address{}, err
	}
	if len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var owner qcrypto.Address
	copy(owner[:], b)
	return owner, nil
}

func (t *QRC721Token) requireOwner(caller qcrypto.Address) error {
	owner, err := t.Owner()
	if err != nil {
		return err
	}
	if owner.IsZero() || caller != owner {
		return fmt.Errorf("QRC721: caller %s is not owner", caller)
	}
	return nil
}

// ── Metadata ──────────────────────────────────────────────────────────────────

// Name returns the NFT collection name.
func (t *QRC721Token) Name() (string, error) { return t.getString(slotNFTName) }

// Symbol returns the NFT collection symbol (ticker).
func (t *QRC721Token) Symbol() (string, error) { return t.getString(slotNFTSymbol) }

// TotalSupply returns the total number of tokens minted (not burned).
func (t *QRC721Token) TotalSupply() (*big.Int, error) { return t.getBigInt(slotNFTTotalSupply) }

// TokenURI returns the metadata URI for a token (IPFS, HTTPS, etc.).
func (t *QRC721Token) TokenURI(tokenId *big.Int) (string, error) {
	return t.getString(nftURISlot(tokenId))
}

// ── Ownership ─────────────────────────────────────────────────────────────────

// OwnerOf returns the owner of a token. Returns zero address if burned or unminted.
func (t *QRC721Token) OwnerOf(tokenId *big.Int) (qcrypto.Address, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, nftOwnerSlot(tokenId))
	if err != nil || len(b) == 0 {
		return qcrypto.Address{}, nil
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

// BalanceOf returns how many tokens `owner` holds.
func (t *QRC721Token) BalanceOf(owner qcrypto.Address) (*big.Int, error) {
	return t.getBigInt(nftBalanceSlot(owner))
}

// Exists returns true if tokenId has been minted and not burned.
func (t *QRC721Token) Exists(tokenId *big.Int) (bool, error) {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return false, err
	}
	return !owner.IsZero(), nil
}

// ── Approvals ─────────────────────────────────────────────────────────────────

// GetApproved returns the address approved to transfer a specific token.
func (t *QRC721Token) GetApproved(tokenId *big.Int) (qcrypto.Address, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, nftApprovedSlot(tokenId))
	if err != nil || len(b) == 0 {
		return qcrypto.Address{}, nil
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

// Approve sets `to` as the approved address for `tokenId`.
// Only the token owner can call this.
func (t *QRC721Token) Approve(caller, to qcrypto.Address, tokenId *big.Int) error {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return err
	}
	if owner.IsZero() {
		return fmt.Errorf("QRC721 approve: token %s does not exist", tokenId)
	}
	if caller != owner {
		isOp, _ := t.IsApprovedForAll(owner, caller)
		if !isOp {
			return fmt.Errorf("QRC721 approve: caller is not owner or operator")
		}
	}
	return t.stateDB.SetStorageValue(t.contract, nftApprovedSlot(tokenId), to[:])
}

// SetApprovalForAll grants or revokes `operator` permission to manage all of `owner`'s tokens.
func (t *QRC721Token) SetApprovalForAll(owner, operator qcrypto.Address, approved bool) error {
	var val []byte
	if approved {
		val = []byte{1}
	} else {
		val = []byte{0}
	}
	return t.stateDB.SetStorageValue(t.contract, nftApprovalForAllSlot(owner, operator), val)
}

// IsApprovedForAll checks if `operator` has been approved to manage all of `owner`'s tokens.
func (t *QRC721Token) IsApprovedForAll(owner, operator qcrypto.Address) (bool, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, nftApprovalForAllSlot(owner, operator))
	if err != nil || len(b) == 0 {
		return false, nil
	}
	return b[0] == 1, nil
}

// IsApprovedOrOwner returns true if `caller` can transfer `tokenId`.
func (t *QRC721Token) IsApprovedOrOwner(caller qcrypto.Address, tokenId *big.Int) (bool, error) {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return false, err
	}
	if caller == owner {
		return true, nil
	}
	approved, err := t.GetApproved(tokenId)
	if err != nil {
		return false, err
	}
	if caller == approved {
		return true, nil
	}
	return t.IsApprovedForAll(owner, caller)
}

// ── Transfers ─────────────────────────────────────────────────────────────────

// Transfer transfers `tokenId` from its current owner to `to`.
// Only the owner (or approved address) may call this.
func (t *QRC721Token) Transfer(caller, to qcrypto.Address, tokenId *big.Int) error {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return err
	}
	if owner.IsZero() {
		return fmt.Errorf("QRC721 transfer: token %s does not exist", tokenId)
	}
	if to.IsZero() {
		return fmt.Errorf("QRC721 transfer: cannot transfer to zero address")
	}
	ok, err := t.IsApprovedOrOwner(caller, tokenId)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC721 transfer: caller %s is not owner or approved", caller)
	}
	return t.doTransfer(owner, to, tokenId)
}

// TransferFrom is the explicit from/to form (same as Transfer but checks from == owner).
func (t *QRC721Token) TransferFrom(caller, from, to qcrypto.Address, tokenId *big.Int) error {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return err
	}
	if owner != from {
		return fmt.Errorf("QRC721 transferFrom: from %s is not owner of token %s", from, tokenId)
	}
	ok, err := t.IsApprovedOrOwner(caller, tokenId)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC721 transferFrom: caller not approved")
	}
	if to.IsZero() {
		return fmt.Errorf("QRC721 transferFrom: cannot transfer to zero address")
	}
	return t.doTransfer(from, to, tokenId)
}

func (t *QRC721Token) doTransfer(from, to qcrypto.Address, tokenId *big.Int) error {
	// Clear approval
	var zero [qcrypto.AddressSize]byte
	t.stateDB.SetStorageValue(t.contract, nftApprovedSlot(tokenId), zero[:])

	// from == to touches one balance slot. The decrement is skipped whenever
	// fromBal is 0, but the increment is not, so a self-transfer from an
	// inconsistent counter would still hand out a token. Ownership is already
	// correct here, so only the approval reset applies.
	if from == to {
		return t.stateDB.SetStorageValue(t.contract, nftOwnerSlot(tokenId), to[:])
	}

	// Decrease from balance
	fromBal, _ := t.getBigInt(nftBalanceSlot(from))
	if fromBal.Sign() > 0 {
		t.setBigInt(nftBalanceSlot(from), new(big.Int).Sub(fromBal, big.NewInt(1)))
	}

	// Increase to balance
	toBal, _ := t.getBigInt(nftBalanceSlot(to))
	t.setBigInt(nftBalanceSlot(to), new(big.Int).Add(toBal, big.NewInt(1)))

	// Set new owner
	return t.stateDB.SetStorageValue(t.contract, nftOwnerSlot(tokenId), to[:])
}

// ── Minting & Burning ─────────────────────────────────────────────────────────

// Mint creates a new token with `tokenId` owned by `to`. Only the collection owner may mint.
func (t *QRC721Token) Mint(caller, to qcrypto.Address, tokenId *big.Int, uri string) error {
	if err := t.requireOwner(caller); err != nil {
		return err
	}
	if to.IsZero() {
		return fmt.Errorf("QRC721 mint: cannot mint to zero address")
	}
	exists, err := t.Exists(tokenId)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("QRC721 mint: token %s already exists", tokenId)
	}

	// Set owner
	if err := t.stateDB.SetStorageValue(t.contract, nftOwnerSlot(tokenId), to[:]); err != nil {
		return err
	}

	// Increase balance
	bal, _ := t.getBigInt(nftBalanceSlot(to))
	if err := t.setBigInt(nftBalanceSlot(to), new(big.Int).Add(bal, big.NewInt(1))); err != nil {
		return err
	}

	// Set URI
	if uri != "" {
		if err := t.setString(nftURISlot(tokenId), uri); err != nil {
			return err
		}
	}

	// Increment total supply
	supply, _ := t.getBigInt(slotNFTTotalSupply)
	return t.setBigInt(slotNFTTotalSupply, new(big.Int).Add(supply, big.NewInt(1)))
}

// Burn destroys a token. Only the owner (or approved) may burn.
func (t *QRC721Token) Burn(caller qcrypto.Address, tokenId *big.Int) error {
	owner, err := t.OwnerOf(tokenId)
	if err != nil {
		return err
	}
	if owner.IsZero() {
		return fmt.Errorf("QRC721 burn: token %s does not exist", tokenId)
	}
	ok, err := t.IsApprovedOrOwner(caller, tokenId)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("QRC721 burn: caller not approved")
	}

	// Clear owner and approval
	var zero [qcrypto.AddressSize]byte
	t.stateDB.SetStorageValue(t.contract, nftOwnerSlot(tokenId), zero[:])
	t.stateDB.SetStorageValue(t.contract, nftApprovedSlot(tokenId), zero[:])

	// Decrease owner balance
	bal, _ := t.getBigInt(nftBalanceSlot(owner))
	if bal.Sign() > 0 {
		t.setBigInt(nftBalanceSlot(owner), new(big.Int).Sub(bal, big.NewInt(1)))
	}

	// Decrease total supply
	supply, _ := t.getBigInt(slotNFTTotalSupply)
	if supply.Sign() > 0 {
		t.setBigInt(slotNFTTotalSupply, new(big.Int).Sub(supply, big.NewInt(1)))
	}
	return nil
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (t *QRC721Token) setBigInt(slot [32]byte, v *big.Int) error {
	return t.stateDB.SetStorageValue(t.contract, slot, v.Bytes())
}

func (t *QRC721Token) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := t.stateDB.GetStorageValue(t.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (t *QRC721Token) setString(slot [32]byte, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return t.stateDB.SetStorageValue(t.contract, slot, b)
}

func (t *QRC721Token) getString(slot [32]byte) (string, error) {
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
