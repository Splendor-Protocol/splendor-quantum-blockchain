package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// proposalAt builds the block the leader for (height, round) would propose,
// signed by that leader, plus the leader's own vote for it.
func proposalAt(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, height uint64, round uint32, salt int64) *ProposalMsg {
	t.Helper()
	idx := int(height+uint64(round)) % len(set)
	b := &types.Block{Header: types.BlockHeader{
		Height:        height,
		Timestamp:     salt,
		ChainID:       types.ChainID,
		AlgID:         qcrypto.AlgMLDSA65,
		TxMerkleRoot:  qcrypto.Hash32([]byte{}),
		ValidatorAddr: set[idx].Address,
	}}
	h, err := b.ComputeVotedHash()
	if err != nil {
		t.Fatal(err)
	}
	b.Hash = h
	if err := b.SignBlock(keys[idx]); err != nil {
		t.Fatal(err)
	}
	v := &Vote{Height: height, Round: round, BlockHash: b.Hash, ValidatorAddr: set[idx].Address, Timestamp: salt}
	if err := v.Sign(keys[idx]); err != nil {
		t.Fatal(err)
	}
	return &ProposalMsg{Block: b, Vote: v}
}

func engineFor(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, me int, cfg BFTConfig) *BFTEngine {
	t.Helper()
	cfg.LocalAddr = set[me].Address
	cfg.LocalPubKey = set[me].PublicKey
	cfg.LocalPrivKey = keys[me]
	if cfg.RoundTimeout == 0 {
		cfg.RoundTimeout = time.Hour
	}
	eng, err := NewBFTEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.UpdateValidatorSet(set)
	return eng
}

// The drift that used to be permanent: a node whose round timer fired once
// judged every later proposal against ITS round and rejected the round-0
// leader as a non-leader. The proposal now carries the round its proposer used,
// signed, so a node behind on rounds catches up instead of deadlocking.
func TestProposalCarriesRoundAndResynchronisesTheReceiver(t *testing.T) {
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 1, BFTConfig{})
	eng.SetHeight(7)

	// This node has timed out twice and is at round 2; the leader is still at
	// round 0 and proposes there.
	eng.handleTimeout()
	eng.handleTimeout()
	if got := eng.Round(); got != 2 {
		t.Fatalf("round = %d after two timeouts, want 2", got)
	}
	stale := proposalAt(t, set, keys, 7, 0, 1)
	eng.handleProposal(pendingProposalOf(stale))
	if eng.voteSet != nil {
		t.Fatal("a proposal from a round the receiver has already left must be refused")
	}

	// The leader times out too and re-proposes at round 2. The receiver must
	// take it, and must have voted on it.
	live := proposalAt(t, set, keys, 7, 2, 2)
	eng.handleProposal(pendingProposalOf(live))
	if eng.voteSet == nil {
		t.Fatal("proposal at the receiver's own round was refused")
	}
	if eng.Round() != 2 || eng.Height() != 7 {
		t.Fatalf("engine at %d/%d, want 7/2", eng.Height(), eng.Round())
	}

	// And the other direction: a receiver at round 0 must adopt a proposal
	// from a leader that has already moved on to round 1.
	behind := engineFor(t, set, keys, 0, BFTConfig{})
	behind.SetHeight(7)
	ahead := proposalAt(t, set, keys, 7, 1, 3)
	behind.handleProposal(pendingProposalOf(ahead))
	if behind.voteSet == nil {
		t.Fatal("a receiver at round 0 refused the round-1 leader: rounds never resynchronise")
	}
	if behind.Round() != 1 {
		t.Fatalf("receiver round = %d, want it to adopt the proposer's 1", behind.Round())
	}
}

// The proposer's own vote rides with the proposal, so the two can never be
// reordered on the wire — the leader's vote used to be a separate broadcast
// that was dropped outright when it arrived first. With two validators that
// makes the proposal alone enough to complete the quorum on the receiver.
func TestProposalCarriesTheProposersVote(t *testing.T) {
	set, keys := testSet(t, 2)
	committed := make(chan *types.Block, 1)
	eng := engineFor(t, set, keys, 1, BFTConfig{
		OnCommit: func(b *types.Block) error { committed <- b; return nil },
	})
	eng.SetHeight(4)

	msg := proposalAt(t, set, keys, 4, 0, 1)
	eng.handleProposal(pendingProposalOf(msg))

	select {
	case b := <-committed:
		voters := map[qcrypto.Address]bool{}
		for _, v := range b.Votes {
			voters[v.ValidatorAddr] = true
		}
		if !voters[set[0].Address] || !voters[set[1].Address] || len(voters) != 2 {
			t.Fatalf("certificate has %d voters, want the proposer's and this node's", len(voters))
		}
	default:
		t.Fatal("the proposer's bundled vote did not complete the quorum")
	}
}

// A proposal whose bundled vote is not the proposer's, or is signed by
// somebody else, or names another block, must be refused outright: the round
// it carries decides who the leader is, so an unauthenticated round is a
// leader-selection primitive for anyone who can reach the port.
func TestProposalWithAnUnauthenticatedRoundIsRefused(t *testing.T) {
	set, keys := testSet(t, 2)

	forged := proposalAt(t, set, keys, 4, 0, 1)
	forged.Vote.Round = 9 // not what was signed
	eng := engineFor(t, set, keys, 1, BFTConfig{})
	eng.SetHeight(4)
	eng.handleProposal(pendingProposalOf(forged))
	if eng.voteSet != nil {
		t.Fatal("a proposal whose round was altered after signing was accepted")
	}

	wrongVoter := proposalAt(t, set, keys, 4, 0, 2)
	other := &Vote{Height: 4, Round: 0, BlockHash: wrongVoter.Block.Hash, ValidatorAddr: set[1].Address, Timestamp: 2}
	if err := other.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	wrongVoter.Vote = other
	eng2 := engineFor(t, set, keys, 1, BFTConfig{})
	eng2.SetHeight(4)
	eng2.handleProposal(pendingProposalOf(wrongVoter))
	if eng2.voteSet != nil {
		t.Fatal("a proposal carrying somebody else's vote was accepted")
	}

	// A round far beyond the receiver's is refused: rotation makes every
	// member the leader at some round, so an unbounded jump lets any member
	// seize the proposal slot whenever it likes.
	far := proposalAt(t, set, keys, 4, MaxRoundAdvance+1, 3)
	eng3 := engineFor(t, set, keys, 1, BFTConfig{})
	eng3.SetHeight(4)
	eng3.handleProposal(pendingProposalOf(far))
	if eng3.voteSet != nil {
		t.Fatalf("a proposal %d rounds ahead was accepted", MaxRoundAdvance+1)
	}
}

// A node that is running BFT but is not IN the validator set — a validator
// that has joined and not yet registered — must not vote. It used to cast one,
// which with a single registered validator was a quorum of one: it committed
// the leader's block under a certificate consisting of its own vote, so two
// honest nodes stored different certificates for the same block.
//
// Applying a block whose certificate is real is still right for such a node.
// What must never happen is manufacturing that certificate from its own vote.
func TestNonMemberDoesNotVote(t *testing.T) {
	set, keys := testSet(t, 1)
	outsiderPriv, outsiderPK, outsiderAddr := testKey(t)

	committed := make(chan *types.Block, 4)
	newOutsider := func(height uint64) *BFTEngine {
		eng, err := NewBFTEngine(BFTConfig{
			LocalAddr: outsiderAddr, LocalPubKey: outsiderPK, LocalPrivKey: outsiderPriv,
			RoundTimeout: time.Hour,
			OnCommit:     func(b *types.Block) error { committed <- b; return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		eng.UpdateValidatorSet(set) // the outsider is NOT in it
		eng.SetHeight(height)
		return eng
	}

	// A bare block — no certificate with it — must not be committed by a node
	// that is not entitled to certify anything. This is the regression: the
	// outsider used to vote, reach a quorum of one, and commit.
	bare := proposalAt(t, set, keys, 3, 0, 1)
	bare.Vote = nil
	eng := newOutsider(3)
	eng.handleProposal(pendingProposalOf(bare))
	select {
	case b := <-committed:
		t.Fatalf("a node outside the validator set committed block %d on its own vote", b.Header.Height)
	default:
	}
	if eng.voteSet == nil {
		t.Fatal("a non-member must still track the proposal so it can count real votes")
	}
	for _, v := range eng.voteSet.Votes() {
		if v.ValidatorAddr == outsiderAddr {
			t.Fatal("a node outside the validator set voted")
		}
	}

	// The same proposal WITH the sole validator's vote is a real 1-of-1
	// certificate: applying it is correct, and the certificate stored is the
	// validator's, not the outsider's.
	eng = newOutsider(3)
	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 3, 0, 2)))
	select {
	case b := <-committed:
		if len(b.Votes) != 1 || b.Votes[0].ValidatorAddr != set[0].Address {
			t.Fatalf("certificate is %v, want exactly the sole validator's vote", b.Votes)
		}
	default:
		t.Fatal("a genuine one-vote certificate was not applied")
	}

	// Once it is in the set, it votes.
	eng = newOutsider(4)
	eng.UpdateValidatorSet(append(append([]ValidatorInfo{}, set...), ValidatorInfo{
		Address: outsiderAddr, PublicKey: outsiderPK, IsActive: true,
	}))
	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 4, 0, 3)))
	voted := false
	if eng.voteSet != nil {
		for _, v := range eng.voteSet.Votes() {
			if v.ValidatorAddr == outsiderAddr {
				voted = true
			}
		}
	}
	select {
	case b := <-committed: // the two votes are a quorum, so the set is already cleared
		for _, v := range b.Votes {
			if v.ValidatorAddr == outsiderAddr {
				voted = true
			}
		}
	default:
	}
	if !voted {
		t.Fatal("a registered member did not vote on the leader's proposal")
	}
}

// Advancing the height must reset the round. Without this a node that timed
// out once, and then took the next blocks by push or pull-sync rather than by
// its own commit, stayed a round ahead for the life of the process.
func TestAdvancingHeightResetsTheRound(t *testing.T) {
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(5)
	eng.handleTimeout()
	if eng.Round() == 0 {
		t.Fatal("timeout did not advance the round")
	}
	eng.SetHeight(6)
	if eng.Round() != 0 {
		t.Fatalf("round = %d after moving to a new height, want 0", eng.Round())
	}
	// Setting the SAME height must not clear a round the node reached by
	// timing out: MultiValidator.propose calls SetHeight before every proposal.
	eng.handleTimeout()
	r := eng.Round()
	eng.SetHeight(6)
	if eng.Round() != r {
		t.Fatalf("round = %d after re-setting the same height, want %d", eng.Round(), r)
	}
}

// A proposal for a height the node has already left must not drag it back.
func TestStaleHeightProposalIsIgnored(t *testing.T) {
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 1, BFTConfig{})
	eng.SetHeight(20)
	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 11, 0, 1)))
	if eng.Height() != 20 {
		t.Fatalf("engine height = %d after a replayed proposal for 11, want 20", eng.Height())
	}
	if eng.voteSet != nil {
		t.Fatal("the engine voted on a height it had already committed")
	}
}

// A vote that overtakes the proposal it belongs to must be held, not dropped.
// MultiValidator broadcast the proposal from outside the engine, so a peer's
// vote could reach the leader before the leader's own VoteSet existed; the
// vote was discarded and the height ran to timeout.
func TestVoteArrivingBeforeTheProposalIsNotLost(t *testing.T) {
	set, keys := testSet(t, 2)
	committed := make(chan *types.Block, 1)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		OnCommit: func(b *types.Block) error { committed <- b; return nil },
	})
	eng.SetHeight(8)

	// Height 8, round 0 → leader is set[0], this node. Build the block it
	// would propose, and deliver the OTHER validator's vote for it first.
	msg := proposalAt(t, set, keys, 8, 0, 1)
	early := &Vote{Height: 8, Round: 0, BlockHash: msg.Block.Hash, ValidatorAddr: set[1].Address, Timestamp: 1}
	if err := early.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	eng.handleVote(early)
	eng.handleProposal(pendingProposalOf(msg))

	select {
	case <-committed:
	default:
		t.Fatalf("quorum not reached: the early vote was dropped (votes=%d)", eng.voteSet.VoteCount())
	}
}

// Buffered votes must not become an unbounded memory sink for anyone holding
// the write-plane token.
func TestPendingVoteBufferIsBounded(t *testing.T) {
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(1)
	for h := uint64(1); h < 400; h++ {
		v := &Vote{Height: h, Round: 0, BlockHash: [32]byte{byte(h)}, ValidatorAddr: set[1].Address, Timestamp: 1}
		if err := v.Sign(keys[1]); err != nil {
			t.Fatal(err)
		}
		eng.handleVote(v)
	}
	if n := eng.pendingVoteCount(); n > maxPendingVotes {
		t.Fatalf("pending vote buffer holds %d votes, cap is %d", n, maxPendingVotes)
	}
	if n := len(eng.seenVotes); n > maxSeenVotes {
		t.Fatalf("equivocation table holds %d entries, cap is %d", n, maxSeenVotes)
	}
}

// The engine broadcasts its own proposal itself, after its VoteSet exists.
// MultiValidator used to broadcast right after handing the block to a buffered
// channel, so peers could answer before the leader was ready to count.
func TestEngineBroadcastsItsOwnProposalAfterTheVoteSetExists(t *testing.T) {
	set, keys := testSet(t, 2)
	seen := make(chan *ProposalMsg, 4)
	eng := engineFor(t, set, keys, 0, BFTConfig{
		BroadcastProposal: func(m *ProposalMsg) { seen <- m },
	})
	eng.SetHeight(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	msg := proposalAt(t, set, keys, 8, 0, 1) // height 8, round 0 → set[0] leads
	eng.ProposeBlock(msg.Block)

	select {
	case got := <-seen:
		if got.Vote == nil || got.Vote.ValidatorAddr != set[0].Address || got.Vote.Round != 0 {
			t.Fatalf("broadcast proposal carries a wrong or missing proposer vote: %+v", got.Vote)
		}
		if got.Block.Hash != msg.Block.Hash {
			t.Fatal("broadcast proposal is not the block that was proposed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the engine never broadcast its own proposal")
	}

	// A proposal this node is not the leader of must not be rebroadcast.
	other := proposalAt(t, set, keys, 9, 0, 2) // height 9 → set[1] leads
	eng.SubmitProposal(other)
	select {
	case got := <-seen:
		t.Fatalf("rebroadcast a peer's proposal at height %d", got.Block.Header.Height)
	case <-time.After(300 * time.Millisecond):
	}
}

// Decoding accepts the envelope and, for a peer that predates it, a bare block.
func TestDecodeProposalAcceptsBothWireForms(t *testing.T) {
	set, keys := testSet(t, 2)
	msg := proposalAt(t, set, keys, 3, 1, 1)

	enc, err := msg.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeProposal(enc)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if got.Vote == nil || got.Vote.Round != 1 || got.Block.Hash != msg.Block.Hash {
		t.Fatalf("envelope round-trip lost data: %+v", got)
	}

	bare, err := json.Marshal(msg.Block)
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeProposal(bare)
	if err != nil {
		t.Fatalf("bare block: %v", err)
	}
	if got.Vote != nil {
		t.Fatal("a bare block cannot carry a vote")
	}
	if got.Block.Hash != msg.Block.Hash {
		t.Fatal("bare block round-trip lost the block")
	}
	if _, err := DecodeProposal([]byte(`{"block":null}`)); err == nil {
		t.Fatal("an envelope with no block was accepted")
	}
}

// A commit that fails must not wedge the height.
//
// Reaching a quorum set a lock, and commitBlock returned early when the apply
// callback failed — leaving the lock in place. Every later proposal at that
// height carries a different hash (a new timestamp at least), so it was
// rejected as "conflicts with locked block", for ever. The live three-node run
// hit it at the set transition: one node sized a vote set from a registry it
// had not refreshed, committed on one vote where the chain wanted two, and the
// height never moved again.
func TestFailedCommitDoesNotWedgeTheHeight(t *testing.T) {
	set, keys := testSet(t, 2)
	fail := true
	eng := engineFor(t, set, keys, 1, BFTConfig{
		OnCommit: func(*types.Block) error {
			if fail {
				return errors.New("not applied")
			}
			return nil
		},
	})
	eng.SetHeight(4)

	// Height 4, round 0 → set[0] leads. Its bundled vote plus this node's is a
	// quorum, so the commit runs — and fails.
	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 4, 0, 1)))
	if eng.hasLock {
		t.Fatal("a failed commit left the height locked: nothing was committed, so there is nothing to be locked on")
	}

	// The leader tries again with a different block at the same height.
	fail = false
	committed := false
	eng.onCommit = func(*types.Block) error { committed = true; return nil }
	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 4, 0, 2)))
	if !committed {
		t.Fatal("the retry at the same height was refused after an earlier commit failed")
	}
}

// The engine sizes a vote set from the validator set it holds, and the
// executor judges the resulting certificate against the registry in the
// block's parent state. When a registration lands, those two disagree until
// the next refresh — and a quorum of one where the chain wants two is a commit
// that always fails. The engine therefore re-reads the registry before it
// sizes anything.
func TestProposalRefreshesTheValidatorSetBeforeSizingTheQuorum(t *testing.T) {
	set, keys := testSet(t, 2)
	eng := engineFor(t, set, keys, 1, BFTConfig{})
	// The engine starts with a stale one-member view; the refresh hook is what
	// the node wires to MultiValidator.RefreshSet.
	eng.UpdateValidatorSet(set[:1])
	eng.refreshSet = func() { eng.UpdateValidatorSet(set) }
	eng.SetHeight(4)

	eng.handleProposal(pendingProposalOf(proposalAt(t, set, keys, 4, 0, 1)))
	if eng.ValidatorCount() != 2 {
		t.Fatalf("validator set is %d after a proposal, want the refreshed 2", eng.ValidatorCount())
	}
	if eng.voteSet != nil && eng.voteSet.requiredWeight != 2 {
		t.Fatalf("quorum sized at %d from a stale set, want 2", eng.voteSet.requiredWeight)
	}
}
