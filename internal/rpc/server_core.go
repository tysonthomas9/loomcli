package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// ServerVersion is the version of this RPC server
// This should match the bd CLI version for proper compatibility checks
// It's set dynamically by daemon.go from cmd/bd/version.go before starting the server
var ServerVersion = "0.0.0" // Placeholder; overridden by daemon startup

const (
	statusUnhealthy = "unhealthy"
)

// Server represents the RPC server that runs in the daemon
type Server struct {
	socketPath    string
	workspacePath string          // Absolute path to workspace root
	dbPath        string          // Absolute path to database file
	storage       storage.Storage // Default storage (for backward compat)
	listener      net.Listener
	mu            sync.RWMutex
	shutdown      bool
	shutdownChan  chan struct{}
	stopOnce      sync.Once
	doneChan      chan struct{} // closed when Start() cleanup is complete
	// Health and metrics
	startTime        time.Time
	lastActivityTime atomic.Value // time.Time - last request timestamp
	metrics          *Metrics
	// Connection limiting
	maxConns      int
	activeConns   int32 // atomic counter
	connSemaphore chan struct{}
	// Request timeout
	requestTimeout time.Duration
	// Ready channel signals when server is listening
	readyChan chan struct{}
	// Auto-import single-flight guard
	importInProgress atomic.Bool
	// Mutation events for event-driven daemon
	mutationChan  chan MutationEvent
	droppedEvents atomic.Int64 // Counter for dropped mutation events
	// Recent mutations buffer for polling (circular buffer, max 100 events)
	recentMutations   []MutationEvent
	recentMutationsMu sync.RWMutex
	maxMutationBuffer int
	// Broadcast subscribers for WaitForMutations (separate from mutationChan)
	subscribersMu sync.Mutex
	subscribers   map[uint64]chan struct{} // subscriber ID -> notification channel
	nextSubID     uint64
	// Daemon configuration (set via SetConfig after creation)
	autoCommit   bool
	autoPush     bool
	autoPull     bool
	localMode    bool
	syncInterval string
	daemonMode   string
}

// Mutation event types
const (
	MutationCreate  = "create"
	MutationUpdate  = "update"
	MutationDelete  = "delete"
	MutationComment = "comment"
	// Molecule-specific event types for activity feed
	MutationBonded   = "bonded"   // Molecule bonded to parent (dynamic bond)
	MutationSquashed = "squashed" // Wisp squashed to digest
	MutationBurned   = "burned"   // Wisp discarded without digest
	MutationStatus   = "status"   // Status change (in_progress, completed, failed)
)

// MutationEvent represents a database mutation for event-driven sync
type MutationEvent struct {
	Type      string // One of the Mutation* constants
	IssueID   string // e.g., "bd-42"
	Title     string // Issue title for display context (may be empty for some operations)
	Assignee  string // Issue assignee for display context (may be empty)
	Actor     string // Who performed the action (may differ from assignee)
	Timestamp time.Time
	// Optional metadata for richer events (used by status, bonded, etc.)
	OldStatus string `json:"old_status,omitempty"` // Previous status (for status events)
	NewStatus string `json:"new_status,omitempty"` // New status (for status events)
	ParentID  string `json:"parent_id,omitempty"`  // Parent molecule (for bonded events)
	StepCount int    `json:"step_count,omitempty"` // Number of steps (for bonded events)
}

// NewServer creates a new RPC server
func NewServer(socketPath string, store storage.Storage, workspacePath string, dbPath string) *Server {
	// Parse config from env vars
	maxConns := 100 // default
	if env := os.Getenv("BEADS_DAEMON_MAX_CONNS"); env != "" {
		var conns int
		if _, err := fmt.Sscanf(env, "%d", &conns); err == nil && conns > 0 {
			maxConns = conns
		}
	}

	requestTimeout := 30 * time.Second // default
	if env := os.Getenv("BEADS_DAEMON_REQUEST_TIMEOUT"); env != "" {
		if timeout, err := time.ParseDuration(env); err == nil && timeout > 0 {
			requestTimeout = timeout
		}
	}

	mutationBufferSize := 512 // default (increased from 100 for better burst handling)
	if env := os.Getenv("BEADS_MUTATION_BUFFER"); env != "" {
		var bufSize int
		if _, err := fmt.Sscanf(env, "%d", &bufSize); err == nil && bufSize > 0 {
			mutationBufferSize = bufSize
		}
	}

	s := &Server{
		socketPath:        socketPath,
		workspacePath:     workspacePath,
		dbPath:            dbPath,
		storage:           store,
		shutdownChan:      make(chan struct{}),
		doneChan:          make(chan struct{}),
		startTime:         time.Now(),
		metrics:           NewMetrics(),
		maxConns:          maxConns,
		connSemaphore:     make(chan struct{}, maxConns),
		requestTimeout:    requestTimeout,
		readyChan:         make(chan struct{}),
		mutationChan:      make(chan MutationEvent, mutationBufferSize), // Configurable buffer
		recentMutations:   make([]MutationEvent, 0, 100),
		maxMutationBuffer: 100,
		subscribers:       make(map[uint64]chan struct{}),
	}
	s.lastActivityTime.Store(time.Now())
	return s
}

// emitMutation sends a mutation event to the daemon's event-driven loop.
// Non-blocking: drops event if channel is full (sync will happen eventually).
// Also stores in recent mutations buffer for polling.
// Title and assignee provide context for activity feeds; pass empty strings if unknown.
func (s *Server) emitMutation(eventType, issueID, title, assignee string) {
	s.emitRichMutation(MutationEvent{
		Type:     eventType,
		IssueID:  issueID,
		Title:    title,
		Assignee: assignee,
	})
}

// emitRichMutation sends a pre-built mutation event with optional metadata.
// Use this for events that include additional context (status changes, bonded events, etc.)
// Non-blocking: drops event if channel is full (sync will happen eventually).
func (s *Server) emitRichMutation(event MutationEvent) {
	// Always set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Send to mutation channel for daemon
	select {
	case s.mutationChan <- event:
		// Event sent successfully
	default:
		// Channel full, increment dropped events counter
		s.droppedEvents.Add(1)
	}

	// Store in recent mutations buffer for polling
	s.recentMutationsMu.Lock()
	s.recentMutations = append(s.recentMutations, event)
	// Keep buffer size limited (circular buffer behavior)
	if len(s.recentMutations) > s.maxMutationBuffer {
		s.recentMutations = s.recentMutations[1:]
	}
	s.recentMutationsMu.Unlock()

	// Notify all WaitForMutations subscribers
	s.notifySubscribers()
}

// MutationChan returns the mutation event channel for the daemon to consume
func (s *Server) MutationChan() <-chan MutationEvent {
	return s.mutationChan
}

// subscribeMutations registers a subscriber that will be notified when mutations occur.
// Returns a notification channel and an unsubscribe function. The caller must call
// unsubscribe when done to prevent resource leaks.
func (s *Server) subscribeMutations() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.subscribersMu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = ch
	s.subscribersMu.Unlock()

	unsubscribe := func() {
		s.subscribersMu.Lock()
		delete(s.subscribers, id)
		s.subscribersMu.Unlock()
	}
	return ch, unsubscribe
}

// notifySubscribers sends a non-blocking notification to all WaitForMutations subscribers.
func (s *Server) notifySubscribers() {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	for _, ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
			// Already notified, skip
		}
	}
}

// SetConfig sets the daemon configuration for status reporting
func (s *Server) SetConfig(autoCommit, autoPush, autoPull, localMode bool, syncInterval, daemonMode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoCommit = autoCommit
	s.autoPush = autoPush
	s.autoPull = autoPull
	s.localMode = localMode
	s.syncInterval = syncInterval
	s.daemonMode = daemonMode
}

// ResetDroppedEventsCount resets the dropped events counter and returns the previous value
func (s *Server) ResetDroppedEventsCount() int64 {
	return s.droppedEvents.Swap(0)
}

// GetRecentMutations returns mutations since the given timestamp
func (s *Server) GetRecentMutations(sinceMillis int64) []MutationEvent {
	s.recentMutationsMu.RLock()
	defer s.recentMutationsMu.RUnlock()

	var result []MutationEvent
	for _, m := range s.recentMutations {
		if m.Timestamp.UnixMilli() > sinceMillis {
			result = append(result, m)
		}
	}
	return result
}

// marshalMutations marshals mutation events into a Response, handling errors.
func marshalMutations(mutations []MutationEvent) Response {
	data, err := json.Marshal(mutations)
	if err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("failed to marshal mutations: %v", err),
		}
	}
	return Response{
		Success: true,
		Data:    data,
	}
}

// handleGetMutations handles the get_mutations RPC operation
func (s *Server) handleGetMutations(req *Request) Response {
	var args GetMutationsArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("invalid arguments: %v", err),
		}
	}

	mutations := s.GetRecentMutations(args.Since)
	return marshalMutations(mutations)
}

// handleWaitForMutations handles the wait_for_mutations RPC operation.
// This is a blocking call that returns immediately if mutations exist since the
// given timestamp, or waits up to the timeout for new mutations to arrive.
// The connCtx is canceled when the client disconnects, allowing early cleanup.
func (s *Server) handleWaitForMutations(req *Request, connCtx context.Context) Response {
	var args WaitForMutationsArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("invalid arguments: %v", err),
		}
	}

	// Set default timeout to 30 seconds
	timeout := 30 * time.Second
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Millisecond
	}

	// First check for existing mutations since the timestamp
	mutations := s.GetRecentMutations(args.Since)
	if len(mutations) > 0 {
		return marshalMutations(mutations)
	}

	// Subscribe to mutation broadcasts (per-subscriber channel, not shared)
	notifyCh, unsubscribe := s.subscribeMutations()
	defer unsubscribe()

	// No existing mutations, wait for new ones
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	emptyResponse := marshalMutations([]MutationEvent{})

	select {
	case <-notifyCh:
		// Got a mutation notification via broadcast.
		// Mutations are already stored in the recent buffer by emitRichMutation().
		return marshalMutations(s.GetRecentMutations(args.Since))

	case <-timer.C:
		// Timeout, return empty array
		return emptyResponse

	case <-connCtx.Done():
		// Client disconnected, bail out immediately to free FD and semaphore slot
		return emptyResponse

	case <-s.shutdownChan:
		// Server shutting down
		return emptyResponse
	}
}

// handleGetMoleculeProgress handles the get_molecule_progress RPC operation
// Returns detailed progress for a molecule (parent issue with child steps)
func (s *Server) handleGetMoleculeProgress(req *Request) Response {
	var args GetMoleculeProgressArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("invalid arguments: %v", err),
		}
	}

	store := s.storage
	if store == nil {
		return Response{
			Success: false,
			Error:   "storage not available",
		}
	}

	ctx, cancel := s.reqCtx(req)
	defer cancel()

	// Get the molecule (parent issue)
	molecule, err := store.GetIssue(ctx, args.MoleculeID)
	if err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("failed to get molecule: %v", err),
		}
	}
	if molecule == nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("molecule not found: %s", args.MoleculeID),
		}
	}

	// Get children (issues that have parent-child dependency on this molecule)
	var children []*types.IssueWithDependencyMetadata
	if sqliteStore, ok := store.(interface {
		GetDependentsWithMetadata(ctx context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error)
	}); ok {
		allDependents, err := sqliteStore.GetDependentsWithMetadata(ctx, args.MoleculeID)
		if err != nil {
			return Response{
				Success: false,
				Error:   fmt.Sprintf("failed to get molecule children: %v", err),
			}
		}
		// Filter for parent-child relationships only
		for _, dep := range allDependents {
			if dep.DependencyType == types.DepParentChild {
				children = append(children, dep)
			}
		}
	}

	// Get blocked issue IDs for status computation
	blockedIDs := make(map[string]bool)
	if sqliteStore, ok := store.(interface {
		GetBlockedIssueIDs(ctx context.Context) ([]string, error)
	}); ok {
		ids, err := sqliteStore.GetBlockedIssueIDs(ctx)
		if err == nil {
			for _, id := range ids {
				blockedIDs[id] = true
			}
		}
	}

	// Build steps from children
	steps := make([]MoleculeStep, 0, len(children))
	for _, child := range children {
		step := MoleculeStep{
			ID:    child.ID,
			Title: child.Title,
		}

		// Compute step status
		switch child.Status {
		case types.StatusClosed:
			step.Status = "done"
		case types.StatusInProgress:
			step.Status = "current"
		default: // open, blocked, etc.
			if blockedIDs[child.ID] {
				step.Status = "blocked"
			} else {
				step.Status = "ready"
			}
		}

		// Set timestamps
		startTime := child.CreatedAt.Format(time.RFC3339)
		step.StartTime = &startTime

		if child.ClosedAt != nil {
			closeTime := child.ClosedAt.Format(time.RFC3339)
			step.CloseTime = &closeTime
		}

		steps = append(steps, step)
	}

	progress := MoleculeProgress{
		MoleculeID: molecule.ID,
		Title:      molecule.Title,
		Assignee:   molecule.Assignee,
		Steps:      steps,
	}

	data, _ := json.Marshal(progress)
	return Response{
		Success: true,
		Data:    data,
	}
}
