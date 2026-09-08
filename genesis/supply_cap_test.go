package genesis

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func newCapState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}

// Minting records the figure, and refuses to go above the cap. Exactly the
// cap is fine: that is the production document.
func TestApplyToStateRecordsMintedAndRefusesAboveTheCap(t *testing.T) {
	g := liveTierGenesis(t)
	sdb := newCapState(t)
	if _, err := g.ApplyToState(sdb); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, ok, err := sdb.GetMeta(MetaNativeMinted)
	if err != nil || !ok {
		t.Fatalf("minted record missing after genesis: ok=%v err=%v", ok, err)
	}
	if string(raw) != g.SumAllocations().String() {
		t.Fatalf("minted record %s, want %s", raw, g.SumAllocations())
	}

	over := liveTierGenesis(t)
	over.Allocations = []Allocation{{
		Address: over.Allocations[0].Address,
		Balance: new(big.Int).Add(NativeSupplyCap(), big.NewInt(1)).String(),
	}}
	over.TotalSupply = ""
	if _, err := over.ApplyToState(newCapState(t)); err == nil || !strings.Contains(err.Error(), "26 billion") {
		t.Fatalf("one unit over the cap must be refused at mint, got: %v", err)
	}

	exact := liveTierGenesis(t)
	exact.Allocations = []Allocation{{Address: exact.Allocations[0].Address, Balance: NativeSupplyCapDecimal}}
	exact.TotalSupply = NativeSupplyCapDecimal
	if err := exact.Validate(); err != nil {
		t.Fatalf("exactly the cap must validate: %v", err)
	}
	if _, err := exact.ApplyToState(newCapState(t)); err != nil {
		t.Fatalf("exactly the cap must mint: %v", err)
	}
}

// The per-block check holds circulating supply to the minted record: a
// transfer passes, an account credited from nowhere does not.
func TestCheckNativeSupplyHoldsCirculationToTheMintedRecord(t *testing.T) {
	g := liveTierGenesis(t)
	sdb := newCapState(t)
	if _, err := g.ApplyToState(sdb); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := CheckNativeSupply(sdb); err != nil {
		t.Fatalf("fresh genesis must pass: %v", err)
	}

	from, err := qcrypto.AddressFromString(g.Allocations[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	to := qcrypto.Address{0x42}
	if err := sdb.Transfer(from, to, big.NewInt(1_000)); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if err := CheckNativeSupply(sdb); err != nil {
		t.Fatalf("a transfer conserves supply and must pass: %v", err)
	}

	// Money from nowhere, staged but not committed: refused before it can be.
	ghost := qcrypto.Address{0x99}
	gacc := types.NewAccount(ghost)
	gacc.AddBalance(big.NewInt(1))
	sdb.SetAccount(gacc)
	err = CheckNativeSupply(sdb)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("one unit of new supply must be refused, got: %v", err)
	}
}
