// Package vm — On-chain Governance for Splendor Quantum Chain.
//
// The Governance contract enables token holders and validators to submit
// proposals, vote, and execute passed changes — all on-chain, all quantum-safe.
//
// Proposal lifecycle:
//   Pending → Active (voting open) → Passed/Rejected → Executed/Expired
//
// Voting rules:
//   - Only accounts with ≥ 1 SPLD staked (or ≥ 1 SPLD balance) can vote
//   - Each address gets one vote per proposal
//   - Proposals pass at 2/3 supermajority of votes cast
//   - Quorum: ≥ 4% of total voting power must participate
//   - Discussion period: 7 days (in blocks)
//   - Voting period: 5 days (in blocks)
//   - Execution delay: 48 hours (in blocks)
//
// All operations: SHA3-256 storage slots, SQ: addresses, ML-DSA-65-signed txns.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// Block-time assumptions (1 block = 2 seconds)
const (
	BlocksPerDay       = uint64(43_200)  // 24h / 2s
	DiscussionBlocks   = 7 * BlocksPerDay
	VotingBlocks       = 5 * BlocksPerDay
	ExecutionDelayBlocks = 2 * BlocksPerDay // 48h timelock
	QuorumBasisPoints  = 400 // 4.00% of total voting power (basis points)
	PassBasisPoints    = 6667 // 66.67% supermajority
)

// ProposalStatus represents a proposal's current state.
type ProposalStatus uint8

const (
	ProposalPending  ProposalStatus = 0 // submitted, in discussion period
	ProposalActive   ProposalStatus = 1 // voting is open
	ProposalPassed   ProposalStatus = 2 // voting closed, passed, awaiting execution
	ProposalRejected ProposalStatus = 3 // voting closed, did not pass
	ProposalExecuted ProposalStatus = 4 // proposal actions have been executed
	ProposalExpired  ProposalStatus = 5 // passed but not executed within deadline
	ProposalCanceled ProposalStatus = 6 // canceled by proposer before voting
)

// ProposalType categorises what kind of change a proposal makes.
type ProposalType uint8

const (
	ProposalTypeText         ProposalType = 0 // signalling only, no on-chain action
	ProposalTypeParamChange  ProposalType = 1 // change a chain parameter
	ProposalTypeUpgrade      ProposalType = 2 // node software upgrade signal
	ProposalTypeFunding      ProposalType = 3 // treasury fund allocation
	ProposalTypeValidatorSet ProposalType = 4 // change validator set composition
)

// VoteOption represents how a voter votes on a proposal.
type VoteOption uint8

const (
	VoteYes        VoteOption = 1
	VoteNo         VoteOption = 2
	VoteAbstain    VoteOption = 3
	VoteNoWithVeto VoteOption = 4 // strong no; if >1/3 of votes, proposal vetoed
)

// Proposal is stored on-chain for each governance proposal.
type Proposal struct {
	ID           uint64         `json:"id"`
	Type         ProposalType   `json:"type"`
	Status       ProposalStatus `json:"status"`
	Proposer     qcrypto.Address `json:"proposer"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	// For ProposalTypeParamChange: JSON-encoded parameter changes
	// For ProposalTypeFunding: JSON-encoded funding request
	Payload      string         `json:"payload,omitempty"`

	// Block heights
	SubmitHeight    uint64 `json:"submit_height"`
	VotingStartHeight uint64 `json:"voting_start_height"`
	VotingEndHeight   uint64 `json:"voting_end_height"`
	ExecuteAfterHeight uint64 `json:"execute_after_height"`

	// Vote tallies (in voting power units = SPLD staked or balance)
	YesVotes     *big.Int `json:"yes_votes"`
	NoVotes      *big.Int `json:"no_votes"`
	AbstainVotes *big.Int `json:"abstain_votes"`
	VetoVotes    *big.Int `json:"veto_votes"`
	TotalVotes   *big.Int `json:"total_votes"`
}

// ── Storage slot derivation ────────────────────────────────────────────────────

var (
	slotGovProposalCount = qcrypto.Hash32([]byte("gov:proposal_count"))
	slotGovTotalPower    = qcrypto.Hash32([]byte("gov:total_voting_power"))
)

func govProposalSlot(id uint64) [32]byte {
	key := fmt.Sprintf("gov:proposal:%d", id)
	return qcrypto.Hash32([]byte(key))
}

func govVoteSlot(proposalID uint64, voter qcrypto.Address) [32]byte {
	key := fmt.Sprintf("gov:vote:%d:", proposalID)
	return qcrypto.Hash32(append([]byte(key), voter[:]...))
}

func govVoterPowerSlot(voter qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("gov:power:"), voter[:]...))
}

// govVoteWeightSlot stores the weight a vote was counted with, so a re-vote
// or a later correction removes exactly what was added.
func govVoteWeightSlot(proposalID uint64, voter qcrypto.Address) [32]byte {
	key := fmt.Sprintf("gov:vote_weight:%d:", proposalID)
	return qcrypto.Hash32(append([]byte(key), voter[:]...))
}

// ── GovernanceContract ────────────────────────────────────────────────────────

// GovernanceContract manages on-chain governance proposals and voting.
type GovernanceContract struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewGovernanceContract returns a handle to the governance contract.
func NewGovernanceContract(stateDB *state.StateDB, contract qcrypto.Address) *GovernanceContract {
	return &GovernanceContract{stateDB: stateDB, contract: contract}
}

// DeployGovernance deploys governance at a deterministic address.
func DeployGovernance(stateDB *state.StateDB) qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor_governance_contract_v1"))
	var addr qcrypto.Address
	copy(addr[:], h[:])
	return addr
}

// ── Proposal lifecycle ────────────────────────────────────────────────────────

// Advisory is the honest label for what this module does today. A passed
// proposal changes no chain parameter, moves no funds and alters no validator
// set: nothing in the node reads governance state. Execute used to flip a
// passed proposal to "Executed" while enacting nothing, which is the kind of
// status a client would trust. So: only text (signalling) proposals are
// accepted, Passed is their terminal status, and Execute refuses. When a
// proposal type gains a real, deterministic effect it gets its own fork rule
// and its own routing.
const Advisory = true

// Voting power is the validator bond. Nothing ever called SetVotingPower, so
// no address had any power and no proposal could be submitted; the registry is
// the one deterministic, on-chain measure of stake this chain has. A validator's
// weight is its bond while it is Active; everyone else weighs zero.
func (g *GovernanceContract) votingPowerOf(addr qcrypto.Address) *big.Int {
	reg := NewStakingContract(g.stateDB, DeployStaking(nil))
	rec, err := reg.GetValidator(addr)
	if err != nil || rec == nil || rec.Status != ValidatorActive || rec.Bond == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(rec.Bond)
}

// activeVotingPower is the sum of every Active validator's bond.
func (g *GovernanceContract) activeVotingPower() (*big.Int, error) {
	reg := NewStakingContract(g.stateDB, DeployStaking(nil))
	active, err := reg.ListActive()
	if err != nil {
		return nil, fmt.Errorf("governance: validator set: %w", err)
	}
	total := new(big.Int)
	for _, rec := range active {
		if rec.Bond != nil {
			total.Add(total, rec.Bond)
		}
	}
	return total, nil
}

// Submit creates a new governance proposal.
// Returns the new proposal ID.
func (g *GovernanceContract) Submit(
	proposer qcrypto.Address,
	proposalType ProposalType,
	title, description, payload string,
	currentHeight uint64,
) (uint64, error) {
	if title == "" {
		return 0, fmt.Errorf("governance: proposal title cannot be empty")
	}
	if len(title) > 256 {
		return 0, fmt.Errorf("governance: proposal title too long (max 256 chars)")
	}
	if len(description) > 10_000 {
		return 0, fmt.Errorf("governance: description too long (max 10,000 chars)")
	}
	if proposalType != ProposalTypeText {
		return 0, fmt.Errorf("governance: proposal type %d has no on-chain execution in this build; only text (signalling) proposals are accepted", proposalType)
	}
	if len(payload) > MaxModuleURIBytes {
		return 0, fmt.Errorf("governance: payload is %d bytes, above the cap of %d", len(payload), MaxModuleURIBytes)
	}

	// Check proposer has voting power
	if power := g.votingPowerOf(proposer); power.Sign() == 0 {
		return 0, fmt.Errorf("governance: proposer %s is not an active validator and has no voting power", proposer)
	}

	// Assign ID
	count, _ := g.ProposalCount()
	newID := count + 1

	p := &Proposal{
		ID:          newID,
		Type:        proposalType,
		Status:      ProposalPending,
		Proposer:    proposer,
		Title:       title,
		Description: description,
		Payload:     payload,

		SubmitHeight:       currentHeight,
		VotingStartHeight:  currentHeight + DiscussionBlocks,
		VotingEndHeight:    currentHeight + DiscussionBlocks + VotingBlocks,
		ExecuteAfterHeight: currentHeight + DiscussionBlocks + VotingBlocks + ExecutionDelayBlocks,

		YesVotes:     big.NewInt(0),
		NoVotes:      big.NewInt(0),
		AbstainVotes: big.NewInt(0),
		VetoVotes:    big.NewInt(0),
		TotalVotes:   big.NewInt(0),
	}

	if err := g.setProposal(p); err != nil {
		return 0, err
	}
	g.setBigInt(slotGovProposalCount, big.NewInt(int64(newID)))

	return newID, nil
}

// Vote casts a vote on an active proposal.
func (g *GovernanceContract) Vote(
	voter qcrypto.Address,
	proposalID uint64,
	option VoteOption,
	currentHeight uint64,
) error {
	p, err := g.GetProposal(proposalID)
	if err != nil || p == nil {
		return fmt.Errorf("governance: proposal %d not found", proposalID)
	}

	// Check proposal is in voting period
	if currentHeight < p.VotingStartHeight {
		return fmt.Errorf("governance: proposal %d not yet in voting period (starts at block %d)",
			proposalID, p.VotingStartHeight)
	}
	if currentHeight > p.VotingEndHeight {
		return fmt.Errorf("governance: proposal %d voting has ended (ended at block %d)",
			proposalID, p.VotingEndHeight)
	}

	if p.Status == ProposalCanceled {
		return fmt.Errorf("governance: proposal %d was cancelled", proposalID)
	}
	if option < VoteYes || option > VoteNoWithVeto {
		return fmt.Errorf("governance: invalid vote option %d", option)
	}

	// Update status if needed
	if p.Status == ProposalPending {
		p.Status = ProposalActive
	}

	// Check voter has power
	power := g.votingPowerOf(voter)
	if power.Sign() == 0 {
		return fmt.Errorf("governance: voter %s is not an active validator and has no voting power", voter)
	}

	// Check not already voted. A re-vote removes the OLD weight, which is
	// recorded with the vote: the voter's bond may have changed since.
	existingVote, err := g.getVote(proposalID, voter)
	if err != nil {
		return err
	}
	if existingVote != 0 {
		oldPower, _ := g.getBigInt(govVoteWeightSlot(proposalID, voter))
		g.removeVoteFromTally(p, VoteOption(existingVote), oldPower)
	}
	if err := g.setBigInt(govVoteWeightSlot(proposalID, voter), power); err != nil {
		return err
	}

	// Record vote
	if err := g.setVote(proposalID, voter, option); err != nil {
		return err
	}

	// Add to tally
	g.addVoteToTally(p, option, power)

	return g.setProposal(p)
}

// TallyNow closes voting against the active validator set's total bond.
func (g *GovernanceContract) TallyNow(proposalID uint64, currentHeight uint64) error {
	total, err := g.activeVotingPower()
	if err != nil {
		return err
	}
	return g.Tally(proposalID, currentHeight, total)
}

// Tally closes voting and determines the result. Call after VotingEndHeight.
func (g *GovernanceContract) Tally(proposalID uint64, currentHeight uint64, totalVotingPower *big.Int) error {
	p, err := g.GetProposal(proposalID)
	if err != nil || p == nil {
		return fmt.Errorf("governance: proposal %d not found", proposalID)
	}
	if currentHeight <= p.VotingEndHeight {
		return fmt.Errorf("governance: voting period for proposal %d has not ended yet", proposalID)
	}
	if p.Status == ProposalCanceled {
		return fmt.Errorf("governance: proposal %d was cancelled", proposalID)
	}
	if p.Status != ProposalActive && p.Status != ProposalPending {
		return fmt.Errorf("governance: proposal %d already tallied (%s)", proposalID, ProposalStatusName(p.Status))
	}

	// Check quorum: total votes ≥ 4% of total voting power
	if totalVotingPower.Sign() > 0 {
		// quorum = (TotalVotes * 10000) / totalVotingPower >= QuorumBasisPoints
		quotient := new(big.Int).Mul(p.TotalVotes, big.NewInt(10_000))
		quotient.Div(quotient, totalVotingPower)
		if quotient.Int64() < int64(QuorumBasisPoints) {
			p.Status = ProposalRejected
			return g.setProposal(p)
		}
	}

	// Check veto: if >1/3 of votes are NoWithVeto, reject
	if p.TotalVotes.Sign() > 0 {
		vetoQuotient := new(big.Int).Mul(p.VetoVotes, big.NewInt(10_000))
		vetoQuotient.Div(vetoQuotient, p.TotalVotes)
		if vetoQuotient.Int64() > 3_333 { // > 33.33%
			p.Status = ProposalRejected
			return g.setProposal(p)
		}
	}

	// Check supermajority: Yes / (Yes + No + Veto) ≥ 66.67%
	denominator := new(big.Int).Add(p.YesVotes, p.NoVotes)
	denominator.Add(denominator, p.VetoVotes)
	if denominator.Sign() == 0 {
		p.Status = ProposalRejected
		return g.setProposal(p)
	}
	yesQuotient := new(big.Int).Mul(p.YesVotes, big.NewInt(10_000))
	yesQuotient.Div(yesQuotient, denominator)
	if yesQuotient.Int64() >= int64(PassBasisPoints) {
		p.Status = ProposalPassed
	} else {
		p.Status = ProposalRejected
	}

	return g.setProposal(p)
}

// Execute refuses. It used to set ProposalExecuted on a passed proposal while
// enacting nothing — a status that says "done" about an action that never
// happened. Passed is the terminal status of a signalling proposal (see
// Advisory); no proposal type in this build has an on-chain effect. The
// function stays so a future type with a real effect has a place to land.
func (g *GovernanceContract) Execute(proposalID uint64, currentHeight uint64) error {
	p, err := g.GetProposal(proposalID)
	if err != nil || p == nil {
		return fmt.Errorf("governance: proposal %d not found", proposalID)
	}
	return fmt.Errorf("governance: proposal %d (%s) is advisory: nothing executes on-chain in this build, Passed is its final status",
		proposalID, ProposalStatusName(p.Status))
}

// Cancel cancels a pending proposal (proposer only, before voting starts).
func (g *GovernanceContract) Cancel(caller qcrypto.Address, proposalID uint64, currentHeight uint64) error {
	p, err := g.GetProposal(proposalID)
	if err != nil || p == nil {
		return fmt.Errorf("governance: proposal %d not found", proposalID)
	}
	if caller != p.Proposer {
		return fmt.Errorf("governance: only the proposer can cancel a proposal")
	}
	if p.Status != ProposalPending {
		return fmt.Errorf("governance: can only cancel pending proposals (voting may have started)")
	}

	p.Status = ProposalCanceled
	return g.setProposal(p)
}

// ── Voting power management ───────────────────────────────────────────────────

// SetVotingPower updates the voting power of an address.
// Called by the staking contract when stake changes.
func (g *GovernanceContract) SetVotingPower(addr qcrypto.Address, power *big.Int) error {
	oldPower, _ := g.GetVotingPower(addr)

	if err := g.setBigInt(govVoterPowerSlot(addr), power); err != nil {
		return err
	}

	// Update total voting power
	total, _ := g.TotalVotingPower()
	newTotal := new(big.Int).Sub(total, oldPower)
	newTotal.Add(newTotal, power)
	if newTotal.Sign() < 0 {
		newTotal = big.NewInt(0)
	}
	return g.setBigInt(slotGovTotalPower, newTotal)
}

// GetVotingPower returns the voting power for an address.
func (g *GovernanceContract) GetVotingPower(addr qcrypto.Address) (*big.Int, error) {
	return g.getBigInt(govVoterPowerSlot(addr))
}

// TotalVotingPower returns the total voting power in the system.
func (g *GovernanceContract) TotalVotingPower() (*big.Int, error) {
	return g.getBigInt(slotGovTotalPower)
}

// ── Queries ────────────────────────────────────────────────────────────────────

// GetProposal returns a proposal by ID.
func (g *GovernanceContract) GetProposal(id uint64) (*Proposal, error) {
	b, err := g.stateDB.GetStorageValue(g.contract, govProposalSlot(id))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var p Proposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.YesVotes == nil { p.YesVotes = big.NewInt(0) }
	if p.NoVotes == nil { p.NoVotes = big.NewInt(0) }
	if p.AbstainVotes == nil { p.AbstainVotes = big.NewInt(0) }
	if p.VetoVotes == nil { p.VetoVotes = big.NewInt(0) }
	if p.TotalVotes == nil { p.TotalVotes = big.NewInt(0) }
	return &p, nil
}

// ProposalCount returns the total number of proposals ever submitted.
func (g *GovernanceContract) ProposalCount() (uint64, error) {
	v, err := g.getBigInt(slotGovProposalCount)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// GetVoteOf returns how a specific voter voted on a proposal.
// Returns 0 if not voted.
func (g *GovernanceContract) GetVoteOf(proposalID uint64, voter qcrypto.Address) (VoteOption, error) {
	v, err := g.getVote(proposalID, voter)
	return VoteOption(v), err
}

// ProposalStatusName returns a human-readable status string.
func ProposalStatusName(s ProposalStatus) string {
	switch s {
	case ProposalPending:  return "pending"
	case ProposalActive:   return "active"
	case ProposalPassed:   return "passed"
	case ProposalRejected: return "rejected"
	case ProposalExecuted: return "executed"
	case ProposalExpired:  return "expired"
	case ProposalCanceled: return "canceled"
	default:               return "unknown"
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func (g *GovernanceContract) setProposal(p *Proposal) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return g.stateDB.SetStorageValue(g.contract, govProposalSlot(p.ID), data)
}

func (g *GovernanceContract) setVote(proposalID uint64, voter qcrypto.Address, option VoteOption) error {
	return g.stateDB.SetStorageValue(g.contract, govVoteSlot(proposalID, voter), []byte{byte(option)})
}

func (g *GovernanceContract) getVote(proposalID uint64, voter qcrypto.Address) (byte, error) {
	b, err := g.stateDB.GetStorageValue(g.contract, govVoteSlot(proposalID, voter))
	if err != nil || len(b) == 0 {
		return 0, nil
	}
	return b[0], nil
}

func (g *GovernanceContract) addVoteToTally(p *Proposal, option VoteOption, power *big.Int) {
	p.TotalVotes.Add(p.TotalVotes, power)
	switch option {
	case VoteYes:
		p.YesVotes.Add(p.YesVotes, power)
	case VoteNo:
		p.NoVotes.Add(p.NoVotes, power)
	case VoteAbstain:
		p.AbstainVotes.Add(p.AbstainVotes, power)
	case VoteNoWithVeto:
		p.VetoVotes.Add(p.VetoVotes, power)
		p.NoVotes.Add(p.NoVotes, power) // also counted as No
	}
}

func (g *GovernanceContract) removeVoteFromTally(p *Proposal, option VoteOption, power *big.Int) {
	sub := func(a, b *big.Int) {
		a.Sub(a, b)
		if a.Sign() < 0 { a.SetInt64(0) }
	}
	sub(p.TotalVotes, power)
	switch option {
	case VoteYes:
		sub(p.YesVotes, power)
	case VoteNo:
		sub(p.NoVotes, power)
	case VoteAbstain:
		sub(p.AbstainVotes, power)
	case VoteNoWithVeto:
		sub(p.VetoVotes, power)
		sub(p.NoVotes, power)
	}
}

func (g *GovernanceContract) setBigInt(slot [32]byte, v *big.Int) error {
	return g.stateDB.SetStorageValue(g.contract, slot, v.Bytes())
}

func (g *GovernanceContract) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := g.stateDB.GetStorageValue(g.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}
