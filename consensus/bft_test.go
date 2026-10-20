package consensus

import (
	"context"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func testKey(t *testing.T) (*qcrypto.PrivateKey, []byte, qcrypto.Address) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub.Bytes(), qcrypto.DeriveAddress(pub)
}

func testSet(t *testing.T, n int) ([]ValidatorInfo, []*qcrypto.PrivateKey) {
	t.Helper()
	infos := make([]ValidatorInfo, n)
	keys := make([]*qcrypto.PrivateKey, n)
	for i := 0; i < n; i++ {
		priv, pk, addr := testKey(t)
		keys[i] = priv
		infos[i] = ValidatorInfo{Address: addr, PublicKey: pk, IsActive: true}
	}
	return infos, keys
}

func TestLeaderRotatesWithRound(t *testing.T) {
	set, keys := testSet(t, 3)
	eng, err := NewBFTEngine(BFTConfig{LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0]})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	a, _ := eng.LeaderAddress(10, 0)
	b, _ := eng.LeaderAddress(10, 1)
	c, _ := eng.LeaderAddress(10, 2)
	if a == b || b == c || a == c {
		t.Fatalf("leaders should rotate: %s %s %s", a, b, c)
	}
	if !eng.IsLeader(10, 0) && set[0].Address == a {
		t.Fatal("local node should be leader when it is scheduled")
	}
}

func TestRoundTimeoutViewChange(t *testing.T) {
	set, keys := testSet(t, 3)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	eng.SetHeight(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if eng.Round() >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("round did not advance on timeout, still %d", eng.Round())
}

func TestEquivocationDropped(t *testing.T) {
	set, keys := testSet(t, 3)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)

	mkVote := func(hash [32]byte, i int) *Vote {
		v := &Vote{
			Height: 1, Round: 0, BlockHash: hash,
			ValidatorAddr: set[i].Address, Timestamp: 1,
		}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		return v
	}
	h1 := [32]byte{1}
	h2 := [32]byte{2}
	eng.SubmitVote(mkVote(h1, 1))
	eng.SubmitVote(mkVote(h2, 1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	key := set[1].Address.String() + "|1|0"
	eng.stateMu.Lock()
	seen := eng.seenVotes[key]
	eng.stateMu.Unlock()
	if seen == nil || seen.BlockHash != h1 {
		t.Fatalf("first vote should be kept, got %+v", seen)
	}
}

func TestQuorumCommits(t *testing.T) {
	set, keys := testSet(t, 3)
	committed := make(chan *types.Block, 1)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: time.Hour,
		OnCommit: func(b *types.Block) error {
			committed <- b
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	eng.SetHeight(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	leader, ok := eng.LeaderAddress(1, 0)
	if !ok {
		t.Fatal("no leader")
	}
	b := &types.Block{Header: types.BlockHeader{Height: 1, ValidatorAddr: leader}}
	h, herr := b.ComputeVotedHash()
	if herr != nil {
		t.Fatal(herr)
	}
	b.Hash = h

	eng.ProposeBlock(b)
	time.Sleep(80 * time.Millisecond)
	// Need 3 validators → quorum 3. Local vote is added on propose; add the other two.
	for i := 1; i < 3; i++ {
		v := &Vote{
			Height: 1, Round: 0, BlockHash: b.Hash,
			ValidatorAddr: set[i].Address, Timestamp: time.Now().Unix(),
		}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.SubmitVote(v)
	}

	select {
	case got := <-committed:
		if got.Header.Height != 1 {
			t.Fatalf("height %d", got.Header.Height)
		}
		if len(got.Votes) < 3 {
			t.Fatalf("want 3 votes, got %d", len(got.Votes))
		}
		if len(got.Header.QCRoot) == 0 {
			t.Fatal("missing QCRoot")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not commit")
	}
}
