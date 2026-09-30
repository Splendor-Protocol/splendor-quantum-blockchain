package consensus

import (
	"errors"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A signed round-change can advertise any lock round. Only a prevote quorum
// that VERIFIES for the advertised (height, round, hash) justifies that lock,
// so a fabricated high round must not out-rank a lower round that really
// reached a quorum.
func TestRoundChangeIgnoresUnverifiedHighLock(t *testing.T) {
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(5)

	hashA := [32]byte{0xAA}
	hashB := [32]byte{0xBB}

	// A fabricated lock at a high round with no certificate.
	rcFab := &RoundChange{Height: 5, Round: 2, LockedHash: hashA, LockedRound: 9, ValidatorAddr: set[0].Address, Timestamp: 1}
	if err := rcFab.Sign(keys[0]); err != nil {
		t.Fatal(err)
	}
	// A genuine lock at a LOWER round carrying a verifiable prevote quorum.
	polB := make([]*Vote, 3)
	for i := 0; i < 3; i++ {
		polB[i] = &Vote{Height: 5, Round: 1, BlockHash: hashB, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := polB[i].Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
	}
	rcJust := &RoundChange{Height: 5, Round: 2, LockedHash: hashB, LockedRound: 1, ValidatorAddr: set[1].Address, Timestamp: 2, POLVotes: polB}
	if err := rcJust.Sign(keys[1]); err != nil {
		t.Fatal(err)
	}
	// A no-lock round-change completes the quorum.
	rcNone := &RoundChange{Height: 5, Round: 2, LockedRound: -1, ValidatorAddr: set[2].Address, Timestamp: 3}
	if err := rcNone.Sign(keys[2]); err != nil {
		t.Fatal(err)
	}

	eng.addRoundChangeLocked(rcFab)
	eng.addRoundChangeLocked(rcJust)
	eng.addRoundChangeLocked(rcNone)

	hash, round, votes, ok := eng.JustifiedValue()
	if !ok {
		t.Fatal("no justified value selected")
	}
	if hash != hashB || round != 1 {
		t.Fatalf("JustifiedValue = (%x, %d), want (%x, 1): a fabricated high lock must not out-rank a verified quorum", hash[:8], round, hashB[:8])
	}
	if len(votes) != 3 || !eng.verifyPOL(5, 1, hashB, votes) {
		t.Fatal("selected certificate must be the verified quorum for B@1")
	}
}

// A round-change selected value takes precedence over this node's own lock: a
// leader that holds body A must not re-propose A when the quorum selected B.
func TestLockedProposalPrefersSelectedValueOverOwnLock(t *testing.T) {
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(4)

	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	hashB := [32]byte{0xBB}

	// This node vote-locked A (and holds the body)...
	eng.lockedProposal = blockA
	eng.hasVoted = true
	eng.votedHash = blockA.Hash
	eng.votedHeight = 4

	// ...but a round-change selected a DIFFERENT value B.
	eng.rcValueSet = true
	eng.rcValueHash = hashB
	eng.rcValueRound = 1

	if got := eng.LockedProposal(); got != nil {
		t.Fatalf("LockedProposal returned %x when the selected value %x differs; want nil so the selected body is fetched", got.Hash[:8], hashB[:8])
	}

	// And when the selected value IS the held body, it is returned.
	eng.rcValueHash = blockA.Hash
	if got := eng.LockedProposal(); got == nil || got.Hash != blockA.Hash {
		t.Fatal("LockedProposal must return the held body when it matches the selected value")
	}
}

// The leader fetches the body of a round-change-selected value it does not
// hold, instead of holding forever or minting a conflicting block.
func TestProposeFetchesRoundChangeSelectedBody(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(1)

	blockB := proposalAt(t, set, keys, 1, 0, 22).Block
	hashB := blockB.Hash

	polB := make([]*Vote, 3)
	for i := 0; i < 3; i++ {
		polB[i] = &Vote{Height: 1, Round: 0, BlockHash: hashB, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := polB[i].Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		rc := &RoundChange{Height: 1, Round: 1, LockedHash: hashB, LockedRound: 0, ValidatorAddr: set[i].Address, Timestamp: int64(i), POLVotes: polB}
		if err := rc.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.addRoundChangeLocked(rc)
	}
	if h, _, _, ok := eng.JustifiedValue(); !ok || h != hashB {
		t.Fatalf("JustifiedValue = %x ok=%v, want %x", h[:8], ok, hashB[:8])
	}

	var fetchedHash [32]byte
	mv := NewMultiValidator(Config{
		Address: set[0].Address, PublicKey: set[0].PublicKey, PrivateKey: keys[0],
		FetchBlock: func(height uint64, hash [32]byte) (*types.Block, error) {
			fetchedHash = hash
			return blockB, nil
		},
	}, eng, ex, pool, nil, sdb)

	if err := mv.propose(); err != nil {
		t.Fatal(err)
	}
	if fetchedHash != hashB {
		t.Fatalf("fetch hook called with hash %x, want %x", fetchedHash[:8], hashB[:8])
	}
	select {
	case p := <-eng.proposeCh:
		if p.block == nil || p.block.Hash != hashB {
			t.Fatalf("proposed %x, want the fetched selected body %x", p.block.Hash[:8], hashB[:8])
		}
	default:
		t.Fatal("propose() did not enqueue the fetched selected body")
	}
}

// When no peer can supply the selected body, the leader holds rather than
// minting a conflicting block.
func TestProposeHoldsWhenSelectedBodyUnavailable(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{})
	eng.SetHeight(1)

	hashB := [32]byte{0xBB}
	polB := make([]*Vote, 3)
	for i := 0; i < 3; i++ {
		polB[i] = &Vote{Height: 1, Round: 0, BlockHash: hashB, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := polB[i].Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		rc := &RoundChange{Height: 1, Round: 1, LockedHash: hashB, LockedRound: 0, ValidatorAddr: set[i].Address, Timestamp: int64(i), POLVotes: polB}
		if err := rc.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.addRoundChangeLocked(rc)
	}

	mv := NewMultiValidator(Config{
		Address: set[0].Address, PublicKey: set[0].PublicKey, PrivateKey: keys[0],
		FetchBlock: func(uint64, [32]byte) (*types.Block, error) {
			return nil, errors.New("peer does not have it")
		},
	}, eng, ex, pool, nil, sdb)

	if err := mv.propose(); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-eng.proposeCh:
		t.Fatalf("propose() queued a block %x instead of holding for the unavailable selected body", p.block.Hash[:8])
	default:
		// held — nothing queued.
	}
}
