package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Four validators with identical history must agree on genesis, including
// joiners that have block 1 but never stored block 0.
func TestSyncedValidatorsRejectRelabelledGenesis(t *testing.T) {
	pubs := make([]*qcrypto.PublicKey, 4)
	privs := make([]*qcrypto.PrivateKey, 4)
	for i := range pubs {
		var err error
		pubs[i], privs[i], err = qcrypto.GenerateKeypair()
		if err != nil {
			t.Fatal(err)
		}
	}
	nodes := make([]*Executor, 4)
	states := make([]*state.StateDB, 4)
	for i := range nodes {
		ex, sdb := newTestExecutor(t)
		t.Cleanup(func() { _ = ex.Close(); _ = sdb.Close() })
		nodes[i], states[i] = ex, sdb
		for _, pub := range pubs {
			registerValidator(t, sdb, pub)
		}
		ex.SetForks(forks.Schedule{forks.MultiValidator: 1, forks.BFTRoundsV1: 1, forks.GenesisHashV1: 2})
		ex.WithGenesisValidatorKey(func() []byte { return pubs[0].Bytes() })
	}
	g := mintGenesis(t, states[0], pubs[0], privs[0])
	if err := nodes[0].ExecuteBlock(g); err != nil {
		t.Fatal(err)
	}
	certify := func(b *types.Block, signers ...int) {
		b.Votes = nil
		for _, i := range signers {
			v := types.QCVote{
				AlgID: qcrypto.AlgMLDSA65, Height: b.Header.Height, BlockHash: b.Hash,
				ValidatorAddr: qcrypto.DeriveAddress(pubs[i]), Timestamp: 1,
				Kind: types.VoteKindPrecommit,
			}
			h, err := v.Hash()
			if err != nil {
				t.Fatal(err)
			}
			v.Signature, err = qcrypto.Sign(privs[i], h[:], qcrypto.CtxVote)
			if err != nil {
				t.Fatal(err)
			}
			b.Votes = append(b.Votes, v)
		}
		root := types.ComputeQCRoot(b.Votes)
		b.Header.QCRoot = root[:]
	}
	b1, err := nodes[0].BuildProposal(nil, qcrypto.DeriveAddress(pubs[0]), pubs[0].Bytes(), privs[0], g.Hash, 1)
	if err != nil {
		t.Fatal(err)
	}
	certify(b1, 0, 1, 2)
	for _, ex := range nodes {
		if err := ex.ExecuteBlock(b1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < 4; i++ {
		if got, known := nodes[i].GenesisHash(); !known || got != g.Hash {
			t.Fatal("replaying block 1 did not establish the real genesis")
		}
		_, err := nodes[i].BuildProposal(nil, qcrypto.DeriveAddress(pubs[i]), pubs[i].Bytes(), privs[i], b1.Hash, 2)
		if err != nil {
			t.Fatalf("synced joiner cannot propose at activation: %v", err)
		}
	}
	good, err := nodes[0].BuildProposal(nil, qcrypto.DeriveAddress(pubs[1]), pubs[1].Bytes(), privs[1], b1.Hash, 2)
	if err != nil {
		t.Fatal(err)
	}
	foreign := reseal(t, good, privs[1], func(h *types.BlockHeader) { h.GenesisHash = testHash(0xcc) })
	for i, ex := range nodes {
		if err := ex.ValidateProposal(foreign); err == nil {
			t.Fatalf("validator %d would vote for an invented genesis", i)
		}
	}
	// Even a correctly signed quorum certificate cannot override identity.
	certify(foreign, 1, 2, 3)
	for i, ex := range nodes {
		if err := ex.ExecuteBlock(foreign); err == nil {
			t.Fatalf("validator %d committed an invented genesis", i)
		}
		if head, _ := ex.LatestBlock(); head == nil || head.Hash != b1.Hash {
			t.Fatalf("validator %d changed its head on rejection", i)
		}
	}
	certify(good, 0, 1, 2)
	for i, ex := range nodes {
		if err := ex.ExecuteBlock(good); err != nil {
			t.Fatalf("validator %d refused the real genesis: %v", i, err)
		}
	}
}
