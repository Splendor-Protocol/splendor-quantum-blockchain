package types

import "testing"

// restoreChainID puts the package-level identity back after a test mutates it,
// so tests cannot leak a chain id into each other.
func restoreChainID(t *testing.T) {
	t.Helper()
	prevID, prevAdopted := ChainID, chainIDAdopted
	t.Cleanup(func() { ChainID, chainIDAdopted = prevID, prevAdopted })
}

func TestSetChainIDAdoptsGenesisValue(t *testing.T) {
	restoreChainID(t)
	ChainID, chainIDAdopted = DefaultChainID, false

	if err := SetChainID("splendor-quantum-devnet-1"); err != nil {
		t.Fatalf("adopting a well-formed id: %v", err)
	}
	if ChainID != "splendor-quantum-devnet-1" {
		t.Fatalf("ChainID = %q, want the adopted id", ChainID)
	}
}

// A second call naming the SAME chain is how a node that verifies its genesis
// on more than one path behaves. It must not be an error.
func TestSetChainIDIsIdempotent(t *testing.T) {
	restoreChainID(t)
	ChainID, chainIDAdopted = DefaultChainID, false

	if err := SetChainID("splendor-quantum-testnet-1"); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	if err := SetChainID("splendor-quantum-testnet-1"); err != nil {
		t.Fatalf("re-adopting the same id must be a no-op, got: %v", err)
	}
}

// The case that matters: two documents disagreeing about which network this is.
// Silently taking the second would let blocks from one chain be admitted under
// another chain's identity.
func TestSetChainIDRefusesToSwitchNetworks(t *testing.T) {
	restoreChainID(t)
	ChainID, chainIDAdopted = DefaultChainID, false

	if err := SetChainID("splendor-quantum-devnet-1"); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	err := SetChainID("splendor-quantum-1")
	if err == nil {
		t.Fatal("adopting a second, different chain id must be refused")
	}
	if ChainID != "splendor-quantum-devnet-1" {
		t.Fatalf("a refused switch must leave ChainID alone, got %q", ChainID)
	}
}

func TestValidateChainIDRejectsMalformed(t *testing.T) {
	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	for _, tc := range []struct{ name, id string }{
		{"empty", ""},
		{"space", "splendor quantum"},
		{"newline", "splendor-quantum-1\n"},
		{"slash", "splendor/quantum"},
		{"too long", long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateChainID(tc.id); err == nil {
				t.Fatalf("ValidateChainID(%q) = nil, want an error", tc.id)
			}
		})
	}
}

// The signed body must carry the chain id actually in force, or splitting the
// tiers would not separate their transactions at all.
func TestTransactionBodyBindsTheActiveChainID(t *testing.T) {
	restoreChainID(t)
	ChainID, chainIDAdopted = DefaultChainID, false

	tx := &Transaction{Version: 1, ChainID: ChainID, Type: TxTransfer}
	before, err := tx.Hash()
	if err != nil {
		t.Fatalf("hash on default chain: %v", err)
	}

	if err := SetChainID("splendor-quantum-devnet-1"); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	tx.ChainID = ChainID
	after, err := tx.Hash()
	if err != nil {
		t.Fatalf("hash on adopted chain: %v", err)
	}
	if before == after {
		t.Fatal("an otherwise identical transaction hashes the same on two chains: " +
			"the chain id is not bound into the signed body, so signatures replay across tiers")
	}
}
