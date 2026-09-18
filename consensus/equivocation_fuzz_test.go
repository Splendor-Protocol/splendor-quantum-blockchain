package consensus

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A randomised search for the live residual: one honest node signing two
// prevotes at one (height, round) with different block hashes, rarely rather
// than every tick.
//
// The harness drives four engines through the same operations the node
// actually performs — MultiValidator.propose (which calls SetHeight
// unconditionally before it mints), the block-interval tick, round timeouts,
// out-of-order and duplicated message delivery, and a validator-set refresh
// that may reorder the set (LeaderAddress indexes into it, and it is guarded
// by a different mutex from the engine state). Every vote an engine broadcasts
// is recorded, and the invariant is checked after every step.

type fuzzNode struct {
	eng       *BFTEngine
	committed uint64 // what this node's executor has stored
}

type fuzzMsg struct {
	from int
	prop *ProposalMsg
	vote *Vote
	rc   *RoundChange
}

type fuzzHarness struct {
	t          *testing.T
	set        []ValidatorInfo
	keys       []*qcrypto.PrivateKey
	nodes      []*fuzzNode
	bus        []fuzzMsg
	sent       []*Vote // every vote every engine broadcast, with its author
	sender     []int
	salt       int64
	failCommit bool
	order      []ValidatorInfo
	rng        *rand.Rand
}

// record keeps the per-(validator,height,round,kind) vote history and reports
// the first conflict it sees.
func (h *fuzzHarness) conflict() string {
	type key struct {
		addr   qcrypto.Address
		height uint64
		round  uint32
		kind   string
	}
	first := map[key][32]byte{}
	for _, v := range h.sent {
		k := key{v.ValidatorAddr, v.Height, v.Round, v.Kind}
		if prev, ok := first[k]; ok {
			if prev != v.BlockHash {
				return fmt.Sprintf("%s signed two %s at height %d round %d: %x and %x",
					v.ValidatorAddr, v.Kind, v.Height, v.Round, prev[:4], v.BlockHash[:4])
			}
			continue
		}
		first[k] = v.BlockHash
	}
	return ""
}

func newFuzzHarness(t *testing.T, seed int64) *fuzzHarness {
	t.Helper()
	set, keys := testSet(t, 4)
	h := &fuzzHarness{t: t, set: set, keys: keys, salt: 1000, rng: rand.New(rand.NewSource(seed))}
	h.order = make([]ValidatorInfo, len(set))
	copy(h.order, set)
	for i := 0; i < 4; i++ {
		i := i
		j, err := OpenVoteJournal(filepath.Join(t.TempDir(), fmt.Sprintf("j%d.json", i)))
		if err != nil {
			t.Fatal(err)
		}
		node := &fuzzNode{}
		node.eng = twoPhaseFor(t, set, keys, i, BFTConfig{
			VoteJournal: j,
			OnCommit: func(b *types.Block) error {
				// A failing apply is the one path that clears e.proposal and
				// the in-memory lock while leaving (currentHeight,
				// currentRound) exactly where they are — and a node that only
				// prevoted has nothing in its journal to fall back on.
				if h.failCommit {
					return fmt.Errorf("injected apply failure at height %d", b.Header.Height)
				}
				if b.Header.Height > h.nodes[i].committed {
					h.nodes[i].committed = b.Header.Height
				}
				return nil
			},
			// The real linkage check: a node votes only on the block that
			// follows the head it has stored.
			ValidateProposal: func(b *types.Block) error {
				if b.Header.Height != h.nodes[i].committed+1 {
					return fmt.Errorf("height %d does not follow head %d",
						b.Header.Height, h.nodes[i].committed)
				}
				return nil
			},
			Broadcast: func(v *Vote) {
				cp := *v
				h.sent = append(h.sent, &cp)
				h.bus = append(h.bus, fuzzMsg{from: i, vote: &cp})
			},
			BroadcastProposal: func(p *ProposalMsg) {
				if p.Vote != nil {
					cp := *p.Vote
					h.sent = append(h.sent, &cp)
				}
				h.bus = append(h.bus, fuzzMsg{from: i, prop: p})
			},
			BroadcastRoundChange: func(rc *RoundChange) {
				cp := *rc
				h.bus = append(h.bus, fuzzMsg{from: i, rc: &cp})
			},
		})
		h.nodes = append(h.nodes, node)
		node.eng.SetHeight(1)
	}
	return h
}

// mint builds a block the leader of (height, round) would produce, on top of
// the parent that leader has stored.
func (h *fuzzHarness) mint(height uint64, round uint32) *types.Block {
	h.salt++
	return proposalAt(h.t, h.set, h.keys, height, round, h.salt).Block
}

// propose is MultiValidator.propose, including its unconditional SetHeight.
//
// careless skips the caller-side guards — the LockedProposal and
// AdoptedProposal shortcuts — and mints a fresh body instead. That is exactly
// what propose did before the same-round re-mint was fixed, and what any
// future caller that forgets would do. The engine, not its caller, has to be
// the thing that refuses: these runs are what tests that.
func (h *fuzzHarness) propose(i int, careless bool) {
	n := h.nodes[i]
	height := n.committed + 1
	n.eng.SetHeight(height)
	if !n.eng.IsLeader(height, n.eng.Round()) {
		return
	}
	if !careless {
		if locked := n.eng.LockedProposal(); locked != nil && locked.Header.Height == height {
			n.eng.handleProposal(&pendingProposal{block: locked, local: true})
			return
		}
		if n.eng.VoteLockedAt(height) {
			return
		}
		if held := n.eng.AdoptedProposal(); held != nil && held.Header.Height == height {
			n.eng.handleProposal(&pendingProposal{block: held, local: true})
			return
		}
	}
	n.eng.handleProposal(&pendingProposal{block: h.mint(height, n.eng.Round()), local: true})
}

// tick is the block-interval tick's height catch-up.
func (h *fuzzHarness) tick(i int) {
	n := h.nodes[i]
	if want := n.committed + 1; n.eng.Height() < want {
		n.eng.SetHeight(want)
	}
}

func (h *fuzzHarness) deliver(idx, to int) {
	m := h.bus[idx]
	if m.from == to {
		return
	}
	eng := h.nodes[to].eng
	switch {
	case m.prop != nil:
		eng.handleProposal(pendingProposalOf(m.prop))
	case m.vote != nil:
		cp := *m.vote
		eng.handleVote(&cp)
	case m.rc != nil:
		cp := *m.rc
		eng.handleRoundChange(&cp)
	}
}

// deliverAll hands one message to every engine but its sender, which is what
// a broadcast actually does.
func (h *fuzzHarness) deliverAll(idx int) {
	for to := range h.nodes {
		h.deliver(idx, to)
	}
}

func (h *fuzzHarness) step() {
	i := h.rng.Intn(4)
	switch n := h.rng.Intn(100); {
	case n < 12:
		h.propose(i, h.rng.Intn(2) == 0)
	case n < 18:
		h.tick(i)
	case n < 21:
		// A round timeout. Rare: in production the deadline only fires when
		// the engine is genuinely waiting, and only after roundTimeout.
		h.nodes[i].eng.handleTimeout()
	case n < 22:
		// The registry comes back in a different order. LeaderAddress indexes
		// into the set, so this changes who leads (height, round) — the way
		// two nodes can both believe they lead one round. Applied to every
		// node, as a real registry change would be.
		h.rng.Shuffle(len(h.order), func(a, b int) {
			h.order[a], h.order[b] = h.order[b], h.order[a]
		})
		for _, n := range h.nodes {
			cp := make([]ValidatorInfo, len(h.order))
			copy(cp, h.order)
			n.eng.UpdateValidatorSet(cp)
		}
	case n < 24:
		// Apply starts failing, and later stops.
		h.failCommit = !h.failCommit
	default:
		if len(h.bus) == 0 {
			return
		}
		idx := h.rng.Intn(len(h.bus))
		// Mostly a real broadcast; sometimes a single straggling copy, which
		// is what reorders and duplicates messages against each receiver.
		if h.rng.Intn(4) == 0 {
			h.deliver(idx, i)
		} else {
			h.deliverAll(idx)
		}
		if h.rng.Intn(2) == 0 {
			h.bus = append(h.bus[:idx], h.bus[idx+1:]...)
		}
		if len(h.bus) > 400 {
			h.bus = h.bus[len(h.bus)-200:]
		}
	}
}

func TestTwoPhaseNoSelfEquivocationUnderRandomInterleavings(t *testing.T) {
	maxCommitted, maxRound, commits := uint64(0), uint32(0), 0
	for seed := int64(1); seed <= 400; seed++ {
		h := newFuzzHarness(t, seed)
		for s := 0; s < 1500; s++ {
			h.step()
			if c := h.conflict(); c != "" {
				t.Fatalf("seed %d step %d: %s", seed, s, c)
			}
		}
		for _, n := range h.nodes {
			if n.committed > maxCommitted {
				maxCommitted = n.committed
			}
			commits += int(n.committed)
			if r := n.eng.Round(); r > maxRound {
				maxRound = r
			}
		}
	}
	t.Logf("coverage: total commits %d, deepest height %d, deepest round %d", commits, maxCommitted, maxRound)
	if commits == 0 {
		t.Fatal("the harness never committed anything; it proves nothing")
	}
	if maxRound == 0 {
		t.Fatal("the harness never left round 0; it proves nothing")
	}
}
