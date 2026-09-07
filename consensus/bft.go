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
	totalWeight    int                        // total number of validators
	requiredWeight int                        // 2/3 + 1 threshold
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

// ProposalBroadcaster sends a proposed block to peers.
type ProposalBroadcaster func(b *types.Block)

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
	seenVotes     map[string]*Vote // validator|height|round → first vote (equivocation)
	lockedHash    [32]byte
	hasLock       bool
	stateMu       sync.Mutex

	voteTimeout  time.Duration
	roundTimeout time.Duration

	voteCh    chan *Vote
	commitCh  chan *types.Block
	proposeCh chan *types.Block

	onCommit     func(*types.Block) error
	onViewChange func(height uint64, round uint32)
	broadcast    VoteBroadcaster
	broadcastP   ProposalBroadcaster
}

// BFTConfig configures the BFT engine.
type BFTConfig struct {
	LocalAddr    qcrypto.Address
	LocalPubKey  []byte
	LocalPrivKey *qcrypto.PrivateKey
	VoteTimeout  time.Duration
	RoundTimeout time.Duration
	OnCommit          func(*types.Block) error
	OnViewChange      func(height uint64, round uint32)
	Broadcast         VoteBroadcaster
	BroadcastProposal ProposalBroadcaster
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
		localAddr:    cfg.LocalAddr,
		localPubKey:  cfg.LocalPubKey,
		localPrivKey: cfg.LocalPrivKey,
		voteTimeout:  cfg.VoteTimeout,
		roundTimeout: cfg.RoundTimeout,
		onCommit:     cfg.OnCommit,
		onViewChange: cfg.OnViewChange,
		broadcast:    cfg.Broadcast,
		broadcastP:   cfg.BroadcastProposal,
		voteCh:       make(chan *Vote, 1000),
		commitCh:     make(chan *types.Block, 16),
		proposeCh:    make(chan *types.Block, 16),
		seenVotes:    make(map[string]*Vote),
	}, nil
}

// UpdateValidatorSet updates the active validator set.
// This is called when the on-chain validator registry changes (epoch transitions).
func (e *BFTEngine) UpdateValidatorSet(validators []ValidatorInfo) {
	e.validatorsMu.Lock()
	defer e.validatorsMu.Unlock()
	e.validators = make([]ValidatorInfo, len(validators))
	copy(e.validators, validators)
	log.Info().Int("count", len(validators)).Msg("BFT: validator set updated")
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
	select {
	case e.voteCh <- vote:
	default:
		log.Warn().Msg("BFT: vote channel full, dropping vote")
	}
}

// ProposeBlock submits a new block proposal (called by leader's block producer).
func (e *BFTEngine) ProposeBlock(b *types.Block) {
	select {
	case e.proposeCh <- b:
	default:
	}
}

// CommitCh returns a channel that emits committed blocks.
func (e *BFTEngine) CommitCh() <-chan *types.Block {
	return e.commitCh
}

// castVote creates and signs a vote for the given block.
func (e *BFTEngine) castVote(b *types.Block) (*Vote, error) {
	vote := &Vote{
		Height:        b.Header.Height,
		Round:         e.currentRound,
		BlockHash:     b.Hash,
		ValidatorAddr: e.localAddr,
		Timestamp:     time.Now().Unix(),
	}
	if err := vote.Sign(e.localPrivKey); err != nil {
		return nil, err
	}
	return vote, nil
}

// Run drives proposals, votes, timeouts, and commits until ctx is cancelled.
func (e *BFTEngine) Run(ctx context.Context) error {
	log.Info().Str("addr", e.localAddr.String()).Msg("BFT: multi-validator engine starting")
	timer := time.NewTimer(e.roundTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("BFT: stopping")
			return nil
		case b := <-e.proposeCh:
			e.handleProposal(b)
			resetTimer(timer, e.roundTimeout)
		case vote := <-e.voteCh:
			e.handleVote(vote)
		case <-timer.C:
			e.handleTimeout()
			timer.Reset(e.roundTimeout)
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (e *BFTEngine) handleTimeout() {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.ValidatorCount() == 0 {
		return
	}
	e.currentRound++
	e.voteSet = nil
	e.proposal = nil
	h, r := e.currentHeight, e.currentRound
	log.Warn().Uint64("height", h).Uint32("round", r).
		Msg("BFT: round timeout, view change")
	cb := e.onViewChange
	if cb != nil {
		go cb(h, r)
	}
}

// handleProposal processes a new block proposal.
func (e *BFTEngine) handleProposal(b *types.Block) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	if b == nil {
		return
	}
	nValidators := e.activeCount()
	if nValidators == 0 {
		log.Error().Uint64("height", b.Header.Height).
			Msg("BFT: no validator set, cannot form a quorum for this proposal")
		return
	}

	leader, ok := e.LeaderAddress(b.Header.Height, e.currentRound)
	if !ok || b.Header.ValidatorAddr != leader {
		log.Warn().
			Uint64("height", b.Header.Height).
			Str("proposer", b.Header.ValidatorAddr.String()).
			Str("leader", leader.String()).
			Msg("BFT: proposal from non-leader rejected")
		return
	}

	if e.hasLock && b.Hash != e.lockedHash {
		log.Warn().Msg("BFT: proposal conflicts with locked block")
		return
	}

	e.currentHeight = b.Header.Height
	e.proposal = b
	e.voteSet = NewVoteSet(b.Header.Height, e.currentRound, b.Hash, nValidators)

	vote, err := e.castVote(b)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to cast vote")
		return
	}
	reachedQuorum, err := e.voteSet.AddVote(vote)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to add own vote")
	}
	if e.broadcast != nil {
		e.broadcast(vote)
	}

	log.Debug().
		Uint64("height", b.Header.Height).
		Int("validators", nValidators).
		Int("required", e.voteSet.requiredWeight).
		Msg("BFT: voted on block proposal")

	if reachedQuorum {
		e.commitBlock(b)
	}
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
		cp := *vote
		e.seenVotes[key] = &cp
	}

	if e.voteSet == nil || vote.Height != e.currentHeight || vote.Round != e.currentRound {
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
		e.lockedHash = e.proposal.Hash
		e.hasLock = true
		e.commitBlock(e.proposal)
	}
}

func (e *BFTEngine) commitBlock(b *types.Block) {
	raw := e.voteSet.Votes()
	qcv := make([]types.QCVote, 0, len(raw))
	for _, v := range raw {
		qcv = append(qcv, types.QCVote{
			AlgID: v.AlgID, Height: v.Height, Round: v.Round,
			BlockHash: v.BlockHash, ValidatorAddr: v.ValidatorAddr,
			Signature: v.Signature, Timestamp: v.Timestamp,
		})
	}
	b.Votes = qcv
	root := types.ComputeQCRoot(qcv)
	b.Header.QCRoot = root[:]

	log.Info().
		Uint64("height", b.Header.Height).
		Int("votes", len(qcv)).
		Msg("BFT: block committed")

	if e.onCommit != nil {
		if err := e.onCommit(b); err != nil {
			log.Error().Err(err).Msg("BFT: commit callback failed")
			return
		}
	}

	select {
	case e.commitCh <- b:
	default:
	}
	e.currentHeight = b.Header.Height + 1
	e.currentRound = 0
	e.voteSet = nil
	e.proposal = nil
	e.hasLock = false
}

func (e *BFTEngine) activeCount() int {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	return len(e.activeLocked())
}

// SetHeight initialises the engine at the next height to produce.
func (e *BFTEngine) SetHeight(h uint64) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.currentHeight = h
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
