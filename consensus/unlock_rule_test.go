package consensus

import (
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Tendermint's unlock rule is three comparisons, not one:
//
//	LastLockRound < PoLC-Round < R
//
// A node locked on A at round L accepts a proposal for B only against a
// prevote quorum for B at some round strictly between L and the round being
// proposed. The MIDDLE relation is the one that is easy to drop, and dropping
// it is not a small thing: a proposal may then cite its OWN round as the round
// that proves it, and a value justifies itself. Two leaders at the same height
// can each unlock their peers onto their own block, which is the safety
// property the lock exists to provide.
//
// Both relations are checked here against the same locked engine so neither
// can be removed without a failure, and the certificate is deliberately valid
// in both cases — it is the round arithmetic under test, not the signatures.
func TestProposalCannotJustifyItselfWithItsOwnRound(t *testing.T) {
	set, keys := testSet(t, 4)
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{
		OnCommit:             func(b *types.Block) error { return nil },
		Broadcast:            func(v *Vote) {},
		BroadcastProposal:    func(p *ProposalMsg) {},
		BroadcastRoundChange: func(rc *RoundChange) {},
	})
	eng.SetHeight(4)

	// Lock the engine on A at round 0 by driving a real prevote quorum.
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !eng.handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader did not adopt A")
	}
	// handleVote, not SubmitVote: the exported path queues for the engine's
	// own loop, which is not running here.
	for i := 1; i < 3; i++ {
		eng.handleVote(prevoteFor(t, set, keys, i, 4, 0, blockA.Hash))
	}
	hash, lockRound, ok := eng.lockAt(4)
	if !ok || hash != blockA.Hash {
		t.Fatalf("expected a lock on A at height 4, got ok=%v", ok)
	}
	if lockRound != 0 {
		t.Fatalf("lock is at round %d, this test assumes round 0", lockRound)
	}

	// A different value B, offered with a certificate that really is for B.
	blockB := proposalAt(t, set, keys, 4, 1, 22).Block
	if blockB.Hash == blockA.Hash {
		t.Fatal("test needs two distinct blocks")
	}
	certFor := func(round uint32) []*Vote {
		var out []*Vote
		for i := 0; i < 3; i++ {
			out = append(out, prevoteFor(t, set, keys, i, 4, round, blockB.Hash))
		}
		return out
	}

	for _, tc := range []struct {
		name      string
		polRound  int32
		propRound uint32
		wantErr   string
	}{
		// The relation upstream's engine still enforces.
		{"POL no later than the lock", 0, 3, "no later than the lock"},
		// The relation it does not: a proposal at round 2 citing round 2.
		{"POL is the proposal's own round", 2, 2, "not earlier than the proposal round"},
		// And citing a round in its own future is no better.
		{"POL after the proposal round", 3, 2, "not earlier than the proposal round"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pendingProposal{
				block: blockB,
				envelope: &ProposalEnvelope{
					Height:    4,
					Round:     tc.propRound,
					BlockHash: blockB.Hash,
					POLRound:  tc.polRound,
					POLHash:   blockB.Hash,
				},
				pol: certFor(uint32(tc.polRound)),
			}
			got, err := eng.admitRemoteValue(p, tc.propRound)
			if err == nil {
				t.Fatalf("a proposal for B with POL round %d at round %d unlocked a node locked on A at round %d (verified POL %d)",
					tc.polRound, tc.propRound, lockRound, got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}

	// The honest shape still unlocks, so the rule refuses bad arithmetic
	// rather than refusing every unlock.
	ok2 := &pendingProposal{
		block: blockB,
		envelope: &ProposalEnvelope{
			Height: 4, Round: 3, BlockHash: blockB.Hash,
			POLRound: 1, POLHash: blockB.Hash,
		},
		pol: certFor(1),
	}
	polRound, err := eng.admitRemoteValue(ok2, 3)
	if err != nil {
		t.Fatalf("a genuine POL at round 1 for a round-3 proposal was refused: %v", err)
	}
	if polRound != 1 {
		t.Fatalf("verified POL round = %d, want 1", polRound)
	}
}

// prevoteFor builds a signed prevote from validator i. The certificate under
// test has to be genuine -- what is being checked is the round arithmetic, not
// the signatures, so a rejection must never be attributable to a bad vote.
func prevoteFor(t *testing.T, set []ValidatorInfo, keys []*qcrypto.PrivateKey, i int, height uint64, round uint32, hash [32]byte) *Vote {
	t.Helper()
	v := &Vote{
		Kind:          types.VoteKindPrevote,
		Height:        height,
		Round:         round,
		BlockHash:     hash,
		ValidatorAddr: set[i].Address,
		Timestamp:     1,
	}
	if err := v.Sign(keys[i]); err != nil {
		t.Fatal(err)
	}
	return v
}
