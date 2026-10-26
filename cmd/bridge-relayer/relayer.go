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
	"math/big"
	"os"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// relayerState is the persisted cursor.
type relayerState struct {
	// Identity binds this cursor to one deployment. A state file whose
	// identity does not match the configured chain/contract/bridge is refused
	// at load, so a testnet cursor reused for mainnet (or for a redeployed
	// contract) can never silently skip the new deployment's early burns.
	EvmChainID  uint64 `json:"evm_chain_id"`
	EvmContract string `json:"evm_contract"`
	Bridge      string `json:"bridge"`

	// LastProcessed is the highest legacy block whose burns are all settled.
	LastProcessed uint64 `json:"last_processed"`
	// Skipped records burns given up after repeated mined reverts, for manual
	// replay (--replay-id rescans the recorded block). The claimed slot
	// on-chain keeps a replay safe however often it is run.
	Skipped []skippedBurn `json:"skipped,omitempty"`
}

type skippedBurn struct {
	ID     uint64 `json:"id"`
	Block  uint64 `json:"block"`
	Reason string `json:"reason"`
}

// verifyIdentity refuses a state file that belongs to a different deployment.
func (s *relayerState) verifyIdentity(evmChainID uint64, evmContract, bridge string) error {
	if s.EvmChainID != evmChainID || s.EvmContract != evmContract || s.Bridge != bridge {
		return fmt.Errorf(
			"state file is bound to (chain %d, contract %s, bridge %s) but this run is (chain %d, contract %s, bridge %s); "+
				"use a separate --state file per deployment",
			s.EvmChainID, s.EvmContract, s.Bridge, evmChainID, evmContract, bridge)
	}
	return nil
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

// save writes the state atomically and durably: write a temp file, fsync it,
// then rename. The fsync matters because the cursor is the one piece of
// relayer state whose loss (a torn write on power failure) would re-scan or,
// worse, be unreadable; loadState fails loudly on a corrupt file rather than
// silently starting fresh.
func (s *relayerState) save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Relayer holds one configured instance of the loop.
type Relayer struct {
	EVM     *EVMClient
	Quantum *QuantumClient

	EvmContract    string // burn contract, 0x + 40 lowercase hex
	Bridge         qcrypto.Address
	Operator       qcrypto.Address
	Priv           *qcrypto.PrivateKey
	Confirmations  uint64
	BatchBlocks    uint64
	Fee            int64
	MaxAttempts    int
	ReceiptWait    time.Duration
	AttestationDir string // shared dir for M-of-N attestations ("" = self-attest only)

	State     *relayerState
	StatePath string

	attempts          map[uint64]int       // mined reverts only; controls the skip limit
	transientAt       map[uint64]time.Time // last transient submit failure per burn
	transientFailures map[uint64]int       // consecutive transient failures, separate from mined reverts
	now               func() time.Time     // optional backoff clock; nil uses time.Now
	Logf              func(format string, args ...any)
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
	// A threshold above 1 without a shared attestation dir can never reach
	// quorum: the relayer would hold on every burn forever, silently. Fail
	// fast at startup instead — the operator must wire the dir (or lower the
	// threshold).
	if info.Threshold > 1 && r.AttestationDir == "" {
		return fmt.Errorf("bridge threshold is %d but no --attestation-dir is set; this relayer could never reach quorum", info.Threshold)
	}
	// Fewer active operators than the threshold means no claim can pass at
	// all. Config can change (add operators, lower threshold), so alarm and
	// keep running rather than exit.
	if len(info.Operators) < int(info.Threshold) {
		r.logf("ALARM: bridge has %d active operators but threshold %d — no claim can pass until operators are added or the threshold is lowered",
			len(info.Operators), info.Threshold)
	}
	if info.MaxClaimAmount != "" {
		if _, ok := new(big.Int).SetString(info.MaxClaimAmount, 10); !ok {
			return fmt.Errorf("bridge reports an unparsable max_claim_amount %q", info.MaxClaimAmount)
		}
	}
	return nil
}

// RunOnce performs one pass and reports how many claims were submitted.
func (r *Relayer) RunOnce() (int, error) {
	if r.attempts == nil {
		r.attempts = map[uint64]int{}
	}

	// Hold entirely while the bridge is paused. Pause is the incident brake
	// (a deep legacy reorg, an operator-key problem); submitting into it would
	// only mine reverts and shunt every burn in range into the skip list — the
	// opposite of what pause is for. Hold the cursor and alarm.
	info, err := r.Quantum.Bridge(r.Bridge.String())
	if err != nil {
		return 0, err
	}
	if info.Paused {
		r.logf("ALARM: bridge is paused — holding cursor at block %d, no claims this pass", r.State.LastProcessed)
		return 0, nil
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

	// Ids already given up on: don't re-submit (fee waste) or re-record them;
	// they advance with the cursor and are recovered with --replay-id.
	skipped := map[uint64]bool{}
	for _, s := range r.State.Skipped {
		skipped[s.ID] = true
	}

	claimed := 0
	for _, l := range logs {
		burn, derr := decodeBurn(l)
		if derr != nil {
			// A well-formed Burned log from the pinned contract must always
			// decode; a failure means corrupt or hostile RPC data. Do NOT
			// advance past it — that would silently drop a real burn. Hold the
			// cursor and alarm: transient corruption self-heals on the next
			// pass, persistent corruption halts loudly so an operator can move
			// to a trustworthy endpoint.
			r.logf("ALARM: undecodable Burned log in tx %s (block %s): %v — holding cursor at %d",
				l.TxHash, l.BlockNumber, derr, r.State.LastProcessed)
			return claimed, nil
		}
		if skipped[burn.ID] {
			continue
		}
		if info.MaxClaimAmount != "" {
			cap, ok := new(big.Int).SetString(info.MaxClaimAmount, 10)
			if !ok {
				return claimed, fmt.Errorf("bridge reports an unparsable max_claim_amount %q", info.MaxClaimAmount)
			}
			if burn.Amount.Cmp(cap) > 0 {
				// A deterministic on-chain refusal (amount > max_claim_amount):
				// submitting would mine-revert and burn fees on every attempt.
				// Record it skipped and alarm — the burn is real, and it
				// becomes claimable again via --replay-id if the owner
				// redeploys with a higher cap.
				r.logf("ALARM: burn %d amount %s exceeds bridge max_claim_amount %s; recording as skipped (recover with --replay-id %d)",
					burn.ID, burn.Amount, cap, burn.ID)
				r.State.Skipped = append(r.State.Skipped, skippedBurn{ID: burn.ID, Block: burn.Block, Reason: "exceeds max_claim_amount"})
				skipped[burn.ID] = true
				continue
			}
		}
		ok, serr := r.settle(burn, info)
		if serr != nil {
			return claimed, serr // transport-level trouble: retry the batch next pass
		}
		if !ok {
			// Only a claim that MINED and REVERTED counts toward the limit
			// (bad params, over-cap, paused) — a transient submit failure or a
			// still-pending receipt does not (see settle). Skip only a burn the
			// chain has actually rejected repeatedly; otherwise hold and retry.
			if r.attempts[burn.ID] >= r.MaxAttempts {
				r.logf("ALARM: burn %d reverted on-chain %d times, recording as skipped (recover with --replay-id %d)",
					burn.ID, r.attempts[burn.ID], burn.ID)
				r.State.Skipped = append(r.State.Skipped, skippedBurn{ID: burn.ID, Block: burn.Block, Reason: "max mined reverts"})
				skipped[burn.ID] = true
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
//
// Only a claim that MINES AND REVERTS increments the attempt count that
// RunOnce uses to give up (r.MaxAttempts). A transient submit rejection (rate
// limit, mempool fee floor, a duplicate pending nonce while a prior claim is
// unmined, a connection blip) and a receipt that does not arrive in the window
// are both retried without counting — otherwise a few minutes of congestion or
// a producer stall would permanently shunt live burns into the skip list.
func (r *Relayer) settle(b Burn, info *BridgeInfo) (bool, error) {
	if r.attempts == nil {
		r.attempts = map[uint64]int{}
	}
	if b.Amount == nil || b.Amount.Sign() <= 0 {
		return false, fmt.Errorf("burn %d: non-positive amount", b.ID)
	}
	switch b.Asset {
	case "spld", "splv":
	default:
		return false, fmt.Errorf("burn %d: unknown asset %q", b.ID, b.Asset)
	}
	already, err := r.Quantum.Claimed(r.Bridge.String(), b.ID)
	if err != nil {
		return false, err
	}
	if already {
		delete(r.transientAt, b.ID)
		delete(r.transientFailures, b.ID)
		return true, nil
	}
	// Gather >= threshold distinct attestations (this relayer's own, plus
	// peers' from the shared dir when threshold > 1). Below threshold we hold
	// and retry — NOT counted, since waiting for co-attestors is not a failure.
	atts, enough, err := r.gatherAttestations(b, info)
	if err != nil {
		r.logf("burn %d: attestation error (not counted): %v", b.ID, err)
		return false, nil
	}
	if !enough {
		r.logf("burn %d: %d/%d attestations gathered, waiting for co-attestors", b.ID, len(atts), info.Threshold)
		return false, nil
	}
	nonce, err := r.Quantum.Nonce(r.Operator)
	if err != nil {
		return false, err
	}
	if r.transientAt == nil {
		r.transientAt = map[uint64]time.Time{}
	}
	if r.transientFailures == nil {
		r.transientFailures = map[uint64]int{}
	}
	// Exponential backoff on transient submit failures: wait progressively
	// longer between attempts so a congested quantum node isn't hammered on
	// every pass. First retry after 1s, doubling each time, capped at 60s.
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	if last, ok := r.transientAt[b.ID]; ok {
		backoff := time.Second
		for i := 1; i < r.transientFailures[b.ID]; i++ {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
				break
			}
		}
		if now.Sub(last) < backoff {
			r.logf("burn %d: transient backoff, %v remaining", b.ID, backoff-now.Sub(last))
			return false, nil
		}
	}
	hash, err := r.Quantum.SubmitClaim(r.Priv, r.Operator, nonce, r.Fee, r.Bridge, b, atts)
	if err != nil {
		// Count only toward backoff, never the mined-revert skip limit. The
		// on-chain claimed slot keeps a duplicate safe if a prior submit landed.
		r.transientAt[b.ID] = now
		r.transientFailures[b.ID]++
		r.logf("burn %d: submit deferred (transient, not counted): %v", b.ID, err)
		return false, nil
	}
	// An accepted submission ends the transient failure streak, even if its
	// receipt is pending or later reports a mined revert.
	delete(r.transientAt, b.ID)
	delete(r.transientFailures, b.ID)
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
			// Mined and reverted: a real, likely-permanent execution failure.
			// This is the only path that counts toward giving up.
			r.attempts[b.ID]++
			r.logf("burn %d: claim reverted on-chain (mined-revert %d/%d): %s", b.ID, r.attempts[b.ID], r.MaxAttempts, msg)
			return false, nil
		}
		time.Sleep(time.Second)
	}
	// Accepted but not mined inside the window (a slow producer, still
	// pending). Not counted; the claimed slot decides next pass whether it
	// actually landed.
	r.logf("burn %d: %s accepted but not mined in %s, will re-check", b.ID, hash, r.ReceiptWait)
	return false, nil
}
