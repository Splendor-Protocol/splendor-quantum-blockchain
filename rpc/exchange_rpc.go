// Package rpc — exchange (settlement module) HTTP read endpoints.
//
// Each GET route is the mirror of one exchange read action on POST /call and
// is served through the same read dispatch (vm.DispatchModuleReadCall), so the
// two can never disagree: the handler builds the envelope the /call caller
// would have sent and returns the decoded value unchanged. The body-carrying
// reads (order_hash, order_remaining, simulate_match) are /call-only.
//
// Endpoints:
//
//	GET /exchange/{addr}                                  — exchange_info
//	GET /exchange/{addr}/condition/{id}                   — condition_info
//	GET /exchange/{addr}/order/{hash}                     — order_status
//	GET /exchange/{addr}/nonce/{maker}                    — nonce
//	GET /exchange/{addr}/position/{condition}/{holder}    — position
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

// RegisterExchangeRoutes adds the exchange read endpoints. head supplies the
// advisory chain head a read may label its answer with (zero when there is
// none — nil is accepted and reads as zero); see HeadFunc for the production
// closure.
func RegisterExchangeRoutes(r *mux.Router, stateDB *state.StateDB, head func() vm.ReadContext) {
	if head == nil {
		head = func() vm.ReadContext { return vm.ReadContext{} }
	}
	r.HandleFunc("/exchange/{addr}", makeExchangeReadHandler(stateDB, head, "exchange_info", noExchangeVars)).Methods(http.MethodGet)
	r.HandleFunc("/exchange/{addr}/condition/{id}", makeExchangeReadHandler(stateDB, head, "condition_info", exchangeConditionVars)).Methods(http.MethodGet)
	r.HandleFunc("/exchange/{addr}/order/{hash}", makeExchangeReadHandler(stateDB, head, "order_status", exchangeOrderVars)).Methods(http.MethodGet)
	r.HandleFunc("/exchange/{addr}/nonce/{maker}", makeExchangeReadHandler(stateDB, head, "nonce", exchangeNonceVars)).Methods(http.MethodGet)
	r.HandleFunc("/exchange/{addr}/position/{condition}/{holder}", makeExchangeReadHandler(stateDB, head, "position", exchangePositionVars)).Methods(http.MethodGet)

	log.Debug().Msg("exchange RPC routes registered")
}

// exchangeVars turns the URL variables of one route into the params object of
// its read action; the instance address is added by the handler.
type exchangeVars func(vars map[string]string) (map[string]interface{}, error)

func noExchangeVars(map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func exchangeConditionVars(vars map[string]string) (map[string]interface{}, error) {
	id, err := parseExchangeConditionID(vars["id"])
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"condition": id}, nil
}

func exchangeOrderVars(vars map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{"hash": vars["hash"]}, nil
}

func exchangeNonceVars(vars map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{"maker": vars["maker"]}, nil
}

func exchangePositionVars(vars map[string]string) (map[string]interface{}, error) {
	id, err := parseExchangeConditionID(vars["condition"])
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"condition": id, "holder": vars["holder"]}, nil
}

// parseExchangeConditionID decodes a condition id path segment: a plain
// decimal uint64 (ids run 1..condition_count; the read reports a missing one).
func parseExchangeConditionID(s string) (uint64, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid condition id %q: want a decimal integer", s)
	}
	return id, nil
}

// makeExchangeReadHandler serves one read action: the URL variables become
// the params, the address becomes `exchange`, and the envelope goes through
// the read dispatch exactly as a /call would.
func makeExchangeReadHandler(stateDB *state.StateDB, head func() vm.ReadContext, action string, vars exchangeVars) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := mux.Vars(r)
		params, err := vars(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params["exchange"] = v["addr"]
		praw, err := json.Marshal(params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data, err := json.Marshal(vm.ModuleCall{Module: "exchange", Action: action, Params: praw})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		decoded, err := vm.DispatchModuleReadCall(stateDB, head(), data)
		if err != nil {
			// The same text /call returns for the same envelope.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, decoded)
	}
}
