package vm

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func TestOracleGetPriceDiscoversRegisteredFeeders(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x01)
	feeder := addr(0x02)
	asset := addr(0x03)
	oaddr := DeployOracle(sdb, owner)
	o := NewPriceOracle(sdb, oaddr)

	pk := make([]byte, qcrypto.PublicKeySize)
	if err := o.RegisterFeeder(owner, feeder, pk, "alpha"); err != nil {
		t.Fatalf("register: %v", err)
	}
	listed, err := o.ListFeeders()
	if err != nil || len(listed) != 1 || listed[0] != feeder {
		t.Fatalf("list = %v (%v), want [%s]", listed, err, feeder)
	}

	if err := o.setPrice(asset, feeder, &PriceFeed{
		Asset: asset.String(), Price: big.NewInt(42), BlockHeight: 1,
	}); err != nil {
		t.Fatalf("set price: %v", err)
	}
	got, err := o.GetPrice(asset, 10)
	if err != nil {
		t.Fatalf("GetPrice: %v", err)
	}
	if got.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("price = %s, want 42", got)
	}
}

func TestOracleGetPriceRefusesStale(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0x01)
	feeder := addr(0x02)
	asset := addr(0x03)
	o := NewPriceOracle(sdb, DeployOracle(sdb, owner))
	if err := o.RegisterFeeder(owner, feeder, make([]byte, qcrypto.PublicKeySize), "a"); err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = o.setPrice(asset, feeder, &PriceFeed{Price: big.NewInt(1), BlockHeight: 1})
	if _, err := o.GetPrice(asset, 1+MaxPriceAgeBlocks+1); err == nil {
		t.Fatal("stale price must be refused")
	}
}
