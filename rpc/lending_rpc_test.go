package rpc

import (
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// Registering HTTP routes must not touch consensus state.
//
// RegisterLendingRoutes used to call vm.DeployLending, which writes the lending
// owner slot. That write happened outside any block, so it landed in whatever
// block the node sealed next: that block's state root then reflected a mutation
// no transaction accounted for, and a peer replaying history recomputed a
// different root and failed with "state root mismatch" at block 1 — permanently
// unsyncable. Serving a read API must never be able to move the state root.
func TestRegisteringRoutesDoesNotMutateConsensusState(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	before, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}

	RegisterLendingRoutes(mux.NewRouter(), sdb)

	// Anything staged by route registration would surface here.
	if pending, err := sdb.PendingStateRoot(); err != nil {
		t.Fatal(err)
	} else if pending != before {
		t.Fatalf("registering routes staged a consensus state write: root %x -> %x", before[:8], pending[:8])
	}

	after, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("registering routes changed the committed state root: %x -> %x", before[:8], after[:8])
	}
}

// The address must still be derivable without deploying, and must match what
// DeployLending would produce — otherwise the read API would look at the wrong
// contract once lending is genuinely deployed by a transaction.
func TestLendingAddressMatchesDeployAndIsPure(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	pure := vm.LendingAddress(qcrypto.Address{}, 1)
	deployed := vm.DeployLending(sdb, qcrypto.Address{}, 1)
	if pure != deployed {
		t.Fatalf("LendingAddress %x != DeployLending %x", pure[:8], deployed[:8])
	}
	// And DeployLending, unlike LendingAddress, is expected to dirty state.
	if pending, _ := sdb.PendingStateRoot(); pending == [32]byte{} {
		t.Fatal("DeployLending should have staged the owner slot")
	}
}
