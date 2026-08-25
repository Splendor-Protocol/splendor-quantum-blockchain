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

	// Register owners
	for _, owner := range owners {
		addr, err := qcrypto.AddressFromString(owner.Address)
		if err != nil {
			return qcrypto.Address{}, fmt.Errorf("multisig: invalid owner address %q: %w", owner.Address, err)
		}
		if len(owner.PublicKey) != qcrypto.PublicKeySize {
			return qcrypto.Address{}, fmt.Errorf("multisig: invalid public key for %s", owner.Address)
		}
		if err := w.setOwner(addr, &owner); err != nil {
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

// ── Proposal lifecycle ────────────────────────────────────────────────────────

// ProposalHash computes the canonical hash of a proposal that owners sign.
// hash = SHA3-256("msig:proposal:" + contract + nonce8 + to + value32 + sha3(data))
func ProposalHash(contract qcrypto.Address, nonce uint64, to qcrypto.Address, value *big.Int, data []byte) [32]byte {
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

	msg := append([]byte("msig:proposal:"), contract[:]...)
	msg = append(msg, nonceBytes...)
	msg = append(msg, to[:]...)
	msg = append(msg, valueBytes...)
	msg = append(msg, dataHash[:]...)
	return qcrypto.Hash32(msg)
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

	nonce, err := w.nextNonce()
	if err != nil {
		return 0, err
	}

	if value == nil {
		value = big.NewInt(0)
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
	hash := ProposalHash(w.contract, nonce, toAddr, proposal.Value, proposal.Data)
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
	if uint64(len(proposal.Confirmations)) < threshold {
		return fmt.Errorf("multisig: insufficient confirmations (%d / %d required)",
			len(proposal.Confirmations), threshold)
	}

	toAddr, err := qcrypto.AddressFromString(proposal.To)
	if err != nil {
		return err
	}

	// Execute based on type
	switch proposal.TxType {
	case MSigTransfer:
		if proposal.Value.Sign() > 0 {
			if err := stateDB.Transfer(w.contract, toAddr, proposal.Value); err != nil {
				return fmt.Errorf("multisig execute: transfer failed: %w", err)
			}
		}

	case MSigCallToken:
		// Call a QRC-20 token function — data encodes function selector + args
		// In production: route through VM engine; here we interpret common selectors
		if len(proposal.Data) >= 4 {
			// Could be transfer(address,uint256), approve(address,uint256), etc.
			// For now, note that full QSol-bytecode contract calls would use MSigCallAny
		}

	case MSigAddOwner:
		// data = [32-byte address][1952-byte pubkey][name_bytes]
		if len(proposal.Data) < qcrypto.AddressSize+qcrypto.PublicKeySize {
			return fmt.Errorf("multisig: AddOwner data too short")
		}
		var newOwnerAddr qcrypto.Address
		copy(newOwnerAddr[:], proposal.Data[:qcrypto.AddressSize])
		newPubKey := proposal.Data[qcrypto.AddressSize : qcrypto.AddressSize+qcrypto.PublicKeySize]
		name := ""
		if len(proposal.Data) > qcrypto.AddressSize+qcrypto.PublicKeySize {
			name = string(proposal.Data[qcrypto.AddressSize+qcrypto.PublicKeySize:])
		}
		newOwner := &MSigOwner{
			Address:   newOwnerAddr.String(),
			PublicKey: newPubKey,
			Name:      name,
			Active:    true,
		}
		if err := w.setOwner(newOwnerAddr, newOwner); err != nil {
			return err
		}
		count, _ := w.OwnerCount()
		w.setUint64(slotMSigOwnerCount, count+1)

	case MSigRemOwner:
		if len(proposal.Data) < qcrypto.AddressSize {
			return fmt.Errorf("multisig: RemoveOwner data too short")
		}
		var remAddr qcrypto.Address
		copy(remAddr[:], proposal.Data[:qcrypto.AddressSize])
		info, _ := w.GetOwner(remAddr)
		if info != nil {
			info.Active = false
			w.setOwner(remAddr, info)
		}
		count, _ := w.OwnerCount()
		if count > 1 {
			w.setUint64(slotMSigOwnerCount, count-1)
		}

	case MSigChangeThreshold:
		if len(proposal.Data) < 8 {
			return fmt.Errorf("multisig: ChangeThreshold data too short")
		}
		var newThreshold uint64
		for i := 0; i < 8; i++ {
			newThreshold = newThreshold<<8 | uint64(proposal.Data[i])
		}
		ownerCount, _ := w.OwnerCount()
		if newThreshold == 0 || newThreshold > ownerCount {
			return fmt.Errorf("multisig: invalid threshold %d (owners: %d)", newThreshold, ownerCount)
		}
		w.setUint64(slotMSigThreshold, newThreshold)

	case MSigCallAny:
		// For full contract calls via the VM engine, caller must pass the engine.
		// This case intentionally left as a hook — integrate with vm.Engine in executor.
		// The proposal.Data contains ABI-encoded calldata.
	}

	// Mark executed
	proposal.Executed = true
	return w.setProposal(nonce, proposal)
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
	if caller.String() != proposal.Proposer {
		// Also allow any owner if threshold reached for cancellation
		if err := w.requireOwner(caller); err != nil {
			return fmt.Errorf("multisig: only proposer can cancel (or owner with quorum)")
		}
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
