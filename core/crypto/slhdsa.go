package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/sign/slhdsa"
	"golang.org/x/crypto/sha3"
)

// SLH-DSA-SHA2-128s (FIPS 205) sizes.
//
// The "s" (small) parameter set is the one SQVL freezes into its wire format:
// a 32-byte public key and a 7,856-byte signature. Signing is the expensive
// half — a few hundred milliseconds natively, seconds in the browser WASM
// signer — and verification is cheap, which is the right way round for a
// chain: one signer pays once, every validator verifies. SHA2-128f would sign
// roughly ten times faster at 17,088 bytes per signature; moving to it would
// change SLHSignatureSize and therefore every stored instruction, so it is a
// decision to take before the first verify_v1 instance is deployed, not after.
const (
	SLHPublicKeySize  = 32
	SLHPrivateKeySize = 64
	SLHSignatureSize  = 7856
)

// slhKeygenDomain separates SLH-DSA key derivation from every other use of a
// 32-byte seed on this chain (the ML-DSA wallet seed in particular), so one
// seed never yields two related keys.
var slhKeygenDomain = []byte("SPLENDOR-QC-SLH-DSA-KEYGEN-v1")

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

// SLHKeypairFromSeed derives an SLH-DSA-SHA2-128s key pair deterministically
// from a 32-byte seed, so a wallet can hold one SLH identity across sessions
// without storing a second secret. The key material is read from
// SHAKE256(slhKeygenDomain ‖ seed); the same seed always yields the same key,
// and the ML-DSA key derived from that seed is unrelated to it.
func SLHKeypairFromSeed(seed [SeedSize]byte) (*SLHPublicKey, *SLHPrivateKey, error) {
	xof := sha3.NewShake256()
	xof.Write(slhKeygenDomain)
	xof.Write(seed[:])
	pub, priv, err := slhdsa.GenerateKey(xof, slhdsa.SHA2_128s)
	if err != nil {
		return nil, nil, fmt.Errorf("slh-dsa keygen from seed: %w", err)
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
// The same context rules as Sign apply: never empty, at most 255 bytes.
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

// VerifySLH checks an SLH-DSA-SHA2-128s signature under ctx. A signature of
// the wrong length is refused before any hashing.
func VerifySLH(pk *SLHPublicKey, message []byte, ctx []byte, sig []byte) bool {
	if pk == nil || len(sig) != SLHSignatureSize {
		return false
	}
	if len(ctx) == 0 || len(ctx) > maxContextSize {
		return false
	}
	return slhdsa.Verify(&pk.k, slhdsa.NewMessage(message), sig, ctx)
}
