// Package bridge hardening provides circuit breaking, rate limiting, address
// validation, and reorg detection for bridge chain adapters.
//
// These defensive layers sit between the relayer and each chain RPC/REST
// endpoint so that a misbehaving or unreachable chain cannot cascade failures,
// exhaust memory, or cause double-settlement across a reorg.
package bridge

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState is the operating state of a circuit breaker.
type CircuitState int32

const (
	// CircuitClosed means calls pass through normally.
	CircuitClosed CircuitState = iota
	// CircuitOpen means calls are rejected immediately (upstream is failing).
	CircuitOpen
	// CircuitHalfOpen means a trial call is being tested for recovery.
	CircuitHalfOpen
)

// ErrCircuitOpen is returned when a call is rejected because the circuit breaker
// is open.
var ErrCircuitOpen = fmt.Errorf("circuit breaker open")

// CircuitBreaker wraps an upstream chain with failure tracking. After
// failureThreshold consecutive failures the circuit opens and all calls are
// rejected with ErrCircuitOpen for recoveryTimeout. After recoveryTimeout
// elapses the circuit moves to half-open and the first call is allowed through:
// success closes the circuit, failure re-opens it.
type CircuitBreaker struct {
	name             string
	failureThreshold int
	recoveryTimeout  time.Duration
	successThreshold int

	state         atomic.Int32 // CircuitState
	failures      atomic.Int32
	successes     atomic.Int32
	lastFailure   atomic.Int64 // unix nanos
	halfOpenCalls atomic.Int32
	mu            sync.Mutex
}

// NewCircuitBreaker creates a circuit breaker that opens after failureThreshold
// consecutive failures and stays open for recoveryTimeout before allowing a
// trial call in half-open state.
func NewCircuitBreaker(name string, failureThreshold int, recoveryTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:             name,
		failureThreshold: failureThreshold,
		recoveryTimeout:  recoveryTimeout,
		successThreshold: 3,
	}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// Call executes f only if the circuit allows it. On success it records a
// success; on failure it records a failure and may open the circuit.
func (cb *CircuitBreaker) Call(f func() error) error {
	// Fast path: closed circuit — no lock needed.
	if cb.state.Load() == int32(CircuitClosed) {
		err := f()
		if err == nil {
			cb.recordSuccess()
			return nil
		}
		cb.recordFailure(err)
		return err
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now().UnixNano()

	switch CircuitState(cb.state.Load()) {
	case CircuitClosed:
		err := f()
		if err == nil {
			cb.recordSuccessLocked()
			return nil
		}
		cb.recordFailureLocked(err, now)
		return err

	case CircuitOpen:
		// Check if recovery timeout has elapsed.
		if now-cb.lastFailure.Load() >= cb.recoveryTimeout.Nanoseconds() {
			cb.state.Store(int32(CircuitHalfOpen))
			cb.halfOpenCalls.Store(0)
			err := f()
			if err == nil {
				cb.successes.Add(1)
				cb.halfOpenCalls.Add(1)
				if cb.halfOpenCalls.Load() >= int32(cb.successThreshold) {
					cb.state.Store(int32(CircuitClosed))
					cb.failures.Store(0)
					cb.successes.Store(0)
				}
				return nil
			}
			cb.failures.Add(1)
			cb.lastFailure.Store(now)
			cb.state.Store(int32(CircuitOpen))
			return err
		}
		return fmt.Errorf("%s: %w", cb.name, ErrCircuitOpen)

	case CircuitHalfOpen:
		err := f()
		if err == nil {
			cb.successes.Add(1)
			cb.halfOpenCalls.Add(1)
			if cb.halfOpenCalls.Load() >= int32(cb.successThreshold) {
				cb.state.Store(int32(CircuitClosed))
				cb.failures.Store(0)
				cb.successes.Store(0)
			}
			return nil
		}
		cb.failures.Add(1)
		cb.lastFailure.Store(now)
		cb.state.Store(int32(CircuitOpen))
		return err
	}
	return fmt.Errorf("%s: %w", cb.name, ErrCircuitOpen)
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.successes.Add(1)
	cb.failures.Store(0)
}

func (cb *CircuitBreaker) recordSuccessLocked() {
	cb.successes.Add(1)
	cb.failures.Store(0)
}

func (cb *CircuitBreaker) recordFailure(err error) {
	cb.failures.Add(1)
	cb.lastFailure.Store(time.Now().UnixNano())
	if cb.failures.Load() >= int32(cb.failureThreshold) {
		cb.state.Store(int32(CircuitOpen))
	}
}

func (cb *CircuitBreaker) recordFailureLocked(err error, now int64) {
	cb.failures.Add(1)
	cb.lastFailure.Store(now)
	if cb.failures.Load() >= int32(cb.failureThreshold) {
		cb.state.Store(int32(CircuitOpen))
	}
}

// State returns the current circuit state for observability.
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}

// Failures returns the consecutive failure count.
func (cb *CircuitBreaker) Failures() int32 {
	return cb.failures.Load()
}

// RateLimiter is a token bucket that limits calls per second per chain.
// Each Allow() consumes one token; tokens replenish at rate tokens/sec up to
// the burst capacity.
type RateLimiter struct {
	rate  float64 // tokens per second
	burst int     // max burst size

	tokens float64
	last   int64 // unix nanos of last refill
	mu     sync.Mutex
}

// NewRateLimiter creates a token bucket with the given sustained rate (tokens
// per second) and burst capacity.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{
		rate:   rate,
		burst:  burst,
		tokens: float64(burst),
		last:   time.Now().UnixNano(),
	}
}

// Allow returns true if a token is available (call proceeds), false if the
// caller is rate limited (call should be rejected or retried later).
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now().UnixNano()
	elapsed := float64(now-rl.last) / 1e9
	rl.last = now

	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	if rl.tokens < 1.0 {
		return false
	}
	rl.tokens--
	return true
}

// ValidationError is a structured error for invalid bridge input: field name,
// human-readable reason, and the offending value (truncated for safety).
type ValidationError struct {
	Field  string
	Reason string
	Value  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %s: %s (value: %q)", e.Field, e.Reason, e.Value)
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// evmAddressRe matches a lowercase 0x-prefixed 40-hex-character EVM address.
var evmAddressRe = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

// zeroEVMAddress is the 20-byte zero address used to reject degenerate inputs.
var zeroEVMAddress = "0x" + strings.Repeat("0", 40) // "0x0000...0000"

// ValidateEVMAddress checks that addr is a well-formed, non-zero EVM address.
// Returns a *ValidationError on failure.
func ValidateEVMAddress(addr string) error {
	if !evmAddressRe.MatchString(addr) {
		return &ValidationError{Field: "evm_address", Reason: "must be 0x-prefixed 40-hex-char address", Value: truncate(addr)}
	}
	if strings.EqualFold(addr, zeroEVMAddress) {
		return &ValidationError{Field: "evm_address", Reason: "zero address is not allowed", Value: addr}
	}
	return nil
}

// sqAddressRe matches a SQ: address: "SQ:" followed by 1-64 alphanumeric chars.
var sqAddressRe = regexp.MustCompile(`^SQ:[A-Za-z0-9]{1,64}$`)

// ValidateSQAddress checks that addr is a well-formed Splendor Quantum address.
// Returns a *ValidationError on failure.
func ValidateSQAddress(addr string) error {
	if !sqAddressRe.MatchString(addr) {
		return &ValidationError{Field: "sq_address", Reason: "must be SQ: prefix followed by 1-64 alphanumeric chars", Value: truncate(addr)}
	}
	return nil
}

// ValidateAsset checks that asset is a registered bridge asset.
// Returns a *ValidationError on failure.
func ValidateAsset(asset Asset) error {
	switch asset {
	case AssetNative, AssetSPLV:
		return nil
	default:
		return &ValidationError{Field: "asset", Reason: "unknown bridge asset", Value: string(asset)}
	}
}

// ValidateAmount checks that amount is non-nil and strictly positive.
// Returns a *ValidationError on failure.
func ValidateAmount(amount *big.Int) error {
	if amount == nil {
		return &ValidationError{Field: "amount", Reason: "amount is nil", Value: "<nil>"}
	}
	if amount.Sign() <= 0 {
		return &ValidationError{Field: "amount", Reason: "amount must be strictly positive", Value: amount.String()}
	}
	return nil
}

// truncate shortens s for inclusion in error messages so a giant value cannot
// balloon logs or responses.
func truncate(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// ReorgDetector tracks recent block heights/hashes to detect chain
// reorganizations. It is used on the head-tip read path: if the chain reports
// a height we have already seen at a different hash, or a height lower than the
// last observed head, a reorg is in flight and calls should be paused until the
// chain settles.
//
// The detector is not a substitute for confirm-depth finality; it is a
// runtime guard that catches reorgs shallower than the confirm depth (or a tip
// that moves backward, which confirm depth alone cannot detect).
type ReorgDetector struct {
	mu         sync.Mutex
	window     int // max recent heights to remember
	hashes     map[uint64]string
	lastHeight uint64 // highest height ever observed
}

// NewReorgDetector creates a detector remembering the last window heights.
func NewReorgDetector(window int) *ReorgDetector {
	return &ReorgDetector{
		window:     window,
		hashes:     make(map[uint64]string),
		lastHeight: 0,
	}
}

// Observe records that height h was observed with block hash hash. It returns
// a *ReorgError if this observation conflicts with a previous one (same height,
// different hash) or if the chain tip moved backward. Call Observe on every
// successful Height() call before returning the height to the relayer.
func (d *ReorgDetector) Observe(height uint64, hash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Tip moved backward: a previously-seen head is now behind us.
	if height > 0 && height < d.lastHeight {
		return &ReorgError{Reason: fmt.Sprintf("chain tip moved backward from %d to %d", d.lastHeight, height), Height: height, PreviousHeight: d.lastHeight}
	}

	// Same height, different hash: classic reorg.
	if existing, ok := d.hashes[height]; ok && existing != hash {
		return &ReorgError{Reason: fmt.Sprintf("height %d previously observed with hash %q, now %q", height, existing, hash), Height: height, PreviousHash: existing, CurrentHash: hash}
	}

	d.hashes[height] = hash
	if height > d.lastHeight {
		d.lastHeight = height
	}
	// Evict old heights to bound memory.
	if uint64(len(d.hashes)) > uint64(d.window) {
		// Find and delete the smallest height.
		var min uint64 = ^uint64(0)
		for h := range d.hashes {
			if h < min {
				min = h
			}
		}
		if min != ^uint64(0) {
			delete(d.hashes, min)
		}
	}
	return nil
}

// Reset clears all recorded observations (use after a confirmed reorg resolve).
func (d *ReorgDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hashes = make(map[uint64]string)
	d.lastHeight = 0
}

// ReorgError is returned when the chain shows signs of a reorganization.
type ReorgError struct {
	Reason         string
	Height         uint64
	PreviousHash   string
	CurrentHash    string
	PreviousHeight uint64
}

func (e *ReorgError) Error() string {
	return fmt.Sprintf("reorg detected: %s (height %d)", e.Reason, e.Height)
}

// IsReorgError reports whether err is a *ReorgError.
func IsReorgError(err error) bool {
	var re *ReorgError
	return errors.As(err, &re)
}
