package consensus

import (
	"encoding/json"
	"fmt"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// MaxRoundAdvance bounds how far ahead of its own round a node will follow a
// proposer.
//
// Leader rotation is (height+round) % n, so every member is the leader at some
// round: a member that could name any round could take the proposal slot at
// will. A view change here carries no certificate proving the previous round
// really timed out, so this cap is the only thing bounding that. It costs
// nothing in practice — rounds advance one timeout at a time — and it is a
// liveness bound, not a safety one: a proposal still needs 2/3+1 votes from
// the set before anything is committed.
const MaxRoundAdvance = 32

// ProposalMsg is the wire form of a BFT proposal: the block, plus the
// proposer's own signed vote for it.
//
// The block alone cannot say which ROUND it was proposed in, and the round is
// what decides who the leader is. A receiver therefore judged every proposal
// against its own round, so a node whose timer had fired once rejected the
// round-0 leader as a non-leader — and since nothing resynchronised the two,
// it stayed that way for the life of the process.
//
// The proposer's vote is what authenticates the round: it is signed over
// (height, round, block hash) under the vote domain, by the same key that
// sealed the block. It also removes a race — the leader's vote used to be a
// separate broadcast, and a receiver that got it before the proposal dropped
// it outright.
type ProposalMsg struct {
	Block *types.Block `json:"block"`
	Vote  *Vote        `json:"vote,omitempty"`
	// Envelope is signed by the current-round leader (bft_rounds_v1). The
	// block's ValidatorAddr may be the original producer of a locked value.
	Envelope *ProposalEnvelope `json:"envelope,omitempty"`
	// POL is the prevote quorum that justifies a locked or re-proposed value.
	POL []*Vote `json:"pol,omitempty"`
}

// Encode serialises the envelope for the sealed proposal channel.
func (p *ProposalMsg) Encode() ([]byte, error) { return json.Marshal(p) }

// DecodeProposal reads either wire form.
//
// The envelope is preferred. A bare block — what a peer running an older
// binary sends — is still accepted and treated as carrying no round, which is
// exactly the pre-existing behaviour for that peer: the receiver judges it
// against its own round.
func DecodeProposal(body []byte) (*ProposalMsg, error) {
	var msg ProposalMsg
	if err := json.Unmarshal(body, &msg); err == nil && msg.Block != nil {
		return &msg, nil
	}
	var bare types.Block
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("invalid proposal: %w", err)
	}
	if bare.Header.AlgID == "" && bare.Header.Height == 0 && len(bare.ValidatorSignature) == 0 {
		return nil, fmt.Errorf("invalid proposal: neither a proposal envelope nor a block")
	}
	return &ProposalMsg{Block: &bare}, nil
}

// pendingProposal is a proposal as the engine's loop sees it: the block, and
// the round it was proposed in when that round is known and authenticated.
type pendingProposal struct {
	block *types.Block

	// vote is the proposer's own vote. nil for a locally built proposal (the
	// engine casts it) and for a bare block from an older peer.
	vote *Vote

	envelope *ProposalEnvelope
	pol      []*Vote

	// local marks a proposal this node is making, which is proposed in this
	// node's current round rather than one read off the wire.
	local bool
}

// pendingProposalOf adapts a decoded wire message for the engine. Exported
// behaviour goes through SubmitProposal; this is the shared conversion.
func pendingProposalOf(msg *ProposalMsg) *pendingProposal {
	if msg == nil {
		return nil
	}
	return &pendingProposal{block: msg.Block, vote: msg.Vote, envelope: msg.Envelope, pol: msg.POL}
}
