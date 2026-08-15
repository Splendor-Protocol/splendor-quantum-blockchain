// Package rpc — Block explorer endpoints for Splendor Quantum Chain.
//
// Additional read-only HTTP endpoints that power block explorers, wallets,
// and analytics dashboards. All endpoints are GET-only and read from the
// TX index store (receipts, logs, transaction lookup).
//
// New endpoints added here:
//   GET /tx/{hash}/receipt           — get receipt by tx hash
//   GET /tx/{hash}                   — get transaction by hash (now implemented via tx index)
//   GET /logs?address=&from=&to=     — filter logs by contract address and block range
//   GET /peers                       — list known peers
//   POST /peers/announce             — announce self to this node
//   GET /sync                        — sync status (is_syncing, local_height, best_height)
//   GET /precompiles                 — list registered precompile addresses
//   GET /contract/{address}          — get contract info (code hash, is_contract)
//   GET /contract/{address}/storage/{slot} — read a storage slot
package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// RegisterExplorerRoutes adds block explorer endpoints to an existing router.
// Call this after building the main router in server.go.
// onPeerAnnounce is an optional callback called when a peer announces itself
// (e.g. to add it to the broadcaster on a validator node).
func RegisterExplorerRoutes(
	r *mux.Router,
	txIndex *state.TxIndexStore,
	stateDB *state.StateDB,
	pool *network.PeerPool,
	localHeight func() uint64,
	onPeerAnnounce ...func(addr string),
) {
	var announceCallback func(addr string)
	if len(onPeerAnnounce) > 0 {
		announceCallback = onPeerAnnounce[0]
	}

	// Receipts + tx lookup (only if txIndex is available)
	if txIndex != nil {
		r.HandleFunc("/tx/{hash}/receipt", makeReceiptHandler(txIndex)).Methods(http.MethodGet)
		r.HandleFunc("/tx/{hash}", makeTxHandler(txIndex)).Methods(http.MethodGet)
	}

	// Peer management
	r.HandleFunc("/peers", makePeersHandler(pool, localHeight)).Methods(http.MethodGet)
	r.HandleFunc("/peers/announce", makeAnnounceHandler(pool, announceCallback)).Methods(http.MethodPost)

	// Sync status
	r.HandleFunc("/sync", makeSyncHandler(pool, localHeight)).Methods(http.MethodGet)

	// Contract inspection
	r.HandleFunc("/contract/{address}", makeContractHandler(stateDB)).Methods(http.MethodGet)
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
	return filter
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
