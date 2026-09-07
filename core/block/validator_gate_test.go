package block

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// gatedSchedule turns the producer gate on from block 1.
func gatedSchedule() forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1}
}

// produce seals an empty block at the given height with the given key.
func produce(ex *Executor, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, height uint64) (*types.Block, error) {
	return ex.ProduceBlock(nil, qcrypto.DeriveAddress(pub), pub.Bytes(), priv, [32]byte{}, height)
}

// bond registers `pub` with `amount` staked, funding it first, and commits, so
// producer and follower can be seeded to the same state.
func bond(t *testing.T, sdb *state.StateDB, pub *qcrypto.PublicKey, amount *big.Int) {
	t.Helper()
	addr := qcrypto.DeriveAddress(pub)
	acc, err := sdb.GetAccount(addr)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	acc.AddBalance(new(big.Int).Mul(amount, big.NewInt(2)))
	sdb.SetAccount(acc)
	if err := vm.Staking(sdb).Register(addr, pub.Bytes(), amount, 1, &types.Receipt{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// grantPass admits `pub`, with `genesisValidator` acting as the admin.
func grantPass(t *testing.T, sdb *state.StateDB, genesisValidator qcrypto.Address, pub *qcrypto.PublicKey, bondExempt bool) {
	t.Helper()
	if err := vm.Staking(sdb).SetPass(genesisValidator, genesisValidator, pub.Bytes(), true, bondExempt, 1, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass: %v", err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// Before the upgrade any key the chain accepts signatures from may produce.
// That is what single-validator Phase 1 has always been, and a replayed block
// must not be judged under a rule adopted after it was sealed.
func TestProducerGateIsInertBeforeTheUpgrade(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	pub, priv, _ := qcrypto.GenerateKeypair()
	other, _, _ := qcrypto.GenerateKeypair()
	ex.SetGenesisValidator(qcrypto.DeriveAddress(other))

	// No schedule at all.
	if _, err := produce(ex, pub, priv, 1); err != nil {
		t.Fatalf("an ungated chain must let any key produce: %v", err)
	}
	// Scheduled, but not yet active.
	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 10})
	if err := ex.EligibleToProduce(pub.Bytes(), 9); err != nil {
		t.Fatalf("one block before activation: %v", err)
	}
	if err := ex.EligibleToProduce(pub.Bytes(), 10); err == nil {
		t.Fatal("at the activation height an unadmitted key must be refused")
	}
}

// The genesis validator is admitted without anything being written: it has to
// seal block 1 before anything can be staked.
func TestGenesisValidatorProducesUnderTheGate(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	pub, priv, _ := qcrypto.GenerateKeypair()
	ex.SetForks(gatedSchedule())
	ex.SetGenesisValidator(qcrypto.DeriveAddress(pub))

	if err := ex.EligibleToProduce(pub.Bytes(), 1); err != nil {
		t.Fatalf("the genesis validator must be admitted: %v", err)
	}
	if _, err := produce(ex, pub, priv, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Anyone else is not.
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	err := ex.EligibleToProduce(otherPub.Bytes(), 2)
	if !errors.Is(err, vm.ErrNotPassed) {
		t.Fatalf("an unlisted key = %v, want ErrNotPassed", err)
	}
	if _, err := produce(ex, otherPub, otherPriv, 2); err == nil ||
		!strings.Contains(err.Error(), "refusing to produce") {
		t.Fatalf("ProduceBlock must refuse an unadmitted key, got %v", err)
	}
}

// Both gates, through the executor: a pass alone is not enough, and a bond
// alone is not either.
func TestProducerGateNeedsPassAndBond(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	genesisPub, _, _ := qcrypto.GenerateKeypair()
	genesisAddr := qcrypto.DeriveAddress(genesisPub)
	ex.SetForks(gatedSchedule())
	ex.SetGenesisValidator(genesisAddr)

	pub, priv, _ := qcrypto.GenerateKeypair()

	// Bonded, unpassed.
	bond(t, sdb, pub, vm.MinValidatorStake)
	if err := ex.EligibleToProduce(pub.Bytes(), 1); !errors.Is(err, vm.ErrNotPassed) {
		t.Fatalf("bonded but unpassed = %v, want ErrNotPassed", err)
	}

	// Passed and bonded.
	grantPass(t, sdb, genesisAddr, pub, false)
	if err := ex.EligibleToProduce(pub.Bytes(), 1); err != nil {
		t.Fatalf("passed and bonded must be admitted: %v", err)
	}
	if _, err := produce(ex, pub, priv, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Passed, unbonded.
	otherPub, _, _ := qcrypto.GenerateKeypair()
	grantPass(t, sdb, genesisAddr, otherPub, false)
	if err := ex.EligibleToProduce(otherPub.Bytes(), 2); !errors.Is(err, vm.ErrNotBonded) {
		t.Fatalf("passed but unbonded = %v, want ErrNotBonded", err)
	}
	// …until the pass carries the documented bond exemption.
	if err := vm.Staking(sdb).SetPass(genesisAddr, genesisAddr, otherPub.Bytes(), true, true, 1, &types.Receipt{}); err != nil {
		t.Fatalf("set_pass exempt: %v", err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := ex.EligibleToProduce(otherPub.Bytes(), 2); err != nil {
		t.Fatalf("a bond-exempt pass must be admitted: %v", err)
	}
}

// The other half of "one predicate": a follower refuses a block sealed by a
// key its own registry does not admit. Without this the gate would only ever
// constrain honest producers.
func TestFollowerRejectsABlockFromAnUnadmittedProducer(t *testing.T) {
	// The producer runs ungated, so it will happily seal the block.
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	pub, priv, _ := qcrypto.GenerateKeypair()
	b1, err := produce(exA, pub, priv, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// The follower is gated and anchored to a different genesis key.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	exB.SetForks(gatedSchedule())
	exB.SetGenesisValidator(qcrypto.DeriveAddress(genesisPub))

	err = exB.ExecuteBlock(b1)
	if err == nil {
		t.Fatal("a follower must reject a block from a key it does not admit")
	}
	if !strings.Contains(err.Error(), "does not admit") || !errors.Is(err, vm.ErrNotPassed) {
		t.Fatalf("rejection = %v, want the admission refusal", err)
	}
	if h, _ := exB.LatestHeight(); h != 0 {
		t.Fatalf("the rejected block was stored anyway: height %d", h)
	}

	// Admit that key and the same block applies.
	grantPass(t, sdbB, qcrypto.DeriveAddress(genesisPub), pub, true)
	// Seeding the follower's registry changed its state root, so re-seal the
	// block against the follower's state to compare like with like.
	b1b, err := produce(exA, pub, priv, 1)
	if err != nil {
		t.Fatalf("re-produce: %v", err)
	}
	b1b.Header.StateRoot = mustPendingRoot(t, sdbB)
	resealBlock(t, b1b, priv)
	if err := exB.ExecuteBlock(b1b); err != nil {
		t.Fatalf("an admitted key's block must apply: %v", err)
	}
}

func mustPendingRoot(t *testing.T, sdb *state.StateDB) [32]byte {
	t.Helper()
	root, err := sdb.PendingStateRoot()
	if err != nil {
		t.Fatalf("pending root: %v", err)
	}
	return root
}

func resealBlock(t *testing.T, b *types.Block, priv *qcrypto.PrivateKey) {
	t.Helper()
	h, err := b.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b.Hash = h
	if err := b.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// cmd/node refuses to start a gated chain whose genesis validator it cannot
// determine, so "no anchor" should never reach production. If it somehow does,
// the executor fails OPEN on an untouched registry rather than halting a chain
// on a rule nobody has configured — and the moment the registry IS configured,
// it enforces again.
func TestGateIsInertOnAnUntouchedRegistryWithNoAnchor(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(gatedSchedule())

	pub, priv, _ := qcrypto.GenerateKeypair()
	if err := ex.EligibleToProduce(pub.Bytes(), 1); err != nil {
		t.Fatalf("an untouched registry with no anchor must not halt the chain: %v", err)
	}
	if _, err := produce(ex, pub, priv, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Configure the registry through a node that DOES know the anchor, then
	// take the anchor away: the gate now has something to enforce, and does.
	adminPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, _, _ := qcrypto.GenerateKeypair()
	grantPass(t, sdb, qcrypto.DeriveAddress(adminPub), otherPub, true)
	ex.SetGenesisValidator(qcrypto.Address{})
	if err := ex.EligibleToProduce(pub.Bytes(), 2); !errors.Is(err, vm.ErrNotPassed) {
		t.Fatalf("once the registry is configured the gate must bite: %v", err)
	}
	if err := ex.EligibleToProduce(otherPub.Bytes(), 2); err != nil {
		t.Fatalf("the explicitly granted key must still be admitted: %v", err)
	}
}

// A chain that adopts the upgrade by extending its fork schedule arrives at
// the activation height with an untouched registry. Because the genesis
// validator is admitted implicitly, the single producer such a chain already
// runs keeps producing: adopting the upgrade cannot halt the chain on itself.
func TestForkExtensionDoesNotHaltAnUnconfiguredChain(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	pub, priv, _ := qcrypto.GenerateKeypair()
	ex.SetGenesisValidator(qcrypto.DeriveAddress(pub))

	// Runs ungated for a while…
	if _, err := produce(ex, pub, priv, 1); err != nil {
		t.Fatalf("produce before the extension: %v", err)
	}
	// …then the operator extends the schedule with the upgrade.
	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 2, forks.ValidatorGatesV1: 2})
	if init, err := vm.Staking(sdb).Initialised(); err != nil || init {
		t.Fatalf("the registry should still be untouched: %v, %v", init, err)
	}
	if err := ex.EligibleToProduce(pub.Bytes(), 2); err != nil {
		t.Fatalf("the chain's own producer must survive its own upgrade: %v", err)
	}
}

// Block 0 is the anchor. A chain that could not seal its own genesis could
// never start, so height 0 is never gated.
func TestGenesisBlockIsNotGated(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1})

	pub, priv, _ := qcrypto.GenerateKeypair()
	otherPub, _, _ := qcrypto.GenerateKeypair()
	ex.SetGenesisValidator(qcrypto.DeriveAddress(otherPub))

	root, _ := sdb.StateRoot()
	genesis := types.GenesisBlock(qcrypto.DeriveAddress(pub), root, 1)
	genesis.ValidatorPubKey = pub.Bytes()
	resealBlock(t, genesis, priv)
	if err := ex.ExecuteBlock(genesis); err != nil {
		t.Fatalf("block 0 must not be gated: %v", err)
	}
}

// The zero address is "unknown", not an address: storing it as one would make
// an unconfigured node enforce admissions against an authority nobody holds.
func TestSetGenesisValidatorZeroMeansUnknown(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	pub, _, _ := qcrypto.GenerateKeypair()
	addr := qcrypto.DeriveAddress(pub)
	ex.WithGenesisValidator(addr)
	if got := ex.GenesisValidator(); got != addr {
		t.Fatalf("GenesisValidator = %s, want %s", got, addr)
	}
	ex.SetGenesisValidator(qcrypto.Address{})
	if got := ex.GenesisValidator(); !got.IsZero() {
		t.Fatalf("GenesisValidator = %s, want the zero address", got)
	}
}

// The anchor has to reach module calls, or set_pass on a fresh chain would
// have no admin to authorise it.
func TestGenesisValidatorReachesModuleCalls(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(gatedSchedule())

	adminPub, adminPriv, _ := qcrypto.GenerateKeypair()
	adminAddr := qcrypto.DeriveAddress(adminPub)
	ex.SetGenesisValidator(adminAddr)
	fund(t, sdb, adminAddr, 10_000_000)

	targetPub, _, _ := qcrypto.GenerateKeypair()
	yes := true
	params := map[string]interface{}{"pubkey": targetPub.Bytes(), "passed": yes, "bond_exempt": true}
	call := types.NewCall(adminAddr, vm.ModuleRouterAddress,
		envelope(t, "staking", "set_pass", params),
		big.NewInt(0), big.NewInt(testFee), 0, 1)
	if err := call.Sign(adminPriv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}

	b1, err := ex.ProduceBlock([]*types.Transaction{call}, adminAddr, adminPub.Bytes(), adminPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatalf("the set_pass call was dropped — the admin was not recognised")
	}
	if err := ex.EligibleToProduce(targetPub.Bytes(), 2); err != nil {
		t.Fatalf("the granted key must now be admitted: %v", err)
	}
}
