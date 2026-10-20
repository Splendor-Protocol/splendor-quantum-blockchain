package signer

import (
	"encoding/hex"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// Golden selector must stay aligned with sdk/sqc-sdk/src/abi.ts (SHA3-256 first 4 bytes).
func TestQSolSelectorGolden(t *testing.T) {
	legacy := vm.SelectorFromSignature("transfer(sqaddress,uint256)")
	if got := hex.EncodeToString(legacy[:]); got != "14cde9ab" {
		t.Fatalf("legacy sqaddress selector = %s, want 14cde9ab", got)
	}
	canonical := vm.SelectorFromSignature("transfer(address,uint256)")
	if got := hex.EncodeToString(canonical[:]); got != "4b40e901" {
		t.Fatalf("canonical address selector = %s, want 4b40e901", got)
	}
}
