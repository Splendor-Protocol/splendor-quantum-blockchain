// Package block — the producer eligibility gate (validator_gates_v1).
//
// Who is allowed to seal a block is decided in exactly one place: the staking
// module's CanProduce (vm/staking.go). This file is what connects it to the two
// paths that matter — ProduceBlock, where a node judges itself before building,
// and ExecuteBlock, where it judges everyone else before applying. Both call
// the same predicate against the same state, so a producer cannot build a
// block its own followers refuse for a reason it never evaluated.
//
// The gate needs one thing the state does not carry: the address of the key
// that sealed block 0. That address is the chain's existing root of trust —
// every node already verifies every block signature against that key — and the
// staking module uses it as its bootstrap authority: the initial admission
// admin, and the one key passed without an entry having been written. The node
// supplies it (cmd/node reads it from the genesis document, the pinned
// validator key, or block 0 itself, and persists it).
package block

import (
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// WithGenesisValidator records the address of the key that sealed block 0,
// at construction time. Builder-style; use SetGenesisValidator on a running
// executor.
func (e *Executor) WithGenesisValidator(addr qcrypto.Address) *Executor {
	e.SetGenesisValidator(addr)
	return e
}

// SetGenesisValidator records the block-0 key's address on a running executor.
//
// It takes the block lock, so the anchor lands between two block transitions
// and never in the middle of one — the same discipline SetForks follows, and
// for the same reason: an anchor that changed halfway through a block would
// have some of that block's module calls judged against one authority and the
// rest against another.
//
// The zero address is stored as "unknown" rather than as an address, because
// no key derives to it and treating it as one would make an unconfigured node
// enforce admissions against an authority nobody holds.
func (e *Executor) SetGenesisValidator(addr qcrypto.Address) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if addr.IsZero() {
		e.genesisValidator.Store(nil)
		return
	}
	own := addr
	e.genesisValidator.Store(&own)
}

// GenesisValidator returns the recorded block-0 key address, or the zero
// address when this node has never been told it. Lock-free: it is read on
// every module call.
func (e *Executor) GenesisValidator() qcrypto.Address {
	if p := e.genesisValidator.Load(); p != nil {
		return *p
	}
	return qcrypto.Address{}
}

// EligibleToProduce reports whether the key may seal the block at `height`,
// nil meaning yes. It is what the consensus loop asks before building: a
// validator that is not eligible should stay quiet, not produce a block every
// one of its peers will reject.
// The key is enough: its address is that key's SHA3-256, so the gate derives
// what it needs. Nothing is decoded before the gate decides it applies — an
// ungated chain must behave exactly as it did, whatever it is handed.
func (e *Executor) EligibleToProduce(pubKey []byte, height uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requireEligibleProducer(pubKey, qcrypto.Address{}, height)
}

// EligibleToProduceAddr is EligibleToProduce for a caller that holds the
// producing ADDRESS rather than the key — a scheduler deciding whose slot a
// height is, or a follower judging a header. On this chain the address is the
// key's SHA3-256, so the registry is keyed by a value both callers have; the
// predicate, the state it reads and the answer it gives are identical.
//
// Same lock, same rule, no second implementation of it: a schedule that decided
// eligibility for itself would be free to disagree with the gate consensus
// actually enforces, which is the one thing a schedule must never do.
func (e *Executor) EligibleToProduceAddr(addr qcrypto.Address, height uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requireEligibleProducer(nil, addr, height)
}

// SetAttachProducerKey makes ProduceBlock embed the sealing key in each block
// it builds.
//
// A Phase-1 chain has one producer and every node pins its key, so blocks carry
// no key above height 0 and followers verify against the pinned one. With
// several producers that pin no longer names whoever sealed the block, so the
// block has to carry its own key and the follower has to bind it to the header
// (DeriveAddress(key) == Header.ValidatorAddr) before trusting the signature.
//
// Off by default. Turning it on is a network-wide decision — it changes what a
// follower must be prepared to verify, not just what this node emits.
func (e *Executor) SetAttachProducerKey(on bool) {
	e.attachProducerKey.Store(on)
}

// requireEligibleProducer is the gate itself. Callers hold e.mu.
//
//	pubKey — the producer's key when the caller has it (ProduceBlock always
//	         does; a block carries one only at height 0). Empty falls back to
//	         the address, which is inside the header and therefore covered by
//	         the block hash.
//	addr   — the producing address (b.Header.ValidatorAddr for a follower).
//	height — the height being produced or applied. The state is at height-1,
//	         which is what both sides judge against.
//
// Three things switch it off, each deliberate:
//
//   - Height 0. Block 0 IS the anchor; there is nothing yet to check it
//     against, and a chain that could not seal its own genesis could not
//     start.
//   - The upgrade not being active at this height. Before it, any key the
//     chain accepts signatures from may produce — which is what
//     single-validator Phase 1 has always been, and replaying old blocks must
//     not judge them under a rule adopted later.
//   - An UNTOUCHED registry on a node with NO ANCHOR. cmd/node refuses to
//     start a gated chain whose genesis validator it cannot determine, so this
//     should never be reached in production; it is the safety net for anything
//     that embeds the executor directly. It fails open rather than halting a
//     chain on a rule nobody has configured, and block signatures are still
//     checked against the pinned validator key, so it is not a door that was
//     otherwise shut. Note this is NOT the fork-extension case: a chain that
//     adopts the upgrade late reaches the activation height with an untouched
//     registry but a known anchor, and its existing producer is the genesis
//     validator, which is admitted implicitly.
//
// Note that block REPLAY (reapplyBlock, after a crash) does not come through
// here. Those blocks were already accepted once; re-judging them under a
// registry that has moved since would let a later revocation retroactively
// invalidate history a node has already committed to.
func (e *Executor) requireEligibleProducer(pubKey []byte, addr qcrypto.Address, height uint64) error {
	if height == 0 {
		return nil
	}
	if !e.schedule().Active(forks.ValidatorGatesV1, height) {
		return nil
	}
	genesisValidator := e.GenesisValidator()
	staking := vm.Staking(e.state)
	if genesisValidator.IsZero() {
		initialised, err := staking.Initialised()
		if err != nil {
			return err
		}
		if !initialised {
			return nil
		}
	}
	if len(pubKey) > 0 {
		return staking.CanProduceKey(genesisValidator, pubKey)
	}
	return staking.CanProduce(genesisValidator, addr)
}
