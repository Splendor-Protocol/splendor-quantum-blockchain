// Package vm — Multi-signature Wallet for Splendor Quantum Chain.
//
// A quantum-safe N-of-M multisig wallet where:
//   - N = required confirmations (threshold)
//   - M = total number of owners
//   - All confirmations use ML-DSA-65 signatures (no ECDSA)
//
// Workflow:
//   1. Any owner submits a transaction proposal (to, value, data, txType)
//   2. Owners confirm the proposal by submitting their ML-DSA-65 signatures
//   3. Once N confirmations are collected, anyone can execute the proposal
//   4. Executed proposals cannot be re-executed
//   5. Any owner can revoke their own confirmation before execution
//
// Security properties:
//   - Each confirmation is an ML-DSA-65 signature over the proposal hash
//   - No confirmation can be forged without the owner's private key
//   - Proposals are identified by sequential nonces (prevents replay)
//   - Executed proposals are marked permanently, preventing re-execution
//
// All storage: SHA3-256 slots. All addresses: SQ:. No ECDSA. No keccak.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Storage slots ─────────────────────────────────────────────────────────────

var (
	slotMSigThreshold   = qcrypto.Hash32([]byte("msig:threshold"))
	slotMSigOwnerCount  = qcrypto.Hash32([]byte("msig:owner_count"))
	slotMSigNonce       = qcrypto.Hash32([]byte("msig:nonce"))
)

func msigOwnerSlot(owner qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("msig:owner:"), owner[:]...))
}

func msigProposalSlot(nonce uint64) [32]byte {
	nonceBytes := make([]byte, 8)
	n := nonce
	for i := 7; i >= 0; i-- {
		nonceBytes[i] = byte(n)
		n >>= 8
	}
	return qcrypto.Hash32(append([]byte("msig:proposal:"), nonceBytes...))
}

func msigConfirmSlot(nonce uint64, owner qcrypto.Address) [32]byte {
	nonceBytes := make([]byte, 8)
	n := nonce
	for i := 7; i >= 0; i-- {
		nonceBytes[i] = byte(n)
		n >>= 8
	}
	key := append([]byte("msig:confirm:"), nonceBytes...)
	key = append(key, owner[:]...)
	return qcrypto.Hash32(key)
}

// ── Data structures ────────────────────────────────────────────────────────────

// MSigTxType identifies what kind of action the proposal performs.
type MSigTxType uint8

const (
	MSigTransfer   MSigTxType = 0 // Transfer SPLD to an address
	MSigCallToken  MSigTxType = 1 // Call a QRC-20 token function
	MSigCallAny    MSigTxType = 2 // Call any contract with arbitrary calldata
	MSigAddOwner   MSigTxType = 3 // Add a new owner
	MSigRemOwner   MSigTxType = 4 // Remove an existing owner
	MSigChangeThreshold MSigTxType = 5 // Change the required threshold
)

// MSigOwner stores info about a registered wallet owner.
type MSigOwner struct {
	Address   string `json:"address"`
	PublicKey []byte `json:"public_key"` // ML-DSA-65 public key
	Name      string `json:"name,omitempty"`
	Active    bool   `json:"active"`
}

// MSigProposal is a submitted but not yet executed transaction.
type MSigProposal struct {
	Nonce       uint64     `json:"nonce"`
	TxType      MSigTxType `json:"tx_type"`
	To          string     `json:"to"`           // target address
	Value       *big.Int   `json:"value"`        // SPLD amount (for transfers)
	Data        []byte     `json:"data,omitempty"` // calldata (for contract calls)
	Description string     `json:"description,omitempty"` // human-readable
	Proposer    string     `json:"proposer"`
	Confirmations []string `json:"confirmations"` // addresses of confirmed owners
	Executed    bool       `json:"executed"`
	Cancelled   bool       `json:"cancelled"`
}

// MSigConfirmation is an ML-DSA-65-signed confirmation from one owner.
type MSigConfirmation struct {
	Owner     string `json:"owner"`
	PublicKey []byte `json:"public_key"`
	Signature []byte `json:"signature"` // ML-DSA-65 sig over ProposalHash
}

// ── MultiSigWallet ────────────────────────────────────────────────────────────

// MultiSigWallet is the N-of-M ML-DSA-65 multi-signature wallet.
type MultiSigWallet struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewMultiSigWallet returns a handle to an existing multisig contract.
func NewMultiSigWallet(stateDB *state.StateDB, contract qcrypto.Address) *MultiSigWallet {
	return &MultiSigWallet{stateDB: stateDB, contract: contract}
}

// DeployMultiSig deploys a new N-of-M multisig wallet.
// owners: initial owner addresses with their ML-DSA-65 public keys.
// threshold: required confirmations (must be <= len(owners)).
func DeployMultiSig(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	owners []MSigOwner,
	threshold uint64,
	nonce uint64,
) (qcrypto.Address, error) {
	if len(owners) == 0 {
		return qcrypto.Address{}, fmt.Errorf("multisig: must have at least 1 owner")
	}
	if threshold == 0 || threshold > uint64(len(owners)) {
		return qcrypto.Address{}, fmt.Errorf("multisig: threshold must be 1..%d, got %d", len(owners), threshold)
	}

	// Deterministic address
	input := append(deployer[:], []byte(fmt.Sprintf("%d:msig", nonce))...)
	hash := qcrypto.Hash32(input)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	w := NewMultiSigWallet(stateDB, contractAddr)

	// Store threshold
	tBuf := make([]byte, 8)
	t := threshold
	for i := 7; i >= 0; i-- {
		tBuf[i] = byte(t)
		t >>= 8
	}
	if err := stateDB.SetStorageValue(contractAddr, slotMSigThreshold, tBuf); err != nil {
		return qcrypto.Address{}, err
	}

	// Register owners. Every owner is Active from the start (the router used
	// to store whatever it was handed, which was Active=false, so no owner of
	// a deployed wallet could ever confirm), the key must be the key of the
	// address it claims, and an address may appear once.
	seen := make(map[qcrypto.Address]struct{}, len(owners))
	for _, owner := range owners {
		addr, err := qcrypto.AddressFromString(owner.Address)
		if err != nil {
			return qcrypto.Address{}, fmt.Errorf("multisig: invalid owner address %q: %w", owner.Address, err)
		}
		if err := checkOwnerKey(addr, owner.PublicKey); err != nil {
			return qcrypto.Address{}, err
		}
		if _, dup := seen[addr]; dup {
			return qcrypto.Address{}, fmt.Errorf("multisig: duplicate owner %s", addr)
		}
		seen[addr] = struct{}{}
		rec := MSigOwner{Address: addr.String(), PublicKey: append([]byte(nil), owner.PublicKey...), Name: owner.Name, Active: true}
		if err := w.setOwner(addr, &rec); err != nil {
			return qcrypto.Address{}, err
		}
	}

	// Store owner count
	cBuf := make([]byte, 8)
	c := uint64(len(owners))
	for i := 7; i >= 0; i-- {
		cBuf[i] = byte(c)
		c >>= 8
	}
	stateDB.SetStorageValue(contractAddr, slotMSigOwnerCount, cBuf)

	return contractAddr, nil
}

// checkOwnerKey requires pubKey to be an ML-DSA-65 key whose address is addr.
// An owner record whose key belongs to someone else would let that someone
// confirm in the owner's name.
func checkOwnerKey(addr qcrypto.Address, pubKey []byte) error {
	if len(pubKey) != qcrypto.PublicKeySize {
		return fmt.Errorf("multisig: invalid public key for %s", addr)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKey)
	if err != nil {
		return fmt.Errorf("multisig: invalid public key for %s: %w", addr, err)
	}
	if qcrypto.DeriveAddress(pub) != addr {
		return fmt.Errorf("multisig: public key does not belong to owner %s", addr)
	}
	return nil
}

// ── Proposal lifecycle ────────────────────────────────────────────────────────

// ProposalHash computes the canonical hash of a proposal that owners sign.
//
//	hash = SHA3-256("msig:proposal:v2:" + chain_id + 0x00 + contract + nonce8
//	                + tx_type + to + value32 + sha3(data))
//
// The chain id is inside the digest because a wallet's address is derived
// from its deployer and nonce alone, so the same wallet exists at the same
// address on every tier; without it a confirmation signed for a testnet
// proposal would verify for the identical proposal on mainnet. The type is
// inside it because the same (to, value, data) means a transfer under one
// type and an owner change under another.
func ProposalHash(contract qcrypto.Address, nonce uint64, txType MSigTxType, to qcrypto.Address, value *big.Int, data []byte) [32]byte {
	nonceBytes := make([]byte, 8)
	n := nonce
	for i := 7; i >= 0; i-- {
		nonceBytes[i] = byte(n)
		n >>= 8
	}

	valueBytes := make([]byte, 32)
	if value != nil && value.Sign() > 0 {
		vb := value.Bytes()
		copy(valueBytes[32-len(vb):], vb)
	}

	dataHash := qcrypto.Hash32(data)

	msg := append([]byte("msig:proposal:v2:"), []byte(types.ChainID)...)
	msg = append(msg, 0x00)
	msg = append(msg, contract[:]...)
	msg = append(msg, nonceBytes...)
	msg = append(msg, byte(txType))
	msg = append(msg, to[:]...)
	msg = append(msg, valueBytes...)
	msg = append(msg, dataHash[:]...)
	return qcrypto.Hash32(msg)
}

// validateAction checks a proposal's payload against the wallet's current
// state. It runs at Submit, so a proposal that can never execute is refused
// up front, and again at Execute, because owners and threshold may have
// changed in between. Transfer, owner and threshold changes are supported;
// contract calls are refused rather than "executed" as no-ops, which is what
// MSigCallToken and MSigCallAny used to do — a proposal marked executed that
// did nothing.
func (w *MultiSigWallet) validateAction(txType MSigTxType, to qcrypto.Address, value *big.Int, data []byte) error {
	switch txType {
	case MSigTransfer:
		if value == nil || value.Sign() <= 0 {
			return fmt.Errorf("multisig: transfer value must be positive")
		}
		if to == (qcrypto.Address{}) {
			return fmt.Errorf("multisig: transfer to the zero address")
		}
		return nil
	case MSigAddOwner:
		addr, pubKey, _, err := decodeAddOwnerData(data)
		if err != nil {
			return err
		}
		if err := checkOwnerKey(addr, pubKey); err != nil {
			return err
		}
		if info, _ := w.GetOwner(addr); info != nil && info.Active {
			return fmt.Errorf("multisig: %s is already an owner", addr)
		}
		return nil
	case MSigRemOwner:
		if len(data) < qcrypto.AddressSize {
			return fmt.Errorf("multisig: RemoveOwner data too short")
		}
		var addr qcrypto.Address
		copy(addr[:], data[:qcrypto.AddressSize])
		info, _ := w.GetOwner(addr)
		if info == nil || !info.Active {
			return fmt.Errorf("multisig: %s is not an active owner", addr)
		}
		count, _ := w.OwnerCount()
		threshold, _ := w.Threshold()
		if count <= 1 || count-1 < threshold {
			return fmt.Errorf("multisig: removing %s would leave %d owners below the threshold of %d", addr, count-1, threshold)
		}
		return nil
	case MSigChangeThreshold:
		if len(data) < 8 {
			return fmt.Errorf("multisig: ChangeThreshold data too short")
		}
		var newThreshold uint64
		for i := 0; i < 8; i++ {
			newThreshold = newThreshold<<8 | uint64(data[i])
		}
		count, _ := w.OwnerCount()
		if newThreshold == 0 || newThreshold > count {
			return fmt.Errorf("multisig: invalid threshold %d (owners: %d)", newThreshold, count)
		}
		return nil
	case MSigCallToken, MSigCallAny:
		return fmt.Errorf("multisig: proposal type %d (contract call) is not supported by this module", txType)
	default:
		return fmt.Errorf("multisig: unknown proposal type %d", txType)
	}
}

// decodeAddOwnerData splits [32-byte address][1952-byte pubkey][name].
func decodeAddOwnerData(data []byte) (qcrypto.Address, []byte, string, error) {
	if len(data) < qcrypto.AddressSize+qcrypto.PublicKeySize {
		return qcrypto.Address{}, nil, "", fmt.Errorf("multisig: AddOwner data too short")
	}
	var addr qcrypto.Address
	copy(addr[:], data[:qcrypto.AddressSize])
	pubKey := data[qcrypto.AddressSize : qcrypto.AddressSize+qcrypto.PublicKeySize]
	name := string(data[qcrypto.AddressSize+qcrypto.PublicKeySize:])
	if len(name) > MaxModuleNameBytes {
		return qcrypto.Address{}, nil, "", fmt.Errorf("multisig: owner name is %d bytes, above the cap of %d", len(name), MaxModuleNameBytes)
	}
	return addr, pubKey, name, nil
}

// Submit creates a new transaction proposal.
// proposer must be a registered owner.
func (w *MultiSigWallet) Submit(
	proposer qcrypto.Address,
	txType MSigTxType,
	to qcrypto.Address,
	value *big.Int,
	data []byte,
	description string,
) (uint64, error) {
	if err := w.requireOwner(proposer); err != nil {
		return 0, err
	}
	if value == nil {
		value = big.NewInt(0)
	}
	if len(description) > MaxModuleURIBytes {
		return 0, fmt.Errorf("multisig: description is %d bytes, above the cap of %d", len(description), MaxModuleURIBytes)
	}
	if err := w.validateAction(txType, to, value, data); err != nil {
		return 0, err
	}

	nonce, err := w.nextNonce()
	if err != nil {
		return 0, err
	}

	proposal := &MSigProposal{
		Nonce:         nonce,
		TxType:        txType,
		To:            to.String(),
		Value:         new(big.Int).Set(value),
		Data:          data,
		Description:   description,
		Proposer:      proposer.String(),
		Confirmations: []string{},
		Executed:      false,
		Cancelled:     false,
	}

	if err := w.setProposal(nonce, proposal); err != nil {
		return 0, err
	}

	return nonce, nil
}

// Confirm adds an ML-DSA-65-signed confirmation from an owner.
// The owner signs ProposalHash(contract, nonce, to, value, data).
func (w *MultiSigWallet) Confirm(
	owner qcrypto.Address,
	nonce uint64,
	signature []byte,
) error {
	info, err := w.GetOwner(owner)
	if err != nil || info == nil || !info.Active {
		return fmt.Errorf("multisig: %s is not an active owner", owner)
	}

	proposal, err := w.GetProposal(nonce)
	if err != nil || proposal == nil {
		return fmt.Errorf("multisig: proposal %d not found", nonce)
	}
	if proposal.Executed {
		return fmt.Errorf("multisig: proposal %d already executed", nonce)
	}
	if proposal.Cancelled {
		return fmt.Errorf("multisig: proposal %d was cancelled", nonce)
	}

	// Check not already confirmed
	for _, addr := range proposal.Confirmations {
		if addr == owner.String() {
			return fmt.Errorf("multisig: %s already confirmed proposal %d", owner, nonce)
		}
	}

	// Parse target address
	toAddr, err := qcrypto.AddressFromString(proposal.To)
	if err != nil {
		return fmt.Errorf("multisig: invalid proposal.To: %w", err)
	}

	// Verify ML-DSA-65 signature
	hash := ProposalHash(w.contract, nonce, proposal.TxType, toAddr, proposal.Value, proposal.Data)
	pub, err := qcrypto.PublicKeyFromBytes(info.PublicKey)
	if err != nil {
		return fmt.Errorf("multisig: invalid owner public key: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxMultisig, signature) {
		return fmt.Errorf("multisig: invalid ML-DSA signature from %s", owner)
	}

	// Store confirmation
	conf := &MSigConfirmation{
		Owner:     owner.String(),
		PublicKey: info.PublicKey,
		Signature: signature,
	}
	if err := w.setConfirmation(nonce, owner, conf); err != nil {
		return err
	}

	// Update proposal confirmations list
	proposal.Confirmations = append(proposal.Confirmations, owner.String())
	return w.setProposal(nonce, proposal)
}

// Revoke removes an owner's confirmation from a proposal.
func (w *MultiSigWallet) Revoke(owner qcrypto.Address, nonce uint64) error {
	if err := w.requireOwner(owner); err != nil {
		return err
	}

	proposal, err := w.GetProposal(nonce)
	if err != nil || proposal == nil {
		return fmt.Errorf("multisig: proposal %d not found", nonce)
	}
	if proposal.Executed {
		return fmt.Errorf("multisig: cannot revoke — proposal %d already executed", nonce)
	}

	// Remove from confirmations list
	newConfs := make([]string, 0, len(proposal.Confirmations))
	found := false
	for _, addr := range proposal.Confirmations {
		if addr == owner.String() {
			found = true
			continue
		}
		newConfs = append(newConfs, addr)
	}
	if !found {
		return fmt.Errorf("multisig: %s has not confirmed proposal %d", owner, nonce)
	}

	proposal.Confirmations = newConfs
	// Delete stored confirmation
	w.stateDB.SetStorageValue(w.contract, msigConfirmSlot(nonce, owner), nil)
	return w.setProposal(nonce, proposal)
}

// Execute runs a proposal that has reached the required threshold.
// Anyone can call this once enough confirmations are collected.
func (w *MultiSigWallet) Execute(caller qcrypto.Address, nonce uint64, stateDB *state.StateDB) error {
	proposal, err := w.GetProposal(nonce)
	if err != nil || proposal == nil {
		return fmt.Errorf("multisig: proposal %d not found", nonce)
	}
	if proposal.Executed {
		return fmt.Errorf("multisig: proposal %d already executed", nonce)
	}
	if proposal.Cancelled {
		return fmt.Errorf("multisig: proposal %d was cancelled", nonce)
	}

	threshold, err := w.Threshold()
	if err != nil {
		return err
	}
	// Only confirmations from owners who are STILL active count: an owner
	// removed since confirming must not carry a proposal over the line.
	live := uint64(0)
	for _, addr := range proposal.Confirmations {
		a, perr := qcrypto.AddressFromString(addr)
		if perr != nil {
			continue
		}
		if info, _ := w.GetOwner(a); info != nil && info.Active {
			live++
		}
	}
	if live < threshold {
		return fmt.Errorf("multisig: insufficient confirmations (%d / %d required)", live, threshold)
	}

	toAddr, err := qcrypto.AddressFromString(proposal.To)
	if err != nil {
		return err
	}
	// The payload is judged against the wallet as it is NOW, not as it was
	// at Submit: owners and threshold may have changed in between.
	if err := w.validateAction(proposal.TxType, toAddr, proposal.Value, proposal.Data); err != nil {
		return err
	}

	// Mark executed BEFORE the effect, so a failed transfer below cannot be
	// retried against a proposal that stays open (the module snapshot rolls
	// this back together with the effect when the call fails).
	proposal.Executed = true
	if err := w.setProposal(nonce, proposal); err != nil {
		return err
	}

	switch proposal.TxType {
	case MSigTransfer:
		if err := stateDB.Transfer(w.contract, toAddr, proposal.Value); err != nil {
			return fmt.Errorf("multisig execute: transfer failed: %w", err)
		}

	case MSigAddOwner:
		newOwnerAddr, newPubKey, name, err := decodeAddOwnerData(proposal.Data)
		if err != nil {
			return err
		}
		newOwner := &MSigOwner{
			Address:   newOwnerAddr.String(),
			PublicKey: append([]byte(nil), newPubKey...),
			Name:      name,
			Active:    true,
		}
		if err := w.setOwner(newOwnerAddr, newOwner); err != nil {
			return err
		}
		count, _ := w.OwnerCount()
		if err := w.setUint64(slotMSigOwnerCount, count+1); err != nil {
			return err
		}

	case MSigRemOwner:
		var remAddr qcrypto.Address
		copy(remAddr[:], proposal.Data[:qcrypto.AddressSize])
		info, _ := w.GetOwner(remAddr) // validated active above
		info.Active = false
		if err := w.setOwner(remAddr, info); err != nil {
			return err
		}
		count, _ := w.OwnerCount()
		if err := w.setUint64(slotMSigOwnerCount, count-1); err != nil {
			return err
		}

	case MSigChangeThreshold:
		var newThreshold uint64
		for i := 0; i < 8; i++ {
			newThreshold = newThreshold<<8 | uint64(proposal.Data[i])
		}
		if err := w.setUint64(slotMSigThreshold, newThreshold); err != nil {
			return err
		}

	default:
		// validateAction refused every other type already.
		return fmt.Errorf("multisig: proposal type %d is not executable", proposal.TxType)
	}
	return nil
}

// Cancel allows the proposer to cancel their own proposal before execution.
func (w *MultiSigWallet) Cancel(caller qcrypto.Address, nonce uint64) error {
	proposal, err := w.GetProposal(nonce)
	if err != nil || proposal == nil {
		return fmt.Errorf("multisig: proposal %d not found", nonce)
	}
	if proposal.Executed {
		return fmt.Errorf("multisig: proposal %d already executed", nonce)
	}
	if proposal.Cancelled {
		return fmt.Errorf("multisig: proposal %d already cancelled", nonce)
	}
	// Only the proposer cancels. The previous rule let ANY owner cancel any
	// proposal, which made a single owner a veto over the whole wallet.
	if caller.String() != proposal.Proposer {
		return fmt.Errorf("multisig: only the proposer %s can cancel proposal %d", proposal.Proposer, nonce)
	}

	proposal.Cancelled = true
	return w.setProposal(nonce, proposal)
}

// ── Queries ────────────────────────────────────────────────────────────────────

// GetProposal returns a proposal by nonce.
func (w *MultiSigWallet) GetProposal(nonce uint64) (*MSigProposal, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, msigProposalSlot(nonce))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var p MSigProposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.Value == nil {
		p.Value = big.NewInt(0)
	}
	if p.Confirmations == nil {
		p.Confirmations = []string{}
	}
	return &p, nil
}

// GetOwner returns the owner record for the given address.
func (w *MultiSigWallet) GetOwner(owner qcrypto.Address) (*MSigOwner, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, msigOwnerSlot(owner))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var info MSigOwner
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Threshold returns the required confirmation count.
func (w *MultiSigWallet) Threshold() (uint64, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, slotMSigThreshold)
	if err != nil || len(b) == 0 {
		return 0, nil
	}
	return w.bytesToUint64(b), nil
}

// OwnerCount returns the number of registered owners.
func (w *MultiSigWallet) OwnerCount() (uint64, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, slotMSigOwnerCount)
	if err != nil || len(b) == 0 {
		return 0, nil
	}
	return w.bytesToUint64(b), nil
}

// CurrentNonce returns the current proposal nonce (total submitted).
func (w *MultiSigWallet) CurrentNonce() (uint64, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, slotMSigNonce)
	if err != nil || len(b) == 0 {
		return 0, nil
	}
	return w.bytesToUint64(b), nil
}

// IsOwner returns true if addr is an active owner.
func (w *MultiSigWallet) IsOwner(addr qcrypto.Address) (bool, error) {
	info, err := w.GetOwner(addr)
	return err == nil && info != nil && info.Active, err
}

// ConfirmationCount returns how many confirmations a proposal has.
func (w *MultiSigWallet) ConfirmationCount(nonce uint64) (int, error) {
	p, err := w.GetProposal(nonce)
	if err != nil || p == nil {
		return 0, err
	}
	return len(p.Confirmations), nil
}

// ConfirmationData returns the full ML-DSA-65 confirmation from a specific owner.
func (w *MultiSigWallet) ConfirmationData(nonce uint64, owner qcrypto.Address) (*MSigConfirmation, error) {
	b, err := w.stateDB.GetStorageValue(w.contract, msigConfirmSlot(nonce, owner))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var conf MSigConfirmation
	if err := json.Unmarshal(b, &conf); err != nil {
		return nil, err
	}
	return &conf, nil
}

// SPLD balance of this multisig wallet.
func (w *MultiSigWallet) Balance(stateDB *state.StateDB) (*big.Int, error) {
	return stateDB.GetBalance(w.contract)
}

// ── Access control ────────────────────────────────────────────────────────────

func (w *MultiSigWallet) requireOwner(caller qcrypto.Address) error {
	ok, err := w.IsOwner(caller)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("multisig: %s is not an active owner", caller)
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// EncodeAddOwnerData encodes owner addition data for a proposal.
func EncodeAddOwnerData(owner qcrypto.Address, pubKey []byte, name string) []byte {
	data := make([]byte, qcrypto.AddressSize+qcrypto.PublicKeySize)
	copy(data[:qcrypto.AddressSize], owner[:])
	copy(data[qcrypto.AddressSize:], pubKey)
	return append(data, []byte(name)...)
}

// EncodeRemoveOwnerData encodes owner removal data for a proposal.
func EncodeRemoveOwnerData(owner qcrypto.Address) []byte {
	data := make([]byte, qcrypto.AddressSize)
	copy(data, owner[:])
	return data
}

// EncodeChangeThresholdData encodes a threshold change for a proposal.
func EncodeChangeThresholdData(newThreshold uint64) []byte {
	buf := make([]byte, 8)
	for i := 0; i < 8; i++ {
		buf[7-i] = byte(newThreshold)
		newThreshold >>= 8
	}
	return buf
}

func (w *MultiSigWallet) nextNonce() (uint64, error) {
	current, err := w.CurrentNonce()
	if err != nil {
		return 0, err
	}
	next := current + 1
	w.setUint64(slotMSigNonce, next)
	return next, nil
}

func (w *MultiSigWallet) setOwner(owner qcrypto.Address, info *MSigOwner) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return w.stateDB.SetStorageValue(w.contract, msigOwnerSlot(owner), data)
}

func (w *MultiSigWallet) setProposal(nonce uint64, p *MSigProposal) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return w.stateDB.SetStorageValue(w.contract, msigProposalSlot(nonce), data)
}

func (w *MultiSigWallet) setConfirmation(nonce uint64, owner qcrypto.Address, conf *MSigConfirmation) error {
	data, err := json.Marshal(conf)
	if err != nil {
		return err
	}
	return w.stateDB.SetStorageValue(w.contract, msigConfirmSlot(nonce, owner), data)
}

func (w *MultiSigWallet) setUint64(slot [32]byte, v uint64) error {
	buf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		buf[i] = byte(v)
		v >>= 8
	}
	return w.stateDB.SetStorageValue(w.contract, slot, buf)
}

func (w *MultiSigWallet) bytesToUint64(b []byte) uint64 {
	var v uint64
	for _, byt := range b {
		v = v<<8 | uint64(byt)
	}
	return v
}

// compile-time check: types.Receipt used in events context
var _ = (*types.Receipt)(nil)
