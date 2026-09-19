package consensus

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func (e *BFTEngine) roundsV1(height uint64) bool {
	return e.roundsActive != nil && e.roundsActive(height)
}

func (e *BFTEngine) handleProposalTwoPhase(p *pendingProposal) bool {
	b := p.block
	if e.refreshSet != nil {
		e.refreshSet()
	}
	nValidators := e.activeCount()
	if nValidators == 0 {
		log.Error().Uint64("height", b.Header.Height).
			Msg("BFT: no validator set, cannot form a quorum for this proposal")
		return false
	}
	if e.isMember() && e.canProgress != nil && !e.canProgress() {
		log.Warn().Uint64("height", b.Header.Height).
			Msg("BFT: refusing to vote while apply is halted pending state reconciliation")
		return false
	}
	if b.Header.Height < e.currentHeight {
		return false
	}

	if p.local {
		if !e.IsLeader(b.Header.Height, e.currentRound) {
			log.Debug().Uint64("height", b.Header.Height).Uint32("round", e.currentRound).
				Msg("BFT: not the envelope leader, holding the local proposal")
			return false
		}
		if e.currentRound > 0 && e.rcQCRound != int32(e.currentRound) {
			if nValidators > 1 {
				log.Debug().Uint32("round", e.currentRound).
					Msg("BFT: waiting for a round-change QC before proposing")
				return false
			}
			e.ensureSoloRoundChangeQCLocked()
		}
		if locked, _, ok := e.lockAt(b.Header.Height); ok && b.Hash != locked {
			log.Warn().Hex("locked", locked[:8]).Hex("proposed", b.Hash[:8]).
				Msg("BFT: refusing to mint a value other than the locked body")
			return false
		}
		if err := e.signLocalEnvelope(p); err != nil {
			log.Error().Err(err).Msg("BFT: failed to sign proposal envelope")
			return false
		}
	}

	round, err := e.proposalRoundTwoPhase(p)
	if err != nil {
		log.Warn().Err(err).Uint64("height", b.Header.Height).Msg("BFT: two-phase proposal rejected")
		return false
	}

	// A view owns exactly one value.
	//
	// Replacing the value at a round this node has already prevoted in means
	// signing a second, conflicting prevote at that (height, round) — and
	// seenVotes is keyed validator|height|round|kind, so every peer answers
	// the second one by dropping that validator's prevotes for the rest of the
	// round ("equivocation — conflicting votes dropped"). An honest node did
	// this to itself: MultiValidator.propose mints a fresh body on every tick
	// until a prevote QC gives it something to lock to, so at a contended
	// height the leader handed the engine a second, different block at the
	// round it had already voted in. Every peer shown that block did the same.
	// A few ticks in, nobody's prevotes counted anywhere and the height was
	// finished. Replacing a value is what a round change is for.
	if b.Header.Height == e.currentHeight && round == e.currentRound && e.proposal != nil {
		if e.proposal.Hash != b.Hash {
			log.Warn().Uint64("height", b.Header.Height).Uint32("round", round).
				Hex("holding", e.proposal.Hash[:8]).Hex("offered", b.Hash[:8]).
				Bool("local", p.local).
				Msg("BFT: a second value at a view this node has already voted in; refused until a round change")
			return false
		}
		return e.answerDuplicateProposalLocked(p)
	}

	if !p.local {
		if err := e.admitRemoteValue(p, round); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).Uint32("round", round).
				Msg("BFT: two-phase proposal rejected")
			return false
		}
	}

	e.currentHeight = b.Header.Height
	e.currentRound = round
	e.proposal = b
	e.voteSet = newVoteSet(b.Header.Height, round, b.Hash, nValidators, types.VoteKindPrevote)
	e.precommits = newVoteSet(b.Header.Height, round, b.Hash, nValidators, types.VoteKindPrecommit)

	if p.vote != nil && p.vote.Kind == types.VoteKindPrevote {
		if _, addErr := e.voteSet.AddVote(p.vote); addErr != nil {
			log.Debug().Err(addErr).Msg("BFT: proposer's prevote not counted")
		}
	}

	var ownPrevote *Vote
	if e.isMember() {
		vote, verr := e.castPrevote(b, round)
		if verr != nil {
			log.Error().Err(verr).Msg("BFT: failed to prevote")
			return false
		}
		ownPrevote = vote
		if _, addErr := e.voteSet.AddVote(vote); addErr != nil {
			logVoteAddFailure(addErr, "own prevote")
		}
		if e.broadcast != nil {
			e.broadcast(vote)
		}
	}

	if p.local && e.broadcastP != nil {
		e.broadcastP(&ProposalMsg{Block: b, Vote: ownPrevote, Envelope: p.envelope, POL: p.pol})
	}

	e.drainPendingVotes()

	if e.voteSet.HasQuorum() {
		e.onPrevoteQuorum(b, round)
	}
	return true
}

// answerDuplicateProposalLocked handles a repeat of the proposal this node has
// already adopted at its current view — the leader's periodic rebroadcast of
// the body it is locked to.
//
// It re-sends the votes this node already signed there. The SAME votes: their
// bytes are unchanged, so a peer that has them counts a duplicate, and a peer
// that lost the first copy finally hears it. Signing fresh ones would change
// nothing but the timestamp, and re-opening the vote sets would throw away
// every prevote collected since.
//
// It reports the proposal as not newly adopted, which is what keeps the round
// deadline where it is: a leader that rebroadcasts faster than the round
// timeout must not be able to hold the view change off for ever.
func (e *BFTEngine) answerDuplicateProposalLocked(p *pendingProposal) bool {
	// Snapshot the value: commitBlock below can clear e.proposal, so reading
	// the field again after it would dereference nil.
	held := e.proposal
	round := e.currentRound
	prevote := e.voteSet.VoteFrom(e.localAddr)
	precommit := e.precommits.VoteFrom(e.localAddr)
	if p.local && e.broadcastP != nil {
		e.broadcastP(&ProposalMsg{Block: held, Vote: prevote, Envelope: p.envelope, POL: p.pol})
	}
	if e.broadcast != nil {
		if prevote != nil {
			e.broadcast(prevote)
		}
		if precommit != nil {
			e.broadcast(precommit)
		}
	}
	// A vote that overtook this copy on the wire still belongs to the set.
	e.drainPendingVotes()
	if e.precommits != nil && e.precommits.HasQuorum() {
		e.recordQuorum(held)
		e.commitBlock(held)
	} else if e.voteSet != nil && e.voteSet.HasQuorum() && !e.hasPOL {
		e.onPrevoteQuorum(held, round)
	}
	log.Debug().Uint64("height", held.Header.Height).Uint32("round", round).
		Hex("hash", held.Hash[:8]).
		Msg("BFT: duplicate proposal at the current view; re-sent this node's votes")
	return false
}

func (e *BFTEngine) signLocalEnvelope(p *pendingProposal) error {
	if p.envelope != nil {
		return nil
	}
	env := &ProposalEnvelope{
		Height:     p.block.Header.Height,
		Round:      e.currentRound,
		BlockHash:  p.block.Hash,
		LeaderAddr: e.localAddr,
		POLRound:   -1,
		Timestamp:  time.Now().Unix(),
	}
	if hash, round, ok := e.lockAt(p.block.Header.Height); ok {
		env.POLRound = int32(round)
		env.POLHash = hash
	}
	if err := env.Sign(e.localPrivKey); err != nil {
		return err
	}
	p.envelope = env
	return nil
}

func (e *BFTEngine) proposalRoundTwoPhase(p *pendingProposal) (uint32, error) {
	if p.local {
		return e.currentRound, nil
	}
	env := p.envelope
	if env == nil {
		return 0, fmt.Errorf("bft_rounds_v1 proposal missing envelope")
	}
	b := p.block
	if env.Height != b.Header.Height || env.BlockHash != b.Hash {
		return 0, fmt.Errorf("envelope does not match the proposed block")
	}
	leader, ok := e.LeaderAddress(env.Height, env.Round)
	if !ok || env.LeaderAddr != leader {
		return 0, fmt.Errorf("envelope leader %s is not scheduled for height %d round %d",
			env.LeaderAddr, env.Height, env.Round)
	}
	pk := e.findValidatorPubKey(env.LeaderAddr)
	if pk == nil {
		return 0, fmt.Errorf("envelope leader %s is not in this node's validator set", env.LeaderAddr)
	}
	if err := env.Verify(pk); err != nil {
		return 0, err
	}
	if env.Round > e.currentRound+MaxRoundAdvance {
		return 0, fmt.Errorf("proposal is at round %d, this node is at %d (max advance %d)",
			env.Round, e.currentRound, MaxRoundAdvance)
	}
	if b.Header.Height == e.currentHeight && env.Round < e.currentRound {
		return 0, fmt.Errorf("proposal is at round %d, this node has already left it for %d",
			env.Round, e.currentRound)
	}
	return env.Round, nil
}

func (e *BFTEngine) admitRemoteValue(p *pendingProposal, round uint32) error {
	b := p.block
	pk := e.findValidatorPubKey(b.Header.ValidatorAddr)
	if pk == nil {
		return fmt.Errorf("no key for the original producer %s", b.Header.ValidatorAddr)
	}
	if err := b.VerifySignature(pk); err != nil {
		return fmt.Errorf("block signature invalid: %w", err)
	}
	if e.validateProposal != nil {
		if err := e.validateProposal(b); err != nil {
			return err
		}
	}
	if locked, lockRound, ok := e.lockAt(b.Header.Height); ok && b.Hash != locked {
		env := p.envelope
		if env == nil || env.POLRound < 0 || env.POLHash != b.Hash {
			return fmt.Errorf("locked to %x, proposal is %x with no later POL", locked[:8], b.Hash[:8])
		}
		if uint32(env.POLRound) <= lockRound || !e.verifyPOL(b.Header.Height, uint32(env.POLRound), b.Hash, p.pol) {
			return fmt.Errorf("proposal POL does not unlock the locked value")
		}
	}
	_ = round
	return nil
}

func (e *BFTEngine) verifyPOL(height uint64, round uint32, hash [32]byte, votes []*Vote) bool {
	n := e.activeCount()
	if n == 0 {
		return false
	}
	need := (n*2)/3 + 1
	seen := make(map[qcrypto.Address]struct{})
	ok := 0
	for _, v := range votes {
		if v == nil || v.Kind != types.VoteKindPrevote || v.Height != height || v.Round != round || v.BlockHash != hash {
			return false
		}
		pk := e.findValidatorPubKey(v.ValidatorAddr)
		if pk == nil || v.Verify(pk) != nil {
			return false
		}
		if _, dup := seen[v.ValidatorAddr]; dup {
			return false
		}
		seen[v.ValidatorAddr] = struct{}{}
		ok++
	}
	return ok >= need
}

func (e *BFTEngine) castPrevote(b *types.Block, round uint32) (*Vote, error) {
	// A prevote is not the value lock. Record it separately so a restart can
	// refuse a conflicting prevote at the SAME (height, round) without carrying
	// a bare prevote as a permanent per-height commitment.
	if e.journal != nil {
		if err := e.journal.RecordPrevote(b.Header.Height, round, b.Hash); err != nil {
			return nil, err
		}
	}
	vote := &Vote{
		Height:        b.Header.Height,
		Round:         round,
		BlockHash:     b.Hash,
		ValidatorAddr: e.localAddr,
		Timestamp:     time.Now().Unix(),
		Kind:          types.VoteKindPrevote,
	}
	if err := vote.Sign(e.localPrivKey); err != nil {
		return nil, err
	}
	return vote, nil
}

func (e *BFTEngine) castPrecommit(b *types.Block, round uint32, polRound uint32) (*Vote, error) {
	if e.journal != nil {
		if err := e.journal.RecordWithPOL(b.Header.Height, round, b.Hash, polRound); err != nil {
			return nil, err
		}
	}
	vote := &Vote{
		Height:        b.Header.Height,
		Round:         round,
		BlockHash:     b.Hash,
		ValidatorAddr: e.localAddr,
		Timestamp:     time.Now().Unix(),
		Kind:          types.VoteKindPrecommit,
	}
	if err := vote.Sign(e.localPrivKey); err != nil {
		return nil, err
	}
	e.signedHeight, e.signedRound, e.hasSigned = b.Header.Height, round, true
	e.votedHash, e.votedHeight, e.hasVoted = b.Hash, b.Header.Height, true
	e.lockedProposal = b
	if e.journal != nil {
		if err := e.journal.SaveProposal(b); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).
				Msg("BFT: failed to persist locked proposal body for rebroadcast")
		}
	}
	return vote, nil
}

func (e *BFTEngine) onPrevoteQuorum(b *types.Block, round uint32) {
	e.hasPOL = true
	e.polHash = b.Hash
	e.polRound = round
	e.polHeight = b.Header.Height
	e.lockedProposal = b
	if !e.isMember() {
		return
	}
	pc, err := e.castPrecommit(b, round, round)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to precommit after POL")
		return
	}
	if e.precommits == nil {
		e.precommits = newVoteSet(b.Header.Height, round, b.Hash, e.activeCount(), types.VoteKindPrecommit)
	}
	if _, addErr := e.precommits.AddVote(pc); addErr != nil {
		log.Debug().Err(addErr).Msg("BFT: own precommit not counted")
	}
	if e.broadcast != nil {
		e.broadcast(pc)
	}
	e.drainPendingVotes()
	if e.precommits.HasQuorum() {
		e.recordQuorum(b)
		e.commitBlock(b)
	}
}

func (e *BFTEngine) handleVoteTwoPhase(vote *Vote) {
	pubKey := e.findValidatorPubKey(vote.ValidatorAddr)
	if pubKey == nil {
		log.Warn().Str("from", vote.ValidatorAddr.String()).Msg("BFT: vote from unknown validator")
		return
	}
	if err := vote.Verify(pubKey); err != nil {
		log.Warn().Err(err).Str("from", vote.ValidatorAddr.String()).Msg("BFT: invalid vote signature")
		return
	}
	if vote.Kind != types.VoteKindPrevote && vote.Kind != types.VoteKindPrecommit {
		log.Debug().Str("kind", vote.Kind).Msg("BFT: ignoring non two-phase vote under bft_rounds_v1")
		return
	}

	key := voteSeenKey(vote)
	if prev, ok := e.seenVotes[key]; ok {
		if prev.BlockHash != vote.BlockHash {
			log.Warn().
				Str("from", vote.ValidatorAddr.String()).
				Str("kind", vote.Kind).
				Uint64("height", vote.Height).
				Msg("BFT: equivocation — conflicting votes dropped")
			return
		}
	} else {
		if len(e.seenVotes) >= maxSeenVotes {
			e.pruneSeenVotes(e.currentHeight)
		}
		if len(e.seenVotes) < maxSeenVotes {
			cp := *vote
			e.seenVotes[key] = &cp
		}
	}

	switch vote.Kind {
	case types.VoteKindPrevote:
		if e.voteSet == nil || vote.Height != e.currentHeight || vote.Round != e.currentRound {
			if vote.Height >= e.currentHeight {
				e.bufferVote(vote)
			}
			return
		}
		reached, err := e.voteSet.AddVote(vote)
		if err != nil {
			log.Debug().Err(err).Msg("BFT: rejected prevote")
			return
		}
		if reached && e.proposal != nil {
			e.onPrevoteQuorum(e.proposal, vote.Round)
		}
	case types.VoteKindPrecommit:
		if e.precommits == nil || vote.Height != e.currentHeight || vote.Round != e.currentRound {
			if vote.Height >= e.currentHeight {
				e.bufferVote(vote)
			}
			return
		}
		reached, err := e.precommits.AddVote(vote)
		if err != nil {
			log.Debug().Err(err).Msg("BFT: rejected precommit")
			return
		}
		if reached && e.proposal != nil {
			e.recordQuorum(e.proposal)
			e.commitBlock(e.proposal)
		}
	}
}

func (e *BFTEngine) viewChangeTwoPhaseLocked() bool {
	e.currentRound++
	e.voteSet = nil
	e.precommits = nil
	e.proposal = nil
	h, r := e.currentHeight, e.currentRound
	rc, err := e.signRoundChange(h, r)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to sign round-change")
	} else {
		e.addRoundChangeLocked(rc)
		if e.broadcastRC != nil {
			e.broadcastRC(rc)
		}
	}
	log.Warn().Uint64("height", h).Uint32("round", r).
		Msg("BFT: round timeout, two-phase view change")
	cb := e.onViewChange
	if cb != nil {
		go cb(h, r)
	}
	return true
}

func (e *BFTEngine) signRoundChange(height uint64, round uint32) (*RoundChange, error) {
	rc := &RoundChange{
		Height:        height,
		Round:         round,
		LockedRound:   -1,
		ValidatorAddr: e.localAddr,
		Timestamp:     time.Now().Unix(),
	}
	if hash, lr, ok := e.lockAt(height); ok {
		rc.LockedHash = hash
		rc.LockedRound = int32(lr)
	}
	if err := rc.Sign(e.localPrivKey); err != nil {
		return nil, err
	}
	return rc, nil
}

func (e *BFTEngine) addRoundChangeLocked(rc *RoundChange) {
	if e.rcByRound == nil {
		e.rcByRound = make(map[uint32]map[qcrypto.Address]*RoundChange)
	}
	m := e.rcByRound[rc.Round]
	if m == nil {
		m = make(map[qcrypto.Address]*RoundChange)
		e.rcByRound[rc.Round] = m
	}
	m[rc.ValidatorAddr] = rc
	need := (e.activeCount()*2)/3 + 1
	if len(m) >= need {
		e.rcQCRound = int32(rc.Round)
	}
}

func (e *BFTEngine) ensureSoloRoundChangeQCLocked() {
	if e.activeCount() != 1 {
		return
	}
	if e.rcQCRound == int32(e.currentRound) {
		return
	}
	rc, err := e.signRoundChange(e.currentHeight, e.currentRound)
	if err != nil {
		return
	}
	e.addRoundChangeLocked(rc)
}

func (e *BFTEngine) handleRoundChange(rc *RoundChange) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if rc == nil {
		return
	}
	pk := e.findValidatorPubKey(rc.ValidatorAddr)
	if pk == nil {
		return
	}
	if err := rc.Verify(pk); err != nil {
		log.Warn().Err(err).Msg("BFT: invalid round-change")
		return
	}
	if rc.Height != e.currentHeight {
		return
	}
	if rc.Round < e.currentRound || rc.Round > e.currentRound+MaxRoundAdvance {
		return
	}
	e.addRoundChangeLocked(rc)
	if e.rcQCRound == int32(e.currentRound) && e.IsLeader(e.currentHeight, e.currentRound) {
		if cb := e.onViewChange; cb != nil {
			h, r := e.currentHeight, e.currentRound
			go cb(h, r)
		}
	}
}

func (e *BFTEngine) lockAt(height uint64) (hash [32]byte, round uint32, ok bool) {
	if e.hasVoted && e.votedHeight == height {
		return e.votedHash, e.signedRound, true
	}
	if e.hasPOL && e.polHeight == height {
		return e.polHash, e.polRound, true
	}
	if e.journal != nil {
		if jh, jr, jhash, jok := e.journal.Last(); jok && jh == height {
			return jhash, jr, true
		}
	}
	return [32]byte{}, 0, false
}

// CanPropose reports whether a two-phase leader may issue a proposal now.
func (e *BFTEngine) CanPropose() bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if !e.roundsV1(e.currentHeight) {
		return true
	}
	if e.currentRound == 0 {
		return true
	}
	return e.rcQCRound == int32(e.currentRound)
}
