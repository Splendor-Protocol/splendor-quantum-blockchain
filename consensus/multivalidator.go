package consensus

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/ai"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
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
	reg := vm.NewStakingContract(m.stateDB, vm.DeployStaking(nil))
	active, _ := reg.ListActive()
	infos := make([]ValidatorInfo, 0, len(active)+1)
	for _, rec := range active {
		infos = append(infos, ValidatorInfo{
			Address: rec.Address, PublicKey: rec.PublicKey, IsActive: true,
		})
	}
	if len(infos) == 0 {
		// Empty registry: the chain is still run by whoever sealed block 0.
		// Every node must name the same key here, or each BFT node on such a
		// chain is its own sole leader and they fork from each other.
		fallback := m.cfg.GenesisValidator
		if len(fallback.PublicKey) == 0 {
			fallback = ValidatorInfo{Address: m.cfg.Address, PublicKey: m.cfg.PublicKey}
		}
		fallback.IsActive = true
		infos = append(infos, fallback)
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

	go func() {
		if err := m.engine.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("BFT engine exited")
		}
	}()

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
	batch := m.cfg.MaxTxPerBlock
	if m.scheduler != nil {
		batch = m.scheduler.RecommendBatchSize(m.pool.Size())
	}
	txs := m.pool.Pending(batch)

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

	b, err := m.executor.BuildProposal(
		txs, m.cfg.Address, m.cfg.PublicKey, m.cfg.PrivateKey, prevHash, height,
	)
	if err != nil {
		return err
	}
	m.engine.ProposeBlock(b)
	if m.engine.broadcastP != nil {
		m.engine.broadcastP(b)
	}
	return nil
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
