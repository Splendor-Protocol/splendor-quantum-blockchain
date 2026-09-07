package vm

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func testReceipt() *types.Receipt {
	return &types.Receipt{Logs: []*types.Log{}}
}

func TestLendingBorrowRefusesCallerSuppliedPrices(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x10)
	ctx := ModuleContext{
		Caller: owner, Height: 10, Nonce: 1,
		Forks: forks.Schedule{forks.ModuleRevertV1: 1, forks.LendingV1: 1},
	}
	rc := testReceipt()
	if err := dispatchLending(sdb, ctx, "deploy", []byte(`{}`), rc); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("lending deploy did not set contract address")
	}

	raw, _ := json.Marshal(map[string]string{
		"lending":          rc.ContractAddress.String(),
		"asset":            addr(0x20).String(),
		"collateral":       addr(0x21).String(),
		"amount":           "1",
		"collateral_price": "999999999999999999",
		"borrow_price":     "1",
	})
	err := dispatchLending(sdb, ctx, "borrow", raw, rc)
	if err == nil {
		t.Fatal("borrow with caller-supplied prices must not succeed")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Fatalf("expected oracle refusal, got: %v", err)
	}
}

func TestLendingSetOracleIsOwnerOnly(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x11)
	stranger := addr(0x12)
	ctx := ModuleContext{
		Caller: owner, Height: 10, Nonce: 1,
		Forks: forks.Schedule{forks.ModuleRevertV1: 1, forks.LendingV1: 1},
	}
	rc := testReceipt()
	if err := dispatchLending(sdb, ctx, "deploy", []byte(`{}`), rc); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	oaddr := DeployOracle(sdb, owner)
	raw, _ := json.Marshal(map[string]string{
		"lending": rc.ContractAddress.String(),
		"oracle":  oaddr.String(),
	})
	strangerCtx := ctx
	strangerCtx.Caller = stranger
	if err := dispatchLending(sdb, strangerCtx, "set_oracle", raw, rc); err == nil {
		t.Fatal("stranger must not set the lending oracle")
	}
	if err := dispatchLending(sdb, ctx, "set_oracle", raw, rc); err != nil {
		t.Fatalf("owner set_oracle: %v", err)
	}
	oa, _ := NewLendingProtocol(sdb, *rc.ContractAddress).Oracle()
	if oa != oaddr {
		t.Fatalf("oracle = %s, want %s", oa, oaddr)
	}
}

func TestBorrowFromOracleUsesPostedPrice(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x30)
	lending := DeployLending(sdb, owner, 1)
	oaddr := DeployOracle(sdb, owner)
	lp := NewLendingProtocol(sdb, lending)
	if err := lp.SetOracle(owner, oaddr); err != nil {
		t.Fatalf("set oracle: %v", err)
	}
	o := NewPriceOracle(sdb, oaddr)
	feeder := addr(0x31)
	if err := o.RegisterFeeder(owner, feeder, make([]byte, qcrypto.PublicKeySize), "f"); err != nil {
		t.Fatalf("register feeder: %v", err)
	}
	col, borrow := addr(0x40), addr(0x41)
	_ = o.setPrice(col, feeder, &PriceFeed{Price: big.NewInt(100), BlockHeight: 1})
	_ = o.setPrice(borrow, feeder, &PriceFeed{Price: big.NewInt(100), BlockHeight: 1})

	cp, bp, err := lp.oraclePrices(col, borrow, 10)
	if err != nil {
		t.Fatalf("oraclePrices: %v", err)
	}
	if cp.Cmp(big.NewInt(100)) != 0 || bp.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("prices = %s / %s, want 100 / 100", cp, bp)
	}
}
