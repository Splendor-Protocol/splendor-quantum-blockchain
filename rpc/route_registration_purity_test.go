package rpc

import (
	"testing"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// No route registrar may touch consensus state — not just the lending one.
//
// The lending registrar called a Deploy function that wrote a storage slot, and
// because registration happens at node startup rather than inside a block, that
// leaf landed in whichever block the validator sealed next. The header then
// committed to a mutation no transaction produced, so a peer replaying history
// recomputed a different root and stopped at block 1. It stayed invisible for as
// long as every tier ran one validator with no peers, because nothing ever
// replayed anything.
//
// The guard that shipped with the fix covered only lending, which leaves the
// same mistake available to the other three registrars — and they all take a
// *state.StateDB, so nothing about their signatures discourages it. This checks
// the whole class instead: whether a fresh chain is replayable depends on every
// one of them staying read-only, not on the one that happened to be caught.
func TestNoRouteRegistrarMutatesConsensusState(t *testing.T) {
	registrars := map[string]func(*mux.Router, *state.StateDB){
		"lending": RegisterLendingRoutes,
		"gov":     RegisterGovRoutes,
		"dex":     RegisterDEXRoutes,
	}

	for name, register := range registrars {
		t.Run(name, func(t *testing.T) {
			sdb, err := state.NewStateDB(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer sdb.Close()

			before, err := sdb.Commit()
			if err != nil {
				t.Fatal(err)
			}

			register(mux.NewRouter(), sdb)

			// Staged writes surface here even if nothing commits them, which is
			// what makes this catch the bug at registration rather than at the
			// next sealed block.
			pending, err := sdb.PendingStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			if pending != before {
				t.Fatalf("Register%sRoutes staged a consensus write: root %x -> %x — "+
					"this is the defect that made every chain minted before it unsyncable",
					name, before[:8], pending[:8])
			}

			after, err := sdb.Commit()
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("Register%sRoutes changed the committed state root: %x -> %x",
					name, before[:8], after[:8])
			}
		})
	}
}
