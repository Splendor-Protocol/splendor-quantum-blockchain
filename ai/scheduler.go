// Package ai provides the on-node AI inference engine for Splendor Quantum Chain.
// It does NOT use a language model. It uses:
//   - Exponential moving average (EMA) for fee trend tracking
//   - A simple reinforcement-learning-style feedback loop for batch size tuning
//   - Rule-based anomaly detection for spam / attack pattern recognition
//
// Everything runs in-process with zero external dependencies or network calls.
// The scheduler exposes a simple API consumed by the consensus validator.
package ai

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// Default batch size targets
	defaultMinBatch = 100
	defaultMaxBatch = 10_000

	// EMA smoothing factor (0 < alpha < 1)
	// Closer to 1 = more weight on recent values
	emaAlpha = 0.3

	// Anomaly threshold: if pending tx count spikes by this factor in one window, flag it
	anomalySpikeMultiplier = 5.0

	// Observation window for metrics
	observationInterval = 500 * time.Millisecond
)

// Metrics is a snapshot of current node performance metrics.
type Metrics struct {
	MempoolDepth    int
	AvgFeeGwei      float64
	CPULoadPercent  float64 // 0–100
	TxsLastBlock    int
	BlockTimeMs     float64
}

// Scheduler is the on-node AI inference engine.
// It observes mempool and execution metrics and recommends operational parameters.
type Scheduler struct {
	mu sync.Mutex

	// Recommended batch size — updated each observation cycle
	batchSize int
	minBatch  int
	maxBatch  int

	// EMA of mempool depth (for trend detection)
	emaMempoolDepth float64
	emaTxPerBlock   float64

	// Anomaly detection: previous mempool depth for spike detection
	prevMempoolDepth int

	// Running total of observations for logging
	observations uint64

	// Whether anomaly has been flagged in current window
	anomalyActive bool
}

// NewScheduler creates a new AI scheduler with default parameters.
func NewScheduler() *Scheduler {
	return &Scheduler{
		batchSize:       1000,
		minBatch:        defaultMinBatch,
		maxBatch:        defaultMaxBatch,
		emaMempoolDepth: 0,
		emaTxPerBlock:   0,
	}
}

// Observe feeds current metrics into the scheduler.
// Called periodically by the node (every ~500ms).
func (s *Scheduler) Observe(m Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observations++

	// Update EMAs
	s.emaMempoolDepth = ema(s.emaMempoolDepth, float64(m.MempoolDepth), emaAlpha)
	s.emaTxPerBlock = ema(s.emaTxPerBlock, float64(m.TxsLastBlock), emaAlpha)

	// Anomaly detection: sudden spike in mempool
	if s.prevMempoolDepth > 0 {
		spike := float64(m.MempoolDepth) / float64(s.prevMempoolDepth)
		if spike >= anomalySpikeMultiplier && m.MempoolDepth > 100 {
			if !s.anomalyActive {
				log.Warn().
					Int("prev", s.prevMempoolDepth).
					Int("now", m.MempoolDepth).
					Float64("spike_factor", spike).
					Msg("AI scheduler: mempool spike detected — possible spam attack")
				s.anomalyActive = true
			}
		} else {
			s.anomalyActive = false
		}
	}
	s.prevMempoolDepth = m.MempoolDepth

	// Batch size tuning: reinforcement-learning-style greedy policy
	// Rule: if mempool is growing (EMA rising), increase batch to drain faster
	//       if mempool is shrinking, reduce batch to save CPU
	//       if CPU > 85%, reduce batch regardless
	newBatch := s.batchSize
	if m.CPULoadPercent > 85 {
		// Throttle: system under stress
		newBatch = max(s.batchSize*8/10, s.minBatch)
	} else if float64(m.MempoolDepth) > s.emaMempoolDepth*1.5 {
		// Mempool growing fast: scale up batch
		newBatch = min(s.batchSize*12/10, s.maxBatch)
	} else if float64(m.MempoolDepth) < s.emaMempoolDepth*0.5 {
		// Mempool shrinking: scale down batch
		newBatch = max(s.batchSize*9/10, s.minBatch)
	}

	if newBatch != s.batchSize {
		log.Debug().
			Int("old_batch", s.batchSize).
			Int("new_batch", newBatch).
			Int("mempool", m.MempoolDepth).
			Float64("cpu", m.CPULoadPercent).
			Msg("AI scheduler: adjusting batch size")
		s.batchSize = newBatch
	}
}

// RecommendBatchSize returns the AI-recommended number of transactions to include
// in the next block, given the current mempool size.
func (s *Scheduler) RecommendBatchSize(mempoolDepth int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If mempool is smaller than our batch size, just use what we have
	if mempoolDepth < s.batchSize {
		return mempoolDepth
	}
	return s.batchSize
}

// IsAnomalyActive returns true if the scheduler has detected an anomaly
// in the current observation window.
func (s *Scheduler) IsAnomalyActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.anomalyActive
}

// Stats returns a snapshot of scheduler state for monitoring/logging.
type Stats struct {
	BatchSize        int
	EMAmempoolDepth  float64
	EMATxPerBlock    float64
	AnomalyActive    bool
	Observations     uint64
}

// GetStats returns current scheduler statistics.
func (s *Scheduler) GetStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		BatchSize:       s.batchSize,
		EMAmempoolDepth: s.emaMempoolDepth,
		EMATxPerBlock:   s.emaTxPerBlock,
		AnomalyActive:   s.anomalyActive,
		Observations:    s.observations,
	}
}

// StartObservationLoop starts a background goroutine that collects metrics
// and feeds them to the scheduler every observationInterval.
// metricsFn is a callback that returns current node metrics.
func (s *Scheduler) StartObservationLoop(metricsFn func() Metrics) {
	go func() {
		ticker := time.NewTicker(observationInterval)
		defer ticker.Stop()
		for range ticker.C {
			m := metricsFn()
			s.Observe(m)
		}
	}()
}

// ema computes the exponential moving average.
func ema(prev, next, alpha float64) float64 {
	return alpha*next + (1-alpha)*prev
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
