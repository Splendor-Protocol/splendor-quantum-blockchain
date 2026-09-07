package main

import (
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// forkGenesis builds an honest genesis carrying an arbitrary schedule.
func forkGenesis(t *testing.T, s forks.Schedule) *genesiscfg.Genesis {
	t.Helper()
	g := honestGenesis(t)
	g.Forks = s
	return g
}

// A rule discovered AFTER a chain launched has to be schedulable on it, or the
// only way to fix consensus behaviour is to wipe the chain — the very outcome
// the fork schedule exists to avoid. An upgrade the stored schedule does not
// carry is therefore accepted from --genesis when it activates in the FUTURE
// and the operator asked for it explicitly; the merged schedule is persisted,
// so it is the chain's schedule from then on.
func TestForkExtensionAddsFutureUpgrade(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	extended := saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	}))
	got, err := loadForkSchedule(extended, sdb, false, forkPolicy{Head: 100, AllowExtension: true})
	if err != nil {
		t.Fatalf("extension refused: %v", err)
	}
	if at, ok := got.At(forks.VMRevertV1); !ok || at != 200 {
		t.Fatalf("schedule = %v, want %s at 200", got, forks.VMRevertV1)
	}
	if at, ok := got.At(forks.MultiValidator); !ok || at != 1 {
		t.Fatalf("schedule = %v, want the existing %s kept at 1", got, forks.MultiValidator)
	}

	// Persisted: the chain's schedule now IS the merged one.
	stored, ok, err := readStoredForkSchedule(sdb)
	if err != nil || !ok {
		t.Fatalf("read stored schedule: %v (ok=%v)", err, ok)
	}
	if at, ok := stored.At(forks.VMRevertV1); !ok || at != 200 {
		t.Fatalf("stored = %v, want the extension persisted", stored)
	}
}

// Adopting a new rule is a network-wide decision, not a side effect of editing
// a file. Without the operator flag the node refuses and names the flag.
func TestForkExtensionRefusedWithoutFlag(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	extended := saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	}))
	_, err := loadForkSchedule(extended, sdb, false, forkPolicy{Head: 100})
	if err == nil {
		t.Fatal("an unrequested schedule extension was adopted silently")
	}
	if !strings.Contains(err.Error(), "--allow-fork-extension") {
		t.Fatalf("error %q does not name the flag that permits this", err)
	}
	// Nothing persisted on the refusal.
	stored, _, _ := readStoredForkSchedule(sdb)
	if _, ok := stored.At(forks.VMRevertV1); ok {
		t.Fatalf("stored = %v, want the refused extension NOT persisted", stored)
	}
}

// An activation at or below the current head would mean blocks already applied
// were judged under rules this node is only now adopting: the schedule and the
// chain would disagree about history. Refused even with the flag.
func TestForkExtensionRefusesActivationAtOrBelowHead(t *testing.T) {
	for _, at := range []uint64{1, 50, 100} {
		sdb, ex := newBootstrapNode(t)
		if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		extended := saveGenesis(t, forkGenesis(t, forks.Schedule{
			forks.MultiValidator: 1,
			forks.VMRevertV1:     at,
		}))
		_, err := loadForkSchedule(extended, sdb, false, forkPolicy{Head: 100, AllowExtension: true})
		if err == nil {
			t.Fatalf("activation at %d (head 100) was accepted: it rewrites applied history", at)
		}
	}
}

// Changing the height of an upgrade the chain already runs under is not an
// extension — it is a different chain. Refused even with the flag.
func TestForkExtensionRefusesChangedHeightOfExistingUpgrade(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	moved := saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 5}))
	if _, err := loadForkSchedule(moved, sdb, false, forkPolicy{Head: 100, AllowExtension: true}); err == nil {
		t.Fatal("moving an existing upgrade's activation height was accepted")
	}
}

// Dropping an upgrade the chain runs under is a downgrade, not an extension.
func TestForkExtensionRefusesDroppedUpgrade(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	dropped := saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1}))
	if _, err := loadForkSchedule(dropped, sdb, false, forkPolicy{Head: 100, AllowExtension: true}); err == nil {
		t.Fatal("dropping a scheduled upgrade was accepted")
	}
}

// The flag is needed for the run that ADOPTS the extension, not forever: once
// persisted, the merged schedule is what the chain was started under, so an
// ordinary restart matches it and needs no flag.
func TestForkExtensionPersistsSoRestartNeedsNoFlag(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	extended := saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	}))
	if _, err := loadForkSchedule(extended, sdb, false, forkPolicy{Head: 100, AllowExtension: true}); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	got, err := loadForkSchedule(extended, sdb, false, forkPolicy{Head: 150})
	if err != nil {
		t.Fatalf("restart after adopting the extension: %v", err)
	}
	if at, ok := got.At(forks.VMRevertV1); !ok || at != 200 {
		t.Fatalf("schedule = %v, want the adopted extension", got)
	}
}

// A height of 0 is "never" (package forks), so adding one reserves a NAME
// without changing any rule. That is the documented way to name an upgrade
// before the code honouring it exists, so it needs no flag and no future
// height — there is nothing to activate.
func TestForkExtensionAcceptsReservedNameAtHeightZero(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	reserved := saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     0,
	}))
	got, err := loadForkSchedule(reserved, sdb, false, forkPolicy{Head: 100})
	if err != nil {
		t.Fatalf("reserving a name was refused: %v", err)
	}
	if _, ok := got.At(forks.VMRevertV1); ok {
		t.Fatalf("schedule = %v, want %s reserved but NOT active", got, forks.VMRevertV1)
	}
}

// A chain that scheduled NOTHING (mainnet's position) can still be extended,
// and an upgrade with a dependency must bring the dependency with it in the
// same document: Schedule.Validate requires module_revert_v1 no later than the
// routed-module upgrades, so adding one alone is refused rather than adopted
// into a schedule the chain cannot run.
func TestForkExtensionOnEmptyScheduleCarriesDependencies(t *testing.T) {
	newChain := func(t *testing.T) *state.StateDB {
		t.Helper()
		sdb, ex := newBootstrapNode(t)
		if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{})), sdb, ex, genesisPolicy{}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		return sdb
	}

	// The dependent alone: refused, naming the dependency it lacks.
	alone := saveGenesis(t, forkGenesis(t, forks.Schedule{forks.ExchangeV1: 200}))
	_, err := loadForkSchedule(alone, newChain(t), false, forkPolicy{Head: 100, AllowExtension: true})
	if err == nil {
		t.Fatal("adopted an upgrade whose dependency the chain does not schedule")
	}
	if !strings.Contains(err.Error(), forks.ModuleRevertV1) {
		t.Fatalf("refusal does not name the missing dependency: %v", err)
	}

	// Both together, dependency no later than the dependent: accepted.
	together := saveGenesis(t, forkGenesis(t, forks.Schedule{
		forks.ModuleRevertV1: 200,
		forks.ExchangeV1:     200,
	}))
	got, err := loadForkSchedule(together, newChain(t), false, forkPolicy{Head: 100, AllowExtension: true})
	if err != nil {
		t.Fatalf("an upgrade adopted with its dependency was refused: %v", err)
	}
	for _, name := range []string{forks.ModuleRevertV1, forks.ExchangeV1} {
		if at, ok := got.At(name); !ok || at != 200 {
			t.Fatalf("schedule = %v, want %s at 200", got, name)
		}
	}
}

// The fork schedule is part of the genesis hash, so an EXTENDED document is by
// construction not the one an operator pinned with --genesis-doc-hash. The pin
// says "this exact document is my network's identity"; adopting a different one
// behind its back would undercut that, and would mean anyone who can write
// genesis.json on a host where --allow-fork-extension was left on could
// schedule a consensus rule change. Adopting a new document therefore takes two
// deliberate changes: the flag AND an updated pin.
func TestForkExtensionRefusesADocumentThatIsNotThePinned(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	original := forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})
	if _, err := applyLocalGenesis(saveGenesis(t, original), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stalePin := original.Hash()

	extended := forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	})
	path := saveGenesis(t, extended)

	_, err := loadForkSchedule(path, sdb, false, forkPolicy{
		Head: 100, AllowExtension: true, DocHash: &stalePin,
	})
	if err == nil {
		t.Fatal("adopted a document that does not match the operator's pin")
	}
	if !strings.Contains(err.Error(), "--genesis-doc-hash") {
		t.Fatalf("refusal does not name the pin the operator must update: %v", err)
	}
}

// Re-pinning to the new document is the operator saying "yes, this one" — and
// then the extension proceeds.
func TestForkExtensionAcceptsTheDocumentMatchingItsPin(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	extended := forkGenesis(t, forks.Schedule{
		forks.MultiValidator: 1,
		forks.VMRevertV1:     200,
	})
	freshPin := extended.Hash()
	got, err := loadForkSchedule(saveGenesis(t, extended), sdb, false, forkPolicy{
		Head: 100, AllowExtension: true, DocHash: &freshPin,
	})
	if err != nil {
		t.Fatalf("a correctly re-pinned document was refused: %v", err)
	}
	if at, ok := got.At(forks.VMRevertV1); !ok || at != 200 {
		t.Fatalf("schedule = %v, want the extension adopted", got)
	}
}

// An unchanged file on a scheduled chain still needs no flag — the ordinary
// restart path must not regress.
func TestUnchangedScheduleNeedsNoFlag(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	same := forkGenesis(t, forks.Schedule{forks.MultiValidator: 1})
	path := saveGenesis(t, same)
	if _, err := applyLocalGenesis(path, sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := loadForkSchedule(path, sdb, false, forkPolicy{Head: 100})
	if err != nil {
		t.Fatalf("unchanged schedule refused: %v", err)
	}
	if at, ok := got.At(forks.MultiValidator); !ok || at != 1 {
		t.Fatalf("schedule = %v, want %s at 1", got, forks.MultiValidator)
	}
}
