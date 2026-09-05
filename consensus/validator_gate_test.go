package consensus

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

func newGatedValidator(t *testing.T) (*Validator, *block.Executor, *qcrypto.PublicKey) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	v := New(Config{
		Address:    qcrypto.DeriveAddress(pub),
		PublicKey:  pub.Bytes(),
		PrivateKey: priv,
	}, ex, mempool.New(16, sdb), nil)
	return v, ex, pub
}

// A validator the chain does not admit must go QUIET, not fail. Letting
// ProduceBlock refuse would log a production error every block interval and
// bury whatever the real problem is; the loop asks first and waits.
func TestValidatorStaysQuietWhenNotAdmitted(t *testing.T) {
	v, ex, _ := newGatedValidator(t)
	otherPub, _, _ := qcrypto.GenerateKeypair()

	ex.SetForks(forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1})
	ex.SetGenesisValidator(qcrypto.DeriveAddress(otherPub))

	if err := v.produceBlock(); err != nil {
		t.Fatalf("an unadmitted validator must skip the slot, not error: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("a block was produced at height %d despite the gate", h)
	}
	if !v.ineligible {
		t.Error("the reason should have been reported once")
	}

	// Admit it, and the very next tick produces.
	ex.SetGenesisValidator(v.cfg.Address)
	if err := v.produceBlock(); err != nil {
		t.Fatalf("an admitted validator must produce: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 1 {
		t.Fatalf("latest height = %d, want 1", h)
	}
	if v.ineligible {
		t.Error("the ineligible flag must clear once the validator is admitted again")
	}
}

// With no gate scheduled the loop behaves exactly as it did before: this is
// the single-validator regression that matters most.
func TestValidatorProducesWithNoGateScheduled(t *testing.T) {
	v, ex, _ := newGatedValidator(t)
	for i := 1; i <= 3; i++ {
		if err := v.produceBlock(); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}
	if h, _ := ex.LatestHeight(); h != 3 {
		t.Fatalf("latest height = %d, want 3", h)
	}
	if v.ineligible {
		t.Error("an ungated validator must never be marked ineligible")
	}
}
