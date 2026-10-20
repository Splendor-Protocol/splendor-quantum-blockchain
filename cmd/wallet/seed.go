package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tyler-smith/go-bip39"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// seedToMnemonic encodes a 32-byte SQC seed as a 24-word BIP39 phrase.
// Same mapping the wallet extension uses (@scure/bip39, English wordlist).
func seedToMnemonic(seed [qcrypto.SeedSize]byte) (string, error) {
	return bip39.NewMnemonic(seed[:])
}

// parseSeedInput accepts a 64-char seed hex (optional 0x) or a 24-word phrase.
// An SQ: address is refused — that is public and cannot rebuild a key.
func parseSeedInput(raw string) ([qcrypto.SeedSize]byte, error) {
	var out [qcrypto.SeedSize]byte
	s := strings.TrimSpace(raw)
	if s == "" {
		return out, fmt.Errorf("seed or recovery phrase required")
	}
	if strings.HasPrefix(strings.ToLower(s), "sq:") {
		return out, fmt.Errorf("that is a public SQ: address, not a seed")
	}

	hexCand := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	hexCand = strings.ReplaceAll(hexCand, " ", "")
	if len(hexCand) == qcrypto.SeedSize*2 {
		b, err := hex.DecodeString(hexCand)
		if err != nil {
			return out, fmt.Errorf("invalid seed hex: %w", err)
		}
		copy(out[:], b)
		return out, nil
	}

	normalized := strings.ToLower(strings.Join(strings.Fields(s), " "))
	entropy, err := bip39.EntropyFromMnemonic(normalized)
	if err != nil {
		return out, fmt.Errorf("invalid recovery phrase: %w", err)
	}
	if len(entropy) != qcrypto.SeedSize {
		return out, fmt.Errorf("phrase must be 24 words (32-byte seed), got %d-byte entropy", len(entropy))
	}
	copy(out[:], entropy)
	return out, nil
}
