// Package rpc implements the HTTP JSON-RPC server for Splendor Quantum Chain.
//
// Endpoints:
//   POST /tx/submit         — submit a signed transaction
//   GET  /tx/{hash}         — get transaction by hash
//   GET  /balance/{address} — get account balance
//   GET  /nonce/{address}   — get account nonce
//   GET  /block/{height}    — get block by height
//   GET  /block/latest      — get latest block
//   GET  /block/hash/{hash} — get block by hash
//   GET  /status            — node status (height, mempool size, peers)
//   POST /block             — receive block from peer (P2P inbound)
//   POST /x402/verify       — verify X402 payment (read-only)
//   POST /x402/settle       — settle X402 payment
//   GET  /ai/stats          — AI scheduler statistics
package rpc

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

// Server is the HTTP RPC server.
type Server struct {
	addr        string
	stateDB     *state.StateDB
	executor    *block.Executor
	pool        *mempool.Mempool
	broadcaster *network.Broadcaster
	x402proc    *x402.Processor
	scheduler   *ai.Scheduler
	validatorPK []byte
	router      *mux.Router
	httpServer  *http.Server
	// peerCount returns the number of connected peers (from peer pool)
	peerCount func() int
	// authToken gates state-mutating admin routes (e.g. POST /x402/settle).
	// Empty = those routes stay disabled (fail closed).
	authToken string
	// x402SettleEnabled must be explicitly turned on to expose POST /x402/settle,
	// which moves funds outside the block/consensus path. Off by default.
	x402SettleEnabled bool
}

// Config holds server configuration.
type Config struct {
	ListenAddr        string     // e.g. ":8080"
	ValidatorPK       []byte     // Dilithium public key of the genesis validator
	PeerCount         func() int // optional: returns live peer count
	AuthToken         string     // shared secret required on admin write routes
	X402SettleEnabled bool       // expose POST /x402/settle (off by default)
}

// New creates a new RPC server.
// broadcaster may be nil — P2P broadcasting is optional.
func New(
	cfg Config,
	stateDB *state.StateDB,
	executor *block.Executor,
	pool *mempool.Mempool,
	broadcaster *network.Broadcaster,
	scheduler *ai.Scheduler,
) *Server {
	s := &Server{
		addr:        cfg.ListenAddr,
		stateDB:     stateDB,
		executor:    executor,
		pool:        pool,
		broadcaster: broadcaster,
		x402proc:          x402.NewProcessor(stateDB),
		scheduler:         scheduler,
		validatorPK:       cfg.ValidatorPK,
		peerCount:         cfg.PeerCount,
		authToken:         cfg.AuthToken,
		x402SettleEnabled: cfg.X402SettleEnabled,
	}
	s.router = s.buildRouter()
	s.httpServer = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      s.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Start starts the HTTP server. Blocks until the server stops.
func (s *Server) Start() error {
	log.Info().Str("addr", s.addr).Msg("RPC server starting")
	return s.httpServer.ListenAndServe()
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop() error {
	return s.httpServer.Close()
}

// ServeHTTP implements http.Handler so the server can be embedded in an external router.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// Router returns the underlying mux.Router for external route registration.
func (s *Server) Router() *mux.Router {
	return s.router
}

func (s *Server) buildRouter() *mux.Router {
	r := mux.NewRouter()
	r.Use(loggingMiddleware)
	r.Use(corsMiddleware)

	r.HandleFunc("/tx/submit", s.handleSubmitTx).Methods(http.MethodPost)
	r.HandleFunc("/tx/{hash}", s.handleGetTx).Methods(http.MethodGet)
	r.HandleFunc("/balance/{address}", s.handleGetBalance).Methods(http.MethodGet)
	r.HandleFunc("/nonce/{address}", s.handleGetNonce).Methods(http.MethodGet)
	r.HandleFunc("/block/latest", s.handleGetLatestBlock).Methods(http.MethodGet)
	r.HandleFunc("/block/hash/{hash}", s.handleGetBlockByHash).Methods(http.MethodGet)
	r.HandleFunc("/block/{height}", s.handleGetBlockByHeight).Methods(http.MethodGet)
	r.HandleFunc("/block", s.handleIncomingBlock).Methods(http.MethodPost)
	r.HandleFunc("/status", s.handleStatus).Methods(http.MethodGet)
	r.HandleFunc("/x402/verify", s.handleX402Verify).Methods(http.MethodPost)
	r.HandleFunc("/x402/settle", s.handleX402Settle).Methods(http.MethodPost)
	r.HandleFunc("/ai/stats", s.handleAIStats).Methods(http.MethodGet)

	return r
}

// ── Handlers ───────────────────────────────────────────────────────────────

func (s *Server) handleSubmitTx(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024)) // 512KB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var tx types.Transaction
	if err := json.Unmarshal(body, &tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid transaction JSON: %v", err))
		return
	}

	if err := s.pool.Add(&tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
		return
	}

	hash, _ := tx.FullHash()
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "accepted",
		"hash":   fmt.Sprintf("%x", hash[:]),
	})
}

func (s *Server) handleGetTx(w http.ResponseWriter, r *http.Request) {
	// Prefer explorer route when txIndex is registered on the outer mux.
	// This stub remains for standalone Server usage without explorer routes.
	writeError(w, http.StatusNotFound,
		"transaction index not available on this node — mount explorer routes with a TxIndexStore, or use GET /tx/{hash}/receipt")
}

func (s *Server) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	addrStr := mux.Vars(r)["address"]
	addr, err := parseAddress(addrStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
		return
	}

	balance, err := s.stateDB.GetBalance(addr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"address": addrStr,
		"balance": balance.String(),
		"unit":    "smallest_spld",
	})
}

func (s *Server) handleGetNonce(w http.ResponseWriter, r *http.Request) {
	addrStr := mux.Vars(r)["address"]
	addr, err := parseAddress(addrStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
		return
	}

	nonce, err := s.stateDB.GetNonce(addr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"address": addrStr,
		"nonce":   nonce,
	})
}

func (s *Server) handleGetLatestBlock(w http.ResponseWriter, r *http.Request) {
	b, err := s.executor.LatestBlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "no blocks yet")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleGetBlockByHeight(w http.ResponseWriter, r *http.Request) {
	heightStr := mux.Vars(r)["height"]
	var height uint64
	if _, err := fmt.Sscanf(heightStr, "%d", &height); err != nil {
		writeError(w, http.StatusBadRequest, "invalid height")
		return
	}

	b, err := s.executor.GetBlockByHeight(height)
	if err == types.ErrBlockNotFound {
		writeError(w, http.StatusNotFound, fmt.Sprintf("block %d not found", height))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleGetBlockByHash(w http.ResponseWriter, r *http.Request) {
	hashHex := mux.Vars(r)["hash"]
	b, err := s.executor.GetBlock(hashHex)
	if err == types.ErrBlockNotFound {
		writeError(w, http.StatusNotFound, "block not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleIncomingBlock(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024)) // 64MB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	err = network.HandleIncomingBlock(body, s.validatorPK, func(b *types.Block) error {
		return s.executor.ExecuteBlock(b)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("block rejected: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	height, _ := s.executor.LatestHeight()
	// Peer count: use peerCount func if set (from peerPool), else broadcaster, else 0
	peerCount := 0
	if s.peerCount != nil {
		peerCount = s.peerCount()
	} else if s.broadcaster != nil {
		peerCount = s.broadcaster.PeerCount()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"chain_id":     types.ChainID,
		"height":       height,
		"mempool_size": s.pool.Size(),
		"peer_count":   peerCount,
		"node_version": "1.0.0-quantum",
		"timestamp":    time.Now().Unix(),
	})
}

func (s *Server) handleX402Verify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // bound the public verify body
	var req struct {
		Requirements x402.PaymentRequirements `json:"requirements"`
		Payload      x402.PaymentPayload      `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	result := s.x402proc.Verify(&req.Requirements, &req.Payload)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleX402Settle(w http.ResponseWriter, r *http.Request) {
	// POST /x402/settle moves funds directly in state, OUTSIDE the block/consensus
	// path, so it is disabled unless explicitly enabled AND authenticated. This
	// prevents an anonymous caller over the public RPC tunnel from moving balances
	// with no on-chain record. (Enable with --enable-x402-settle + --rpc-auth-token.)
	if !s.x402SettleEnabled {
		writeError(w, http.StatusServiceUnavailable, "x402 settlement is disabled on this node")
		return
	}
	if !s.checkAuth(r) {
		writeError(w, http.StatusUnauthorized, "missing or invalid X-Node-Auth token")
		return
	}

	var req struct {
		Requirements x402.PaymentRequirements `json:"requirements"`
		Payload      x402.PaymentPayload      `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	result := s.x402proc.Verify(&req.Requirements, &req.Payload)
	if !result.Valid {
		writeJSON(w, http.StatusPaymentRequired, x402.SettleResult{
			Error: result.Reason,
		})
		return
	}

	// NOTE: this settlement mutates state directly rather than going through a
	// signed transaction + block. That is a known Phase-1 shortcut (the moved
	// funds appear in no block/merkle root); the gating above keeps it to trusted
	// callers until it is reworked to route through the mempool.
	payerNonce, err := s.stateDB.GetNonce(req.Payload.From)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "nonce lookup failed")
		return
	}

	_ = payerNonce // settlement tx nonce management handled by x402 processor

	// For Phase 1, we directly mark the nonce and transfer in state
	// (simplified settlement without full tx round-trip)
	nonceKey := buildNonceKey(req.Payload.From, req.Payload.Nonce)
	height, _ := s.executor.LatestHeight()
	if err := s.stateDB.MarkX402NonceUsed(nonceKey, height); err != nil {
		writeError(w, http.StatusInternalServerError, "nonce registry error")
		return
	}
	if err := s.stateDB.Transfer(req.Payload.From, req.Payload.To, req.Payload.Amount); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("transfer failed: %v", err))
		return
	}
	if _, err := s.stateDB.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "state commit failed")
		return
	}

	writeJSON(w, http.StatusOK, x402.SettleResult{
		Success: true,
		TxHash:  fmt.Sprintf("%x", nonceKey[:8]),
	})
}

func (s *Server) handleAIStats(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "scheduler not running"})
		return
	}
	writeJSON(w, http.StatusOK, s.scheduler.GetStats())
}

// ── Helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// parseAddress parses an SQ: address string into a qcrypto.Address.
func parseAddress(s string) (qcrypto.Address, error) {
	return qcrypto.AddressFromString(s)
}

// checkAuth reports whether the request carries the configured admin auth token
// in the X-Node-Auth header, compared in constant time. If no token is
// configured the answer is always false, so admin routes fail closed.
func (s *Server) checkAuth(r *http.Request) bool {
	if s.authToken == "" {
		return false
	}
	got := r.Header.Get("X-Node-Auth")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) == 1
}

// buildNonceKey computes SHA3-256(from_address || nonce_bytes).
func buildNonceKey(from qcrypto.Address, nonce [32]byte) [32]byte {
	combined := append(from[:], nonce[:]...)
	return qcrypto.Hash32(combined)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote", r.RemoteAddr).
			Msg("RPC request")
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
