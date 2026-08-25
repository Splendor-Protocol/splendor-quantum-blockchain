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

// P2PConfig wires the sealed transport to the node's handlers.
type P2PConfig struct {
	// Identity is this node's ML-DSA + ML-KEM key material.
	Identity *network.NodeIdentity
	// Token is the write-plane secret expected inside sealed requests.
	Token string
	// HandleBlock applies a received block body (raw JSON).
	HandleBlock func(body []byte) error
	// HandleAnnounce processes a peer announcement body (raw JSON).
	HandleAnnounce func(body []byte) error
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

	r.HandleFunc(network.PathSealed, func(w http.ResponseWriter, r *http.Request) {
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

		// Authenticate AFTER decryption: the token is inside the envelope.
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
