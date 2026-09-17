package forks

import (
	"strings"
	"testing"
)

// Genesis is height 0, so treating an unset height as 0 would switch every
// unscheduled rule on from the very first block — the opposite of the default
// anyone would expect, and one that only shows up in production.
func TestZeroAndAbsentMeanNever(t *testing.T) {
	for name, s := range map[string]Schedule{
		"nil":      nil,
		"empty":    {},
		"explicit": {MultiValidator: 0},
	} {
		for _, h := range []uint64{0, 1, 1 << 40} {
			if s.Active(MultiValidator, h) {
				t.Fatalf("%s schedule reported active at height %d", name, h)
			}
		}
	}
}

// The scheduled height is the FIRST block under the new rules, not the last
// under the old — an off-by-one here splits the network at the fork.
func TestActivationHeightIsInclusive(t *testing.T) {
	s := Schedule{MultiValidator: 100}
	if s.Active(MultiValidator, 99) {
		t.Fatal("must not be active one block early")
	}
	if !s.Active(MultiValidator, 100) {
		t.Fatal("must be active at the scheduled height itself")
	}
	if !s.Active(MultiValidator, 101) {
		t.Fatal("must stay active after the scheduled height")
	}
}

func TestAtReportsScheduling(t *testing.T) {
	if _, ok := (Schedule{}).At(MultiValidator); ok {
		t.Fatal("an unscheduled upgrade must report not-scheduled")
	}
	if _, ok := (Schedule{MultiValidator: 0}).At(MultiValidator); ok {
		t.Fatal("height 0 means never, so it is not scheduled")
	}
	at, ok := Schedule{MultiValidator: 42}.At(MultiValidator)
	if !ok || at != 42 {
		t.Fatalf("At = (%d, %v), want (42, true)", at, ok)
	}
}

func TestBFTRoundsV1UnscheduledMeansNever(t *testing.T) {
	if !isKnown(BFTRoundsV1) {
		t.Fatal("bft_rounds_v1 must be a known name so a genesis can schedule it")
	}
	if (Schedule{}).Active(BFTRoundsV1, 1) || (Schedule{BFTRoundsV1: 0}).Active(BFTRoundsV1, 1) {
		t.Fatal("unscheduled bft_rounds_v1 must keep the single-vote path")
	}
	if err := (Schedule{BFTRoundsV1: 100}).Validate(); err != nil {
		t.Fatalf("scheduling bft_rounds_v1 must be accepted: %v", err)
	}
}

func TestVMGasV1UnscheduledMeansNever(t *testing.T) {
	if !isKnown(VMGasV1) {
		t.Fatal("vm_gas_v1 must be a known name so a genesis can schedule it")
	}
	if (Schedule{}).Active(VMGasV1, 1) || (Schedule{VMGasV1: 0}).Active(VMGasV1, 1) {
		t.Fatal("unscheduled vm_gas_v1 must keep pre-fork child gas and drop OOG txs")
	}
	if err := (Schedule{VMGasV1: 100}).Validate(); err != nil {
		t.Fatalf("scheduling vm_gas_v1 must be accepted: %v", err)
	}
}

func TestVerifyIdentityV1UnscheduledMeansNever(t *testing.T) {
	if !isKnown(VerifyIdentityV1) {
		t.Fatal("verify_identity_v1 must be a known name so a genesis can schedule it")
	}
	if (Schedule{}).Active(VerifyIdentityV1, 1) || (Schedule{VerifyIdentityV1: 0}).Active(VerifyIdentityV1, 1) {
		t.Fatal("unscheduled verify_identity_v1 must keep unbound SLH and no-op x402 spend")
	}
	if err := (Schedule{VerifyIdentityV1: 100, VerifyV1: 1, ModuleRevertV1: 1}).Validate(); err != nil {
		t.Fatalf("scheduling verify_identity_v1 must be accepted: %v", err)
	}
	if err := (Schedule{VerifyIdentityV1: 1}).Validate(); err == nil {
		t.Fatal("scheduling verify_identity_v1 without verify_v1 must be refused")
	}
}

func TestUnknownNameRefused(t *testing.T) {
	if err := (Schedule{"nope": 1}).Validate(); err == nil {
		t.Fatal("unknown upgrade names must be refused")
	}
	if err := (Schedule{MultiValidator: 1}).Validate(); err != nil {
		t.Fatalf("known name should validate: %v", err)
	}
}

func TestNamesAreSorted(t *testing.T) {
	got := Schedule{MultiValidator: 1}.Names()
	if len(got) != 1 || got[0] != MultiValidator {
		t.Fatalf("Names = %v", got)
	}
}

// A settlement-plan name is honoured only once its gate exists. A document
// scheduling one this build does not implement must be refused, not accepted
// and silently ignored; each name flipped to accepted in the change that
// implemented its gate (module_revert_v1 with the executor's module-revert
// branch, qrc20_v2 with the routed QRC-20 v2 surface, qrc1155_v1 with the
// owned, routed QRC-1155, exchange_v1 with the routed settlement module). The
// refused list is empty now that every reserved name is implemented; a future
// reserved name goes back into it until its gate lands.
func TestReservedNamesAreRefusedUntilImplemented(t *testing.T) {
	for _, name := range []string{} {
		if isKnown(name) {
			t.Fatalf("%s is listed as known: only the change implementing its gate may add it", name)
		}
		if err := (Schedule{name: 1}).Validate(); err == nil {
			t.Fatalf("scheduling %s must be refused until its gate exists", name)
		}
	}
	if !isKnown(ModuleRevertV1) {
		t.Fatalf("%s has a gate in this build but is not listed as known", ModuleRevertV1)
	}
	if err := (Schedule{ModuleRevertV1: 1}).Validate(); err != nil {
		t.Fatalf("scheduling %s must be accepted by this build: %v", ModuleRevertV1, err)
	}
	// The routed modules are known too; their dependency rule refuses each
	// alone (a SOFT failure would drop the tx and pin the nonce), so each is
	// tried with module_revert_v1 — what is on trial here is the name.
	for _, name := range []string{
		QRC20V2, QRC1155V1, ExchangeV1, BridgeV1,
		VerifyV1, ValidatorsV1, LendingV1, OracleV1, MultisigV1, GovernanceV1, QRC721V1, QRC4626V1,
	} {
		if !isKnown(name) {
			t.Fatalf("%s has a gate in this build but is not listed as known", name)
		}
		if err := (Schedule{name: 1}).Validate(); err == nil || !strings.Contains(err.Error(), ModuleRevertV1) {
			t.Fatalf("scheduling %s without %s must be refused, got %v", name, ModuleRevertV1, err)
		}
		if err := (Schedule{name: 1, ModuleRevertV1: 1}).Validate(); err != nil {
			t.Fatalf("scheduling %s must be accepted by this build: %v", name, err)
		}
	}
}

// exchange_v1 needs module_revert_v1 in force no later than itself, or a
// failed match would jam the operator's account instead of producing a failed
// receipt. A schedule that gets the order wrong describes a chain that cannot
// run the exchange safely, and is refused whole.
func TestExchangeRequiresModuleRevertNoLaterThanItself(t *testing.T) {
	for name, tc := range map[string]struct {
		s  Schedule
		ok bool
	}{
		"both at 1":              {Schedule{ExchangeV1: 1, ModuleRevertV1: 1}, true},
		"revert earlier":         {Schedule{ExchangeV1: 10, ModuleRevertV1: 5}, true},
		"revert alone":           {Schedule{ModuleRevertV1: 1}, true},
		"exchange alone":         {Schedule{ExchangeV1: 1}, false},
		"revert never (0)":       {Schedule{ExchangeV1: 1, ModuleRevertV1: 0}, false},
		"revert later":           {Schedule{ExchangeV1: 5, ModuleRevertV1: 6}, false},
		"exchange never, no dep": {Schedule{ExchangeV1: 0}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want refused, got nil")
			}
		})
	}
}

// With two unknown names the error must name the same one every run, or the
// operator chasing it sees a different message on each restart.
func TestValidateReportsUnknownNamesDeterministically(t *testing.T) {
	s := Schedule{"zzz": 1, "aaa": 1}
	first := s.Validate()
	if first == nil {
		t.Fatal("unknown names must be refused")
	}
	for i := 0; i < 50; i++ {
		if got := s.Validate(); got.Error() != first.Error() {
			t.Fatalf("error text varies across runs: %q vs %q", got, first)
		}
	}
}

// validators_admission_v1 qualifies register, so it needs validators_v1 (and,
// as a routed module rule, module_revert_v1) in force no later than itself.
func TestValidatorsAdmissionRequiresValidatorsV1NoLaterThanItself(t *testing.T) {
	for name, tc := range map[string]struct {
		s  Schedule
		ok bool
	}{
		"all at 1":            {Schedule{ValidatorsAdmissionV1: 1, ValidatorsV1: 1, ModuleRevertV1: 1}, true},
		"validators earlier":  {Schedule{ValidatorsAdmissionV1: 10, ValidatorsV1: 5, ModuleRevertV1: 1}, true},
		"validators alone":    {Schedule{ValidatorsV1: 1, ModuleRevertV1: 1}, true},
		"admission alone":     {Schedule{ValidatorsAdmissionV1: 1, ModuleRevertV1: 1}, false},
		"validators later":    {Schedule{ValidatorsAdmissionV1: 5, ValidatorsV1: 6, ModuleRevertV1: 1}, false},
		"no module_revert":    {Schedule{ValidatorsAdmissionV1: 1, ValidatorsV1: 1}, false},
		"admission never (0)": {Schedule{ValidatorsAdmissionV1: 0, ValidatorsV1: 1, ModuleRevertV1: 1}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want refused, got nil")
			}
		})
	}
}
