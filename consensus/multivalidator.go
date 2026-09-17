package consensus

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// MultiValidator drives BFT production: the local node proposes only when it
// is the scheduled leader, and commits only after a quorum.
type MultiValidator struct {
	cfg       Config
	engine    *BFTEngine
	executor  *block.Executor
	pool      *mempool.Mempool
	scheduler *ai.Scheduler
	stateDB   *state.StateDB

	subscribers []chan<- *types.Block

	warnedLoneRegistrant atomic.Bool
}

func NewMultiValidator(
	cfg Config,
	engine *BFTEngine,
	executor *block.Executor,
	pool *mempool.Mempool,
	scheduler *ai.Scheduler,
	stateDB *state.StateDB,
) *MultiValidator {
	if cfg.BlockInterval == 0 {
		cfg.BlockInterval = 2 * time.Second
	}
	if cfg.MaxTxPerBlock == 0 {
		cfg.MaxTxPerBlock = block.MaxTxPerBlock
	}
	return &MultiValidator{
		cfg: cfg, engine: engine, executor: executor,
		pool: pool, scheduler: scheduler, stateDB: stateDB,
	}
}

func (m *MultiValidator) Engine() *BFTEngine { return m.engine }

func (m *MultiValidator) Subscribe(ch chan<- *types.Block) {
	m.subscribers = append(m.subscribers, ch)
}

func (m *MultiValidator) RefreshSet() {
	active, err := m.executor.CommittedActiveValidators()
	if err != nil {
		// Fail closed: keep the set the engine already holds. An unreadable
		// registry used to be indistinguishable from an empty one, and an
		// empty one elects the genesis validator — or, with no genesis key
		// known, this node — as the sole leader. A read error must never
		// shrink the quorum to one.
		log.Error().Err(err).Msg("BFT: validator registry unreadable, keeping the current set")
		return
	}
	// The chain is run by whoever sealed block 0 until the registry holds at
	// least two validators. This is the executor's rule (verifyProducerAndQC:
	// "below two the registry is not yet the authority on who produces"), and
	// the engine must agree with it: electing a lone registrant here while
	// followers still accept the genesis validator's blocks let anyone with
	// one bond replace the leader set with themselves — a stranger who cannot
	// push a proposal to this node, so the chain simply stopped. A lone
	// registrant that IS the genesis validator changes nothing.
	fallback := m.cfg.GenesisValidator
	if len(fallback.PublicKey) == 0 {
		fallback = ValidatorInfo{Address: m.cfg.Address, PublicKey: m.cfg.PublicKey}
	}
	fallback.IsActive = true
	if len(active) < 2 {
		if len(active) == 1 && active[0].Address != fallback.Address {
			if m.warnedLoneRegistrant.CompareAndSwap(false, true) {
				log.Warn().Str("registrant", active[0].Address.String()).Str("producer", fallback.Address.String()).
					Msg("BFT: registry lists one validator that is not the genesis validator; " +
						"the genesis validator keeps producing until a second validator registers")
			}
		}
		m.engine.UpdateValidatorSet([]ValidatorInfo{fallback})
		return
	}
	infos := make([]ValidatorInfo, 0, len(active))
	for _, rec := range active {
		infos = append(infos, ValidatorInfo{
			Address: rec.Address, PublicKey: rec.PublicKey, IsActive: true,
		})
	}
	m.engine.UpdateValidatorSet(infos)
}

// ready reports whether this node may propose. A validator that is behind its
// peers still votes on their proposals; proposing on a stale head would put a
// second chain in front of the same set.
func (m *MultiValidator) ready() bool {
	return m.cfg.Ready == nil || m.cfg.Ready()
}

func (m *MultiValidator) Run(ctx context.Context) error {
	head, _ := m.executor.LatestHeight()
	m.engine.SetHeight(head + 1)
	m.RefreshSet()

	// Both callbacks are installed BEFORE the engine loop starts. They used to
	// be assigned after `go m.engine.Run(ctx)`, which is a data race with the
	// loop that reads them — and, on the losing schedule, a first view change
	// that called a nil callback and simply did nothing.
	//
	// refreshSet: the engine sizes a quorum from the set it holds, while the
	// executor judges the certificate against the registry in the block's
	// parent state. Refreshing only on the tick left those two disagreeing for
	// up to one interval — the window a registration lands in — and a quorum of
	// one where the chain wants two is a commit that can only fail.
	m.engine.refreshSet = m.RefreshSet
	// A remote leader proposes on its own tick after applying the previous
	// block, so a follower waiting for that proposal allows one interval on
	// top of the round timeout before it rotates the leader away.
	m.engine.proposalWait = m.cfg.BlockInterval
	m.engine.onViewChange = func(height uint64, round uint32) {
		if m.engine.IsLeader(height, round) {
			if !m.ready() {
				log.Debug().Uint64("height", height).Msg("BFT: leader but not synced, holding the proposal")
				return
			}
			if err := m.propose(); err != nil {
				log.Warn().Err(err).Msg("BFT: view-change propose failed")
			}
		}
	}

	go func() {
		if err := m.engine.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("BFT engine exited")
		}
	}()

	ticker := time.NewTicker(m.cfg.BlockInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.RefreshSet()
			h := m.engine.Height()
			latest, _ := m.executor.LatestBlock()
			want := uint64(1)
			if latest != nil {
				want = latest.Header.Height + 1
			}
			if h < want {
				m.engine.SetHeight(want)
				h = want
			}
			if !m.engine.IsLeader(h, m.engine.Round()) {
				continue
			}
			if !m.ready() {
				log.Debug().Uint64("height", h).Msg("BFT: leader but not synced, holding the proposal")
				continue
			}
			if err := m.propose(); err != nil {
				log.Error().Err(err).Msg("BFT: propose failed")
			}
		}
	}
}

func (m *MultiValidator) propose() error {
	prevHash := [32]byte{}
	height := uint64(1)
	latest, err := m.executor.LatestBlock()
	if err != nil && err != types.ErrBlockNotFound {
		return err
	}
	if latest != nil {
		prevHash = latest.Hash
		height = latest.Header.Height + 1
	}
	m.engine.SetHeight(height)

	// If we already vote-locked this height, rebroadcast that exact block
	// (same hash, possibly a higher round). Minting a fresh BuildProposal
	// every tick is what left joiners view-changing on divergent 54703 hashes
	// while the producer's journal stayed locked on the first one.
	if locked := m.engine.LockedProposal(); locked != nil && locked.Header.Height == height {
		log.Info().Uint64("height", height).Hex("hash", locked.Hash[:8]).
			Uint32("round", m.engine.Round()).
			Msg("BFT: rebroadcasting vote-locked proposal")
		m.engine.ProposeBlock(locked)
		return nil
	}
	if m.engine.VoteLockedAt(height) {
		log.Warn().Uint64("height", height).
			Msg("BFT: vote-locked at this height but proposal body missing; holding (will not mint an alternative hash)")
		return nil
	}

	batch := m.cfg.MaxTxPerBlock
	if m.scheduler != nil {
		batch = m.scheduler.RecommendBatchSize(m.pool.Size())
	}
	txs := m.pool.Pending(batch)

	b, skipped, err := m.executor.BuildProposalReport(
		txs, m.cfg.Address, m.cfg.PublicKey, m.cfg.PrivateKey, prevHash, height,
	)
	if err != nil {
		return err
	}
	// What the builder could not apply at its turn will not apply next tick
	// either; it is dropped now rather than handed back on every block until
	// PruneStale happened to catch its nonce. A nonce gap is kept: it clears
	// by itself once the sender's earlier transactions land.
	if dead := unappliable(skipped); len(dead) > 0 {
		log.Info().Uint64("height", height).Int("dropped", m.pool.Drop(dead)).Int("skipped", len(skipped)).
			Msg("BFT: dropped pool entries the proposal could not apply")
	}
	// The engine broadcasts, once its own vote set exists. Sending from here —
	// immediately after handing the block to a buffered channel — let a peer's
	// vote reach this node before it could count one, and the vote was dropped.
	m.engine.ProposeBlock(b)
	return nil
}

// unappliable picks out of the builder's report the candidates that can
// never apply as they are.
func unappliable(skipped []block.SkippedTx) []*types.Transaction {
	var out []*types.Transaction
	for _, s := range skipped {
		if s.Unappliable() {
			out = append(out, s.Tx)
		}
	}
	return out
}

func (m *MultiValidator) NotifyCommit(b *types.Block) {
	m.pool.RemoveBlock(b)
	m.pool.PruneStale()
	for _, ch := range m.subscribers {
		select {
		case ch <- b:
		default:
		}
	}
}
