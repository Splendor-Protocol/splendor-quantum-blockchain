// bridge-relayer — the core loop.
//
// One pass: read the legacy head, stop `confirmations` blocks behind it,
// fetch Burned logs from the cursor forward in bounded batches, and for each
// burn not yet claimed on the quantum bridge submit one bridge.claim and wait
// for its receipt. The cursor advances only past blocks whose burns are all
// settled (claimed, already-claimed, or given up after maxAttempts and
// recorded as skipped), and is persisted to a state file so a restart resumes
// where it left off — over-scanning after a crash is harmless because claim
// idempotency lives on-chain.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// relayerState is the persisted cursor.
type relayerState struct {
	// LastProcessed is the highest legacy block whose burns are all settled.
	LastProcessed uint64 `json:"last_processed"`
	// Skipped records burns given up after maxAttempts, for manual replay
	// (--replay-id rescans the recorded block). The claimed slot on-chain
	// keeps a replay safe however often it is run.
	Skipped []skippedBurn `json:"skipped,omitempty"`
}

type skippedBurn struct {
	ID     uint64 `json:"id"`
	Block  uint64 `json:"block"`
	Reason string `json:"reason"`
}

func loadState(path string) (*relayerState, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s relayerState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state file %s: %w", path, err)
	}
	return &s, nil
}

func (s *relayerState) save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Relayer holds one configured instance of the loop.
type Relayer struct {
	EVM     *EVMClient
	Quantum *QuantumClient

	EvmContract   string // burn contract, 0x + 40 lowercase hex
	Bridge        qcrypto.Address
	Operator      qcrypto.Address
	Priv          *qcrypto.PrivateKey
	Confirmations uint64
	BatchBlocks   uint64
	Fee           int64
	MaxAttempts   int
	ReceiptWait   time.Duration

	State     *relayerState
	StatePath string

	attempts map[uint64]int
	Logf     func(format string, args ...any)
}

func (r *Relayer) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// Preflight refuses to run against the wrong chains or an unusable bridge:
// the blockpeak-credits "chain-id mismatch; unplanned reset" lesson, applied
// on both sides before the first claim is ever signed.
func (r *Relayer) Preflight(wantEvmChainID uint64, wantQuantumChainID string) error {
	evmID, err := r.EVM.ChainID()
	if err != nil {
		return fmt.Errorf("legacy chain unreachable: %w", err)
	}
	if evmID != wantEvmChainID {
		return fmt.Errorf("legacy chain id %d, want %d", evmID, wantEvmChainID)
	}
	qID, _, err := r.Quantum.Status()
	if err != nil {
		return fmt.Errorf("quantum node unreachable: %w", err)
	}
	if qID != wantQuantumChainID {
		return fmt.Errorf("quantum chain id %q, want %q", qID, wantQuantumChainID)
	}
	info, err := r.Quantum.Bridge(r.Bridge.String())
	if err != nil {
		return fmt.Errorf("bridge %s: %w", r.Bridge, err)
	}
	if info.EvmChainID != wantEvmChainID {
		return fmt.Errorf("bridge is bound to legacy chain %d, relayer configured for %d", info.EvmChainID, wantEvmChainID)
	}
	if info.EvmContract != r.EvmContract {
		return fmt.Errorf("bridge is bound to burn contract %s, relayer watches %s", info.EvmContract, r.EvmContract)
	}
	authorised := false
	for _, op := range info.Operators {
		if op == r.Operator.String() {
			authorised = true
		}
	}
	if info.Owner == r.Operator.String() {
		authorised = true
	}
	if !authorised {
		return fmt.Errorf("operator %s is neither the bridge owner nor an active operator", r.Operator)
	}
	if info.Paused {
		r.logf("warning: bridge is paused — claims will fail until unpause")
	}
	return nil
}

// RunOnce performs one pass and reports how many claims were submitted.
func (r *Relayer) RunOnce() (int, error) {
	if r.attempts == nil {
		r.attempts = map[uint64]int{}
	}
	head, err := r.EVM.BlockNumber()
	if err != nil {
		return 0, err
	}
	if head < r.Confirmations {
		return 0, nil
	}
	safe := head - r.Confirmations
	if safe <= r.State.LastProcessed {
		return 0, nil
	}
	from := r.State.LastProcessed + 1
	to := safe
	if to-from+1 > r.BatchBlocks {
		to = from + r.BatchBlocks - 1
	}
	logs, err := r.EVM.BurnedLogs(r.EvmContract, from, to)
	if err != nil {
		return 0, err
	}
	claimed := 0
	for _, l := range logs {
		burn, err := decodeBurn(l)
		if err != nil {
			// A malformed log from the pinned contract is an alarm, not a
			// claim; it cannot block the cursor because it has no valid id.
			r.logf("skipping undecodable log in tx %s: %v", l.TxHash, err)
			continue
		}
		ok, err := r.settle(burn)
		if err != nil {
			return claimed, err // transport-level trouble: retry the batch next pass
		}
		if !ok {
			// Give up on this burn for now without advancing the cursor —
			// unless it has exhausted its attempts, in which case it is
			// recorded and the cursor moves on.
			if r.attempts[burn.ID] >= r.MaxAttempts {
				r.logf("ALARM: burn %d failed %d times, recording as skipped (replay with --replay-id %d)", burn.ID, r.attempts[burn.ID], burn.ID)
				r.State.Skipped = append(r.State.Skipped, skippedBurn{ID: burn.ID, Block: burn.Block, Reason: "max attempts"})
				continue
			}
			return claimed, nil
		}
		claimed++
	}
	r.State.LastProcessed = to
	if r.StatePath != "" {
		if err := r.State.save(r.StatePath); err != nil {
			return claimed, fmt.Errorf("persist state: %w", err)
		}
	}
	return claimed, nil
}

// settle brings one burn to a settled state: already claimed, or claimed now
// with a success receipt. false with nil error means "not settled, try again".
func (r *Relayer) settle(b Burn) (bool, error) {
	already, err := r.Quantum.Claimed(r.Bridge.String(), b.ID)
	if err != nil {
		return false, err
	}
	if already {
		return true, nil
	}
	nonce, err := r.Quantum.Nonce(r.Operator)
	if err != nil {
		return false, err
	}
	hash, err := r.Quantum.SubmitClaim(r.Priv, r.Operator, nonce, r.Fee, r.Bridge, b)
	if err != nil {
		r.attempts[b.ID]++
		r.logf("burn %d: submit failed (attempt %d): %v", b.ID, r.attempts[b.ID], err)
		return false, nil
	}
	r.logf("burn %d (%s %s -> %s): claim %s submitted", b.ID, b.Amount, b.Asset, b.Recipient, hash)

	deadline := time.Now().Add(r.ReceiptWait)
	for time.Now().Before(deadline) {
		status, msg, err := r.Quantum.ReceiptStatus(hash)
		if err != nil {
			return false, err
		}
		switch status {
		case 1:
			r.logf("burn %d: claimed in %s", b.ID, hash)
			return true, nil
		case 0:
			r.attempts[b.ID]++
			r.logf("burn %d: claim reverted (attempt %d): %s", b.ID, r.attempts[b.ID], msg)
			return false, nil
		}
		time.Sleep(time.Second)
	}
	// The receipt never appeared inside the window. The claimed slot decides
	// on the next pass whether it actually landed.
	r.logf("burn %d: receipt for %s not seen in %s, will re-check", b.ID, hash, r.ReceiptWait)
	return false, nil
}
