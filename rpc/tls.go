package rpc

import (
	"crypto/tls"
	"fmt"

	"github.com/rs/zerolog/log"
)

// PQTLSConfig builds a TLS 1.3 configuration whose key exchange is
// post-quantum hybrid.
//
// Why this matters: ML-DSA protects transactions and blocks, but everything
// those signatures travel over — RPC, WebSocket, metrics — is ordinary TLS. An
// adversary recording that traffic today can decrypt it once a CRQC exists
// ("harvest now, decrypt later"), exposing pre-broadcast transactions, wallet
// addresses, and operator tokens. X25519MLKEM768 is a hybrid group: the shared
// secret is safe unless BOTH X25519 and ML-KEM-768 are broken, so this is never
// weaker than classical X25519.
//
// TLS 1.3 is required, since the hybrid group only exists there.
func PQTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	return PQTLSConfigWithPolicy(certFile, keyFile, false)
}

// PQTLSConfigWithPolicy builds the same configuration, but requireHybrid decides
// what happens when a client cannot do ML-KEM.
//
// Offering a group is not the same as using one, and that gap is the whole
// problem: a deployment can advertise post-quantum transport, negotiate
// classical X25519 with every real client, and look identical in its logs. With
// requireHybrid the classical fallback is removed, so such a client fails to
// connect instead of quietly downgrading -- which is the only configuration in
// which "the transport is post-quantum" is a statement about what happened
// rather than about what was offered.
func PQTLSConfigWithPolicy(certFile, keyFile string, requireHybrid bool) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}
	curves := []tls.CurveID{tls.X25519MLKEM768, tls.X25519}
	if requireHybrid {
		curves = []tls.CurveID{tls.X25519MLKEM768}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		// Hybrid first. X25519 stays as a fallback so a client without ML-KEM can
		// still connect, losing the PQ property; --tls-require-hybrid drops it.
		CurvePreferences: curves,
		// Every completed handshake is reported, so an operator can see what was
		// actually negotiated rather than what was configured.
		VerifyConnection: func(state tls.ConnectionState) error {
			LogTLSHandshake(state)
			return nil
		},
	}, nil
}

// LogTLSHandshake records whether a completed handshake actually used the
// post-quantum hybrid group. Offering a curve does not guarantee the peer chose
// it, so this is the only way to know the connection is PQ-protected in
// practice rather than in configuration.
func LogTLSHandshake(state tls.ConnectionState) {
	if state.CurveID == tls.X25519MLKEM768 {
		log.Debug().
			Str("curve", curveName(state.CurveID)).
			Bool("post_quantum", true).
			Msg("TLS handshake complete")
		return
	}
	// A classical handshake is the case worth seeing. At Debug it was invisible
	// in every real deployment, which meant a node could serve nothing but
	// classical TLS while its startup banner claimed post-quantum key exchange.
	// Warn is the level at which that contradiction becomes findable.
	log.Warn().
		Str("curve", curveName(state.CurveID)).
		Bool("post_quantum", false).
		Msg("TLS handshake completed WITHOUT the post-quantum hybrid group — " +
			"this connection is classical; use --tls-require-hybrid to refuse instead")
}

func curveName(id tls.CurveID) string {
	switch id {
	case tls.X25519MLKEM768:
		return "X25519MLKEM768"
	case tls.X25519:
		return "X25519"
	case tls.CurveP256:
		return "P-256"
	case tls.CurveP384:
		return "P-384"
	case tls.CurveP521:
		return "P-521"
	default:
		return fmt.Sprintf("0x%04x", uint16(id))
	}
}
