package consensus

import (
	"testing"
	"time"
)

func TestJunkVotesDoNotFillVoteCh(t *testing.T) {
	set, keys := testSet(t, 3)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)

	for i := 0; i < 500; i++ {
		eng.SubmitVote(&Vote{
			Height: 1, Round: 0, BlockHash: [32]byte{1},
			ValidatorAddr: set[1].Address, Timestamp: int64(i),
			// no signature
		})
	}
	unknown := [32]byte{0xEE}
	var unknownAddr [32]byte
	copy(unknownAddr[:], unknown[:])
	for i := 0; i < 500; i++ {
		eng.SubmitVote(&Vote{
			Height: 1, Round: 0, BlockHash: [32]byte{2},
			ValidatorAddr: unknownAddr, Timestamp: int64(i),
		})
	}
	if n := eng.voteQueueLen(); n != 0 {
		t.Fatalf("junk votes occupied %d voteCh slots", n)
	}

	good := &Vote{
		Height: 1, Round: 0, BlockHash: [32]byte{3},
		ValidatorAddr: set[1].Address, Timestamp: 1,
	}
	if err := good.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	eng.SubmitVote(good)
	if n := eng.voteQueueLen(); n != 1 {
		t.Fatalf("authenticated vote queue len = %d, want 1", n)
	}
}

func TestJunkRoundChangeDoesNotFillQueue(t *testing.T) {
	set, keys := testSet(t, 3)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	for i := 0; i < 300; i++ {
		eng.SubmitRoundChange(&RoundChange{
			Height: 1, Round: 1, ValidatorAddr: set[1].Address, Timestamp: int64(i),
		})
	}
	if n := len(eng.rcCh); n != 0 {
		t.Fatalf("junk round-changes occupied %d rcCh slots", n)
	}
}
