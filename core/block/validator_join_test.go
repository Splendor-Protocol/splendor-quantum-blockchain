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
