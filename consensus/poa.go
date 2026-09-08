// Package consensus — poa.go: a round-robin producer schedule for running the
// validator gates with more than one producer.
//
// # This is a harness, not Phase 2
//
// `multi_validator` is unimplemented (bft.go says so at length): no vote
// gossip, no equivocation detection, no round timeouts, no fork choice. This
// file adds none of them. What it adds is the one thing missing between "a
// second passed-and-bonded validator is eligible" and an observable
// multi-producer chain: something that gives it a turn.
//
// The schedule is deterministic and needs no messages:
//
//	eligible(h) := [p for p in producers if the chain admits p at h-1]
//	owner(h)    := eligible(h)[h mod len(eligible(h))]
//
// `producers` is an ordered list every node is configured with identically —
// that is the PoA part, and the part a real consensus would derive from state
// instead. Everything else comes from the chain: the filter is
// Executor.EligibleToProduceAddr, which is the same vm.StakingContract.
// CanProduce that ProduceBlock and ExecuteBlock enforce, read against the same
// state at h-1. So the gates are not modelled here, they are *consulted* here,
// and a key the chain stops admitting stops being scheduled — on every node, at
// the same height, without anyone announcing it.
//
// What this deliberately does NOT provide:
//
//   - Liveness under failure. A scheduled producer that is merely *down* still
//     owns its slot, because "down" is not a fact the state carries and the
//     other nodes cannot agree on it without the view change this does not
//     implement. The chain stops until it returns. A timeout that let the next
//     node take the slot would let two nodes take it, which is a fork.
//   - Byzantine safety. Nothing here detects a producer that seals two blocks
//     at one height, and nothing punishes it.
//
// Both are why this is a proof harness for the gates and not a consensus
// engine, and why it lives behind an explicit flag that is off by default.
package consensus

import (
	"errors"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// ErrNoEligibleProducer is returned when the chain admits none of the
// configured producers for a height. On a gated chain this should be
// unreachable — the staking module refuses the action that would remove the
// last one that can produce — so it means the configured list and the chain's
// registry have drifted apart.
var ErrNoEligibleProducer = errors.New("no configured producer is admitted by the chain at this height")

// Slots names which producer may seal a height. Validator.Config takes one;
// nil means the Phase-1 single producer, for which every height is its own.
type Slots interface {
	SlotOwner(height uint64) (qcrypto.Address, error)
}

// ProducerGate is the chain's own eligibility predicate, satisfied by
// *block.Executor. Taking it as an interface keeps the direction of the
// dependency honest: the schedule asks the gate, never the other way round.
type ProducerGate interface {
	EligibleToProduceAddr(addr qcrypto.Address, height uint64) error
}

// RoundRobin is the schedule described in the package comment.
//
// It holds no state of its own beyond its configuration: every answer is
// recomputed from the chain, so two nodes that agree on the producer list and
// on the chain agree on the schedule without exchanging a message.
type RoundRobin struct {
	producers []qcrypto.Address
	gate      ProducerGate
}

// NewRoundRobin validates the producer list and binds it to the chain's gate.
// Order matters and duplicates are refused: the list is consensus input, and a
// node that ordered it differently from its peers would expect a different
// producer for every height.
func NewRoundRobin(producers []qcrypto.Address, gate ProducerGate) (*RoundRobin, error) {
	if gate == nil {
		return nil, errors.New("round-robin schedule: nil producer gate")
	}
	if len(producers) == 0 {
		return nil, errors.New("round-robin schedule: no producers configured")
	}
	seen := make(map[qcrypto.Address]struct{}, len(producers))
	for i, p := range producers {
		if p.IsZero() {
			return nil, fmt.Errorf("round-robin schedule: producer %d is the zero address", i)
		}
		if _, dup := seen[p]; dup {
			return nil, fmt.Errorf("round-robin schedule: %s appears twice", p)
		}
		seen[p] = struct{}{}
	}
	return &RoundRobin{
		producers: append([]qcrypto.Address(nil), producers...),
		gate:      gate,
	}, nil
}

// Producers returns the configured list, in order. The copy is deliberate: the
// caller must not be able to reorder the schedule by holding it.
func (r *RoundRobin) Producers() []qcrypto.Address {
	return append([]qcrypto.Address(nil), r.producers...)
}

// Eligible returns the configured producers the chain admits for `height`, in
// configured order, along with the reason each excluded one was excluded.
//
// The reasons are the gate's own error strings, so an operator asking why a
// node is silent gets the answer consensus would give rather than a second
// account of it.
func (r *RoundRobin) Eligible(height uint64) ([]qcrypto.Address, map[qcrypto.Address]error) {
	admitted := make([]qcrypto.Address, 0, len(r.producers))
	refused := make(map[qcrypto.Address]error)
	for _, p := range r.producers {
		if err := r.gate.EligibleToProduceAddr(p, height); err != nil {
			refused[p] = err
			continue
		}
		admitted = append(admitted, p)
	}
	return admitted, refused
}

// SlotOwner returns the producer scheduled to seal `height`.
//
// The rotation runs over the ADMITTED producers, not over the configured ones,
// so revoking a pass or dropping a bond below the floor removes that key from
// the rotation instead of leaving it an empty slot the chain would stall on.
// Every node computes this from the same committed state at height-1 and so
// reaches the same answer.
func (r *RoundRobin) SlotOwner(height uint64) (qcrypto.Address, error) {
	admitted, refused := r.Eligible(height)
	if len(admitted) == 0 {
		return qcrypto.Address{}, fmt.Errorf("%w (height %d, %d configured, %d refused)",
			ErrNoEligibleProducer, height, len(r.producers), len(refused))
	}
	return admitted[height%uint64(len(admitted))], nil
}

// Authorize reports whether `addr` was the producer scheduled for `height`,
// nil meaning yes. This is the follower half: without it any admitted producer
// could seal any height, and two of them sealing the same one is a fork.
//
// It is a schedule check ONLY. Whether the chain admits addr at all is the
// gate's decision and ExecuteBlock's to enforce; this says nothing about it
// beyond what SlotOwner already had to ask.
func (r *RoundRobin) Authorize(height uint64, addr qcrypto.Address) error {
	owner, err := r.SlotOwner(height)
	if err != nil {
		return err
	}
	if owner != addr {
		return fmt.Errorf("block %d was sealed by %s, but the round-robin schedule gives that slot to %s",
			height, addr, owner)
	}
	return nil
}
