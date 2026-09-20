// Package compiler is a from-scratch compiler for a Solidity-flavored subset
// ("QSol subset") that emits SQC-VM v4 bytecode. It is NOT consensus code:
// only the bytecode it produces is deployed, through an ordinary TxDeploy, and
// the node executes that bytecode exactly as it would any other. Nothing here
// runs inside a validator.
//
// See docs/plans/2026-08-31-sqcvm-contract-compiler.md for the design.
package compiler

import (
	"fmt"
	"math/big"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// The assembler turns a linear list of items into bytecode, resolving label
// references to absolute byte offsets. Jump targets are always emitted as a
// fixed-width PUSH2 (2-byte operand, up to 64 KiB of code) so that every
// item's address is known in a single pass — there is no PUSH-width/label
// fixpoint to iterate. Non-jump constants use the minimal PUSH width.

type itemKind uint8

const (
	kindOp        itemKind = iota // a bare opcode
	kindPush                      // PUSH of a constant (minimal width)
	kindPushLabel                 // PUSH2 of a label's resolved address
	kindLabel                     // a JUMPDEST marker naming a position
)

type item struct {
	kind  itemKind
	op    byte
	value *big.Int
	label string
}

// asm accumulates items and assembles them.
type asm struct {
	items []item
	// bytes is the assembled length of items so far, kept as they are appended
	// so codegen can bound the SIZE of what it generates and not only the
	// number of walk steps it took to get there. It is exact, not an estimate:
	// every append charges the same item.size() assemble() will charge.
	bytes int
}

func newAsm() *asm { return &asm{} }

// add appends one item and charges its assembled length. Every emitter goes
// through here so the running size cannot drift from the item list.
func (a *asm) add(it item) {
	a.items = append(a.items, it)
	a.bytes += it.size()
}

// op appends a bare opcode.
func (a *asm) op(op byte) { a.add(item{kind: kindOp, op: op}) }

// pushInt appends a minimal-width PUSH of a non-negative integer.
func (a *asm) pushInt(v int64) { a.pushBig(big.NewInt(v)) }

// pushBig appends a minimal-width PUSH of a non-negative big integer.
func (a *asm) pushBig(v *big.Int) {
	a.add(item{kind: kindPush, value: new(big.Int).Set(v)})
}

// pushBytes appends a PUSH of a fixed 32-byte value (e.g. an event topic).
func (a *asm) pushBytes32(b [32]byte) {
	a.add(item{kind: kindPush, value: new(big.Int).SetBytes(b[:])})
}

// pushLabel appends a fixed PUSH2 of the address label resolves to.
func (a *asm) pushLabel(name string) {
	a.add(item{kind: kindPushLabel, label: name})
}

// label marks the current position with a JUMPDEST that name resolves to.
func (a *asm) label(name string) {
	a.add(item{kind: kindLabel, label: name})
}

// pushWidth returns the number of operand bytes a minimal PUSH of v needs
// (at least 1, at most 32).
func pushWidth(v *big.Int) int {
	n := len(v.Bytes())
	if n == 0 {
		return 1
	}
	if n > 32 {
		panic("compiler: constant wider than 256 bits")
	}
	return n
}

// size returns the assembled byte length of one item.
func (it item) size() int {
	switch it.kind {
	case kindOp:
		return 1
	case kindPush:
		return 1 + pushWidth(it.value)
	case kindPushLabel:
		return 3 // PUSH2 + 2 operand bytes
	case kindLabel:
		return 1 // the JUMPDEST byte
	default:
		panic("compiler: unknown item kind")
	}
}

// assemble resolves labels and returns the bytecode.
func (a *asm) assemble() ([]byte, error) {
	// Pass 1: address of each label (the offset of its JUMPDEST byte).
	labels := map[string]int{}
	addr := 0
	for _, it := range a.items {
		if it.kind == kindLabel {
			if _, dup := labels[it.label]; dup {
				return nil, fmt.Errorf("duplicate label %q", it.label)
			}
			labels[it.label] = addr
		}
		addr += it.size()
	}
	if addr > 0xFFFF {
		return nil, fmt.Errorf("code is %d bytes, above the 64 KiB label ceiling", addr)
	}

	// Pass 2: emit.
	out := make([]byte, 0, addr)
	for _, it := range a.items {
		switch it.kind {
		case kindOp:
			out = append(out, it.op)
		case kindPush:
			w := pushWidth(it.value)
			out = append(out, vm.OpPUSH1+byte(w-1))
			b := it.value.Bytes()
			pad := make([]byte, w)
			copy(pad[w-len(b):], b)
			out = append(out, pad...)
		case kindPushLabel:
			target, ok := labels[it.label]
			if !ok {
				return nil, fmt.Errorf("reference to undefined label %q", it.label)
			}
			out = append(out, vm.OpPUSH1+1) // PUSH2
			out = append(out, byte(target>>8), byte(target))
		case kindLabel:
			out = append(out, vm.OpJUMPDEST)
		}
	}
	return out, nil
}
