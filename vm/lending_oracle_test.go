package vm

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// A market bound to an oracle borrows at the posted median, never at a price
// the caller brings.
func TestBorrowFromOracleUsesPostedPrice(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x30)
	stranger := addr(0x31)
	lending := DeployLending(sdb, owner, 1)
	oaddr := DeployOracle(sdb, owner)
	lp := NewLendingProtocol(sdb, lending)

	if err := lp.SetOracle(stranger, oaddr); err == nil {
		t.Fatal("a stranger bound the market's oracle")
	}
	if _, _, err := lp.oraclePrices(addr(0x40), addr(0x41), 10); err == nil || !strings.Contains(err.Error(), "oracle not set") {
		t.Fatalf("prices without an oracle: %v", err)
	}
	if err := lp.SetOracle(owner, oaddr); err != nil {
		t.Fatalf("set oracle: %v", err)
	}
	bound, _ := lp.Oracle()
	if bound != oaddr {
		t.Fatalf("oracle = %s, want %s", bound, oaddr)
	}

	o := NewPriceOracle(sdb, oaddr)
	feeder := addr(0x32)
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
	// Stale feeds are not prices.
	if _, _, err := lp.oraclePrices(col, borrow, 1+MaxPriceAgeBlocks+1); err == nil {
		t.Fatal("stale oracle feed was accepted as a price")
	}
}
