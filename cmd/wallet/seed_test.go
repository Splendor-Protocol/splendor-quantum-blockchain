package main

import (
	"encoding/hex"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func TestParseSeedHexAndMnemonicRoundTrip(t *testing.T) {
	var seed [qcrypto.SeedSize]byte
	for i := range seed {
		seed[i] = byte(i * 3)
	}
	phrase, err := seedToMnemonic(seed)
	if err != nil {
		t.Fatal(err)
	}
	if words := strings.Fields(phrase); len(words) != 24 {
		t.Fatalf("got %d words, want 24", len(words))
	}

	fromPhrase, err := parseSeedInput(phrase)
	if err != nil {
		t.Fatal(err)
	}
	if fromPhrase != seed {
		t.Fatal("mnemonic did not recover the seed")
	}

	hexStr := hex.EncodeToString(seed[:])
	fromHex, err := parseSeedInput(hexStr)
	if err != nil {
		t.Fatal(err)
	}
	if fromHex != seed {
		t.Fatal("hex did not recover the seed")
	}

	from0x, err := parseSeedInput("0x" + hexStr)
	if err != nil {
		t.Fatal(err)
	}
	if from0x != seed {
		t.Fatal("0x-prefixed hex did not recover the seed")
	}
}

func TestParseSeedRejectsAddress(t *testing.T) {
	_, err := parseSeedInput("SQ:" + strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("expected address to be rejected")
	}
}
