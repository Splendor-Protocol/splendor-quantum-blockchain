// Package vm — On-chain Staking for Splendor Quantum Chain.
//
// The Staking contract manages validator registration and SPLD staking.
// It is the source of truth for:
//   - Who is a registered validator
//   - How much SPLD each validator has staked
//   - Which validators are active vs. jailed
//   - Reward accumulation and claiming
//
// All operations use SHA3-256 storage slots and SQ: addresses.
// No ECDSA. No keccak. No Ethereum address format.
//
// Staking tiers:
//   Bronze  ≥ 3,947 SPLD    (3947 * 10^18 units)
//   Silver  ≥ 39,474 SPLD
//   Gold    ≥ 394,737 SPLD
//   Platinum ≥ 3,947,368 SPLD
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// Staking tiers (in smallest SPLD units — multiply by 10^18 for SPLD)
var (
	StakeBronze   = new(big.Int).Mul(big.NewInt(3_947), spldUnit())
	StakeSilver   = new(big.Int).Mul(big.NewInt(39_474), spldUnit())
	StakeGold     = new(big.Int).Mul(big.NewInt(394_737), spldUnit())
	StakePlatinum = new(big.Int).Mul(big.NewInt(3_947_368), spldUnit())
)

func spldUnit() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
}

// ValidatorStatus represents a validator's current state.
type ValidatorStatus uint8

const (
	ValidatorInactive ValidatorStatus = 0 // not registered or removed
	ValidatorActive   ValidatorStatus = 1 // participating in consensus
	ValidatorJailed   ValidatorStatus = 2 // slashed, temporarily excluded
	ValidatorExiting  ValidatorStatus = 3 // requested exit, waiting unbonding
)

// StakingTier represents a validator's tier based on stake.
type StakingTier uint8

const (
	TierNone     StakingTier = 0
	TierBronze   StakingTier = 1
	TierSilver   StakingTier = 2
	TierGold     StakingTier = 3
	TierPlatinum StakingTier = 4
)

// ValidatorRecord is stored on-chain for each registered validator.
type ValidatorRecord struct {
	Address      qcrypto.Address `json:"address"`
	PublicKey    []byte          `json:"public_key"`
	Stake        *big.Int        `json:"stake"`
	Status       ValidatorStatus `json:"status"`
	Tier         StakingTier     `json:"tier"`
	Rewards      *big.Int        `json:"rewards"`
	JoinedHeight uint64          `json:"joined_height"`
	LastSlashed  uint64          `json:"last_slashed"`
	UptimeScore  uint8           `json:"uptime_score"` // 0-100
}

// ── Storage slot derivation ────────────────────────────────────────────────────

var slotStakingTotalStaked = qcrypto.Hash32([]byte("staking:total_staked"))
var slotStakingValidatorCount = qcrypto.Hash32([]byte("staking:validator_count"))

func stakingValidatorSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:validator:"), addr[:]...))
}

func stakingDelegatorSlot(delegator, validator qcrypto.Address) [32]byte {
	key := append([]byte("staking:delegation:"), delegator[:]...)
	key = append(key, validator[:]...)
	return qcrypto.Hash32(key)
}

// ── StakingContract ────────────────────────────────────────────────────────────

// StakingContract manages validator staking and rewards.
type StakingContract struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewStakingContract returns a handle to the staking contract.
func NewStakingContract(stateDB *state.StateDB, contract qcrypto.Address) *StakingContract {
	return &StakingContract{stateDB: stateDB, contract: contract}
}

// DeployStaking deploys the staking contract at a deterministic address.
func DeployStaking(stateDB *state.StateDB) qcrypto.Address {
	// Staking contract lives at a well-known address: SHA3-256("staking_contract")
	h := qcrypto.Hash32([]byte("splendor_staking_contract_v1"))
	var addr qcrypto.Address
	copy(addr[:], h[:])
	return addr
}

// ── Validator management ──────────────────────────────────────────────────────

// Register registers a new validator with the given ML-DSA-65 public key and stake.
// The staker must have already transferred the stake SPLD to the contract.
func (s *StakingContract) Register(
	caller qcrypto.Address,
	pubKey []byte,
	stakeAmount *big.Int,
	currentHeight uint64,
) error {
	if len(pubKey) != qcrypto.PublicKeySize {
		return fmt.Errorf("staking: invalid public key size")
	}
	if stakeAmount.Cmp(StakeBronze) < 0 {
		return fmt.Errorf("staking: stake below minimum Bronze tier (%s SPLD units)", StakeBronze)
	}

	// Check not already registered
	existing, _ := s.GetValidator(caller)
	if existing != nil && existing.Status != ValidatorInactive {
		return fmt.Errorf("staking: validator %s already registered", caller)
	}

	rec := &ValidatorRecord{
		Address:      caller,
		PublicKey:    pubKey,
		Stake:        new(big.Int).Set(stakeAmount),
		Status:       ValidatorActive,
		Tier:         computeTier(stakeAmount),
		Rewards:      big.NewInt(0),
		JoinedHeight: currentHeight,
		UptimeScore:  100,
	}

	if err := s.setValidator(rec); err != nil {
		return err
	}

	// Update total staked
	total, _ := s.TotalStaked()
	s.setBigInt(slotStakingTotalStaked, new(big.Int).Add(total, stakeAmount))

	// Increment count
	count, _ := s.ValidatorCount()
	s.setBigInt(slotStakingValidatorCount, big.NewInt(int64(count+1)))

	return nil
}

// AddStake adds more stake to an existing validator.
func (s *StakingContract) AddStake(caller qcrypto.Address, amount *big.Int) error {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil {
		return fmt.Errorf("staking: validator %s not found", caller)
	}
	if rec.Status == ValidatorJailed {
		return fmt.Errorf("staking: cannot add stake to jailed validator")
	}

	rec.Stake.Add(rec.Stake, amount)
	rec.Tier = computeTier(rec.Stake)

	total, _ := s.TotalStaked()
	s.setBigInt(slotStakingTotalStaked, new(big.Int).Add(total, amount))

	return s.setValidator(rec)
}

// Unstake initiates withdrawal. Moves validator to Exiting status.
// Actual withdrawal is processed after unbonding period.
func (s *StakingContract) Unstake(caller qcrypto.Address, amount *big.Int) error {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil {
		return fmt.Errorf("staking: validator %s not found", caller)
	}
	if rec.Stake.Cmp(amount) < 0 {
		return fmt.Errorf("staking: insufficient stake to unstake")
	}

	rec.Stake.Sub(rec.Stake, amount)
	if rec.Stake.Cmp(StakeBronze) < 0 {
		rec.Status = ValidatorExiting
	}
	rec.Tier = computeTier(rec.Stake)

	total, _ := s.TotalStaked()
	s.setBigInt(slotStakingTotalStaked, new(big.Int).Sub(total, amount))

	return s.setValidator(rec)
}

// Slash penalises a validator by reducing their stake by `slashAmount`.
// Sets status to Jailed if stake drops below minimum.
func (s *StakingContract) Slash(validator qcrypto.Address, slashAmount *big.Int, currentHeight uint64) error {
	rec, err := s.GetValidator(validator)
	if err != nil || rec == nil {
		return fmt.Errorf("staking: validator %s not found", validator)
	}

	if rec.Stake.Cmp(slashAmount) < 0 {
		slashAmount.Set(rec.Stake) // cap at current stake
	}
	rec.Stake.Sub(rec.Stake, slashAmount)
	rec.LastSlashed = currentHeight

	if rec.Stake.Cmp(StakeBronze) < 0 {
		rec.Status = ValidatorJailed
	}
	rec.Tier = computeTier(rec.Stake)

	total, _ := s.TotalStaked()
	newTotal := new(big.Int).Sub(total, slashAmount)
	if newTotal.Sign() < 0 {
		newTotal = big.NewInt(0)
	}
	s.setBigInt(slotStakingTotalStaked, newTotal)

	return s.setValidator(rec)
}

// Unjail removes a validator from jail if they meet the minimum stake again.
func (s *StakingContract) Unjail(caller qcrypto.Address, additionalStake *big.Int) error {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil {
		return fmt.Errorf("staking: validator %s not found", caller)
	}
	if rec.Status != ValidatorJailed {
		return fmt.Errorf("staking: validator is not jailed")
	}

	if additionalStake.Sign() > 0 {
		rec.Stake.Add(rec.Stake, additionalStake)
	}
	if rec.Stake.Cmp(StakeBronze) < 0 {
		return fmt.Errorf("staking: stake still below minimum after unjail")
	}

	rec.Status = ValidatorActive
	rec.Tier = computeTier(rec.Stake)
	return s.setValidator(rec)
}

// AddRewards credits pending rewards to a validator.
func (s *StakingContract) AddRewards(validator qcrypto.Address, amount *big.Int) error {
	rec, err := s.GetValidator(validator)
	if err != nil || rec == nil {
		return fmt.Errorf("staking: validator %s not found", validator)
	}
	rec.Rewards.Add(rec.Rewards, amount)
	return s.setValidator(rec)
}

// ClaimRewards withdraws all pending rewards for a validator.
// Returns the claimed amount.
func (s *StakingContract) ClaimRewards(caller qcrypto.Address) (*big.Int, error) {
	rec, err := s.GetValidator(caller)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("staking: validator %s not found", caller)
	}
	if rec.Rewards.Sign() == 0 {
		return big.NewInt(0), nil
	}
	claimed := new(big.Int).Set(rec.Rewards)
	rec.Rewards = big.NewInt(0)
	if err := s.setValidator(rec); err != nil {
		return nil, err
	}
	return claimed, nil
}

// UpdateUptime updates a validator's uptime score (0–100).
func (s *StakingContract) UpdateUptime(validator qcrypto.Address, score uint8) error {
	rec, err := s.GetValidator(validator)
	if err != nil || rec == nil {
		return nil // silently ignore unknown validators
	}
	rec.UptimeScore = score
	return s.setValidator(rec)
}

// ── Queries ────────────────────────────────────────────────────────────────────

// GetValidator returns the validator record for the given address.
// Returns nil (no error) if not found.
func (s *StakingContract) GetValidator(addr qcrypto.Address) (*ValidatorRecord, error) {
	b, err := s.stateDB.GetStorageValue(s.contract, stakingValidatorSlot(addr))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var rec ValidatorRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("staking: failed to decode validator: %w", err)
	}
	if rec.Stake == nil {
		rec.Stake = big.NewInt(0)
	}
	if rec.Rewards == nil {
		rec.Rewards = big.NewInt(0)
	}
	return &rec, nil
}

// IsActive returns true if the validator is currently active.
func (s *StakingContract) IsActive(addr qcrypto.Address) (bool, error) {
	rec, err := s.GetValidator(addr)
	if err != nil || rec == nil {
		return false, err
	}
	return rec.Status == ValidatorActive, nil
}

// TotalStaked returns the total SPLD staked across all validators.
func (s *StakingContract) TotalStaked() (*big.Int, error) {
	return s.getBigInt(slotStakingTotalStaked)
}

// ValidatorCount returns the number of registered validators.
func (s *StakingContract) ValidatorCount() (uint64, error) {
	v, err := s.getBigInt(slotStakingValidatorCount)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// GetTier returns the staking tier name for a given stake amount.
func GetTierName(stake *big.Int) string {
	switch computeTier(stake) {
	case TierPlatinum:
		return "Platinum"
	case TierGold:
		return "Gold"
	case TierSilver:
		return "Silver"
	case TierBronze:
		return "Bronze"
	default:
		return "None"
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func computeTier(stake *big.Int) StakingTier {
	switch {
	case stake.Cmp(StakePlatinum) >= 0:
		return TierPlatinum
	case stake.Cmp(StakeGold) >= 0:
		return TierGold
	case stake.Cmp(StakeSilver) >= 0:
		return TierSilver
	case stake.Cmp(StakeBronze) >= 0:
		return TierBronze
	default:
		return TierNone
	}
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
