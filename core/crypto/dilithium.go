// Package crypto provides all cryptographic primitives for Splendor Quantum Chain.
// Every operation uses post-quantum algorithms only.
// Signing:     CRYSTALS-Dilithium Mode 3 (NIST FIPS 204 / ML-DSA-65)
// Hashing:     SHA-3-256 (NIST FIPS 202)
// Key storage: AES-256-GCM + Argon2id
// NO ECDSA. NO SHA-256. NO KECCAK.
package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
	"golang.org/x/crypto/sha3"
)

const (
	// Dilithium Mode 3 (NIST FIPS 204 ML-DSA-65) — sizes in bytes
	PublicKeySize  = mode3.PublicKeySize  // 1952
	PrivateKeySize = mode3.PrivateKeySize // 4032
	SignatureSize  = mode3.SignatureSize  // 3293

	// SQ addresses are 32 bytes derived from SHA3-256(public_key)
	AddressSize = 32

	// AddressPrefix identifies Splendor Quantum Chain addresses
	AddressPrefix = "SQ:"
)

// PublicKey is a Dilithium Mode 3 public key.
type PublicKey struct {
	k *mode3.PublicKey
}

// PrivateKey is a Dilithium Mode 3 private key.
type PrivateKey struct {
	k *mode3.PrivateKey
}

// Address is a 32-byte quantum-safe blockchain address.
type Address [AddressSize]byte

// GenerateKeypair generates a new Dilithium Mode 3 keypair.
func GenerateKeypair() (*PublicKey, *PrivateKey, error) {
	pub, priv, err := mode3.GenerateKey(rand.Reader)
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
	pub := new(mode3.PublicKey)
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
	priv := new(mode3.PrivateKey)
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

// Zeroize overwrites Dilithium private key material then drops the pointer.
// Best-effort: replaces key bytes with a deterministic zero-seed key, then nils.
func (sk *PrivateKey) Zeroize() {
	if sk == nil || sk.k == nil {
		return
	}
	var zeroSeed [32]byte
	_, junk := mode3.NewKeyFromSeed(&zeroSeed)
	*sk.k = *junk
	sk.k = nil
}

// Public derives the public key from a private key.
func (sk *PrivateKey) Public() *PublicKey {
	pub := sk.k.Public().(*mode3.PublicKey)
	return &PublicKey{k: pub}
}

// Sign produces a 3293-byte Dilithium Mode 3 signature over the message.
// Uses mode3.SignTo which writes into a pre-allocated buffer.
func Sign(sk *PrivateKey, message []byte) ([]byte, error) {
	if sk == nil {
		return nil, errors.New("nil private key")
	}
	sig := make([]byte, SignatureSize)
	mode3.SignTo(sk.k, message, sig)
	return sig, nil
}

// Verify checks a Dilithium Mode 3 signature.
func Verify(pk *PublicKey, message []byte, sig []byte) bool {
	if pk == nil || len(sig) != SignatureSize {
		return false
	}
	return mode3.Verify(pk.k, message, sig)
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
