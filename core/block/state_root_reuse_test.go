package block

import (
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

type rootNode struct {
	ex  *Executor
	sdb *state.StateDB
}

// newRootNode returns an executor over the state every node starts from here:
// one registered validator and one funded sender.
func newRootNode(t *testing.T, vpub *qcrypto.PublicKey, sender qcrypto.Address) rootNode {
	t.Helper()
	ex, sdb := newTestExecutor(t)
	t.Cleanup(func() {
		ex.Close()
		sdb.Close()
	})
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdb, vpub)
	fund(t, sdb, sender, 1_000_000)
	return rootNode{ex, sdb}
}

func hashedRoots(n rootNode) uint64 { return n.sdb.RootStats().Computed }

// withQC attaches the validator's vote, as a committed block carries it.
func withQC(t *testing.T, b *types.Block, addr qcrypto.Address, priv *qcrypto.PrivateKey) {
	t.Helper()
	b.Votes = []types.QCVote{signVote(t, b, addr, priv)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]
}

// A node hashes a block's state once however many times it executes the
// block: the leader builds then applies, a follower validates — twice here,
// as after a round change — then applies. An empty block hashes nothing.
func TestBlockStateHashedOncePerNode(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	spub, spriv, _ := qcrypto.GenerateKeypair()
	sender := qcrypto.DeriveAddress(spub)
	leader := newRootNode(t, vpub, sender)
	follower := newRootNode(t, vpub, sender)

	// Block 1 pays a fresh address, an insert like the testnet heartbeat's.
	l0, f0 := hashedRoots(leader), hashedRoots(follower)
	tx := benchTransfer(t, sender, spriv, testAddr(0x77), 5, 0)
	b1, err := leader.ex.BuildProposal([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := follower.ex.ValidateProposal(b1); err != nil {
			t.Fatalf("validate #%d: %v", i+1, err)
		}
	}
	withQC(t, b1, vaddr, vpriv)
	if err := leader.ex.ExecuteBlock(b1); err != nil {
		t.Fatalf("leader apply: %v", err)
	}
	if err := follower.ex.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower apply: %v", err)
	}
	if n := hashedRoots(leader) - l0; n != 1 {
		t.Fatalf("the leader hashed block 1's state %d times, want 1", n)
	}
	if n := hashedRoots(follower) - f0; n != 1 {
		t.Fatalf("the follower hashed block 1's state %d times, want 1", n)
	}
	lr, _ := leader.sdb.StateRoot()
	fr, _ := follower.sdb.StateRoot()
	if lr != b1.Header.StateRoot || fr != b1.Header.StateRoot {
		t.Fatalf("roots after block 1: leader %x follower %x header %x", lr[:8], fr[:8], b1.Header.StateRoot[:8])
	}

	l1, f1 := hashedRoots(leader), hashedRoots(follower)
	b2, err := leader.ex.BuildProposal(nil, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.ex.ValidateProposal(b2); err != nil {
		t.Fatalf("validate empty block: %v", err)
	}
	withQC(t, b2, vaddr, vpriv)
	if err := leader.ex.ExecuteBlock(b2); err != nil {
		t.Fatalf("leader apply empty block: %v", err)
	}
	if err := follower.ex.ExecuteBlock(b2); err != nil {
		t.Fatalf("follower apply empty block: %v", err)
	}
	if n := hashedRoots(leader) - l1; n != 0 {
		t.Fatalf("the leader hashed an empty block's state %d times", n)
	}
	if n := hashedRoots(follower) - f1; n != 0 {
		t.Fatalf("the follower hashed an empty block's state %d times", n)
	}
}

// A forged proposal whose changes match a block this node already hashed must
// still fail its root check. The memo answers "what root do these changes
// give", never "is this header's root right".
func TestForgedRootRefusedWhenItsChangesAreRemembered(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	spub, spriv, _ := qcrypto.GenerateKeypair()
	sender := qcrypto.DeriveAddress(spub)
	leader := newRootNode(t, vpub, sender)
	follower := newRootNode(t, vpub, sender)
	before, _ := follower.sdb.StateRoot()

	tx := benchTransfer(t, sender, spriv, testAddr(0x77), 5, 0)
	honest, err := leader.ex.BuildProposal([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.ex.ValidateProposal(honest); err != nil {
		t.Fatalf("honest proposal refused: %v", err)
	}

	forged := *honest
	forged.Header.StateRoot[0] ^= 0xff
	h, err := forged.ComputeHash()
	if err != nil {
		t.Fatal(err)
	}
	forged.Hash = h
	if err := forged.SignBlock(vpriv); err != nil {
		t.Fatal(err)
	}
	if err := follower.ex.ValidateProposal(&forged); err == nil || !strings.Contains(err.Error(), "state root") {
		t.Fatalf("a forged root over remembered changes was accepted at validation: %v", err)
	}
	withQC(t, &forged, vaddr, vpriv)
	if err := follower.ex.ExecuteBlock(&forged); err == nil || !strings.Contains(err.Error(), "state root") {
		t.Fatalf("a forged root over remembered changes was applied: %v", err)
	}
	if got, _ := follower.sdb.StateRoot(); got != before {
		t.Fatal("a refused forged block moved the committed root")
	}

	withQC(t, honest, vaddr, vpriv)
	if err := follower.ex.ExecuteBlock(honest); err != nil {
		t.Fatalf("the honest block no longer applies: %v", err)
	}
	if got, _ := follower.sdb.StateRoot(); got != honest.Header.StateRoot {
		t.Fatalf("root after the honest block %x, header %x", got[:8], honest.Header.StateRoot[:8])
	}
}
