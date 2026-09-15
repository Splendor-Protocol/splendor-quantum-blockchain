package consensus

import (
	"errors"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Of what the builder left out, only what can never apply is handed to the
// pool to drop; a nonce gap stays, since it clears by itself.
func TestOnlyUnappliableSkipsAreDropped(t *testing.T) {
	gap := &types.Transaction{Nonce: 7}
	stale := &types.Transaction{Nonce: 1}
	broke := &types.Transaction{Nonce: 3}
	dead := unappliable([]block.SkippedTx{
		{Tx: gap, Err: block.ErrNonceGap},
		{Tx: stale, Err: types.ErrInvalidNonce},
		{Tx: broke, Err: errors.New("insufficient balance")},
	})
	if len(dead) != 2 || dead[0] != stale || dead[1] != broke {
		t.Fatalf("dropping %v, want the stale and the unfunded transaction only", dead)
	}
	if unappliable(nil) != nil {
		t.Fatal("nothing skipped, something to drop")
	}
}
