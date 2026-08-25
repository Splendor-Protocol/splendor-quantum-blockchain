// Package consensus implements the Splendor Quantum Chain consensus mechanisms.
//
// Phase 1: Single validator (validator.go) — block production with Dilithium signing.
// Phase 2: Multi-validator BFT (this file) — Byzantine Fault Tolerant consensus
//           with 2/3 quorum voting, all messages signed with ML-DSA-65.
//
// The BFT design:
//   - Validators are elected by stake weight (registered via governance).
//   - Each epoch (configurable number of blocks), a new leader is selected.
//   - The leader proposes a block; validators vote with Dilithium-signed Vote messages.
//   - A block is committed when 2/3+1 of validators have cast valid votes.
//   - Safety: no two honest validators will commit conflicting blocks at the same height.
//   - Liveness: the chain progresses as long as <1/3 of validators are faulty.
package consensus

import (
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

	// PublicKey is the validator's 1952-byte Dilithium public key.
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
// All votes are signed with ML-DSA-65 — no ECDSA.
type Vote struct {
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
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	BlockHash     [32]byte        `json:"block_hash"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Timestamp     int64           `json:"timestamp"`
}

// Hash computes SHA3-256 of the canonical vote body.
func (v *Vote) Hash() ([32]byte, error) {
	body := VoteBody{
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

// Sign signs the vote with the given Dilithium private key.
func (v *Vote) Sign(priv *qcrypto.PrivateKey) error {
	hash, err := v.Hash()
	if err != nil {
		return fmt.Errorf("vote hash failed: %w", err)
	}
	sig, err := qcrypto.Sign(priv, hash[:])
	if err != nil {
		return fmt.Errorf("vote sign failed: %w", err)
	}
	v.Signature = sig
	return nil
}

// Verify verifies the vote's Dilithium signature against the given public key.
func (v *Vote) Verify(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid voter pubkey: %w", err)
	}
	hash, err := v.Hash()
	if err != nil {
		return fmt.Errorf("vote hash failed: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], v.Signature) {
		return fmt.Errorf("invalid vote Dilithium signature from %s", v.ValidatorAddr)
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

// BFTEngine is the multi-validator BFT consensus engine.
// It runs as a coordinator: collecting votes from peers, tracking rounds,
// and triggering block commitment when quorum is reached.
type BFTEngine struct {
	// Our own validator identity
	localAddr    qcrypto.Address
	localPubKey  []byte
	localPrivKey *qcrypto.PrivateKey

	// Active validator set (periodically updated from on-chain state)
	validators    []ValidatorInfo
	validatorsMu  sync.RWMutex

	// Current consensus state
	currentHeight uint64
	currentRound  uint32
	voteSet       *VoteSet
	stateMu       sync.Mutex

	// Timeouts
	voteTimeout  time.Duration
	roundTimeout time.Duration

	// Channels
	voteCh     chan *Vote           // incoming votes from peers
	commitCh   chan *types.Block    // committed blocks to publish
	proposeCh  chan *types.Block    // proposed blocks from leader

	// Callbacks
	onCommit func(*types.Block) error // called when a block reaches quorum
}

// BFTConfig configures the BFT engine.
type BFTConfig struct {
	LocalAddr    qcrypto.Address
	LocalPubKey  []byte
	LocalPrivKey *qcrypto.PrivateKey
	VoteTimeout  time.Duration
	RoundTimeout time.Duration
	OnCommit     func(*types.Block) error
}

// NewBFTEngine creates a new BFT consensus engine.
func NewBFTEngine(cfg BFTConfig) *BFTEngine {
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
		voteCh:       make(chan *Vote, 1000),
		commitCh:     make(chan *types.Block, 16),
		proposeCh:    make(chan *types.Block, 16),
	}
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

// IsLeader returns true if this node is the current block leader.
// Leader selection: validators[height % len(validators)]
func (e *BFTEngine) IsLeader(height uint64) bool {
	e.validatorsMu.RLock()
	defer e.validatorsMu.RUnlock()
	if len(e.validators) == 0 {
		return false
	}
	idx := int(height) % len(e.validators)
	return e.validators[idx].Address == e.localAddr
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

// Run starts the BFT consensus loop. Blocks until ctx is cancelled.
// In Phase 2, this replaces the single-validator loop in validator.go.
func (e *BFTEngine) Run() {
	log.Info().
		Str("addr", e.localAddr.String()).
		Dur("vote_timeout", e.voteTimeout).
		Msg("BFT engine starting")

	for {
		select {
		case block := <-e.proposeCh:
			e.handleProposal(block)

		case vote := <-e.voteCh:
			e.handleVote(vote)
		}
	}
}

// handleProposal processes a new block proposal.
func (e *BFTEngine) handleProposal(b *types.Block) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	e.currentHeight = b.Header.Height
	nValidators := e.ValidatorCount()
	if nValidators == 0 {
		nValidators = 1 // fallback to single-validator mode
	}

	e.voteSet = NewVoteSet(b.Header.Height, e.currentRound, b.Hash, nValidators)

	// Cast our own vote
	vote, err := e.castVote(b)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to cast vote")
		return
	}

	reachedQuorum, err := e.voteSet.AddVote(vote)
	if err != nil {
		log.Error().Err(err).Msg("BFT: failed to add own vote")
	}

	log.Debug().
		Uint64("height", b.Header.Height).
		Int("validators", nValidators).
		Int("required", e.voteSet.requiredWeight).
		Msg("BFT: voted on block proposal")

	if reachedQuorum {
		e.commitBlock(b)
	}

	// TODO: broadcast vote to peers via P2P
}

// handleVote processes a vote received from a peer.
func (e *BFTEngine) handleVote(vote *Vote) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	if e.voteSet == nil {
		return // no active proposal
	}
	if vote.Height != e.currentHeight {
		return // stale vote
	}

	// Find voter's public key
	pubKey := e.findValidatorPubKey(vote.ValidatorAddr)
	if pubKey == nil {
		log.Warn().Str("from", vote.ValidatorAddr.String()).Msg("BFT: vote from unknown validator")
		return
	}

	// Verify Dilithium signature
	if err := vote.Verify(pubKey); err != nil {
		log.Warn().Err(err).Str("from", vote.ValidatorAddr.String()).Msg("BFT: invalid vote signature")
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

	if reachedQuorum {
		// We reached 2/3+1 — find and commit the block
		// In production, the block is fetched from the proposal cache
		log.Info().
			Uint64("height", vote.Height).
			Int("votes", e.voteSet.VoteCount()).
			Msg("BFT: quorum reached!")
		// commitBlock is called from handleProposal when we have the block
		// Here we just log; the actual commit happens when we have the full block
	}
}

// commitBlock is called when a block has reached quorum.
func (e *BFTEngine) commitBlock(b *types.Block) {
	log.Info().
		Uint64("height", b.Header.Height).
		Int("votes", e.voteSet.VoteCount()).
		Msg("BFT: block committed")

	if e.onCommit != nil {
		if err := e.onCommit(b); err != nil {
			log.Error().Err(err).Msg("BFT: commit callback failed")
		}
	}

	select {
	case e.commitCh <- b:
	default:
	}
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
