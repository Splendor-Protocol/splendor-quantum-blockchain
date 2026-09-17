package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// WSEventType identifies the kind of WebSocket push event.
type WSEventType string

const (
	WSEventNewBlock   WSEventType = "new_block"
	WSEventNewTx      WSEventType = "new_tx"
	WSEventNewReceipt WSEventType = "new_receipt"
	WSEventNodeStatus WSEventType = "node_status"
	WSEventError      WSEventType = "error"
)

// DefaultMaxSSESubscribers caps concurrent /ws streams so one source cannot
// pin unbounded response goroutines.
const DefaultMaxSSESubscribers = 128

// sseBackfillKeep is how many recent broadcasts a reconnecting client can
// request with Last-Event-ID.
const sseBackfillKeep = 256

// Tunables so tests can prove write-deadline renewal without a 30s wait.
var (
	sseHeartbeat = 30 * time.Second
	sseWriteIdle = 90 * time.Second
)

// ErrTooManySubscribers is returned when the hub is at its subscriber cap.
var ErrTooManySubscribers = errors.New("too many SSE subscribers")

// WSEvent is a push message sent to WebSocket subscribers.
type WSEvent struct {
	Type      WSEventType `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

type sseMsg struct {
	id uint64
	ev *WSEvent
}

// WSSubscription represents a connected WebSocket client.
type WSSubscription struct {
	id      string
	send    chan sseMsg
	filters map[WSEventType]bool // which event types this client wants
	done    chan struct{}
}

// Hub manages all active WebSocket subscriptions.
// It is goroutine-safe and allows broadcasting to all or filtered clients.
type Hub struct {
	mu          sync.RWMutex
	subs        map[string]*WSSubscription
	idSeq       uint64
	nextEventID uint64
	recent      []sseMsg
	maxSubs     int
}

// NewHub creates a new WebSocket subscription hub.
func NewHub() *Hub {
	return NewHubWithLimit(DefaultMaxSSESubscribers)
}

// NewHubWithLimit creates a hub that refuses connections beyond maxSubs.
// maxSubs <= 0 uses DefaultMaxSSESubscribers.
func NewHubWithLimit(maxSubs int) *Hub {
	if maxSubs <= 0 {
		maxSubs = DefaultMaxSSESubscribers
	}
	return &Hub{
		subs:    make(map[string]*WSSubscription),
		maxSubs: maxSubs,
	}
}

func (h *Hub) limit() int {
	if h.maxSubs <= 0 {
		return DefaultMaxSSESubscribers
	}
	return h.maxSubs
}

// subscribe creates a new subscription and returns it.
func (h *Hub) subscribe(filters map[WSEventType]bool) (*WSSubscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= h.limit() {
		return nil, ErrTooManySubscribers
	}
	h.idSeq++
	sub := &WSSubscription{
		id:      fmt.Sprintf("ws-%d", h.idSeq),
		send:    make(chan sseMsg, 256),
		filters: filters,
		done:    make(chan struct{}),
	}
	h.subs[sub.id] = sub
	log.Debug().Str("sub_id", sub.id).Msg("WebSocket client subscribed")
	return sub, nil
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

func wants(filters map[WSEventType]bool, typ WSEventType) bool {
	return filters[typ] || filters["*"]
}

// Broadcast sends an event to all subscribers that have subscribed to its type.
func (h *Hub) Broadcast(event *WSEvent) {
	h.mu.Lock()
	h.nextEventID++
	id := h.nextEventID
	h.recent = append(h.recent, sseMsg{id: id, ev: event})
	if len(h.recent) > sseBackfillKeep {
		h.recent = h.recent[len(h.recent)-sseBackfillKeep:]
	}
	subs := make([]*WSSubscription, 0, len(h.subs))
	for _, sub := range h.subs {
		if wants(sub.filters, event.Type) {
			subs = append(subs, sub)
		}
	}
	h.mu.Unlock()

	msg := sseMsg{id: id, ev: event}
	for _, sub := range subs {
		select {
		case sub.send <- msg:
		default:
			// Drop event if client buffer is full — they're too slow
			log.Warn().Str("sub_id", sub.id).Str("type", string(event.Type)).
				Msg("WS client buffer full, dropping event")
		}
	}
}

func (h *Hub) backfillSince(lastID uint64, filters map[WSEventType]bool) (out []sseMsg, gap bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if lastID == 0 {
		return nil, false
	}
	if len(h.recent) == 0 || h.recent[0].id > lastID+1 {
		gap = true
	}
	for _, m := range h.recent {
		if m.id <= lastID {
			continue
		}
		if wants(filters, m.ev.Type) {
			out = append(out, m)
		}
	}
	return out, gap
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
//
// Mount it behind the node's CORSPolicy like every other RPC route: the stream
// is a live view of the node, so browser access belongs on the same allowlist.
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

	sub, err := h.subscribe(filters)
	if err != nil {
		w.Header().Set("Retry-After", "1")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer h.unsubscribe(sub.id)

	// Set SSE headers. Access-Control-Allow-Origin is deliberately absent here:
	// the CORSPolicy middleware wrapping this route has already decided whether
	// this origin may read the response, and it runs before the handler, so an
	// allowed origin still gets the header on the streaming response. Forcing
	// "*" at this point overrode that decision and made /ws the one route that
	// handed any page on the internet a live feed of blocks and pending
	// transactions, whatever the operator's allowlist said.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	renewSSEDeadline(w)

	// Send connection confirmation
	if !sendSSE(w, flusher, 0, &WSEvent{
		Type:      "connected",
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"sub_id":    sub.id,
			"subscribe": subscribeParam,
		},
	}) {
		return
	}

	lastID := parseLastEventID(r.Header.Get("Last-Event-ID"))
	replay, gap := h.backfillSince(lastID, filters)
	if gap {
		_ = sendSSE(w, flusher, 0, &WSEvent{
			Type:      WSEventError,
			Timestamp: time.Now().Unix(),
			Data: map[string]interface{}{
				"reason":  "backfill_gap",
				"last_id": lastID,
			},
		})
	}
	for _, msg := range replay {
		if !sendSSE(w, flusher, msg.id, msg.ev) {
			return
		}
	}

	// Heartbeat ticker to keep the connection alive through proxies.
	// Each write also slides the response write deadline so a 15s/60s
	// Server.WriteTimeout cannot kill a live stream that is still heartbeating.
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case msg := <-sub.send:
			if !sendSSE(w, flusher, msg.id, msg.ev) {
				return
			}

		case <-heartbeat.C:
			// Send a comment line (SSE spec: lines starting with ":" are comments)
			if !writeSSE(w, flusher, ": heartbeat\n\n") {
				return
			}

		case <-r.Context().Done():
			return

		case <-sub.done:
			return
		}
	}
}

func parseLastEventID(s string) uint64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func renewSSEDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sseWriteIdle))
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload string) bool {
	renewSSEDeadline(w)
	if _, err := fmt.Fprint(w, payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// sendSSE serialises an event and writes it as a Server-Sent Event.
// Returns false if writing failed (client disconnected).
func sendSSE(w http.ResponseWriter, flusher http.Flusher, id uint64, event *WSEvent) bool {
	data, err := json.Marshal(event)
	if err != nil {
		return true // skip bad event but keep connection
	}
	var payload string
	if id > 0 {
		payload = fmt.Sprintf("id: %d\ndata: %s\n\n", id, data)
	} else {
		payload = fmt.Sprintf("data: %s\n\n", data)
	}
	return writeSSE(w, flusher, payload)
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
