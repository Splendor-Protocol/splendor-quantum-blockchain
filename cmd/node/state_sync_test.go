package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// --state-sync has three preconditions, each refused at start-up with the
// flag that fixes it named: a trust anchor for the head block, a genesis
// document for the fork schedule and minted supply, and a node that will not
// mint a block of its own before the import lands.
func TestStateSyncFlagValidation(t *testing.T) {
	key := []byte{1}
	var pin [32]byte

	cases := []struct {
		name      string
		validator bool
		trusted   []byte
		pin       *[32]byte
		genesis   string
		peers     []string
		wantErr   string
	}{
		// A validator may state-sync — it holds production until the snapshot
		// has landed — but it must have somewhere to take the snapshot from.
		{"validator with a peer", true, key, nil, "g.json", []string{"http://p"}, ""},
		{"validator with no peer", true, key, nil, "g.json", nil, "--peer"},
		{"no trust anchor", false, nil, nil, "g.json", []string{"http://p"}, "--validator-pubkey-hex"},
		{"no genesis source", false, key, nil, "", nil, "--genesis"},
		{"key and local genesis", false, key, nil, "g.json", nil, ""},
		{"pin and peer genesis", false, nil, &pin, "", []string{"http://p"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStateSyncFlags(tc.validator, tc.trusted, tc.pin, tc.genesis, tc.peers)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one naming %s", err, tc.wantErr)
			}
		})
	}
}

// State sync replaces the allocation apply: the node takes the fork schedule,
// chain id and minted supply from the pinned genesis document, and NOTHING
// else — an allocation written here would make the state non-fresh and
// commit balances the snapshot is about to overwrite anyway.
func TestPrepareStateSyncGenesisWritesBookkeepingOnly(t *testing.T) {
	withCleanChainID(t)
	sdb, ex := newBootstrapNode(t)
	g := forkGenesis(t, forks.Schedule{forks.MultiValidator: 1, forks.ValidatorsV1: 1, forks.ModuleRevertV1: 1})
	g.ChainID = "splendor-quantum-devnet-1"
	pin := g.Hash()

	if err := prepareStateSyncGenesis(g, &pin, "test", sdb, ex); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !sdb.IsFresh() {
		t.Fatal("state is no longer fresh: allocations were applied")
	}
	stored, ok, err := readStoredForkSchedule(sdb)
	if err != nil || !ok {
		t.Fatalf("fork schedule not persisted: ok=%v err=%v", ok, err)
	}
	if at, _ := stored.At(forks.MultiValidator); at != 1 {
		t.Fatalf("stored schedule %v", stored)
	}
	if at, _ := ex.Forks().At(forks.MultiValidator); at != 1 {
		t.Fatal("schedule not installed on the executor")
	}
	minted, ok, err := sdb.GetMeta(genesiscfg.MetaNativeMinted)
	if err != nil || !ok || string(minted) != g.SumAllocations().String() {
		t.Fatalf("minted supply = %q ok=%v err=%v, want %s", minted, ok, err, g.SumAllocations())
	}
	if types.ChainID != "splendor-quantum-devnet-1" {
		t.Fatalf("chain id not adopted: %s", types.ChainID)
	}

	wrong := g.Hash()
	wrong[0] ^= 0xff
	sdb2, ex2 := newBootstrapNode(t)
	if err := prepareStateSyncGenesis(g, &wrong, "test", sdb2, ex2); err == nil {
		t.Fatal("a document that does not match the pin was accepted")
	}
}

func TestShouldRunStateSync(t *testing.T) {
	if !shouldRunStateSync(true, nil, false) {
		t.Fatal("empty chain + --state-sync must run")
	}
	if shouldRunStateSync(false, nil, false) {
		t.Fatal("empty chain without --state-sync must not run")
	}
	if !shouldRunStateSync(true, &types.Block{}, true) {
		t.Fatal("incomplete import + --state-sync must run even with a stored head")
	}
	if shouldRunStateSync(true, &types.Block{}, false) {
		t.Fatal("a fully imported head must not re-enter state sync")
	}
}

func TestReconcileSnapshotImportAtStartupAllowsRetry(t *testing.T) {
	if err := classifyReconcileStartupError(nil, true); err != nil {
		t.Fatalf("nil: %v", err)
	}
	if err := classifyReconcileStartupError(block.ErrSnapshotImportIncomplete, true); err != nil {
		t.Fatalf("incomplete with retry: %v", err)
	}
	if err := classifyReconcileStartupError(block.ErrSnapshotImportIncomplete, false); err == nil {
		t.Fatal("incomplete without --state-sync must stay fatal")
	}
	if err := classifyReconcileStartupError(errors.New("stores disagree"), true); err == nil {
		t.Fatal("hard recovery errors must stay fatal")
	}
}
