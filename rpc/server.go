// Package rpc implements the HTTP JSON-RPC server for Splendor Quantum Chain.
//
// Endpoints:
//
//	POST /tx/submit         — submit a signed transaction
//	GET  /tx/{hash}         — get transaction by hash
//	GET  /balance/{address} — get account balance
//	GET  /nonce/{address}   — get account nonce
//	GET  /block/{height}    — get block by height
//	GET  /block/latest      — get latest block
//	GET  /block/hash/{hash} — get block by hash
//	GET  /status            — node status (height, mempool size, peers)
//	POST /block             — receive block from peer (P2P inbound)
//	POST /x402/verify       — verify X402 payment (read-only)
//	POST /x402/settle       — settle X402 payment
//	GET  /ai/stats          — AI scheduler statistics
package rpc

import (
	"encoding/json"
	"fmt"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
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
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
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
	// simGuard meters the anonymous VM-execution routes (/call, /estimate) and
	// the receipt scan behind /logs.
	simGuard *ExecGuard
	// allowPlaintextP2P registers the cleartext peer routes. Off = sealed only.
	allowPlaintextP2P bool
}

// Config holds server configuration.
type Config struct {
	ListenAddr  string     // e.g. "127.0.0.1:8080"
	ValidatorPK []byte     // ML-DSA public key of the genesis validator
	PeerCount   func() int // optional: returns live peer count
	AuthToken   string     // shared secret required on write-plane routes

	// AllowPlaintextP2P registers the CLEARTEXT peer routes (POST /block,
	// POST /peers/announce). The flag of the same name used to gate only the
	// outbound direction, so a node that refused to SEND cleartext still ACCEPTED
	// it — which is not what "the channel cannot be downgraded" means to anyone
	// reading it. Off means the sealed transport is the only way in.
	AllowPlaintextP2P bool

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

	// SimRatePerSec / SimBurst / SimMaxInFlight / SimTimeout bound the routes
	// that execute the VM or scan the receipt index for an anonymous caller
	// (/call, /estimate, /logs). Zero values fall back to the defaults below.
	SimRatePerSec  float64
	SimBurst       float64
	SimMaxInFlight int
	SimTimeout     time.Duration
}

// Defaults for the simulation plane.
//
// The concurrency cap is the real control and is deliberately small: it bounds
// how many cores a caller can occupy at once, which is what actually decides
// whether the node stays responsive. The rate limit is intentionally loose
// because it usually cannot tell callers apart — public nodes sit behind a
// tunnel or load balancer, so without TrustProxy every request appears to come
// from one address and the per-IP bucket degrades into a global throttle. A
// tight rate there would reject legitimate explorer and wallet traffic while
// adding nothing that the concurrency cap does not already provide.
const (
	defaultSimRatePerSec  = 100
	defaultSimBurst       = 200
	defaultSimMaxInFlight = 8
	defaultSimTimeout     = 5 * time.Second
)

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
		allowPlaintextP2P: cfg.AllowPlaintextP2P,
		addr:              cfg.ListenAddr,
		stateDB:           stateDB,
		executor:          executor,
		pool:              pool,
		broadcaster:       broadcaster,
		x402proc:          x402.NewProcessor(stateDB),
		scheduler:         scheduler,
		validatorPK:       cfg.ValidatorPK,
		peerCount:         cfg.PeerCount,
		writeAuth:         NewWriteAuth(cfg.AuthToken),
		cors:              NewCORSPolicy(cfg.CORSOrigins),
		submitLimiter:     NewRateLimiter(cfg.SubmitRatePerSec, cfg.SubmitBurst, cfg.TrustProxy),
		simGuard:          newSimGuard(cfg),
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
	// Registered as an explicit refusal rather than omitted, so a legacy peer is
	// told why instead of getting a 404 that reads like a broken route.
	if !s.allowPlaintextP2P {
		r.HandleFunc("/block", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusGone,
				"this node accepts blocks only over the sealed ML-KEM transport; "+
					"start it with --allow-plaintext-p2p to accept cleartext pushes")
		}).Methods(http.MethodPost)
	} else {
		r.Handle("/block", s.writeAuth.Middleware(http.HandlerFunc(s.handleIncomingBlock))).
			Methods(http.MethodPost)
	}

	return r
}

// limited wraps a handler with the public-submission rate limiter.
func (s *Server) limited(h http.HandlerFunc) http.Handler {
	return s.submitLimiter.Middleware(h)
}

// newSimGuard builds the simulation-plane guard, substituting defaults for
// unset config values so a caller cannot leave these routes unmetered by
// omission.
func newSimGuard(cfg Config) *ExecGuard {
	rate, burst := cfg.SimRatePerSec, cfg.SimBurst
	if rate <= 0 {
		rate, burst = defaultSimRatePerSec, defaultSimBurst
	}
	if burst <= 0 {
		burst = rate * 2
	}
	inFlight := cfg.SimMaxInFlight
	if inFlight <= 0 {
		inFlight = defaultSimMaxInFlight
	}
	timeout := cfg.SimTimeout
	if timeout <= 0 {
		timeout = defaultSimTimeout
	}
	return NewExecGuard(rate, burst, inFlight, timeout, cfg.TrustProxy)
}

// SimGuard exposes the simulation-plane guard so /call, /estimate and /logs —
// registered on the outer router — enforce the same limits.
func (s *Server) SimGuard() *ExecGuard { return s.simGuard }

// WriteAuth exposes the write-plane guard so routes registered outside this
// file (explorer peer announce) can enforce the same policy.
func (s *Server) WriteAuth() *WriteAuth { return s.writeAuth }

// CORS exposes the origin policy so an outer router can apply the same rules.
func (s *Server) CORS() *CORSPolicy { return s.cors }

// ── Handlers ───────────────────────────────────────────────────────────────

// preflightModuleCall is vm.PreflightModuleCall behind a variable so a test
// can count how often /tx/submit reaches it (it must never run on an envelope
// the pool would refuse on size or structure).
var preflightModuleCall = vm.PreflightModuleCall

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

	// The pool's cheap structural checks and Data cap come FIRST: the module
	// preflight below parses tx.Data (twice for an exchange match), so an
	// unauthenticated 512 KiB body must be refused on size before any of that
	// work is done on it. Same check, same wording as pool.Add.
	if err := mempool.ValidateEnvelope(&tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
		return
	}

	// Admission preflight for native-module calls: the HARD failure classes
	// (bad envelope, unknown module, non-write action, fork not active) can
	// never apply, and a tx that can never apply is dropped by the producer
	// while pinning the sender's nonce in the pool. Checked against the
	// executor's live schedule at the height the tx would earliest land in,
	// head+1. Local and non-consensus: the executor re-judges every call.
	if tx.Type == types.TxCall && tx.To == vm.ModuleRouterAddress {
		head, _ := s.executor.LatestHeight()
		mc, err := preflightModuleCall(tx.Data, s.executor.Forks(), head+1)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
			return
		}
		// An exchange.match from anyone but an operator of the instance can
		// only ever be a failed receipt; refusing it here keeps mempool and
		// block space for settlements that can settle. Local, like the rest.
		if err := vm.PreflightExchangeMatch(s.stateDB, mc, tx.From); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
			return
		}
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
	writeJSON(w, http.StatusOK, blockResponse(r, b))
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
	writeJSON(w, http.StatusOK, blockResponse(r, b))
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
	writeJSON(w, http.StatusOK, blockResponse(r, b))
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
	status := map[string]any{
		"chain_id":     types.ChainID,
		"height":       height,
		"mempool_size": s.pool.Size(),
		"peer_count":   peerCount,
		"node_version": buildinfo.Version,
		"commit":       buildinfo.Commit,
		"image_tag":    buildinfo.ImageTag,
		"timestamp":    time.Now().Unix(),
	}
	// The rule-change schedule is reported so an operator can confirm every
	// node agrees on it BEFORE a fork height arrives: nodes disagreeing about an
	// activation height do not fail at startup — they fail at the height, by
	// splitting. It is read from the executor at request time, never cached
	// here, so a schedule installed after this server was built (a follower's
	// arrives with the peer-served genesis, minutes after boot) is visible
	// without a restart. Only present on a chain that schedules one, so the
	// response shape is unchanged for chains that do not.
	if schedule := s.executor.Forks(); len(schedule) > 0 {
		status["forks"] = schedule
	}
	writeJSON(w, http.StatusOK, status)
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
