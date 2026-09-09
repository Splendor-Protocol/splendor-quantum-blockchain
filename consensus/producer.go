package consensus

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Producer is the block producer a validator node runs for the life of the
// process. It seals on the single path (Validator) while the block it is
// about to seal predates multi_validator, and on the BFT path
// (BFTEngine + MultiValidator) from the first block that falls under it.
//
// The path is chosen per block, not once at boot. A node that chose at boot
// stayed on the single path when it booted below the activation height, and
// when the height arrived with two validators registered — the point of
// scheduling the upgrade — ProduceBlock returned ErrQuorumRequired at every
// tick and production stopped until the process was restarted. The
// activation is a height, the node is running when that height arrives, and
// the hand-over happens there: the single-path loop returns ErrBFTRequired
// instead of sealing, and Run continues on the BFT engine on the same
// executor, mempool and subscribers, with the head the single path left.
//
// From then on the set is re-read from the registry every interval
// (MultiValidator.RefreshSet), so a second validator that registers after
// the switch widens the quorum without any further action here.
type Producer struct {
	cfg       Config
	bftCfg    BFTConfig
	executor  *block.Executor
	pool      *mempool.Mempool
	scheduler *ai.Scheduler
	stateDB   *state.StateDB

	subscribers []chan<- *types.Block

	// engine is nil until the BFT path starts. The node's vote and proposal
	// ingress read it on every message, so it is published atomically once.
	engine atomic.Pointer[BFTEngine]
}

// NewProducer prepares both paths. bftCfg is used only when, and as soon as,
// the BFT path is needed; its callbacks must therefore be valid for the whole
// life of the producer, not just at boot.
func NewProducer(
	cfg Config,
	bftCfg BFTConfig,
	executor *block.Executor,
	pool *mempool.Mempool,
	scheduler *ai.Scheduler,
	stateDB *state.StateDB,
) *Producer {
	return &Producer{
		cfg: cfg, bftCfg: bftCfg, executor: executor,
		pool: pool, scheduler: scheduler, stateDB: stateDB,
	}
}

// Subscribe registers a channel that receives every block this node seals or
// commits, on whichever path. Call before Run.
func (p *Producer) Subscribe(ch chan<- *types.Block) {
	p.subscribers = append(p.subscribers, ch)
}

// Engine returns the BFT engine once the producer is on the BFT path, and
// nil while it is still on the single path. Peers' votes and proposals have
// nowhere to go until then, which is correct: nothing is voted on before
// multi_validator is active.
func (p *Producer) Engine() *BFTEngine {
	return p.engine.Load()
}

// Run produces blocks until ctx is cancelled.
func (p *Producer) Run(ctx context.Context) error {
	v := New(p.cfg, p.executor, p.pool, p.scheduler)
	for _, ch := range p.subscribers {
		v.Subscribe(ch)
	}
	err := v.Run(ctx)
	if !errors.Is(err, ErrBFTRequired) {
		return err // nil on cancellation; anything else is fatal to production
	}
	head, _ := p.executor.LatestHeight()
	log.Info().Uint64("head", head).Uint64("first_bft_block", head+1).
		Msg("multi_validator is active for the next block: leaving the single path, starting the BFT producer")
	return p.runBFT(ctx)
}

// runBFT builds the BFT engine and drives it from the current head. It is
// entered exactly once per process, whether at boot or at the activation
// height.
func (p *Producer) runBFT(ctx context.Context) error {
	eng, err := NewBFTEngine(p.bftCfg)
	if err != nil {
		return fmt.Errorf("bft engine: %w", err)
	}
	mv := NewMultiValidator(p.cfg, eng, p.executor, p.pool, p.scheduler, p.stateDB)
	for _, ch := range p.subscribers {
		mv.Subscribe(ch)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-eng.CommitCh():
				mv.NotifyCommit(b)
			}
		}
	}()
	// Published before the engine runs so no vote or proposal that arrives
	// while it starts is refused as "not running BFT"; the channels buffer
	// them and MultiValidator.Run fills the validator set before draining.
	p.engine.Store(eng)
	log.Info().Msg("multi_validator BFT producer started")
	return mv.Run(ctx)
}
