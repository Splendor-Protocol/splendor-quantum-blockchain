// Package consensus implements the single-validator consensus for Phase 1.
// The validator continuously pulls transactions from the mempool,
// produces blocks at a configured interval, and broadcasts them.
// All block and validator messages are signed with ML-DSA-65.
package consensus

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Config holds validator configuration.
type Config struct {
	// How often to produce a block (e.g. 2 * time.Second).
	BlockInterval time.Duration

	// Maximum transactions per block.
	MaxTxPerBlock int

	// Validator identity
	Address    qcrypto.Address
	PublicKey  []byte
	PrivateKey *qcrypto.PrivateKey
}

// Validator is the single Phase-1 block producer.
// It reads from the mempool, produces a block every BlockInterval,
// signs it with ML-DSA-65, and emits it to subscribers.
type Validator struct {
	cfg       Config
	executor  *block.Executor
	pool      *mempool.Mempool
	scheduler *ai.Scheduler

	// Subscribers receive newly produced blocks (e.g. P2P broadcaster, RPC).
	subscribers []chan<- *types.Block

	// ineligible is true while this validator is not admitted to produce, so
	// the reason is logged when it starts and when it changes rather than on
	// every tick. Touched only from the single Run goroutine.
	ineligible bool
}

// New creates a new single-validator consensus engine.
func New(
	cfg Config,
	executor *block.Executor,
	pool *mempool.Mempool,
	scheduler *ai.Scheduler,
) *Validator {
	if cfg.BlockInterval == 0 {
		cfg.BlockInterval = 2 * time.Second
	}
	if cfg.MaxTxPerBlock == 0 {
		cfg.MaxTxPerBlock = block.MaxTxPerBlock
	}
	return &Validator{
		cfg:       cfg,
		executor:  executor,
		pool:      pool,
		scheduler: scheduler,
	}
}

// Subscribe registers a channel to receive newly produced blocks.
func (v *Validator) Subscribe(ch chan<- *types.Block) {
	v.subscribers = append(v.subscribers, ch)
}

// Run starts the consensus loop. It blocks until ctx is cancelled.
func (v *Validator) Run(ctx context.Context) error {
	log.Info().
		Str("validator", v.cfg.Address.String()).
		Dur("interval", v.cfg.BlockInterval).
		Msg("validator starting")

	ticker := time.NewTicker(v.cfg.BlockInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("validator stopping")
			return nil

		case <-ticker.C:
			if err := v.produceBlock(); err != nil {
				log.Error().Err(err).Msg("block production failed")
				// Continue — don't halt the chain on a single production error
			}
		}
	}
}

// reportIneligible says once why this validator is not producing, and stays
// quiet until something changes. A validator waiting for its admission pass
// could otherwise fill the log with the same line every block interval, which
// is how a real reason gets missed.
func (v *Validator) reportIneligible(height uint64, err error) {
	if v.ineligible {
		log.Debug().Err(err).Uint64("height", height).Msg("still not admitted to produce")
		return
	}
	v.ineligible = true
	log.Warn().Err(err).
		Uint64("height", height).
		Str("validator", v.cfg.Address.String()).
		Msg("not producing: this validator is not admitted by the chain's validator gates")
}

// produceBlock pulls transactions from the mempool and produces a signed block.
func (v *Validator) produceBlock() error {
	// Ask AI scheduler for recommended batch size
	batchSize := v.cfg.MaxTxPerBlock
	if v.scheduler != nil {
		batchSize = v.scheduler.RecommendBatchSize(v.pool.Size())
	}

	// Pull transactions
	txs := v.pool.Pending(batchSize)

	// Get previous block hash and height
	prevHash := [32]byte{}
	height := uint64(1)
	latest, err := v.executor.LatestBlock()
	if err != nil && err != types.ErrBlockNotFound {
		return fmt.Errorf("failed to get latest block: %w", err)
	}
	if latest != nil {
		prevHash = latest.Hash
		height = latest.Header.Height + 1
	}

	// Under validator_gates_v1 a producer must hold an admission pass and a
	// bond (core/block/validator_gate.go). Asking here, rather than letting
	// ProduceBlock refuse, is the difference between a validator that waits
	// quietly for its pass and one that logs a production failure every two
	// seconds. The rule is the same either way — this is not a second check,
	// it is the same predicate asked earlier.
	if err := v.executor.EligibleToProduce(v.cfg.PublicKey, height); err != nil {
		v.reportIneligible(height, err)
		return nil
	}
	v.ineligible = false

	// Produce and sign block
	b, err := v.executor.ProduceBlock(
		txs,
		v.cfg.Address,
		v.cfg.PublicKey,
		v.cfg.PrivateKey,
		prevHash,
		height,
	)
	if err != nil {
		return fmt.Errorf("ProduceBlock failed at height %d: %w", height, err)
	}

	// Remove included transactions from mempool, then drop any now-stale entries
	// (skipped/failed txs below the sender's committed nonce) so they release their
	// per-address slots instead of jamming MaxPerAddr.
	v.pool.RemoveBlock(b)
	v.pool.PruneStale()

	// Notify subscribers (P2P, RPC)
	for _, ch := range v.subscribers {
		select {
		case ch <- b:
		default:
			log.Warn().Msg("block subscriber channel full, dropping notification")
		}
	}

	log.Info().
		Uint64("height", b.Header.Height).
		Int("txs", len(b.Transactions)).
		Str("hash", fmt.Sprintf("%x", b.Hash[:8])).
		Msg("block produced")

	return nil
}
