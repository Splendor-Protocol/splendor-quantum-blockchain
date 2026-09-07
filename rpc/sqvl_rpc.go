package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// SQVL product doors (docs/modules/verify.md). They add nothing the module
// router does not already do: /verify/check is a stateless dual-signature
// check, /verify/proof is the `verify.proof` read by URL, and /verify/attest
// and /verify/settle admit an already-signed module TxCall after checking it
// really is a verify.attest / verify.spend|finalize — the same shape as the
// x402 doors, for integrators that speak one endpoint per step.

// maxVerifyCheckBody bounds POST /verify/check: an instruction carries two
// public keys and two signatures (~18 KB base64) plus a 4 KiB payload cap.
const maxVerifyCheckBody = 256 * 1024

func (s *Server) handleVerifyCheck(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVerifyCheckBody)
	var ins types.VerifyInstruction
	if err := decodeWireJSON(r.Body, &ins); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid instruction: %v", err))
		return
	}
	digest, signer, err := types.CheckVerifyInstruction(types.ChainID, &ins)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false, "reason": err.Error(),
		})
		return
	}
	action := ins.SettlementAction
	if action == "" {
		action = types.SettleFinalizeOnly
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":             true,
		"chain_id":          types.ChainID,
		"digest":            hex.EncodeToString(digest[:]),
		"signer":            signer.String(),
		"instruction_id":    ins.InstructionID.Hex(),
		"purpose":           ins.Purpose,
		"origin":            ins.Origin,
		"settlement_hint":   ins.SettlementHint,
		"settlement_action": action,
	})
}

// GET /verify/proof/{id}?instance=SQ:… — the verify.proof read, by URL.
func (s *Server) handleVerifyProof(w http.ResponseWriter, r *http.Request) {
	id, err := types.ParseInstructionID(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	instance := r.URL.Query().Get("instance")
	if instance == "" {
		instance = r.URL.Query().Get("verify")
	}
	if instance == "" {
		writeError(w, http.StatusBadRequest, "query parameter instance= (the verify instance address) is required")
		return
	}
	params, _ := json.Marshal(map[string]string{"verify": instance, "instruction_id": id.Hex()})
	env, _ := json.Marshal(vm.ModuleCall{Module: "verify", Action: "proof", Params: params})
	out, err := vm.DispatchModuleReadCall(s.stateDB, headReadContext(s.executor), env)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleVerifyAttest(w http.ResponseWriter, r *http.Request) {
	s.handleVerifyModuleTx(w, r, "attest")
}

func (s *Server) handleVerifySettle(w http.ResponseWriter, r *http.Request) {
	s.handleVerifyModuleTx(w, r, "spend")
}

// handleVerifyModuleTx admits a signed verify-module TxCall, given either
// bare or wrapped as {"tx": …}, after checking that it is the action this
// door is for. Admission itself is admitTx — the same path as /tx/submit.
func (s *Server) handleVerifyModuleTx(w http.ResponseWriter, r *http.Request, wantAction string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	body, err = normalizeWireAmounts(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var wrap struct {
		Tx *types.Transaction `json:"tx"`
		*types.Transaction
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	tx := wrap.Tx
	if tx == nil {
		tx = wrap.Transaction
	}
	if tx == nil {
		writeError(w, http.StatusBadRequest, "signed tx required: a verify-module TxCall, bare or as {\"tx\": …}")
		return
	}
	if tx.Type != types.TxCall || tx.To != vm.ModuleRouterAddress {
		writeError(w, http.StatusBadRequest, "tx must be a module TxCall to the module router")
		return
	}
	var mc vm.ModuleCall
	if err := json.Unmarshal(tx.Data, &mc); err != nil || mc.Module != "verify" {
		writeError(w, http.StatusBadRequest, "tx must call the verify module")
		return
	}
	if wantAction == "spend" {
		if mc.Action != "spend" && mc.Action != "finalize" {
			writeError(w, http.StatusBadRequest, "settle tx action must be spend or finalize")
			return
		}
	} else if mc.Action != wantAction {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("tx action must be %s", wantAction))
		return
	}
	s.admitTx(w, tx)
}
