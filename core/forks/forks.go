// Package forks holds the chain's rule-change schedule: which consensus rules
// take effect at which block height.
//
// A chain that has no way to change a rule at a height can only change one by
// having every node upgrade at the same instant. Nodes that restart minutes
// apart then disagree about whether a block is valid, which is not a slow
// rollout — it is a partition. The alternative operators reach for is wiping
// the chain and starting again from a new genesis, which is why rule changes
// on a chain without a schedule tend to cost the history.
//
// The schedule therefore lives in the genesis document, where it is fixed
// before block 0, is covered by the genesis hash operators pin, and is
// identical on every node by construction rather than by coordination.
//
// A height of 0 (or an absent name) means "never" rather than "at genesis".
// Genesis is height 0, so treating 0 as active would silently switch on every
// unset rule from the first block — the opposite of the intended default, and
// the kind of default that is discovered in production.
package forks

import (
	"fmt"
	"sort"
	"strings"
)

// MultiValidator is the name of the upgrade that moves the chain from a single
// block producer to a validator set with quorum certificates.
//
// It is declared here, unset, before it exists. That is the point: the schedule
// has to be in genesis to be agreed on, and genesis cannot be edited later, so
// the name has to be reservable before the code that honours it is written.
const MultiValidator = "multi_validator"

// Known lists every upgrade name this build understands.
//
// An unrecognised name in genesis is refused rather than ignored. A typo in a
// fork name is otherwise invisible: the node reports a healthy schedule, the
// rule never activates, and nobody finds out until the height passes without
// anything happening.
var Known = []string{MultiValidator}

// Schedule maps an upgrade name to the height at which it takes effect.
type Schedule map[string]uint64

// Active reports whether an upgrade is in force at the given height.
//
// The comparison is >=, so the named height is the FIRST block produced under
// the new rules, not the last one under the old.
func (s Schedule) Active(name string, height uint64) bool {
	if s == nil {
		return false
	}
	at, ok := s[name]
	if !ok || at == 0 {
		return false
	}
	return height >= at
}

// At returns the activation height for an upgrade, and whether it is scheduled.
func (s Schedule) At(name string) (uint64, bool) {
	if s == nil {
		return 0, false
	}
	at, ok := s[name]
	if !ok || at == 0 {
		return 0, false
	}
	return at, true
}

// Validate refuses a schedule this build cannot honour.
func (s Schedule) Validate() error {
	for name := range s {
		if !isKnown(name) {
			return fmt.Errorf(
				"genesis schedules unknown upgrade %q at height %d: this build understands only %s. "+
					"Running it would mean ignoring a rule change the rest of the network applies",
				name, s[name], strings.Join(Known, ", "))
		}
	}
	return nil
}

// Names returns the scheduled upgrade names in a stable order, for logging.
func (s Schedule) Names() []string {
	out := make([]string, 0, len(s))
	for name := range s {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func isKnown(name string) bool {
	for _, k := range Known {
		if k == name {
			return true
		}
	}
	return false
}
