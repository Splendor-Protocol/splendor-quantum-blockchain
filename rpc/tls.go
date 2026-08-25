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
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		// Offer the hybrid group first. X25519 stays as a fallback so a client
		// without ML-KEM support can still connect (it simply loses the PQ
		// property); drop it to make the hybrid group mandatory.
		CurvePreferences: []tls.CurveID{
			tls.X25519MLKEM768,
			tls.X25519,
		},
	}, nil
}

// LogTLSHandshake records whether a completed handshake actually used the
// post-quantum hybrid group. Offering a curve does not guarantee the peer chose
// it, so this is the only way to know the connection is PQ-protected in
// practice rather than in configuration.
func LogTLSHandshake(state tls.ConnectionState) {
	pq := state.CurveID == tls.X25519MLKEM768
	log.Debug().
		Str("curve", curveName(state.CurveID)).
		Bool("post_quantum", pq).
		Msg("TLS handshake complete")
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
