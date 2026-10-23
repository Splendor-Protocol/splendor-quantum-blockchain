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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
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
	// trustedProducerKey and lookupProducerKey authenticate pushed blocks on
	// the cleartext ingest route; see Config.
	trustedProducerKey func() []byte
	lookupProducerKey  network.ProducerKeyLookup
	// consensusMode reports the live producer path; see Config.
	consensusMode func() string
	// relayTx passes a transaction this node admitted to its peers; see Config.
	relayTx    func(tx *types.Transaction)
	router     *mux.Router
	httpServer *http.Server
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
	// maxRequestBody caps the size of any single JSON-RPC request body.
	// Without it, a client can send a multi-megabyte payload and OOM the node
	// while json.Decoder reads it into memory. 4 MiB is plenty for any valid
	// transaction or X402 settlement envelope.
	maxRequestBody int64
	// simGuard meters the anonymous VM-execution routes (/call, /estimate) and
	// the receipt scan behind /logs.
	simGuard *ExecGuard
	// allowPlaintextP2P registers the cleartext peer routes. Off = sealed only.
	allowPlaintextP2P bool
	// lookupReceipt returns an indexed receipt when this node has a tx index.
	// Used by /x402/settle to distinguish confirmed payment from a queued tx.
	lookupReceipt func(hash [32]byte) (*types.Receipt, error)
}

// Config holds server configuration.
type Config struct {
	ListenAddr  string     // e.g. "127.0.0.1:8080"
	ValidatorPK []byte     // ML-DSA public key this node reports as /status.local_validator
	PeerCount   func() int // optional: returns live peer count
	AuthToken   string     // shared secret required on write-plane routes

	// TrustedProducerKey returns the genesis validator key pushed blocks are
	// authenticated against — read at call time, because a node that pins
	// only --genesis-hash learns the key from block 0 after start-up. nil
	// falls back to ValidatorPK (the pre-existing behaviour).
	TrustedProducerKey func() []byte
	// LookupProducerKey resolves an active validator's key for a pushed block
	// sealed by someone other than the genesis validator. nil keeps the
	// single-key rule.
	LookupProducerKey network.ProducerKeyLookup

	// ConsensusMode reports the producer path this node is ACTUALLY on right
	// now, read at request time: ConsensusModeSingle, ConsensusModeBFT or
	// ConsensusModeFollower. nil reports ConsensusModeFollower. This is
	// distinct from /status.multi_validator, which is the scheduled rule bit
	// and says nothing about the engine that sealed the head.
	ConsensusMode func() string

	// RelayTx hands a transaction this node has just admitted to its peers.
	//
	// Only the leader of a height puts transactions into a block, and nothing
	// carried one mempool's contents to another: a transaction submitted to any
	// node that is not the current leader — an RPC node, or the validator that
	// happens not to be leading — was never included. nil disables the relay,
	// which is the correct setting on the ingest path itself, so a relayed
	// transaction is never relayed on.
	RelayTx func(tx *types.Transaction)

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
	// MaxRequestBodyBytes caps any single JSON-RPC request body. Zero means
	// a sane default of 4 MiB.
	MaxRequestBodyBytes int

	// SimRatePerSec / SimBurst / SimMaxInFlight / SimTimeout bound the routes
	// that execute the VM or scan the receipt index for an anonymous caller
	// (/call, /estimate, /logs). Zero values fall back to the defaults below.
	SimRatePerSec  float64
	SimBurst       float64
	SimMaxInFlight int
	SimTimeout     time.Duration

	// LookupReceipt returns an indexed receipt by transaction hash. Optional;
	// without it /x402/settle can still report submitted vs confirmed via the
	// X402 nonce registry after the payment has been applied.
	LookupReceipt func(hash [32]byte) (*types.Receipt, error)
}

// Values of /status.consensus_mode.
const (
	// ConsensusModeSingle: this node seals blocks on the single-validator path
	// (no certificate; the block predates multi_validator).
	ConsensusModeSingle = "single"
	// ConsensusModeBFT: this node proposes and commits through the BFT engine
	// and attaches a quorum certificate to every block it commits.
	ConsensusModeBFT = "bft"
	// ConsensusModeFollower: this node produces nothing; it applies blocks
	// pushed or pulled from peers.
	ConsensusModeFollower = "follower"
)

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
		allowPlaintextP2P:  cfg.AllowPlaintextP2P,
		addr:               cfg.ListenAddr,
		stateDB:            stateDB,
		executor:           executor,
		pool:               pool,
		broadcaster:        broadcaster,
		x402proc:           x402.NewProcessor(stateDB),
		scheduler:          scheduler,
		validatorPK:        cfg.ValidatorPK,
		trustedProducerKey: cfg.TrustedProducerKey,
		lookupProducerKey:  cfg.LookupProducerKey,
		consensusMode:      cfg.ConsensusMode,
		relayTx:            cfg.RelayTx,
		peerCount:          cfg.PeerCount,
		writeAuth:          NewWriteAuth(cfg.AuthToken),
		cors:               NewCORSPolicy(cfg.CORSOrigins),
		submitLimiter:      NewRateLimiter(cfg.SubmitRatePerSec, cfg.SubmitBurst, cfg.TrustProxy),
		maxRequestBody: func() int64 {
			if cfg.MaxRequestBodyBytes > 0 {
				return int64(cfg.MaxRequestBodyBytes)
			}
			return 4 * 1024 * 1024
		}(),
		simGuard:      newSimGuard(cfg),
		lookupReceipt: cfg.LookupReceipt,
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
	r.Handle("/verify/check", s.limited(s.handleVerifyCheck)).Methods(http.MethodPost)
	r.Handle("/verify/attest", s.limited(s.handleVerifyAttest)).Methods(http.MethodPost)
	r.Handle("/verify/settle", s.limited(s.handleVerifySettle)).Methods(http.MethodPost)
	r.HandleFunc("/verify/proof/{id}", s.handleVerifyProof).Methods(http.MethodGet)

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

// limited wraps a handler with the public-submission rate limiter and a
// request-body size cap so an oversized payload cannot OOM the node.
func (s *Server) limited(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > s.maxRequestBody && s.maxRequestBody > 0 {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body too large: %d bytes (max %d)", r.ContentLength, s.maxRequestBody))
			return
		}
		s.submitLimiter.Middleware(h).ServeHTTP(w, r)
	})
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

	// amount/fee may arrive as decimal strings (JS cannot carry >2^53 in a
	// Number); rewrite them to the numeric form the core types decode.
	body, err = normalizeWireAmounts(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid transaction JSON: %v", err))
		return
	}

	var tx types.Transaction
	if err := json.Unmarshal(body, &tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid transaction JSON: %v", err))
		return
	}
	s.admitTx(w, &tx)
}

func (s *Server) admitTx(w http.ResponseWriter, tx *types.Transaction) {
	if err := mempool.ValidateEnvelope(tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
		return
	}
	if tx.Type == types.TxCall && tx.To == vm.ModuleRouterAddress {
		head, _ := s.executor.LatestHeight()
		if _, err := preflightModuleCall(tx.Data, s.executor.Forks(), head+1); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
			return
		}
	}
	if err := s.pool.Add(tx); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rejected: %v", err))
		return
	}
	// Admitted here, so every peer should see it: this node may not be the one
	// that leads the next height. The relay runs after Add, so only a
	// transaction this node itself accepted is ever passed on.
	if s.relayTx != nil {
		s.relayTx(tx)
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

	trusted := s.validatorPK
	if s.trustedProducerKey != nil {
		trusted = s.trustedProducerKey()
	}
	err = network.HandleIncomingBlock(body, trusted, s.lookupProducerKey, func(b *types.Block) error {
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
		"node_version": buildinfo.Version(),
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
	schedule := s.executor.Forks()
	if len(schedule) > 0 {
		status["forks"] = schedule
	}
	status["min_fee"] = s.pool.MinFeeNow()
	// min_fee is an integer in the smallest unit. The unit is stated because
	// "1000" has been read as 0.001 SPLD; it is 1000 × 10^-18 SPLD.
	status["fee_unit"] = "smallest_spld"
	status["decimals"] = types.Decimals
	// multi_validator is the SCHEDULE bit — whether the rule applies at the
	// head. It is kept for compatibility. It does not say which engine sealed
	// the head or whether a certificate exists; the fields below do.
	status["multi_validator"] = schedule.Active(forks.MultiValidator, height)
	reg := vm.NewStakingContract(s.stateDB, vm.DeployStaking(nil))
	if list, err := reg.ListActive(); err == nil {
		status["validator_set_size"] = len(list)
	}
	if len(s.validatorPK) > 0 {
		if pub, err := qcrypto.PublicKeyFromBytes(s.validatorPK); err == nil {
			status["local_validator"] = qcrypto.DeriveAddress(pub).String()
		}
	}

	// The engine this process is actually running, read now.
	mode := ConsensusModeFollower
	if s.consensusMode != nil {
		mode = s.consensusMode()
	}
	status["consensus_mode"] = mode
	// The head's certificate, judged against the committed validator set.
	// "healthy HTTP", "producing blocks", "registered validators" and "a
	// valid certificate on the head" are four different properties; this
	// is the fourth one, so an operator no longer has to infer it from the
	// schedule bit.
	hc := s.executor.HeadConsensus()
	status["head_qc_present"] = hc.QCPresent
	status["head_qc_valid"] = hc.QCValid
	status["head_qc_votes"] = hc.QCVotes
	status["head_quorum_required"] = hc.QuorumRequired
	status["validator_set_height"] = hc.ValidatorSetHeight
	if hc.Reason != "" {
		status["head_qc_reason"] = hc.Reason
	}
	// The identity of THIS chain, not just its id string: block 0's hash is
	// what a --genesis-hash pin must match, and it changes on every reset even
	// when chain_id does not.
	if g, err := s.executor.GetBlockByHeight(0); err == nil && g != nil {
		status["genesis_hash"] = hex.EncodeToString(g.Hash[:])
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleX402Verify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // bound the public verify body
	var req struct {
		Requirements x402.PaymentRequirements `json:"requirements"`
		Payload      x402.PaymentPayload      `json:"payload"`
	}
	if err := decodeWireJSON(r.Body, &req); err != nil {
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
	if err := decodeWireJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	result := s.x402proc.Verify(&req.Requirements, &req.Payload)
	if !result.Valid && !result.NonceUsed {
		writeJSON(w, http.StatusPaymentRequired, x402.SettleResult{
			Status: x402.SettleFailed,
			Error:  result.Reason,
		})
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

	hash, err := req.SettlementTx.FullHash()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash settlement tx")
		return
	}
	hashHex := fmt.Sprintf("%x", hash[:])

	// Idempotent confirmed path: a successful receipt for THIS transaction is
	// proof the payment settled. A queued tx is never described as confirmed.
	if rec := s.settlementReceipt(hash); rec != nil {
		if rec.Status == types.TxStatusSuccess {
			writeJSON(w, http.StatusOK, x402.SettleResult{
				Success: true,
				Status:  x402.SettleConfirmed,
				TxHash:  hashHex,
			})
			return
		}
		errMsg := rec.Error
		if errMsg == "" {
			errMsg = "settlement transaction failed"
		}
		writeJSON(w, http.StatusBadRequest, x402.SettleResult{
			Status: x402.SettleFailed,
			TxHash: hashHex,
			Error:  errMsg,
		})
		return
	}

	// A consumed nonce alone is NOT proof of which payment happened. Confirm on
	// the used-nonce path only when the on-chain settlement record binds the
	// nonce to exactly this payment — same payer, payee, amount and chain. This
	// closes the bypass where a used payer/nonce was paired with different
	// claimed details and still took the confirmed fallback.
	//
	// result.NonceUsed is only set after the payment signature verified, so a
	// fabricated retry cannot reach this branch; and SettlementMatch re-checks
	// the settled details against the authoritative record.
	if result.NonceUsed {
		ok, serr := s.x402proc.SettlementMatch(&req.Payload)
		if serr != nil {
			writeJSON(w, http.StatusInternalServerError, x402.SettleResult{
				Status: x402.SettleFailed,
				Error:  fmt.Sprintf("settlement record error: %v", serr),
			})
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, x402.SettleResult{
				Success: true,
				Status:  x402.SettleConfirmed,
				TxHash:  hashHex,
			})
			return
		}
		writeJSON(w, http.StatusPaymentRequired, x402.SettleResult{
			Status: x402.SettleFailed,
			Error:  "payment nonce already used by a different payment",
		})
		return
	}
	if !result.Valid {
		writeJSON(w, http.StatusPaymentRequired, x402.SettleResult{
			Status: x402.SettleFailed,
			Error:  result.Reason,
		})
		return
	}

	// The mempool verifies the payer's signature, account nonce, fee floor, and
	// balance. The executor re-checks the X402 replay registry at block time.
	if err := s.pool.Add(req.SettlementTx); err != nil {
		if settlementAlreadyQueued(err) {
			writeJSON(w, http.StatusAccepted, x402.SettleResult{
				Success: false,
				Status:  x402.SettleSubmitted,
				TxHash:  hashHex,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, x402.SettleResult{
			Status: x402.SettleFailed,
			Error:  fmt.Sprintf("settlement rejected: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusAccepted, x402.SettleResult{
		Success: false,
		Status:  x402.SettleSubmitted,
		TxHash:  hashHex,
	})
}

func (s *Server) settlementReceipt(hash [32]byte) *types.Receipt {
	if s == nil || s.lookupReceipt == nil {
		return nil
	}
	rec, err := s.lookupReceipt(hash)
	if err != nil || rec == nil {
		return nil
	}
	return rec
}

func settlementAlreadyQueued(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, types.ErrDuplicateTx) {
		return true
	}
	return strings.Contains(err.Error(), "duplicate pending nonce")
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
