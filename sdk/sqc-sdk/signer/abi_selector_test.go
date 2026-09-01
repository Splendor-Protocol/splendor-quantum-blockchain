package signer

import (
	"encoding/hex"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// Golden selector must stay aligned with sdk/bridge/src/abi.ts (SHA3-256 first 4 bytes).
func TestQSolSelectorGolden(t *testing.T) {
	sel := vm.SelectorFromSignature("transfer(sqaddress,uint256)")
	got := hex.EncodeToString(sel[:])
	const want = "14cde9ab"
	if got != want {
		t.Fatalf("selector mismatch: got %s want %s", got, want)
	}
}
