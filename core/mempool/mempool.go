// Package mempool manages the pending transaction pool.
// Transactions are validated before being accepted.
// The AI scheduler in the ai package reads mempool metrics to optimise batching.
package mempool

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

const (
	// DefaultMaxSize is the maximum number of pending transactions.
	DefaultMaxSize = 50_000

	// MinFee is the minimum accepted transaction fee (1000 units).
	MinFee = 1000

	// MaxPerAddr caps how many pending transactions a single sender may occupy.
	// The global cap alone lets one funded address fill the entire pool with
	// distinct nonces; this bounds that and limits the signature-verification
	// work an attacker can force per address before admission is refused.
	MaxPerAddr = 64

	// MaxTxDataBytes caps the tx Data (calldata / deploy bytecode / memo). Without
	// it, 50k mempool slots each holding ~512 KiB of Data is ~24 GiB of payload —
	// an OOM vector and multi-GiB candidate blocks. 128 KiB is generous for real
	// calldata and contract code.
	MaxTxDataBytes = 128 * 1024
)

// Fee floor multipliers applied as the pool fills. A flat MinFee makes flooding
// cheap: an attacker pays the same 1000 units for the last slot as for the
// first. Raising the floor with occupancy means sustaining congestion costs
// progressively more, while an empty pool stays cheap for ordinary users.
var congestionTiers = []struct {
	loadPercent int
	multiplier  int64
}{
	{90, 32},
	{75, 8},
	{50, 2},
}

// Mempool holds pending transactions ordered by fee (highest first).
type Mempool struct {
	mu      sync.RWMutex
	txs     map[[32]byte]*txEntry          // txHash → entry
	byAddr  map[qcrypto.Address][]*txEntry // addr → sorted by nonce
	maxSize int
	state   *state.StateDB
}

type txEntry struct {
	tx   *types.Transaction
	hash [32]byte
}

// New creates a new mempool with the given maximum size.
func New(maxSize int, stateDB *state.StateDB) *Mempool {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	return &Mempool{
		txs:     make(map[[32]byte]*txEntry),
		byAddr:  make(map[qcrypto.Address][]*txEntry),
		maxSize: maxSize,
		state:   stateDB,
	}
}

// ValidateEnvelope is the cheap, stateless part of admission: the structural
// checks and the Data size cap. Add runs it first; an ingress that does any
// work on tx.Data before Add (the RPC module-call preflight parses it) must
// run it first too, so an oversized or malformed envelope is refused before
// its payload is ever looked at.
func ValidateEnvelope(tx *types.Transaction) error {
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("invalid tx: %w", err)
	}
	// Bound the tx payload so the pool can't be filled with multi-hundred-KiB txs.
	if len(tx.Data) > MaxTxDataBytes {
		return fmt.Errorf("tx data too large: %d bytes (max %d)", len(tx.Data), MaxTxDataBytes)
	}
	return nil
}

// Add validates and adds a transaction to the mempool.
// Returns an error if the transaction is invalid, duplicate, or the pool is full.
func (m *Mempool) Add(tx *types.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Structural validation and the payload cap.
	if err := ValidateEnvelope(tx); err != nil {
		return err
	}

	// Minimum fee check, scaled by how full the pool currently is.
	minFee := m.minFeeLocked()
	if tx.Fee.Cmp(big.NewInt(minFee)) < 0 {
		return fmt.Errorf("fee too low: minimum %d at current pool load, got %s", minFee, tx.Fee.String())
	}

	// A native-module call carries no value: the executor's module branch
	// returns before the call-value transfer, so an Amount on a router call is
	// debited from nobody and credited to nobody — the coins simply stay put
	// while the sender believes they paid. Refuse it at the door.
	if tx.Type == types.TxCall && tx.To == vm.ModuleRouterAddress && tx.Amount.Sign() != 0 {
		return fmt.Errorf("module call must carry amount 0, got %s: native modules take no call value", tx.Amount.String())
	}

	// A nonce below the committed nonce can never execute: the executor demands
	// tx.Nonce == account.Nonce and an account nonce only advances. Admitting it
	// would spend a signature verification and a pool slot (until PruneStale)
	// on a tx the producer will only ever skip, while /tx/submit reports it
	// accepted. One cheap state read, before the expensive verify. Best-effort
	// like the balance check below: a state read error defers to the executor.
	if committed, err := m.state.GetNonce(tx.From); err == nil && tx.Nonce < committed {
		return fmt.Errorf("%w: committed nonce for %s is %d, got %d",
			types.ErrNonceTooLow, tx.From.String(), committed, tx.Nonce)
	}

	// Reject a duplicate pending (from, nonce): a wallet that reads only the
	// committed nonce re-submits the same nonce while its prior tx is still
	// pending. Without this, those resends pile up and hit MaxPerAddr, locking the
	// sender out (observed live as a stuck heartbeat). Replace-by-fee is a future
	// refinement; today the first tx for a nonce wins until it clears or is pruned.
	for _, e := range m.byAddr[tx.From] {
		if e.tx.Nonce == tx.Nonce {
			return fmt.Errorf("duplicate pending nonce %d for %s", tx.Nonce, tx.From.String())
		}
	}

	// Per-address admission cap (checked before the expensive signature verify so
	// a flooding sender is rejected cheaply).
	if len(m.byAddr[tx.From]) >= MaxPerAddr {
		return fmt.Errorf("per-address mempool limit reached (%d) for %s", MaxPerAddr, tx.From.String())
	}

	// A full pool used to reject everything, which let a spammer who had already
	// taken every slot at MinFee lock out all later traffic no matter what it
	// paid. Instead the pool stays full but repriced: an arrival must outbid the
	// cheapest resident to displace it. Decided here, before signature
	// verification, so an underbid costs us nothing.
	//
	// Add holds m.mu for its whole body, so the pool cannot change underneath us
	// and this victim stays valid until the eviction below.
	var victim *txEntry
	if len(m.txs) >= m.maxSize {
		victim = m.cheapestLocked()
		if victim == nil || tx.Fee.Cmp(victim.tx.Fee) <= 0 {
			return types.ErrMempoolFull
		}
	}

	// Compute hash and check for duplicates
	hash, err := tx.FullHash()
	if err != nil {
		return fmt.Errorf("failed to hash tx: %w", err)
	}
	if _, exists := m.txs[hash]; exists {
		return types.ErrDuplicateTx
	}

	// Verify signature
	pubKey, err := m.state.GetPublicKey(tx.From)
	if err != nil && err != types.ErrNoPublicKey {
		return fmt.Errorf("state error: %w", err)
	}
	if err == types.ErrNoPublicKey {
		// First transaction: public key must be in the tx itself
		if len(tx.PublicKey) != qcrypto.PublicKeySize {
			return types.ErrNoPublicKey
		}
		// Verify the public key matches the from address
		pub, parseErr := qcrypto.PublicKeyFromBytes(tx.PublicKey)
		if parseErr != nil {
			return fmt.Errorf("invalid embedded public key: %w", parseErr)
		}
		if qcrypto.DeriveAddress(pub) != tx.From {
			return fmt.Errorf("embedded public key does not match from address")
		}
		pubKey = tx.PublicKey
	}

	if err := tx.VerifySignature(pubKey); err != nil {
		return types.ErrInvalidSignature
	}

	// Check sender has enough balance (best-effort — definitive check is in executor)
	balance, err := m.state.GetBalance(tx.From)
	if err == nil {
		total := new(big.Int).Add(tx.Amount, tx.Fee)
		if balance.Cmp(total) < 0 {
			return types.ErrInsufficientBalance
		}
	}

	// Make room only now that the transaction is known to be valid, so a bad
	// signature can never cost an honest sender their slot.
	if victim != nil {
		log.Debug().
			Str("evicted", victim.tx.From.String()).
			Str("evicted_fee", victim.tx.Fee.String()).
			Str("incoming_fee", tx.Fee.String()).
			Msg("mempool full: evicted cheapest transaction")
		m.removeLocked(victim.hash)
	}

	// Add to pool
	entry := &txEntry{tx: tx, hash: hash}
	m.txs[hash] = entry
	m.byAddr[tx.From] = append(m.byAddr[tx.From], entry)
	// Keep per-address list sorted by nonce
	sort.Slice(m.byAddr[tx.From], func(i, j int) bool {
		return m.byAddr[tx.From][i].tx.Nonce < m.byAddr[tx.From][j].tx.Nonce
	})

	log.Debug().
		Str("from", tx.From.String()).
		Uint64("nonce", tx.Nonce).
		Str("fee", tx.Fee.String()).
		Msg("tx added to mempool")

	return nil
}

// Pending returns up to n transactions in the order the block producer should
// try them: by fee descending, then timestamp ascending (FIFO within a fee),
// then tx hash, so the order is total and two calls over the same pool agree.
//
// A sender's transactions are then re-slotted so they always come out in nonce
// order: the i-th slot the fee sort gives sender S is filled with S's i-th
// lowest pending nonce (byAddr is kept nonce-sorted by Add). Without this a
// higher-fee nonce 2 could be handed to the producer before nonce 1; the
// executor then rejects nonce 2 ("want 1") and the sender's whole batch
// stalls. The re-slotting happens BEFORE the cut at n: substituting after
// truncation could emit nonce n+1 without n when the lower nonce's slot fell
// past the cut.
func (m *Mempool) Pending(n int) []*types.Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]*txEntry, 0, len(m.txs))
	for _, e := range m.txs {
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		cmp := entries[i].tx.Fee.Cmp(entries[j].tx.Fee)
		if cmp != 0 {
			return cmp > 0 // higher fee first
		}
		if entries[i].tx.Timestamp != entries[j].tx.Timestamp {
			return entries[i].tx.Timestamp < entries[j].tx.Timestamp
		}
		return bytes.Compare(entries[i].hash[:], entries[j].hash[:]) < 0
	})

	if n > len(entries) {
		n = len(entries)
	}

	// Walking the full sorted pool and cutting at n is the same as
	// substituting over the whole pool and then truncating: the k-th slot a
	// sender occupies depends only on the entries before it.
	result := make([]*types.Transaction, 0, n)
	seen := make(map[qcrypto.Address]int)
	for _, e := range entries {
		if len(result) == n {
			break
		}
		k := seen[e.tx.From]
		seen[e.tx.From] = k + 1
		result = append(result, m.byAddr[e.tx.From][k].tx)
	}
	return result
}

// minFeeLocked returns the fee floor for the pool's current occupancy. The
// caller must already hold m.mu.
func (m *Mempool) minFeeLocked() int64 {
	if m.maxSize <= 0 {
		return MinFee
	}
	load := len(m.txs) * 100 / m.maxSize
	for _, tier := range congestionTiers {
		if load >= tier.loadPercent {
			return MinFee * tier.multiplier
		}
	}
	return MinFee
}

// MinFeeNow reports the fee a transaction must pay to be accepted right now, so
// callers can quote an accurate floor instead of the static MinFee.
func (m *Mempool) MinFeeNow() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.minFeeLocked()
}

// cheapestLocked returns the lowest-fee resident transaction, breaking ties
// toward the higher nonce so a sender's earlier transactions are not stranded
// behind an evicted later one. The caller must already hold m.mu.
func (m *Mempool) cheapestLocked() *txEntry {
	var cheapest *txEntry
	for _, e := range m.txs {
		if cheapest == nil {
			cheapest = e
			continue
		}
		switch e.tx.Fee.Cmp(cheapest.tx.Fee) {
		case -1:
			cheapest = e
		case 0:
			if e.tx.Nonce > cheapest.tx.Nonce {
				cheapest = e
			}
		}
	}
	return cheapest
}

// Remove removes a transaction from the mempool by its hash.
// Called after a transaction is included in a committed block.
func (m *Mempool) Remove(hash [32]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(hash)
}

// removeLocked removes a tx by hash. The caller must already hold m.mu.
func (m *Mempool) removeLocked(hash [32]byte) {
	entry, ok := m.txs[hash]
	if !ok {
		return
	}

	delete(m.txs, hash)

	// Remove from per-address list
	addr := entry.tx.From
	addrList := m.byAddr[addr]
	for i, e := range addrList {
		if e.hash == hash {
			m.byAddr[addr] = append(addrList[:i], addrList[i+1:]...)
			break
		}
	}
	if len(m.byAddr[addr]) == 0 {
		delete(m.byAddr, addr)
	}
}

// RemoveBlock removes all transactions that were included in a committed block.
func (m *Mempool) RemoveBlock(b *types.Block) {
	for _, tx := range b.Transactions {
		hash, err := tx.FullHash()
		if err == nil {
			m.Remove(hash)
		}
	}
}

// PruneStale drops pending txs whose nonce is below the sender's committed nonce
// (already applied, or superseded). Call it after each produced block: without it
// a tx that ProduceBlock skips (failed / stale nonce) is never removed, so it pins
// a per-address slot and eventually locks the sender out at MaxPerAddr — observed
// live as a jammed heartbeat with mempool_size == MaxPerAddr. Returns the count removed.
func (m *Mempool) PruneStale() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for hash, e := range m.txs {
		if committed, err := m.state.GetNonce(e.tx.From); err == nil && e.tx.Nonce < committed {
			m.removeLocked(hash)
			removed++
		}
	}
	return removed
}

// Size returns the current number of pending transactions.
func (m *Mempool) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.txs)
}

// Stats returns mempool metrics for the AI scheduler.
type Stats struct {
	PendingCount int
	MaxFee       *big.Int
	MinFee       *big.Int
	AvgFee       *big.Int
}

// GetStats returns current mempool statistics.
func (m *Mempool) GetStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.txs) == 0 {
		return Stats{
			PendingCount: 0,
			MaxFee:       big.NewInt(0),
			MinFee:       big.NewInt(0),
			AvgFee:       big.NewInt(0),
		}
	}

	maxFee := new(big.Int)
	minFee := new(big.Int).SetInt64(1 << 62)
	sum := new(big.Int)

	for _, e := range m.txs {
		if e.tx.Fee.Cmp(maxFee) > 0 {
			maxFee.Set(e.tx.Fee)
		}
		if e.tx.Fee.Cmp(minFee) < 0 {
			minFee.Set(e.tx.Fee)
		}
		sum.Add(sum, e.tx.Fee)
	}

	avg := new(big.Int).Div(sum, big.NewInt(int64(len(m.txs))))

	return Stats{
		PendingCount: len(m.txs),
		MaxFee:       maxFee,
		MinFee:       minFee,
		AvgFee:       avg,
	}
}
