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

func (s *Server) handleVerifyCheck(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var ins types.VerifyInstruction
	if err := decodeWireJSON(r.Body, &ins); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid instruction: %v", err))
		return
	}
	digest, addr, err := types.CheckVerifyInstruction(types.ChainID, &ins)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false, "reason": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":           true,
		"digest":          hex.EncodeToString(digest[:]),
		"signer":          addr.String(),
		"instruction_id":  types.InstructionIDHex(ins.InstructionID),
		"origin":          ins.Origin,
		"settlement_hint": ins.SettlementHint,
		"action":          ins.SettlementAction,
	})
}

func (s *Server) handleVerifyProof(w http.ResponseWriter, r *http.Request) {
	idHex := mux.Vars(r)["id"]
	idBytes, err := hex.DecodeString(idHex)
	if err != nil || len(idBytes) != 32 {
		writeError(w, http.StatusBadRequest, "instruction id must be 32-byte hex")
		return
	}
	instStr := r.URL.Query().Get("instance")
	if instStr == "" {
		instStr = r.URL.Query().Get("verify")
	}
	if instStr == "" {
		writeError(w, http.StatusBadRequest, "query instance= (verify module address) is required")
		return
	}
	params, _ := json.Marshal(map[string]string{
		"verify": instStr, "instruction_id": idHex,
	})
	env, _ := json.Marshal(vm.ModuleCall{Module: "verify", Action: "proof", Params: params})
	out, err := vm.DispatchModuleReadCall(s.stateDB, vm.ReadContext{}, env)
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
		writeError(w, http.StatusBadRequest, "signed tx required (X402-style: admit a TxCall)")
		return
	}
	if tx.Type != types.TxCall || tx.To != vm.ModuleRouterAddress {
		writeError(w, http.StatusBadRequest, "tx must be a module TxCall")
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
