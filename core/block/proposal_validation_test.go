package block

import (
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ValidateProposal is what a BFT validator runs before it votes. It must
// refuse everything ExecuteBlock would refuse — except the missing certificate
// — and must leave no trace in state whether it accepts or refuses.

func TestValidateProposalAcceptsAnApplicableProposalAndLeavesNoTrace(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdb, v1)
	rootBefore, _ := sdb.StateRoot()

	b, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if err := ex.ValidateProposal(b); err != nil {
		t.Fatalf("an applicable proposal without a certificate was refused: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("validating a proposal advanced the head to %d", h)
	}
	if root, _ := sdb.StateRoot(); root != rootBefore {
		t.Fatal("validating a proposal changed committed state")
	}
	// The pending layer is empty again: the same block still applies for real.
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]
	if err := ex.ExecuteBlock(b); err != nil {
		t.Fatalf("the block did not apply after being validated: %v", err)
	}
}

func TestValidateProposalRefusesWrongParentAndStateRoot(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdb, v1)

	// Wrong parent: names a prev_hash on an empty chain at height 2.
	b, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{7}, 2)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if err := ex.ValidateProposal(b); err == nil || !strings.Contains(err.Error(), "linkage") {
		t.Fatalf("a proposal that does not link to the head was accepted: %v", err)
	}

	// Wrong state root: a well-linked block whose header lies about the root.
	good, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	good.Header.StateRoot[0] ^= 0xff
	h, _ := good.ComputeHash()
	good.Hash = h
	if err := good.SignBlock(priv1); err != nil {
		t.Fatal(err)
	}
	if err := ex.ValidateProposal(good); err == nil || !strings.Contains(err.Error(), "state root") {
		t.Fatalf("a proposal with a false state root was accepted: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("head moved to %d on refused proposals", h)
	}
}

// Below two registered validators an unregistered producer is accepted only
// when it IS the genesis validator. The key the block carries is not enough
// once the node knows who sealed block 0.
func TestUnregisteredProducerMustBeTheGenesisValidatorWhenKnown(t *testing.T) {
	genesis, genesisPriv, _ := qcrypto.GenerateKeypair()
	stranger, strangerPriv, _ := qcrypto.GenerateKeypair()

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	ex.WithGenesisValidatorKey(func() []byte { return genesis.Bytes() })

	// A stranger with a real signature and its own key in the block: refused.
	bad, err := ex.BuildProposal(nil, qcrypto.DeriveAddress(stranger), stranger.Bytes(), strangerPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if err := ex.ExecuteBlock(bad); err == nil || !strings.Contains(err.Error(), "genesis validator") {
		t.Fatalf("an unregistered stranger was accepted as producer: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a refused block", h)
	}

	// The genesis validator itself, unregistered: accepted, and on an
	// unexported ValidatorPubKey too — the executor uses the key it knows.
	ok, err := ex.BuildProposal(nil, qcrypto.DeriveAddress(genesis), nil, genesisPriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if err := ex.ExecuteBlock(ok); err != nil {
		t.Fatalf("the genesis validator was refused: %v", err)
	}
}

// A one-validator chain carries one-vote certificates. They used to pass
// unread; a certificate that is present must verify whatever the set size.
func TestPresentCertificateIsVerifiedEvenBelowTwoValidators(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	stranger, strangerPriv, _ := qcrypto.GenerateKeypair()

	build := func() (*Executor, qcrypto.Address, *types.Block) {
		ex, sdb := newTestExecutor(t)
		t.Cleanup(func() { ex.Close(); sdb.Close() })
		ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
		addr1 := registerValidator(t, sdb, v1)
		b, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
		if err != nil {
			t.Fatalf("proposal: %v", err)
		}
		return ex, addr1, b
	}

	// 1. Vote from a key outside the set, qc_root consistent with it.
	ex, _, b := build()
	b.Votes = []types.QCVote{signVote(t, b, qcrypto.DeriveAddress(stranger), strangerPriv)}
	r := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = r[:]
	if err := ex.ExecuteBlock(b); err == nil || !strings.Contains(err.Error(), "unknown validator") {
		t.Fatalf("a one-vote certificate from a stranger was accepted: %v", err)
	}

	// 2. Genuine vote, qc_root that does not cover it.
	ex, addr1, b := build()
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	b.Header.QCRoot = []byte{1, 2, 3}
	if err := ex.ExecuteBlock(b); err == nil || !strings.Contains(err.Error(), "qc_root") {
		t.Fatalf("a qc_root that does not cover the votes was accepted: %v", err)
	}

	// 3. Votes without a qc_root at all.
	ex, addr1, b = build()
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	if err := ex.ExecuteBlock(b); err == nil || !strings.Contains(err.Error(), "quorum certificate") {
		t.Fatalf("votes without a qc_root were accepted: %v", err)
	}

	// 4. The genuine article still applies.
	ex, addr1, b = build()
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	r = types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = r[:]
	if err := ex.ExecuteBlock(b); err != nil {
		t.Fatalf("a genuine one-vote certificate was refused: %v", err)
	}
}
