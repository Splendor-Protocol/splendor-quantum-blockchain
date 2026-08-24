// Package mempool manages the pending transaction pool.
// Transactions are validated before being accepted.
// The AI scheduler in the ai package reads mempool metrics to optimise batching.
package mempool

import (
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
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
)

// Mempool holds pending transactions ordered by fee (highest first).
type Mempool struct {
	mu      sync.RWMutex
	txs     map[[32]byte]*txEntry // txHash → entry
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

// Add validates and adds a transaction to the mempool.
// Returns an error if the transaction is invalid, duplicate, or the pool is full.
func (m *Mempool) Add(tx *types.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check pool capacity
	if len(m.txs) >= m.maxSize {
		return types.ErrMempoolFull
	}

	// Structural validation
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("invalid tx: %w", err)
	}

	// Minimum fee check
	if tx.Fee.Cmp(big.NewInt(MinFee)) < 0 {
		return fmt.Errorf("fee too low: minimum %d, got %s", MinFee, tx.Fee.String())
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

// Pending returns up to n transactions sorted by fee descending (highest fee first).
// This is what the block producer calls to fill a block.
func (m *Mempool) Pending(n int) []*types.Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]*txEntry, 0, len(m.txs))
	for _, e := range m.txs {
		entries = append(entries, e)
	}

	// Sort by fee descending, then by timestamp ascending (FIFO within same fee)
	sort.Slice(entries, func(i, j int) bool {
		cmp := entries[i].tx.Fee.Cmp(entries[j].tx.Fee)
		if cmp != 0 {
			return cmp > 0 // higher fee first
		}
		return entries[i].tx.Timestamp < entries[j].tx.Timestamp
	})

	if n > len(entries) {
		n = len(entries)
	}

	result := make([]*types.Transaction, n)
	for i := 0; i < n; i++ {
		result[i] = entries[i].tx
	}
	return result
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
	minFee := new(big.Int).SetInt64(1<<62)
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
