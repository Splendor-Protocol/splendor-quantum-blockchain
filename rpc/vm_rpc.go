// Package rpc — VM-specific RPC endpoints for Splendor Quantum Chain.
//
// New endpoints:
//
//	POST /vm/disasm          — disassemble SQC bytecode → human-readable listing
//	POST /vm/validate        — validate bytecode structure, report warnings/errors
//	POST /vm/estimate-gas    — estimate gas for a bytecode sequence
//	GET  /healthz            — node health check (liveness probe)
//	GET  /readyz             — node readiness check (has committed at least 1 block)
package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterVMRoutes adds VM inspection endpoints to an existing router.
func RegisterVMRoutes(r *mux.Router, nodeStartTime time.Time, isReady func() bool) {
	r.HandleFunc("/vm/disasm", handleVMDisasm).Methods(http.MethodPost)
	r.HandleFunc("/vm/validate", handleVMValidate).Methods(http.MethodPost)
	r.HandleFunc("/vm/estimate-gas", handleVMEstimateGas).Methods(http.MethodPost)
	r.HandleFunc("/vm/opcodes", handleVMOpcodes).Methods(http.MethodGet)
	r.HandleFunc("/healthz", makeHealthzHandler(nodeStartTime)).Methods(http.MethodGet)
	r.HandleFunc("/readyz", makeReadyzHandler(isReady)).Methods(http.MethodGet)

	log.Debug().Msg("VM RPC routes registered")
}

// ── Request/response types ─────────────────────────────────────────────────────

type disasmRequest struct {
	// Hex-encoded bytecode (with or without 0x prefix).
	Bytecode string `json:"bytecode"`
}

type disasmResponse struct {
	// Listing is the full disassembly as an array of instruction strings.
	Listing []string `json:"listing"`
	// Instructions is the structured representation.
	Instructions []disasmInstruction `json:"instructions"`
	// TotalBytes is the total size of the bytecode.
	TotalBytes int `json:"total_bytes"`
	// GasEstimate is a static gas estimate (approximate).
	GasEstimate uint64 `json:"gas_estimate"`
}

type disasmInstruction struct {
	Offset    int    `json:"offset"`
	Op        string `json:"op"`
	Name      string `json:"name"`
	Immediate string `json:"immediate,omitempty"` // hex-encoded, if present
	Size      int    `json:"size"`
}

type validateRequest struct {
	Bytecode string `json:"bytecode"`
}

type validateResponse struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

type estimateGasRequest struct {
	Bytecode string `json:"bytecode"`
	// Network: "mainnet" or "testnet" (default: mainnet)
	Network string `json:"network,omitempty"`
}

type estimateGasResponse struct {
	GasEstimate uint64 `json:"gas_estimate"`
	Network     string `json:"network"`
}

// ── Handlers ───────────────────────────────────────────────────────────────────

func handleVMDisasm(w http.ResponseWriter, r *http.Request) {
	var req disasmRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	bytecode, err := hexDecode(req.Bytecode)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bytecode hex: %v", err))
		return
	}

	instructions := vm.Disassemble(bytecode)
	gasTable := vm.DefaultGasTable()

	resp := disasmResponse{
		TotalBytes:   len(bytecode),
		GasEstimate:  vm.GasEstimate(bytecode, gasTable),
		Listing:      make([]string, len(instructions)),
		Instructions: make([]disasmInstruction, len(instructions)),
	}

	for i, inst := range instructions {
		resp.Listing[i] = inst.String()
		di := disasmInstruction{
			Offset: inst.Offset,
			Op:     fmt.Sprintf("0x%02X", inst.Op),
			Name:   inst.Name,
			Size:   inst.Size,
		}
		if len(inst.Immediate) > 0 {
			di.Immediate = hex.EncodeToString(inst.Immediate)
		}
		resp.Instructions[i] = di
	}

	writeJSON(w, http.StatusOK, resp)
}

func handleVMValidate(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	bytecode, err := hexDecode(req.Bytecode)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bytecode hex: %v", err))
		return
	}

	warnings, errors := vm.ValidateBytecode(bytecode)
	writeJSON(w, http.StatusOK, validateResponse{
		Valid:    len(errors) == 0,
		Warnings: nullToEmpty(warnings),
		Errors:   nullToEmpty(errors),
	})
}

func handleVMEstimateGas(w http.ResponseWriter, r *http.Request) {
	var req estimateGasRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	bytecode, err := hexDecode(req.Bytecode)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bytecode hex: %v", err))
		return
	}

	network := req.Network
	if network == "" {
		network = "mainnet"
	}

	var gasTable vm.GasTable
	switch strings.ToLower(network) {
	case "testnet":
		gasTable = vm.TestnetGasTable()
	default:
		gasTable = vm.DefaultGasTable()
		network = "mainnet"
	}

	writeJSON(w, http.StatusOK, estimateGasResponse{
		GasEstimate: vm.GasEstimate(bytecode, gasTable),
		Network:     network,
	})
}

// handleVMOpcodes returns the complete opcode table as a JSON map.
func handleVMOpcodes(w http.ResponseWriter, r *http.Request) {
	opcodes := make(map[string]string, 256)
	for i := 0; i < 256; i++ {
		name := vm.OpName(byte(i))
		if !strings.HasPrefix(name, "UNKNOWN_") {
			opcodes[fmt.Sprintf("0x%02X", i)] = name
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"opcodes": opcodes,
		"count":   len(opcodes),
		"version": "sqc-vm-v3",
	})
}

// ── Health / Readiness ─────────────────────────────────────────────────────────

func makeHealthzHandler(startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":         "ok",
			"uptime_seconds": int64(time.Since(startTime).Seconds()),
			"chain_id":       types.ChainID,
			"node_version":   buildinfo.Version,
			"commit":         buildinfo.Commit,
			"image_tag":      buildinfo.ImageTag,
			"timestamp":      time.Now().Unix(),
		})
	}
}

func makeReadyzHandler(isReady func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isReady == nil || isReady() {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		} else {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"reason": "chain not yet synced",
			})
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// hexDecode decodes a hex string (with or without 0x prefix) to bytes.
func hexDecode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
	}
	return hex.DecodeString(s)
}

// readJSON reads and decodes a JSON request body.
func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// nullToEmpty converts a nil slice to an empty slice for clean JSON output.
func nullToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
