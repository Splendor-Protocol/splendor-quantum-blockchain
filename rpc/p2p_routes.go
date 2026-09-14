package rpc

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// maxSealedBody bounds an encrypted envelope. The plaintext inside is a block,
// so allow the block wire limit plus room for base64 expansion and the KEM
// ciphertext.
const maxSealedBody = (block.MaxBlockWireBytes * 4 / 3) + (64 * 1024)

// defaultSealedMaxInFlight bounds how many sealed envelopes are being read and
// opened at once. The token that authenticates a sealed write lives INSIDE the
// envelope, so the ML-KEM decapsulation and the body read happen for anyone who
// can reach the port; without a cap, N parallel junk envelopes pin N cores and
// N block-sized buffers before a single byte is authenticated. A live cluster
// exchanges a handful of proposals, votes and blocks per height, so this is
// generous for real peers and cheap to shed for everyone else.
const defaultSealedMaxInFlight = 64

// P2PConfig wires the sealed transport to the node's handlers.
type P2PConfig struct {
	// Identity is this node's ML-DSA + ML-KEM key material.
	Identity *network.NodeIdentity
	// Token is the write-plane secret expected inside sealed requests.
	Token string
	// MaxInFlight caps concurrent sealed requests, counted from before the
	// body is read until the handler returns. 0 uses defaultSealedMaxInFlight;
	// a negative value disables the cap (tests only).
	MaxInFlight int
	// HandleBlock applies a received block body (raw JSON).
	HandleBlock func(body []byte) error
	// HandleAnnounce processes a peer announcement body (raw JSON).
	HandleAnnounce func(body []byte) error
	// HandleVote processes a sealed BFT vote body (raw JSON).
	HandleVote func(body []byte) error
	// HandleProposal processes a sealed block proposal (raw JSON).
	HandleProposal func(body []byte) error
	// HandleTx admits a transaction relayed by a peer (raw JSON). The
	// implementation must NOT relay it onwards: relaying is one hop.
	HandleTx func(body []byte) error
	// ServeBlock returns a block's JSON by height, for sealed catch-up sync.
	ServeBlock func(height uint64) ([]byte, error)
	// ServeLatestBlock returns the head block's JSON, for sealed catch-up sync.
	ServeLatestBlock func() ([]byte, error)
	// ServeStateSnapshot returns this node's committed state at its head as a
	// types.StateSnapshot JSON, for a peer joining a chain it cannot replay.
	// nil means the node serves none.
	ServeStateSnapshot func() ([]byte, error)
}

// RegisterP2PRoutes exposes the post-quantum sealed transport.
//
// GET  /p2p/identity — the node's signed ML-KEM encapsulation key.
// POST /p2p/sealed   — an encrypted write-plane request.
//
// Unlike the plaintext write plane, the token here is carried inside the
// encrypted envelope, so it is never observable on the wire.
func RegisterP2PRoutes(r *mux.Router, cfg P2PConfig) {
	if cfg.Identity == nil {
		log.Warn().Msg("p2p: no node identity configured — encrypted transport disabled")
		return
	}

	r.HandleFunc(network.PathIdentity, func(w http.ResponseWriter, _ *http.Request) {
		doc, err := cfg.Identity.Document()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "identity unavailable")
			return
		}
		writeJSON(w, http.StatusOK, doc)
	}).Methods(http.MethodGet)

	var slots chan struct{}
	switch {
	case cfg.MaxInFlight == 0:
		slots = make(chan struct{}, defaultSealedMaxInFlight)
	case cfg.MaxInFlight > 0:
		slots = make(chan struct{}, cfg.MaxInFlight)
	}

	r.HandleFunc(network.PathSealed, func(w http.ResponseWriter, r *http.Request) {
		// Take the slot before touching the body: reading and decrypting is
		// the cost this cap exists to bound. Shed rather than queue — a queued
		// request still holds its connection and goroutine.
		if slots != nil {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				log.Debug().Str("remote", r.RemoteAddr).Msg("p2p: sealed request shed, too many in flight")
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusServiceUnavailable, "node busy: too many sealed requests in flight")
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxSealedBody)
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "sealed body too large")
			return
		}

		env, err := network.UnmarshalEnvelope(wire)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		plaintext, err := cfg.Identity.Open(env)
		if err != nil {
			// Do not echo the reason: a caller who cannot decrypt should not
			// learn which key or window it missed.
			log.Debug().Err(err).Str("remote", r.RemoteAddr).Msg("p2p: envelope failed to open")
			writeError(w, http.StatusBadRequest, "envelope could not be opened")
			return
		}

		var req network.SealedRequest
		if err := json.Unmarshal(plaintext, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid sealed request")
			return
		}

		// Read kinds return chain data that is public once published, so they
		// are not token-gated: a syncing RPC node must be able to catch up
		// without being handed the validator's write secret. They are still
		// sealed, which is what gives the channel confidentiality.
		if network.IsReadKind(req.Kind) {
			serveSealedRead(w, r, cfg, &req)
			return
		}

		// AUTHORIZATION: Message-specific policies replace the monolithic token check.
		// Transactions validate their own signatures; consensus messages check
		// validator membership; admin operations require the cluster token.
		switch req.Kind {
		case network.KindTx:
			// Public transaction relay: any node can submit a validly-signed
			// transaction. The HandleTx implementation validates the signature,
			// nonce and fee; the relay will reject invalid transactions without
			// needing a shared secret. This enables public RPC nodes and
			// independent transaction submission.
			// NO TOKEN CHECK HERE - signature validation happens in HandleTx
		
		case network.KindVote, network.KindProposal:
			// Consensus messages: the signature inside the vote/proposal proves
			// it came from a validator. HandleVote/HandleProposal check the
			// signer is in the validator set for the applicable height.
			// NO TOKEN CHECK HERE - validator membership checked in handlers
		
		case network.KindBlock, network.KindAnnounce:
			// Block delivery and peer announcements: these are signed by their
			// producers and carry validator/peer proofs. For defense in depth
			// during rollout, retain the token check until the signatures are
			// independently verified in the handlers.
			// TEMPORARY TOKEN CHECK - will migrate to signature-based auth
			if cfg.Token == "" {
				writeError(w, http.StatusServiceUnavailable,
					"write plane disabled: this node has no --rpc-auth-token configured")
				return
			}
			if subtle.ConstantTimeCompare([]byte(req.Token), []byte(cfg.Token)) != 1 {
				log.Warn().Str("remote", r.RemoteAddr).Str("kind", req.Kind).
					Msg("p2p: sealed request carried an invalid write-plane token")
				writeError(w, http.StatusUnauthorized, "invalid write-plane token")
				return
			}
		
		default:
			// Unknown message kinds are rejected before any handler.
			writeError(w, http.StatusBadRequest, "unknown sealed request kind")
			return
		}

		var handleErr error
		switch req.Kind {
		case network.KindBlock:
			if cfg.HandleBlock == nil {
				writeError(w, http.StatusNotImplemented, "block ingest not available")
				return
			}
			handleErr = cfg.HandleBlock(req.Body)
		case network.KindAnnounce:
			if cfg.HandleAnnounce == nil {
				writeError(w, http.StatusNotImplemented, "peer announce not available")
				return
			}
			handleErr = cfg.HandleAnnounce(req.Body)
		case network.KindVote:
			if cfg.HandleVote == nil {
				writeError(w, http.StatusNotImplemented, "vote ingest not available")
				return
			}
			handleErr = cfg.HandleVote(req.Body)
		case network.KindProposal:
			if cfg.HandleProposal == nil {
				writeError(w, http.StatusNotImplemented, "proposal ingest not available")
				return
			}
			handleErr = cfg.HandleProposal(req.Body)
		case network.KindTx:
			if cfg.HandleTx == nil {
				writeError(w, http.StatusNotImplemented, "transaction relay not available")
				return
			}
			handleErr = cfg.HandleTx(req.Body)
		default:
			writeError(w, http.StatusBadRequest, "unknown sealed request kind")
			return
		}

		if handleErr != nil {
			writeError(w, http.StatusBadRequest, handleErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
	}).Methods(http.MethodPost)

	log.Info().Msg("p2p: encrypted transport enabled (ML-KEM-768 + ML-DSA-65 + AES-256-GCM)")
}

// serveSealedRead answers a sealed catch-up query, sealing the block back to
// the one-shot reply key the requester supplied.
func serveSealedRead(w http.ResponseWriter, r *http.Request, cfg P2PConfig, req *network.SealedRequest) {
	if len(req.ReplyKEM) == 0 {
		writeError(w, http.StatusBadRequest, "read request carried no reply key")
		return
	}

	var (
		payload []byte
		err     error
	)
	switch req.Kind {
	case network.KindGetBlock:
		if cfg.ServeBlock == nil {
			writeError(w, http.StatusNotImplemented, "sealed block reads not available")
			return
		}
		var q network.GetBlockRequest
		if jsonErr := json.Unmarshal(req.Body, &q); jsonErr != nil {
			writeError(w, http.StatusBadRequest, "invalid get_block body")
			return
		}
		payload, err = cfg.ServeBlock(q.Height)
	case network.KindGetLatest:
		if cfg.ServeLatestBlock == nil {
			writeError(w, http.StatusNotImplemented, "sealed latest-block reads not available")
			return
		}
		payload, err = cfg.ServeLatestBlock()
	case network.KindGetStateSnapshot:
		if cfg.ServeStateSnapshot == nil {
			writeError(w, http.StatusNotImplemented, "sealed state snapshot reads not available")
			return
		}
		payload, err = cfg.ServeStateSnapshot()
	default:
		writeError(w, http.StatusBadRequest, "unknown sealed read kind")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	replyEnv, err := network.SealToKEM(req.ReplyKEM, payload)
	if err != nil {
		log.Debug().Err(err).Str("remote", r.RemoteAddr).Msg("p2p: could not seal read reply")
		writeError(w, http.StatusBadRequest, "reply key rejected")
		return
	}
	wire, err := network.MarshalEnvelope(replyEnv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode reply")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wire)
}
