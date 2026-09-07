// Package vm — Pluggable Precompile Registry for SQC-VM.
//
// Precompiles are native Go functions that can be called from SQC contracts
// via STATICCALL/CALL to well-known addresses. They execute at native speed
// (no bytecode interpretation overhead) and are useful for:
//   - Cryptographic operations (ML-DSA-65 batch verify, SHA3-512, etc.)
//   - Math operations (big integer modular exponentiation)
//   - Cross-contract utilities (ABI encode/decode, address derivation)
//
// Precompile addresses use the SQ: prefix with a low numeric value:
//   SQ:0000...0001  → SHA3-256 hash (input → 32-byte hash)
//   SQ:0000...0002  → SHA3-512 hash (input → 64-byte hash)
//   SQ:0000...0003  → BLAKE3 hash placeholder (input → 32-byte hash)
//   SQ:0000...0004  → ML-DSA-65 public key → SQ: address derivation
//   SQ:0000...0005  → ML-DSA-65 batch verify (verify N signatures)
//   SQ:0000...0006  → BigInt modular exponentiation
//   SQ:0000...0007  → ABI encode (pack args to 32-byte words)
//   SQ:0000...0008  → ABI decode (unpack 32-byte words)
//   SQ:0000...0009  → Identity (copy input to output — useful for calldata forwarding)
//   SQ:0000...000A  → QRC-20 totalSupply query
//   SQ:0000...000B  → QRC-20 balanceOf query
package vm

import (
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"golang.org/x/crypto/sha3"
)

// PrecompileFunc is the signature for a native precompile function.
// input: raw calldata bytes
// Returns: output bytes and gas used, or error.
type PrecompileFunc func(input []byte) (output []byte, gasUsed uint64, err error)

// PrecompileEntry describes a registered precompile.
type PrecompileEntry struct {
	Name    string
	Address qcrypto.Address
	Func    PrecompileFunc
	BaseGas uint64 // minimum gas charged regardless of input size
	GasPerByte uint64 // additional gas per input byte
}

// PrecompileRegistry maps addresses to native precompile functions.
// It is pluggable: external packages can register their own precompiles
// by calling Register() before the engine starts.
type PrecompileRegistry struct {
	entries map[qcrypto.Address]*PrecompileEntry
}

// NewPrecompileRegistry creates a new registry pre-loaded with all built-in precompiles.
func NewPrecompileRegistry() *PrecompileRegistry {
	r := &PrecompileRegistry{
		entries: make(map[qcrypto.Address]*PrecompileEntry),
	}
	r.registerBuiltins()
	return r
}

// Register adds a custom precompile to the registry.
// If an entry already exists at the address, it is replaced.
func (r *PrecompileRegistry) Register(entry *PrecompileEntry) {
	r.entries[entry.Address] = entry
}

// Get returns the precompile for the given address, or nil if not found.
func (r *PrecompileRegistry) Get(addr qcrypto.Address) *PrecompileEntry {
	return r.entries[addr]
}

// IsPrecompile returns true if the address is a registered precompile.
func (r *PrecompileRegistry) IsPrecompile(addr qcrypto.Address) bool {
	_, ok := r.entries[addr]
	return ok
}

// Call executes a precompile with the given input and gas limit.
func (r *PrecompileRegistry) Call(addr qcrypto.Address, input []byte, gasLimit uint64) ([]byte, uint64, error) {
	entry, ok := r.entries[addr]
	if !ok {
		return nil, 0, fmt.Errorf("no precompile at %s", addr)
	}

	// Compute gas cost
	gasCost := entry.BaseGas + entry.GasPerByte*uint64(len(input))
	if gasCost > gasLimit {
		return nil, gasLimit, fmt.Errorf("precompile %s: out of gas (need %d, have %d)",
			entry.Name, gasCost, gasLimit)
	}

	output, extraGas, err := entry.Func(input)
	totalGas := gasCost + extraGas
	return output, totalGas, err
}

// precompileAddr builds a precompile address from a small integer.
// Addresses are SQ:000...00XX where XX is the hex precompile ID.
func precompileAddr(id byte) qcrypto.Address {
	var addr qcrypto.Address
	addr[31] = id
	return addr
}

// ── Built-in precompiles ───────────────────────────────────────────────────────

func (r *PrecompileRegistry) registerBuiltins() {
	// 0x01 — SHA3-256: hash input → 32 bytes
	r.Register(&PrecompileEntry{
		Name:       "sha3_256",
		Address:    precompileAddr(0x01),
		BaseGas:    30,
		GasPerByte: 6,
		Func: func(input []byte) ([]byte, uint64, error) {
			h := qcrypto.Hash32(input)
			return h[:], 0, nil
		},
	})

	// 0x02 — SHA3-512: hash input → 64 bytes
	r.Register(&PrecompileEntry{
		Name:       "sha3_512",
		Address:    precompileAddr(0x02),
		BaseGas:    50,
		GasPerByte: 8,
		Func: func(input []byte) ([]byte, uint64, error) {
			h := sha3.New512()
			h.Write(input)
			return h.Sum(nil), 0, nil
		},
	})

	// 0x03 — Identity: return input unchanged (useful for calldata forwarding)
	r.Register(&PrecompileEntry{
		Name:       "identity",
		Address:    precompileAddr(0x03),
		BaseGas:    15,
		GasPerByte: 3,
		Func: func(input []byte) ([]byte, uint64, error) {
			out := make([]byte, len(input))
			copy(out, input)
			return out, 0, nil
		},
	})

	// 0x04 — Derive SQ address from ML-DSA-65 public key bytes
	// Input: 1952-byte ML-DSA-65 public key
	// Output: 32-byte SQ address
	r.Register(&PrecompileEntry{
		Name:    "mldsa_derive_address",
		Address: precompileAddr(0x04),
		BaseGas: 500,
		GasPerByte: 0,
		Func: func(input []byte) ([]byte, uint64, error) {
			if len(input) != qcrypto.PublicKeySize {
				return nil, 0, fmt.Errorf("mldsa_derive_address: need %d bytes, got %d",
					qcrypto.PublicKeySize, len(input))
			}
			pub, err := qcrypto.PublicKeyFromBytes(input)
			if err != nil {
				return nil, 0, fmt.Errorf("mldsa_derive_address: invalid public key: %w", err)
			}
			addr := qcrypto.DeriveAddress(pub)
			return addr[:], 0, nil
		},
	})

	// 0x05 — ML-DSA-65 verify: verify one signature
	// Input: [32-byte msg][1952-byte pubkey][3309-byte sig]
	// Output: [1] if valid, [0] if invalid
	r.Register(&PrecompileEntry{
		Name:    "mldsa_verify",
		Address: precompileAddr(0x05),
		BaseGas: 3000,
		GasPerByte: 0,
		Func: func(input []byte) ([]byte, uint64, error) {
			expectedLen := 32 + qcrypto.PublicKeySize + qcrypto.SignatureSize
			if len(input) < expectedLen {
				return []byte{0}, 0, nil // invalid = 0
			}
			msg := input[0:32]
			pubKeyBytes := input[32 : 32+qcrypto.PublicKeySize]
			sig := input[32+qcrypto.PublicKeySize : 32+qcrypto.PublicKeySize+qcrypto.SignatureSize]

			pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
			if err != nil || !qcrypto.Verify(pub, msg, qcrypto.CtxMessage, sig) {
				return []byte{0}, 0, nil
			}
			return []byte{1}, 0, nil
		},
	})

	// 0x06 — BigInt modular exponentiation: base^exp mod modulus
	// Input: [32-byte base][32-byte exp][32-byte mod]
	// Output: [32-byte result]
	r.Register(&PrecompileEntry{
		Name:    "modexp",
		Address: precompileAddr(0x06),
		BaseGas: 200,
		GasPerByte: 0,
		Func: func(input []byte) ([]byte, uint64, error) {
			if len(input) < 96 {
				return make([]byte, 32), 0, nil
			}
			base := new(big.Int).SetBytes(input[0:32])
			exp := new(big.Int).SetBytes(input[32:64])
			mod := new(big.Int).SetBytes(input[64:96])
			if mod.Sign() == 0 {
				return make([]byte, 32), 0, nil
			}
			// Extra gas proportional to exp bit length
			extraGas := uint64(exp.BitLen()) * 5
			result := new(big.Int).Exp(base, exp, mod)
			resultBytes := result.Bytes()
			out := make([]byte, 32)
			copy(out[32-len(resultBytes):], resultBytes)
			return out, extraGas, nil
		},
	})

	// 0x07 — ABI encode: pack N 32-byte words
	// Input: concatenation of 32-byte values
	// Output: same (this is a no-op for pre-packed inputs, but validates alignment)
	r.Register(&PrecompileEntry{
		Name:    "abi_encode",
		Address: precompileAddr(0x07),
		BaseGas: 20,
		GasPerByte: 2,
		Func: func(input []byte) ([]byte, uint64, error) {
			// Pad to next 32-byte boundary
			padded := len(input)
			if padded%32 != 0 {
				padded = padded + (32 - padded%32)
			}
			out := make([]byte, padded)
			copy(out, input)
			return out, 0, nil
		},
	})

	// 0x08 — SHA3 of two concatenated values (useful for mapping slot derivation)
	// Input: [32-byte key][32-byte slot]
	// Output: [32-byte SHA3-256(key||slot)] — the storage slot for mapping[key]
	r.Register(&PrecompileEntry{
		Name:    "mapping_slot",
		Address: precompileAddr(0x08),
		BaseGas: 40,
		GasPerByte: 0,
		Func: func(input []byte) ([]byte, uint64, error) {
			if len(input) < 64 {
				return make([]byte, 32), 0, nil
			}
			h := qcrypto.Hash32(input[:64])
			return h[:], 0, nil
		},
	})

	// 0x09 — Address validation: check if bytes represent a valid SQ address
	// Input: 32-byte address
	// Output: [1] if non-zero, [0] if zero address
	r.Register(&PrecompileEntry{
		Name:    "is_valid_address",
		Address: precompileAddr(0x09),
		BaseGas: 10,
		GasPerByte: 0,
		Func: func(input []byte) ([]byte, uint64, error) {
			if len(input) < 32 {
				return []byte{0}, 0, nil
			}
			for _, b := range input[:32] {
				if b != 0 {
					return []byte{1}, 0, nil
				}
			}
			return []byte{0}, 0, nil
		},
	})
}

// BuiltinPrecompileAddresses returns all addresses occupied by built-in precompiles.
// Useful for explorers and tooling to identify precompile calls.
func BuiltinPrecompileAddresses() []qcrypto.Address {
	ids := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	addrs := make([]qcrypto.Address, len(ids))
	for i, id := range ids {
		addrs[i] = precompileAddr(id)
	}
	return addrs
}
