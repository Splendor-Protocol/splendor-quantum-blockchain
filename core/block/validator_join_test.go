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

// registerValidator funds addr with the bond and registers it as Active in
// committed state, exactly as a validators.register tx would leave it.
func registerValidator(t *testing.T, sdb *state.StateDB, pub *qcrypto.PublicKey) qcrypto.Address {
	t.Helper()
	addr := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(addr)
	acc.AddBalance(new(big.Int).Add(vm.ValidatorBond(), big.NewInt(1_000_000)))
	sdb.SetAccount(acc)
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(nil))
	if err := reg.Register(addr, pub.Bytes(), 0); err != nil {
		t.Fatalf("register %s: %v", addr, err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit registry: %v", err)
	}
	return addr
}

// The mainnet-5786 failure, made visible: a single-path producer on a chain
// where multi_validator is active and two keys are registered must refuse to
// seal a QC-less block rather than fork itself away from every follower.
func TestSinglePathRefusesWhenQuorumRequired(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, _, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	registerValidator(t, sdb, v2)
	rootBefore, _ := sdb.StateRoot()

	_, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if !errors.Is(err, ErrQuorumRequired) {
		t.Fatalf("ProduceBlock with 2 active under multi_validator = %v, want ErrQuorumRequired", err)
	}
	if !strings.Contains(err.Error(), "BFT") {
		t.Fatalf("error should tell the operator to run BFT: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a refused block", h)
	}
	if root, _ := sdb.StateRoot(); root != rootBefore {
		t.Fatal("state changed on a refused block")
	}
}

// With fewer than two active validators no certificate is demanded of anyone,
// so the single path stays usable (this is every chain before its second
// register lands).
func TestSinglePathStillProducesWithFewerThanTwoActive(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v1, priv1, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	if _, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1); err != nil {
		t.Fatalf("one active validator must still produce on the single path: %v", err)
	}
}

// A chain whose schedule never activates multi_validator is untouched by the
// guard, however many keys are registered (devnet today).
func TestSinglePathIgnoresRegistryWhenForkInactive(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, _, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	registerValidator(t, sdb, v2)
	if _, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1); err != nil {
		t.Fatalf("fork inactive: single path must produce: %v", err)
	}
}

// The hole a fresh node hits on a mainnet-shaped chain: the block after the
// second register carries no certificate and can never be applied.
func TestExecuteBlockRejectsQCLessBlockWithTwoActive(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, _, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t) // no schedule: seals a QC-less block
	defer producer.Close()
	defer sdbA.Close()
	addr1 := registerValidator(t, sdbA, v1)
	registerValidator(t, sdbA, v2)
	b, err := producer.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	registerValidator(t, sdbB, v1)
	registerValidator(t, sdbB, v2)
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	err = follower.ExecuteBlock(b)
	if err == nil || !strings.Contains(err.Error(), "quorum certificate") {
		t.Fatalf("QC-less block with 2 active accepted: %v", err)
	}
	if h, _ := follower.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a rejected block", h)
	}
}

// signVote produces the QC vote a BFT engine attaches on commit.
func signVote(t *testing.T, b *types.Block, addr qcrypto.Address, priv *qcrypto.PrivateKey) types.QCVote {
	t.Helper()
	v := types.QCVote{
		AlgID: qcrypto.AlgMLDSA65, Height: b.Header.Height, BlockHash: b.Hash,
		ValidatorAddr: addr, Timestamp: 1,
	}
	h, err := v.Hash()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := qcrypto.Sign(priv, h[:], qcrypto.CtxVote)
	if err != nil {
		t.Fatal(err)
	}
	v.Signature = sig
	return v
}

// The block a BFT producer with a one-key set self-commits: one vote, a
// qc_root, and a follower with fewer than two active validators applies it.
func TestExecuteBlockAcceptsOneVoteQCFromSoleValidator(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t)
	defer producer.Close()
	defer sdbA.Close()
	producer.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdbA, v1)
	b, err := producer.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbB, v1)
	if err := follower.ExecuteBlock(b); err != nil {
		t.Fatalf("one-vote block from the sole validator rejected: %v", err)
	}
	if h, _ := follower.LatestHeight(); h != 1 {
		t.Fatalf("head = %d, want 1", h)
	}
}

// The resolver the network layer authenticates non-genesis producers with:
// only an Active registry entry yields a key.
func TestActiveValidatorKeyResolvesOnlyActiveMembers(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	v1, _, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	if got := ex.ActiveValidatorKey(addr1); string(got) != string(v1.Bytes()) {
		t.Fatal("active validator key not resolved")
	}
	stranger, _, _ := qcrypto.GenerateKeypair()
	if got := ex.ActiveValidatorKey(qcrypto.DeriveAddress(stranger)); got != nil {
		t.Fatal("unregistered address resolved to a key")
	}
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(nil))
	if err := reg.Unregister(addr1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := ex.ActiveValidatorKey(addr1); got != nil {
		t.Fatal("an exiting validator resolved to a key")
	}
}

// Once a quorum is required, the producer must be IN the active set.
//
// The gate used to fall back to the key the block carries when the header's
// producer was not registered — and a block always verifies against its own
// embedded key, so any producer with a quorum of real votes was admitted.
// Below two active validators that fallback is still needed (the genesis
// validator is typically not registered, and the block that registers the
// first one is sealed by a producer the parent state does not list), so the
// two cases are checked separately here.
func TestExecuteBlockRejectsProducerOutsideTheActiveSet(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, priv2, _ := qcrypto.GenerateKeypair()
	stranger, strangerPriv, _ := qcrypto.GenerateKeypair()
	strangerAddr := qcrypto.DeriveAddress(stranger)

	// A block sealed by a key nobody registered, but carrying a full quorum of
	// genuine votes from the two validators that ARE registered.
	producer, sdbA := newTestExecutor(t)
	defer producer.Close()
	defer sdbA.Close()
	producer.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbA, stranger) // funds it; the follower will not have it
	b, err := producer.BuildProposal(nil, strangerAddr, stranger.Bytes(), strangerPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{
		signVote(t, b, qcrypto.DeriveAddress(v1), priv1),
		signVote(t, b, qcrypto.DeriveAddress(v2), priv2),
	}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbB, v1)
	registerValidator(t, sdbB, v2)

	err = follower.ExecuteBlock(b)
	if err == nil || !strings.Contains(err.Error(), "not in the active validator set") {
		t.Fatalf("a block from an unregistered producer with a real quorum was accepted: %v", err)
	}

	// With a single active validator the fallback still applies: this is the
	// pre-registry shape every chain starts in.
	solo, sdbC := newTestExecutor(t)
	defer solo.Close()
	defer sdbC.Close()
	solo.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbC, v1)
	soloBlock, err := solo.BuildProposal(nil, strangerAddr, stranger.Bytes(), strangerPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if err := solo.ExecuteBlock(soloBlock); err != nil {
		t.Fatalf("below a quorum an unregistered producer must still be followed: %v", err)
	}
}

// Every vote in a certificate must name the height of the block it certifies.
func TestExecuteBlockRejectsQCVoteForAnotherHeight(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, priv2, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t)
	defer producer.Close()
	defer sdbA.Close()
	producer.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdbA, v1)
	registerValidator(t, sdbA, v2)
	b, err := producer.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	good := signVote(t, b, addr1, priv1)
	bad := signVote(t, b, qcrypto.DeriveAddress(v2), priv2)
	bad.Height = 99
	h, herr := bad.Hash()
	if herr != nil {
		t.Fatal(herr)
	}
	sig, serr := qcrypto.Sign(priv2, h[:], qcrypto.CtxVote)
	if serr != nil {
		t.Fatal(serr)
	}
	bad.Signature = sig // properly signed, just about another height
	b.Votes = []types.QCVote{good, bad}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbB, v1)
	registerValidator(t, sdbB, v2)
	if err := follower.ExecuteBlock(b); err == nil || !strings.Contains(err.Error(), "height") {
		t.Fatalf("a certificate containing a vote about another height was accepted: %v", err)
	}
}

// Under validators_admission_v1 the genesis validator is the only producer
// below two members, and a follower enforces it: a block sealed by a lone
// registrant that is not the genesis validator is refused even though the
// registry lists it, while the unregistered genesis validator's block is
// followed. Without the fork the lone registrant is still accepted, so
// history sealed before activation replays unchanged.
func TestAdmissionForkRefusesLoneRegistrantBelowTwo(t *testing.T) {
	genesis, genesisPriv, _ := qcrypto.GenerateKeypair()
	genesisAddr := qcrypto.DeriveAddress(genesis)
	stranger, strangerPriv, _ := qcrypto.GenerateKeypair()
	strangerAddr := qcrypto.DeriveAddress(stranger)
	withFork := forks.Schedule{forks.MultiValidator: 1, forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1, forks.ValidatorsAdmissionV1: 1}
	preFork := forks.Schedule{forks.MultiValidator: 1, forks.ModuleRevertV1: 1, forks.ValidatorsV1: 1}
	genesisKey := func() []byte { return genesis.Bytes() }

	// The stranger's own node: it registered itself (n=1) and seals a block
	// on the old rule, where a lone registrant believes it leads.
	attacker, sdbA := newTestExecutor(t)
	defer attacker.Close()
	defer sdbA.Close()
	attacker.SetForks(preFork)
	registerValidator(t, sdbA, stranger)
	strangerBlock, err := attacker.BuildProposal(nil, strangerAddr, stranger.Bytes(), strangerPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("stranger proposal: %v", err)
	}

	// A follower on the same state with the admission fork in force and the
	// genesis key pinned refuses it.
	follower, sdbF := newTestExecutor(t)
	defer follower.Close()
	defer sdbF.Close()
	follower.SetForks(withFork)
	follower.WithGenesisValidatorKey(genesisKey)
	registerValidator(t, sdbF, stranger)
	err = follower.ExecuteBlock(strangerBlock)
	if err == nil || !strings.Contains(err.Error(), "nor the genesis validator") {
		t.Fatalf("a lone non-genesis registrant's block was followed under validators_admission_v1: %v", err)
	}

	// The unregistered genesis validator's block on the same state is followed.
	producer, sdbP := newTestExecutor(t)
	defer producer.Close()
	defer sdbP.Close()
	producer.SetForks(withFork)
	producer.WithGenesisValidatorKey(genesisKey)
	registerValidator(t, sdbP, stranger)
	genesisBlock, err := producer.BuildProposal(nil, genesisAddr, genesis.Bytes(), genesisPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("genesis proposal: %v", err)
	}
	if err := follower.ExecuteBlock(genesisBlock); err != nil {
		t.Fatalf("the genesis validator's block must be followed below two members: %v", err)
	}

	// Without the fork the old rule stands, so already-sealed history replays.
	legacy, sdbL := newTestExecutor(t)
	defer legacy.Close()
	defer sdbL.Close()
	legacy.SetForks(preFork)
	legacy.WithGenesisValidatorKey(genesisKey)
	registerValidator(t, sdbL, stranger)
	if err := legacy.ExecuteBlock(strangerBlock); err != nil {
		t.Fatalf("below the fork a listed lone registrant must still be followed: %v", err)
	}
}
