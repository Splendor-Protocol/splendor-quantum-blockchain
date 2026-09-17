// Package consensus implements the Splendor Quantum Chain consensus mechanisms.
//
// Single-validator production lives in validator.go and still runs when
// multi_validator is not active or only one key is registered.
//
// This file is the multi-validator BFT engine: ML-DSA-65 votes, 2/3+1 quorum,
// leader schedule, vote gossip hooks, equivocation reject, and round timeout
// / view change so a failed leader cannot stall the height.
package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ValidatorInfo holds on-chain information about a registered validator.
type ValidatorInfo struct {
	// Address is the validator's SQ: address.
	Address qcrypto.Address

	// PublicKey is the validator's 1952-byte ML-DSA-65 public key.
	PublicKey []byte

	// StakeAmount is the validator's staked SPLD balance (in smallest units).
	// Used to weight the validator set (currently all validators have equal weight).
	StakeAmount fmt.Stringer

	// IsActive indicates whether this validator is currently in the active set.
	IsActive bool

	// UptimeScore is 0-100, computed from recent participation history.
	UptimeScore uint8
}

// Vote is a validator's signed attestation that it accepts a proposed block.
// All votes are signed with ML-DSA-65 — no ECDSA, and no Ed25519 "for speed":
// a classical signature anywhere in consensus would make the whole chain
// forgeable by a quantum adversary regardless of how post-quantum the rest is.
type Vote struct {
	// AlgID names the signature scheme, exactly as blocks and transactions do,
	// so a future second scheme cannot be silently substituted here.
	AlgID string `json:"alg_id"`

	// Height is the block height this vote is for.
	Height uint64 `json:"height"`

	// Round is the consensus round (increases on timeout/leader failure).
	Round uint32 `json:"round"`

	// BlockHash is the SHA3-256 hash of the proposed block being voted on.
	BlockHash [32]byte `json:"block_hash"`

	// ValidatorAddr is the address of the voting validator.
	ValidatorAddr qcrypto.Address `json:"validator_addr"`

	// Signature is the ML-DSA-65 signature over SHA3-256(vote_body).
	Signature []byte `json:"signature"`

	// Timestamp when this vote was created.
	Timestamp int64 `json:"timestamp"`
}

// VoteBody is the canonical signable subset of Vote (excludes Signature).
type VoteBody struct {
	AlgID         string          `json:"alg_id"`
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	BlockHash     [32]byte        `json:"block_hash"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Timestamp     int64           `json:"timestamp"`
}

// Hash computes SHA3-256 of the canonical vote body.
//
// AlgID is inside the signed body, so an attacker cannot downgrade a vote by
// rewriting the algorithm label on the wire.
func (v *Vote) Hash() ([32]byte, error) {
	body := VoteBody{
		AlgID:         v.AlgID,
		Height:        v.Height,
		Round:         v.Round,
		BlockHash:     v.BlockHash,
		ValidatorAddr: v.ValidatorAddr,
		Timestamp:     v.Timestamp,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, err
	}
	return qcrypto.Hash32(b), nil
}

// Sign signs the vote with the given ML-DSA-65 private key, under the vote
// domain context so a vote signature can never be replayed as a block or
// transaction signature.
func (v *Vote) Sign(priv *qcrypto.PrivateKey) error {
	v.AlgID = qcrypto.AlgMLDSA65
	hash, err := v.Hash()
	if err != nil {
		return fmt.Errorf("vote hash failed: %w", err)
	}
	sig, err := qcrypto.Sign(priv, hash[:], qcrypto.CtxVote)
	if err != nil {
		return fmt.Errorf("vote sign failed: %w", err)
	}
	v.Signature = sig
	return nil
}

// Verify verifies the vote's ML-DSA-65 signature against the given public key.
func (v *Vote) Verify(pubKeyBytes []byte) error {
	if v.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported vote alg_id %q: consensus accepts only %s",
			v.AlgID, qcrypto.AlgMLDSA65)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid voter pubkey: %w", err)
	}
	// The vote names its author, so that name must be the owner of the key
	// checking it. Without this, a valid signature from validator A could be
	// counted as a vote from validator B and inflate a quorum.
	if qcrypto.DeriveAddress(pub) != v.ValidatorAddr {
		return fmt.Errorf("vote claims to be from %s but the key belongs to %s",
			v.ValidatorAddr, qcrypto.DeriveAddress(pub))
	}
	hash, err := v.Hash()
	if err != nil {
		return fmt.Errorf("vote hash failed: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxVote, v.Signature) {
		return fmt.Errorf("invalid vote ML-DSA signature from %s", v.ValidatorAddr)
	}
	return nil
}

// VoteSet collects votes for a specific block at a specific height/round.
// It tracks whether a 2/3+1 quorum has been reached.
type VoteSet struct {
	mu             sync.Mutex
	height         uint64
	round          uint32
	blockHash      [32]byte
	votes          map[qcrypto.Address]*Vote // one vote per validator
	totalWeight    int                       // total number of validators
	requiredWeight int                       // 2/3 + 1 threshold
}

// NewVoteSet creates a new vote set for a block at height/round.
func NewVoteSet(height uint64, round uint32, blockHash [32]byte, validatorCount int) *VoteSet {
	required := (validatorCount*2)/3 + 1
	return &VoteSet{
		height:         height,
		round:          round,
		blockHash:      blockHash,
		votes:          make(map[qcrypto.Address]*Vote),
		totalWeight:    validatorCount,
		requiredWeight: required,
	}
}

// AddVote adds a validated vote to the set.
// Returns true if this vote caused the quorum to be reached.
func (vs *VoteSet) AddVote(vote *Vote) (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Validate vote is for this height/round/block
	if vote.Height != vs.height {
		return false, fmt.Errorf("wrong height: want %d got %d", vs.height, vote.Height)
	}
	if vote.Round != vs.round {
		return false, fmt.Errorf("wrong round: want %d got %d", vs.round, vote.Round)
	}
	if vote.BlockHash != vs.blockHash {
		return false, fmt.Errorf("wrong block hash in vote from %s", vote.ValidatorAddr)
	}

	// Check for duplicate vote
	if _, exists := vs.votes[vote.ValidatorAddr]; exists {
		return false, fmt.Errorf("duplicate vote from %s", vote.ValidatorAddr)
	}

	vs.votes[vote.ValidatorAddr] = vote

	quorumBefore := len(vs.votes)-1 >= vs.requiredWeight
	quorumNow := len(vs.votes) >= vs.requiredWeight

	return !quorumBefore && quorumNow, nil
}

// HasQuorum returns true if >= 2/3+1 validators have voted.
func (vs *VoteSet) HasQuorum() bool {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return len(vs.votes) >= vs.requiredWeight
}

// VoteCount returns the current number of collected votes.
func (vs *VoteSet) VoteCount() int {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return len(vs.votes)
}

// Votes returns a copy of collected votes.
func (vs *VoteSet) Votes() []*Vote {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	out := make([]*Vote, 0, len(vs.votes))
	for _, v := range vs.votes {
		cp := *v
		out = append(out, &cp)
	}
	return out
}

// VoteBroadcaster sends a signed vote to peers. Implementations use sealed P2P.
type VoteBroadcaster func(vote *Vote)

// ProposalBroadcaster sends a proposal to peers. It carries the round and the
// proposer's own vote, not just the block — see ProposalMsg.
type ProposalBroadcaster func(p *ProposalMsg)

// BFTEngine is the multi-validator BFT consensus engine.
type BFTEngine struct {
	localAddr    qcrypto.Address
	localPubKey  []byte
	localPrivKey *qcrypto.PrivateKey

	validators   []ValidatorInfo
	validatorsMu sync.RWMutex

	currentHeight uint64
	currentRound  uint32
	voteSet       *VoteSet
	proposal      *types.Block
	// lockedProposal is the block body this node voted for at votedHeight.
	// Survives round timeouts so the leader rebroadcasts the SAME hash
	// instead of minting a new proposal every interval while the journal is locked.
	lockedProposal *types.Block
	seenVotes      map[string]*Vote // validator|height|round → first vote (equivocation)

	// pendingVotes holds verified votes that arrived before the proposal they
	// belong to, keyed height|round. Dropping them meant a peer's vote that
	// overtook the proposal on the wire was lost and the height ran to
	// timeout, which with two validators is the whole quorum.
	pendingVotes map[string][]*Vote

	// Vote commitment: immutable per height
	// Once we vote for a block at height H, we are committed to that hash
	// for ALL rounds at height H. This prevents two-QC forks.
	votedHash   [32]byte
	votedHeight uint64
	hasVoted    bool

	// Quorum tracking: separate from voting for safety
	// Allows verification that quorum matches our vote before commit
	quorumHash   [32]byte
	quorumHeight uint64
	hasQuorum    bool

	stateMu sync.Mutex

	// refreshSet re-reads the on-chain validator registry. The engine sizes a
	// quorum from the set it holds, and the executor judges the resulting
	// certificate against the registry in the block's parent state; between a
	// registration landing and the next periodic refresh those two disagree,
	// and a quorum of one where the chain wants two is a commit that can only
	// fail. nil leaves the set to whoever calls UpdateValidatorSet.
	refreshSet func()

	voteTimeout  time.Duration
	roundTimeout time.Duration
	// proposalWait is added to roundTimeout while the engine waits for a
	// REMOTE leader's first proposal at a view. A leader proposes on its own
	// block-interval tick after it has applied the previous block, so the
	// wait for its proposal is one interval longer than the wait for votes on
	// a proposal that already exists. MultiValidator sets it to the block
	// interval before the loop starts; zero (tests) makes both waits equal.
	proposalWait time.Duration

	voteCh    chan *Vote
	commitCh  chan *types.Block
	proposeCh chan *pendingProposal
	// wakeCh nudges the loop to re-evaluate the round timer after state that
	// the timer depends on changed outside the loop: SetHeight from the
	// producer's tick, or a validator-set refresh.
	wakeCh chan struct{}

	onCommit     func(*types.Block) error
	onViewChange func(height uint64, round uint32)
	// canProgress, when set, must be true before this node casts a vote or
	// adopts a proposal. Used to halt consensus after a partial state persist.
	canProgress func() bool
	// The last vote THIS process signed, so a journal refusal can be told
	// apart: one for a vote a previous process cast (a restart landed
	// mid-height; the round is spent, move on) versus one for a vote this
	// process cast itself (a tick re-proposed while peers were still voting;
	// leave the round alone and let the timer decide, as before).
	signedHeight     uint64
	signedRound      uint32
	hasSigned        bool
	validateProposal func(*types.Block) error
	journal          *VoteJournal
	broadcast        VoteBroadcaster
	broadcastP       ProposalBroadcaster
}

// BFTConfig configures the BFT engine.
type BFTConfig struct {
	LocalAddr         qcrypto.Address
	LocalPubKey       []byte
	LocalPrivKey      *qcrypto.PrivateKey
	VoteTimeout       time.Duration
	RoundTimeout      time.Duration
	OnCommit          func(*types.Block) error
	OnViewChange      func(height uint64, round uint32)
	Broadcast         VoteBroadcaster
	BroadcastProposal ProposalBroadcaster
	// ValidateProposal judges a peer's proposal before this node votes on
	// it: linkage to the local head, producer authorization, every
	// transaction, the state root — everything the committed block will be
	// judged on except the certificate, which does not exist yet. A proposal
	// it rejects gets no vote and changes no engine state. nil skips the
	// check, which is only acceptable in tests: without it a quorum can form
	// on a block no honest node can apply, and the leader's own commit is the
	// first place anyone finds out.
	ValidateProposal func(*types.Block) error
	// VoteJournal, when set, is consulted and written before every vote this
	// node signs, so a restart cannot make it sign a second, conflicting
	// vote for a height and round it already voted in. nil (tests) keeps
	// only the in-memory lock.
	VoteJournal *VoteJournal
	// CanProgress, when set, must return true before this node votes on or
	// adopts a proposal. nil (tests) means always. Wired to the executor's
	// apply-halt so a failed state commit cannot be followed by a new vote.
	CanProgress func() bool
}

// NewBFTEngine creates a working multi-validator BFT engine.
func NewBFTEngine(cfg BFTConfig) (*BFTEngine, error) {
	if cfg.VoteTimeout == 0 {
		cfg.VoteTimeout = 800 * time.Millisecond
	}
	if cfg.RoundTimeout == 0 {
		cfg.RoundTimeout = 3 * time.Second
	}
	return &BFTEngine{
		localAddr:        cfg.LocalAddr,
		localPubKey:      cfg.LocalPubKey,
		localPrivKey:     cfg.LocalPrivKey,
		voteTimeout:      cfg.VoteTimeout,
		roundTimeout:     cfg.RoundTimeout,
		onCommit:         cfg.OnCommit,
		onViewChange:     cfg.OnViewChange,
		validateProposal: cfg.ValidateProposal,
		journal:          cfg.VoteJournal,
		canProgress:      cfg.CanProgress,
		broadcast:        cfg.Broadcast,
		broadcastP:       cfg.BroadcastProposal,
		voteCh:           make(chan *Vote, 1000),
		commitCh:         make(chan *types.Block, 16),
		proposeCh:        make(chan *pendingProposal, 16),
		wakeCh:           make(chan struct{}, 1),
		seenVotes:        make(map[string]*Vote),
		pendingVotes:     make(map[string][]*Vote),
	}, nil
}

// maxPendingVotes bounds the early-vote buffer, and maxSeenVotes the
// equivocation table. Both are fed from the network, so both need a ceiling:
// without one a peer holding the write-plane token could grow either without
// limit by sending votes for heights that never arrive.
const (
	maxPendingVotes = 256
	maxSeenVotes    = 4096
)

// UpdateValidatorSet updates the active validator set.
// This is called when the on-chain validator registry changes (epoch transitions).
func (e *BFTEngine) UpdateValidatorSet(validators []ValidatorInfo) {
	e.validatorsMu.Lock()
	defer e.validatorsMu.Unlock()
	e.validators = make([]ValidatorInfo, len(validators))
	copy(e.validators, validators)
	log.Info().Int("count", len(validators)).Msg("BFT: validator set updated")
	e.wake()
}

// wake asks the loop to re-evaluate the round timer. Non-blocking: one
// pending wake is as good as many.
func (e *BFTEngine) wake() {
	select {
	case e.wakeCh <- struct{}{}:
	default:
	}
}

// ValidatorCount returns the number of active validators.
func (e *BFTEngine) ValidatorCount() int {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	return len(e.validators)
}

// LeaderAddress returns the scheduled leader for height and round.
// Rotation: validators[(height+round) % n] so a timed-out leader yields.
func (e *BFTEngine) LeaderAddress(height uint64, round uint32) (qcrypto.Address, bool) {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	active := e.activeLocked()
	if len(active) == 0 {
		return qcrypto.Address{}, false
	}
	idx := int(height+uint64(round)) % len(active)
	return active[idx].Address, true
}

// IsLeader reports whether this node is the scheduled leader for height/round.
func (e *BFTEngine) IsLeader(height uint64, round uint32) bool {
	addr, ok := e.LeaderAddress(height, round)
	return ok && addr == e.localAddr
}

func (e *BFTEngine) activeLocked() []ValidatorInfo {
	out := make([]ValidatorInfo, 0, len(e.validators))
	for _, v := range e.validators {
		if v.IsActive {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return e.validators
	}
	return out
}

// SubmitVote processes an incoming vote from a peer.
func (e *BFTEngine) SubmitVote(vote *Vote) {
	// Reject a nil vote before it can occupy a bounded vote-channel slot: the
	// slot is one of a small number of liveness resources, and a nil vote can
	// never contribute to a quorum.
	if vote == nil {
		return
	}
	select {
	case e.voteCh <- vote:
	default:
		log.Warn().Msg("BFT: vote channel full, dropping vote")
	}
}

// ProposeBlock submits a block this node is proposing. The engine stamps it
// with the round it is on, casts and attaches this node's vote, and only then
// broadcasts it — so a peer can never answer a proposal before the proposer is
// able to count the answer.
func (e *BFTEngine) ProposeBlock(b *types.Block) {
	select {
	case e.proposeCh <- &pendingProposal{block: b, local: true}:
	default:
	}
}

// SubmitProposal queues a proposal received from a peer.
func (e *BFTEngine) SubmitProposal(msg *ProposalMsg) {
	p := pendingProposalOf(msg)
	if p == nil || p.block == nil {
		return
	}
	select {
	case e.proposeCh <- p:
	default:
		log.Warn().Msg("BFT: proposal channel full, dropping proposal")
	}
}

// CommitCh returns a channel that emits committed blocks.
func (e *BFTEngine) CommitCh() <-chan *types.Block {
	return e.commitCh
}

// castVote creates and signs a vote for the given block at round.
//
// The journal is written before the signature exists: if the write fails or
// the journal refuses (this node already voted differently at this height
// and round, possibly in a previous process), no vote is produced and nothing
// reaches the wire.
func (e *BFTEngine) castVote(b *types.Block, round uint32) (*Vote, error) {
	if e.journal != nil {
		if err := e.journal.Record(b.Header.Height, round, b.Hash); err != nil {
			return nil, err
		}
	}
	vote := &Vote{
		Height:        b.Header.Height,
		Round:         round,
		BlockHash:     b.Hash,
		ValidatorAddr: e.localAddr,
		Timestamp:     time.Now().Unix(),
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

// spentByPreviousProcess reports whether a journal refusal of this node's own
// proposal at (height, round) is the restart shape: the journal holds a vote
// at exactly that height and round, and this process did not sign it. Only
// then is advancing the round the right answer. A journal AHEAD of the engine
// (a data dir restored under a surviving journal) or a vote this process cast
// itself are left to the round timer, as before — bumping the round there
// would loop at CPU speed, or abandon a round peers are still voting in.
func (e *BFTEngine) spentByPreviousProcess(height uint64, round uint32) bool {
	if e.journal == nil {
		return false
	}
	jh, jr, _, ok := e.journal.Last()
	if !ok || jh != height || jr != round {
		return false
	}
	return !(e.hasSigned && e.signedHeight == height && e.signedRound == round)
}

// isMember reports whether this node is in the active validator set.
//
// A validator that has joined a running chain and not yet registered runs the
// engine — it needs to follow proposals and count real votes — but it is not
// entitled to a vote. It used to cast one anyway, and with a single registered
// validator that was a quorum of one: the joiner committed the leader's block
// under a certificate consisting of its own vote, so two honest nodes held
// different certificates for the same block.
func (e *BFTEngine) isMember() bool {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	for _, v := range e.activeLocked() {
		if v.Address == e.localAddr {
			return true
		}
	}
	return false
}

// Run drives proposals, votes, timeouts, and commits until ctx is cancelled.
//
// The round timer is armed only while the engine is waiting on a peer: for
// votes on a proposal it holds open, or for a remote leader's proposal at the
// current view. It is disarmed by a commit and never fires for a view the
// engine has already left. It used to run continuously — armed at start and
// reset to the full round timeout after every proposal, committed or not — so
// a sole validator whose block apply took longer than the timeout logged a
// "round timeout, view change" on a height it had already sealed, and a
// leader whose commit arrived by vote saw the stale deadline fire the moment
// the apply returned.
func (e *BFTEngine) Run(ctx context.Context) error {
	log.Info().Str("addr", e.localAddr.String()).Msg("BFT: multi-validator engine starting")
	rt := newRoundTimer()
	defer rt.disarm()
	e.syncRoundTimer(rt, false)
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("BFT: stopping")
			return nil
		case p := <-e.proposeCh:
			accepted := e.handleProposal(p)
			e.syncRoundTimer(rt, accepted)
		case vote := <-e.voteCh:
			e.handleVote(vote)
			e.syncRoundTimer(rt, false)
		case <-e.wakeCh:
			e.syncRoundTimer(rt, false)
		case <-rt.t.C:
			h, r := rt.height, rt.round
			rt.armed = false
			e.expireRound(h, r)
			e.syncRoundTimer(rt, false)
		}
	}
}

// roundTimer is the loop's single deadline, tagged with the view (height,
// round) it was armed for so a firing can be matched against the view the
// engine is in when it is finally read.
type roundTimer struct {
	t        *time.Timer
	armed    bool
	height   uint64
	round    uint32
	deadline time.Time
}

func newRoundTimer() *roundTimer {
	t := time.NewTimer(time.Hour)
	if !t.Stop() {
		<-t.C
	}
	return &roundTimer{t: t}
}

// arm sets a fresh deadline d from now for view (height, round), replacing
// any deadline already set.
func (rt *roundTimer) arm(d time.Duration, height uint64, round uint32) {
	rt.disarm()
	rt.t.Reset(d)
	rt.armed, rt.height, rt.round = true, height, round
	rt.deadline = time.Now().Add(d)
}

// disarm stops the timer and drains a firing that is already buffered, so a
// deadline that expired while a handler ran (a slow block apply, say) can
// never be read as a timeout afterwards.
func (rt *roundTimer) disarm() {
	if !rt.t.Stop() {
		select {
		case <-rt.t.C:
		default:
		}
	}
	rt.armed = false
	rt.deadline = time.Time{}
}

// syncRoundTimer brings the timer in line with what the engine is waiting
// for at its current view:
//
//   - a proposal held open, waiting for a quorum: roundTimeout;
//   - no proposal, and the view's leader is another member of a set larger
//     than one: roundTimeout + proposalWait, so a leader that never proposes
//     is rotated away;
//   - anything else — a sole validator, a height just committed, a view this
//     node leads itself: no timer at all.
//
// A deadline belongs to a view. Once armed it is left alone until the view
// changes, except that an accepted proposal at the same view (the leader's
// periodic rebroadcast of the block it is locked to) renews it, as every
// proposal used to: a leader that is still trying is not a failed leader.
func (e *BFTEngine) syncRoundTimer(rt *roundTimer, renew bool) {
	e.stateMu.Lock()
	h, r := e.currentHeight, e.currentRound
	open := e.voteSet != nil && e.proposal != nil
	e.stateMu.Unlock()

	var wait time.Duration
	switch {
	case open:
		wait = e.roundTimeout
	case e.awaitingRemoteLeader(h, r):
		wait = e.roundTimeout + e.proposalWait
	default:
		rt.disarm()
		return
	}
	if rt.armed && rt.height == h && rt.round == r && !renew {
		return
	}
	rt.arm(wait, h, r)
}

// awaitingRemoteLeader reports whether the engine, holding no proposal at
// (height, round), is waiting for another member to propose there.
func (e *BFTEngine) awaitingRemoteLeader(height uint64, round uint32) bool {
	if e.activeCount() <= 1 {
		return false
	}
	leader, ok := e.LeaderAddress(height, round)
	return ok && leader != e.localAddr
}

// expireRound is the timer firing for view (height, round). The view change
// happens only if the engine is still in that view; a deadline for a height
// that has since committed, or a round that has since moved, is stale and
// changes nothing.
func (e *BFTEngine) expireRound(height uint64, round uint32) bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.currentHeight != height || e.currentRound != round {
		log.Debug().Uint64("timer_height", height).Uint32("timer_round", round).
			Uint64("height", e.currentHeight).Uint32("round", e.currentRound).
			Msg("BFT: stale round timer ignored")
		return false
	}
	return e.viewChangeLocked()
}

// handleTimeout advances the round unconditionally. The loop goes through
// expireRound, which first checks the deadline still belongs to the current
// view; this entry point remains for callers that have established that
// themselves.
func (e *BFTEngine) handleTimeout() {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.viewChangeLocked()
}

// viewChangeLocked leaves the current round for the next one. Called with
// stateMu held.
func (e *BFTEngine) viewChangeLocked() bool {
	if e.ValidatorCount() == 0 {
		return false
	}
	e.currentRound++
	e.voteSet = nil
	e.proposal = nil
	// Keep lockedProposal: the journal still binds this height to that hash.
	h, r := e.currentHeight, e.currentRound
	log.Warn().Uint64("height", h).Uint32("round", r).
		Msg("BFT: round timeout, view change")
	cb := e.onViewChange
	if cb != nil {
		go cb(h, r)
	}
	return true
}

// proposalRound resolves the round a received proposal was made in.
//
// For a locally built proposal that is simply this node's round. For one off
// the wire it comes from the proposer's bundled vote, which is signed over
// (height, round, block hash) by the key that sealed the block — so the round
// is authenticated by the proposer, not asserted by whoever relayed it. A
// proposal with no vote is an older peer's bare block: it is judged against
// this node's round, exactly as every proposal used to be.
func (e *BFTEngine) proposalRound(p *pendingProposal) (uint32, error) {
	b := p.block
	if p.local || p.vote == nil {
		return e.currentRound, nil
	}
	v := p.vote
	if v.ValidatorAddr != b.Header.ValidatorAddr {
		return 0, fmt.Errorf("proposal carries a vote from %s but was sealed by %s",
			v.ValidatorAddr, b.Header.ValidatorAddr)
	}
	if v.Height != b.Header.Height || v.BlockHash != b.Hash {
		return 0, fmt.Errorf("proposal's vote is for %d/%x, block is %d/%x",
			v.Height, v.BlockHash[:4], b.Header.Height, b.Hash[:4])
	}
	pubKey := e.findValidatorPubKey(v.ValidatorAddr)
	if pubKey == nil {
		return 0, fmt.Errorf("proposer %s is not in this node's validator set", v.ValidatorAddr)
	}
	if err := v.Verify(pubKey); err != nil {
		return 0, fmt.Errorf("proposal's own vote does not verify: %w", err)
	}
	if v.Round > e.currentRound+MaxRoundAdvance {
		return 0, fmt.Errorf("proposal is at round %d, this node is at %d (max advance %d)",
			v.Round, e.currentRound, MaxRoundAdvance)
	}
	if b.Header.Height == e.currentHeight && v.Round < e.currentRound {
		return 0, fmt.Errorf("proposal is at round %d, this node has already left it for %d",
			v.Round, e.currentRound)
	}
	return v.Round, nil
}

// handleProposal processes a new block proposal. It reports whether the
// proposal was adopted — voted on, or at least tracked — at this node's view,
// which is what tells the loop a deadline for that view is worth renewing.
func (e *BFTEngine) handleProposal(p *pendingProposal) bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	if p == nil || p.block == nil {
		return false
	}
	b := p.block
	// Size the quorum from the registry as it is NOW, not as it was at the
	// last tick: a registration that has just landed changes what the executor
	// will demand of this block's certificate.
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

	// Never go backwards. handleProposal used to adopt whatever height it was
	// handed, so a replayed proposal for a height the node had already
	// committed dragged the engine back to it.
	if b.Header.Height < e.currentHeight {
		log.Debug().Uint64("height", b.Header.Height).Uint64("engine_height", e.currentHeight).
			Msg("BFT: proposal for a height already left behind, ignored")
		return false
	}

	round, err := e.proposalRound(p)
	if err != nil {
		log.Warn().Err(err).Uint64("height", b.Header.Height).
			Str("proposer", b.Header.ValidatorAddr.String()).Msg("BFT: proposal rejected")
		return false
	}

	leader, ok := e.LeaderAddress(b.Header.Height, round)
	if !ok || b.Header.ValidatorAddr != leader {
		log.Warn().
			Uint64("height", b.Header.Height).
			Uint32("round", round).
			Str("proposer", b.Header.ValidatorAddr.String()).
			Str("leader", leader.String()).
			Msg("BFT: proposal from non-leader rejected")
		return false
	}

	// A peer's proposal is judged before anything here changes and before a
	// vote is cast. The leader check above says who MAY have sealed it; the
	// block signature says who DID — a bare block from an older peer carries
	// no vote, so proposalRound verified nothing about it — and the validator
	// says whether it can ever be applied to this node's head.
	if !p.local {
		pk := e.findValidatorPubKey(b.Header.ValidatorAddr)
		if pk == nil {
			log.Warn().Uint64("height", b.Header.Height).Str("proposer", b.Header.ValidatorAddr.String()).
				Msg("BFT: proposal rejected, no key for the proposer in the validator set")
			return false
		}
		if err := b.VerifySignature(pk); err != nil {
			log.Warn().Err(err).Uint64("height", b.Header.Height).
				Str("proposer", b.Header.ValidatorAddr.String()).Msg("BFT: proposal rejected, block signature invalid")
			return false
		}
		if e.validateProposal != nil {
			if err := e.validateProposal(b); err != nil {
				log.Warn().Err(err).Uint64("height", b.Header.Height).Uint32("round", round).
					Str("proposer", b.Header.ValidatorAddr.String()).Msg("BFT: proposal rejected, would not apply; no vote cast")
				return false
			}
		}
	}

	// SECURITY CHECK: Vote commitment enforcement
	// This is the CORE anti-fork protection - once voted, committed to that hash
	if e.hasVoted && b.Header.Height == e.votedHeight && b.Hash != e.votedHash {
		ev := log.Warn().
			Uint64("height", b.Header.Height).
			Uint32("round", round).
			Hex("voted_for", e.votedHash[:8]).
			Hex("proposed", b.Hash[:8]).
			Bool("durable_journal", e.journal != nil)
		if e.journal != nil {
			if jh, jr, _, ok := e.journal.Last(); ok && jh == e.votedHeight {
				ev = ev.Uint32("journal_round", jr)
			}
		}
		ev.Msg("SECURITY: durable vote-lock refuses different block at this height; view-change will not unlock — align peers, then clear vote-journal on conflicting nodes (--clear-vote-journal)")
		return false
	}

	e.currentHeight = b.Header.Height
	e.currentRound = round
	e.proposal = b
	e.voteSet = NewVoteSet(b.Header.Height, round, b.Hash, nValidators)

	// The proposer's own vote travels with the proposal, so it can never be
	// reordered ahead of it and dropped.
	if p.vote != nil {
		if _, addErr := e.voteSet.AddVote(p.vote); addErr != nil {
			log.Debug().Err(addErr).Msg("BFT: proposer's own vote not counted")
		}
	}

	var ownVote *Vote
	if e.isMember() {
		vote, verr := e.castVote(b, round)
		if verr != nil {
			log.Error().Err(verr).
				Uint64("height", b.Header.Height).
				Uint32("round", round).
				Hex("proposed", b.Hash[:8]).
				Msg("BFT: failed to cast vote")
			if errors.Is(verr, ErrDoubleSign) && e.hasVoted && e.votedHeight == b.Header.Height && e.votedHash != b.Hash {
				log.Error().
					Uint64("height", b.Header.Height).
					Hex("voted_for", e.votedHash[:8]).
					Hex("proposed", b.Hash[:8]).
					Msg("BFT: split vote-lock (journal/in-memory); view-change cannot unlock — use --clear-vote-journal after peers are healthy")
			}
			// Our own proposal, refused because this round is already spent in
			// the journal. Retrying at this round can never succeed, and our
			// retries are what keep the round timer from firing — so take the
			// view change now rather than wait for a timeout that never comes.
			if p.local && errors.Is(verr, ErrDoubleSign) && round == e.currentRound && e.spentByPreviousProcess(b.Header.Height, round) {
				e.currentRound = round + 1
				e.voteSet = nil
				e.proposal = nil
				log.Warn().Uint64("height", b.Header.Height).Uint32("round", round+1).
					Msg("BFT: own proposal refused by the vote journal, advancing the round")
				if cb := e.onViewChange; cb != nil {
					go cb(b.Header.Height, round+1)
				}
			}
			return false
		}
		ownVote = vote
		if _, addErr := e.voteSet.AddVote(vote); addErr != nil {
			log.Error().Err(addErr).Msg("BFT: failed to add own vote")
		}
		if e.broadcast != nil {
			e.broadcast(vote)
		}
	} else {
		log.Debug().Uint64("height", b.Header.Height).
			Msg("BFT: this node is not in the active validator set, following without voting")
	}

	// Own the broadcast here, after the vote set exists: MultiValidator used to
	// send the proposal straight after handing the block to a buffered channel,
	// so a peer's answer could arrive before this node could count it.
	if p.local && e.broadcastP != nil && b.Header.ValidatorAddr == e.localAddr {
		e.broadcastP(&ProposalMsg{Block: b, Vote: ownVote})
	}

	// Anything that overtook the proposal on the wire belongs to this set.
	e.drainPendingVotes()

	log.Debug().
		Uint64("height", b.Header.Height).
		Uint32("round", round).
		Int("validators", nValidators).
		Int("votes", e.voteSet.VoteCount()).
		Int("required", e.voteSet.requiredWeight).
		Msg("BFT: proposal accepted")

	if e.voteSet.HasQuorum() {
		e.recordQuorum(b)
		e.commitBlock(b)
	}
	return true
}

// recordQuorum marks that this block achieved 2/3+1 votes.
// SECURITY: Verifies quorum matches our vote before allowing commit.
func (e *BFTEngine) recordQuorum(b *types.Block) {
	// SECURITY CHECK: Quorum must match our vote
	if e.hasVoted && e.votedHeight == b.Header.Height && e.votedHash != b.Hash {
		// This should be impossible if handleProposal works correctly
		log.Error().
			Uint64("height", b.Header.Height).
			Hex("voted", e.votedHash[:8]).
			Hex("quorum", b.Hash[:8]).
			Msg("CRITICAL: quorum for different block than we voted for")
		// Do NOT set quorum - this is a safety violation
		return
	}

	e.quorumHash = b.Hash
	e.quorumHeight = b.Header.Height
	e.hasQuorum = true

	log.Info().
		Uint64("height", b.Header.Height).
		Int("votes", e.voteSet.VoteCount()).
		Hex("block", b.Hash[:8]).
		Msg("quorum achieved")
}

// pendingVoteKey identifies the vote set a buffered vote belongs to.
func pendingVoteKey(height uint64, round uint32) string {
	return fmt.Sprintf("%d|%d", height, round)
}

// bufferVote holds a verified vote whose proposal has not arrived yet.
func (e *BFTEngine) bufferVote(v *Vote) {
	if e.pendingVoteCountLocked() >= maxPendingVotes {
		e.prunePendingVotes(e.currentHeight)
		if e.pendingVoteCountLocked() >= maxPendingVotes {
			log.Debug().Msg("BFT: early-vote buffer full, dropping vote")
			return
		}
	}
	key := pendingVoteKey(v.Height, v.Round)
	for _, have := range e.pendingVotes[key] {
		if have.ValidatorAddr == v.ValidatorAddr {
			return
		}
	}
	cp := *v
	e.pendingVotes[key] = append(e.pendingVotes[key], &cp)
}

// drainPendingVotes feeds the current vote set every early vote that belongs
// to it. Called with stateMu held and e.voteSet non-nil.
func (e *BFTEngine) drainPendingVotes() {
	key := pendingVoteKey(e.currentHeight, e.currentRound)
	held := e.pendingVotes[key]
	delete(e.pendingVotes, key)
	for _, v := range held {
		if _, err := e.voteSet.AddVote(v); err != nil {
			log.Debug().Err(err).Msg("BFT: early vote not counted")
		}
	}
	e.prunePendingVotes(e.currentHeight)
}

// prunePendingVotes drops buffers for heights the node has left.
func (e *BFTEngine) prunePendingVotes(below uint64) {
	for key, votes := range e.pendingVotes {
		if len(votes) == 0 || votes[0].Height < below {
			delete(e.pendingVotes, key)
		}
	}
}

func (e *BFTEngine) pendingVoteCountLocked() int {
	n := 0
	for _, v := range e.pendingVotes {
		n += len(v)
	}
	return n
}

// pendingVoteCount reports how many early votes are held. Test observability.
func (e *BFTEngine) pendingVoteCount() int {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.pendingVoteCountLocked()
}

// handleVote processes a vote received from a peer.
func (e *BFTEngine) handleVote(vote *Vote) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	if vote == nil {
		return
	}
	pubKey := e.findValidatorPubKey(vote.ValidatorAddr)
	if pubKey == nil {
		log.Warn().Str("from", vote.ValidatorAddr.String()).Msg("BFT: vote from unknown validator")
		return
	}
	if err := vote.Verify(pubKey); err != nil {
		log.Warn().Err(err).Str("from", vote.ValidatorAddr.String()).Msg("BFT: invalid vote signature")
		return
	}

	key := vote.ValidatorAddr.String() + "|" + fmt.Sprintf("%d|%d", vote.Height, vote.Round)
	if prev, ok := e.seenVotes[key]; ok {
		if prev.BlockHash != vote.BlockHash {
			log.Warn().
				Str("from", vote.ValidatorAddr.String()).
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

	if e.voteSet == nil || vote.Height != e.currentHeight || vote.Round != e.currentRound {
		// The proposal it belongs to may simply not have arrived yet: the
		// leader broadcasts the proposal and its vote as two sealed messages
		// and the network is free to reorder them. Dropping the vote here cost
		// the height a full round timeout, which with two validators is the
		// whole quorum.
		if vote.Height >= e.currentHeight {
			e.bufferVote(vote)
		}
		return
	}

	reachedQuorum, err := e.voteSet.AddVote(vote)
	if err != nil {
		log.Debug().Err(err).Msg("BFT: rejected vote")
		return
	}

	log.Debug().
		Str("from", vote.ValidatorAddr.String()).
		Int("votes", e.voteSet.VoteCount()).
		Int("required", e.voteSet.requiredWeight).
		Msg("BFT: vote collected")

	if reachedQuorum && e.proposal != nil {
		e.recordQuorum(e.proposal)
		e.commitBlock(e.proposal)
	}
}

func (e *BFTEngine) commitBlock(b *types.Block) {
	// SECURITY CHECK 1: Must have quorum
	if !e.hasQuorum || e.quorumHeight != b.Header.Height || e.quorumHash != b.Hash {
		log.Error().
			Uint64("height", b.Header.Height).
			Bool("has_quorum", e.hasQuorum).
			Msg("CRITICAL: attempting commit without valid quorum")
		return
	}

	// Build QC from vote set
	raw := e.voteSet.Votes()
	if len(raw) < e.voteSet.requiredWeight {
		log.Error().
			Uint64("height", b.Header.Height).
			Int("votes", len(raw)).
			Int("required", e.voteSet.requiredWeight).
			Msg("CRITICAL: insufficient votes for commit")
		return
	}

	// Convert votes to QC format with validation
	qcv := make([]types.QCVote, 0, len(raw))
	seen := make(map[qcrypto.Address]struct{})

	for _, v := range raw {
		// SECURITY CHECK 2: No duplicate votes
		if _, dup := seen[v.ValidatorAddr]; dup {
			log.Error().
				Str("validator", v.ValidatorAddr.String()).
				Msg("CRITICAL: duplicate vote in QC")
			return
		}
		seen[v.ValidatorAddr] = struct{}{}

		// SECURITY CHECK 3: Vote is for THIS block
		if v.Height != b.Header.Height || v.BlockHash != b.Hash {
			log.Error().
				Str("validator", v.ValidatorAddr.String()).
				Msg("CRITICAL: vote in QC is for wrong height/hash")
			return
		}

		qcv = append(qcv, types.QCVote{
			AlgID:         v.AlgID,
			Height:        v.Height,
			Round:         v.Round,
			BlockHash:     v.BlockHash,
			ValidatorAddr: v.ValidatorAddr,
			Signature:     v.Signature,
			Timestamp:     v.Timestamp,
		})
	}

	// Attach QC to block
	b.Votes = qcv
	root := types.ComputeQCRoot(qcv)
	b.Header.QCRoot = root[:]

	log.Info().
		Uint64("height", b.Header.Height).
		Int("votes", len(qcv)).
		Hex("qc_root", root[:8]).
		Msg("block committed with valid QC")

	// Attempt to apply block
	if e.onCommit != nil {
		if err := e.onCommit(b); err != nil {
			// Commit failed - clear ALL state including vote commitment
			// Nothing was committed, so there's nothing to be locked on
			log.Error().Err(err).Uint64("height", b.Header.Height).
				Msg("commit callback failed, will retry")

			e.hasQuorum = false
			e.hasVoted = false
			e.voteSet = nil
			e.proposal = nil
			e.lockedProposal = nil
			return
		}
	}

	// Success - emit committed block
	select {
	case e.commitCh <- b:
	default:
		log.Warn().Msg("commit channel full")
	}

	// Move to next height
	e.currentHeight = b.Header.Height + 1
	e.currentRound = 0

	// Clear ALL state (including vote commitment)
	e.voteSet = nil
	e.proposal = nil
	e.lockedProposal = nil
	e.hasVoted = false
	e.hasQuorum = false

	// Cleanup
	e.prunePendingVotes(e.currentHeight)
	e.pruneSeenVotes(e.currentHeight)
}

// pruneSeenVotes drops equivocation records for heights the chain has passed.
// The table is fed from the network and was never trimmed, so a long-running
// node accumulated one entry per validator per height for ever.
func (e *BFTEngine) pruneSeenVotes(below uint64) {
	for key, v := range e.seenVotes {
		if v.Height < below {
			delete(e.seenVotes, key)
		}
	}
}

func (e *BFTEngine) activeCount() int {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	return len(e.activeLocked())
}

// SetHeight moves the engine to the next height to produce.
//
// Moving to a DIFFERENT height resets the round, because a round is a
// statement about one height and the next one starts a fresh decision. Without
// this, a node that timed out once and then took the following blocks by push
// or pull-sync — rather than by its own commit, which does reset — stayed a
// round ahead of its peers for the life of the process and rejected every
// proposal the round-0 leader made.
//
// Re-setting the SAME height leaves the round alone: MultiValidator.propose
// calls this before every proposal, including the one that follows a view
// change, and clearing the round there would undo the view change.
func (e *BFTEngine) SetHeight(h uint64) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.currentHeight == h {
		return
	}
	e.currentHeight = h
	e.currentRound = 0
	e.hasVoted = false
	e.hasQuorum = false
	e.lockedProposal = nil

	// Crash recovery: A restart re-enters a height the previous process may
	// already have voted at. Round r is spent— the journal refuses every other
	// block there —so resume at r+1 and keep that hash. A later round may
	// re-sign it, never a different block. Reload the locked proposal body so
	// the leader can rebroadcast it instead of minting a new hash.
	if e.journal != nil {
		if jh, jr, jhash, ok := e.journal.Last(); ok && jh == h {
			e.currentRound = jr + 1
			e.votedHash, e.votedHeight, e.hasVoted = jhash, jh, true
			if prop, ok := e.journal.LoadProposal(); ok {
				e.lockedProposal = prop
			}
			log.Warn().
				Uint64("height", h).
				Uint32("journal_round", jr).
				Uint32("resume_round", jr+1).
				Hex("committed_to", jhash[:8]).
				Bool("proposal_body", e.lockedProposal != nil).
				Msg("crash recovery: restored vote commitment from journal")
		}
	}
	e.voteSet = nil
	e.proposal = nil
	e.prunePendingVotes(h)
	e.wake()
}

// LockedProposal returns the block this node is vote-locked to at the current
// height, when the body is still available (in memory or restored from the
// journal). Callers rebroadcast it instead of minting a new hash.
func (e *BFTEngine) LockedProposal() *types.Block {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.lockedProposal == nil || !e.hasVoted {
		return nil
	}
	if e.lockedProposal.Header.Height != e.votedHeight || e.lockedProposal.Hash != e.votedHash {
		return nil
	}
	return e.lockedProposal
}

// VoteLockedAt reports whether this node is already committed to a hash at h
// (in-memory or durable journal), even if the proposal body is missing.
func (e *BFTEngine) VoteLockedAt(h uint64) bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.hasVoted && e.votedHeight == h {
		return true
	}
	if e.journal != nil {
		if jh, _, _, ok := e.journal.Last(); ok && jh == h {
			return true
		}
	}
	return false
}

// Height returns the height the engine is working on.
func (e *BFTEngine) Height() uint64 {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.currentHeight
}

// Round returns the current view-change round.
func (e *BFTEngine) Round() uint32 {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.currentRound
}

// findValidatorPubKey returns the public key for a validator address, or nil.
func (e *BFTEngine) findValidatorPubKey(addr qcrypto.Address) []byte {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	for _, v := range e.validators {
		if v.Address == addr {
			pk := make([]byte, len(v.PublicKey))
			copy(pk, v.PublicKey)
			return pk
		}
	}
	return nil
}
