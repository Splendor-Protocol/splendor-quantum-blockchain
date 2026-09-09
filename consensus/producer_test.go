package consensus

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// registerActive funds pk's address with the bond and registers it as Active
// in committed state, as a validators.register tx would leave it.
func registerActive(t *testing.T, sdb *state.StateDB, pk []byte) qcrypto.Address {
	t.Helper()
	pub, err := qcrypto.PublicKeyFromBytes(pk)
	if err != nil {
		t.Fatal(err)
	}
	addr := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(addr)
	acc.AddBalance(new(big.Int).Add(vm.ValidatorBond(), big.NewInt(1_000_000)))
	sdb.SetAccount(acc)
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(nil))
	if err := reg.Register(addr, pk, 0); err != nil {
		t.Fatalf("register %s: %v", addr, err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit registry: %v", err)
	}
	return addr
}

func waitForHeight(t *testing.T, ex *block.Executor, want uint64, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if h, _ := ex.LatestHeight(); h >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h, _ := ex.LatestHeight()
	t.Fatalf("head stuck at %d, wanted %d within %s", h, want, within)
}

// The bug the reviewer failed the branch for. A producer that booted on the
// single path (multi_validator scheduled above its head) sealed QC-less
// blocks until the schedule activated with two validators registered — then
// ProduceBlock returned ErrQuorumRequired at every tick and the chain stopped
// until someone restarted the process. The producer must instead move onto
// the BFT path in-process for the first multi_validator block, propose it,
// and commit it with the joiner's vote.
func TestSinglePathProducerHandsOverToBFTWhenQuorumBecomesRequired(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	joinerPriv, joinerPK, joinerAddr := testKey(t)
	registerActive(t, sdb, localPK)
	registerActive(t, sdb, joinerPK)

	// The first multi_validator height at which the local node is the round-0
	// leader, so the test does not have to drive a view change to get a
	// proposal out of it. At least two single-path blocks come first.
	active, err := vm.NewStakingContract(sdb, vm.DeployStaking(nil)).ListActive()
	if err != nil || len(active) != 2 {
		t.Fatalf("active set = %d (%v), want 2", len(active), err)
	}
	activation := uint64(3)
	for active[activation%uint64(len(active))].Address != localAddr {
		activation++
	}
	ex.SetForks(forks.Schedule{forks.MultiValidator: activation})

	proposals := make(chan *types.Block, 16)
	p := NewProducer(Config{
		BlockInterval: 250 * time.Millisecond,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
	}, BFTConfig{
		LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv,
		RoundTimeout: time.Hour,
		OnCommit:     func(b *types.Block) error { return ex.ExecuteBlock(b) },
		BroadcastProposal: func(m *ProposalMsg) {
			select {
			case proposals <- m.Block:
			default:
			}
		},
	}, ex, pool, nil, sdb)
	sealed := make(chan *types.Block, 64)
	p.Subscribe(sealed)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- p.Run(ctx) }()

	// Single path up to the activation: QC-less blocks, no engine.
	waitForHeight(t, ex, activation-1, 5*time.Second)
	if p.Engine() != nil {
		t.Fatal("BFT engine started before multi_validator activated")
	}
	last, err := ex.GetBlockByHeight(activation - 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Votes) != 0 || len(last.Header.QCRoot) != 0 {
		t.Fatalf("block %d was sealed on the single path but carries a certificate", activation-1)
	}

	// Block `activation` needs a 2-of-2 quorum. The old producer stalled here.
	var prop *types.Block
	select {
	case prop = <-proposals:
	case <-time.After(5 * time.Second):
		h, _ := ex.LatestHeight()
		t.Fatalf("no BFT proposal for block %d (head %d): the single-path producer did not hand over", activation, h)
	}
	if prop.Header.Height != activation || prop.Header.ValidatorAddr != localAddr {
		t.Fatalf("proposal is for height %d by %s, want %d by %s",
			prop.Header.Height, prop.Header.ValidatorAddr, activation, localAddr)
	}
	if h, _ := ex.LatestHeight(); h != activation-1 {
		t.Fatalf("head is %d: a block was sealed under multi_validator without a quorum", h)
	}
	eng := p.Engine()
	if eng == nil {
		t.Fatal("Engine() is nil although the producer has proposed through BFT")
	}
	select {
	case err := <-runErr:
		t.Fatalf("Producer.Run exited during the hand-over: %v", err)
	default:
	}

	// Vote as the joiner once the engine holds the proposal, so the vote is
	// judged against the vote set it opened rather than dropped ahead of it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		eng.stateMu.Lock()
		held := eng.proposal != nil && eng.proposal.Hash == prop.Hash
		round := eng.currentRound
		eng.stateMu.Unlock()
		if held {
			vote := &Vote{
				Height: activation, Round: round, BlockHash: prop.Hash,
				ValidatorAddr: joinerAddr, Timestamp: time.Now().Unix(),
			}
			if err := vote.Sign(joinerPriv); err != nil {
				t.Fatal(err)
			}
			eng.SubmitVote(vote)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine never opened a vote set for its own proposal")
		}
		time.Sleep(5 * time.Millisecond)
	}

	waitForHeight(t, ex, activation, 5*time.Second)
	got, err := ex.GetBlockByHeight(activation)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.ValidatorAddr != localAddr {
		t.Fatalf("block %d sealed by %s, want the local leader %s", activation, got.Header.ValidatorAddr, localAddr)
	}
	if len(got.Votes) != 2 || len(got.Header.QCRoot) == 0 {
		t.Fatalf("block %d carries %d votes and qc_root=%v, want a 2-vote certificate",
			activation, len(got.Votes), len(got.Header.QCRoot) != 0)
	}

	// Subscribers (the P2P broadcaster in the node) hear about the committed
	// block exactly as they heard about the single-path ones.
	seen := map[uint64]bool{}
	drain := time.After(2 * time.Second)
	for !seen[activation-1] || !seen[activation] {
		select {
		case b := <-sealed:
			seen[b.Header.Height] = true
		case <-drain:
			t.Fatalf("subscribers saw %v, want blocks %d and %d", seen, activation-1, activation)
		}
	}
}

// The single-path loop itself must refuse to seal a multi_validator block and
// say so with a sentinel, rather than fail ProduceBlock every interval.
func TestValidatorRunReturnsErrBFTRequiredAtActivation(t *testing.T) {
	ex, _, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	ex.SetForks(forks.Schedule{forks.MultiValidator: 2})
	v := New(Config{
		BlockInterval: 20 * time.Millisecond,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
	}, ex, pool, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- v.Run(ctx) }()

	select {
	case err := <-runErr:
		if !errors.Is(err, ErrBFTRequired) {
			t.Fatalf("Run returned %v, want ErrBFTRequired", err)
		}
	case <-time.After(3 * time.Second):
		h, _ := ex.LatestHeight()
		t.Fatalf("Run kept going at head %d instead of returning ErrBFTRequired", h)
	}
	if h, _ := ex.LatestHeight(); h != 1 {
		t.Fatalf("head = %d, want exactly the one pre-activation block", h)
	}
}

// A validator whose NEXT block already falls under multi_validator hands over
// at once, not after idling one interval on a path it will never use — this
// is the boot at head 0 on a chain that activates at height 1.
func TestValidatorRunHandsOverBeforeFirstTickWhenBFTAlreadyDue(t *testing.T) {
	ex, _, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v := New(Config{
		BlockInterval: time.Hour,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
	}, ex, pool, nil)

	runErr := make(chan error, 1)
	go func() { runErr <- v.Run(context.Background()) }()
	select {
	case err := <-runErr:
		if !errors.Is(err, ErrBFTRequired) {
			t.Fatalf("Run returned %v, want ErrBFTRequired", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run waited for a tick although BFT was already due")
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("head = %d, a block was sealed on the single path under multi_validator", h)
	}
}

// Boot behaviour preserved: multi_validator active for the next block means
// BFT from the first block, self-committed with one vote by the sole validator.
func TestProducerStartsOnBFTWhenAlreadyActive(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	registerActive(t, sdb, localPK)
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	p := NewProducer(Config{
		BlockInterval: 20 * time.Millisecond,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
	}, BFTConfig{
		LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv,
		RoundTimeout: time.Hour,
		OnCommit:     func(b *types.Block) error { return ex.ExecuteBlock(b) },
	}, ex, pool, nil, sdb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	waitForHeight(t, ex, 1, 3*time.Second)
	if p.Engine() == nil {
		t.Fatal("Engine() is nil on a chain where multi_validator is active from block 1")
	}
	b, err := ex.GetBlockByHeight(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Votes) != 1 || len(b.Header.QCRoot) == 0 {
		t.Fatalf("block 1 carries %d votes, qc_root=%v; want the sole validator's one-vote certificate",
			len(b.Votes), len(b.Header.QCRoot) != 0)
	}
}

// A chain that never schedules multi_validator is untouched: single path, no
// engine, QC-less blocks, however many keys are registered.
func TestProducerStaysOnSinglePathWithoutSchedule(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	_, otherPK, _ := testKey(t)
	registerActive(t, sdb, localPK)
	registerActive(t, sdb, otherPK)
	p := NewProducer(Config{
		BlockInterval: 20 * time.Millisecond,
		Address:       localAddr, PublicKey: localPK, PrivateKey: localPriv,
	}, BFTConfig{
		LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv,
	}, ex, pool, nil, sdb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	waitForHeight(t, ex, 3, 3*time.Second)
	if p.Engine() != nil {
		t.Fatal("BFT engine started on a chain with no multi_validator schedule")
	}
	b, err := ex.GetBlockByHeight(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Votes) != 0 || len(b.Header.QCRoot) != 0 {
		t.Fatal("single-path block carries a certificate")
	}
}
