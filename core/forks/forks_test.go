package forks

import "testing"

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
