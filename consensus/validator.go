// Package consensus implements the single-validator consensus for Phase 1.
// The validator continuously pulls transactions from the mempool,
// produces blocks at a configured interval, and broadcasts them.
// All block and validator messages are signed with ML-DSA-65.
package consensus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
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

	// GenesisValidator is the key that sealed block 0. A BFT node whose
	// registry is empty runs with this as the whole validator set, so two
	// nodes on a chain whose genesis validator never registered agree on one
	// leader instead of each electing itself. The zero value keeps the older
	// local-node fallback, for a producer that knows nothing but its own key.
	GenesisValidator ValidatorInfo

	// Ready reports whether this node may propose. nil means always. Wired to
	// the syncer, so a validator that is still catching up votes on peers'
	// proposals but never proposes on top of a stale head.
	Ready func() bool

	// FetchBlock retrieves the block body with the given hash at the given
	// height, used to recover a round-change-selected value this node does not
	// hold. nil keeps the pre-fetch behaviour (hold until a peer re-proposes
	// it). Wired to the syncer's block fetcher on validator nodes.
	FetchBlock func(height uint64, hash [32]byte) (*types.Block, error)
}

// ErrBFTRequired is returned by Validator.Run when the block this node would
// seal next falls under multi_validator. The single path attaches no quorum
// certificate, so it is finished for good at that height: a block it sealed
// there would be canonical here and refused by every follower once two
// validators are registered (mainnet from 5786). Run returns instead of
// sealing, and the caller — Producer — continues on the BFT path in the same
// process. Whether the path is judged at boot or mid-run makes no difference:
// it is decided per block, for the block about to be sealed.
var ErrBFTRequired = errors.New("multi_validator: the next block must be sealed by the BFT path")

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

// Run starts the consensus loop. It blocks until ctx is cancelled, or returns
// ErrBFTRequired the moment the next block to seal falls under
// multi_validator — before the first tick if it already does, so a node
// booting at head 0 on a chain that activates at height 1 never idles an
// interval on a path it will not use.
func (v *Validator) Run(ctx context.Context) error {
	log.Info().
		Str("validator", v.cfg.Address.String()).
		Dur("interval", v.cfg.BlockInterval).
		Msg("validator starting")

	if _, height, err := v.nextBlock(); err != nil {
		// Not fatal here: the tick below retries it, as production always has.
		log.Error().Err(err).Msg("cannot read the chain head; retrying on the next tick")
	} else if v.sealsUnderBFT(height) {
		return fmt.Errorf("%w: block %d", ErrBFTRequired, height)
	}

	ticker := time.NewTicker(v.cfg.BlockInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("validator stopping")
			return nil

		case <-ticker.C:
			if err := v.produceBlock(); err != nil {
				if errors.Is(err, ErrBFTRequired) {
					// Not a production error to ride out: this loop can never
					// seal that block. Hand the height back to the caller.
					return err
				}
				log.Error().Err(err).Msg("block production failed")
				// Continue — don't halt the chain on a single production error
			}
		}
	}
}

// nextBlock returns the parent hash and height of the block this node would
// seal next.
func (v *Validator) nextBlock() (prevHash [32]byte, height uint64, err error) {
	height = 1
	latest, err := v.executor.LatestBlock()
	if err != nil && err != types.ErrBlockNotFound {
		return prevHash, 0, fmt.Errorf("failed to get latest block: %w", err)
	}
	if latest != nil {
		prevHash = latest.Hash
		height = latest.Header.Height + 1
	}
	return prevHash, height, nil
}

// sealsUnderBFT reports whether a block at height belongs to the BFT path:
// multi_validator is active there. Judged against the executor's live
// schedule every time, for the block about to be sealed, never once at boot
// for the head the node happened to have — the schedule activates at a
// height, and the node is running when that height arrives.
func (v *Validator) sealsUnderBFT(height uint64) bool {
	return v.executor.Forks().Active(forks.MultiValidator, height)
}

// ready reports whether this node may seal. nil means always.
func (v *Validator) ready() bool {
	return v.cfg.Ready == nil || v.cfg.Ready()
}

// produceBlock pulls transactions from the mempool and produces a signed block.
// It returns ErrBFTRequired, and seals nothing, when the block it would seal
// falls under multi_validator.
func (v *Validator) produceBlock() error {
	prevHash, height, err := v.nextBlock()
	if err != nil {
		return err
	}
	if v.sealsUnderBFT(height) {
		return fmt.Errorf("%w: block %d", ErrBFTRequired, height)
	}
	// The gate applies to the single path too. A validator that joined a chain
	// which never scheduled multi_validator has no BFT engine to hold it back,
	// and sealing there would put a second producer on the same chain — which
	// on an empty data directory means block 1 on top of genesis, and a fork.
	if !v.ready() {
		log.Debug().Uint64("height", height).Msg("validator: not synced, holding production")
		return nil
	}

	// Ask AI scheduler for recommended batch size
	batchSize := v.cfg.MaxTxPerBlock
	if v.scheduler != nil {
		batchSize = v.scheduler.RecommendBatchSize(v.pool.Size())
	}

	// Pull transactions
	txs := v.pool.Pending(batchSize)

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
