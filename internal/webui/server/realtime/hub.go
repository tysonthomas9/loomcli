package realtime

import (
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// RetryMs is the reconnection interval sent to clients in milliseconds.
	RetryMs = 5000
	// HeartbeatInterval is how often heartbeat comments are sent to keep connections alive.
	HeartbeatInterval = 30 * time.Second
	// ClientSendBuf is the per-client channel buffer size for outbound mutation events.
	ClientSendBuf = 64
)

// eventIDCounter provides monotonically increasing event IDs across all SSE connections.
// Initialized to current time in milliseconds so IDs remain roughly time-ordered,
// which is important for the Last-Event-ID catch-up mechanism.
var eventIDCounter atomic.Int64

func init() {
	eventIDCounter.Store(time.Now().UnixMilli())
}

// NextEventID returns the next monotonically increasing event ID.
func NextEventID() int64 {
	return eventIDCounter.Add(1)
}

// MutationPayload represents mutation data sent to clients.
type MutationPayload struct {
	Cursor     string `json:"cursor,omitempty"`      // Durable stream cursor for SSE Last-Event-ID when available
	Type       string `json:"type"`                  // create, update, delete, comment, status, bonded, squashed, burned, refresh, terminal_metadata, terminal_session_change
	EntityType string `json:"entity_type,omitempty"` // Generic changed entity type (issue, dependency, terminal, ...)
	EntityID   string `json:"entity_id,omitempty"`   // Generic changed entity identifier
	Action     string `json:"action,omitempty"`      // Source action, usually fleet-db action (issue.update, dep.add, ...)
	IssueID    string `json:"issue_id,omitempty"`    // Legacy issue identifier for issue-scoped consumers
	Title      string `json:"title,omitempty"`
	// Assignee is a pointer so issue.assign can distinguish an omitted field
	// from an explicit empty value. Clearing an assignee is a real projection
	// change and must survive JSON encoding for live clients.
	Assignee    *string `json:"assignee,omitempty"`
	Actor       string  `json:"actor,omitempty"`
	Timestamp   string  `json:"timestamp"`
	OldStatus   string  `json:"old_status,omitempty"`   // For status events
	NewStatus   string  `json:"new_status,omitempty"`   // For status events
	ParentID    string  `json:"parent_id,omitempty"`    // For bonded events
	StepCount   int     `json:"step_count,omitempty"`   // For bonded events
	Priority    *int    `json:"priority,omitempty"`     // Issue priority (for update events from external poll)
	SourceRepo  string  `json:"source_repo,omitempty"`  // Source repository for multi-repo filtering
	WorkspaceID string  `json:"workspace_id,omitempty"` // Workspace ID for multi-workspace filtering
}

// Hub manages connected SSE clients and broadcasts mutations to them.
type Hub struct {
	clients      map[*Client]bool
	register     chan *Client
	unregister   chan *Client
	broadcast    chan *MutationPayload
	mu           sync.RWMutex
	done         chan struct{}
	retryQueue   []*MutationPayload // Buffer when broadcast full
	retryMu      sync.Mutex
	droppedCount int64 // For metrics
	startedAt    time.Time
}

// Client represents a single SSE connection.
type Client struct {
	id          int64
	send        chan *MutationPayload
	done        chan struct{}
	lastSince   string
	sourceRepos []string // repos this client wants; empty = all
	workspaceID string   // workspace this client subscribed to; empty = no mutations (fail-closed)
}

// NewClient creates a new SSE client with the given parameters.
func NewClient(id int64, sendBuf int, lastSince string, sourceRepos []string, workspaceID string) *Client {
	return &Client{
		id:          id,
		send:        make(chan *MutationPayload, sendBuf),
		done:        make(chan struct{}),
		lastSince:   lastSince,
		sourceRepos: sourceRepos,
		workspaceID: workspaceID,
	}
}

// ID returns the client's unique identifier.
func (c *Client) ID() int64 { return c.id }

// Send returns the client's send channel.
func (c *Client) Send() <-chan *MutationPayload { return c.send }

// Done returns the client's done channel.
func (c *Client) Done() chan struct{} { return c.done }

// ParseSourceRepos parses a comma-separated source_repos query parameter,
// trimming whitespace and skipping empty entries.
func ParseSourceRepos(param string) []string {
	if param == "" {
		return nil
	}
	var repos []string
	for _, repo := range strings.Split(param, ",") {
		repo = strings.TrimSpace(repo)
		if repo != "" {
			repos = append(repos, repo)
		}
	}
	return repos
}

// MatchesWorkspaceFilter returns true if the mutation should be delivered.
// Fail-closed: empty client workspace or empty mutation workspace = no delivery.
func MatchesWorkspaceFilter(clientWorkspaceID, mutationWorkspaceID string) bool {
	return clientWorkspaceID != "" && mutationWorkspaceID != "" && clientWorkspaceID == mutationWorkspaceID
}

// MatchesSourceRepoFilter returns true if the mutation should be delivered
// to a client with the given sourceRepos filter. Empty sourceRepo -> true
// (intentional fan-out for events with unknown origin; client refetch is scoped).
func MatchesSourceRepoFilter(sourceRepos []string, sourceRepo string) bool {
	if len(sourceRepos) == 0 || sourceRepo == "" {
		return true
	}
	return slices.Contains(sourceRepos, sourceRepo)
}

// NewHub creates a new SSE hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client, 16),
		unregister: make(chan *Client, 16),
		broadcast:  make(chan *MutationPayload, 256),
		done:       make(chan struct{}),
		startedAt:  time.Now(),
	}
}

// Run starts the hub's main loop for managing clients and broadcasts.
func (h *Hub) Run() {
	retryTicker := time.NewTicker(100 * time.Millisecond)
	defer retryTicker.Stop()

	for {
		select {
		case client := <-h.register:
			h.addClient(client)
		case client := <-h.unregister:
			h.removeClient(client)
		case mutation := <-h.broadcast:
			h.fanOutMutation(mutation)
		case <-retryTicker.C:
			h.drainRetryQueue()
		case <-h.done:
			h.closeAllClients()
			return
		}
	}
}

// addClient registers a new SSE client.
func (h *Hub) addClient(client *Client) {
	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()
	slog.Info("SSE client registered", "client_id", client.id, "count", len(h.clients))
}

// removeClient unregisters an SSE client and closes its send channel.
func (h *Hub) removeClient(client *Client) {
	h.mu.Lock()
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
	}
	h.mu.Unlock()
	slog.Info("SSE client unregistered", "client_id", client.id, "count", len(h.clients))
}

// fanOutMutation sends a mutation to all matching connected clients.
func (h *Hub) fanOutMutation(mutation *MutationPayload) {
	if mutation.WorkspaceID == "" {
		slog.Warn("SSE: dropping mutation with empty workspace_id", "type", mutation.Type, "issue_id", mutation.IssueID)
		return
	}
	h.mu.RLock()
	var slow []*Client
	for client := range h.clients {
		if !MatchesWorkspaceFilter(client.workspaceID, mutation.WorkspaceID) {
			continue
		}
		if !MatchesSourceRepoFilter(client.sourceRepos, mutation.SourceRepo) {
			continue
		}
		select {
		case client.send <- mutation:
		default:
			slog.Warn("SSE client buffer full, disconnecting client", "client_id", client.id, "workspace_id", client.workspaceID)
			slow = append(slow, client)
		}
	}
	h.mu.RUnlock()
	for _, client := range slow {
		h.removeClient(client)
	}
}

// closeAllClients closes all client send channels and clears the client map.
func (h *Hub) closeAllClients() {
	h.mu.Lock()
	for client := range h.clients {
		close(client.send)
		delete(h.clients, client)
	}
	h.mu.Unlock()
}

// Stop gracefully stops the hub.
func (h *Hub) Stop() {
	close(h.done)
}

// RegisterClient adds a new client to the hub.
// Non-blocking if the hub has been stopped -- closes the client's send channel instead.
func (h *Hub) RegisterClient(client *Client) {
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
func (h *Hub) UnregisterClient(client *Client) {
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
func (h *Hub) Broadcast(mutation *MutationPayload) {
	select {
	case h.broadcast <- mutation:
	default:
		h.retryMu.Lock()
		if len(h.retryQueue) < 1024 {
			h.retryQueue = append(h.retryQueue, mutation)
			slog.Warn("SSE broadcast channel full, queued mutation", "queue_size", len(h.retryQueue))
		} else {
			atomic.AddInt64(&h.droppedCount, 1)
			slog.Warn("SSE retry queue full, dropped mutation", "total_dropped", atomic.LoadInt64(&h.droppedCount))
		}
		h.retryMu.Unlock()
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ClientCountForWorkspace returns the number of SSE clients for a workspace.
func (h *Hub) ClientCountForWorkspace(wsID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		if c.workspaceID == wsID {
			n++
		}
	}
	return n
}

// GetDroppedCount returns the number of mutations dropped due to queue overflow.
func (h *Hub) GetDroppedCount() int64 {
	return atomic.LoadInt64(&h.droppedCount)
}

// GetRetryQueueDepth returns the current number of mutations waiting in the retry queue.
func (h *Hub) GetRetryQueueDepth() int {
	h.retryMu.Lock()
	defer h.retryMu.Unlock()
	return len(h.retryQueue)
}

// GetUptime returns the duration since the hub was created.
func (h *Hub) GetUptime() time.Duration {
	return time.Since(h.startedAt)
}

// drainRetryQueue attempts to send queued mutations to the broadcast channel.
func (h *Hub) drainRetryQueue() {
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
				slog.Info("SSE retry queue partially drained", "sent", sent, "remaining", len(h.retryQueue))
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
		slog.Info("SSE retry queue fully drained", "sent", sent)
	}
}

// ParseLastSince extracts the reconnection catch-up cursor from the request.
// It preserves opaque cursors (fleet-db v2). If both header and query are
// present and both are numeric, the larger value wins; otherwise the explicit
// query parameter wins over Last-Event-ID.
func ParseLastSince(r *http.Request) string {
	lastEventID := r.Header.Get("Last-Event-ID")
	since := r.URL.Query().Get("since")
	if lastEventID == "" {
		return since
	}
	if since == "" {
		return lastEventID
	}
	headerTS, headerErr := strconv.ParseInt(lastEventID, 10, 64)
	queryTS, queryErr := strconv.ParseInt(since, 10, 64)
	if headerErr == nil && queryErr == nil && headerTS > queryTS {
		return lastEventID
	}
	return since
}

func ParseLastSinceMillis(r *http.Request) int64 {
	var lastSince int64
	if lastEventID := r.Header.Get("Last-Event-ID"); lastEventID != "" {
		if ts, err := strconv.ParseInt(lastEventID, 10, 64); err == nil {
			lastSince = ts
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if ts, err := strconv.ParseInt(since, 10, 64); err == nil && ts > lastSince {
			lastSince = ts
		}
	}
	return lastSince
}

// GetActiveSourceRepos returns deduplicated source repos across connected
// clients that have a repo filter. Returns nil when no client has a filter.
func (h *Hub) GetActiveSourceRepos() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[string]struct{})
	for c := range h.clients {
		for _, r := range c.sourceRepos {
			seen[r] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	return out
}
