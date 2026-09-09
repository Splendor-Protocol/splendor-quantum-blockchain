package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// A two-node cluster in one process, wired through a link that reorders and
// delays messages the way a real network does.
//
// This is the join scenario the shell harness runs against real processes,
// reduced to what consensus itself is responsible for: a producer that is
// already running, a second validator that arrives later, registers with a
// signed transaction, and from then on shares the leadership. It runs in a few
// seconds and under -race, so the races the live harness can only hit by luck
// — a vote overtaking its proposal, a set change seen at different moments on
// the two nodes, a round timer firing on one side only — are exercised on
// every run.

type labNode struct {
	name string
	ex   *block.Executor
	sdb  *state.StateDB
	pool *mempool.Mempool
	eng  *BFTEngine
	mv   *MultiValidator

	pub  *qcrypto.PublicKey
	priv *qcrypto.PrivateKey
	addr qcrypto.Address

	mu    sync.Mutex
	peers []*labNode
}

// labFork is the schedule both nodes run: BFT from block 1, the validators
// module available from block 1.
func labFork() forks.Schedule {
	return forks.Schedule{forks.MultiValidator: 1, forks.ValidatorsV1: 1}
}

// newLabNode builds a node whose state is seeded identically on every node in
// the cluster, so their state roots agree from block 1 on.
func newLabNode(t *testing.T, name string, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, fund []*qcrypto.PublicKey) *labNode {
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
	ex.SetForks(labFork())

	// Genesis allocations, applied in the same order on every node.
	for _, p := range fund {
		acc := types.NewAccount(qcrypto.DeriveAddress(p))
		acc.AddBalance(new(big.Int).Mul(vm.ValidatorBond(), big.NewInt(10)))
		sdb.SetAccount(acc)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}

	return &labNode{
		name: name, ex: ex, sdb: sdb, pool: mempool.New(256, sdb),
		pub: pub, priv: priv, addr: qcrypto.DeriveAddress(pub),
	}
}

// applyCommitted mirrors the node binary's commit callback: a block that
// another path already applied is committed, not a failure.
func applyCommitted(ex *block.Executor, b *types.Block) error {
	if err := ex.ExecuteBlock(b); err == nil {
		return nil
	} else if head, herr := ex.LatestBlock(); herr == nil && head != nil && head.Hash == b.Hash {
		return nil
	} else {
		return err
	}
}

// start wires the node's consensus and begins producing. genesisValidator is
// the empty-registry fallback both nodes must agree on.
func (n *labNode) start(t *testing.T, ctx context.Context, genesisValidator ValidatorInfo, ready func() bool, jitter *rand.Rand, jitterMu *sync.Mutex) {
	t.Helper()
	delay := func() time.Duration {
		jitterMu.Lock()
		defer jitterMu.Unlock()
		return time.Duration(jitter.Intn(12)) * time.Millisecond
	}
	// Every hop goes through a goroutine with an independent delay, so the
	// order in which a peer sees a proposal, a vote and the committed block is
	// not the order they were sent in.
	send := func(f func(*labNode)) {
		n.mu.Lock()
		peers := append([]*labNode(nil), n.peers...)
		n.mu.Unlock()
		for _, p := range peers {
			p := p
			d := delay()
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(d):
					f(p)
				}
			}()
		}
	}

	cfg := Config{
		BlockInterval:    60 * time.Millisecond,
		MaxTxPerBlock:    block.MaxTxPerBlock,
		Address:          n.addr,
		PublicKey:        n.pub.Bytes(),
		PrivateKey:       n.priv,
		GenesisValidator: genesisValidator,
		Ready:            ready,
	}
	eng, err := NewBFTEngine(BFTConfig{
		LocalAddr: n.addr, LocalPubKey: n.pub.Bytes(), LocalPrivKey: n.priv,
		RoundTimeout: 250 * time.Millisecond,
		OnCommit: func(b *types.Block) error {
			if err := applyCommitted(n.ex, b); err != nil {
				return err
			}
			// Committed blocks are pushed to peers, exactly as the broadcaster
			// does; the receiving node may already have committed it itself.
			raw, mErr := json.Marshal(b)
			if mErr != nil {
				return nil
			}
			send(func(p *labNode) {
				var cp types.Block
				if json.Unmarshal(raw, &cp) == nil {
					_ = applyCommitted(p.ex, &cp)
				}
			})
			return nil
		},
		Broadcast: func(v *Vote) {
			raw, err := json.Marshal(v)
			if err != nil {
				return
			}
			send(func(p *labNode) {
				var cp Vote
				if json.Unmarshal(raw, &cp) == nil {
					p.eng.SubmitVote(&cp)
				}
			})
		},
		BroadcastProposal: func(m *ProposalMsg) {
			raw, err := m.Encode()
			if err != nil {
				return
			}
			send(func(p *labNode) {
				if decoded, derr := DecodeProposal(raw); derr == nil {
					p.eng.SubmitProposal(decoded)
				}
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	n.eng = eng
	n.mv = NewMultiValidator(cfg, eng, n.ex, n.pool, nil, n.sdb)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-eng.CommitCh():
				n.mv.NotifyCommit(b)
			}
		}
	}()
	go func() { _ = n.mv.Run(ctx) }()
}

// startSync is the lab's stand-in for the node's 1 s pull-sync loop: a pushed
// block that arrives above the local head cannot be applied, so a node joining
// a chain that is already running has to fetch what it missed. Without it a
// joiner would sit at height 0 for ever, which is exactly what the live
// harness proves the real syncer does not do.
func (n *labNode) startSync(ctx context.Context) {
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n.mu.Lock()
				peers := append([]*labNode(nil), n.peers...)
				n.mu.Unlock()
				for _, p := range peers {
					for n.height() < p.height() {
						blk, err := p.ex.GetBlockByHeight(n.height() + 1)
						if err != nil || blk == nil {
							break
						}
						if err := applyCommitted(n.ex, blk); err != nil {
							break
						}
					}
				}
			}
		}
	}()
}

// bestPeerHeight is what the node's Ready gate reads from its peer pool.
func (n *labNode) bestPeerHeight() uint64 {
	n.mu.Lock()
	peers := append([]*labNode(nil), n.peers...)
	n.mu.Unlock()
	best := uint64(0)
	for _, p := range peers {
		if h := p.height(); h > best {
			best = h
		}
	}
	return best
}

// synced mirrors cmd/node's joining-validator gate: a stored head, and within
// one block of the best peer.
func (n *labNode) synced() bool {
	return n.height() > 0 && n.bestPeerHeight() <= n.height()+1
}

func (n *labNode) connect(peers ...*labNode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers = append(n.peers, peers...)
}

func (n *labNode) height() uint64 {
	h, _ := n.ex.LatestHeight()
	return h
}

// registerTx is the signed validators.register an operator submits.
func registerTx(t *testing.T, n *labNode, nonce uint64) *types.Transaction {
	t.Helper()
	calldata, err := json.Marshal(struct {
		Module string          `json:"module"`
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}{"validators", "register", json.RawMessage(fmt.Sprintf(`{"pubkey":%q}`, hexOf(n.pub.Bytes())))})
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewCall(n.addr, vm.ModuleRouterAddress, calldata,
		big.NewInt(0), big.NewInt(int64(mempool.MinFee)), nonce, time.Now().Unix())
	if err := tx.Sign(n.priv, nonce == 0); err != nil {
		t.Fatal(err)
	}
	return tx
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// labDiag is set by each test to the cluster it is driving, so a timeout says
// which of the two failure shapes happened: a chain that stopped, or one that
// carried on without the joiner.
var labDiag func() string

func waitUntil(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := ""
	if labDiag != nil {
		state = "\n  state: " + labDiag()
	}
	t.Fatalf("timed out after %s waiting for %s%s", d, what, state)
}

// describe renders what every node believes, for a timeout message that says
// which of the two failure shapes happened: a chain that stopped, or one that
// carried on without the joiner.
func describe(nodes ...*labNode) string {
	var b []string
	for _, n := range nodes {
		if n.eng == nil {
			b = append(b, fmt.Sprintf("%s{not started}", n.name))
			continue
		}
		b = append(b, fmt.Sprintf("%s{height=%d set=%d engine=%d/%d mempool=%d}",
			n.name, n.height(), n.activeCount(), n.eng.Height(), n.eng.Round(), n.pool.Size()))
	}
	return strings.Join(b, " ")
}

// activeCount reads the node's own view of the registry.
func (n *labNode) activeCount() int {
	list, _ := vm.NewStakingContract(n.sdb, vm.DeployStaking(nil)).ListActive()
	return len(list)
}

// TestSecondValidatorJoinsARunningProducer is the whole scenario: A produces
// alone, B arrives, follows, registers on-chain, and the two then share the
// leadership on a chain that never stopped.
func TestSecondValidatorJoinsARunningProducer(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-second consensus integration")
	}
	aPub, aPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	bPub, bPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fund := []*qcrypto.PublicKey{aPub, bPub}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jitter := rand.New(rand.NewSource(20260909))
	var jitterMu sync.Mutex

	a := newLabNode(t, "A", aPub, aPriv, fund)
	b := newLabNode(t, "B", bPub, bPriv, fund)
	labDiag = func() string { return describe(a, b) }
	t.Cleanup(func() { labDiag = nil })
	genesisValidator := ValidatorInfo{Address: a.addr, PublicKey: aPub.Bytes(), IsActive: true}

	// A alone, with an empty registry: the fallback set is the genesis
	// validator, so A is the sole leader and self-commits.
	a.start(t, ctx, genesisValidator, nil, jitter, &jitterMu)
	waitUntil(t, "A to produce on its own", 10*time.Second, func() bool { return a.height() >= 3 })

	// A registers itself, on-chain, while it is running.
	if err := a.pool.Add(registerTx(t, a, 0)); err != nil {
		t.Fatalf("submit A's registration: %v", err)
	}
	waitUntil(t, "A's registration to be included", 10*time.Second, func() bool { return a.activeCount() == 1 })
	heightBeforeJoin := a.height()

	// B joins the RUNNING chain. It is not registered, so it must follow
	// without voting; its Ready gate keeps it from proposing until it has a
	// head, exactly as the node binary's does.
	b.start(t, ctx, genesisValidator, b.synced, jitter, &jitterMu)
	a.connect(b)
	b.connect(a)
	a.startSync(ctx)
	b.startSync(ctx)

	waitUntil(t, "B to catch up with A", 20*time.Second, func() bool {
		return b.height() > 0 && b.height()+2 >= a.height()
	})
	// The producer must not have paused for the join. B can reach the tip
	// inside one block interval, so this is a wait, not an instant assertion.
	waitUntil(t, "the producer to keep producing across the join", 10*time.Second,
		func() bool { return a.height() > heightBeforeJoin })

	// B registers with its own signed transaction, submitted to A — the node
	// an operator can reach — which is the only one that can include it.
	if err := a.pool.Add(registerTx(t, b, 0)); err != nil {
		t.Fatalf("submit B's registration: %v", err)
	}
	waitUntil(t, "both nodes to see a two-member set", 20*time.Second, func() bool {
		return a.activeCount() == 2 && b.activeCount() == 2
	})

	quorumFrom := a.height() + 2
	target := quorumFrom + 8
	waitUntil(t, "the two-validator chain to advance", 40*time.Second, func() bool {
		return a.height() >= target && b.height() >= target
	})

	// Both nodes must agree on every block, and both must have sealed some.
	producers := map[qcrypto.Address]int{}
	for h := quorumFrom; h <= target; h++ {
		ba, err := a.ex.GetBlockByHeight(h)
		if err != nil {
			t.Fatalf("A has no block %d: %v", h, err)
		}
		bb, err := b.ex.GetBlockByHeight(h)
		if err != nil {
			t.Fatalf("B has no block %d: %v", h, err)
		}
		if ba.Hash != bb.Hash {
			t.Fatalf("height %d: A has %x, B has %x", h, ba.Hash[:8], bb.Hash[:8])
		}
		if ba.Header.StateRoot != bb.Header.StateRoot {
			t.Fatalf("height %d: state roots differ", h)
		}
		if len(ba.Header.QCRoot) == 0 {
			t.Fatalf("height %d carries no quorum certificate", h)
		}
		voters := map[qcrypto.Address]bool{}
		for _, v := range ba.Votes {
			voters[v.ValidatorAddr] = true
		}
		if len(voters) < 2 {
			t.Fatalf("height %d certified by %d validators, want 2", h, len(voters))
		}
		producers[ba.Header.ValidatorAddr]++
	}
	if len(producers) < 2 {
		t.Fatalf("only %d distinct producer(s) in heights %d..%d — the joiner never became a validator",
			len(producers), quorumFrom, target)
	}
	if producers[b.addr] == 0 {
		t.Fatal("the joined validator sealed no blocks")
	}
}

// A third validator arriving on a two-validator chain must widen the quorum to
// three without the chain stopping. This is the set transition the two-node
// case cannot exercise: for one height each node can believe a different peer
// is the leader, and only the round carried in the proposal gets them back in
// step.
func TestThirdValidatorJoinsAndWidensTheQuorum(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-second consensus integration")
	}
	keys := make([]*qcrypto.PublicKey, 3)
	privs := make([]*qcrypto.PrivateKey, 3)
	for i := range keys {
		pub, priv, err := qcrypto.GenerateKeypair()
		if err != nil {
			t.Fatal(err)
		}
		keys[i], privs[i] = pub, priv
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jitter := rand.New(rand.NewSource(7))
	var jitterMu sync.Mutex

	nodes := make([]*labNode, 3)
	for i := range nodes {
		nodes[i] = newLabNode(t, string(rune('A'+i)), keys[i], privs[i], keys)
	}
	labDiag = func() string { return describe(nodes...) }
	t.Cleanup(func() { labDiag = nil })
	gv := ValidatorInfo{Address: nodes[0].addr, PublicKey: keys[0].Bytes(), IsActive: true}

	nodes[0].start(t, ctx, gv, nil, jitter, &jitterMu)
	nodes[0].startSync(ctx)
	waitUntil(t, "the producer to start", 10*time.Second, func() bool { return nodes[0].height() >= 2 })
	if err := nodes[0].pool.Add(registerTx(t, nodes[0], 0)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the producer to register", 10*time.Second, func() bool { return nodes[0].activeCount() == 1 })

	for i := 1; i < 3; i++ {
		n := nodes[i]
		n.start(t, ctx, gv, n.synced, jitter, &jitterMu)
		for j := 0; j < i; j++ {
			nodes[j].connect(n)
			n.connect(nodes[j])
		}
		n.startSync(ctx)
		waitUntil(t, fmt.Sprintf("node %d to catch up", i), 30*time.Second, func() bool {
			return n.height() > 0 && n.height()+2 >= nodes[0].height()
		})
		if err := nodes[0].pool.Add(registerTx(t, n, 0)); err != nil {
			t.Fatalf("submit node %d registration: %v", i, err)
		}
		want := i + 1
		waitUntil(t, fmt.Sprintf("every node to see a %d-member set", want), 40*time.Second, func() bool {
			for _, m := range nodes[:i+1] {
				if m.activeCount() != want {
					return false
				}
			}
			return true
		})
	}

	from := nodes[0].height() + 2
	to := from + 8
	waitUntil(t, "the three-validator chain to advance", 60*time.Second, func() bool {
		for _, n := range nodes {
			if n.height() < to {
				return false
			}
		}
		return true
	})

	producers := map[qcrypto.Address]int{}
	for h := from; h <= to; h++ {
		var ref *types.Block
		for _, n := range nodes {
			blk, err := n.ex.GetBlockByHeight(h)
			if err != nil {
				t.Fatalf("%s has no block %d: %v", n.name, h, err)
			}
			if ref == nil {
				ref = blk
				continue
			}
			if blk.Hash != ref.Hash {
				t.Fatalf("height %d: %s disagrees on the block hash", h, n.name)
			}
		}
		voters := map[qcrypto.Address]bool{}
		for _, v := range ref.Votes {
			voters[v.ValidatorAddr] = true
		}
		if len(voters) < 3 {
			t.Fatalf("height %d certified by %d validators, want 3", h, len(voters))
		}
		producers[ref.Header.ValidatorAddr]++
	}
	if len(producers) < 3 {
		t.Fatalf("%d distinct producers in %d..%d, want 3", len(producers), from, to)
	}
}
