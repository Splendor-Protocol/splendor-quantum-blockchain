// Package crypto provides all cryptographic primitives for Splendor Quantum Chain.
// Every operation uses post-quantum algorithms only.
//
// Signing:     ML-DSA-65 (NIST FIPS 204, CIRCL sign/mldsa/mldsa65). This is the
//              standardized post-quantum lattice signature. Signatures use an
//              empty context string and deterministic (hedged=false) signing so a
//              given (key, message) always yields the same signature — required
//              for consensus reproducibility. NOT interoperable with the earlier
//              round-3 CRYSTALS-Dilithium mode3 (a fresh genesis is required).
// Hashing:     SHA-3-256 (NIST FIPS 202)
// Key storage: AES-256-GCM + Argon2id
// NO ECDSA. NO SHA-256. NO KECCAK.
package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	mldsa65 "github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/sha3"
)

// sigCtx is the ML-DSA context string. Empty and constant across the whole
// chain — every signer and verifier must use the identical context or
// signatures will not validate.
var sigCtx []byte = nil

const (
	// ML-DSA-65 (NIST FIPS 204) — sizes in bytes
	PublicKeySize  = mldsa65.PublicKeySize  // 1952
	PrivateKeySize = mldsa65.PrivateKeySize // 4032
	SignatureSize  = mldsa65.SignatureSize  // 3309

	// SQ addresses are 32 bytes derived from SHA3-256(public_key)
	AddressSize = 32

	// AddressPrefix identifies Splendor Quantum Chain addresses
	AddressPrefix = "SQ:"
)

// PublicKey is an ML-DSA-65 (FIPS-204) public key.
type PublicKey struct {
	k *mldsa65.PublicKey
}

// PrivateKey is an ML-DSA-65 (FIPS-204) private key.
type PrivateKey struct {
	k *mldsa65.PrivateKey
}

// Address is a 32-byte quantum-safe blockchain address.
type Address [AddressSize]byte

// GenerateKeypair generates a new ML-DSA-65 (FIPS-204) keypair.
func GenerateKeypair() (*PublicKey, *PrivateKey, error) {
	pub, priv, err := mldsa65.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("dilithium keygen: %w", err)
	}
	return &PublicKey{k: pub}, &PrivateKey{k: priv}, nil
}

// PublicKeyFromBytes deserialises 1952 bytes into a public key.
func PublicKeyFromBytes(b []byte) (*PublicKey, error) {
	if len(b) != PublicKeySize {
		return nil, fmt.Errorf("pubkey: want %d bytes, got %d", PublicKeySize, len(b))
	}
	pub := new(mldsa65.PublicKey)
	if err := pub.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("pubkey unmarshal: %w", err)
	}
	return &PublicKey{k: pub}, nil
}

// PrivateKeyFromBytes deserialises 4032 bytes into a private key.
func PrivateKeyFromBytes(b []byte) (*PrivateKey, error) {
	if len(b) != PrivateKeySize {
		return nil, fmt.Errorf("privkey: want %d bytes, got %d", PrivateKeySize, len(b))
	}
	priv := new(mldsa65.PrivateKey)
	if err := priv.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("privkey unmarshal: %w", err)
	}
	return &PrivateKey{k: priv}, nil
}

// Bytes serialises the public key to 1952 bytes.
func (pk *PublicKey) Bytes() []byte {
	b, _ := pk.k.MarshalBinary()
	return b
}

// Bytes serialises the private key to 4032 bytes.
func (sk *PrivateKey) Bytes() []byte {
	b, _ := sk.k.MarshalBinary()
	return b
}

// Zeroize overwrites ML-DSA private key material then drops the pointer.
// Best-effort: replaces key bytes with a deterministic zero-seed key, then nils.
func (sk *PrivateKey) Zeroize() {
	if sk == nil || sk.k == nil {
		return
	}
	var zeroSeed [32]byte
	_, junk := mldsa65.NewKeyFromSeed(&zeroSeed)
	*sk.k = *junk
	sk.k = nil
}

// Public derives the public key from a private key.
func (sk *PrivateKey) Public() *PublicKey {
	pub := sk.k.Public().(*mldsa65.PublicKey)
	return &PublicKey{k: pub}
}

// Sign produces a 3309-byte ML-DSA-65 (FIPS-204) signature over the message.
// Signing is DETERMINISTIC (randomized=false) with the chain-wide empty context,
// so a given (key, message) always yields the same signature — consensus nodes
// must reproduce validator signatures bit-for-bit.
func Sign(sk *PrivateKey, message []byte) ([]byte, error) {
	if sk == nil {
		return nil, errors.New("nil private key")
	}
	sig := make([]byte, SignatureSize)
	if err := mldsa65.SignTo(sk.k, message, sigCtx, false, sig); err != nil {
		return nil, fmt.Errorf("ml-dsa sign: %w", err)
	}
	return sig, nil
}

// Verify checks an ML-DSA-65 (FIPS-204) signature over the message.
func Verify(pk *PublicKey, message []byte, sig []byte) bool {
	if pk == nil || len(sig) != SignatureSize {
		return false
	}
	return mldsa65.Verify(pk.k, message, sigCtx, sig)
}

// DeriveAddress computes SHA3-256(public_key_bytes) as a 32-byte address.
func DeriveAddress(pk *PublicKey) Address {
	b := pk.Bytes()
	hash := sha3.Sum256(b)
	var addr Address
	copy(addr[:], hash[:])
	return addr
}

// Hash32 returns SHA3-256 of data as a [32]byte.
func Hash32(data []byte) [32]byte {
	return sha3.Sum256(data)
}

// HashBytes returns SHA3-256 of data as a byte slice.
func HashBytes(data []byte) []byte {
	h := sha3.Sum256(data)
	return h[:]
}

// String returns "SQ:<hex>" for the address.
func (a Address) String() string {
	return AddressPrefix + hex.EncodeToString(a[:])
}

// AddressFromString parses "SQ:<hex>" into an Address.
func AddressFromString(s string) (Address, error) {
	if len(s) <= len(AddressPrefix) {
		return Address{}, errors.New("address too short")
	}
	if s[:len(AddressPrefix)] != AddressPrefix {
		return Address{}, fmt.Errorf("missing SQ: prefix in %q", s)
	}
	b, err := hex.DecodeString(s[len(AddressPrefix):])
	if err != nil {
		return Address{}, fmt.Errorf("invalid hex: %w", err)
	}
	if len(b) != AddressSize {
		return Address{}, fmt.Errorf("wrong length: got %d, want %d", len(b), AddressSize)
	}
	var addr Address
	copy(addr[:], b)
	return addr, nil
}

// IsZero returns true if the address is all zeros.
func (a Address) IsZero() bool {
	for _, b := range a {
		if b != 0 {
			return false
		}
	}
	return true
}
