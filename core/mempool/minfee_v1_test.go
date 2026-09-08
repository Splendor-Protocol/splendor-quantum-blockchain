package mempool

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// richSender is fundedSender with a balance that covers the min_fee_v1 floor.
func richSender(t *testing.T, sdb *state.StateDB) (qcrypto.Address, *qcrypto.PrivateKey) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	addr := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(addr)
	acc.AddBalance(new(big.Int).Mul(big.NewInt(MinFeeV1Amount), big.NewInt(1000)))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return addr, priv
}

// Under min_fee_v1 the pool's floor is 0.001 SPLD: the launch fee is refused
// as "fee too low", the new floor is admitted, the congestion multipliers
// scale the new base, and clearing the base restores the launch floor.
func TestMinFeeV1RaisesThePoolFloor(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	m := New(100, sdb)
	if got := m.MinFeeNow(); got != MinFee {
		t.Fatalf("launch base fee = %d, want %d", got, MinFee)
	}
	m.SetBaseFee(MinFeeV1Amount)
	if got := m.MinFeeNow(); got != MinFeeV1Amount {
		t.Fatalf("floor under min_fee_v1 = %d, want %d", got, MinFeeV1Amount)
	}

	var to qcrypto.Address
	to[0] = 0x55
	from, priv := richSender(t, sdb)

	cheap := types.NewTransfer(from, to, big.NewInt(1), big.NewInt(MinFee), 0, 1_700_000_000)
	if err := cheap.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(cheap); err == nil || !strings.Contains(err.Error(), "fee too low") {
		t.Fatalf("launch fee under min_fee_v1: want fee too low, got %v", err)
	}

	paid := types.NewTransfer(from, to, big.NewInt(1), big.NewInt(MinFeeV1Amount), 0, 1_700_000_001)
	if err := paid.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(paid); err != nil {
		t.Fatalf("0.001 SPLD under min_fee_v1 refused: %v", err)
	}

	// Congestion tiers multiply the new base, not the launch one.
	if got := m.minFeeLocked(); got < MinFeeV1Amount {
		t.Fatalf("congestion floor %d fell below the base", got)
	}

	m.SetBaseFee(0)
	if got := m.MinFeeNow(); got != MinFee {
		t.Fatalf("floor after clearing the base = %d, want %d", got, MinFee)
	}
}
