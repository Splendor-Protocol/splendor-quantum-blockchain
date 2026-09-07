// Package rpc — Governance and Staking HTTP API endpoints.
//
// These endpoints expose the on-chain governance and staking contracts
// through the node's RPC layer, making them accessible to wallets, explorers,
// and governance frontends without needing to interact with raw contract ABI.
//
// Governance endpoints:
//   GET  /gov/proposals                     — list proposals (paginated)
//   GET  /gov/proposals/{id}                — get a single proposal
//   GET  /gov/proposals/{id}/votes/{addr}   — get a voter's vote
//   POST /gov/proposals/{id}/tally          — manually trigger tally (node only)
//
// Validator endpoints:
//   GET  /validators                        — list active validators
//   GET  /validators/{addr}                 — one validator record
//   GET  /staking/validators/{addr}         — alias of /validators/{addr}
//   GET  /staking/total-staked              — total bonded SPLD
//   GET  /staking/count                     — active validator count
//
// All responses: JSON. All addresses: SQ: format.
package rpc

import (
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterGovRoutes adds governance and staking endpoints to the router.
func RegisterGovRoutes(r *mux.Router, stateDB *state.StateDB) {
	govAddr := vm.DeployGovernance(stateDB)
	stakingAddr := vm.DeployStaking(stateDB)

	gov := vm.NewGovernanceContract(stateDB, govAddr)
	staking := vm.NewStakingContract(stateDB, stakingAddr)

	// Governance
	r.HandleFunc("/gov/proposals", makeListProposalsHandler(gov)).Methods(http.MethodGet)
	r.HandleFunc("/gov/proposals/{id}", makeGetProposalHandler(gov)).Methods(http.MethodGet)
	r.HandleFunc("/gov/proposals/{id}/votes/{addr}", makeGetVoteHandler(gov)).Methods(http.MethodGet)
	r.HandleFunc("/gov/power/{addr}", makeVotingPowerHandler(gov)).Methods(http.MethodGet)
	r.HandleFunc("/gov/total-power", makeTotalPowerHandler(gov)).Methods(http.MethodGet)

	// Staking
	r.HandleFunc("/validators", makeListValidatorsHandler(staking)).Methods(http.MethodGet)
	r.HandleFunc("/validators/{addr}", makeGetValidatorHandler(staking)).Methods(http.MethodGet)
	r.HandleFunc("/staking/validators/{addr}", makeGetValidatorHandler(staking)).Methods(http.MethodGet)
	r.HandleFunc("/staking/total-staked", makeTotalStakedHandler(staking)).Methods(http.MethodGet)
	r.HandleFunc("/staking/count", makeValidatorCountHandler(staking)).Methods(http.MethodGet)

	log.Debug().
		Str("gov_addr", govAddr.String()).
		Str("staking_addr", stakingAddr.String()).
		Msg("governance + staking RPC routes registered")
}

// ── Governance handlers ────────────────────────────────────────────────────────

func makeListProposalsHandler(gov *vm.GovernanceContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := gov.ProposalCount()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get proposal count: %v", err))
			return
		}

		// Pagination
		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")
		limit := uint64(20)
		offset := uint64(0)
		if l, err := strconv.ParseUint(limitStr, 10, 64); err == nil && l > 0 && l <= 100 {
			limit = l
		}
		if o, err := strconv.ParseUint(offsetStr, 10, 64); err == nil {
			offset = o
		}

		// Collect proposals (newest first)
		type proposalSummary struct {
			ID          uint64 `json:"id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Proposer    string `json:"proposer"`
			VotingStart uint64 `json:"voting_start_height"`
			VotingEnd   uint64 `json:"voting_end_height"`
			YesVotes    string `json:"yes_votes"`
			NoVotes     string `json:"no_votes"`
			TotalVotes  string `json:"total_votes"`
		}

		var proposals []proposalSummary
		start := count
		if offset < count {
			start = count - offset
		}

		fetched := uint64(0)
		for id := start; id >= 1 && fetched < limit; id-- {
			p, err := gov.GetProposal(id)
			if err != nil || p == nil {
				continue
			}
			proposals = append(proposals, proposalSummary{
				ID:          p.ID,
				Title:       p.Title,
				Status:      vm.ProposalStatusName(p.Status),
				Proposer:    p.Proposer.String(),
				VotingStart: p.VotingStartHeight,
				VotingEnd:   p.VotingEndHeight,
				YesVotes:    p.YesVotes.String(),
				NoVotes:     p.NoVotes.String(),
				TotalVotes:  p.TotalVotes.String(),
			})
			fetched++
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"proposals": proposals,
			"total":     count,
			"offset":    offset,
			"limit":     limit,
		})
	}
}

func makeGetProposalHandler(gov *vm.GovernanceContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := mux.Vars(r)["id"]
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid proposal ID")
			return
		}

		p, err := gov.GetProposal(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if p == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("proposal %d not found", id))
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":                   p.ID,
			"type":                 p.Type,
			"status":               vm.ProposalStatusName(p.Status),
			"proposer":             p.Proposer.String(),
			"title":                p.Title,
			"description":          p.Description,
			"payload":              p.Payload,
			"submit_height":        p.SubmitHeight,
			"voting_start_height":  p.VotingStartHeight,
			"voting_end_height":    p.VotingEndHeight,
			"execute_after_height": p.ExecuteAfterHeight,
			"yes_votes":            p.YesVotes.String(),
			"no_votes":             p.NoVotes.String(),
			"abstain_votes":        p.AbstainVotes.String(),
			"veto_votes":           p.VetoVotes.String(),
			"total_votes":          p.TotalVotes.String(),
		})
	}
}

func makeGetVoteHandler(gov *vm.GovernanceContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseUint(vars["id"], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid proposal ID")
			return
		}
		addr, err := qcrypto.AddressFromString(vars["addr"])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		vote, err := gov.GetVoteOf(id, addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		voteNames := map[vm.VoteOption]string{
			0: "not_voted",
			1: "yes",
			2: "no",
			3: "abstain",
			4: "no_with_veto",
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"proposal_id": id,
			"voter":       addr.String(),
			"vote":        vote,
			"vote_name":   voteNames[vote],
		})
	}
}

func makeVotingPowerHandler(gov *vm.GovernanceContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["addr"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		power, err := gov.GetVotingPower(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"address":      addrStr,
			"voting_power": power.String(),
		})
	}
}

func makeTotalPowerHandler(gov *vm.GovernanceContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		total, err := gov.TotalVotingPower()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"total_voting_power": total.String(),
		})
	}
}

// ── Staking handlers ───────────────────────────────────────────────────────────

func makeGetValidatorHandler(staking *vm.StakingContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["addr"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		rec, err := staking.GetValidator(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rec == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("validator %s not found", addrStr))
			return
		}

		statusNames := map[vm.ValidatorStatus]string{
			vm.ValidatorInactive: "inactive",
			vm.ValidatorActive:   "active",
			vm.ValidatorExiting:  "exiting",
		}
		bond := rec.Bond
		if bond == nil {
			bond = big.NewInt(0)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"address":       rec.Address.String(),
			"bond":          bond.String(),
			"bond_spld":     vm.ValidatorBondSPLD,
			"status":        statusNames[rec.Status],
			"joined_height": rec.JoinedHeight,
			"exit_height":   rec.ExitHeight,
		})
	}
}

func makeTotalStakedHandler(staking *vm.StakingContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		total, err := staking.TotalStaked()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"total_staked": total.String(),
		})
	}
}

func makeValidatorCountHandler(staking *vm.StakingContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := staking.ValidatorCount()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]uint64{
			"validator_count": count,
		})
	}
}

func makeListValidatorsHandler(staking *vm.StakingContract) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := staking.ListActive()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]map[string]interface{}, 0, len(list))
		for _, rec := range list {
			bond := rec.Bond
			if bond == nil {
				bond = big.NewInt(0)
			}
			out = append(out, map[string]interface{}{
				"address":       rec.Address.String(),
				"bond":          bond.String(),
				"status":        "active",
				"joined_height": rec.JoinedHeight,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"validators": out,
			"count":      len(out),
			"bond_spld":  vm.ValidatorBondSPLD,
		})
	}
}
