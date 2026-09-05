// Package vm — validator staking and the admission pass registry
// (validator_gates_v1).
//
// The chain admits a block producer through two independent gates, and a key
// must pass BOTH before it may seal a block:
//
//   - ADMISSION. An entry in the pass registry, held against the producer's
//     ML-DSA-65 consensus public key. Granting and revoking it is an admin
//     action (see Admin); the genesis validator is passed implicitly, because
//     it is the key the whole chain already trusts to sign block 0.
//   - BOND. A registered validator record whose stake is at or above
//     MinValidatorStake and whose status is Active. The stake is real: it
//     moves out of the staker's balance into this module's escrow account and
//     comes back only through unstake + withdraw, after the unbonding delay.
//
// Two gates rather than one because they fail differently. An admission pass
// says a key is who the network agreed to let in; a bond says the key has
// something to lose if it misbehaves. Either alone is a chain anyone with a
// wallet — or anyone on a list — can produce blocks on.
//
// Registering and bonding is OPEN: anyone may sync the chain, register a key
// and stake behind it, and everything here is reachable through the module
// router by ordinary signed transactions. What is not open is producing —
// that is what the pass adds. This is the dual-gate shape classic Splendor's
// Proposal.sol + Validators.sol has, ported to this chain's storage model
// rather than to its Solidity.
//
// Everything is keyed by the CONSENSUS KEY. On this chain an address is
// SHA3-256 of the ML-DSA-65 public key (qcrypto.DeriveAddress), so the key
// and its address are one value, not two — there is no separate account
// identifier a pass could be keyed by instead. Each entry still carries the
// full 1952-byte key, so the binding between the pass and the key that will
// sign blocks is auditable rather than assumed, and every grant/revoke has to
// name the key itself.
//
// Staking tiers (Bronze is the floor a producer must clear):
//
//	Bronze   ≥ 3,947 SPLD
//	Silver   ≥ 39,474 SPLD
//	Gold     ≥ 394,737 SPLD
//	Platinum ≥ 3,947,368 SPLD
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

// Staking tiers (in smallest SPLD units — multiply by 10^18 for SPLD).
//
// Treat these as constants: they are package-level *big.Int, so a caller that
// mutated one would move the floor for the whole process. Every arithmetic
// path below copies before it writes.
var (
	StakeBronze   = new(big.Int).Mul(big.NewInt(3_947), spldUnit())
	StakeSilver   = new(big.Int).Mul(big.NewInt(39_474), spldUnit())
	StakeGold     = new(big.Int).Mul(big.NewInt(394_737), spldUnit())
	StakePlatinum = new(big.Int).Mul(big.NewInt(3_947_368), spldUnit())
)

// MinValidatorStake is the bond a passed key must hold to produce a block: the
// Bronze floor, the same number classic Splendor uses to admit a validator.
// Naming it separately is deliberate — the tiers rank validators, this one
// decides eligibility, and a later change to the ranking must not silently
// move the gate.
var MinValidatorStake = StakeBronze

func spldUnit() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
}

const (
	// UnbondingBlocks is how long unstaked SPLD stays in escrow before
	// withdraw can pay it out: ~7 days at the 2-second block interval.
	//
	// A bond that can be pulled in the same block it was counted is not a
	// bond — it is a deposit that happens to be visible when someone looks.
	// The delay is what makes slashing possible at all: misbehaviour is
	// detected after the block that contained it, so the stake has to still
	// be here then.
	UnbondingBlocks uint64 = 302_400

	// MaxValidatorPasses caps how many distinct keys the registry may ever
	// hold an entry for (granted or revoked), so the validator_set walk is
	// bounded. Phase 1 runs one producer; this leaves room for the set
	// multi_validator will need without letting the list grow without limit.
	MaxValidatorPasses = 64
)

// ValidatorStatus represents a validator's current state.
type ValidatorStatus uint8

const (
	ValidatorInactive ValidatorStatus = 0 // not registered or removed
	ValidatorActive   ValidatorStatus = 1 // bonded at or above the minimum
	ValidatorJailed   ValidatorStatus = 2 // slashed, excluded until unjailed
	ValidatorExiting  ValidatorStatus = 3 // bond fell below the minimum
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
//
// Stake is the live bond; Unbonding is what unstake moved out of it and
// withdraw will pay out once UnbondingHeight is reached. Both are backed by
// SPLD sitting in the module's escrow account, so
// balance(StakingAddress) >= Σ(Stake) + Σ(Unbonding) always holds.
type ValidatorRecord struct {
	Address         qcrypto.Address `json:"address"`
	PublicKey       []byte          `json:"public_key"`
	Stake           *big.Int        `json:"stake"`
	Unbonding       *big.Int        `json:"unbonding"`
	UnbondingHeight uint64          `json:"unbonding_height"`
	Status          ValidatorStatus `json:"status"`
	Tier            StakingTier     `json:"tier"`
	Rewards         *big.Int        `json:"rewards"`
	JoinedHeight    uint64          `json:"joined_height"`
	LastSlashed     uint64          `json:"last_slashed"`
	UptimeScore     uint8           `json:"uptime_score"` // 0-100
}

// PassEntry is one key's admission decision, keyed by the key itself.
//
// The full public key is stored, not just its address: a pass is a statement
// about the key that will sign blocks, and an operator auditing the registry
// must be able to compare it against the key their validator actually runs
// without trusting that a 32-byte digest was derived from the right thing.
type PassEntry struct {
	PublicKey []byte          `json:"public_key"`
	Address   qcrypto.Address `json:"address"`
	Passed    bool            `json:"passed"`

	// BondExempt lets a passed key produce without meeting
	// MinValidatorStake. It exists for exactly one situation: the genesis
	// validator, which has to produce block 1 before anything can be staked
	// — nothing can bond on a chain with no blocks. Any other use is an
	// admin choosing to run an unbonded producer, and the flag is on the
	// entry (rather than implied) so that choice is visible to everyone
	// reading the registry.
	BondExempt bool `json:"bond_exempt"`

	// Height is the block the entry was last written at. Zero on the
	// implicit genesis entry, which was never written.
	Height uint64 `json:"height"`

	// Genesis marks the implicit entry the genesis validator gets. It is
	// never stored — GetPass sets it on the value it synthesises — so an
	// explicit entry for the same key always overrides it, revocation
	// included.
	Genesis bool `json:"genesis,omitempty"`
}

// ── Storage slot derivation ────────────────────────────────────────────────────

var (
	slotStakingTotalStaked    = qcrypto.Hash32([]byte("staking:total_staked"))
	slotStakingValidatorCount = qcrypto.Hash32([]byte("staking:validator_count"))
	slotStakingSlashedTotal   = qcrypto.Hash32([]byte("staking:slashed_total"))
	slotStakingAdmin          = qcrypto.Hash32([]byte("staking:admin"))
	slotStakingPassCount      = qcrypto.Hash32([]byte("staking:pass_count"))
)

func stakingValidatorSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:validator:"), addr[:]...))
}

func stakingPassSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:pass:"), addr[:]...))
}

func stakingPassIndexSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:pass_index:"), addr[:]...))
}

func stakingPassListSlot(i uint64) [32]byte {
	return qcrypto.Hash32(append([]byte("staking:pass_list:"), u64be(i)...))
}

// StakingAddress is the singleton registry, and the escrow account every bond
// is held in: SHA3-256 of a fixed domain string, reproducible offline by any
// client, and unreachable by any ML-DSA key (no key derives to it), so nothing
// but this module can ever move what it holds.
//
// A singleton rather than a per-deployer instance, unlike the token and
// exchange modules: a chain has exactly one validator set, and a second
// staking contract would be a second answer to the only question consensus
// asks here.
var StakingAddress = deriveStakingAddress()

func deriveStakingAddress() qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor_staking_contract_v1"))
	var addr qcrypto.Address
	copy(addr[:], h[:])
	return addr
}

// ── StakingContract ────────────────────────────────────────────────────────────

// StakingContract manages validator staking, admission and eligibility.
type StakingContract struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewStakingContract returns a handle to the staking registry at addr. Pass
// StakingAddress for the chain's real one; a test may use any address.
func NewStakingContract(stateDB *state.StateDB, contract qcrypto.Address) *StakingContract {
	return &StakingContract{stateDB: stateDB, contract: contract}
}

// Staking returns a handle to the chain's singleton registry.
func Staking(stateDB *state.StateDB) *StakingContract {
	return NewStakingContract(stateDB, StakingAddress)
}

// Address is the registry (and escrow) address.
func (s *StakingContract) Address() qcrypto.Address { return s.contract }

// ── Admission ─────────────────────────────────────────────────────────────────

// ErrNotPassed is the sentinel behind an admission refusal, so a caller can
// tell "this key was never admitted" from "this key is not bonded".
var ErrNotPassed = errors.New("validator has no admission pass")

// ErrNotBonded is the sentinel behind a bond refusal.
var ErrNotBonded = errors.New("validator is not bonded")

// Admin is the authority that may grant and revoke passes, slash a bond, and
// hand the authority on.
//
// Until SetAdmin is called it is the GENESIS VALIDATOR's address: that key
// already signs every block the chain accepts, so making it the initial
// admission authority adds no trust the chain did not already place. The
// argument is the genesis validator address the executor supplies (zero when
// this node does not know it — see CanProduce).
func (s *StakingContract) Admin(genesisValidator qcrypto.Address) (qcrypto.Address, error) {
	stored, err := s.getAddrSlot(slotStakingAdmin)
	if err != nil {
		return qcrypto.Address{}, err
	}
	if !stored.IsZero() {
		return stored, nil
	}
	return genesisValidator, nil
}

// Initialised reports whether the registry has been written to at all: an
// admin has been set, or at least one pass has been granted or revoked.
//
// A chain that adopts validator_gates_v1 by extending its fork schedule
// reaches the activation height with an untouched registry. The executor reads
// this to decide whether the gates have anything to say yet, so adopting the
// upgrade can never halt a running chain that has not configured it.
func (s *StakingContract) Initialised() (bool, error) {
	admin, err := s.getAddrSlot(slotStakingAdmin)
	if err != nil {
		return false, err
	}
	if !admin.IsZero() {
		return true, nil
	}
	n, err := s.PassCount()
	return n > 0, err
}

// GetPass returns the admission entry for a validator address.
//
// An explicit entry always wins. Absent one, the genesis validator gets the
// implicit entry described on PassEntry.Genesis — passed and bond-exempt, so
// the chain that has always run on one key keeps running on it. Every other
// address returns nil.
func (s *StakingContract) GetPass(genesisValidator, addr qcrypto.Address) (*PassEntry, error) {
	b, err := s.stateDB.GetStorageValue(s.contract, stakingPassSlot(addr))
	if err != nil {
		return nil, fmt.Errorf("staking: read pass for %s: %w", addr, err)
	}
	if len(b) > 0 {
		var e PassEntry
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, fmt.Errorf("staking: pass entry for %s is corrupt: %w", addr, err)
		}
		return &e, nil
	}
	if !genesisValidator.IsZero() && addr == genesisValidator {
		return &PassEntry{Address: addr, Passed: true, BondExempt: true, Genesis: true}, nil
	}
	return nil, nil
}

// SetPass grants or revokes admission for one consensus key. Admin only.
//
// It takes the full ML-DSA-65 public key rather than an address: a pass is a
// statement about the key that signs blocks, and requiring it here means the
// admin cannot admit a 32-byte value they have not seen the key behind.
func (s *StakingContract) SetPass(
	caller, genesisValidator qcrypto.Address,
	pubKey []byte,
	passed, bondExempt bool,
	height uint64,
	receipt *types.Receipt,
) error {
	const what = "staking set_pass: "
	if err := s.requireAdmin(what, caller, genesisValidator); err != nil {
		return err
	}
	addr, err := validatorAddressOf(pubKey)
	if err != nil {
		return errors.New(what + err.Error())
	}
	if !passed && bondExempt {
		return errors.New(what + "bond_exempt on a revoked pass is meaningless — revoke, or grant with the exemption")
	}
	existing, err := s.GetPass(genesisValidator, addr)
	if err != nil {
		return err
	}
	return s.atomic(receipt, func() error {
		// The implicit genesis entry has never been written, so it does not
		// occupy a list slot: anything GetPass synthesised counts as absent.
		if existing == nil || existing.Genesis {
			if err := s.appendPass(addr); err != nil {
				return errors.New(what + err.Error())
			}
		}
		entry := &PassEntry{
			PublicKey:  append([]byte(nil), pubKey...),
			Address:    addr,
			Passed:     passed,
			BondExempt: bondExempt,
			Height:     height,
		}
		if err := s.setPassEntry(entry); err != nil {
			return err
		}
		// The registry may be written by a grant before anyone has staked, so
		// the marker is set here too — an explorer should not have to wait for
		// the first bond to recognise the account.
		markContract(s.stateDB, s.contract)
		if !passed {
			if err := s.requireProducerRemains(what, genesisValidator); err != nil {
				return err
			}
		}
		s.log(receipt, ValidatorPassSetEvent(s.contract, addr, passed, bondExempt))
		return nil
	})
}

// SetAdmin hands the admission authority to another address. Admin only.
func (s *StakingContract) SetAdmin(caller, genesisValidator, next qcrypto.Address, receipt *types.Receipt) error {
	const what = "staking set_admin: "
	if err := s.requireAdmin(what, caller, genesisValidator); err != nil {
		return err
	}
	if next.IsZero() {
		return errors.New(what + "the new admin is the zero address; no key could ever act as it")
	}
	if err := requireOwnableTarget(s.stateDB, "staking set_admin", s.contract, next); err != nil {
		return err
	}
	current, err := s.Admin(genesisValidator)
	if err != nil {
		return err
	}
	return s.atomic(receipt, func() error {
		if err := s.setAddrSlot(slotStakingAdmin, next); err != nil {
			return err
		}
		s.log(receipt, StakingAdminChangedEvent(s.contract, current, next))
		return nil
	})
}

// EligibleProducerExists reports whether at least one key may still produce.
//
// The walk is bounded by MaxValidatorPasses, plus the implicit genesis entry,
// and stops at the first eligible key.
func (s *StakingContract) EligibleProducerExists(genesisValidator qcrypto.Address) (bool, error) {
	if !genesisValidator.IsZero() && s.CanProduce(genesisValidator, genesisValidator) == nil {
		return true, nil
	}
	entries, err := s.Passes()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if s.CanProduce(genesisValidator, e.Address) == nil {
			return true, nil
		}
	}
	return false, nil
}

// requireProducerRemains refuses a mutation that would leave the chain with
// nobody able to seal a block.
//
// This is not a theoretical concern. Revoking the only producer's pass — or
// slashing it, or letting it unstake — stops block production, and the
// transaction that would undo the mistake can never be mined, because mining it
// requires a producer. The chain is then only recoverable by re-genesis.
//
// So the last eligible key cannot be removed: the exclusion has to be
// preceded by admitting a replacement. It runs inside the action's own
// snapshot, AFTER the mutation, so what it judges is the state the action
// would actually leave behind.
//
// Skipped when the caller has no genesis anchor. Such a node cannot reason
// about who is eligible at all, and it refuses to start on a gated chain
// (cmd/node), so this is unreachable rather than lenient.
func (s *StakingContract) requireProducerRemains(what string, genesisValidator qcrypto.Address) error {
	if genesisValidator.IsZero() {
		return nil
	}
	ok, err := s.EligibleProducerExists(genesisValidator)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%swould leave the chain with no validator able to produce a block, "+
			"and the transaction undoing that could never be mined — admit a replacement first", what)
	}
	return nil
}

// requireAdmin refuses a caller that is not the admission authority.
func (s *StakingContract) requireAdmin(what string, caller, genesisValidator qcrypto.Address) error {
	admin, err := s.Admin(genesisValidator)
	if err != nil {
		return err
	}
	if admin.IsZero() {
		return fmt.Errorf("%sthe registry has no admin and this node does not know the genesis validator, "+
			"so nothing here can be authorised", what)
	}
	if caller != admin {
		return fmt.Errorf("%scaller %s is not the admin (%s)", what, caller, admin)
	}
	return nil
}

// ── Eligibility ───────────────────────────────────────────────────────────────

// CanProduce is THE predicate: may the key behind `addr` seal a block?
//
// It is the single function block production and block acceptance both call,
// so a producer can never build a block its own followers would refuse for a
// reason the producer never evaluated.
//
//	pass && (bond_exempt || (registered && active && stake >= MinValidatorStake))
//
// genesisValidator is the chain's block-0 key address, which is passed
// implicitly (see GetPass). It may be zero on a node that does not know it;
// the gate then has no bootstrap anchor, and only explicit entries admit
// anyone. Callers decide what to do with that — see the executor, which keeps
// the gate inert on an untouched registry rather than halting.
//
// A nil return means eligible. The error says which gate refused and why, in
// text identical on every node judging the same block.
func (s *StakingContract) CanProduce(genesisValidator, addr qcrypto.Address) error {
	entry, err := s.GetPass(genesisValidator, addr)
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("%w: %s is not in the validator pass registry", ErrNotPassed, addr)
	}
	if !entry.Passed {
		return fmt.Errorf("%w: %s was admitted and then revoked", ErrNotPassed, addr)
	}
	if entry.BondExempt {
		return nil
	}
	rec, err := s.GetValidator(addr)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("%w: %s holds a pass but has never registered a stake", ErrNotBonded, addr)
	}
	if rec.Status != ValidatorActive {
		return fmt.Errorf("%w: %s is %s, not active", ErrNotBonded, addr, ValidatorStatusName(rec.Status))
	}
	if rec.Stake.Cmp(MinValidatorStake) < 0 {
		return fmt.Errorf("%w: %s has %s staked, below the minimum of %s",
			ErrNotBonded, addr, rec.Stake, MinValidatorStake)
	}
	return nil
}

// CanProduceKey is CanProduce for a caller holding the key itself — the block
// producer, which knows its own public key, rather than a follower, which has
// only the header's validator address.
//
// Beyond deriving the address it checks that an explicit entry names THIS key.
// Deriving cannot collide (the address is the key's SHA3-256), so this can
// only fire on an entry written for a different key with the same address,
// which is not reachable — it is here so that a future change to how addresses
// are derived fails loudly instead of admitting the wrong key.
func (s *StakingContract) CanProduceKey(genesisValidator qcrypto.Address, pubKey []byte) error {
	addr, err := validatorAddressOf(pubKey)
	if err != nil {
		return fmt.Errorf("staking: %w", err)
	}
	entry, err := s.GetPass(genesisValidator, addr)
	if err != nil {
		return err
	}
	if entry != nil && len(entry.PublicKey) > 0 && !bytesEqual(entry.PublicKey, pubKey) {
		return fmt.Errorf("%w: the pass at %s was granted to a different public key", ErrNotPassed, addr)
	}
	return s.CanProduce(genesisValidator, addr)
}

// ── Validator management ──────────────────────────────────────────────────────

// Register bonds a new validator: the caller's own ML-DSA-65 key, and at least
// MinValidatorStake moved from the caller's balance into escrow.
//
// The caller must BE the key's address. A stake registered against someone
// else's key would be a bond the registrant cannot lose control of and the key
// holder cannot lose — it would gate nothing.
func (s *StakingContract) Register(
	caller qcrypto.Address,
	pubKey []byte,
	stakeAmount *big.Int,
	currentHeight uint64,
	receipt *types.Receipt,
) error {
	const what = "staking register: "
	addr, err := validatorAddressOf(pubKey)
	if err != nil {
		return errors.New(what + err.Error())
	}
	if addr != caller {
		return fmt.Errorf("%spublic key belongs to %s, not to the caller %s", what, addr, caller)
	}
	if stakeAmount.Cmp(MinValidatorStake) < 0 {
		return fmt.Errorf("%sstake %s is below the minimum of %s", what, stakeAmount, MinValidatorStake)
	}
	existing, err := s.GetValidator(caller)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%s%s is already registered — use add_stake", what, caller)
	}
	return s.atomic(receipt, func() error {
		if err := s.escrowIn(what, caller, stakeAmount); err != nil {
			return err
		}
		rec := &ValidatorRecord{
			Address:      caller,
			PublicKey:    append([]byte(nil), pubKey...),
			Stake:        new(big.Int).Set(stakeAmount),
			Unbonding:    big.NewInt(0),
			Status:       ValidatorActive,
			Tier:         computeTier(stakeAmount),
			Rewards:      big.NewInt(0),
			JoinedHeight: currentHeight,
			UptimeScore:  100,
		}
		if err := s.setValidator(rec); err != nil {
			return err
		}
		if err := s.addTotalStaked(stakeAmount); err != nil {
			return err
		}
		count, err := s.ValidatorCount()
		if err != nil {
			return err
		}
		if err := s.setBigInt(slotStakingValidatorCount, new(big.Int).SetUint64(count+1)); err != nil {
			return err
		}
		markContract(s.stateDB, s.contract)
		s.log(receipt, ValidatorRegisteredEvent(s.contract, caller, rec.Stake))
		return nil
	})
}

// AddStake tops up an existing bond, moving more SPLD into escrow. A validator
// that had fallen below the minimum becomes Active again once it clears it; a
// jailed one must go through Unjail instead, so that returning from a slash is
// always a deliberate act.
func (s *StakingContract) AddStake(caller qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	const what = "staking add_stake: "
	rec, err := s.requireValidator(what, caller)
	if err != nil {
		return err
	}
	if amount.Sign() <= 0 {
		return errors.New(what + "amount must be positive")
	}
	if rec.Status == ValidatorJailed {
		return errors.New(what + "validator is jailed — use unjail")
	}
	return s.atomic(receipt, func() error {
		if err := s.escrowIn(what, caller, amount); err != nil {
			return err
		}
		rec.Stake = new(big.Int).Add(rec.Stake, amount)
		rec.Tier = computeTier(rec.Stake)
		if rec.Stake.Cmp(MinValidatorStake) >= 0 {
			rec.Status = ValidatorActive
		}
		if err := s.setValidator(rec); err != nil {
			return err
		}
		if err := s.addTotalStaked(amount); err != nil {
			return err
		}
		s.log(receipt, ValidatorStakeAddedEvent(s.contract, caller, amount, rec.Stake))
		return nil
	})
}

// Unstake moves stake out of the bond and into unbonding. The coins stay in
// escrow until UnbondingBlocks have passed and Withdraw is called.
//
// A second unstake before the first matures adds to the same pending amount
// and RESTARTS the clock. Letting the later amount ride the earlier one's
// timer would make the delay a function of when you first unstaked rather than
// of when you last did, which is a bond that can be released early by anyone
// who plans one unbonding period ahead.
func (s *StakingContract) Unstake(caller, genesisValidator qcrypto.Address, amount *big.Int, height uint64, receipt *types.Receipt) error {
	const what = "staking unstake: "
	rec, err := s.requireValidator(what, caller)
	if err != nil {
		return err
	}
	if amount.Sign() <= 0 {
		return errors.New(what + "amount must be positive")
	}
	if rec.Stake.Cmp(amount) < 0 {
		return fmt.Errorf("%sstake is %s, cannot unstake %s", what, rec.Stake, amount)
	}
	return s.atomic(receipt, func() error {
		rec.Stake = new(big.Int).Sub(rec.Stake, amount)
		rec.Unbonding = new(big.Int).Add(rec.Unbonding, amount)
		rec.UnbondingHeight = height + UnbondingBlocks
		rec.Tier = computeTier(rec.Stake)
		if rec.Stake.Cmp(MinValidatorStake) < 0 && rec.Status == ValidatorActive {
			rec.Status = ValidatorExiting
		}
		if err := s.setValidator(rec); err != nil {
			return err
		}
		if err := s.subTotalStaked(amount); err != nil {
			return err
		}
		// A validator withdrawing its own bond can halt the chain exactly as a
		// revocation can, so the last producer cannot unstake its way out
		// either. It is refused with the same explanation.
		if err := s.requireProducerRemains(what, genesisValidator); err != nil {
			return err
		}
		s.log(receipt, ValidatorUnstakedEvent(s.contract, caller, amount, rec.UnbondingHeight))
		return nil
	})
}

// Withdraw pays out matured unbonding from escrow back to the validator.
func (s *StakingContract) Withdraw(caller qcrypto.Address, height uint64, receipt *types.Receipt) error {
	const what = "staking withdraw: "
	rec, err := s.requireValidator(what, caller)
	if err != nil {
		return err
	}
	if rec.Unbonding.Sign() == 0 {
		return errors.New(what + "nothing is unbonding")
	}
	if height < rec.UnbondingHeight {
		return fmt.Errorf("%s%s unbonds at height %d, the chain is at %d",
			what, rec.Unbonding, rec.UnbondingHeight, height)
	}
	amount := new(big.Int).Set(rec.Unbonding)
	return s.atomic(receipt, func() error {
		if err := s.stateDB.Transfer(s.contract, caller, amount); err != nil {
			return fmt.Errorf("%sescrow payout: %w", what, err)
		}
		rec.Unbonding = big.NewInt(0)
		rec.UnbondingHeight = 0
		if err := s.setValidator(rec); err != nil {
			return err
		}
		s.log(receipt, ValidatorWithdrawnEvent(s.contract, caller, amount))
		return nil
	})
}

// Unjail returns a slashed validator to Active, optionally topping the bond up
// in the same action. The bond must clear the minimum afterwards.
func (s *StakingContract) Unjail(caller qcrypto.Address, additionalStake *big.Int, receipt *types.Receipt) error {
	const what = "staking unjail: "
	rec, err := s.requireValidator(what, caller)
	if err != nil {
		return err
	}
	if rec.Status != ValidatorJailed {
		return fmt.Errorf("%s%s is %s, not jailed", what, caller, ValidatorStatusName(rec.Status))
	}
	if additionalStake.Sign() < 0 {
		return errors.New(what + "amount cannot be negative")
	}
	return s.atomic(receipt, func() error {
		if additionalStake.Sign() > 0 {
			if err := s.escrowIn(what, caller, additionalStake); err != nil {
				return err
			}
			rec.Stake = new(big.Int).Add(rec.Stake, additionalStake)
			if err := s.addTotalStaked(additionalStake); err != nil {
				return err
			}
		}
		if rec.Stake.Cmp(MinValidatorStake) < 0 {
			return fmt.Errorf("%sstake is %s after the top-up, still below the minimum of %s",
				what, rec.Stake, MinValidatorStake)
		}
		rec.Status = ValidatorActive
		rec.Tier = computeTier(rec.Stake)
		if err := s.setValidator(rec); err != nil {
			return err
		}
		s.log(receipt, ValidatorUnjailedEvent(s.contract, caller, rec.Stake))
		return nil
	})
}

// Slash burns part of a validator's bond and jails it. Admin only.
//
// Jailing is unconditional, not a consequence of dropping below the floor. A
// validator that was slashed has been judged to have misbehaved, and coming
// back has to be a deliberate act (Unjail) rather than something that happens
// on its own the moment the bond is topped up again.
//
// The slashed SPLD does NOT leave the escrow account and is not credited to
// anyone: no action pays it out, and no key derives to the module address, so
// it is out of circulation for good. That is deliberate — a slash that paid
// someone would give whoever can call this a reason to call it.
func (s *StakingContract) Slash(
	caller, genesisValidator, validator qcrypto.Address,
	slashAmount *big.Int,
	currentHeight uint64,
	receipt *types.Receipt,
) error {
	const what = "staking slash: "
	if err := s.requireAdmin(what, caller, genesisValidator); err != nil {
		return err
	}
	rec, err := s.requireValidator(what, validator)
	if err != nil {
		return err
	}
	if slashAmount.Sign() <= 0 {
		return errors.New(what + "amount must be positive")
	}
	// Capped rather than refused: the point of a slash is to take what is
	// there, and an over-estimate must not make the penalty a no-op.
	burn := new(big.Int).Set(slashAmount)
	if rec.Stake.Cmp(burn) < 0 {
		burn.Set(rec.Stake)
	}
	return s.atomic(receipt, func() error {
		rec.Stake = new(big.Int).Sub(rec.Stake, burn)
		rec.LastSlashed = currentHeight
		rec.Tier = computeTier(rec.Stake)
		rec.Status = ValidatorJailed
		if err := s.setValidator(rec); err != nil {
			return err
		}
		if err := s.subTotalStaked(burn); err != nil {
			return err
		}
		slashed, err := s.getBigInt(slotStakingSlashedTotal)
		if err != nil {
			return err
		}
		if err := s.setBigInt(slotStakingSlashedTotal, new(big.Int).Add(slashed, burn)); err != nil {
			return err
		}
		if err := s.requireProducerRemains(what, genesisValidator); err != nil {
			return err
		}
		s.log(receipt, ValidatorSlashedEvent(s.contract, validator, burn, rec.Stake))
		return nil
	})
}

// AddRewards credits pending rewards to a validator.
//
// Nothing on the chain calls this yet: block rewards are Phase 2, and until
// there is an issuance path a validator's Rewards stays at zero. It is kept
// (and unrouted) so the record shape does not have to change when that lands.
func (s *StakingContract) AddRewards(validator qcrypto.Address, amount *big.Int) error {
	rec, err := s.GetValidator(validator)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("staking: validator %s not found", validator)
	}
	rec.Rewards = new(big.Int).Add(rec.Rewards, amount)
	return s.setValidator(rec)
}

// UpdateUptime updates a validator's uptime score (0–100).
func (s *StakingContract) UpdateUptime(validator qcrypto.Address, score uint8) error {
	rec, err := s.GetValidator(validator)
	if err != nil || rec == nil {
		return err // unknown validators are silently ignored
	}
	rec.UptimeScore = score
	return s.setValidator(rec)
}

// ── Queries ────────────────────────────────────────────────────────────────────

// GetValidator returns the validator record for the given address, or nil (and
// no error) when the address has never registered.
func (s *StakingContract) GetValidator(addr qcrypto.Address) (*ValidatorRecord, error) {
	b, err := s.stateDB.GetStorageValue(s.contract, stakingValidatorSlot(addr))
	if err != nil {
		return nil, fmt.Errorf("staking: read validator %s: %w", addr, err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var rec ValidatorRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("staking: failed to decode validator: %w", err)
	}
	if rec.Stake == nil {
		rec.Stake = big.NewInt(0)
	}
	if rec.Unbonding == nil {
		rec.Unbonding = big.NewInt(0)
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

// TotalStaked returns the live bonded total (unbonding amounts are excluded —
// they no longer back anyone's eligibility).
func (s *StakingContract) TotalStaked() (*big.Int, error) {
	return s.getBigInt(slotStakingTotalStaked)
}

// SlashedTotal returns how much stake has been slashed on this chain.
func (s *StakingContract) SlashedTotal() (*big.Int, error) {
	return s.getBigInt(slotStakingSlashedTotal)
}

// ValidatorCount returns the number of addresses that have ever registered.
func (s *StakingContract) ValidatorCount() (uint64, error) {
	v, err := s.getBigInt(slotStakingValidatorCount)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// PassCount returns how many keys hold an explicit registry entry (granted or
// revoked). The implicit genesis entry is not counted: it was never written.
func (s *StakingContract) PassCount() (uint64, error) {
	v, err := s.getBigInt(slotStakingPassCount)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// Passes returns every explicit entry in insertion order, bounded by
// MaxValidatorPasses.
func (s *StakingContract) Passes() ([]*PassEntry, error) {
	n, err := s.PassCount()
	if err != nil {
		return nil, err
	}
	out := make([]*PassEntry, 0, n)
	for i := uint64(0); i < n && i < MaxValidatorPasses; i++ {
		raw, err := s.stateDB.GetStorageValue(s.contract, stakingPassListSlot(i))
		if err != nil {
			return nil, fmt.Errorf("staking: read pass list %d: %w", i, err)
		}
		if len(raw) < qcrypto.AddressSize {
			continue
		}
		var addr qcrypto.Address
		copy(addr[:], raw)
		// Zero genesis validator: the list holds only explicit entries, and
		// GetPass synthesises nothing for an address that has one.
		entry, err := s.GetPass(qcrypto.Address{}, addr)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

// EscrowBalance is what the module currently holds: the sum of every live bond
// and every pending unbonding, plus anything slashed and anything donated.
func (s *StakingContract) EscrowBalance() (*big.Int, error) {
	acc, err := s.stateDB.GetAccount(s.contract)
	if err != nil {
		return nil, fmt.Errorf("staking: escrow balance: %w", err)
	}
	if acc.Balance == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(acc.Balance), nil
}

// GetTierName returns the staking tier name for a given stake amount.
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

// ValidatorStatusName renders a status for humans and for the read API.
func ValidatorStatusName(s ValidatorStatus) string {
	switch s {
	case ValidatorActive:
		return "active"
	case ValidatorJailed:
		return "jailed"
	case ValidatorExiting:
		return "exiting"
	default:
		return "inactive"
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// validatorAddressOf parses an ML-DSA-65 public key and returns its address.
func validatorAddressOf(pubKey []byte) (qcrypto.Address, error) {
	if len(pubKey) != qcrypto.PublicKeySize {
		return qcrypto.Address{}, fmt.Errorf("public key is %d bytes, want %d", len(pubKey), qcrypto.PublicKeySize)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKey)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("public key: %w", err)
	}
	return qcrypto.DeriveAddress(pub), nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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

// requireValidator loads a record that must exist.
func (s *StakingContract) requireValidator(what string, addr qcrypto.Address) (*ValidatorRecord, error) {
	rec, err := s.GetValidator(addr)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("%s%s has not registered a stake", what, addr)
	}
	return rec, nil
}

// escrowIn moves a bond from the staker into the module's escrow account. The
// balance check is Transfer's (types.ErrInsufficientBalance), so a staker who
// cannot cover the amount gets the same refusal a plain transfer would give.
func (s *StakingContract) escrowIn(what string, from qcrypto.Address, amount *big.Int) error {
	if err := s.stateDB.Transfer(from, s.contract, amount); err != nil {
		return fmt.Errorf("%sbonding %s: %w", what, amount, err)
	}
	return nil
}

func (s *StakingContract) addTotalStaked(amount *big.Int) error {
	total, err := s.TotalStaked()
	if err != nil {
		return err
	}
	return s.setBigInt(slotStakingTotalStaked, new(big.Int).Add(total, amount))
}

// subTotalStaked floors at zero: the total is a reporting figure derived from
// the records, and a negative one would be reported as a wildly large unsigned
// value by anything reading the slot back.
func (s *StakingContract) subTotalStaked(amount *big.Int) error {
	total, err := s.TotalStaked()
	if err != nil {
		return err
	}
	next := new(big.Int).Sub(total, amount)
	if next.Sign() < 0 {
		next = big.NewInt(0)
	}
	return s.setBigInt(slotStakingTotalStaked, next)
}

// appendPass adds an address to the bounded enumeration list.
func (s *StakingContract) appendPass(addr qcrypto.Address) error {
	idx, err := s.getBigInt(stakingPassIndexSlot(addr))
	if err != nil {
		return err
	}
	if idx.Sign() > 0 {
		return nil // already listed (stored as index+1)
	}
	n, err := s.PassCount()
	if err != nil {
		return err
	}
	if n >= MaxValidatorPasses {
		return fmt.Errorf("the registry already holds %d entries, the cap", MaxValidatorPasses)
	}
	if err := s.stateDB.SetStorageValue(s.contract, stakingPassListSlot(n), addr[:]); err != nil {
		return err
	}
	if err := s.setBigInt(stakingPassIndexSlot(addr), new(big.Int).SetUint64(n+1)); err != nil {
		return err
	}
	return s.setBigInt(slotStakingPassCount, new(big.Int).SetUint64(n+1))
}

func (s *StakingContract) setValidator(rec *ValidatorRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, stakingValidatorSlot(rec.Address), data)
}

func (s *StakingContract) setPassEntry(e *PassEntry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.stateDB.SetStorageValue(s.contract, stakingPassSlot(e.Address), data)
}

// atomic runs fn under a StateDB snapshot, so a partially-applied action
// (escrow moved, record not written) can never survive its own failure. Mirrors
// the bridge's helper: a snapshot-layer failure is HARD, because the state is
// then not what either branch describes.
func (s *StakingContract) atomic(receipt *types.Receipt, fn func() error) error {
	snap := s.stateDB.Snapshot()
	logs := 0
	if receipt != nil {
		logs = len(receipt.Logs)
	}
	if err := fn(); err != nil {
		if rerr := s.stateDB.RevertToSnapshot(snap); rerr != nil {
			return hard(fmt.Errorf("staking: %w", rerr))
		}
		if receipt != nil {
			receipt.Logs = receipt.Logs[:logs]
		}
		return err
	}
	if err := s.stateDB.CommitSnapshot(snap); err != nil {
		return hard(fmt.Errorf("staking: %w", err))
	}
	return nil
}

func (s *StakingContract) log(receipt *types.Receipt, l *types.Log) {
	if receipt != nil {
		receipt.AddLog(l)
	}
}

func (s *StakingContract) setBigInt(slot [32]byte, v *big.Int) error {
	return s.stateDB.SetStorageValue(s.contract, slot, v.Bytes())
}

// getBigInt reports a StateDB read error rather than swallowing it: an empty
// slot is zero, an I/O fault is not, and treating the second as the first is
// how two nodes end up disagreeing about a total.
func (s *StakingContract) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := s.stateDB.GetStorageValue(s.contract, slot)
	if err != nil {
		return big.NewInt(0), fmt.Errorf("staking: read slot %x: %w", slot[:4], err)
	}
	if len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (s *StakingContract) setAddrSlot(slot [32]byte, a qcrypto.Address) error {
	return s.stateDB.SetStorageValue(s.contract, slot, a[:])
}

func (s *StakingContract) getAddrSlot(slot [32]byte) (qcrypto.Address, error) {
	v, err := s.stateDB.GetStorageValue(s.contract, slot)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("staking: read slot %x: %w", slot[:4], err)
	}
	if len(v) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var a qcrypto.Address
	copy(a[:], v)
	return a, nil
}
