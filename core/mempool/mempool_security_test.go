package mempool

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// One funded address must not be able to occupy more than MaxPerAddr pending slots,
// even when the global pool cap is far larger.
func TestPerAddressCap(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000_000)) // enough for every tx's fee
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	m := New(10_000, sdb) // global cap >> per-address cap, so per-address is the limiter

	for i := 0; i < MaxPerAddr; i++ {
		tx := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), uint64(i), int64(1_700_000_000+i))
		if err := tx.Sign(priv, true); err != nil {
			t.Fatalf("sign %d: %v", i, err)
		}
		if err := m.Add(tx); err != nil {
			t.Fatalf("add %d unexpectedly rejected: %v", i, err)
		}
	}

	// The (MaxPerAddr+1)-th from the same sender must be rejected by the cap.
	over := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), uint64(MaxPerAddr), 1_700_099_999)
	if err := over.Sign(priv, true); err != nil {
		t.Fatalf("sign overflow tx: %v", err)
	}
	err = m.Add(over)
	if err == nil {
		t.Fatal("expected per-address cap rejection, got nil")
	}
	if !strings.Contains(err.Error(), "per-address") {
		t.Fatalf("expected per-address error, got: %v", err)
	}
}
