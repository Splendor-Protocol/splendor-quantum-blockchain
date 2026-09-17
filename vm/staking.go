// Package vm — permissionless validator registry.
//
// One bond: 2,500 SPLD. No Bronze / Silver / Gold / Platinum.
// Register locks the bond; unregister returns it after UnbondingBlocks.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// ValidatorBondSPLD is the single flat bond (2,500 SPLD).
const ValidatorBondSPLD int64 = 2_500

// UnbondingBlocks is the delay before a bond is returned.
const UnbondingBlocks uint64 = 32

func spldUnit() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
}

// ValidatorBond is 2,500 SPLD in smallest units.
func ValidatorBond() *big.Int {
	return new(big.Int).Mul(big.NewInt(ValidatorBondSPLD), spldUnit())
}

// ValidatorStatus represents a validator's current state.
type ValidatorStatus uint8

const (
	ValidatorInactive ValidatorStatus = 0
	ValidatorActive   ValidatorStatus = 1
	ValidatorExiting  ValidatorStatus = 3
)

// ValidatorRecord is stored on-chain for each registered validator.
type ValidatorRecord struct {
	Address      qcrypto.Address `json:"address"`
	PublicKey    []byte          `json:"public_key"`
	Bond         *big.Int        `json:"bond"`
	Status       ValidatorStatus `json:"status"`
	JoinedHeight uint64          `json:"joined_height"`
	ExitHeight   uint64          `json:"exit_height,omitempty"`
}

var (
	slotStakingTotalStaked    = qcrypto.Hash32([]byte("staking:total_staked"))
	slotStakingValidatorCount = qcrypto.Hash32([]byte("staking:validator_count"))
	slotStakingIndex          = qcrypto.Hash32([]byte("staking:index"))
)

func stakingValidatorSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:validator:"), addr[:]...))
}

// stakingApprovalSlot holds a single byte, 1 while addr may register under
// validators_admission_v1. Absent or 0 means not approved.
func stakingApprovalSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:approved:"), addr[:]...))
}

// StakingContract is the validator registry (name kept so existing RPC compiles).
type StakingContract struct {
	stateDB       *state.StateDB
	contract      qcrypto.Address
	committedOnly bool
}

func NewStakingContract(stateDB *state.StateDB, contract qcrypto.Address) *StakingContract {
	return &StakingContract{stateDB: stateDB, contract: contract}
}

// CommittedView returns a registry reader that ignores the pending overlay.
// Consensus (RefreshSet, producer lookup, QC checks) must use this so a
// discarded proposal cannot change the effective validator set.
func (s *StakingContract) CommittedView() *StakingContract {
	out := *s
	out.committedOnly = true
	return &out
}

func (s *StakingContract) readSlot(slot [32]byte) ([]byte, error) {
	if s.committedOnly {
		return s.stateDB.GetCommittedStorageValue(s.contract, slot)
	}
	return s.stateDB.GetStorageValue(s.contract, slot)
}

func DeployStaking(stateDB *state.StateDB) qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor_staking_contract_v1"))
	var addr qcrypto.Address
	copy(addr[:], h[:])
	return addr
}

func ValidatorsModuleAddress() qcrypto.Address {
	return DeployStaking(nil)
}

// Register locks ValidatorBond() from the caller and adds them to the set.
//
// Legacy rule: only an Active record refuses a new registration. An Exiting
// record — bond still owed, unbonding in progress — was overwritten by a fresh
// Active record with a second bond, so the pool held two bonds against one
// record and the first was never returned. RegisterV2 is the corrected rule;
// the router chooses by fork.
func (s *StakingContract) Register(caller qcrypto.Address, pubKey []byte, currentHeight uint64) error {
	return s.register(caller, pubKey, currentHeight, false)
}

// RegisterV2 is Register under modules_v2: a caller may register only when it
// has no record or a record whose exit is finalized (Inactive, bond
// returned). Active is refused as before; Exiting is refused until
// finalize_exit has run. A registry read error is refused rather than read
// as "no record".
func (s *StakingContract) RegisterV2(caller qcrypto.Address, pubKey []byte, currentHeight uint64) error {
	return s.register(caller, pubKey, currentHeight, true)
}

func (s *StakingContract) register(caller qcrypto.Address, pubKey []byte, currentHeight uint64, strict bool) error {
	if len(pubKey) != qcrypto.PublicKeySize {
		return fmt.Errorf("validators: invalid public key size")
	}
	derived := qcrypto.DeriveAddress(mustPub(pubKey))
	if derived != caller {
		return fmt.Errorf("validators: pubkey does not match caller")
	}
	existing, err := s.GetValidator(caller)
	if strict && err != nil {
		return fmt.Errorf("validators: read record for %s: %w", caller, err)
	}
	if existing != nil && existing.Status == ValidatorActive {
		return fmt.Errorf("validators: %s already registered", caller)
	}
	if strict && existing != nil && existing.Status == ValidatorExiting {
		return fmt.Errorf("validators: %s is exiting (bond unbonding until height %d); finalize_exit before registering again",
			caller, existing.ExitHeight)
	}
	bond := ValidatorBond()
	acc, err := s.stateDB.GetAccount(caller)
	if err != nil {
		return err
	}
	if acc.Balance.Cmp(bond) < 0 {
		return fmt.Errorf("validators: need %s units bond, have %s", bond, acc.Balance)
	}
	acc.Balance.Sub(acc.Balance, bond)
	s.stateDB.SetAccount(acc)

	pool, err := s.stateDB.GetAccount(s.contract)
	if err != nil {
		return err
	}
	if pool.Balance == nil {
		pool.Balance = big.NewInt(0)
	}
	pool.Balance.Add(pool.Balance, bond)
	s.stateDB.SetAccount(pool)

	rec := &ValidatorRecord{
		Address:      caller,
		PublicKey:    append([]byte(nil), pubKey...),
		Bond:         new(big.Int).Set(bond),
		Status:       ValidatorActive,
		JoinedHeight: currentHeight,
	}
	if err := s.setValidator(rec); err != nil {
		return err
	}
	if err := s.addIndex(caller); err != nil {
		return err
	}
	total, _ := s.TotalStaked()
	s.setBigInt(slotStakingTotalStaked, new(big.Int).Add(total, bond))
	count, _ := s.ValidatorCount()
	s.setBigInt(slotStakingValidatorCount, big.NewInt(int64(count+1)))
	return nil
}

// CheckAdmission is the validators_admission_v1 rule for register: the caller
// must be the producer of the block the call executes in, or an address an
// active validator has approved. The producer clause is how a chain's genesis
// validator enrols itself without anyone to approve it — only a real
// producer seals a canonical block, and every node reads the same producer
// from the header — and it needs no node-local knowledge of who the genesis
// validator is, which a consensus rule must never depend on.
func (s *StakingContract) CheckAdmission(caller, producer qcrypto.Address) error {
	if caller == producer {
		return nil
	}
	ok, err := s.IsApproved(caller)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("validators: %s is not approved to register (an active validator must approve it first)", caller)
	}
	return nil
}

// Approve marks addr as allowed to register. The approver must be an active
// validator; approval stands until Revoke, so an approved validator that exits
// may re-register without a fresh approval.
func (s *StakingContract) Approve(approver, addr qcrypto.Address) error {
	if err := s.requireActive(approver, "approve"); err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, stakingApprovalSlot(addr), []byte{1})
}

// Revoke withdraws an approval. It does not touch an existing registration;
// an already-registered validator leaves through unregister.
func (s *StakingContract) Revoke(approver, addr qcrypto.Address) error {
	if err := s.requireActive(approver, "revoke"); err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, stakingApprovalSlot(addr), []byte{0})
}

// IsApproved reports whether addr holds a standing approval.
func (s *StakingContract) IsApproved(addr qcrypto.Address) (bool, error) {
	v, err := s.stateDB.GetStorageValue(s.contract, stakingApprovalSlot(addr))
	if err != nil {
		return false, err
	}
	return len(v) == 1 && v[0] == 1, nil
}

func (s *StakingContract) requireActive(who qcrypto.Address, what string) error {
	active, err := s.IsActive(who)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("validators %s: %s is not an active validator", what, who)
	}
	return nil
}

// Unregister starts the unbonding delay. Bond returns at FinalizeExit.
func (s *StakingContract) Unregister(caller qcrypto.Address, currentHeight uint64) error {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil || rec.Status != ValidatorActive {
		return fmt.Errorf("validators: %s is not active", caller)
	}
	rec.Status = ValidatorExiting
	rec.ExitHeight = currentHeight + UnbondingBlocks
	if err := s.setValidator(rec); err != nil {
		return err
	}
	count, _ := s.ValidatorCount()
	if count > 0 {
		s.setBigInt(slotStakingValidatorCount, big.NewInt(int64(count-1)))
	}
	return nil
}

// FinalizeExit returns the bond after UnbondingBlocks.
func (s *StakingContract) FinalizeExit(caller qcrypto.Address, currentHeight uint64) error {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil || rec.Status != ValidatorExiting {
		return fmt.Errorf("validators: %s is not exiting", caller)
	}
	if currentHeight < rec.ExitHeight {
		return fmt.Errorf("validators: unbonding until height %d", rec.ExitHeight)
	}
	bond := rec.Bond
	if bond == nil {
		bond = ValidatorBond()
	}
	pool, err := s.stateDB.GetAccount(s.contract)
	if err != nil {
		return err
	}
	if pool.Balance.Cmp(bond) < 0 {
		return fmt.Errorf("validators: bond pool underfunded")
	}
	pool.Balance.Sub(pool.Balance, bond)
	s.stateDB.SetAccount(pool)
	acc, err := s.stateDB.GetAccount(caller)
	if err != nil {
		return err
	}
	acc.Balance.Add(acc.Balance, bond)
	s.stateDB.SetAccount(acc)

	rec.Status = ValidatorInactive
	rec.Bond = big.NewInt(0)
	if err := s.setValidator(rec); err != nil {
		return err
	}
	total, _ := s.TotalStaked()
	s.setBigInt(slotStakingTotalStaked, new(big.Int).Sub(total, bond))
	return s.removeIndex(caller)
}

func (s *StakingContract) GetValidator(addr qcrypto.Address) (*ValidatorRecord, error) {
	b, err := s.readSlot(stakingValidatorSlot(addr))
	if err != nil {
		return nil, fmt.Errorf("validators: read %s: %w", addr, err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var rec ValidatorRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("validators: decode %s: %w", addr, err)
	}
	if rec.Bond == nil {
		rec.Bond = big.NewInt(0)
	}
	return &rec, nil
}

func (s *StakingContract) IsActive(addr qcrypto.Address) (bool, error) {
	rec, err := s.GetValidator(addr)
	if err != nil || rec == nil {
		return false, err
	}
	return rec.Status == ValidatorActive, nil
}

func (s *StakingContract) TotalStaked() (*big.Int, error) {
	return s.getBigInt(slotStakingTotalStaked)
}

func (s *StakingContract) ValidatorCount() (uint64, error) {
	v, err := s.getBigInt(slotStakingValidatorCount)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// ListActive returns every active validator (for BFT).
//
// An empty index is "no validators", not an error. An unreadable index or
// record is an error so callers fail closed instead of shrinking the set.
func (s *StakingContract) ListActive() ([]ValidatorRecord, error) {
	raw, err := s.readSlot(slotStakingIndex)
	if err != nil {
		return nil, fmt.Errorf("validators: read index: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var addrs []qcrypto.Address
	if err := json.Unmarshal(raw, &addrs); err != nil {
		return nil, fmt.Errorf("validators: decode index: %w", err)
	}
	var out []ValidatorRecord
	for _, a := range addrs {
		rec, err := s.GetValidator(a)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, fmt.Errorf("validators: index lists %s but the record is missing", a)
		}
		if rec.Status != ValidatorActive {
			continue
		}
		out = append(out, *rec)
	}
	return out, nil
}

func (s *StakingContract) addIndex(addr qcrypto.Address) error {
	raw, _ := s.stateDB.GetStorageValue(s.contract, slotStakingIndex)
	var addrs []qcrypto.Address
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &addrs)
	}
	for _, a := range addrs {
		if a == addr {
			return nil
		}
	}
	addrs = append(addrs, addr)
	b, err := json.Marshal(addrs)
	if err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, slotStakingIndex, b)
}

func (s *StakingContract) removeIndex(addr qcrypto.Address) error {
	raw, _ := s.stateDB.GetStorageValue(s.contract, slotStakingIndex)
	var addrs []qcrypto.Address
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &addrs)
	}
	next := addrs[:0]
	for _, a := range addrs {
		if a != addr {
			next = append(next, a)
		}
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, slotStakingIndex, b)
}

func (s *StakingContract) setValidator(rec *ValidatorRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, stakingValidatorSlot(rec.Address), data)
}

func (s *StakingContract) setBigInt(slot [32]byte, v *big.Int) error {
	return s.stateDB.SetStorageValue(s.contract, slot, v.Bytes())
}

func (s *StakingContract) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := s.stateDB.GetStorageValue(s.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func mustPub(b []byte) *qcrypto.PublicKey {
	pk, err := qcrypto.PublicKeyFromBytes(b)
	if err != nil {
		return &qcrypto.PublicKey{}
	}
	return pk
}
