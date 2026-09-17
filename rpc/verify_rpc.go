// Package rpc — QSol source verification.
//
//	POST /contract/verify — does this source compile to the code deployed at
//	                        this address?
//
// The proof is byte equality. compiler.Compile is a pure function of its input
// (no clock, no map-iteration order — see TestCompileDeterministic), so the
// same source always produces the same bytecode. If a recompile reproduces the
// deployed runtime code exactly, the source IS what is running; if it does
// not, it is not. Nothing is trusted about the submitter.
//
// The endpoint is read-only: it compiles in memory and reads one account's
// code. It never writes state and is not consensus code.
package rpc

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/compiler"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// maxVerifySourceBytes bounds the source an anonymous caller can make this node
// compile. The rate limiter caps how OFTEN; this caps how MUCH per request, so
// the work per call is bounded before any of it is spent. Far above any real
// contract in this subset, which has no imports to pull in.
const maxVerifySourceBytes = 128 * 1024

// RegisterVerifyRoutes mounts source verification.
//
// guard may be nil in tests; in production pass Server.SimGuard(). Compiling
// arbitrary source is CPU spent for an anonymous caller, so this belongs on the
// metered plane with /call and /estimate rather than the free read plane.
func RegisterVerifyRoutes(r *mux.Router, stateDB *state.StateDB, guard *ExecGuard) {
	r.Handle("/contract/verify", guard.Wrap(makeVerifyHandler(stateDB))).Methods(http.MethodPost)
	log.Debug().Msg("contract source-verification route registered (/contract/verify)")
}

type verifyRequest struct {
	Address string `json:"address"`
	Source  string `json:"source"`
}

type verifyFunction struct {
	Signature string `json:"signature"`
	// Selector is bare hex with NO 0x prefix, matching the rest of this API
	// (code hashes, topics). Callers add their own prefix for display.
	Selector string `json:"selector"`
}

type verifyEvent struct {
	Signature string `json:"signature"`
	Topic0    string `json:"topic0"` // bare hex, no 0x prefix
}

// verifyResponse always carries Kind, so a caller distinguishes the outcomes
// WITHOUT inspecting the HTTP status. Every well-formed question about a
// contract answers 200 — including "it did not compile" and "that is not the
// source" — because the request itself was fine. 400 means the request was
// malformed (bad address, oversize source), which is not a verdict.
type verifyResponse struct {
	Verified bool `json:"verified"`
	// Kind is one of: verified, mismatch, compile_error, not_a_contract,
	// native_module, bad_request.
	Kind    string `json:"kind"`
	Address string `json:"address,omitempty"`
	// Reason carries the compiler's message verbatim for compile_error, and a
	// short explanation for the other non-verified kinds.
	Reason string `json:"reason,omitempty"`

	CodeHash     string `json:"code_hash,omitempty"`
	BytecodeSize int    `json:"bytecode_size,omitempty"`
	// OnchainSize and CompiledSize are set on a mismatch so the caller can see
	// how far apart the two are without fetching the code itself.
	OnchainSize  int `json:"onchain_size,omitempty"`
	CompiledSize int `json:"compiled_size,omitempty"`

	Functions []verifyFunction `json:"functions,omitempty"`
	Events    []verifyEvent    `json:"events,omitempty"`
}

func makeVerifyHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req verifyRequest
		if err := readJSON(r, &req); err != nil {
			writeVerifyRefusal(w, "bad_request", fmt.Sprintf("invalid request: %v", err))
			return
		}
		// Bound the work BEFORE compiling any of it.
		if len(req.Source) == 0 {
			writeVerifyRefusal(w, "bad_request", "source is empty")
			return
		}
		if len(req.Source) > maxVerifySourceBytes {
			writeVerifyRefusal(w, "bad_request",
				fmt.Sprintf("source is %d bytes, above the %d-byte limit", len(req.Source), maxVerifySourceBytes))
			return
		}
		addr, err := qcrypto.AddressFromString(req.Address)
		if err != nil {
			writeVerifyRefusal(w, "bad_request", fmt.Sprintf("invalid address: %v", err))
			return
		}

		acc, err := stateDB.GetAccount(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Two addresses have no bytecode to compare against, and calling either
		// "not verified" would be misleading: they are different questions.
		if !acc.IsContract {
			writeVerifyVerdict(w, verifyResponse{
				Kind: "not_a_contract", Address: req.Address,
				Reason: "no contract is deployed at this address",
			})
			return
		}
		if acc.CodeHash == ([32]byte{}) {
			writeVerifyVerdict(w, verifyResponse{
				Kind: "native_module", Address: req.Address,
				Reason: "this is a native-module contract: its behaviour is compiled into the node, so there is no deployed bytecode to match a source against",
			})
			return
		}

		onchain, err := stateDB.GetCode(acc.CodeHash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("load code: %v", err))
			return
		}

		c, cerr := compiler.CompileContext(r.Context(), req.Source)
		if cerr != nil && r.Context().Err() != nil {
			writeError(w, http.StatusRequestTimeout, "verification cancelled")
			return
		}
		if cerr != nil {
			// The submitter's own mistake, and the compiler's message is the
			// only useful thing to say about it — passed through verbatim.
			writeVerifyVerdict(w, verifyResponse{
				Kind: "compile_error", Address: req.Address, Reason: cerr.Error(),
			})
			return
		}
		if !bytes.Equal(onchain, c.Bytecode) {
			writeVerifyVerdict(w, verifyResponse{
				Kind: "mismatch", Address: req.Address,
				Reason:       "this source compiles to different bytecode than the contract deployed at this address",
				OnchainSize:  len(onchain),
				CompiledSize: len(c.Bytecode),
				CodeHash:     hex.EncodeToString(acc.CodeHash[:]),
			})
			return
		}

		out := verifyResponse{
			Verified: true, Kind: "verified", Address: req.Address,
			CodeHash:     hex.EncodeToString(acc.CodeHash[:]),
			BytecodeSize: len(onchain),
		}
		for _, f := range c.Functions {
			out.Functions = append(out.Functions, verifyFunction{
				Signature: f.Signature,
				Selector:  hex.EncodeToString(f.Selector[:]),
			})
		}
		for _, e := range c.Events {
			out.Events = append(out.Events, verifyEvent{
				Signature: e.Signature,
				Topic0:    hex.EncodeToString(e.Topic[:]),
			})
		}
		writeVerifyVerdict(w, out)
	}
}

// writeVerifyVerdict answers a well-formed question about a contract. Always
// 200: "did not compile" and "wrong source" are answers, not request errors.
func writeVerifyVerdict(w http.ResponseWriter, resp verifyResponse) {
	writeJSON(w, http.StatusOK, resp)
}

// writeVerifyRefusal answers a request that was not well formed. It keeps the
// same body shape so a caller can decode one type either way.
func writeVerifyRefusal(w http.ResponseWriter, kind, reason string) {
	writeJSON(w, http.StatusBadRequest, verifyResponse{Kind: kind, Reason: reason})
}
