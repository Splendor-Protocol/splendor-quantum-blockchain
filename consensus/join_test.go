package consensus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func newTestChain(t *testing.T) (*block.Executor, *state.StateDB, *mempool.Mempool) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ex.Close(); sdb.Close() })
	return ex, sdb, mempool.New(100, sdb)
}

// Two BFT nodes on a chain whose genesis validator never registered must agree
// on one leader. With the local node as the fallback each elected itself; the
// fallback is now the key that sealed block 0.
func TestRefreshSetFallsBackToGenesisValidator(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	_, genPK, genAddr := testKey(t)
	eng, err := NewBFTEngine(BFTConfig{LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv})
	if err != nil {
		t.Fatal(err)
	}
	mv := NewMultiValidator(Config{
		Address: localAddr, PublicKey: localPK, PrivateKey: localPriv,
		GenesisValidator: ValidatorInfo{Address: genAddr, PublicKey: genPK, IsActive: true},
	}, eng, ex, pool, nil, sdb)
	mv.RefreshSet()
	leader, ok := eng.LeaderAddress(1, 0)
	if !ok || leader != genAddr {
		t.Fatalf("empty registry: leader = %s ok=%v, want the genesis validator %s", leader, ok, genAddr)
	}
	if eng.IsLeader(1, 0) {
		t.Fatal("an unregistered joiner must not believe it is the sole leader")
	}
	if n := eng.ValidatorCount(); n != 1 {
		t.Fatalf("set size %d, want 1", n)
	}
}

// A node that knows no genesis validator keeps today's behaviour, so the
// producer that minted the chain still runs alone from block 1.
func TestRefreshSetUsesLocalWhenNoGenesisValidatorKnown(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	eng, err := NewBFTEngine(BFTConfig{LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv})
	if err != nil {
		t.Fatal(err)
	}
	mv := NewMultiValidator(Config{Address: localAddr, PublicKey: localPK, PrivateKey: localPriv}, eng, ex, pool, nil, sdb)
	mv.RefreshSet()
	if !eng.IsLeader(1, 0) {
		t.Fatal("with no genesis validator configured the local fallback must hold")
	}
}

// A validator still catching up must vote, never propose: a proposal built on
// a stale head is a fork by another name.
func TestMultiValidatorDoesNotProposeUntilReady(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	var ready atomic.Bool
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv,
		RoundTimeout: time.Hour,
		OnCommit:     func(b *types.Block) error { return ex.ExecuteBlock(b) },
	})
	if err != nil {
		t.Fatal(err)
	}
	mv := NewMultiValidator(Config{
		BlockInterval: 20 * time.Millisecond,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
		Ready: ready.Load,
	}, eng, ex, pool, nil, sdb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-eng.CommitCh():
				mv.NotifyCommit(b)
			}
		}
	}()
	go func() { _ = mv.Run(ctx) }()

	time.Sleep(150 * time.Millisecond)
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("proposed %d blocks while not ready", h)
	}
	ready.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h, _ := ex.LatestHeight(); h >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no block produced after becoming ready")
}

// A commit that fails at height h (the block landed through push or pull-sync
// first, or the node is behind) must not wedge the engine: the next height's
// proposal is a different block and must still be voted on.
func TestFailedCommitDoesNotLockOutNextHeight(t *testing.T) {
	set, keys := testSet(t, 2)
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: time.Hour,
		OnCommit:     func(*types.Block) error { return errors.New("not applied") },
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	eng.SetHeight(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	mkBlock := func(h uint64) *types.Block {
		leader, _ := eng.LeaderAddress(h, 0)
		b := &types.Block{Header: types.BlockHeader{Height: h, ValidatorAddr: leader}}
		hash, herr := b.ComputeVotedHash()
		if herr != nil {
			t.Fatal(herr)
		}
		b.Hash = hash
		return b
	}
	b1 := mkBlock(1)
	eng.ProposeBlock(b1)
	time.Sleep(50 * time.Millisecond)
	for i := range set {
		v := &Vote{Height: 1, Round: 0, BlockHash: b1.Hash, ValidatorAddr: set[i].Address, Timestamp: 1}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.SubmitVote(v)
	}
	time.Sleep(50 * time.Millisecond)
	eng.stateMu.Lock()
	voted := eng.hasVoted
	height := eng.currentHeight
	eng.stateMu.Unlock()
	// The failed commit used to leave a lock behind, and this test asserted
	// that as its set-up. It no longer does: nothing was committed, so there is
	// nothing to be locked on, and holding one wedged the height against every
	// retry (see TestFailedCommitDoesNotWedgeTheHeight). The property this test
	// exists for — the NEXT height is still votable — is unchanged.
	if voted {
		t.Fatal("a failed commit left the engine with vote commitment")
	}
	if height != 1 {
		t.Fatalf("engine moved to height %d on a commit that failed", height)
	}

	b2 := mkBlock(2)
	eng.ProposeBlock(b2)
	time.Sleep(50 * time.Millisecond)
	eng.stateMu.Lock()
	voting := eng.voteSet != nil && eng.currentHeight == 2
	eng.stateMu.Unlock()
	if !voting {
		t.Fatal("engine refused the next height's proposal after a failed commit")
	}
}

// One registrant that is not the genesis validator must not become the
// leader set: below two validators the executor still accepts the genesis
// validator's blocks, and a lone stranger who cannot reach this node would
// otherwise just stop the chain. The genesis validator keeps producing.
func TestRefreshSetKeepsGenesisValidatorBelowTwoRegistrants(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	genPriv, genPK, genAddr := testKey(t)
	_, strangerPK, strangerAddr := testKey(t)
	acc := types.NewAccount(strangerAddr)
	acc.AddBalance(vm.ValidatorBond())
	sdb.SetAccount(acc)
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(sdb))
	if err := reg.Register(strangerAddr, strangerPK, 1); err != nil {
		t.Fatal(err)
	}
	if n, _ := reg.ValidatorCount(); n != 1 {
		t.Fatalf("precondition: registry holds %d, want 1", n)
	}

	eng, err := NewBFTEngine(BFTConfig{LocalAddr: genAddr, LocalPubKey: genPK, LocalPrivKey: genPriv})
	if err != nil {
		t.Fatal(err)
	}
	mv := NewMultiValidator(Config{
		Address: genAddr, PublicKey: genPK, PrivateKey: genPriv,
		GenesisValidator: ValidatorInfo{Address: genAddr, PublicKey: genPK, IsActive: true},
	}, eng, ex, pool, nil, sdb)
	mv.RefreshSet()
	if !eng.IsLeader(2, 0) {
		leader, _ := eng.LeaderAddress(2, 0)
		t.Fatalf("a lone foreign registrant took the leader set: leader = %s, want genesis %s", leader, genAddr)
	}
	if n := eng.ValidatorCount(); n != 1 {
		t.Fatalf("set size %d, want 1", n)
	}

	// The genesis validator registers itself too: two members, the registry
	// is now the authority and both are in the set.
	gacc := types.NewAccount(genAddr)
	gacc.AddBalance(vm.ValidatorBond())
	sdb.SetAccount(gacc)
	if err := reg.Register(genAddr, genPK, 2); err != nil {
		t.Fatal(err)
	}
	mv.RefreshSet()
	if n := eng.ValidatorCount(); n != 2 {
		t.Fatalf("after the second registration set size %d, want 2", n)
	}
}
