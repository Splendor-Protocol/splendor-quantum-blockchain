package main

import (
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
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
