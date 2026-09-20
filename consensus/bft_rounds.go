package consensus

import (
	"bytes"
	"errors"
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
		// The round-change quorum, not the leader, decides the value. A leader
		// that reached this round through a view change re-proposes the
		// highest justified lock reported to it; only a quorum that proves no
		// lock leaves it free to offer its own body.
		sel, haveSel := e.safeValueLocked()
		if haveSel && b.Hash != sel.LockedHash {
			log.Warn().Uint64("height", b.Header.Height).Uint32("round", e.currentRound).
				Hex("justified", sel.LockedHash[:8]).Int32("justified_round", sel.LockedRound).
				Hex("offered", b.Hash[:8]).
				Msg("BFT: the round-change quorum justifies a different value; refusing to propose this one")
			return false
		}
		if locked, lockRound, ok := e.lockAt(b.Header.Height); ok && b.Hash != locked {
			// A leader unlocks on the same evidence a follower does: a proven
			// prevote quorum for the offered value at a strictly later round.
			// Without this the node holding the OLDEST lock refuses to lead
			// the round that would have resolved the height, and the set waits
			// for a leader rotation it does not need.
			if !(haveSel && sel.LockedHash == b.Hash && sel.LockedRound > int32(lockRound)) {
				log.Warn().Hex("locked", locked[:8]).Uint32("locked_round", lockRound).
					Hex("proposed", b.Hash[:8]).
					Msg("BFT: refusing to mint a value other than the locked body")
				return false
			}
			log.Info().Uint64("height", b.Header.Height).Hex("was_locked", locked[:8]).
				Uint32("lock_round", lockRound).Hex("now", b.Hash[:8]).Int32("pol_round", sel.LockedRound).
				Msg("BFT: leaving the lock for a value proven at a later round")
		}
		// A body this node did not build gets the same judgement a peer's
		// proposal gets in admitRemoteValue. Re-proposing a locked value means
		// putting somebody else's block on the wire under this node's envelope,
		// and the local path used to skip validateProposal entirely: a leader
		// would prevote and broadcast a body every peer then rejected, which
		// turns a round it was meant to resolve into a wasted one.
		if !e.validateOwnValueLocked(b) {
			return false
		}
		if err := e.signLocalEnvelope(p, sel); err != nil {
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

	// polRound is the proof of lock this node actually VERIFIED for the value,
	// or -1. It is what the durable prevote record is allowed to leave a lock
	// on, so it must never be the round an envelope merely claimed: for a
	// peer's proposal it comes back from admitRemoteValue, which checked the
	// certificate, and for this node's own it comes from the envelope it just
	// signed over evidence it holds.
	polRound := int32(-1)
	if p.local {
		polRound = envelopePOLRound(p)
	} else {
		verified, err := e.admitRemoteValue(p, round)
		if err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).Uint32("round", round).
				Msg("BFT: two-phase proposal rejected")
			return false
		}
		polRound = verified
	}

	// Sign the prevote BEFORE anything here moves, so a refusal leaves the
	// engine in the view it was already in. The journal can refuse — this node
	// prevoted another value at this very view, possibly in a previous process
	// — and half-adopting a value nobody here voted for helps nothing.
	var ownPrevote *Vote
	if e.isMember() {
		vote, verr := e.castPrevote(b, round, polRound)
		if verr != nil {
			log.Error().Err(verr).Uint64("height", b.Header.Height).Uint32("round", round).
				Hex("proposed", b.Hash[:8]).Msg("BFT: failed to prevote")
			// Our own proposal, refused because a previous process already
			// prevoted something else at this view. Retrying here can never
			// succeed, and while we retry no deadline is running — the timer
			// only arms for a view this node holds a proposal in or is waiting
			// on someone else for. Take the view change now.
			if p.local && errors.Is(verr, ErrDoubleSign) && round == e.currentRound {
				if e.prevoteSpentByPreviousProcess(b.Header.Height, round) {
					log.Warn().Uint64("height", b.Header.Height).Uint32("round", round).
						Msg("BFT: own proposal refused by the durable prevote record, advancing the round")
					e.viewChangeTwoPhaseLocked()
				} else {
					// The refusal is not the restart shape — the durable record
					// is at another view entirely, or this process wrote it. The
					// round is left to the timer, as before; the mark is what
					// gives this view a deadline for the timer to fire, since a
					// view this node leads and holds no proposal in arms none.
					e.markRefusedViewLocked(b.Header.Height, round)
				}
			}
			return false
		}
		ownPrevote = vote
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

	if ownPrevote != nil {
		if _, addErr := e.voteSet.AddVote(ownPrevote); addErr != nil {
			logVoteAddFailure(addErr, "own prevote")
		}
		if e.broadcast != nil {
			e.broadcast(ownPrevote)
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

// validateOwnValueLocked judges a value this node is about to offer as leader.
//
// A body it built itself was validated as it was built. A body that came from
// somewhere else — the value a round-change quorum justified, or this node's
// own locked body reloaded from the journal after a restart — has to be checked
// against the head this node would apply it to, and against its own contents:
// the producer's signature, the transaction root, the transaction count.
func (e *BFTEngine) validateOwnValueLocked(b *types.Block) bool {
	if b.Header.ValidatorAddr == e.localAddr {
		return true
	}
	// Already adopted at this view: it was judged when it was adopted, and a
	// leader rebroadcasting the body it is bound to does that on every block
	// interval. The check that matters is the one before the value first goes
	// on the wire.
	if e.proposal != nil && e.proposal.Hash == b.Hash && e.proposal.Header.Height == b.Header.Height {
		return true
	}
	if err := b.Validate(); err != nil {
		log.Warn().Err(err).Uint64("height", b.Header.Height).Hex("value", b.Hash[:8]).
			Str("producer", b.Header.ValidatorAddr.String()).
			Msg("BFT: refusing to re-propose a body that does not match its own header")
		return false
	}
	if pk := e.findValidatorPubKey(b.Header.ValidatorAddr); pk == nil {
		log.Warn().Uint64("height", b.Header.Height).Str("producer", b.Header.ValidatorAddr.String()).
			Msg("BFT: refusing to re-propose a body whose producer is not in this node's validator set")
		return false
	} else if err := b.VerifySignature(pk); err != nil {
		log.Warn().Err(err).Uint64("height", b.Header.Height).
			Str("producer", b.Header.ValidatorAddr.String()).
			Msg("BFT: refusing to re-propose a body its producer did not sign")
		return false
	}
	if e.validateProposal != nil {
		if err := e.validateProposal(b); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).Hex("value", b.Hash[:8]).
				Msg("BFT: refusing to re-propose a body that would not apply here")
			return false
		}
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

// signLocalEnvelope seals this node's proposal and attaches the proof of lock
// that justifies it.
//
// The envelope used to name a POL round and send nothing else, which is a
// claim, not a proof: verifyPOL counts votes, so a node locked on another
// value rejected every such re-proposal and the height could not converge.
// sel is the round-change quorum's justified value when there is one — its
// certificate was verified on admission and is the one to forward; otherwise
// the justification is this node's own lock, whose certificate it kept when
// the prevote quorum formed (or reloaded from the journal after a restart).
func (e *BFTEngine) signLocalEnvelope(p *pendingProposal, sel *RoundChange) error {
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
	switch {
	case sel != nil && sel.LockedHash == p.block.Hash:
		env.POLRound = sel.LockedRound
		env.POLHash = sel.LockedHash
		p.pol = sel.LockedCert
	default:
		if hash, round, ok := e.lockAt(p.block.Header.Height); ok && hash == p.block.Hash {
			env.POLRound = int32(round)
			env.POLHash = hash
			p.pol = e.certificateFor(p.block.Header.Height, round, hash)
		}
	}
	if env.POLRound >= 0 && len(p.pol) == 0 {
		// Nothing to prove it with. Say so rather than sending a claim that
		// every locked peer will reject and no peer can act on.
		log.Warn().Uint64("height", env.Height).Uint32("round", env.Round).
			Int32("pol_round", env.POLRound).Hex("value", env.BlockHash[:8]).
			Msg("BFT: proposing a locked value without its certificate; peers locked elsewhere cannot follow")
	}
	if err := env.Sign(e.localPrivKey); err != nil {
		return err
	}
	p.envelope = env
	return nil
}

// envelopePOLRound is the POL round a proposal claims, or -1.
func envelopePOLRound(p *pendingProposal) int32 {
	if p == nil || p.envelope == nil {
		return -1
	}
	return p.envelope.POLRound
}

// certificateFor returns the prevote quorum this node holds for a value at
// (height, round), from memory or from the journal after a restart.
func (e *BFTEngine) certificateFor(height uint64, round uint32, hash [32]byte) []*Vote {
	if e.hasPOL && e.polHeight == height && e.polRound == round && e.polHash == hash && len(e.polCert) > 0 {
		return e.polCert
	}
	if e.journal != nil {
		if ch, cr, chash, votes, ok := e.journal.LoadCertificate(); ok && ch == height && cr == round && chash == hash {
			return votes
		}
	}
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

// admitRemoteValue judges a peer's proposal and reports the proof-of-lock
// round it verified for that value, or -1 when it verified none.
func (e *BFTEngine) admitRemoteValue(p *pendingProposal, round uint32) (int32, error) {
	b := p.block
	pk := e.findValidatorPubKey(b.Header.ValidatorAddr)
	if pk == nil {
		return -1, fmt.Errorf("no key for the original producer %s", b.Header.ValidatorAddr)
	}
	if err := b.VerifySignature(pk); err != nil {
		return -1, fmt.Errorf("block signature invalid: %w", err)
	}
	if e.validateProposal != nil {
		if err := e.validateProposal(b); err != nil {
			return -1, err
		}
	}
	// Tendermint's unlock rule, with its round relationship spelled out:
	// "if the validator is locked on a block since LastLockRound but now has a
	// PoLC for something else at round PoLC-Round where LastLockRound <
	// PoLC-Round < R, then it unlocks". All three comparisons matter here.
	//
	//   lockRound < POLRound  — an earlier or equal proof says nothing new;
	//                           this node's own lock is at least that fresh.
	//   POLRound  < Round     — a prevote quorum for the round now being
	//                           proposed cannot exist yet, so a proposal
	//                           justifying itself is justifying nothing.
	//   the certificate       — 2/3+1 distinct, verified prevotes for exactly
	//                           this value at exactly POLRound. Naming a round
	//                           is a claim; this is the proof.
	if locked, lockRound, ok := e.lockAt(b.Header.Height); ok && b.Hash != locked {
		env := p.envelope
		if env == nil || env.POLRound < 0 || env.POLHash != b.Hash {
			return -1, fmt.Errorf("locked to %x, proposal is %x with no later POL", locked[:8], b.Hash[:8])
		}
		if uint32(env.POLRound) <= lockRound {
			return -1, fmt.Errorf("proposal POL is at round %d, no later than the lock at round %d",
				env.POLRound, lockRound)
		}
		if uint32(env.POLRound) >= env.Round {
			return -1, fmt.Errorf("proposal POL is at round %d, not earlier than the proposal round %d",
				env.POLRound, env.Round)
		}
		if !e.verifyPOL(b.Header.Height, uint32(env.POLRound), b.Hash, p.pol) {
			return -1, fmt.Errorf("proposal carries no prevote quorum for %x at round %d, so it cannot unlock %x",
				b.Hash[:8], env.POLRound, locked[:8])
		}
		_ = round
		return env.POLRound, nil
	}
	// Not locked, so nothing had to be unlocked. Any POL the envelope names
	// went unchecked and must not be reported as verified: a prevote record
	// that leaves a lock on an unverified claim is no record at all.
	_ = round
	return -1, nil
}

// quorumVotes extracts the prevotes in vs that belong to (height, round,
// hash) — the certificate that value's quorum consists of. The set is built
// for exactly one view and one hash, so this is normally all of it; the filter
// is there so a certificate can never be assembled out of anything else.
func quorumVotes(vs *VoteSet, height uint64, round uint32, hash [32]byte) []*Vote {
	if vs == nil {
		return nil
	}
	var out []*Vote
	for _, v := range vs.Votes() {
		if v.Kind != types.VoteKindPrevote || v.Height != height || v.Round != round || v.BlockHash != hash {
			continue
		}
		out = append(out, v)
	}
	return out
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

// castPrevote signs this node's prevote after recording it durably.
//
// The record is written and fsynced before the signature exists, so a process
// that dies between the two comes back knowing at least as much as anything it
// put on the wire. Without it the journal protected precommits only: a
// validator that prevoted A and restarted before precommitting had nothing on
// disk saying so, and prevoted whatever the next proposal at that same view
// carried. Every peer answers that second prevote by dropping this validator's
// prevotes for the rest of the round.
//
// polRound is the round of the proof of lock the caller verified, or -1.
func (e *BFTEngine) castPrevote(b *types.Block, round uint32, polRound int32) (*Vote, error) {
	if e.journal != nil {
		if err := e.journal.RecordPrevote(b.Header.Height, round, b.Hash, polRound); err != nil {
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
	e.prevotedHeight, e.prevotedRound, e.hasPrevoted = b.Header.Height, round, true
	return vote, nil
}

// prevoteSpentByPreviousProcess reports whether a journal refusal of this
// node's own proposal at (height, round) is the restart shape: the durable
// prevote record sits at exactly that view and this process did not write it.
// A refusal this process caused is left to the round timer, as before.
func (e *BFTEngine) prevoteSpentByPreviousProcess(height uint64, round uint32) bool {
	if e.journal == nil {
		return false
	}
	ph, pr, _, ok := e.journal.LastPrevote()
	if !ok || ph != height || pr != round {
		return false
	}
	return !(e.hasPrevoted && e.prevotedHeight == height && e.prevotedRound == round)
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
	// Keep the quorum itself, not just the fact of it. This is the only copy
	// of the proof that this value was locked at this round: a round change
	// clears the vote set, and after that neither this node's round-change
	// message nor a re-proposal it leads can justify the value to anyone.
	e.polCert = quorumVotes(e.voteSet, b.Header.Height, round, b.Hash)
	if e.journal != nil && len(e.polCert) > 0 {
		if err := e.journal.SaveCertificate(b.Header.Height, round, b.Hash, e.polCert); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).Uint32("round", round).
				Msg("BFT: failed to persist the proof of lock; a restart here cannot prove this lock")
		}
	}
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
	return e.enterRoundLocked(e.currentRound+1, "round timeout, two-phase view change")
}

// enterRoundLocked moves the engine to round, announces this node's own
// round-change there and lets the leader know a view exists to propose in.
//
// The round is a statement about this height only: the vote lock, the proof of
// lock and everything durable are untouched, which is what makes it safe to
// arrive at a round by any route — a timeout, or a quorum of peers already
// there.
func (e *BFTEngine) enterRoundLocked(round uint32, why string) bool {
	e.currentRound = round
	e.voteSet = nil
	e.precommits = nil
	e.proposal = nil
	h, r := e.currentHeight, e.currentRound
	// Records for rounds now behind the engine can never be read again, and
	// attachments outside the window are not read either.
	e.dropStaleRoundChangesLocked()
	rc, err := e.signRoundChange(h, r)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to sign round-change")
	} else {
		e.addRoundChangeLocked(rc)
		if e.broadcastRC != nil {
			e.broadcastRC(rc)
		}
	}
	log.Warn().Uint64("height", h).Uint32("round", r).Msg("BFT: " + why)
	cb := e.onViewChange
	if cb != nil {
		go cb(h, r)
	}
	return true
}

// signRoundChange builds this node's vote to leave the current round, with
// the justification for whatever it is locked to.
//
// The lock is reported with its proof and its body. Reporting the hash and the
// round alone is what left a new leader counting messages: it could see that
// somebody was locked and could neither verify the claim nor re-propose the
// value, because it did not have it.
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
		rc.LockedCert = e.certificateFor(height, lr, hash)
		if body := e.lockedProposalLocked(); body != nil && body.Hash == hash {
			rc.LockedBlock = body
		}
		if len(rc.LockedCert) == 0 || rc.LockedBlock == nil {
			log.Warn().Uint64("height", height).Uint32("round", round).
				Hex("locked", hash[:8]).Uint32("locked_round", lr).
				Bool("certificate", len(rc.LockedCert) > 0).Bool("body", rc.LockedBlock != nil).
				Msg("BFT: round-change reports a lock it cannot prove; the next leader will have to pick another value")
		}
	}
	if err := rc.Sign(e.localPrivKey); err != nil {
		return nil, err
	}
	return rc, nil
}

// roundChangeJustified reports whether rc proves the lock it claims.
//
// IBFT 2.0 calls this JustifyRoundChange: a Prepared-Certificate is valid when
// it holds a quorum of messages "for the same round r'" with "r' < round
// number of Round-Change messages" and a body whose hash matches. The same
// three conditions apply here to a prevote quorum.
func (e *BFTEngine) roundChangeJustified(rc *RoundChange) bool {
	if rc.LockedRound < 0 {
		return false // claims no lock; there is nothing to prove
	}
	if uint32(rc.LockedRound) >= rc.Round {
		return false
	}
	if rc.LockedBlock == nil || rc.LockedBlock.Hash != rc.LockedHash {
		return false
	}
	if rc.LockedBlock.Header.Height != rc.Height {
		return false
	}
	// Bind the WHOLE body to the hash the certificate is about, not just its
	// header. ComputeVotedHash marshals the header alone — deliberately, so a
	// certificate can be attached to a block after it is signed — which leaves
	// the transaction list bound by nothing but the header's Merkle root.
	// Checking only the hash therefore admitted a genuine certificate carrying
	// a garbage body: the leader re-proposed it, every peer rejected it on the
	// transaction root, and the round that was supposed to resolve the height
	// was spent. Validate recomputes the voted hash, the transaction Merkle
	// root and the transaction count, and requires the producer's signature to
	// be present; validateOwnValueLocked verifies that signature before this
	// node puts the body back on the wire.
	if err := rc.LockedBlock.Validate(); err != nil {
		log.Debug().Err(err).Str("from", rc.ValidatorAddr.String()).
			Uint64("height", rc.Height).Hex("locked", rc.LockedHash[:8]).
			Msg("BFT: round-change body does not match the value its certificate proves")
		return false
	}
	return e.verifyPOL(rc.Height, uint32(rc.LockedRound), rc.LockedHash, rc.LockedCert)
}

func (e *BFTEngine) addRoundChangeLocked(rc *RoundChange) {
	if e.rcByRound == nil {
		e.rcByRound = make(map[uint32]map[qcrypto.Address]*roundChangeRecord)
	}
	m := e.rcByRound[rc.Round]
	if m == nil {
		m = make(map[qcrypto.Address]*roundChangeRecord)
		e.rcByRound[rc.Round] = m
	}
	// An unproven lock claim still counts toward the quorum, and this is where
	// Splendor departs from IBFT 2.0, which discards an unjustified
	// ROUND-CHANGE outright. With four validators and one faulty, three
	// round-changes are the whole quorum: dropping one because its sender lost
	// a certificate stops the chain rather than protecting it. The claim is
	// signed, so nobody can forge a lock; an unproven one simply loses its say
	// in the value selection below. Safety does not rest on the selection —
	// every node still refuses to leave its own lock without a certificate it
	// verified itself.
	//
	// What is NOT kept is the attachment on such a message. Only a justified
	// record is ever read — safeValueLocked skips the rest — and only at the
	// current round, so an unproven or far-ahead attachment is a body held for
	// nothing. rcByRound lives for the whole height, admits rounds up to 32
	// ahead and replaces a record on a re-send, so one member could park a
	// body per admissible round on every peer, each up to the 32 MiB a block
	// may be. The record is stored either way; only the proof and the body go.
	justified := e.roundChangeJustified(rc)
	if !justified || !e.roundChangeBodyWantedLocked(rc.Round) {
		// A stripped record is recorded as unjustified, so that "justified
		// implies the proof and the body are here" stays true for every reader
		// — CanPropose says so in as many words.
		stripped := *rc
		stripped.LockedCert, stripped.LockedBlock = nil, nil
		rc, justified = &stripped, false
	}
	m[rc.ValidatorAddr] = &roundChangeRecord{rc: rc, justified: justified}
	need := (e.activeCount()*2)/3 + 1
	if len(m) >= need {
		e.rcQCRound = int32(rc.Round)
	}
}

// roundChangeBodyWantedLocked reports whether a round-change for round is close
// enough to the current view for its attachment to be worth keeping.
func (e *BFTEngine) roundChangeBodyWantedLocked(round uint32) bool {
	return round >= e.currentRound && round <= e.currentRound+roundChangeBodyWindow
}

// dropStaleRoundChangesLocked forgets the round-change records the engine can
// no longer read and strips the attachments off those it keeps beyond the
// window. handleRoundChange refuses a round below the current one and
// safeValueLocked reads only the current round, so a record for a round already
// left is unreachable — it used to be held until the height committed.
func (e *BFTEngine) dropStaleRoundChangesLocked() {
	for round, m := range e.rcByRound {
		if round < e.currentRound {
			delete(e.rcByRound, round)
			continue
		}
		if e.roundChangeBodyWantedLocked(round) {
			continue
		}
		for addr, rec := range m {
			if rec.rc.LockedCert == nil && rec.rc.LockedBlock == nil {
				continue
			}
			stripped := *rec.rc
			stripped.LockedCert, stripped.LockedBlock = nil, nil
			m[addr] = &roundChangeRecord{rc: &stripped, justified: false}
		}
	}
}

// safeValueLocked is the value a leader may propose at the current view: the
// highest justified lock across the round-change quorum it holds there, or
// ok=false when the quorum proves no lock and the leader is free to mint.
//
// This is HotStuff's NEW-VIEW selection — the new leader takes the highest QC
// reported to it as its safety anchor — over IBFT 2.0's wire form. It is a
// LIVENESS rule: it makes the leader offer the one value some correct node may
// still be bound to, so the round can gather a quorum instead of deadlocking
// against a lock nobody can dislodge. Safety is elsewhere, in admitRemoteValue
// and the journal, and does not depend on the leader choosing well.
func (e *BFTEngine) safeValueLocked() (*RoundChange, bool) {
	if !e.roundsV1(e.currentHeight) || e.currentRound == 0 {
		return nil, false
	}
	m := e.rcByRound[e.currentRound]
	need := (e.activeCount()*2)/3 + 1
	if len(m) < need {
		return nil, false
	}
	var best *RoundChange
	for _, rec := range m {
		if !rec.justified || rec.rc.Height != e.currentHeight {
			continue
		}
		// The set can have changed size since admission; a certificate that
		// was a quorum then may not be one now.
		if len(rec.rc.LockedCert) < need {
			continue
		}
		if best == nil {
			best = rec.rc
			continue
		}
		if rec.rc.LockedRound == best.LockedRound && rec.rc.LockedHash != best.LockedHash {
			// Two proven prevote quorums for different values at one round
			// needs more than a third of the set signing twice. Say plainly
			// that the fault assumption has been exceeded; the order below
			// still picks one, and picks the same one everywhere.
			log.Error().Uint64("height", e.currentHeight).Int32("locked_round", rec.rc.LockedRound).
				Hex("a", best.LockedHash[:8]).Hex("b", rec.rc.LockedHash[:8]).
				Msg("CRITICAL: two justified values at one round; more than f validators have equivocated")
		}
		if betterJustifiedLock(rec.rc, best) {
			best = rec.rc
		}
	}
	return best, best != nil
}

// betterJustifiedLock is the total order the value selection runs on: a later
// lock first, then the lower hash, then the lower reporter address.
//
// The order has to be total. The selection used to fall through to map
// iteration order whenever two records agreed on the round and the hash, so two
// leaders at one view — or the same leader on a retry — could forward different
// certificates for the same value, and the record whose body the proposal
// carried was whichever the map happened to yield first. The tie-breaks decide
// nothing about safety (every node still refuses to leave its own lock without
// a certificate it verified itself); they make the choice reproducible.
func betterJustifiedLock(a, b *RoundChange) bool {
	if a.LockedRound != b.LockedRound {
		return a.LockedRound > b.LockedRound
	}
	if a.LockedHash != b.LockedHash {
		return bytes.Compare(a.LockedHash[:], b.LockedHash[:]) < 0
	}
	return bytes.Compare(a.ValidatorAddr[:], b.ValidatorAddr[:]) < 0
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
	defer e.publishViewLocked()
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
	// A quorum of signed round-changes for a LATER round is proof that the set
	// has left the rounds in between: at most f of those signatures are faulty,
	// so at least one correct node is already at that round and will not answer
	// anything earlier. Walking there one timeout at a time cost a lagging node
	// a full round timeout per intermediate round — with nothing to wait for in
	// any of them — while the rest of the set sat at the round it could not
	// reach. Safety is untouched: a round change carries no value, and this
	// node's lock, its proof of lock and its journal all survive it.
	if rc.Round > e.currentRound && e.rcQCRound == int32(rc.Round) {
		e.enterRoundLocked(rc.Round, "round-change quorum for a later round, catching up")
		return
	}
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
	if e.rcQCRound != int32(e.currentRound) {
		return false
	}
	// A quorum of round-changes is the permission to propose, not the answer
	// to what. The leader may proceed once the selection has resolved: either
	// the quorum proves no lock, or it proves one and handed over the body to
	// re-propose. A justified record always carries the body — the check is
	// here so that can never be assumed silently.
	sel, ok := e.safeValueLocked()
	return !ok || sel.LockedBlock != nil
}
