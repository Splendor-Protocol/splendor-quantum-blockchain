package rpc

import (
	"testing"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// No route registrar may touch consensus state.
//
// The (since removed) lending registrar called a Deploy function that wrote a
// storage slot, and because registration happens at node startup rather than
// inside a block, that leaf landed in whichever block the validator sealed
// next. The header then committed to a mutation no transaction produced, so a
// peer replaying history recomputed a different root and stopped at block 1.
// It stayed invisible for as long as every tier ran one validator with no
// peers, because nothing ever replayed anything.
//
// Every registrar that takes a *state.StateDB can make the same mistake, and
// nothing about their signatures discourages it. This checks the whole class:
// whether a fresh chain is replayable depends on every one of them staying
// read-only, not on the one that happened to be caught. The executor, pool and
// guard are nil: none is consulted at registration, only per request.
func TestNoRouteRegistrarMutatesConsensusState(t *testing.T) {
	registrars := map[string]func(*mux.Router, *state.StateDB){
		"bridge": RegisterBridgeRoutes,
		"exchange": func(r *mux.Router, sdb *state.StateDB) {
			RegisterExchangeRoutes(r, sdb, func() vm.ReadContext { return vm.ReadContext{} })
		},
		"state": func(r *mux.Router, sdb *state.StateDB) {
			RegisterStateRoutes(r, sdb, nil, nil)
		},
		"call": func(r *mux.Router, sdb *state.StateDB) {
			RegisterCallRoutes(r, sdb, nil, nil, nil)
		},
		"verify": func(r *mux.Router, sdb *state.StateDB) {
			RegisterVerifyRoutes(r, sdb, nil)
		},
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
