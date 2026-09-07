package mempool

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A nonce below the sender's committed nonce can never be included: the executor
// requires tx.Nonce == account.Nonce and an account nonce never moves backwards.
// Admitting it anyway spends an ML-DSA verification and a pool slot on a tx that
// only PruneStale will ever remove, while telling the sender it was accepted.
func TestAddRejectsNonceBelowCommitted(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	acc, _ := sdb.GetAccount(from)
	acc.Nonce = 207
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var to qcrypto.Address
	to[0] = 0x55

	stale := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 0, 1_700_000_000)
	_ = stale.Sign(priv, true)
	err := m.Add(stale)
	if !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("stale nonce: want ErrNonceTooLow, got %v", err)
	}
	if !strings.Contains(err.Error(), "nonce too low") {
		t.Fatalf("error should say nonce too low: %v", err)
	}
	if m.Size() != 0 {
		t.Fatalf("stale tx took a pool slot: size=%d", m.Size())
	}

	// The committed nonce itself and a future nonce (gap) are still admitted:
	// only the past is refused.
	current := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 207, 1_700_000_001)
	_ = current.Sign(priv, true)
	if err := m.Add(current); err != nil {
		t.Fatalf("current nonce rejected: %v", err)
	}
	future := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 209, 1_700_000_002)
	_ = future.Sign(priv, true)
	if err := m.Add(future); err != nil {
		t.Fatalf("future nonce rejected: %v", err)
	}
}
