package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// WSEventType identifies the kind of WebSocket push event.
type WSEventType string

const (
	WSEventNewBlock       WSEventType = "new_block"
	WSEventNewTx          WSEventType = "new_tx"
	WSEventNewReceipt     WSEventType = "new_receipt"
	WSEventNodeStatus     WSEventType = "node_status"
	WSEventError          WSEventType = "error"
)

// WSEvent is a push message sent to WebSocket subscribers.
type WSEvent struct {
	Type      WSEventType `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// WSSubscription represents a connected WebSocket client.
type WSSubscription struct {
	id      string
	send    chan *WSEvent
	filters map[WSEventType]bool // which event types this client wants
	done    chan struct{}
}

// Hub manages all active WebSocket subscriptions.
// It is goroutine-safe and allows broadcasting to all or filtered clients.
type Hub struct {
	mu    sync.RWMutex
	subs  map[string]*WSSubscription
	idSeq uint64
}

// NewHub creates a new WebSocket subscription hub.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[string]*WSSubscription),
	}
}

// subscribe creates a new subscription and returns it.
func (h *Hub) subscribe(filters map[WSEventType]bool) *WSSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.idSeq++
	sub := &WSSubscription{
		id:      fmt.Sprintf("ws-%d", h.idSeq),
		send:    make(chan *WSEvent, 256),
		filters: filters,
		done:    make(chan struct{}),
	}
	h.subs[sub.id] = sub
	log.Debug().Str("sub_id", sub.id).Msg("WebSocket client subscribed")
	return sub
}

// unsubscribe removes a subscription and signals its done channel.
func (h *Hub) unsubscribe(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub, ok := h.subs[id]
	if !ok {
		return
	}
	close(sub.done)
	delete(h.subs, id)
	log.Debug().Str("sub_id", id).Msg("WebSocket client unsubscribed")
}

// Broadcast sends an event to all subscribers that have subscribed to its type.
func (h *Hub) Broadcast(event *WSEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.subs {
		if sub.filters[event.Type] || sub.filters["*"] {
			select {
			case sub.send <- event:
			default:
				// Drop event if client buffer is full — they're too slow
				log.Warn().Str("sub_id", sub.id).Str("type", string(event.Type)).
					Msg("WS client buffer full, dropping event")
			}
		}
	}
}

// BroadcastNewBlock pushes a new block event to all subscribers.
func (h *Hub) BroadcastNewBlock(b *types.Block) {
	h.Broadcast(&WSEvent{
		Type:      WSEventNewBlock,
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"height":    b.Header.Height,
			"hash":      fmt.Sprintf("%x", b.Hash[:]),
			"tx_count":  b.Header.TxCount,
			"timestamp": b.Header.Timestamp,
			"validator": b.Header.ValidatorAddr.String(),
		},
	})
}

// BroadcastNewTx pushes a new pending transaction event to all subscribers.
func (h *Hub) BroadcastNewTx(tx *types.Transaction) {
	hash, _ := tx.FullHash()
	h.Broadcast(&WSEvent{
		Type:      WSEventNewTx,
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"hash":      fmt.Sprintf("%x", hash[:]),
			"from":      tx.From.String(),
			"to":        tx.To.String(),
			"amount":    tx.Amount.String(),
			"fee":       tx.Fee.String(),
			"type":      tx.Type,
			"timestamp": tx.Timestamp,
		},
	})
}

// BroadcastReceipt pushes a transaction receipt event to all subscribers.
func (h *Hub) BroadcastReceipt(r *types.Receipt) {
	h.Broadcast(&WSEvent{
		Type:      WSEventNewReceipt,
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"tx_hash":      fmt.Sprintf("%x", r.TxHash[:]),
			"block_height": r.BlockHeight,
			"status":       r.Status,
			"gas_used":     r.GasUsed,
			"error":        r.Error,
			"log_count":    len(r.Logs),
		},
	})
}

// ClientCount returns the number of connected WebSocket clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// ── HTTP handler ───────────────────────────────────────────────────────────────

// HandleWebSocket is the HTTP handler for the /ws endpoint.
// It uses long-polling (SSE) as a fallback when a true WebSocket library
// is not available, making it dependency-free.
// Clients connect via GET /ws?subscribe=new_block,new_tx
// and receive newline-delimited JSON events (NDJSON / Server-Sent Events).
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Parse requested event types
	subscribeParam := r.URL.Query().Get("subscribe")
	filters := parseFilters(subscribeParam)

	// Check if client supports streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	sub := h.subscribe(filters)
	defer h.unsubscribe(sub.id)

	// Send connection confirmation
	sendSSE(w, flusher, &WSEvent{
		Type:      "connected",
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"sub_id":    sub.id,
			"subscribe": subscribeParam,
		},
	})

	// Heartbeat ticker to keep the connection alive through proxies
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event := <-sub.send:
			if !sendSSE(w, flusher, event) {
				return
			}

		case <-heartbeat.C:
			// Send a comment line (SSE spec: lines starting with ":" are comments)
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case <-r.Context().Done():
			return

		case <-sub.done:
			return
		}
	}
}

// sendSSE serialises an event and writes it as a Server-Sent Event.
// Returns false if writing failed (client disconnected).
func sendSSE(w http.ResponseWriter, flusher http.Flusher, event *WSEvent) bool {
	data, err := json.Marshal(event)
	if err != nil {
		return true // skip bad event but keep connection
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// parseFilters converts a comma-separated list of event type names into a filter map.
// "*" or empty string means subscribe to all events.
func parseFilters(s string) map[WSEventType]bool {
	filters := make(map[WSEventType]bool)
	if s == "" || s == "*" {
		filters["*"] = true
		return filters
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			token := s[start:i]
			if token != "" {
				filters[WSEventType(token)] = true
			}
			start = i + 1
		}
	}
	return filters
}
