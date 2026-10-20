package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/sign/slhdsa"
)

// SLH-DSA-SHA2-128s (FIPS 205) sizes.
const (
	SLHPublicKeySize  = 32
	SLHPrivateKeySize = 64
	SLHSignatureSize  = 7856
)

// SLHPublicKey is an SLH-DSA-SHA2-128s public key.
type SLHPublicKey struct {
	k slhdsa.PublicKey
}

// SLHPrivateKey is an SLH-DSA-SHA2-128s private key.
type SLHPrivateKey struct {
	k slhdsa.PrivateKey
}

// GenerateSLHKeypair returns a random SLH-DSA-SHA2-128s key pair.
func GenerateSLHKeypair() (*SLHPublicKey, *SLHPrivateKey, error) {
	pub, priv, err := slhdsa.GenerateKey(rand.Reader, slhdsa.SHA2_128s)
	if err != nil {
		return nil, nil, fmt.Errorf("slh-dsa keygen: %w", err)
	}
	return &SLHPublicKey{k: pub}, &SLHPrivateKey{k: priv}, nil
}

// SLHPublicKeyFromBytes decodes a 32-byte SLH-DSA-SHA2-128s public key.
func SLHPublicKeyFromBytes(b []byte) (*SLHPublicKey, error) {
	if len(b) != SLHPublicKeySize {
		return nil, fmt.Errorf("slh-dsa pubkey: got %d bytes, want %d", len(b), SLHPublicKeySize)
	}
	var pub slhdsa.PublicKey
	pub.ID = slhdsa.SHA2_128s
	if err := pub.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("slh-dsa pubkey: %w", err)
	}
	return &SLHPublicKey{k: pub}, nil
}

// Bytes serialises the public key.
func (pk *SLHPublicKey) Bytes() []byte {
	b, err := pk.k.MarshalBinary()
	if err != nil {
		return nil
	}
	return b
}

// SLHPrivateKeyFromBytes decodes an SLH-DSA-SHA2-128s private key.
func SLHPrivateKeyFromBytes(b []byte) (*SLHPrivateKey, error) {
	if len(b) != SLHPrivateKeySize {
		return nil, fmt.Errorf("slh-dsa privkey: got %d bytes, want %d", len(b), SLHPrivateKeySize)
	}
	var priv slhdsa.PrivateKey
	priv.ID = slhdsa.SHA2_128s
	if err := priv.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("slh-dsa privkey: %w", err)
	}
	return &SLHPrivateKey{k: priv}, nil
}

// Bytes serialises the private key.
func (sk *SLHPrivateKey) Bytes() []byte {
	b, err := sk.k.MarshalBinary()
	if err != nil {
		return nil
	}
	return b
}

// Public derives the public key.
func (sk *SLHPrivateKey) Public() *SLHPublicKey {
	pub := sk.k.PublicKey()
	return &SLHPublicKey{k: pub}
}

// SignSLH produces a deterministic SLH-DSA-SHA2-128s signature under ctx.
func SignSLH(sk *SLHPrivateKey, message []byte, ctx []byte) ([]byte, error) {
	if sk == nil {
		return nil, errors.New("nil slh-dsa private key")
	}
	if len(ctx) == 0 {
		return nil, errors.New("empty signing context: every signature must be domain-separated")
	}
	if len(ctx) > maxContextSize {
		return nil, fmt.Errorf("signing context too long: %d bytes (max %d)", len(ctx), maxContextSize)
	}
	sig, err := slhdsa.SignDeterministic(&sk.k, slhdsa.NewMessage(message), ctx)
	if err != nil {
		return nil, fmt.Errorf("slh-dsa sign: %w", err)
	}
	return sig, nil
}

// VerifySLH checks an SLH-DSA-SHA2-128s signature under ctx.
func VerifySLH(pk *SLHPublicKey, message []byte, ctx []byte, sig []byte) bool {
	if pk == nil || len(sig) != SLHSignatureSize {
		return false
	}
	if len(ctx) == 0 || len(ctx) > maxContextSize {
		return false
	}
	return slhdsa.Verify(&pk.k, slhdsa.NewMessage(message), sig, ctx)
}
