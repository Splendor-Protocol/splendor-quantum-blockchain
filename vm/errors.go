// Package vm — module-call failure classes.
//
// A native-module call can fail in two ways that the executor must tell apart:
//
//   - HARD: the call could never have been meant for this chain as it stands —
//     an unparseable envelope, an unknown module, an unknown action, or an
//     action whose fork is not active at this height. Every one of these is
//     statically detectable at admission, so a producer never packs one; a
//     block that still carries one was built by a node running different rules,
//     and the follower rejects the whole block ("unappliable tx") rather than
//     applying it and diverging on state root later.
//   - SOFT: everything an action itself returns (insufficient balance, not
//     owner, expired deadline, a StateDB read error, a recovered panic). Under
//     module_revert_v1 the executor reverts the module's writes, records a
//     failed receipt, keeps the fee and consumes the nonce — the sender's
//     account moves on instead of jamming at that nonce forever.
//
// Modules signal HARD by wrapping the error in HardModuleError; anything
// unwrapped is SOFT. Keep HARD to the four classes above: a HARD error
// classified differently on two nodes is a chain split, a SOFT error is only a
// failed receipt.
package vm

import (
	"errors"
	"fmt"
)

// ErrForkInactive is the sentinel behind a fork-gated action reached before
// its scheduled height (or on a chain that never schedules it).
var ErrForkInactive = errors.New("fork not active")

// HardModuleError marks a module-call failure the executor must treat as
// unappliable (see the package comment). It unwraps to the underlying error so
// callers can still errors.Is against sentinels such as ErrForkInactive.
type HardModuleError struct {
	Err error
}

func (e *HardModuleError) Error() string { return e.Err.Error() }
func (e *HardModuleError) Unwrap() error { return e.Err }

// IsHardModuleError reports whether err (or anything it wraps) is a
// HardModuleError.
func IsHardModuleError(err error) bool {
	var hard *HardModuleError
	return errors.As(err, &hard)
}

// hard wraps err as a HardModuleError. nil stays nil.
func hard(err error) error {
	if err == nil {
		return nil
	}
	return &HardModuleError{Err: err}
}

// requireFork is the gate a fork-scheduled action calls first: it returns a
// HARD ErrForkInactive error unless `name` is active at the height of the block
// being applied. Nil-safe through Schedule.Active, so a chain with no schedule
// reads "never". The height is in the message so the text is the same on every
// node applying the same block.
func requireFork(ctx ModuleContext, name string) error {
	if ctx.Forks.Active(name, ctx.Height) {
		return nil
	}
	return hard(fmt.Errorf("%w: %s is not scheduled at height %d", ErrForkInactive, name, ctx.Height))
}
