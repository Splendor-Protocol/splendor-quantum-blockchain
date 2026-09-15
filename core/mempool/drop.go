package mempool

import "github.com/splendor-protocol/splendor-quantum-blockchain/core/types"

// Drop removes the given transactions from the pool and reports how many
// were resident.
//
// The producer calls it with the candidates it found unappliable at their
// turn: a nonce below the sender's next, a signature that does not verify
// against the registered key, funds already spent by an earlier transaction,
// a pre-tx hook that now refuses them. Left in the pool, every one of those
// was handed back to the producer on every block — each costing an account
// read and, until the executor learned to judge the nonce first, an ML-DSA
// verification — until PruneStale happened to catch the nonce, which for a
// nonce that never advances is never.
func (m *Mempool) Drop(txs []*types.Transaction) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, tx := range txs {
		hash, err := tx.FullHash()
		if err != nil {
			continue
		}
		if _, ok := m.txs[hash]; ok {
			m.removeLocked(hash)
			n++
		}
	}
	return n
}
