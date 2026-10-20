// Package block — Pluggable middleware hooks for Splendor Quantum Chain.
//
// Hooks allow external code to intercept key moments in transaction and block
// processing without modifying the core executor. This makes the system modular:
//   - Analytics modules can hook post-block to aggregate stats
//   - Compliance modules can hook pre-tx to screen transactions
//   - DEX modules can hook post-tx to detect arbitrage opportunities
//   - Monitoring modules can hook any point to emit metrics
//
// All hooks are called synchronously in the order they were registered.
// A hook that returns an error on a "pre" hook will abort processing.
// Errors on "post" hooks are logged but do not affect finality.
package block

import (
	"fmt"
	"math/big"
	"sync"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Hook function types ────────────────────────────────────────────────────────

// PreTxHook is called before a transaction is executed.
// Return an error to reject the transaction (it will be skipped, not reverted).
type PreTxHook func(tx *types.Transaction, blockHeight uint64) error

// PostTxHook is called after a transaction is executed (success or failure).
// receipt.Status == 0 if the tx failed. Errors from post hooks are logged only.
type PostTxHook func(tx *types.Transaction, receipt *types.Receipt) error

// PreBlockHook is called before a block's transactions are executed.
// Return an error to reject the entire block.
type PreBlockHook func(block *types.Block) error

// PostBlockHook is called after a block's transactions are fully executed and committed.
// stateRoot is the new state root after this block.
type PostBlockHook func(block *types.Block, stateRoot [32]byte) error

// ValidatorHook is called to check whether a validator is eligible to produce a block.
// Return an error to reject the block proposal.
type ValidatorHook func(validatorAddr qcrypto.Address, blockHeight uint64) error

// ── Hook registry ─────────────────────────────────────────────────────────────

// HookRegistry manages all registered hooks.
// It is goroutine-safe and can have hooks registered at any time.
type HookRegistry struct {
	mu sync.RWMutex

	preTx      []namedPreTxHook
	postTx     []namedPostTxHook
	preBlock   []namedPreBlockHook
	postBlock  []namedPostBlockHook
	validators []namedValidatorHook
}

type namedPreTxHook struct {
	name string
	fn   PreTxHook
}
type namedPostTxHook struct {
	name string
	fn   PostTxHook
}
type namedPreBlockHook struct {
	name string
	fn   PreBlockHook
}
type namedPostBlockHook struct {
	name string
	fn   PostBlockHook
}
type namedValidatorHook struct {
	name string
	fn   ValidatorHook
}

// NewHookRegistry creates an empty hook registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// OnPreTx registers a hook called before each transaction executes.
func (h *HookRegistry) OnPreTx(name string, fn PreTxHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.preTx = append(h.preTx, namedPreTxHook{name: name, fn: fn})
	log.Debug().Str("hook", name).Msg("pre-tx hook registered")
}

// OnPostTx registers a hook called after each transaction executes.
func (h *HookRegistry) OnPostTx(name string, fn PostTxHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.postTx = append(h.postTx, namedPostTxHook{name: name, fn: fn})
	log.Debug().Str("hook", name).Msg("post-tx hook registered")
}

// OnPreBlock registers a hook called before a block is executed.
func (h *HookRegistry) OnPreBlock(name string, fn PreBlockHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.preBlock = append(h.preBlock, namedPreBlockHook{name: name, fn: fn})
	log.Debug().Str("hook", name).Msg("pre-block hook registered")
}

// OnPostBlock registers a hook called after a block is committed.
func (h *HookRegistry) OnPostBlock(name string, fn PostBlockHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.postBlock = append(h.postBlock, namedPostBlockHook{name: name, fn: fn})
	log.Debug().Str("hook", name).Msg("post-block hook registered")
}

// OnValidatorCheck registers a hook that validates a block producer's eligibility.
func (h *HookRegistry) OnValidatorCheck(name string, fn ValidatorHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.validators = append(h.validators, namedValidatorHook{name: name, fn: fn})
	log.Debug().Str("hook", name).Msg("validator-check hook registered")
}

// ── Hook execution ─────────────────────────────────────────────────────────────

// RunPreTx runs all pre-tx hooks. Returns the first error encountered.
// On error, the transaction should be skipped.
func (h *HookRegistry) RunPreTx(tx *types.Transaction, blockHeight uint64) error {
	h.mu.RLock()
	hooks := make([]namedPreTxHook, len(h.preTx))
	copy(hooks, h.preTx)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.fn(tx, blockHeight); err != nil {
			return fmt.Errorf("pre-tx hook %q rejected tx: %w", hook.name, err)
		}
	}
	return nil
}

// RunPostTx runs all post-tx hooks. Errors are logged but do not affect finality.
func (h *HookRegistry) RunPostTx(tx *types.Transaction, receipt *types.Receipt) {
	h.mu.RLock()
	hooks := make([]namedPostTxHook, len(h.postTx))
	copy(hooks, h.postTx)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.fn(tx, receipt); err != nil {
			log.Warn().Err(err).Str("hook", hook.name).Msg("post-tx hook error (non-fatal)")
		}
	}
}

// RunPreBlock runs all pre-block hooks. Returns the first error (block rejected).
func (h *HookRegistry) RunPreBlock(block *types.Block) error {
	h.mu.RLock()
	hooks := make([]namedPreBlockHook, len(h.preBlock))
	copy(hooks, h.preBlock)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.fn(block); err != nil {
			return fmt.Errorf("pre-block hook %q rejected block at height %d: %w",
				hook.name, block.Header.Height, err)
		}
	}
	return nil
}

// RunPostBlock runs all post-block hooks. Errors are logged but non-fatal.
func (h *HookRegistry) RunPostBlock(block *types.Block, stateRoot [32]byte) {
	h.mu.RLock()
	hooks := make([]namedPostBlockHook, len(h.postBlock))
	copy(hooks, h.postBlock)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.fn(block, stateRoot); err != nil {
			log.Warn().Err(err).Str("hook", hook.name).
				Uint64("height", block.Header.Height).
				Msg("post-block hook error (non-fatal)")
		}
	}
}

// RunValidatorCheck runs all validator eligibility hooks.
// Returns an error if any hook rejects the validator.
func (h *HookRegistry) RunValidatorCheck(addr qcrypto.Address, blockHeight uint64) error {
	h.mu.RLock()
	hooks := make([]namedValidatorHook, len(h.validators))
	copy(hooks, h.validators)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.fn(addr, blockHeight); err != nil {
			return fmt.Errorf("validator-check hook %q rejected %s at height %d: %w",
				hook.name, addr, blockHeight, err)
		}
	}
	return nil
}

// ── Built-in hook examples (register these as needed) ─────────────────────────

// TimestampValidationHook rejects transactions with timestamps far in the future or past.
// Register with: registry.OnPreTx("timestamp_validation", TimestampValidationHook(300))
func TimestampValidationHook(maxDriftSeconds int64) PreTxHook {
	return func(tx *types.Transaction, blockHeight uint64) error {
		// Note: in production, inject current time as a parameter
		// This is a stub showing the pattern
		_ = tx
		_ = blockHeight
		return nil // placeholder — implement with real clock comparison
	}
}

// MinFeeHook rejects transactions that pay less than the minimum fee.
// Register with: registry.OnPreTx("min_fee", MinFeeHook(1000))
func MinFeeHook(minFee int64) PreTxHook {
	// Compared as a big.Int. Fee is arbitrary precision and big.Int.Int64() is
	// UNDEFINED above MaxInt64 — it returns the low 64 bits — so a fee of exactly
	// 2^64 truncated to 0 and was rejected as "too low". That is ~18.4 SPLD, an
	// ordinary amount, and the producer does not run this hook: it would pack the
	// transaction while every follower replaying the block rejected it. A fork,
	// reachable by anyone who can afford 19 SPLD.
	min := big.NewInt(minFee)
	return func(tx *types.Transaction, blockHeight uint64) error {
		if tx.Fee == nil || tx.Fee.Cmp(min) < 0 {
			return fmt.Errorf("fee too low: got %v, minimum %d", tx.Fee, minFee)
		}
		return nil
	}
}

// BlockSizeHook rejects blocks with too many transactions.
func BlockSizeHook(maxTxCount int) PreBlockHook {
	return func(block *types.Block) error {
		if len(block.Transactions) > maxTxCount {
			return fmt.Errorf("block too large: %d txs > %d limit",
				len(block.Transactions), maxTxCount)
		}
		return nil
	}
}

// LoggingHook logs every committed block to stdout.
func LoggingHook() PostBlockHook {
	return func(block *types.Block, stateRoot [32]byte) error {
		log.Info().
			Uint64("height", block.Header.Height).
			Uint32("txs", block.Header.TxCount).
			Str("state_root", fmt.Sprintf("%x", stateRoot[:8])).
			Msg("block committed")
		return nil
	}
}
