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

	// Kind is empty for the pre-bft_rounds_v1 single vote. Under
	// bft_rounds_v1 it is types.VoteKindPrevote or VoteKindPrecommit and
	// is inside the signed digest.
	Kind string `json:"kind,omitempty"`
}

// VoteBody is the canonical signable subset of Vote (excludes Signature).
type VoteBody struct {
	AlgID         string          `json:"alg_id"`
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	BlockHash     [32]byte        `json:"block_hash"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Timestamp     int64           `json:"timestamp"`
	Kind          string          `json:"kind,omitempty"`
}

// Hash computes SHA3-256 of the canonical vote body.
//
// AlgID is inside the signed body, so an attacker cannot downgrade a vote by
// rewriting the algorithm label on the wire.
func (v *Vote) Hash() ([32]byte, error) {
	return types.VoteDigest(v.AlgID, v.Height, v.Round, v.BlockHash, v.ValidatorAddr, v.Timestamp, v.Kind)
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
	kind           string                    // empty, prevote, or precommit
}

// NewVoteSet creates a new vote set for a block at height/round.
func NewVoteSet(height uint64, round uint32, blockHash [32]byte, validatorCount int) *VoteSet {
	return newVoteSet(height, round, blockHash, validatorCount, "")
}

func newVoteSet(height uint64, round uint32, blockHash [32]byte, validatorCount int, kind string) *VoteSet {
	required := (validatorCount*2)/3 + 1
	return &VoteSet{
		height:         height,
		round:          round,
		blockHash:      blockHash,
		votes:          make(map[qcrypto.Address]*Vote),
		totalWeight:    validatorCount,
		requiredWeight: required,
		kind:           kind,
	}
}

// ErrDuplicateVote reports a vote from a validator the set already holds one
// from. The set is built for one block hash and rejects any other, so a
// duplicate necessarily carries the same hash as the vote already counted:
// it is an honest re-send, not an equivocation, and it is counted once.
var ErrDuplicateVote = errors.New("vote already counted from this validator")

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
	if vs.kind != "" && vote.Kind != vs.kind {
		return false, fmt.Errorf("wrong vote kind: want %s got %s", vs.kind, vote.Kind)
	}

	// Already counted. Note what this is NOT: a vote for a different block is
	// rejected above, on the hash, so anything reaching here agrees with the
	// vote already held. It is a re-send — the network duplicated it, or a
	// leader's rebroadcast asked this node to say again what it already said —
	// and it is counted once. Callers tell it apart with ErrDuplicateVote and
	// must not report it as a fault.
	if _, exists := vs.votes[vote.ValidatorAddr]; exists {
		return false, fmt.Errorf("%w: %s", ErrDuplicateVote, vote.ValidatorAddr)
	}

	vs.votes[vote.ValidatorAddr] = vote

	quorumBefore := len(vs.votes)-1 >= vs.requiredWeight
	quorumNow := len(vs.votes) >= vs.requiredWeight

	return !quorumBefore && quorumNow, nil
}

// VoteFrom returns a copy of the vote addr cast into this set, or nil. A nil
// set holds nothing. Used to re-send a vote this node already signed without
// signing a second one: the bytes are identical, so a peer that already
// counted it sees a duplicate rather than an equivocation.
func (vs *VoteSet) VoteFrom(addr qcrypto.Address) *Vote {
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	v, ok := vs.votes[addr]
	if !ok {
		return nil
	}
	cp := *v
	return &cp
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
	broadcastRC      func(*RoundChange)
	roundsActive     func(uint64) bool
	rcCh             chan *RoundChange
	precommits       *VoteSet
	hasPOL           bool
	polHash          [32]byte
	polRound         uint32
	polHeight        uint64
	// polVotes is the prevote quorum (POL) this node witnessed for polHash at
	// (polHeight, polRound). Retained — not just the hash/round — so a leader
	// re-proposing the locked value can attach the actual certificate and let a
	// peer that missed the quorum verify and unlock to it.
	polVotes  []*Vote
	rcByRound map[uint32]map[qcrypto.Address]*RoundChange
	rcQCRound int32
	// rcValue* is the safe value selected from the current round-change quorum:
	// the one with the highest locked round, plus its certificate. A leader must
	// re-propose this value (or hold for its body) instead of minting a value
	// that could conflict with a validator's lock.
	rcValueHash  [32]byte
	rcValueRound uint32
	rcValueVotes []*Vote
	rcValueSet   bool
}

// BFTConfig configures the BFT engine.
type BFTConfig struct {
	LocalAddr    qcrypto.Address
	LocalPubKey  []byte
	LocalPrivKey *qcrypto.PrivateKey
	// VoteTimeout and RoundTimeout default to DefaultVoteTimeout and
	// DefaultRoundTimeout when zero. RoundTimeout is how long a view is given
	// before this node rotates the leader away; a set spread across regions
	// needs it raised (see the constants).
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
	// RoundsV1 reports whether two-phase BFT is active at height. nil keeps
	// the pre-fork single-vote path.
	RoundsV1 func(height uint64) bool
	// BroadcastRoundChange sends an authenticated round-change. nil (tests)
	// still records the local message so a solo validator can form a QC.
	BroadcastRoundChange func(*RoundChange)
}

// DefaultRoundTimeout and DefaultVoteTimeout are the deadlines a node uses
// when an operator has not set them.
//
// Three seconds is comfortable for validators in one region and tight for a
// set spread across continents: the round-0 leader has to receive the previous
// block, build on it, sign with ML-DSA-65 and get the proposal back across the
// link before its peers give up on it. On a four-validator set with 200ms
// added to one member, that member missed 29 of its 84 leader slots — 34.5% —
// while the three co-located members missed none at all, and every view change
// in the window was one of its heights. Operators running validators far apart
// need to raise this; there is no auto-tuning, by design.
const (
	DefaultRoundTimeout = 3 * time.Second
	DefaultVoteTimeout  = 800 * time.Millisecond
)

// NewBFTEngine creates a working multi-validator BFT engine.
func NewBFTEngine(cfg BFTConfig) (*BFTEngine, error) {
	if cfg.VoteTimeout == 0 {
		cfg.VoteTimeout = DefaultVoteTimeout
	}
	if cfg.RoundTimeout == 0 {
		cfg.RoundTimeout = DefaultRoundTimeout
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
		broadcastRC:      cfg.BroadcastRoundChange,
		roundsActive:     cfg.RoundsV1,
		voteCh:           make(chan *Vote, 1000),
		commitCh:         make(chan *types.Block, 16),
		proposeCh:        make(chan *pendingProposal, 16),
		wakeCh:           make(chan struct{}, 1),
		rcCh:             make(chan *RoundChange, 256),
		seenVotes:        make(map[string]*Vote),
		pendingVotes:     make(map[string][]*Vote),
		rcByRound:        make(map[uint32]map[qcrypto.Address]*RoundChange),
		rcQCRound:        -1,
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

// logVoteAddFailure reports why a vote was not added to a set. A re-send of a
// vote already counted is normal traffic — the network duplicates messages and
// a leader's rebroadcast asks for the vote again — so it is logged at debug.
// Anything else means the vote did not belong to the set at all, which is
// worth an operator's attention.
func logVoteAddFailure(err error, what string) {
	if errors.Is(err, ErrDuplicateVote) {
		log.Debug().Err(err).Msgf("BFT: %s already counted, ignoring the re-send", what)
		return
	}
	log.Error().Err(err).Msgf("BFT: failed to add %s", what)
}

// UpdateValidatorSet updates the active validator set.
// This is called when the on-chain validator registry changes (epoch transitions).
func (e *BFTEngine) UpdateValidatorSet(validators []ValidatorInfo) {
	e.validatorsMu.Lock()
	defer e.validatorsMu.Unlock()
	changed := len(e.validators) != len(validators)
	for i := range validators {
		if !changed && (e.validators[i].Address != validators[i].Address ||
			e.validators[i].IsActive != validators[i].IsActive) {
			changed = true
		}
	}
	e.validators = make([]ValidatorInfo, len(validators))
	copy(e.validators, validators)
	// Logged only when the set actually moved. refreshSet runs on every tick
	// AND on every proposal, so an unconditional line here was three
	// "validator set updated" a second on a busy node — noise that read like
	// a refresh loop while the real fault was elsewhere.
	if changed {
		log.Info().Int("count", len(validators)).Msg("BFT: validator set updated")
	}
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
	// Membership and signature are checked BEFORE the vote occupies a
	// voteCh slot. Junk from an anonymous sealed sender used to fill the
	// 1000-deep queue and starve authenticated consensus traffic.
	if err := e.admitVote(vote); err != nil {
		log.Debug().Err(err).Msg("BFT: dropping unauthenticated vote")
		return
	}
	select {
	case e.voteCh <- vote:
	default:
		log.Warn().Msg("BFT: vote channel full, dropping vote")
	}
}

func (e *BFTEngine) admitVote(vote *Vote) error {
	if vote == nil {
		return fmt.Errorf("nil vote")
	}
	pubKey := e.findValidatorPubKey(vote.ValidatorAddr)
	if pubKey == nil {
		return fmt.Errorf("vote from unknown validator %s", vote.ValidatorAddr)
	}
	return vote.Verify(pubKey)
}

// voteQueueLen is the number of admitted votes waiting on voteCh.
func (e *BFTEngine) voteQueueLen() int { return len(e.voteCh) }

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

// SubmitRoundChange queues an authenticated round-change from a peer.
func (e *BFTEngine) SubmitRoundChange(rc *RoundChange) {
	if rc == nil {
		return
	}
	pubKey := e.findValidatorPubKey(rc.ValidatorAddr)
	if pubKey == nil {
		log.Debug().Str("from", rc.ValidatorAddr.String()).Msg("BFT: dropping unknown-validator round-change")
		return
	}
	if err := rc.Verify(pubKey); err != nil {
		log.Debug().Err(err).Msg("BFT: dropping unauthenticated round-change")
		return
	}
	select {
	case e.rcCh <- rc:
	default:
		log.Warn().Msg("BFT: round-change channel full, dropping")
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
		case rc := <-e.rcCh:
			e.handleRoundChange(rc)
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
// changes, except that a NEWLY adopted proposal at the same view renews it:
// the engine stops waiting for a leader's proposal and starts waiting for
// votes on one, which is a different wait.
//
// A repeat of a proposal already adopted is not a new adoption.
// handleProposal reports those as not accepted, and they must stay that way:
// a leader rebroadcasting its locked body on a 2s interval renewed a 3s
// deadline before it could fire, so the view change that would have rotated a
// stuck round away never happened at all.
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
	if e.roundsV1(e.currentHeight) {
		return e.viewChangeTwoPhaseLocked()
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
	if e.roundsV1(p.block.Header.Height) {
		return e.handleProposalTwoPhase(p)
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
			logVoteAddFailure(addErr, "own vote")
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

func voteSeenKey(v *Vote) string {
	if v.Kind == "" {
		return v.ValidatorAddr.String() + "|" + fmt.Sprintf("%d|%d", v.Height, v.Round)
	}
	return v.ValidatorAddr.String() + "|" + fmt.Sprintf("%d|%d|%s", v.Height, v.Round, v.Kind)
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
	var rest []*Vote
	for _, v := range held {
		added := false
		if e.precommits != nil && v.Kind == types.VoteKindPrecommit {
			if _, err := e.precommits.AddVote(v); err != nil {
				log.Debug().Err(err).Msg("BFT: early precommit not counted")
			}
			added = true
		} else if e.voteSet != nil && (e.voteSet.kind == "" || v.Kind == e.voteSet.kind) {
			if _, err := e.voteSet.AddVote(v); err != nil {
				log.Debug().Err(err).Msg("BFT: early vote not counted")
			}
			added = true
		}
		if !added {
			rest = append(rest, v)
		}
	}
	if len(rest) == 0 {
		delete(e.pendingVotes, key)
	} else {
		e.pendingVotes[key] = rest
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
	if vote.Kind != "" || e.roundsV1(vote.Height) {
		e.handleVoteTwoPhase(vote)
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

	key := voteSeenKey(vote)
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

	// Build QC from vote set. Two-phase commits on precommits.
	vs := e.voteSet
	if e.precommits != nil {
		vs = e.precommits
	}
	raw := vs.Votes()
	if len(raw) < vs.requiredWeight {
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
			Kind:          v.Kind,
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
			// A failed apply means the block was not stored. It does NOT mean
			// this node did not vote.
			//
			// This used to clear the whole view — proposal, vote sets, the
			// POL, the vote commitment and the round-change QC — on the
			// reasoning that nothing was committed so there was nothing to be
			// locked on. But the prevote this node cast at (currentHeight,
			// currentRound) was broadcast and is in every peer's seenVotes,
			// keyed validator|height|round|kind. Forgetting it left the node
			// free to adopt a DIFFERENT value at the very same view and
			// prevote again, and every peer answered the second one with
			// "equivocation — conflicting votes dropped" and kept that key
			// poisoned for the round. A node that had only prevoted had
			// nothing in its journal to fall back on, so nothing caught it.
			// Rare, because it takes a failed apply — which is exactly how it
			// showed up live: four lines at two heights, from one node.
			//
			// Clearing the lock was not even effective: lockAt consults the
			// journal, which still holds any precommit this node signed, so
			// dropping the in-memory copy only lost lockedProposal — the body
			// the leader needs in order to rebroadcast rather than mint.
			//
			// The VIEW is left exactly as it was — the proposal, the vote
			// sets, the POL and the round-change QC all still describe the
			// value this node voted for, and a re-delivery of that same block
			// still commits (TestFailedCommitDoesNotWedgeTheHeight).
			//
			// The quorum and the vote-commitment FLAGS are dropped, as they
			// always were: nothing was committed, so a later quorum for the
			// same block must be able to record itself, and the next height
			// must stay votable (TestFailedCommitDoesNotLockOutNextHeight).
			// Dropping hasVoted costs no safety — a precommit this node
			// actually signed is in the journal, which lockAt consults, so the
			// lock survives there.
			//
			// Recovery from a value that will not apply is the round timer: it
			// fires, the view changes, and the next round starts clean. That is
			// what a round change is for.
			log.Error().Err(err).Uint64("height", b.Header.Height).
				Uint32("round", e.currentRound).
				Msg("commit callback failed; holding the view for the round timer")

			e.hasQuorum = false
			e.hasVoted = false
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
	e.precommits = nil
	e.proposal = nil
	e.lockedProposal = nil
	e.hasVoted = false
	e.hasQuorum = false
	e.hasPOL = false
	e.polVotes = nil
	e.clearRoundChangeLocked()

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
	e.hasPOL = false
	e.polVotes = nil
	e.precommits = nil
	e.clearRoundChangeLocked()
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
// journal). A round-change-selected value (higher than this node's own lock) is
// preferred when its body is locally held, so the leader re-proposes the safe
// value instead of its own lower lock. Callers rebroadcast it instead of
// minting a new hash.
func (e *BFTEngine) LockedProposal() *types.Block {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.lockedProposal == nil {
		return nil
	}
	if e.rcValueSet && e.lockedProposal.Header.Height == e.currentHeight && e.lockedProposal.Hash == e.rcValueHash {
		return e.lockedProposal
	}
	if e.hasVoted && e.lockedProposal.Header.Height == e.votedHeight && e.lockedProposal.Hash == e.votedHash {
		return e.lockedProposal
	}
	if e.hasPOL && e.lockedProposal.Header.Height == e.polHeight && e.lockedProposal.Hash == e.polHash {
		return e.lockedProposal
	}
	return nil
}

// AdoptedProposal returns the block this node has already adopted — and
// prevoted for — at its current view, or nil. A caller about to build a
// proposal uses it to see that the view is taken: the engine refuses any other
// body there until a round change, so minting one is wasted work.
func (e *BFTEngine) AdoptedProposal() *types.Block {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.proposal
}

// VoteLockedAt reports whether this node is already committed to a hash at h
// (in-memory or durable journal), even if the proposal body is missing.
func (e *BFTEngine) VoteLockedAt(h uint64) bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.hasVoted && e.votedHeight == h {
		return true
	}
	if e.hasPOL && e.polHeight == h {
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

// ConsensusStatus is a point-in-time snapshot of the engine's progress, for
// readiness and diagnostics. Locked reports the node's in-memory vote lock (a
// prevote it cast, or a POL it witnessed) at the current height — a normal
// in-round state, not a failure. Callers must not infer health from Locked
// alone.
type ConsensusStatus struct {
	Height       uint64
	Round        uint32
	Locked       bool
	LockedHeight uint64
}

// Status returns a consistent snapshot of the current height, round and vote
// lock under a single stateMu hold.
func (e *BFTEngine) Status() ConsensusStatus {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	st := ConsensusStatus{Height: e.currentHeight, Round: e.currentRound}
	if e.hasVoted && e.votedHeight == e.currentHeight {
		st.Locked = true
		st.LockedHeight = e.votedHeight
	} else if e.hasPOL && e.polHeight == e.currentHeight {
		st.Locked = true
		st.LockedHeight = e.polHeight
	}
	return st
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
