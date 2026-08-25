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
	// writeAuth gates the write plane (block ingest, peer announce). With no
	// token configured those routes stay disabled, so a misconfigured node fails
	// closed rather than serving them unauthenticated.
	writeAuth *WriteAuth
	// cors is the browser-origin allowlist policy.
	cors *CORSPolicy
	// submitLimiter bounds public transaction submission per client IP.
	submitLimiter *RateLimiter
}

// Config holds server configuration.
type Config struct {
	ListenAddr  string     // e.g. "127.0.0.1:8080"
	ValidatorPK []byte     // ML-DSA public key of the genesis validator
	PeerCount   func() int // optional: returns live peer count
	AuthToken   string     // shared secret required on write-plane routes

	// CORSOrigins is the browser-origin allowlist. Empty = emit no CORS headers.
	CORSOrigins []string
	// SubmitRatePerSec / SubmitBurst bound public transaction submission per IP.
	// Zero rate disables limiting.
	SubmitRatePerSec float64
	SubmitBurst      float64
	// TrustProxy reads the client IP from X-Forwarded-For. Only enable behind a
	// reverse proxy you control — otherwise the header is attacker-controlled and
	// rate limits can be bypassed by spoofing it.
	TrustProxy bool
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
		x402proc:      x402.NewProcessor(stateDB),
		scheduler:     scheduler,
		validatorPK:   cfg.ValidatorPK,
		peerCount:     cfg.PeerCount,
		writeAuth:     NewWriteAuth(cfg.AuthToken),
		cors:          NewCORSPolicy(cfg.CORSOrigins),
		submitLimiter: NewRateLimiter(cfg.SubmitRatePerSec, cfg.SubmitBurst, cfg.TrustProxy),
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
	r.Use(s.cors.Middleware)

	// ── Read plane: safe to expose publicly ──────────────────────────────
	r.HandleFunc("/tx/{hash}", s.handleGetTx).Methods(http.MethodGet)
	r.HandleFunc("/balance/{address}", s.handleGetBalance).Methods(http.MethodGet)
	r.HandleFunc("/nonce/{address}", s.handleGetNonce).Methods(http.MethodGet)
	r.HandleFunc("/block/latest", s.handleGetLatestBlock).Methods(http.MethodGet)
	r.HandleFunc("/block/hash/{hash}", s.handleGetBlockByHash).Methods(http.MethodGet)
	r.HandleFunc("/block/{height}", s.handleGetBlockByHeight).Methods(http.MethodGet)
	r.HandleFunc("/status", s.handleStatus).Methods(http.MethodGet)
	r.HandleFunc("/ai/stats", s.handleAIStats).Methods(http.MethodGet)

	// ── Public but metered: each of these costs an ML-DSA verification ───
	r.Handle("/tx/submit", s.limited(s.handleSubmitTx)).Methods(http.MethodPost)
	r.Handle("/x402/verify", s.limited(s.handleX402Verify)).Methods(http.MethodPost)
	r.Handle("/x402/settle", s.limited(s.handleX402Settle)).Methods(http.MethodPost)

	// ── Write plane: authenticated. POST /block applies a block to local
	// state, so it must never be reachable by an anonymous caller. ───────
	r.Handle("/block", s.writeAuth.Middleware(http.HandlerFunc(s.handleIncomingBlock))).
		Methods(http.MethodPost)

	return r
}

// limited wraps a handler with the public-submission rate limiter.
func (s *Server) limited(h http.HandlerFunc) http.Handler {
	return s.submitLimiter.Middleware(h)
}

// WriteAuth exposes the write-plane guard so routes registered outside this
// file (explorer peer announce) can enforce the same policy.
func (s *Server) WriteAuth() *WriteAuth { return s.writeAuth }

// CORS exposes the origin policy so an outer router can apply the same rules.
func (s *Server) CORS() *CORSPolicy { return s.cors }

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
	// Bound the block body at the consensus wire-size limit. A block larger than
	// this could never be valid, so refusing to buffer it costs nothing and
	// removes a memory-exhaustion vector.
	r.Body = http.MaxBytesReader(w, r.Body, block.MaxBlockWireBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("block body exceeds the %d-byte limit", block.MaxBlockWireBytes))
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

// handleX402Settle settles a verified X402 payment by admitting the payer's
// signed settlement transaction to the mempool.
//
// It does NOT move balances itself. An earlier version called Transfer +
// Commit directly in this handler, which changed balances with no transaction
// in any block: replaying the chain could not reproduce the transfer, and nodes
// that never received the HTTP call diverged from the one that did. Settlement
// now goes through consensus like any other transaction, so the payment appears
// in a block and in the state root.
func (s *Server) handleX402Settle(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

	var req struct {
		Requirements x402.PaymentRequirements `json:"requirements"`
		Payload      x402.PaymentPayload      `json:"payload"`
		// SettlementTx is the payer-signed TxX402 that performs the transfer.
		// Only the payer can produce it: settlement debits the payer's account
		// and consumes the payer's account nonce.
		SettlementTx *types.Transaction `json:"settlement_tx"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	result := s.x402proc.Verify(&req.Requirements, &req.Payload)
	if !result.Valid {
		writeJSON(w, http.StatusPaymentRequired, x402.SettleResult{Error: result.Reason})
		return
	}

	if req.SettlementTx == nil {
		writeError(w, http.StatusBadRequest,
			"settlement_tx is required: sign a TxX402 with the payer key (see payX402 in @splendor/pq-bridge)")
		return
	}
	if err := x402.BindSettlementTx(&req.Payload, req.SettlementTx); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The mempool verifies the payer's signature, account nonce, fee floor, and
	// balance. The executor re-checks the X402 replay registry at block time.
	if err := s.pool.Add(req.SettlementTx); err != nil {
		writeJSON(w, http.StatusBadRequest, x402.SettleResult{
			Error: fmt.Sprintf("settlement rejected: %v", err),
		})
		return
	}

	hash, err := req.SettlementTx.FullHash()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash settlement tx")
		return
	}

	writeJSON(w, http.StatusAccepted, x402.SettleResult{
		Success: true,
		TxHash:  fmt.Sprintf("%x", hash[:]),
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

