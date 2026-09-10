package consensus

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestSameKeyCannotVoteAThenBAcrossRounds(t *testing.T) {
	set, keys := testSet(t, 2)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	eng := engineFor(t, set, keys, 0, BFTConfig{VoteJournal: j})
	blockA := proposalAt(t, set, keys, 5, 0, 1).Block
	blockB := proposalAt(t, set, keys, 5, 1, 2).Block
	if _, err := eng.castVote(blockA, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.castVote(blockB, 1); !errors.Is(err, ErrDoubleSign) {
		t.Fatalf("signed B at (5,1) after A at (5,0): %v", err)
	}
	if _, err := eng.castVote(blockA, 1); err != nil {
		t.Fatalf("re-sign A at (5,1): %v", err)
	}
}

func TestTwoValidatorsCannotCertifyTwoBlocksAtOneHeight(t *testing.T) {
	set, keys := testSet(t, 2)
	engines := make([]*BFTEngine, 2)
	for i := 0; i < 2; i++ {
		j, err := OpenVoteJournal(filepath.Join(t.TempDir(), fmt.Sprintf("v%d.json", i)))
		if err != nil {
			t.Fatal(err)
		}
		engines[i] = engineFor(t, set, keys, i, BFTConfig{VoteJournal: j})
	}
	propA := proposalAt(t, set, keys, 5, 0, 1)
	propB := proposalAt(t, set, keys, 5, 1, 2)
	votesA := make([]*Vote, 2)
	for i, eng := range engines {
		var err error
		if votesA[i], err = eng.castVote(propA.Block, 0); err != nil {
			t.Fatalf("v%d vote A: %v", i, err)
		}
		if _, err = eng.castVote(propB.Block, 1); !errors.Is(err, ErrDoubleSign) {
			t.Fatalf("v%d signed B after A: %v", i, err)
		}
	}
	if err := certificateAccepted(certify(propA.Block, votesA), set); err != nil {
		t.Fatalf("certificate for A: %v", err)
	}
}

func TestTimeoutDoesNotFlipTheVotedBlock(t *testing.T) {
	set, keys := testSet(t, 2)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "vote-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	eng := engineFor(t, set, keys, 1, BFTConfig{VoteJournal: j})
	eng.SetHeight(5)
	first := proposalAt(t, set, keys, 5, 0, 1)
	eng.handleProposal(&pendingProposal{block: first.Block, vote: first.Vote, local: true})
	eng.handleTimeout()
	second := proposalAt(t, set, keys, 5, 1, 2)
	eng.handleProposal(pendingProposalOf(second))
	_, jr, jhash, ok := j.Last()
	if !ok || jr != 0 || jhash != first.Block.Hash {
		t.Fatalf("journal after timeout+B = round %d hash %x, want A", jr, jhash[:6])
	}
}

func certify(b *types.Block, votes []*Vote) *types.Block {
	qcv := make([]types.QCVote, len(votes))
	for i, v := range votes {
		qcv[i] = types.QCVote{
			AlgID: v.AlgID, Height: v.Height, Round: v.Round,
			BlockHash: v.BlockHash, ValidatorAddr: v.ValidatorAddr,
			Signature: v.Signature, Timestamp: v.Timestamp,
		}
	}
	b.Votes = qcv
	root := types.ComputeQCRoot(qcv)
	b.Header.QCRoot = root[:]
	return b
}

func certificateAccepted(b *types.Block, set []ValidatorInfo) error {
	if err := b.Validate(); err != nil {
		return err
	}
	need := (len(set)*2)/3 + 1
	if len(b.Votes) < need {
		return fmt.Errorf("qc has %d votes, need %d", len(b.Votes), need)
	}
	byAddr := make(map[qcrypto.Address][]byte, len(set))
	for _, v := range set {
		byAddr[v.Address] = v.PublicKey
	}
	seen := make(map[qcrypto.Address]struct{})
	for _, v := range b.Votes {
		pk, ok := byAddr[v.ValidatorAddr]
		if !ok {
			return fmt.Errorf("unknown voter %s", v.ValidatorAddr)
		}
		if _, dup := seen[v.ValidatorAddr]; dup {
			return fmt.Errorf("duplicate vote from %s", v.ValidatorAddr)
		}
		seen[v.ValidatorAddr] = struct{}{}
		if v.Height != b.Header.Height || v.BlockHash != b.Hash {
			return fmt.Errorf("vote from %s is for a different height/hash", v.ValidatorAddr)
		}
		if err := v.Verify(pk); err != nil {
			return err
		}
	}
	return nil
}
