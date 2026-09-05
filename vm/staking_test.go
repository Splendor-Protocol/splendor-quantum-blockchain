package vm

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// validatorKey returns a deterministic ML-DSA-65 keypair's public bytes and
// the address they derive to. Deterministic so a failing run reproduces.
func validatorKey(t *testing.T, seedByte byte) ([]byte, qcrypto.Address) {
	t.Helper()
	var seed [qcrypto.SeedSize]byte
	for i := range seed {
		seed[i] = seedByte
	}
	pub, _ := qcrypto.KeypairFromSeed(seed)
	return pub.Bytes(), qcrypto.DeriveAddress(pub)
}

func fund(t *testing.T, sdb *state.StateDB, a qcrypto.Address, amount *big.Int) {
	t.Helper()
	acc, err := sdb.GetAccount(a)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	acc.AddBalance(amount)
	sdb.SetAccount(acc)
}

func balanceOf(t *testing.T, sdb *state.StateDB, a qcrypto.Address) *big.Int {
	t.Helper()
	b, err := sdb.GetBalance(a)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return b
}

// spld returns n SPLD in smallest units.
func spld(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), spldUnit())
}

// stakingFixture is a registry on a fresh StateDB, with one funded staker.
type stakingFixture struct {
	sdb     *state.StateDB
	s       *StakingContract
	pub     []byte
	addr    qcrypto.Address
	genesis qcrypto.Address
}

func newStakingFixture(t *testing.T) *stakingFixture {
	t.Helper()
	sdb := newTestState(t)
	pub, a := validatorKey(t, 0x11)
	_, genesis := validatorKey(t, 0xAA)
	fund(t, sdb, a, spld(1_000_000))
	return &stakingFixture{sdb: sdb, s: Staking(sdb), pub: pub, addr: a, genesis: genesis}
}

func (f *stakingFixture) register(t *testing.T, amount *big.Int, height uint64) error {
	t.Helper()
	return f.s.Register(f.addr, f.pub, amount, height, &types.Receipt{})
}

// A bond is only a bond if the coins actually move: the module's escrow gains
// exactly what the staker loses, and the record says so.
func TestRegisterMovesTheBondIntoEscrow(t *testing.T) {
	f := newStakingFixture(t)
	before := balanceOf(t, f.sdb, f.addr)
	bond := spld(5_000)

	if err := f.register(t, bond, 7); err != nil {
		t.Fatalf("register: %v", err)
	}

	if got, want := balanceOf(t, f.sdb, f.addr), new(big.Int).Sub(before, bond); got.Cmp(want) != 0 {
		t.Errorf("staker balance = %s, want %s", got, want)
	}
	escrow, err := f.s.EscrowBalance()
	if err != nil {
		t.Fatalf("escrow: %v", err)
	}
	if escrow.Cmp(bond) != 0 {
		t.Errorf("escrow = %s, want %s", escrow, bond)
	}
	rec, err := f.s.GetValidator(f.addr)
	if err != nil || rec == nil {
		t.Fatalf("GetValidator = %v, %v", rec, err)
	}
	if rec.Stake.Cmp(bond) != 0 || rec.Status != ValidatorActive || rec.JoinedHeight != 7 {
		t.Errorf("record = %+v", rec)
	}
	if rec.Tier != TierBronze {
		t.Errorf("tier = %d, want Bronze", rec.Tier)
	}
	total, _ := f.s.TotalStaked()
	if total.Cmp(bond) != 0 {
		t.Errorf("total staked = %s, want %s", total, bond)
	}
	count, _ := f.s.ValidatorCount()
	if count != 1 {
		t.Errorf("validator count = %d, want 1", count)
	}
}

// Registering someone else's key would bond coins the key holder never risks
// and the registrant cannot be punished for — a gate that gates nothing.
func TestRegisterRefusesAKeyThatIsNotTheCallers(t *testing.T) {
	f := newStakingFixture(t)
	otherPub, _ := validatorKey(t, 0x22)
	err := f.s.Register(f.addr, otherPub, spld(5_000), 1, &types.Receipt{})
	if err == nil {
		t.Fatal("registering another key's stake must be refused")
	}
	if rec, _ := f.s.GetValidator(f.addr); rec != nil {
		t.Error("a refused register must leave no record")
	}
}

func TestRegisterRefusesBelowTheMinimum(t *testing.T) {
	f := newStakingFixture(t)
	just_under := new(big.Int).Sub(MinValidatorStake, big.NewInt(1))
	if err := f.register(t, just_under, 1); err == nil {
		t.Fatal("a stake one unit below the minimum must be refused")
	}
	if err := f.register(t, new(big.Int).Set(MinValidatorStake), 1); err != nil {
		t.Fatalf("a stake exactly at the minimum must be accepted: %v", err)
	}
}

// A register that cannot pay must leave nothing behind: no record, no count,
// no total. The snapshot is what guarantees that, so it is worth pinning.
func TestRegisterIsAtomicWhenTheStakerCannotPay(t *testing.T) {
	sdb := newTestState(t)
	s := Staking(sdb)
	pub, a := validatorKey(t, 0x33)

	err := s.Register(a, pub, spld(5_000), 1, &types.Receipt{})
	if err == nil {
		t.Fatal("an unfunded staker must not be able to register")
	}
	if rec, _ := s.GetValidator(a); rec != nil {
		t.Error("a failed register left a validator record behind")
	}
	if n, _ := s.ValidatorCount(); n != 0 {
		t.Errorf("validator count = %d after a failed register", n)
	}
	if total, _ := s.TotalStaked(); total.Sign() != 0 {
		t.Errorf("total staked = %s after a failed register", total)
	}
	if escrow, _ := s.EscrowBalance(); escrow.Sign() != 0 {
		t.Errorf("escrow = %s after a failed register", escrow)
	}
}

func TestRegisterTwiceIsRefused(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.register(t, spld(5_000), 2); err == nil {
		t.Fatal("a second register must be refused — add_stake is the way up")
	}
}

// The full bond lifecycle: top up, unstake, wait, withdraw. The escrow holds
// the unbonding amount the whole time, so a withdrawal is always payable.
func TestStakeUnstakeWithdrawLifecycle(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.AddStake(f.addr, spld(1_000), &types.Receipt{}); err != nil {
		t.Fatalf("add_stake: %v", err)
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Stake.Cmp(spld(6_000)) != 0 {
		t.Fatalf("stake after top-up = %s", rec.Stake)
	}

	// Unstaking below the floor moves the validator out of Active.
	if err := f.s.Unstake(f.addr, f.genesis, spld(3_000), 100, &types.Receipt{}); err != nil {
		t.Fatalf("unstake: %v", err)
	}
	rec, _ = f.s.GetValidator(f.addr)
	if rec.Stake.Cmp(spld(3_000)) != 0 || rec.Unbonding.Cmp(spld(3_000)) != 0 {
		t.Fatalf("after unstake: stake=%s unbonding=%s", rec.Stake, rec.Unbonding)
	}
	if rec.Status != ValidatorExiting {
		t.Errorf("status = %s, want exiting once the bond is under the floor", ValidatorStatusName(rec.Status))
	}
	if rec.UnbondingHeight != 100+UnbondingBlocks {
		t.Errorf("unbonding height = %d, want %d", rec.UnbondingHeight, 100+UnbondingBlocks)
	}
	if total, _ := f.s.TotalStaked(); total.Cmp(spld(3_000)) != 0 {
		t.Errorf("total staked = %s, unbonding must not count", total)
	}

	// The coins are still in escrow — unstaking does not pay out.
	if escrow, _ := f.s.EscrowBalance(); escrow.Cmp(spld(6_000)) != 0 {
		t.Errorf("escrow = %s, want the whole 6000 still held", escrow)
	}

	// Early withdrawal is refused.
	if err := f.s.Withdraw(f.addr, 100+UnbondingBlocks-1, &types.Receipt{}); err == nil {
		t.Fatal("withdrawing one block early must be refused")
	}

	before := balanceOf(t, f.sdb, f.addr)
	if err := f.s.Withdraw(f.addr, 100+UnbondingBlocks, &types.Receipt{}); err != nil {
		t.Fatalf("withdraw at the release height: %v", err)
	}
	if got, want := balanceOf(t, f.sdb, f.addr), new(big.Int).Add(before, spld(3_000)); got.Cmp(want) != 0 {
		t.Errorf("balance after withdraw = %s, want %s", got, want)
	}
	if escrow, _ := f.s.EscrowBalance(); escrow.Cmp(spld(3_000)) != 0 {
		t.Errorf("escrow after withdraw = %s, want the live bond only", escrow)
	}
	if err := f.s.Withdraw(f.addr, 1<<40, &types.Receipt{}); err == nil {
		t.Fatal("a second withdraw with nothing pending must be refused")
	}
}

// Unstaking again before the first amount matures must restart the clock. If
// the later amount rode the earlier one's timer, anyone planning one unbonding
// period ahead could release a bond on demand.
func TestUnstakeRestartsTheUnbondingClock(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(10_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.Unstake(f.addr, f.genesis, spld(1_000), 10, &types.Receipt{}); err != nil {
		t.Fatalf("first unstake: %v", err)
	}
	if err := f.s.Unstake(f.addr, f.genesis, spld(1_000), 500, &types.Receipt{}); err != nil {
		t.Fatalf("second unstake: %v", err)
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Unbonding.Cmp(spld(2_000)) != 0 {
		t.Errorf("unbonding = %s, want the two amounts pooled", rec.Unbonding)
	}
	if rec.UnbondingHeight != 500+UnbondingBlocks {
		t.Errorf("unbonding height = %d, want the LATER unstake's clock (%d)",
			rec.UnbondingHeight, 500+UnbondingBlocks)
	}
	if err := f.s.Withdraw(f.addr, 10+UnbondingBlocks, &types.Receipt{}); err == nil {
		t.Fatal("the first unstake's height must not release the pooled amount")
	}
}

func TestUnstakeMoreThanStakedIsRefused(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.Unstake(f.addr, f.genesis, spld(5_001), 1, &types.Receipt{}); err == nil {
		t.Fatal("unstaking more than the bond must be refused")
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Stake.Cmp(spld(5_000)) != 0 || rec.Unbonding.Sign() != 0 {
		t.Errorf("a refused unstake changed the record: %+v", rec)
	}
}

// A slash takes the coins out of anyone's reach: the record shrinks, the
// escrow does not, and no action pays the difference to anybody.
func TestSlashJailsAndStrandsTheCoins(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	escrowBefore, _ := f.s.EscrowBalance()

	if err := f.s.Slash(f.addr, f.genesis, f.addr, spld(2_000), 42, &types.Receipt{}); err == nil {
		t.Fatal("slash by a non-admin must be refused")
	}

	if err := f.s.Slash(f.genesis, f.genesis, f.addr, spld(2_000), 42, &types.Receipt{}); err != nil {
		t.Fatalf("slash by the admin: %v", err)
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Stake.Cmp(spld(3_000)) != 0 {
		t.Errorf("stake after slash = %s, want 3000", rec.Stake)
	}
	if rec.Status != ValidatorJailed {
		t.Errorf("status = %s, want jailed once under the floor", ValidatorStatusName(rec.Status))
	}
	if rec.LastSlashed != 42 {
		t.Errorf("last slashed = %d, want 42", rec.LastSlashed)
	}
	if escrow, _ := f.s.EscrowBalance(); escrow.Cmp(escrowBefore) != 0 {
		t.Errorf("escrow = %s, want it unchanged at %s — a slash pays nobody", escrow, escrowBefore)
	}
	if slashed, _ := f.s.SlashedTotal(); slashed.Cmp(spld(2_000)) != 0 {
		t.Errorf("slashed total = %s, want 2000", slashed)
	}
	if total, _ := f.s.TotalStaked(); total.Cmp(spld(3_000)) != 0 {
		t.Errorf("total staked = %s, want 3000", total)
	}
}

// An over-estimated slash takes what is there rather than doing nothing.
func TestSlashIsCappedAtTheBond(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	ask := spld(9_999)
	if err := f.s.Slash(f.genesis, f.genesis, f.addr, ask, 1, &types.Receipt{}); err != nil {
		t.Fatalf("slash: %v", err)
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Stake.Sign() != 0 {
		t.Errorf("stake = %s, want 0", rec.Stake)
	}
	if ask.Cmp(spld(9_999)) != 0 {
		t.Errorf("slash mutated the caller's amount: %s", ask)
	}
	if slashed, _ := f.s.SlashedTotal(); slashed.Cmp(spld(5_000)) != 0 {
		t.Errorf("slashed total = %s, want the bond that was there", slashed)
	}
}

func TestUnjailNeedsTheMinimumBack(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.Slash(f.genesis, f.genesis, f.addr, spld(4_000), 1, &types.Receipt{}); err != nil {
		t.Fatalf("slash: %v", err)
	}
	if err := f.s.AddStake(f.addr, spld(100), &types.Receipt{}); err == nil {
		t.Fatal("add_stake on a jailed validator must be refused — unjail is the way back")
	}
	if err := f.s.Unjail(f.addr, spld(100), &types.Receipt{}); err == nil {
		t.Fatal("unjail below the minimum must be refused")
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Stake.Cmp(spld(1_000)) != 0 {
		t.Fatalf("a refused unjail must not keep the top-up: stake=%s", rec.Stake)
	}
	if err := f.s.Unjail(f.addr, spld(3_000), &types.Receipt{}); err != nil {
		t.Fatalf("unjail with enough to clear the floor: %v", err)
	}
	rec, _ = f.s.GetValidator(f.addr)
	if rec.Status != ValidatorActive || rec.Stake.Cmp(spld(4_000)) != 0 {
		t.Errorf("after unjail: %+v", rec)
	}
}

// ── Admission ────────────────────────────────────────────────────────────────

// An untouched registry has nothing to say. The executor reads this to keep a
// chain that adopts the upgrade by fork extension from halting on itself.
func TestInitialisedReportsAnUntouchedRegistry(t *testing.T) {
	f := newStakingFixture(t)
	if init, err := f.s.Initialised(); err != nil || init {
		t.Fatalf("Initialised = %v, %v on a fresh registry", init, err)
	}
	pub, _ := validatorKey(t, 0x44)
	if err := f.s.SetPass(f.genesis, f.genesis, pub, true, false, 3, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	if init, err := f.s.Initialised(); err != nil || !init {
		t.Fatalf("Initialised = %v, %v after a grant", init, err)
	}
}

// The genesis validator is passed and bond-exempt without anything being
// written: it has to produce block 1 before anything can be staked.
func TestGenesisValidatorIsPassedImplicitly(t *testing.T) {
	f := newStakingFixture(t)
	entry, err := f.s.GetPass(f.genesis, f.genesis)
	if err != nil {
		t.Fatalf("GetPass: %v", err)
	}
	if entry == nil || !entry.Passed || !entry.BondExempt || !entry.Genesis {
		t.Fatalf("genesis entry = %+v", entry)
	}
	if err := f.s.CanProduce(f.genesis, f.genesis); err != nil {
		t.Errorf("the genesis validator must be able to produce: %v", err)
	}
	// Nobody else is.
	if err := f.s.CanProduce(f.genesis, f.addr); !errors.Is(err, ErrNotPassed) {
		t.Errorf("CanProduce for an unlisted key = %v, want ErrNotPassed", err)
	}
	// And with no anchor at all, not even the genesis address is admitted.
	if err := f.s.CanProduce(qcrypto.Address{}, f.genesis); !errors.Is(err, ErrNotPassed) {
		t.Errorf("with no genesis anchor: %v, want ErrNotPassed", err)
	}
}

// The implicit entry is a default, not a privilege: an explicit revocation
// overrides it — once somebody else can take over.
func TestGenesisPassCanBeRevoked(t *testing.T) {
	f := newStakingFixture(t)
	genesisPub, genesisAddr := validatorKey(t, 0xAA)
	if genesisAddr != f.genesis {
		t.Fatalf("fixture genesis address drifted")
	}
	if err := f.s.SetPass(f.genesis, f.genesis, f.pub, true, true, 8, &types.Receipt{}); err != nil {
		t.Fatalf("admit a successor: %v", err)
	}
	if err := f.s.SetPass(f.genesis, f.genesis, genesisPub, false, false, 9, &types.Receipt{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.genesis); !errors.Is(err, ErrNotPassed) {
		t.Fatalf("a revoked genesis key must not produce: %v", err)
	}
	entry, _ := f.s.GetPass(f.genesis, f.genesis)
	if entry == nil || entry.Genesis {
		t.Fatalf("an explicit entry must replace the implicit one: %+v", entry)
	}
}

// Removing the last eligible producer stops block production, and the
// transaction that would undo it can never be mined — the chain would only be
// recoverable by re-genesis. Every path that could do it is refused instead.
func TestTheLastProducerCannotBeRemoved(t *testing.T) {
	genesisPub, _ := validatorKey(t, 0xAA)

	t.Run("revoke", func(t *testing.T) {
		f := newStakingFixture(t)
		err := f.s.SetPass(f.genesis, f.genesis, genesisPub, false, false, 1, &types.Receipt{})
		if err == nil {
			t.Fatal("revoking the only producer must be refused")
		}
		if !strings.Contains(err.Error(), "no validator able to produce") {
			t.Fatalf("refusal = %v", err)
		}
		// And the refusal left nothing behind: the key still produces.
		if err := f.s.CanProduce(f.genesis, f.genesis); err != nil {
			t.Fatalf("the refused revocation took effect anyway: %v", err)
		}
	})

	t.Run("slash", func(t *testing.T) {
		f := newStakingFixture(t)
		// Hand production to a bonded key, then revoke the genesis default, so
		// the bonded key is the only thing keeping the chain alive.
		if err := f.register(t, spld(5_000), 1); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := f.s.SetPass(f.genesis, f.genesis, f.pub, true, false, 1, &types.Receipt{}); err != nil {
			t.Fatalf("admit: %v", err)
		}
		if err := f.s.SetPass(f.genesis, f.genesis, genesisPub, false, false, 1, &types.Receipt{}); err != nil {
			t.Fatalf("revoke genesis: %v", err)
		}
		if err := f.s.Slash(f.genesis, f.genesis, f.addr, spld(1), 2, &types.Receipt{}); err == nil {
			t.Fatal("slashing the only producer must be refused")
		}
		if err := f.s.CanProduce(f.genesis, f.addr); err != nil {
			t.Fatalf("the refused slash jailed it anyway: %v", err)
		}
		if err := f.s.Unstake(f.addr, f.genesis, spld(2_000), 2, &types.Receipt{}); err == nil {
			t.Fatal("the only producer must not be able to unstake below the floor either")
		}
		if err := f.s.CanProduce(f.genesis, f.addr); err != nil {
			t.Fatalf("the refused unstake took effect anyway: %v", err)
		}
		// Unstaking an amount that keeps it above the floor is still fine.
		if err := f.s.Unstake(f.addr, f.genesis, spld(500), 2, &types.Receipt{}); err != nil {
			t.Fatalf("a safe unstake must still work: %v", err)
		}
	})

	t.Run("with a successor admitted", func(t *testing.T) {
		f := newStakingFixture(t)
		successorPub, _ := validatorKey(t, 0xC3)
		if err := f.s.SetPass(f.genesis, f.genesis, successorPub, true, true, 1, &types.Receipt{}); err != nil {
			t.Fatalf("admit a successor: %v", err)
		}
		if err := f.s.SetPass(f.genesis, f.genesis, genesisPub, false, false, 2, &types.Receipt{}); err != nil {
			t.Fatalf("with a successor in place the revocation must be allowed: %v", err)
		}
	})
}

// Both gates, independently: a pass without a bond and a bond without a pass
// are each refused, and the sentinel says which.
func TestCanProduceNeedsPassAndBond(t *testing.T) {
	f := newStakingFixture(t)

	// Bonded, never passed.
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.addr); !errors.Is(err, ErrNotPassed) {
		t.Fatalf("bonded but unpassed = %v, want ErrNotPassed", err)
	}

	// Passed and bonded.
	if err := f.s.SetPass(f.genesis, f.genesis, f.pub, true, false, 2, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.addr); err != nil {
		t.Fatalf("passed and bonded must be eligible: %v", err)
	}
	if err := f.s.CanProduceKey(f.genesis, f.pub); err != nil {
		t.Fatalf("CanProduceKey must agree with CanProduce: %v", err)
	}

	// Bond drops below the floor: the pass survives, eligibility does not.
	if err := f.s.Unstake(f.addr, f.genesis, spld(2_000), 3, &types.Receipt{}); err != nil {
		t.Fatalf("unstake: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.addr); !errors.Is(err, ErrNotBonded) {
		t.Fatalf("under the floor = %v, want ErrNotBonded", err)
	}

	// Passed but never registered at all.
	otherPub, otherAddr := validatorKey(t, 0x55)
	if err := f.s.SetPass(f.genesis, f.genesis, otherPub, true, false, 4, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, otherAddr); !errors.Is(err, ErrNotBonded) {
		t.Fatalf("passed but unregistered = %v, want ErrNotBonded", err)
	}

	// The exemption is the documented bootstrap, and it is explicit.
	if err := f.s.SetPass(f.genesis, f.genesis, otherPub, true, true, 5, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass exempt: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, otherAddr); err != nil {
		t.Fatalf("a bond-exempt pass must produce: %v", err)
	}
}

// A jailed validator holds a pass and a bond above the floor, and still may
// not produce: the status gate is the point of jailing.
func TestJailedValidatorCannotProduce(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(100_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.SetPass(f.genesis, f.genesis, f.pub, true, false, 1, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.addr); err != nil {
		t.Fatalf("before the slash it must be eligible: %v", err)
	}

	// A slash jails whatever is left, so the bond stays far above the floor
	// and the STATUS is the only thing standing between this key and a block.
	if err := f.s.Slash(f.genesis, f.genesis, f.addr, spld(1_000), 2, &types.Receipt{}); err != nil {
		t.Fatalf("slash: %v", err)
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Status != ValidatorJailed {
		t.Fatalf("status = %s, want jailed", ValidatorStatusName(rec.Status))
	}
	if rec.Stake.Cmp(MinValidatorStake) < 0 {
		t.Fatalf("this case needs a bond above the floor, got %s", rec.Stake)
	}
	if err := f.s.AddStake(f.addr, spld(10_000), &types.Receipt{}); err == nil {
		t.Fatal("a jailed validator must not top up through add_stake")
	}
	if err := f.s.CanProduce(f.genesis, f.addr); !errors.Is(err, ErrNotBonded) {
		t.Fatalf("a jailed validator = %v, want ErrNotBonded", err)
	}
	// Returning is deliberate, and it works with the bond already there.
	if err := f.s.Unjail(f.addr, big.NewInt(0), &types.Receipt{}); err != nil {
		t.Fatalf("unjail: %v", err)
	}
	if err := f.s.CanProduce(f.genesis, f.addr); err != nil {
		t.Fatalf("after unjail: %v", err)
	}
}

func TestSetPassIsAdminOnly(t *testing.T) {
	f := newStakingFixture(t)
	pub, _ := validatorKey(t, 0x66)
	if err := f.s.SetPass(f.addr, f.genesis, pub, true, false, 1, &types.Receipt{}); err == nil {
		t.Fatal("a non-admin must not be able to grant a pass")
	}
	if n, _ := f.s.PassCount(); n != 0 {
		t.Errorf("a refused grant listed an entry: count = %d", n)
	}
}

func TestSetPassRefusesAnUnusableKey(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.s.SetPass(f.genesis, f.genesis, []byte("too short"), true, false, 1, &types.Receipt{}); err == nil {
		t.Fatal("a public key of the wrong size must be refused")
	}
	pub, _ := validatorKey(t, 0x77)
	if err := f.s.SetPass(f.genesis, f.genesis, pub, false, true, 1, &types.Receipt{}); err == nil {
		t.Fatal("a bond exemption on a revoked pass is contradictory and must be refused")
	}
}

// The authority can be handed on, and the old holder loses it in the same
// action — otherwise "transfer" would mean "share".
func TestSetAdminHandsOverAuthority(t *testing.T) {
	f := newStakingFixture(t)
	next := addr(0x5A)
	if err := f.s.SetAdmin(f.addr, f.genesis, next, &types.Receipt{}); err == nil {
		t.Fatal("a non-admin must not be able to hand over the authority")
	}
	if err := f.s.SetAdmin(f.genesis, f.genesis, next, &types.Receipt{}); err != nil {
		t.Fatalf("set_admin: %v", err)
	}
	got, err := f.s.Admin(f.genesis)
	if err != nil {
		t.Fatalf("Admin: %v", err)
	}
	if got != next {
		t.Fatalf("admin = %s, want %s", got, next)
	}
	pub, _ := validatorKey(t, 0x88)
	if err := f.s.SetPass(f.genesis, f.genesis, pub, true, false, 1, &types.Receipt{}); err == nil {
		t.Fatal("the previous admin must no longer be able to grant a pass")
	}
	if err := f.s.SetPass(next, f.genesis, pub, true, false, 1, &types.Receipt{}); err != nil {
		t.Fatalf("the new admin must be able to grant a pass: %v", err)
	}
}

func TestSetAdminRefusesAnUnusableHolder(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.s.SetAdmin(f.genesis, f.genesis, qcrypto.Address{}, &types.Receipt{}); err == nil {
		t.Fatal("the zero address must be refused as admin")
	}
	if err := f.s.SetAdmin(f.genesis, f.genesis, ModuleRouterAddress, &types.Receipt{}); err == nil {
		t.Fatal("the module router must be refused as admin")
	}
}

// The registry enumerates in insertion order and re-granting an existing key
// does not list it twice.
func TestPassesEnumerate(t *testing.T) {
	f := newStakingFixture(t)
	pubA, addrA := validatorKey(t, 0x91)
	pubB, addrB := validatorKey(t, 0x92)
	for _, p := range [][]byte{pubA, pubB, pubA} {
		if err := f.s.SetPass(f.genesis, f.genesis, p, true, false, 1, &types.Receipt{}); err != nil {
			t.Fatalf("set_pass: %v", err)
		}
	}
	if n, _ := f.s.PassCount(); n != 2 {
		t.Fatalf("pass count = %d, want 2", n)
	}
	got, err := f.s.Passes()
	if err != nil {
		t.Fatalf("Passes: %v", err)
	}
	if len(got) != 2 || got[0].Address != addrA || got[1].Address != addrB {
		t.Fatalf("Passes = %+v", got)
	}
	if !bytesEqual(got[0].PublicKey, pubA) {
		t.Error("the entry must carry the full public key it was granted to")
	}
}

// A failed action inside the module must leave no half-written state: the
// snapshot wrapper is what guarantees the escrow and the record move together.
func TestWithdrawIsAtomicOnAFailedPayout(t *testing.T) {
	f := newStakingFixture(t)
	if err := f.register(t, spld(5_000), 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.s.Unstake(f.addr, f.genesis, spld(1_000), 1, &types.Receipt{}); err != nil {
		t.Fatalf("unstake: %v", err)
	}
	// Drain the escrow behind the module's back, so the payout cannot settle.
	esc, err := f.sdb.GetAccount(StakingAddress)
	if err != nil {
		t.Fatalf("escrow account: %v", err)
	}
	esc.Balance = big.NewInt(0)
	f.sdb.SetAccount(esc)

	if err := f.s.Withdraw(f.addr, 1+UnbondingBlocks, &types.Receipt{}); err == nil {
		t.Fatal("a payout the escrow cannot cover must fail")
	}
	rec, _ := f.s.GetValidator(f.addr)
	if rec.Unbonding.Cmp(spld(1_000)) != 0 {
		t.Errorf("a failed withdraw cleared the unbonding amount: %s", rec.Unbonding)
	}
}

// Receipt logs roll back with the writes, or a failed action would advertise
// itself in the log stream.
func TestFailedActionLeavesNoLogs(t *testing.T) {
	f := newStakingFixture(t)
	r := &types.Receipt{}
	if err := f.s.Register(f.addr, f.pub, spld(1), 1, r); err == nil {
		t.Fatal("a sub-minimum register must fail")
	}
	if len(r.Logs) != 0 {
		t.Errorf("a failed register emitted %d logs", len(r.Logs))
	}
	if err := f.s.Register(f.addr, f.pub, spld(5_000), 1, r); err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(r.Logs) != 1 {
		t.Errorf("a successful register emitted %d logs, want 1", len(r.Logs))
	}
}

// The tier ladder is what ranks validators; the floor is what admits them.
func TestTierLadder(t *testing.T) {
	for _, tc := range []struct {
		stake *big.Int
		want  string
	}{
		{big.NewInt(0), "None"},
		{new(big.Int).Sub(StakeBronze, big.NewInt(1)), "None"},
		{StakeBronze, "Bronze"},
		{StakeSilver, "Silver"},
		{StakeGold, "Gold"},
		{StakePlatinum, "Platinum"},
	} {
		if got := GetTierName(tc.stake); got != tc.want {
			t.Errorf("GetTierName(%s) = %s, want %s", tc.stake, got, tc.want)
		}
	}
	if MinValidatorStake.Cmp(StakeBronze) != 0 {
		t.Error("the eligibility floor must be the Bronze tier")
	}
}

// The registry address must be reproducible offline by any client, so it is
// pinned here rather than left to whatever the derivation happens to produce.
func TestStakingAddressIsStable(t *testing.T) {
	want := qcrypto.Hash32([]byte("splendor_staking_contract_v1"))
	if StakingAddress != qcrypto.Address(want) {
		t.Fatalf("StakingAddress = %s", StakingAddress)
	}
}
