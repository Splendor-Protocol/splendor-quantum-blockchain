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
// A rule decided AFTER launch is the remaining case, and it is a real one: a
// consensus bug found at height 100k cannot wait for the next network. Such an
// upgrade may be ADDED to a running chain's schedule — never one already in it,
// and only at a height above the chain's head, so no applied block is
// retroactively judged under a rule adopted later. That is deliberately an
// operator action (`sqc-node --allow-fork-extension`), not an automatic one:
// the whole network must be on the new document before the activation height,
// and a node adopting a rule alone forks there. See cmd/node's
// extendForkSchedule for the reconciliation rules.
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

// The names below are reserved for the upgrades the settlement plan schedules.
// Each is added to Known only by the change that implements its gate — a name
// this build "knows" but does not honour would let a genesis document schedule
// a rule the binary silently fails to apply, the exact failure Validate exists
// to prevent. Until then a document scheduling one is refused at load.
const (
	// QRC20V2 routes the QRC-20 permit/allowance surface and gates the
	// IsContract guard on approve. Requires ModuleRevertV1 no later than
	// itself (see Validate).
	QRC20V2 = "qrc20_v2"
	// QRC1155V1 routes QRC-1155 behind an owner slot. Requires ModuleRevertV1
	// no later than itself (see Validate).
	QRC1155V1 = "qrc1155_v1"
	// ExchangeV1 activates the settlement module. It requires ModuleRevertV1 to
	// be in force no later than itself (see Validate).
	ExchangeV1 = "exchange_v1"
	// ModuleRevertV1 makes a failing module call a failed receipt instead of a
	// dropped transaction.
	ModuleRevertV1 = "module_revert_v1"
	// VMRevertV1 makes a top-level bytecode REVERT atomic: the entry frame's
	// storage writes and logs roll back with it (the engine snapshots only
	// sub-calls), while the fee stays charged and the nonce consumed. Without
	// it, a contract that writes then reverts keeps the write although its
	// receipt says "execution reverted".
	VMRevertV1 = "vm_revert_v1"
	// BridgeV1 activates the one-way legacy-mainnet migration bridge module.
	// Requires ModuleRevertV1 no later than itself (see Validate).
	BridgeV1 = "bridge_v1"
	// ValidatorGatesV1 routes the staking module and turns on the two gates a
	// block producer must pass: an admission pass held against its ML-DSA
	// consensus key, and a bond at or above the minimum stake. Before it the
	// staking module is unreachable and any key the chain accepts signatures
	// from may produce, which is what single-validator Phase 1 has always
	// been. Requires ModuleRevertV1 no later than itself (see Validate).
	//
	// It does NOT make the chain multi-validator: that is MultiValidator, and
	// it remains unimplemented. What this upgrade decides is WHO is allowed to
	// produce, not how many of them agree.
	ValidatorGatesV1 = "validator_gates_v1"
	// VerifyV1 routes the native SQVL verify/settlement module (dual-signed
	// ML-DSA-65 + SLH-DSA instructions). Requires ModuleRevertV1 no later
	// than itself (see Validate).
	VerifyV1 = "verify_v1"
	// MinFeeV1 raises the transaction fee floor from the launch value of
	// 1,000 units to 0.001 SPLD (10^15 units): the mempool refuses cheaper
	// transactions and the producer's pre-tx hook refuses to include them.
	// Admission-only — a block sealed before the height is judged by the old
	// floor. Standalone: it needs no module and no module_revert_v1.
	MinFeeV1 = "min_fee_v1"
	// QRC721V1 routes the QRC-721 NFT module (owner-only mint). Requires
	// ModuleRevertV1 no later than itself (see Validate).
	QRC721V1 = "qrc721_v1"
)

// Known lists every upgrade name this build understands.
//
// An unrecognised name in genesis is refused rather than ignored. A typo in a
// fork name is otherwise invisible: the node reports a healthy schedule, the
// rule never activates, and nobody finds out until the height passes without
// anything happening.
var Known = []string{MultiValidator, ModuleRevertV1, QRC20V2, QRC1155V1, ExchangeV1, VMRevertV1, BridgeV1, ValidatorGatesV1, VerifyV1, MinFeeV1, QRC721V1}

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
//
// Names are checked in sorted order so two runs over the same document report
// the same offending name.
func (s Schedule) Validate() error {
	for _, name := range s.Names() {
		if !isKnown(name) {
			return fmt.Errorf(
				"genesis schedules unknown upgrade %q at height %d: this build understands only %s. "+
					"Running it would mean ignoring a rule change the rest of the network applies",
				name, s[name], strings.Join(Known, ", "))
		}
	}
	// Every routed module relies on a failed module call being a failed
	// receipt rather than a dropped transaction: an operator whose match
	// fails permanently, or a holder whose permit or transfer fails SOFT,
	// would otherwise jam its account at that nonce. Scheduling one of them
	// before (or without) that rule describes a chain that cannot run it
	// safely, so the document is refused rather than honoured in part. The
	// dependents are checked in sorted order for a stable report.
	for _, name := range []string{BridgeV1, ExchangeV1, QRC1155V1, QRC20V2, QRC721V1, ValidatorGatesV1, VerifyV1} {
		at, ok := s.At(name)
		if !ok {
			continue
		}
		mrAt, ok := s.At(ModuleRevertV1)
		if !ok {
			return fmt.Errorf("genesis schedules %s at height %d but never schedules %s, which it depends on",
				name, at, ModuleRevertV1)
		}
		if mrAt > at {
			return fmt.Errorf("genesis schedules %s at height %d but %s only at height %d: "+
				"the dependency must be in force no later than the upgrade that needs it",
				name, at, ModuleRevertV1, mrAt)
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
