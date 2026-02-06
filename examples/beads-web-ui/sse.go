package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

const (
	// sseRetryMs is the reconnection interval sent to clients in milliseconds.
	sseRetryMs = 5000
	// sseHeartbeatInterval is how often heartbeat comments are sent to keep connections alive.
	sseHeartbeatInterval = 30 * time.Second
)

// sseEventIDCounter provides monotonically increasing event IDs across all SSE connections.
// Initialized to current time in milliseconds so IDs remain roughly time-ordered,
// which is important for the Last-Event-ID catch-up mechanism.
var sseEventIDCounter atomic.Int64

func init() {
	sseEventIDCounter.Store(time.Now().UnixMilli())
}

// MutationPayload represents mutation data sent to clients.
type MutationPayload struct {
	Type      string `json:"type"` // create, update, delete, comment, status, bonded, squashed, burned
	IssueID   string `json:"issue_id"`
	Title     string `json:"title,omitempty"`
	Assignee  string `json:"assignee,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Timestamp string `json:"timestamp"`
	OldStatus string `json:"old_status,omitempty"` // For status events
	NewStatus string `json:"new_status,omitempty"` // For status events
	ParentID  string `json:"parent_id,omitempty"`  // For bonded events
	StepCount int    `json:"step_count,omitempty"` // For bonded events
}

// SSEHub manages connected SSE clients and broadcasts mutations to them.
type SSEHub struct {
	clients      map[*SSEClient]bool
	register     chan *SSEClient
	unregister   chan *SSEClient
	broadcast    chan *MutationPayload
	mu           sync.RWMutex
	done         chan struct{}
	retryQueue   []*MutationPayload // Buffer when broadcast full
	retryMu      sync.Mutex
	droppedCount int64 // For metrics
	startedAt    time.Time
}

// SSEClient represents a single SSE connection.
type SSEClient struct {
	id        int64
	send      chan *MutationPayload
	done      chan struct{}
	lastSince int64
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    make(map[*SSEClient]bool),
		register:   make(chan *SSEClient, 16),
		unregister: make(chan *SSEClient, 16),
		broadcast:  make(chan *MutationPayload, 256),
		done:       make(chan struct{}),
		startedAt:  time.Now(),
	}
}

// Run starts the hub's main loop for managing clients and broadcasts.
func (h *SSEHub) Run() {
	retryTicker := time.NewTicker(100 * time.Millisecond)
	defer retryTicker.Stop()

	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("SSE client %d registered (total: %d)", client.id, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("SSE client %d unregistered (total: %d)", client.id, len(h.clients))

		case mutation := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- mutation:
				default:
					// Client buffer full, skip this mutation
					log.Printf("SSE client %d buffer full, skipping mutation", client.id)
				}
			}
			h.mu.RUnlock()

		case <-retryTicker.C:
			h.drainRetryQueue()

		case <-h.done:
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Stop gracefully stops the hub.
func (h *SSEHub) Stop() {
	close(h.done)
}

// RegisterClient adds a new client to the hub.
// Non-blocking if the hub has been stopped — closes the client's send channel instead.
func (h *SSEHub) RegisterClient(client *SSEClient) {
	// Check done first to avoid writing to the buffered register channel
	// after Run() has exited (nobody would process it).
	select {
	case <-h.done:
		close(client.send)
		return
	default:
	}
	select {
	case h.register <- client:
	case <-h.done:
		close(client.send)
	}
}

// UnregisterClient removes a client from the hub.
// Non-blocking if the hub has been stopped.
func (h *SSEHub) UnregisterClient(client *SSEClient) {
	select {
	case <-h.done:
		return
	default:
	}
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

// Broadcast sends a mutation to all connected clients.
// If the broadcast channel is full, mutations are queued for retry.
func (h *SSEHub) Broadcast(mutation *MutationPayload) {
	select {
	case h.broadcast <- mutation:
	default:
		h.retryMu.Lock()
		if len(h.retryQueue) < 1024 {
			h.retryQueue = append(h.retryQueue, mutation)
			log.Printf("SSE broadcast channel full, queued mutation (queue size: %d)", len(h.retryQueue))
		} else {
			atomic.AddInt64(&h.droppedCount, 1)
			log.Printf("SSE retry queue full, dropped mutation (total dropped: %d)", atomic.LoadInt64(&h.droppedCount))
		}
		h.retryMu.Unlock()
	}
}

// ClientCount returns the number of connected clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// GetDroppedCount returns the number of mutations dropped due to queue overflow.
func (h *SSEHub) GetDroppedCount() int64 {
	return atomic.LoadInt64(&h.droppedCount)
}

// GetRetryQueueDepth returns the current number of mutations waiting in the retry queue.
func (h *SSEHub) GetRetryQueueDepth() int {
	h.retryMu.Lock()
	defer h.retryMu.Unlock()
	return len(h.retryQueue)
}

// GetUptime returns the duration since the hub was created.
func (h *SSEHub) GetUptime() time.Duration {
	return time.Since(h.startedAt)
}

// drainRetryQueue attempts to send queued mutations to the broadcast channel.
func (h *SSEHub) drainRetryQueue() {
	h.retryMu.Lock()
	defer h.retryMu.Unlock()

	if len(h.retryQueue) == 0 {
		return
	}

	// Try to drain as many as possible
	sent := 0
	for i, mutation := range h.retryQueue {
		select {
		case h.broadcast <- mutation:
			sent++
		default:
			// Broadcast channel full again, keep remaining in queue
			// Clear sent items to allow GC
			for j := 0; j < i; j++ {
				h.retryQueue[j] = nil
			}
			h.retryQueue = h.retryQueue[i:]
			if sent > 0 {
				log.Printf("SSE retry queue drained %d mutations, %d remaining", sent, len(h.retryQueue))
			}
			return
		}
	}

	// All sent, clear queue and allow GC
	for i := range h.retryQueue {
		h.retryQueue[i] = nil
	}
	h.retryQueue = h.retryQueue[:0]
	if sent > 0 {
		log.Printf("SSE retry queue fully drained %d mutations", sent)
	}
}

// handleSSE creates an HTTP handler for the SSE endpoint.
func handleSSE(hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent) http.HandlerFunc {
	var clientIDCounter int64

	return func(w http.ResponseWriter, r *http.Request) {
		// Thread-safe client ID generation
		clientID := atomic.AddInt64(&clientIDCounter, 1)
		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

		// Get flusher for streaming
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Parse Last-Event-ID header for reconnection catch-up
		var lastSince int64
		if lastEventID := r.Header.Get("Last-Event-ID"); lastEventID != "" {
			if ts, err := strconv.ParseInt(lastEventID, 10, 64); err == nil {
				lastSince = ts
			}
		}

		// Also accept ?since query parameter
		if since := r.URL.Query().Get("since"); since != "" {
			if ts, err := strconv.ParseInt(since, 10, 64); err == nil && ts > lastSince {
				lastSince = ts
			}
		}

		// Create client
		client := &SSEClient{
			id:        clientID,
			send:      make(chan *MutationPayload, 64),
			done:      make(chan struct{}),
			lastSince: lastSince,
		}

		// Check if shutting down before registering (r.Context() derives from
		// the server's BaseContext, which is cancelled on shutdown signal).
		select {
		case <-r.Context().Done():
			return
		default:
		}

		// Register with hub
		hub.RegisterClient(client)

		// Ensure cleanup on disconnect
		defer func() {
			hub.UnregisterClient(client)
			close(client.done)
		}()

		log.Printf("SSE client %d connected from %s (since=%d)", client.id, r.RemoteAddr, lastSince)

		// Send catch-up events if reconnecting
		if lastSince > 0 && getMutationsSince != nil {
			catchUpMutations := getMutationsSince(lastSince)
			for _, m := range catchUpMutations {
				payload := rpcMutationToPayload(m)
				writeSSEEvent(w, payload)
				flusher.Flush()
			}
		}

		// Send retry interval first so client knows reconnection time from the start
		fmt.Fprintf(w, "retry: %d\n\n", sseRetryMs)
		flusher.Flush()

		// Send initial connection event
		fmt.Fprintf(w, "event: connected\ndata: {\"clientId\":%d}\n\n", client.id)
		flusher.Flush()

		// Start heartbeat ticker to keep connection alive through proxies
		heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
		defer heartbeatTicker.Stop()

		// Stream events
		for {
			select {
			case mutation, ok := <-client.send:
				if !ok {
					// Channel closed
					return
				}
				writeSSEEvent(w, mutation)
				flusher.Flush()

			case <-heartbeatTicker.C:
				// Send SSE comment to keep connection alive
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					log.Printf("SSE client %d heartbeat failed: %v", client.id, err)
					return
				}
				flusher.Flush()

			case <-r.Context().Done():
				// Client disconnected
				log.Printf("SSE client %d disconnected", client.id)
				return
			}
		}
	}
}

// writeSSEEvent writes a mutation as an SSE event.
func writeSSEEvent(w http.ResponseWriter, mutation *MutationPayload) {
	eventID := sseEventIDCounter.Add(1)

	data, err := json.Marshal(mutation)
	if err != nil {
		log.Printf("SSE marshal error: %v", err)
		return
	}

	fmt.Fprintf(w, "id: %d\nevent: mutation\ndata: %s\n\n", eventID, string(data))
}

// rpcMutationToPayload converts an RPC mutation event to a payload.
func rpcMutationToPayload(m rpc.MutationEvent) *MutationPayload {
	return &MutationPayload{
		Type:      m.Type,
		IssueID:   m.IssueID,
		Title:     m.Title,
		Assignee:  m.Assignee,
		Actor:     m.Actor,
		Timestamp: m.Timestamp.UTC().Format(time.RFC3339),
		OldStatus: m.OldStatus,
		NewStatus: m.NewStatus,
		ParentID:  m.ParentID,
		StepCount: m.StepCount,
	}
}
