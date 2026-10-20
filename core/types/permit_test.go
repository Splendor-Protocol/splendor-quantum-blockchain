package types

import (
	"encoding/hex"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func permitTestAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

// The fixed vector every other implementation of the permit digest (the WASM
// signer, the TypeScript client) is pinned to. Changing it is a wire break.
const (
	permitGoldenChainID  = "splendor-quantum-1"
	permitGoldenAmount   = "1000000000000000000" // 1e18
	permitGoldenNonce    = uint64(7)
	permitGoldenDeadline = int64(1_800_000_000)
	permitGoldenDigest   = "376313a1948273267869c6fed8a3b6415e8559862a072b57d7cb7e3235cb7827"
)

func permitGolden(t *testing.T, chainID string) [32]byte {
	t.Helper()
	amount, _ := new(big.Int).SetString(permitGoldenAmount, 10)
	d, err := PermitDigest(chainID,
		permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcc),
		amount, permitGoldenNonce, permitGoldenDeadline)
	if err != nil {
		t.Fatalf("PermitDigest(%q): %v", chainID, err)
	}
	return d
}

func TestPermitDigestGolden(t *testing.T) {
	d := permitGolden(t, permitGoldenChainID)
	got := hex.EncodeToString(d[:])
	if got != permitGoldenDigest {
		t.Fatalf("permit digest golden moved:\n got %s\nwant %s", got, permitGoldenDigest)
	}
}

// The whole point of v2: the same token address exists on every tier, so the
// chain id has to be part of what is signed.
func TestPermitDigestBindsChainID(t *testing.T) {
	if permitGolden(t, "splendor-quantum-1") == permitGolden(t, "splendor-quantum-devnet-1") {
		t.Fatal("digest must differ across chain ids")
	}
}

func TestPermitDigestEveryFieldMoves(t *testing.T) {
	base := permitGolden(t, permitGoldenChainID)
	amount, _ := new(big.Int).SetString(permitGoldenAmount, 10)
	variants := map[string]func() ([32]byte, error){
		"contract": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xab), permitTestAddr(0xbb), permitTestAddr(0xcc), amount, permitGoldenNonce, permitGoldenDeadline)
		},
		"owner": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xaa), permitTestAddr(0xbc), permitTestAddr(0xcc), amount, permitGoldenNonce, permitGoldenDeadline)
		},
		"spender": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcd), amount, permitGoldenNonce, permitGoldenDeadline)
		},
		"amount": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcc), new(big.Int).Add(amount, big.NewInt(1)), permitGoldenNonce, permitGoldenDeadline)
		},
		"nonce": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcc), amount, permitGoldenNonce+1, permitGoldenDeadline)
		},
		"deadline": func() ([32]byte, error) {
			return PermitDigest(permitGoldenChainID, permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcc), amount, permitGoldenNonce, permitGoldenDeadline+1)
		},
	}
	for name, f := range variants {
		d, err := f()
		if err != nil {
			t.Fatalf("%s variant: %v", name, err)
		}
		if d == base {
			t.Errorf("changing %s did not move the digest", name)
		}
	}
}

// Caller-supplied values reach the digest from the vm, so every malformed
// input must come back as an error — a panic would take the node down.
func TestPermitDigestErrorsNotPanics(t *testing.T) {
	c, o, s := permitTestAddr(0xaa), permitTestAddr(0xbb), permitTestAddr(0xcc)
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256)
	maxU256 := new(big.Int).Sub(twoPow256, big.NewInt(1))

	if _, err := PermitDigest(permitGoldenChainID, c, o, s, maxU256, 0, 1); err != nil {
		t.Fatalf("2^256-1 must be accepted: %v", err)
	}
	cases := map[string]func() ([32]byte, error){
		"2^256":       func() ([32]byte, error) { return PermitDigest(permitGoldenChainID, c, o, s, twoPow256, 0, 1) },
		"nil amount":  func() ([32]byte, error) { return PermitDigest(permitGoldenChainID, c, o, s, nil, 0, 1) },
		"negative":    func() ([32]byte, error) { return PermitDigest(permitGoldenChainID, c, o, s, big.NewInt(-1), 0, 1) },
		"deadline<0":  func() ([32]byte, error) { return PermitDigest(permitGoldenChainID, c, o, s, big.NewInt(1), 0, -1) },
		"empty chain": func() ([32]byte, error) { return PermitDigest("", c, o, s, big.NewInt(1), 0, 1) },
		"bad chain":   func() ([32]byte, error) { return PermitDigest("splendor quantum", c, o, s, big.NewInt(1), 0, 1) },
	}
	for name, f := range cases {
		if _, err := f(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
