package rpc

// metrics.go implements a lightweight Prometheus-compatible /metrics HTTP endpoint.
// It does NOT depend on the prometheus/client_golang library — it writes the
// Prometheus text exposition format manually, keeping the dependency tree small.
//
// Prometheus text format:
//   # HELP metric_name Description.
//   # TYPE metric_name gauge|counter|histogram
//   metric_name{label="value"} numeric_value [optional_timestamp_ms]
//
// Exposed metrics:
//   sqc_chain_height              - current chain height (gauge)
//   sqc_mempool_size              - pending transactions in mempool (gauge)
//   sqc_peer_count                - connected peers (gauge)
//   sqc_ws_client_count           - active WebSocket/SSE subscribers (gauge)
//   sqc_blocks_produced_total     - total blocks produced by this validator (counter)
//   sqc_txs_processed_total       - total transactions executed (counter)
//   sqc_txs_failed_total          - total transactions that failed execution (counter)
//   sqc_ai_batch_size             - current AI-recommended batch size (gauge)
//   sqc_ai_anomaly_active         - 1 if AI anomaly detected, 0 otherwise (gauge)
//   sqc_rpc_requests_total        - total RPC HTTP requests received (counter)
//   sqc_uptime_seconds            - node uptime in seconds (gauge)

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// NodeMetrics holds all runtime metrics for the node.
// All numeric fields are updated atomically or under a mutex.
type NodeMetrics struct {
	startTime time.Time

	// Chain metrics
	ChainHeight    atomic.Uint64
	MempoolSize    atomic.Int64
	PeerCount      atomic.Int64
	WSClientCount  atomic.Int64

	// Validator metrics
	BlocksProduced atomic.Uint64
	TxsProcessed   atomic.Uint64
	TxsFailed      atomic.Uint64

	// AI scheduler metrics
	AIBatchSize    atomic.Int64
	AIAnomaly      atomic.Int32 // 1 = anomaly active, 0 = normal

	// RPC metrics
	RPCRequests atomic.Uint64

	// Custom labels
	mu     sync.RWMutex
	labels map[string]string // static labels applied to all metrics
}

// NewNodeMetrics creates a new metrics collector.
func NewNodeMetrics() *NodeMetrics {
	return &NodeMetrics{
		startTime: time.Now(),
		labels:    make(map[string]string),
	}
}

// SetLabel adds a static label to all metrics (e.g., node_name, chain_id).
func (m *NodeMetrics) SetLabel(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[key] = value
}

// IncrRPCRequests increments the RPC request counter.
// Call this in the logging middleware.
func (m *NodeMetrics) IncrRPCRequests() {
	m.RPCRequests.Add(1)
}

// IncrBlocksProduced increments the block production counter.
func (m *NodeMetrics) IncrBlocksProduced() {
	m.BlocksProduced.Add(1)
}

// IncrTxsProcessed increments the processed transactions counter.
func (m *NodeMetrics) IncrTxsProcessed(n uint64) {
	m.TxsProcessed.Add(n)
}

// IncrTxsFailed increments the failed transactions counter.
func (m *NodeMetrics) IncrTxsFailed(n uint64) {
	m.TxsFailed.Add(n)
}

// SetChainHeight updates the current chain height.
func (m *NodeMetrics) SetChainHeight(h uint64) {
	m.ChainHeight.Store(h)
}

// SetMempoolSize updates the mempool size gauge.
func (m *NodeMetrics) SetMempoolSize(n int) {
	m.MempoolSize.Store(int64(n))
}

// SetPeerCount updates the peer count gauge.
func (m *NodeMetrics) SetPeerCount(n int) {
	m.PeerCount.Store(int64(n))
}

// SetWSClientCount updates the WebSocket subscriber count.
func (m *NodeMetrics) SetWSClientCount(n int) {
	m.WSClientCount.Store(int64(n))
}

// SetAIBatchSize updates the AI scheduler batch size gauge.
func (m *NodeMetrics) SetAIBatchSize(n int) {
	m.AIBatchSize.Store(int64(n))
}

// SetAIAnomaly sets the anomaly flag (1 = anomaly detected).
func (m *NodeMetrics) SetAIAnomaly(active bool) {
	if active {
		m.AIAnomaly.Store(1)
	} else {
		m.AIAnomaly.Store(0)
	}
}

// UptimeSeconds returns the node uptime in seconds.
func (m *NodeMetrics) UptimeSeconds() float64 {
	return time.Since(m.startTime).Seconds()
}

// ServeHTTP handles GET /metrics requests.
// The output is in Prometheus text exposition format v0.0.4.
func (m *NodeMetrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	labels := make(map[string]string, len(m.labels))
	for k, v := range m.labels {
		labels[k] = v
	}
	m.mu.RUnlock()

	labelStr := formatLabels(labels)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// Write each metric in Prometheus text format
	writeMetric(w, "sqc_chain_height", "gauge",
		"Current chain height (latest committed block number)",
		labelStr, float64(m.ChainHeight.Load()))

	writeMetric(w, "sqc_mempool_size", "gauge",
		"Number of pending transactions in the mempool",
		labelStr, float64(m.MempoolSize.Load()))

	writeMetric(w, "sqc_peer_count", "gauge",
		"Number of connected peers",
		labelStr, float64(m.PeerCount.Load()))

	writeMetric(w, "sqc_ws_client_count", "gauge",
		"Number of active WebSocket/SSE subscriber connections",
		labelStr, float64(m.WSClientCount.Load()))

	writeMetric(w, "sqc_blocks_produced_total", "counter",
		"Total number of blocks produced by this validator node",
		labelStr, float64(m.BlocksProduced.Load()))

	writeMetric(w, "sqc_txs_processed_total", "counter",
		"Total number of transactions successfully executed",
		labelStr, float64(m.TxsProcessed.Load()))

	writeMetric(w, "sqc_txs_failed_total", "counter",
		"Total number of transactions that failed execution",
		labelStr, float64(m.TxsFailed.Load()))

	writeMetric(w, "sqc_ai_batch_size", "gauge",
		"Current AI scheduler recommended batch size",
		labelStr, float64(m.AIBatchSize.Load()))

	writeMetric(w, "sqc_ai_anomaly_active", "gauge",
		"1 if the AI scheduler has detected a mempool anomaly, 0 otherwise",
		labelStr, float64(m.AIAnomaly.Load()))

	writeMetric(w, "sqc_rpc_requests_total", "counter",
		"Total number of HTTP RPC requests received",
		labelStr, float64(m.RPCRequests.Load()))

	writeMetric(w, "sqc_uptime_seconds", "gauge",
		"Node uptime in seconds",
		labelStr, m.UptimeSeconds())
}

// MetricsServer runs a dedicated HTTP server for the /metrics endpoint.
// This is intentionally separate from the RPC server so it can be
// firewalled independently.
type MetricsServer struct {
	addr    string
	metrics *NodeMetrics
	server  *http.Server
}

// NewMetricsServer creates a new metrics HTTP server.
func NewMetricsServer(addr string, metrics *NodeMetrics) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","uptime_seconds":%.0f}`, metrics.UptimeSeconds())
	})

	return &MetricsServer{
		addr:    addr,
		metrics: metrics,
		server: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
	}
}

// Start starts the metrics HTTP server. Non-blocking.
func (s *MetricsServer) Start() {
	go func() {
		log.Info().Str("addr", s.addr).Msg("metrics server starting")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("metrics server error")
		}
	}()
}

// Stop gracefully shuts down the metrics server.
func (s *MetricsServer) Stop() error {
	return s.server.Close()
}

// ── Format helpers ─────────────────────────────────────────────────────────────

// writeMetric writes a single metric in Prometheus text format.
func writeMetric(w http.ResponseWriter, name, metricType, help, labels string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)
	if labels == "" {
		fmt.Fprintf(w, "%s %g\n", name, value)
	} else {
		fmt.Fprintf(w, "%s{%s} %g\n", name, labels, value)
	}
}

// formatLabels converts a label map to Prometheus label string format.
// Example: key1="val1",key2="val2"
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	result := ""
	for k, v := range labels {
		if result != "" {
			result += ","
		}
		result += fmt.Sprintf(`%s="%s"`, k, escapeLabelValue(v))
	}
	return result
}

// escapeLabelValue escapes special characters in Prometheus label values.
func escapeLabelValue(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			result = append(result, '\\', '\\')
		case '"':
			result = append(result, '\\', '"')
		case '\n':
			result = append(result, '\\', 'n')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
