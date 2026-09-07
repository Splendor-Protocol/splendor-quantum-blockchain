// Package rpc — migration bridge HTTP read endpoints.
//
// Each GET route is the mirror of one bridge read action on POST /call and is
// served through the same read dispatch (vm.DispatchModuleReadCall), so the
// two can never disagree: the handler builds the envelope the /call caller
// would have sent and returns the decoded value unchanged.
//
// Endpoints:
//
//	GET /bridge/{addr}            — bridge_info
//	GET /bridge/{addr}/claim/{id} — claim_status
//
// Not fork-gated. Nothing here writes state (rpc/route_registration_purity_test.go).
package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterBridgeRoutes adds the migration bridge read endpoints.
func RegisterBridgeRoutes(r *mux.Router, stateDB *state.StateDB) {
	r.HandleFunc("/bridge/{addr}", makeBridgeReadHandler(stateDB, "bridge_info", noBridgeVars)).Methods(http.MethodGet)
	r.HandleFunc("/bridge/{addr}/claim/{id}", makeBridgeReadHandler(stateDB, "claim_status", bridgeClaimVars)).Methods(http.MethodGet)

	log.Debug().Msg("bridge RPC routes registered")
}

// bridgeVars turns the URL variables of one route into the params object of
// its read action; the instance address is added by the handler.
type bridgeVars func(vars map[string]string) (map[string]interface{}, error)

func noBridgeVars(map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func bridgeClaimVars(vars map[string]string) (map[string]interface{}, error) {
	id, err := strconv.ParseUint(vars["id"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid burn id %q: want a decimal integer", vars["id"])
	}
	return map[string]interface{}{"id": id}, nil
}

// makeBridgeReadHandler serves one read action: the URL variables become the
// params, the address becomes `bridge`, and the envelope goes through the
// read dispatch exactly as a /call would.
func makeBridgeReadHandler(stateDB *state.StateDB, action string, vars bridgeVars) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := mux.Vars(r)
		params, err := vars(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params["bridge"] = v["addr"]
		praw, err := json.Marshal(params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data, err := json.Marshal(vm.ModuleCall{Module: "bridge", Action: action, Params: praw})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		decoded, err := vm.DispatchModuleReadCall(stateDB, vm.ReadContext{}, data)
		if err != nil {
			// The same text /call returns for the same envelope.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, decoded)
	}
}
