package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// CommittedNonce answers from committed state alone: an uncommitted write is
// invisible to it until Commit, and an unknown account reads as zero.
func TestCommittedNonceIgnoresThePendingOverlay(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	var addr qcrypto.Address
	addr[0] = 0x42
	if n, err := sdb.CommittedNonce(addr); err != nil || n != 0 {
		t.Fatalf("unknown account: nonce %d err %v, want 0 nil", n, err)
	}

	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(5))
	acc.Nonce = 7
	acc.PublicKey = make([]byte, qcrypto.PublicKeySize)
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n, err := sdb.CommittedNonce(addr); err != nil || n != 7 {
		t.Fatalf("committed: nonce %d err %v, want 7 nil", n, err)
	}

	acc.Nonce = 8
	sdb.SetAccount(acc)
	if n, _ := sdb.GetNonce(addr); n != 8 {
		t.Fatalf("overlay nonce %d, want 8", n)
	}
	if n, err := sdb.CommittedNonce(addr); err != nil || n != 7 {
		t.Fatalf("with a pending write: nonce %d err %v, want 7 nil", n, err)
	}
	sdb.DiscardPending()
	if n, err := sdb.CommittedNonce(addr); err != nil || n != 7 {
		t.Fatalf("after discard: nonce %d err %v, want 7 nil", n, err)
	}
}
