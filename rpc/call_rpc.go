// Package rpc — contract-interaction endpoints (read-only view calls + gas/fee
// estimation) for Splendor Quantum Chain.
//
// These are the node methods the explorer/wallet need for mainnet contract
// interaction that block/tx history can't provide (see issue #4). Both are
// read-only wrappers over the VM engine + native-module read dispatch; neither
// mutates state, needs a signature, nor touches consensus.
//
//	POST /call      — execute a view call against current state (eth_call analog).
//	POST /estimate  — dry-run gas + report the mempool fee floor (eth_estimateGas).
//
// (The eth_getLogs analog lives in explorer.go as POST /logs, over the receipt
// index, because that is where the TxIndexStore is wired.)
package rpc

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterCallRoutes adds the read-only contract-interaction endpoints to the
// router. executor may be nil (native-module reads do not need it); the generic
// bytecode path uses it only to resolve the current block height. pool may be
// nil, in which case /estimate quotes the static fee floor.
//
// guard may be nil in tests; in production pass Server.SimGuard() so these
// routes carry the same per-IP rate limit and global concurrency cap as the
// rest of the metered plane. Each one can run a full gas-limited VM execution,
// so unmetered they are the cheapest way to spend a validator's CPU.
func RegisterCallRoutes(r *mux.Router, stateDB *state.StateDB, executor *block.Executor, pool *mempool.Mempool, guard *ExecGuard) {
	r.Handle("/call", guard.Wrap(makeCallHandler(stateDB, executor))).Methods(http.MethodPost)
	r.Handle("/estimate", guard.Wrap(makeEstimateHandler(stateDB, executor, pool))).Methods(http.MethodPost)
	log.Debug().Msg("contract-interaction RPC routes registered (/call, /estimate)")
}

// ── /call — read-only view call ──────────────────────────────────────────────

type callRequest struct {
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
	Data     string `json:"data,omitempty"`
	Encoding string `json:"encoding,omitempty"` // "hex" (default) | "base64"
	Value    string `json:"value,omitempty"`    // decimal string, default "0"
	// View selects the state the call runs against: "committed" (default, the
	// applied state of the block named in the response) or "speculative"
	// (committed state plus the uncommitted pending layer of the block being
	// assembled or validated). "pending" and "latest" are accepted as the
	// familiar spellings of the two.
	View string `json:"view,omitempty"`
}

type callResponse struct {
	ReturnData   string      `json:"return_data"` // hex, for bytecode/precompile calls
	GasUsed      uint64      `json:"gas_used"`
	Reverted     bool        `json:"reverted"`
	RevertReason string      `json:"revert_reason,omitempty"`
	Decoded      interface{} `json:"decoded,omitempty"` // decoded value for native-module reads
	// The state this answer is true of. ViewHeight/ViewRoot are the committed
	// anchor even when View is "speculative": the pending writes on top of it
	// may never commit.
	View       string `json:"view"`
	ViewHeight uint64 `json:"view_height"`
	ViewRoot   string `json:"view_root"`
}

// View names for callRequest.View / callResponse.View.
const (
	ViewCommitted   = "committed"
	ViewSpeculative = "speculative"
)

// parseView resolves the requested state view. An unknown name is refused
// rather than quietly served from the default: a caller asking for a view this
// node does not have must not be told the answer came from it.
func parseView(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", ViewCommitted, "latest":
		return ViewCommitted, nil
	case ViewSpeculative, "pending":
		return ViewSpeculative, nil
	default:
		return "", fmt.Errorf("unknown 'view' %q (want %q or %q)", name, ViewCommitted, ViewSpeculative)
	}
}

// openSim creates the sandbox for a view. A sandbox that cannot pin its
// LevelDB snapshot is an error, not a silent read of live state: the response
// labels itself with a height and a root, and both would be lies.
func openSim(stateDB *state.StateDB, view string) (*state.StateDB, state.CommittedHead, error) {
	var (
		sim *state.StateDB
		err error
	)
	if view == ViewSpeculative {
		sim, err = stateDB.SimulateSpeculative()
	} else {
		sim, err = stateDB.SimulateCommitted()
	}
	if err != nil {
		return nil, state.CommittedHead{}, err
	}
	head, _ := sim.SandboxView()
	return sim, head, nil
}

func writeViewUnavailable(w http.ResponseWriter, err error) {
	writeError(w, http.StatusServiceUnavailable, "state view unavailable: "+err.Error())
}

func makeCallHandler(stateDB *state.StateDB, executor *block.Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req callRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
			return
		}
		to, err := qcrypto.AddressFromString(req.To)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'to' address (want SQ:…): %v", err))
			return
		}
		calldata, err := decodeCallData(req.Data, req.Encoding)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'data': %v", err))
			return
		}

		view, err := parseView(req.View)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Execution runs against a private overlay. The engine snapshots and
		// reverts as it goes, and the live StateDB's journal is shared with
		// block execution — so simulating on it lets an anonymous caller roll
		// back the block being assembled.
		sim, head, err := openSim(stateDB, view)
		if err != nil {
			writeViewUnavailable(w, err)
			return
		}
		// The sandbox pins a LevelDB snapshot; release it with the request
		// rather than at GC, or every /call defers compaction for a while.
		defer sim.Close()
		viewFields := func(resp *callResponse) {
			resp.View = view
			resp.ViewHeight = head.Height
			resp.ViewRoot = hex.EncodeToString(head.Root[:])
		}

		// Native-module read (token / DEX view calls). Routed explicitly: the
		// module-router address is not a bytecode contract, and a write action
		// has no signature here to authorise it — so /call serves only the
		// module read set via the read-only dispatch, never CallWithContext.
		if to == vm.ModuleRouterAddress {
			decoded, derr := vm.DispatchModuleReadCall(sim, headReadContext(executor), calldata)
			if derr != nil {
				writeError(w, http.StatusBadRequest, derr.Error())
				return
			}
			resp := callResponse{Decoded: decoded}
			viewFields(&resp)
			writeJSON(w, http.StatusOK, resp)
			return
		}

		// Generic view call over the VM engine (precompiles + bytecode
		// contracts). isStatic=true → mutating opcodes are rejected and
		// precompiles are pure, so no state is written; nothing is committed.
		from := qcrypto.Address{}
		if req.From != "" {
			if from, err = qcrypto.AddressFromString(req.From); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'from' address: %v", err))
				return
			}
		}
		value, err := parseValue(req.Value)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		height := simHeight(sim, executor)
		res, cerr := simEngine(sim, executor).CallWithCancel(r.Context(), from, from, to, calldata, value, height, time.Now().Unix(), true)
		if writeSimCancel(w, cerr) {
			return
		}
		if res == nil {
			msg := "call failed"
			if cerr != nil {
				msg = cerr.Error()
			}
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		resp := callResponse{
			ReturnData: fmt.Sprintf("%x", res.ReturnValue),
			GasUsed:    res.GasUsed,
			Reverted:   res.Reverted,
		}
		viewFields(&resp)
		if res.Reverted || res.Err != nil {
			resp.Reverted = true
			if res.Err != nil {
				resp.RevertReason = res.Err.Error()
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ── /estimate — tx-level gas + fee ───────────────────────────────────────────

type estimateRequest struct {
	Type     uint8  `json:"type,omitempty"` // 1 transfer, 2 deploy, 3 call, 5 x402; default: 3 if data else 1
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Data     string `json:"data,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Value    string `json:"value,omitempty"`
	// View selects the state the estimate runs against; see callRequest.View.
	View string `json:"view,omitempty"`
}

type estimateResponse struct {
	Gas          uint64 `json:"gas"`
	Fee          string `json:"fee"`          // smallest SPLD unit — the mempool floor
	FeeIsFloor   bool   `json:"fee_is_floor"` // on this chain fee is flat, not gas-priced
	Reverted     bool   `json:"reverted"`
	RevertReason string `json:"revert_reason,omitempty"`
	Note         string `json:"note,omitempty"`
	// The state this estimate is true of; see callResponse.
	View       string `json:"view"`
	ViewHeight uint64 `json:"view_height"`
	ViewRoot   string `json:"view_root"`
}

func makeEstimateHandler(stateDB *state.StateDB, executor *block.Executor, pool *mempool.Mempool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req estimateRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
			return
		}
		calldata, err := decodeCallData(req.Data, req.Encoding)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'data': %v", err))
			return
		}
		value, err := parseValue(req.Value)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, err := parseView(req.View)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// One view for the whole estimate: the affordability check below and
		// the VM run must not read two different states.
		sim, head, err := openSim(stateDB, view)
		if err != nil {
			writeViewUnavailable(w, err)
			return
		}
		defer sim.Close()

		txType := req.Type
		if txType == 0 {
			if len(calldata) > 0 {
				txType = uint8(types.TxCall)
			} else {
				txType = uint8(types.TxTransfer)
			}
		}

		// Quote the live floor: it rises with pool occupancy, so quoting the
		// static MinFee during congestion would hand back a fee that the
		// mempool then rejects.
		minFee := int64(mempool.MinFee)
		if pool != nil {
			minFee = pool.MinFeeNow()
		}

		resp := estimateResponse{
			Fee:        fmt.Sprintf("%d", minFee),
			FeeIsFloor: true,
			View:       view,
			ViewHeight: head.Height,
			ViewRoot:   hex.EncodeToString(head.Root[:]),
			Note:       "on Splendor Quantum the fee is a mempool floor a sender may exceed for priority; the floor rises while the pool is congested, so re-quote before signing. It is decoupled from VM gas — 'gas' is the VM meter and applies to contract calls only",
		}

		// Read-only affordability check (native SPLD) when 'from' is supplied.
		if req.From != "" {
			from, ferr := qcrypto.AddressFromString(req.From)
			if ferr != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'from' address: %v", ferr))
				return
			}
			if bal, berr := sim.GetBalance(from); berr == nil {
				need := new(big.Int).Add(value, big.NewInt(minFee))
				if bal.Cmp(need) < 0 {
					resp.Reverted = true
					resp.RevertReason = fmt.Sprintf("insufficient balance: have %s, need %s (value+fee)", bal.String(), need.String())
				}
			}
		}

		switch types.TxType(txType) {
		case types.TxCall:
			to, terr := qcrypto.AddressFromString(req.To)
			if terr != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'to' address: %v", terr))
				return
			}
			if to == vm.ModuleRouterAddress {
				// Native-module call: no VM gas meter; only the flat fee applies.
				// Validate the envelope so a malformed call is flagged (no
				// execution), against the live schedule at head+1 so a
				// fork-gated action reads "not active" before it is signed.
				var schedule forks.Schedule
				if executor != nil {
					schedule = executor.Forks()
				}
				if _, verr := vm.PreflightModuleCall(calldata, schedule, simHeight(sim, executor)); verr != nil {
					resp.Reverted = true
					if resp.RevertReason == "" {
						resp.RevertReason = verr.Error()
					}
				}
				resp.Gas = 0
			} else {
				from := qcrypto.Address{}
				if req.From != "" {
					from, _ = qcrypto.AddressFromString(req.From)
				}
				// The committed/speculative view opened above owns sim and
				// closes it; run it under the same fork rules consensus uses,
				// at head+1 so a fork activating there is already active.
				height := simHeight(sim, executor)
				res, err := simEngine(sim, executor).CallWithCancel(r.Context(), from, from, to, calldata, value, height, time.Now().Unix(), true)
				if writeSimCancel(w, err) {
					return
				}
				if res != nil {
					resp.Gas = res.GasUsed
					if res.Reverted || res.Err != nil {
						resp.Reverted = true
						if resp.RevertReason == "" && res.Err != nil {
							resp.RevertReason = res.Err.Error()
						}
					}
				}
			}
		case types.TxDeploy:
			resp.Gas = vm.GasEstimate(calldata, vm.DefaultGasTable())
		default:
			// transfer / x402: no VM gas meter.
			resp.Gas = 0
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// simEngine builds the VM engine the simulation endpoints run on.
//
// A bare vm.NewEngine carries no fork predicates, so gasV1/callV1 read false
// however the chain is configured: once vm_gas_v1 or vm_call_v1 activate,
// /call and /estimate would answer with rules consensus has already left
// behind — a caller would be quoted a child-gas, copy-charge and CALL-value
// model the block that executes the transaction does not use. Consensus
// installs these in block.NewExecutor (SetGasRules / SetCallRules over the
// live schedule); the simulation has to carry the same ones or it is not
// simulating the chain. The schedule is read per request, so one installed
// after the server was built is picked up, as /status picks it up.
func simEngine(sim *state.StateDB, executor *block.Executor) *vm.Engine {
	e := vm.NewEngine(sim)
	if executor == nil {
		return e
	}
	schedule := executor.Forks() // nil-safe: Schedule.Active on nil reports false
	e.SetGasRules(func(h uint64) bool { return schedule.Active(forks.VMGasV1, h) })
	e.SetCallRules(func(h uint64) bool { return schedule.Active(forks.VMCallV1, h) })
	return e
}

// simHeight is the height a simulation is evaluated at: head+1, the block a
// transaction sent now would land in — the same height the module preflight in
// /estimate already uses. Simulating at the head itself re-runs the rules of a
// block that has already been decided, so a fork activating at head+1 would be
// reported inactive by /call and /estimate throughout the block in which it
// first applies, which is exactly when a caller consults them.
//
// The height comes from the pinned view when there is one, not from the
// executor. Asking the executor separately meant a commit landing between
// opening the view and reading the height evaluated the call one block ahead of
// the state it was actually running against — the same two-reads-of-one-thing
// the committed view exists to prevent, reintroduced a line later. A sandbox
// carries the applied height of the snapshot it pinned, so it can answer for
// itself; the executor is the fallback for a caller that has no view.
func simHeight(sim *state.StateDB, executor *block.Executor) uint64 {
	if sim != nil {
		if h, ok := sim.AppliedHeight(); ok {
			return h + 1
		}
	}
	if executor == nil {
		return 0
	}
	head, err := executor.LatestHeight()
	if err != nil {
		return 0
	}
	return head + 1
}

// headReadContext describes the chain head for a native-module read. It is
// zero when there is no executor to ask or the chain has no block yet; module
// reads treat it as advisory, so a zero context is a valid answer, not an
// error (rpc/call_rpc_test.go runs the read path with a nil executor).
func headReadContext(executor *block.Executor) vm.ReadContext {
	if executor == nil {
		return vm.ReadContext{}
	}
	head, err := executor.LatestBlock()
	if err != nil || head == nil {
		return vm.ReadContext{}
	}
	return vm.ReadContext{Height: head.Header.Height, Time: head.Header.Timestamp}
}

// decodeCallData decodes calldata from hex (default) or base64.
func decodeCallData(data, encoding string) ([]byte, error) {
	if data == "" {
		return []byte{}, nil
	}
	switch encoding {
	case "", "hex":
		return hexDecode(data) // shared helper (vm_rpc.go): tolerates a 0x prefix
	case "base64":
		return base64.StdEncoding.DecodeString(data)
	default:
		return nil, fmt.Errorf("unknown encoding %q (want \"hex\" or \"base64\")", encoding)
	}
}

func writeSimCancel(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "simulation deadline exceeded")
		return true
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, http.StatusRequestTimeout, "simulation cancelled")
		return true
	}
	return false
}

// parseValue parses an optional non-negative decimal SPLD value (default 0).
func parseValue(s string) (*big.Int, error) {
	if s == "" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid 'value' (want a non-negative decimal string)")
	}
	return v, nil
}
