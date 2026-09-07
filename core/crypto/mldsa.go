// Package crypto provides all cryptographic primitives for Splendor Quantum Chain.
// Every operation uses post-quantum algorithms only.
//
// Signing:     ML-DSA-65 (NIST FIPS 204, CIRCL sign/mldsa/mldsa65). This is the
//
//	standardized post-quantum lattice signature. Signing is
//	deterministic (hedged=false) so a given (key, context, message)
//	always yields the same signature — required for consensus
//	reproducibility. NOT interoperable with the earlier round-3
//	CRYSTALS-Dilithium mode3 (a fresh genesis is required).
//
// Hashing:     SHA-3-256 (NIST FIPS 202)
// Key storage: AES-256-GCM + Argon2id
// NO ECDSA. NO SHA-256. NO KECCAK.
//
// Domain separation: FIPS 204 signing is context-aware. Every signature on this
// chain commits to a purpose-specific context string (see Ctx* below), so a
// signature produced for one purpose can never be replayed as another — a block
// signature cannot be presented as a transaction signature even if an attacker
// finds two purposes that hash the same bytes.
package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	mldsa65 "github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/sha3"
)

// AlgID values identify the signature scheme bound to a key, transaction, or
// block. Recorded on-chain so a future scheme (e.g. SLH-DSA) can coexist without
// a silent mix-up: verifiers reject anything whose alg_id they do not implement.
const (
	// AlgMLDSA65 is ML-DSA-65 (NIST FIPS 204) — the only scheme accepted today
	// for native transactions and blocks.
	AlgMLDSA65 = "ML-DSA-65"
	// AlgSLHDSA is SLH-DSA-SHA2-128s (NIST FIPS 205). Required alongside
	// ML-DSA-65 on SQVL instructions and attestations.
	AlgSLHDSA = "SLH-DSA-SHA2-128s"
)

// Domain separation context strings (FIPS 204 ctx parameter, max 255 bytes).
// Each signing purpose MUST use its own context. Changing any of these is a
// consensus-breaking change and requires a new genesis.
var (
	// CtxTransaction domain-separates user transaction signatures.
	CtxTransaction = []byte("SPLENDOR-QC-TX-v1")
	// CtxBlock domain-separates validator block signatures.
	CtxBlock = []byte("SPLENDOR-QC-BLOCK-v1")
	// CtxX402 domain-separates X402 payment authorisations.
	CtxX402 = []byte("SPLENDOR-QC-X402-v1")
	// CtxVote domain-separates BFT consensus votes.
	CtxVote = []byte("SPLENDOR-QC-VOTE-v1")
	// CtxMessage domain-separates free-form user message signing (wallet
	// signMessage, and the contract-side verify precompiles that check it).
	CtxMessage = []byte("SPLENDOR-QC-MSG-v1")
	// CtxPeerHandshake domain-separates P2P node identity proofs.
	CtxPeerHandshake = []byte("SPLENDOR-QC-P2P-v1")
	// CtxOracle domain-separates oracle price-feed attestations.
	CtxOracle = []byte("SPLENDOR-QC-ORACLE-v1")
	// CtxPermit domain-separates QRC-20 gasless approval (permit) signatures.
	CtxPermit = []byte("SPLENDOR-QC-PERMIT-v1")
	// CtxMultisig domain-separates multisig co-signer approvals.
	CtxMultisig = []byte("SPLENDOR-QC-MULTISIG-v1")
	// CtxOrder domain-separates exchange authorisations a maker signs off-chain:
	// orders (types.OrderDigest), signed cancels (types.CancelDigest) and
	// signed nonce bumps (types.IncrementNonceDigest). Appended after
	// CtxMultisig: the block is append-only.
	CtxOrder = []byte("SPLENDOR-QC-ORDER-v1")
	// CtxBridge domain-separates migration-bridge claim attestations: an
	// attestor signs types.BridgeClaimDigest over one legacy-chain burn to
	// authorise crediting it. Appended after CtxOrder: the block is
	// append-only.
	CtxBridge = []byte("SPLENDOR-QC-BRIDGE-v1")
	// CtxVerify domain-separates SQVL instructions and operator attestations.
	CtxVerify = []byte("SPLENDOR-QC-VERIFY-v1")
)

// maxContextSize is the FIPS 204 limit on the signing context string.
const maxContextSize = 255

const (
	// ML-DSA-65 (NIST FIPS 204) — sizes in bytes
	PublicKeySize  = mldsa65.PublicKeySize  // 1952
	PrivateKeySize = mldsa65.PrivateKeySize // 4032
	SignatureSize  = mldsa65.SignatureSize  // 3309

	// SQ addresses are 32 bytes derived from SHA3-256(public_key)
	AddressSize = 32

	// SeedSize is the 32-byte ML-DSA-65 seed the extension and CLI share.
	// Address body and seed hex are the same length; the seed is secret entropy,
	// the address is SHA3-256(pubkey). They are not interchangeable.
	SeedSize = mldsa65.SeedSize // 32

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
		return nil, nil, fmt.Errorf("ml-dsa-65 keygen: %w", err)
	}
	return &PublicKey{k: pub}, &PrivateKey{k: priv}, nil
}

// GenerateSeed returns 32 bytes of CSPRNG entropy. This is the same seed
// format the browser extension imports (64 hex characters, no 0x required).
func GenerateSeed() ([SeedSize]byte, error) {
	var seed [SeedSize]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return seed, fmt.Errorf("seed: %w", err)
	}
	return seed, nil
}

// KeypairFromSeed derives ML-DSA-65 keys the same way the extension does
// (circl mldsa65.NewKeyFromSeed). The same 32-byte seed always yields the
// same SQ: address in the CLI and in the wallet extension.
func KeypairFromSeed(seed [SeedSize]byte) (*PublicKey, *PrivateKey) {
	pub, priv := mldsa65.NewKeyFromSeed(&seed)
	return &PublicKey{k: pub}, &PrivateKey{k: priv}
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

// Sign produces a 3309-byte ML-DSA-65 (FIPS-204) signature over the message,
// bound to the given domain-separation context (one of the Ctx* values).
//
// Signing is DETERMINISTIC (randomized=false), so a given (key, context,
// message) always yields the same signature — consensus nodes must reproduce
// validator signatures bit-for-bit.
func Sign(sk *PrivateKey, message []byte, ctx []byte) ([]byte, error) {
	if sk == nil {
		return nil, errors.New("nil private key")
	}
	if len(ctx) == 0 {
		return nil, errors.New("empty signing context: every signature must be domain-separated")
	}
	if len(ctx) > maxContextSize {
		return nil, fmt.Errorf("signing context too long: %d bytes (max %d)", len(ctx), maxContextSize)
	}
	sig := make([]byte, SignatureSize)
	if err := mldsa65.SignTo(sk.k, message, ctx, false, sig); err != nil {
		return nil, fmt.Errorf("ml-dsa sign: %w", err)
	}
	return sig, nil
}

// Verify checks an ML-DSA-65 (FIPS-204) signature over the message under the
// given domain-separation context. A signature made for a different context
// fails here, which is what stops cross-purpose signature replay.
func Verify(pk *PublicKey, message []byte, ctx []byte, sig []byte) bool {
	if pk == nil || len(sig) != SignatureSize {
		return false
	}
	if len(ctx) == 0 || len(ctx) > maxContextSize {
		return false
	}
	return mldsa65.Verify(pk.k, message, ctx, sig)
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
