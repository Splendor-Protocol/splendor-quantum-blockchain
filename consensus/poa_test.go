package consensus

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// fakeGate stands in for the chain's admission rule so a test can move a
// producer in and out of the admitted set without building a staking registry.
// The rule it models is the real one's shape: an answer per address, decided by
// the chain rather than by the schedule.
type fakeGate struct {
	refused map[qcrypto.Address]error
	asked   int
}

func (g *fakeGate) EligibleToProduceAddr(addr qcrypto.Address, _ uint64) error {
	g.asked++
	return g.refused[addr]
}

// sealGenesis puts a block 0 on the chain, the way cmd/node does, so a test
// about heights above 0 is not also a test about the empty-chain rule.
func sealGenesis(t *testing.T, ex *block.Executor, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, root [32]byte) {
	t.Helper()
	g := types.GenesisBlock(qcrypto.DeriveAddress(pub), root, time.Now().Unix())
	g.ValidatorPubKey = pub.Bytes()
	hash, err := g.ComputeHash()
	if err != nil {
		t.Fatalf("genesis hash: %v", err)
	}
	g.Hash = hash
	if err := g.SignBlock(priv); err != nil {
		t.Fatalf("genesis sign: %v", err)
	}
	if err := ex.ExecuteBlock(g); err != nil {
		t.Fatalf("genesis apply: %v", err)
	}
}

func addrs(t *testing.T, n int) []qcrypto.Address {
	t.Helper()
	out := make([]qcrypto.Address, n)
	for i := range out {
		pub, _, err := qcrypto.GenerateKeypair()
		if err != nil {
			t.Fatalf("keygen: %v", err)
		}
		out[i] = qcrypto.DeriveAddress(pub)
	}
	return out
}

// The rotation must be a rotation: over four admitted producers, four
// consecutive heights go to four different keys, and the cycle repeats.
func TestRoundRobinRotatesOverAdmittedProducers(t *testing.T) {
	p := addrs(t, 4)
	rr, err := NewRoundRobin(p, &fakeGate{})
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}
	for h := uint64(1); h <= 12; h++ {
		got, err := rr.SlotOwner(h)
		if err != nil {
			t.Fatalf("SlotOwner(%d): %v", h, err)
		}
		if want := p[h%4]; got != want {
			t.Fatalf("height %d: slot went to %s, want %s", h, got, want)
		}
	}
}

// The whole point of filtering by the gate rather than by configuration: a key
// the chain stops admitting stops getting slots, and the remaining producers
// close the gap instead of leaving the chain to stall on an empty one.
func TestRoundRobinDropsAndRestoresOnGateAnswer(t *testing.T) {
	p := addrs(t, 4)
	gate := &fakeGate{refused: map[qcrypto.Address]error{}}
	rr, err := NewRoundRobin(p, gate)
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}

	gate.refused[p[2]] = errors.New("validator has no admission pass")

	seen := map[qcrypto.Address]int{}
	for h := uint64(1); h <= 30; h++ {
		owner, err := rr.SlotOwner(h)
		if err != nil {
			t.Fatalf("SlotOwner(%d): %v", h, err)
		}
		seen[owner]++
	}
	if seen[p[2]] != 0 {
		t.Fatalf("the refused producer got %d slots; it must get none", seen[p[2]])
	}
	for _, want := range []qcrypto.Address{p[0], p[1], p[3]} {
		if seen[want] == 0 {
			t.Fatalf("%s got no slots; the remaining three must share them", want)
		}
	}

	// Restoring the pass puts it back in, without anything else changing.
	delete(gate.refused, p[2])
	back := false
	for h := uint64(1); h <= 8 && !back; h++ {
		owner, _ := rr.SlotOwner(h)
		back = owner == p[2]
	}
	if !back {
		t.Fatal("a re-admitted producer never got a slot back")
	}

	// And the reason a producer is out is the gate's own, not a paraphrase.
	gate.refused[p[1]] = errors.New("validator is not bonded")
	if _, refused := rr.Eligible(5); refused[p[1]] == nil ||
		!strings.Contains(refused[p[1]].Error(), "not bonded") {
		t.Fatalf("Eligible must report the gate's reason verbatim, got %v", refused[p[1]])
	}
}

// Authorize is the follower half. Without it any admitted producer could seal
// any height, and two of them sealing the same one is a fork.
func TestRoundRobinAuthorizeRefusesOutOfTurn(t *testing.T) {
	p := addrs(t, 4)
	rr, err := NewRoundRobin(p, &fakeGate{})
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}
	owner, _ := rr.SlotOwner(9)
	if err := rr.Authorize(9, owner); err != nil {
		t.Fatalf("the scheduled producer must be authorised: %v", err)
	}
	for _, other := range p {
		if other == owner {
			continue
		}
		err := rr.Authorize(9, other)
		if err == nil {
			t.Fatalf("%s sealed height 9 out of turn and was authorised", other)
		}
		if !strings.Contains(err.Error(), "schedule gives that slot to") {
			t.Fatalf("unhelpful refusal: %v", err)
		}
	}
}

// With nobody admitted there is no owner, and the schedule says so rather than
// picking one. On a gated chain this is unreachable — the staking module
// refuses to remove the last producer — so reaching it means the configured
// list and the chain's registry have drifted apart, which is worth an error.
func TestRoundRobinNoEligibleProducer(t *testing.T) {
	p := addrs(t, 2)
	gate := &fakeGate{refused: map[qcrypto.Address]error{
		p[0]: errors.New("no pass"), p[1]: errors.New("no pass"),
	}}
	rr, _ := NewRoundRobin(p, gate)
	if _, err := rr.SlotOwner(3); !errors.Is(err, ErrNoEligibleProducer) {
		t.Fatalf("want ErrNoEligibleProducer, got %v", err)
	}
	if err := rr.Authorize(3, p[0]); !errors.Is(err, ErrNoEligibleProducer) {
		t.Fatalf("Authorize must surface the same condition, got %v", err)
	}
}

// The list is consensus input. A duplicate or a zero address means two nodes
// given "the same" configuration would disagree about who owns which height,
// so it is refused at construction rather than at the fork it would cause.
func TestRoundRobinRefusesUnusableConfiguration(t *testing.T) {
	p := addrs(t, 2)
	for name, producers := range map[string][]qcrypto.Address{
		"empty":     {},
		"duplicate": {p[0], p[1], p[0]},
		"zero":      {p[0], {}},
	} {
		if _, err := NewRoundRobin(producers, &fakeGate{}); err == nil {
			t.Errorf("%s producer list was accepted", name)
		}
	}
	if _, err := NewRoundRobin(p, nil); err == nil {
		t.Error("a schedule with no gate was accepted; it would have nothing to filter by")
	}
	// The returned list must be a copy: a caller that could reorder it could
	// reorder the schedule.
	rr, _ := NewRoundRobin(p, &fakeGate{})
	got := rr.Producers()
	got[0], got[1] = got[1], got[0]
	if rr.Producers()[0] != p[0] {
		t.Error("Producers() handed out the schedule's own slice")
	}
}

// A scheduled producer must not mint block 1 on an empty chain. Exactly one key
// mints block 0; a second producer that sealed height 1 on a null prev-hash
// would fork away from the chain it was started to join. Found on a control run
// with the gates inactive, where nothing else kept the other three out of the
// rotation until they had caught up.
func TestScheduledValidatorWaitsForBlockZero(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	self := qcrypto.DeriveAddress(pub)
	other := addrs(t, 1)[0]

	// Ungated, and this node owns slot 1 — so nothing but the empty-chain rule
	// stands between it and a forking block.
	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 1})
	rr, err := NewRoundRobin([]qcrypto.Address{other, self}, ex)
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}
	if owner, _ := rr.SlotOwner(1); owner != self {
		t.Fatalf("test setup: height 1 should be this node's slot, got %s", owner)
	}

	v := New(Config{
		Address: self, PublicKey: pub.Bytes(), PrivateKey: priv, Schedule: rr,
	}, ex, mempool.New(16, sdb), nil)

	if err := v.produceBlock(); err != nil {
		t.Fatalf("waiting for block 0 must not be an error: %v", err)
	}
	if b, _ := ex.LatestBlock(); b != nil {
		t.Fatalf("a scheduled producer minted height %d on an empty chain", b.Header.Height)
	}

	// Without a schedule the single Phase-1 producer behaves exactly as before.
	v.cfg.Schedule = nil
	if err := v.produceBlock(); err != nil {
		t.Fatalf("single-producer path: %v", err)
	}
	if b, _ := ex.LatestBlock(); b == nil || b.Header.Height != 1 {
		t.Fatal("the ungated single producer should still have sealed block 1")
	}
}

// A scheduled validator waits for its turn, and the wait is silent — the same
// discipline the gate skip follows, and for the same reason: a producer that
// logged an error on three out of every four ticks would bury the one tick
// that mattered.
func TestValidatorSkipsHeightsOwnedByAnotherProducer(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	self := qcrypto.DeriveAddress(pub)
	other := addrs(t, 1)[0]

	// Ungated chain: the staking gate is inert, so what is being tested is the
	// schedule alone. Block 0 exists, so the empty-chain rule is not in play
	// either.
	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 1})
	root, err := sdb.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	sealGenesis(t, ex, pub, priv, root)

	// Height 1 belongs to `other` under [other, self] (1 mod 2 == 1 is self —
	// so order the list to put `other` on the odd slot).
	rr, err := NewRoundRobin([]qcrypto.Address{self, other}, ex)
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}
	v := New(Config{
		Address: self, PublicKey: pub.Bytes(), PrivateKey: priv, Schedule: rr,
	}, ex, mempool.New(16, sdb), nil)

	owner, err := rr.SlotOwner(1)
	if err != nil {
		t.Fatalf("SlotOwner: %v", err)
	}
	if owner != other {
		t.Fatalf("test setup: height 1 should belong to the other producer, got %s", owner)
	}
	if err := v.produceBlock(); err != nil {
		t.Fatalf("waiting for its turn must not be an error: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("a block was produced at height %d on another producer's slot", h)
	}

	// Its own slot, and it seals.
	solo, err := NewRoundRobin([]qcrypto.Address{other, self}, ex)
	if err != nil {
		t.Fatalf("NewRoundRobin: %v", err)
	}
	v.cfg.Schedule = solo
	if err := v.produceBlock(); err != nil {
		t.Fatalf("produce on its own slot: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 1 {
		t.Fatalf("height is %d; the validator did not seal its own slot", h)
	}
}
