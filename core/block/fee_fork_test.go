package block

import (
	"math/big"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A fee the producer accepts must not be one every follower rejects.
//
// big.Int.Int64() is undefined above MaxInt64 — it returns the low 64 bits — so
// a fee of exactly 2^64 truncated to 0 and failed a minimum of 1000. The producer
// did not run pre-tx hooks and the follower did, so the producer packed the
// transaction and every follower refused the block. 2^64 smallest units is about
// 18.4 SPLD, so this needed no privilege at all.
func TestMinFeeHookDoesNotTruncateLargeFees(t *testing.T) {
	hook := MinFeeHook(1000)

	twoPow64, _ := new(big.Int).SetString("18446744073709551616", 10)
	if got := twoPow64.Int64(); got != 0 {
		t.Fatalf("premise changed: 2^64 .Int64() = %d, expected the truncation to 0", got)
	}

	if err := hook(&types.Transaction{Fee: twoPow64}, 1); err != nil {
		t.Fatalf("a fee of 2^64 (~18.4 SPLD) was rejected as too low: %v", err)
	}
}

func TestMinFeeHookStillRejectsGenuinelyLowFees(t *testing.T) {
	hook := MinFeeHook(1000)
	for _, fee := range []*big.Int{big.NewInt(0), big.NewInt(999)} {
		if err := hook(&types.Transaction{Fee: fee}, 1); err == nil {
			t.Fatalf("fee %v should be below the minimum", fee)
		}
	}
	if err := hook(&types.Transaction{Fee: nil}, 1); err == nil {
		t.Fatal("a nil fee should be rejected")
	}
	if err := hook(&types.Transaction{Fee: big.NewInt(1000)}, 1); err != nil {
		t.Fatalf("exactly the minimum should pass: %v", err)
	}
}
