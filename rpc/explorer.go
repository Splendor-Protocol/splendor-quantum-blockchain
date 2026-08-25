// Package rpc — Block explorer endpoints for Splendor Quantum Chain.
//
// Additional read-only HTTP endpoints that power block explorers, wallets,
// and analytics dashboards. All endpoints are GET-only and read from the
// TX index store (receipts, logs, transaction lookup).
//
// New endpoints added here:
//
//	GET /tx/{hash}/receipt           — get receipt by tx hash
//	GET /tx/{hash}                   — get transaction by hash (now implemented via tx index)
//	GET /logs?address=&from=&to=     — filter logs by contract address and block range
//	GET /peers                       — list known peers
//	POST /peers/announce             — announce self to this node
//	GET /sync                        — sync status (is_syncing, local_height, best_height)
//	GET /precompiles                 — list registered precompile addresses
//	GET /contract/{address}          — get contract info (code hash, is_contract)
//	GET /contract/{address}/storage/{slot} — read a storage slot
package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterExplorerRoutes adds block explorer endpoints to an existing router.
// Call this after building the main router in server.go.
//
// writeAuth guards POST /peers/announce. An announced address becomes an
// outbound target for block pushes and sync GETs, so accepting announcements
// from anonymous callers lets a stranger steer this node's peer set. The SSRF
// guard limits where those requests can go; the token limits who can add them.
//
// onPeerAnnounce is an optional callback called when a peer announces itself
// (e.g. to add it to the broadcaster on a validator node).
func RegisterExplorerRoutes(
	r *mux.Router,
	txIndex *state.TxIndexStore,
	stateDB *state.StateDB,
	pool *network.PeerPool,
	localHeight func() uint64,
	writeAuth *WriteAuth,
	guard *ExecGuard,
	onPeerAnnounce ...func(addr string),
) {
	var announceCallback func(addr string)
	if len(onPeerAnnounce) > 0 {
		announceCallback = onPeerAnnounce[0]
	}

	// Receipts + tx lookup + log filter (only if txIndex is available)
	if txIndex != nil {
		r.HandleFunc("/tx/{hash}/receipt", makeReceiptHandler(txIndex)).Methods(http.MethodGet)
		r.HandleFunc("/tx/{hash}", makeTxHandler(txIndex)).Methods(http.MethodGet)
		// eth_getLogs analog. The Postgres indexer serves the scalable, unbounded
		// version at /idx/v1/logs; this native method is for SDK/RPC-standard
		// parity + bounded ranges (see maxLogBlockRange). Metered with the
		// simulation plane: the range cap bounds one request, not the number of
		// concurrent full-range scans a caller can start.
		r.Handle("/logs", guard.Wrap(makeLogsHandler(txIndex, localHeight))).
			Methods(http.MethodPost, http.MethodGet)
	}

	// Peer management
	r.HandleFunc("/peers", makePeersHandler(pool, localHeight)).Methods(http.MethodGet)
	r.Handle("/peers/announce",
		writeAuth.Middleware(makeAnnounceHandler(pool, announceCallback))).
		Methods(http.MethodPost)

	// Sync status
	r.HandleFunc("/sync", makeSyncHandler(pool, localHeight)).Methods(http.MethodGet)

	// Contract inspection
	r.HandleFunc("/contract/{address}", makeContractHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/contract/{address}/code", makeContractCodeHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/contract/{address}/storage/{slot}", makeStorageHandler(stateDB)).Methods(http.MethodGet)

	log.Debug().Msg("explorer routes registered")
}

// ── Receipt handler ───────────────────────────────────────────────────────────

func makeReceiptHandler(txIndex *state.TxIndexStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hashHex := mux.Vars(r)["hash"]
		hashBytes, err := hexToBytes32(hashHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid tx hash: %v", err))
			return
		}

		receipt, err := txIndex.GetReceipt(hashBytes)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("receipt not found: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, receipt)
	}
}

// makeTxHandler returns transaction location (+ receipt when present) by hash.
func makeTxHandler(txIndex *state.TxIndexStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hashHex := mux.Vars(r)["hash"]
		hashBytes, err := hexToBytes32(hashHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid tx hash: %v", err))
			return
		}

		loc, err := txIndex.GetTxLocation(hashBytes)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("transaction not found: %v", err))
			return
		}

		out := map[string]interface{}{
			"hash":         hashHex,
			"block_height": loc.BlockHeight,
			"block_hash":   fmt.Sprintf("%x", loc.BlockHash),
			"tx_index":     loc.TxIndex,
		}
		if receipt, rerr := txIndex.GetReceipt(hashBytes); rerr == nil && receipt != nil {
			out["receipt"] = receipt
			out["status"] = receipt.Status
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// ── Peer handlers ─────────────────────────────────────────────────────────────

func makePeersHandler(pool *network.PeerPool, localHeight func() uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Return peer list including self
		selfAddr := r.Host
		peers := pool.FormatPeerList("http://"+selfAddr, localHeight())
		writeJSON(w, http.StatusOK, peers)
	}
}

func makeAnnounceHandler(pool *network.PeerPool, onAnnounce func(addr string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var peer network.PeerInfo
		if err := json.Unmarshal(body, &peer); err != nil {
			writeError(w, http.StatusBadRequest, "invalid peer info JSON")
			return
		}
		if peer.Addr == "" {
			writeError(w, http.StatusBadRequest, "peer addr required")
			return
		}

		// SSRF guard: an announced peer URL becomes an outbound target for block
		// pushes and sync GETs. Reject non-http(s) schemes and any host that
		// resolves into loopback / private / link-local ranges — including the
		// cloud instance-metadata IP 169.254.169.254 — so an anonymous caller
		// cannot coerce the node into probing internal services. (Same guard is
		// applied to PEX-discovered peers in network.Discoverer.)
		if err := network.IsSafePeerURL(peer.Addr); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected peer addr: %v", err))
			return
		}

		// Add to peer pool for discovery
		pool.Add(&peer)

		// Notify broadcaster (if validator) to start pushing blocks to this peer
		if onAnnounce != nil {
			onAnnounce(peer.Addr)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
	}
}

// ── Sync status handler ───────────────────────────────────────────────────────

func makeSyncHandler(pool *network.PeerPool, localHeight func() uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := pool.GetSyncStatus(localHeight())
		writeJSON(w, http.StatusOK, status)
	}
}

// ── Contract inspection handlers ──────────────────────────────────────────────

func makeContractHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["address"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		acc, err := stateDB.GetAccount(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"address":      addrStr,
			"is_contract":  acc.IsContract,
			"code_hash":    fmt.Sprintf("%x", acc.CodeHash[:]),
			"storage_root": fmt.Sprintf("%x", acc.StorageRoot[:]),
			"balance":      acc.Balance.String(),
			"nonce":        acc.Nonce,
		})
	}
}

// makeContractCodeHandler serves GET /contract/{address}/code — the runtime code
// of a contract, so explorers/tools can show it.
//
// Two cases, because this chain has two kinds of "contract":
//   - Native-module contracts (QRC-20, DEX) carry NO per-contract bytecode
//     (code_hash is zero). Their behaviour is the canonical module compiled into
//     the node. We report native_module=true with empty code.
//   - Raw-bytecode contracts (TxDeploy) store runtime bytecode; we return it as
//     hex plus a disassembly listing (same as POST /vm/disasm, but by address).
func makeContractCodeHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["address"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}
		acc, err := stateDB.GetAccount(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		codeHashHex := fmt.Sprintf("%x", acc.CodeHash[:])
		// A zero code hash = no stored bytecode (native module or a plain account).
		if acc.CodeHash == ([32]byte{}) {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"address":       addrStr,
				"is_contract":   acc.IsContract,
				"native_module": acc.IsContract, // a contract with no bytecode is a native module
				"code_hash":     codeHashHex,
				"code":          "",
				"code_size":     0,
				"disassembly":   []string{},
			})
			return
		}

		code, err := stateDB.GetCode(acc.CodeHash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("load code: %v", err))
			return
		}
		instructions := vm.Disassemble(code)
		listing := make([]string, len(instructions))
		for i, inst := range instructions {
			listing[i] = inst.String()
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"address":       addrStr,
			"is_contract":   acc.IsContract,
			"native_module": false,
			"code_hash":     codeHashHex,
			"code":          fmt.Sprintf("%x", code),
			"code_size":     len(code),
			"disassembly":   listing,
		})
	}
}

func makeStorageHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		addrStr := vars["address"]
		slotHex := vars["slot"]

		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		slot, err := hexToBytes32(slotHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid slot: %v", err))
			return
		}

		val, err := stateDB.GetStorageValue(addr, slot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"address": addrStr,
			"slot":    slotHex,
			"value":   fmt.Sprintf("%x", val),
		})
	}
}

// ── Log filter handler ─────────────────────────────────────────────────────────

// LogFilter specifies which logs to retrieve.
type LogFilter struct {
	// Contract address to filter by (optional)
	Address string `json:"address"`

	// Block range to scan
	FromBlock uint64 `json:"from_block"`
	ToBlock   uint64 `json:"to_block"`

	// Topics to filter by (optional, SHA3-256 hashes)
	Topics []string `json:"topics"`
}

// parseLogFilter parses query parameters into a LogFilter.
func parseLogFilter(r *http.Request) LogFilter {
	q := r.URL.Query()
	filter := LogFilter{
		Address: q.Get("address"),
	}

	if from := q.Get("from"); from != "" {
		filter.FromBlock, _ = strconv.ParseUint(from, 10, 64)
	}
	if to := q.Get("to"); to != "" {
		filter.ToBlock, _ = strconv.ParseUint(to, 10, 64)
	}
	if t := q.Get("topics"); t != "" {
		filter.Topics = strings.Split(t, ",")
	}
	return filter
}

// ── Log query handler (eth_getLogs analog) ──────────────────────────────────────

const (
	// maxLogBlockRange bounds a single /logs scan (this native method iterates
	// receipts, so an all-miss filter still costs O(txs in the window)). Kept
	// deliberately small: this endpoint is for recent/SDK-parity queries;
	// unbounded history is served by the Postgres indexer (/idx/v1/logs).
	maxLogBlockRange = 2_000
	// maxLogResults caps returned logs; truncated=true signals more matched.
	maxLogResults = 5_000
)

// makeLogsHandler filters event logs by block range + address + positional
// topics, reading the receipt index the node already maintains. Accepts a JSON
// body (POST) or query params (GET). Both /call and this endpoint are read-only.
func makeLogsHandler(txIndex *state.TxIndexStore, localHeight func() uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var filter LogFilter
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read body")
				return
			}
			if len(body) > 0 {
				if err := json.Unmarshal(body, &filter); err != nil {
					writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
					return
				}
			}
		} else {
			filter = parseLogFilter(r)
		}

		var wantAddr *qcrypto.Address
		if filter.Address != "" {
			a, err := qcrypto.AddressFromString(filter.Address)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
				return
			}
			wantAddr = &a
		}

		wantTopics, err := parseTopicFilters(filter.Topics)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Clamp the range to the current head, then bound the scan window.
		head := localHeight()
		if filter.ToBlock == 0 || filter.ToBlock > head {
			filter.ToBlock = head
		}
		if filter.FromBlock > filter.ToBlock {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"logs": []*types.Log{}, "count": 0,
				"from_block": filter.FromBlock, "to_block": filter.ToBlock, "truncated": false,
			})
			return
		}
		if filter.ToBlock-filter.FromBlock > maxLogBlockRange {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"block range too large: %d blocks (max %d per request). Narrow the range, or use the indexer's /idx/v1/logs for unbounded history.",
				filter.ToBlock-filter.FromBlock, maxLogBlockRange))
			return
		}

		out := make([]*types.Log, 0, 64)
		truncated := false
		for h := filter.FromBlock; h <= filter.ToBlock && !truncated; h++ {
			hashes, herr := txIndex.GetBlockTxHashes(h)
			if herr != nil || len(hashes) == 0 {
				continue
			}
			for _, hash := range hashes {
				rcpt, rerr := txIndex.GetReceipt(hash)
				if rerr != nil || rcpt == nil {
					continue
				}
				for _, lg := range rcpt.Logs {
					if wantAddr != nil && lg.Address != *wantAddr {
						continue
					}
					if !topicsMatch(wantTopics, lg.Topics) {
						continue
					}
					out = append(out, lg)
					if len(out) >= maxLogResults {
						truncated = true
						break
					}
				}
				if truncated {
					break
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"logs":       out,
			"count":      len(out),
			"from_block": filter.FromBlock,
			"to_block":   filter.ToBlock,
			"truncated":  truncated,
		})
	}
}

// parseTopicFilters converts positional topic hex strings to [32]byte matchers.
// An empty string or "null" at a position is a wildcard (matches any topic).
func parseTopicFilters(topics []string) ([]*[32]byte, error) {
	out := make([]*[32]byte, 0, len(topics))
	for i, t := range topics {
		if t == "" || t == "null" {
			out = append(out, nil)
			continue
		}
		b, err := hexToBytes32(t)
		if err != nil {
			return nil, fmt.Errorf("invalid topic[%d]: %v", i, err)
		}
		bb := b
		out = append(out, &bb)
	}
	return out, nil
}

// topicsMatch applies eth_getLogs positional topic semantics: filter[i] must
// equal logTopics[i] (a nil filter position is a wildcard). A filter longer than
// the log's topic list never matches.
func topicsMatch(filter []*[32]byte, logTopics [][32]byte) bool {
	if len(filter) > len(logTopics) {
		return false
	}
	for i, f := range filter {
		if f == nil {
			continue
		}
		if logTopics[i] != *f {
			return false
		}
	}
	return true
}

// ── Utility helpers ────────────────────────────────────────────────────────────

// hexToBytes32 converts a hex string (with or without 0x prefix) to [32]byte.
func hexToBytes32(s string) ([32]byte, error) {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	if len(s) != 64 {
		return [32]byte{}, fmt.Errorf("expected 64 hex chars, got %d", len(s))
	}
	var b [32]byte
	for i := 0; i < 32; i++ {
		hi := hexNibble(s[i*2])
		lo := hexNibble(s[i*2+1])
		if hi < 0 || lo < 0 {
			return [32]byte{}, fmt.Errorf("invalid hex char at position %d", i*2)
		}
		b[i] = byte(hi<<4 | lo)
	}
	return b, nil
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}
