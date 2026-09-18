package network

import (
	"crypto/rand"
	"testing"
)

// The wire bound must cover a maximal sealed reply, or the largest symbol
// proofs are truncated before they can be opened.
func TestSealedReplyBoundCoversAMaximalEnvelope(t *testing.T) {
	for _, plain := range []int{0, 1, maxObjectManifestReplyBytes, maxObjectSymbolReplyBytes} {
		env := &Envelope{
			KEMCiphertext: make([]byte, 1088),
			Nonce:         make([]byte, 12),
			Ciphertext:    make([]byte, plain+16),
			Timestamp:     1<<62 - 1,
		}
		rand.Read(env.KEMCiphertext)
		rand.Read(env.Nonce)
		rand.Read(env.Ciphertext)
		wire, err := MarshalEnvelope(env)
		if err != nil {
			t.Fatal(err)
		}
		if got, bound := int64(len(wire)), sealedReplyBound(plain); got > bound {
			t.Fatalf("plain %d: sealed wire is %d bytes, bound is %d", plain, got, bound)
		}
	}
}
