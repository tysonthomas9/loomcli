package subscription

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	defaultIdleDeactivationInterval = 15 * time.Second
	defaultIdleDeactivationTimeout  = 60 * time.Second
)

// ActivationReason records why a workspace subscriber was activated.
type ActivationReason string

const (
	ActivationReasonHTTP     ActivationReason = "http"
	ActivationReasonRegistry ActivationReason = "registry"
	ActivationReasonLegacy   ActivationReason = "legacy"
	ActivationReasonSSE      ActivationReason = "sse"
)

type workspaceSubscriber interface {
	Start()
	Stop()
	Ready(context.Context) (string, error)
	Head() string
	GetMutationPage(context.Context, string, int) (backend.MutationPage, error)
	GetMutationPageThrough(context.Context, string, string, int) (backend.MutationPage, error)
}

type subscriberEntry struct {
	sub       workspaceSubscriber
	starting  bool
	idleSince time.Time
}

// MultiWorkspaceSubscriber manages per-workspace backend subscribers and
// shares each subscriber's readiness result across concurrent activations.
type MultiWorkspaceSubscriber struct {
	hub         *realtime.Hub
	logger      *slog.Logger
	subscribers map[string]*subscriberEntry
	lastHeads   map[string]string
	budgets     subscriberBudgets

	idleDeactivationInterval time.Duration
	idleDeactivationTimeout  time.Duration

	mu        sync.RWMutex
	closed    bool
	startOnce sync.Once
	idleWG    sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewStartedMultiWorkspaceSubscriber creates and starts a process-lifetime
// multi-workspace subscriber manager.
func NewStartedMultiWorkspaceSubscriber(ctx context.Context, hub *realtime.Hub, logger *slog.Logger) *MultiWorkspaceSubscriber {
	m := NewMultiWorkspaceSubscriber(hub, logger)
	m.Start(ctx)
	return m
}

// NewMultiWorkspaceSubscriber creates a subscriber manager.
func NewMultiWorkspaceSubscriber(hub *realtime.Hub, logger *slog.Logger) *MultiWorkspaceSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &MultiWorkspaceSubscriber{
		hub:                      hub,
		logger:                   logger,
		subscribers:              make(map[string]*subscriberEntry),
		lastHeads:                make(map[string]string),
		budgets:                  defaultSubscriberBudgets(),
		idleDeactivationInterval: defaultIdleDeactivationInterval,
		idleDeactivationTimeout:  defaultIdleDeactivationTimeout,
	}
}

// AddWorkspaceWithBackend activates a subscriber for legacy callers.
func (m *MultiWorkspaceSubscriber) AddWorkspaceWithBackend(wsID string, b backend.IssueBackend) error {
	_, err := m.EnsureActive(context.Background(), wsID, b, ActivationReasonLegacy)
	return err
}

// EnsureActive installs at most one starting subscriber for a workspace and
// waits outside the manager lock for its complete head cursor.
func (m *MultiWorkspaceSubscriber) EnsureActive(
	ctx context.Context,
	wsID string,
	b backend.IssueBackend,
	reason ActivationReason,
) (string, error) {
	if b == nil {
		return "", fmt.Errorf("EnsureActive: backend must not be nil for workspace %q", wsID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	entry, created, err := m.ensureStartingSubscriber(wsID, b)
	if err != nil {
		return "", err
	}

	head, err := entry.sub.Ready(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.mu.Lock()
			if m.subscribers[wsID] == entry {
				m.rememberHeadLocked(wsID, entry.sub.Head())
				delete(m.subscribers, wsID)
			}
			m.mu.Unlock()
			entry.sub.Stop()
			m.rememberHead(wsID, entry.sub.Head())
		}
		return "", err
	}

	m.mu.Lock()
	if m.subscribers[wsID] == entry {
		entry.starting = false
	}
	m.mu.Unlock()
	if created {
		m.logger.Info("workspace backend subscriber started", "workspace", wsID, "reason", reason, "cursor", head)
	}
	return head, nil
}

func (m *MultiWorkspaceSubscriber) ensureStartingSubscriber(
	wsID string,
	b backend.IssueBackend,
) (*subscriberEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, fmt.Errorf("EnsureActive: subscriber manager is stopped")
	}
	if entry, exists := m.subscribers[wsID]; exists {
		return entry, false, nil
	}

	initialHead := m.lastHeads[wsID]
	entry := &subscriberEntry{
		sub:      newBackendMutationSubscriber(b, m.hub, wsID, initialHead, m.budgets),
		starting: true,
	}
	m.subscribers[wsID] = entry
	entry.sub.Start()
	return entry, true, nil
}

// Ready waits for an existing workspace subscriber's head cursor.
func (m *MultiWorkspaceSubscriber) Ready(ctx context.Context, wsID string) (string, error) {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	closed := m.closed
	m.mu.RUnlock()
	if !ok {
		if closed {
			return "", fmt.Errorf("subscriber manager is stopped")
		}
		return "", fmt.Errorf("workspace %q has no active subscriber", wsID)
	}
	return entry.sub.Ready(ctx)
}

// GetMutationPageForWorkspace returns one catch-up page for an active
// workspace subscriber.
func (m *MultiWorkspaceSubscriber) GetMutationPageForWorkspace(
	ctx context.Context,
	wsID string,
	since string,
	limit int,
) (backend.MutationPage, error) {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	m.mu.RUnlock()
	if !ok {
		return backend.MutationPage{}, fmt.Errorf("workspace %q has no active subscriber", wsID)
	}
	return entry.sub.GetMutationPage(ctx, since, limit)
}

// GetMutationPageThroughForWorkspace keeps the subscriber identity fixed for
// the entire request, rejecting results from a retired or replaced workspace.
func (m *MultiWorkspaceSubscriber) GetMutationPageThroughForWorkspace(ctx context.Context, wsID, since, through string, limit int) (backend.MutationPage, error) {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	closed := m.closed
	m.mu.RUnlock()
	if !ok || closed {
		return backend.MutationPage{}, fmt.Errorf("workspace %q has no active subscriber", wsID)
	}
	page, err := entry.sub.GetMutationPageThrough(ctx, since, through, limit)
	if err != nil {
		return backend.MutationPage{}, err
	}
	m.mu.RLock()
	current := m.subscribers[wsID]
	closed = m.closed
	m.mu.RUnlock()
	if current != entry || closed {
		return backend.MutationPage{}, fmt.Errorf("workspace %q subscriber changed during bounded replay", wsID)
	}
	if err := ctx.Err(); err != nil {
		return backend.MutationPage{}, err
	}
	return page, nil
}

// HasSubscriber reports whether a workspace has an active or starting entry.
func (m *MultiWorkspaceSubscriber) HasSubscriber(wsID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.subscribers[wsID]
	return ok
}

// RemoveWorkspace stops and removes a workspace subscriber, retaining its
// last in-memory head for an old-FleetDB reactivation drain.
func (m *MultiWorkspaceSubscriber) RemoveWorkspace(wsID string) {
	m.mu.Lock()
	entry, ok := m.subscribers[wsID]
	if ok {
		m.rememberHeadLocked(wsID, entry.sub.Head())
		delete(m.subscribers, wsID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	entry.sub.Stop()
	m.rememberHead(wsID, entry.sub.Head())
	m.logger.Info("workspace subscriber stopped and removed", "workspace", wsID)
}

// Start starts the manager's idle-deactivation loop once.
func (m *MultiWorkspaceSubscriber) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.startOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			return
		}
		m.ctx, m.cancel = context.WithCancel(ctx)
		m.idleWG.Add(1)
		go func() {
			defer m.idleWG.Done()
			m.idleDeactivationLoop()
		}()
	})
}

// Stop stops the manager and all subscribers, releasing any readiness waiters.
func (m *MultiWorkspaceSubscriber) Stop() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel := m.cancel
	entries := make(map[string]*subscriberEntry, len(m.subscribers))
	for wsID, entry := range m.subscribers {
		entries[wsID] = entry
		m.rememberHeadLocked(wsID, entry.sub.Head())
	}
	m.subscribers = make(map[string]*subscriberEntry)
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.idleWG.Wait()
	for wsID, entry := range entries {
		entry.sub.Stop()
		m.rememberHead(wsID, entry.sub.Head())
		m.logger.Info("workspace subscriber stopped", "workspace", wsID)
	}
}

// WorkspaceIDs returns sorted active workspace IDs.
func (m *MultiWorkspaceSubscriber) WorkspaceIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.subscribers))
	for id := range m.subscribers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *MultiWorkspaceSubscriber) idleDeactivationLoop() {
	ticker := time.NewTicker(m.idleDeactivationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.deactivateIdle(now)
		}
	}
}

func (m *MultiWorkspaceSubscriber) deactivateIdle(now time.Time) {
	m.mu.RLock()
	wsIDs := make([]string, 0, len(m.subscribers))
	for id := range m.subscribers {
		wsIDs = append(wsIDs, id)
	}
	m.mu.RUnlock()

	clientCounts := make(map[string]int, len(wsIDs))
	for _, id := range wsIDs {
		if m.hub != nil {
			clientCounts[id] = m.hub.ClientCountForWorkspace(id)
		}
	}

	var stopped map[string]*subscriberEntry
	m.mu.Lock()
	for wsID, entry := range m.subscribers {
		if clientCounts[wsID] > 0 {
			entry.idleSince = time.Time{}
			continue
		}
		if entry.idleSince.IsZero() {
			entry.idleSince = now
			continue
		}
		if now.Sub(entry.idleSince) < m.idleDeactivationTimeout {
			continue
		}
		if stopped == nil {
			stopped = make(map[string]*subscriberEntry)
		}
		stopped[wsID] = entry
		m.rememberHeadLocked(wsID, entry.sub.Head())
		delete(m.subscribers, wsID)
	}
	m.mu.Unlock()

	for wsID, entry := range stopped {
		m.logger.Info("deactivating idle subscriber", "workspace", wsID)
		entry.sub.Stop()
		m.rememberHead(wsID, entry.sub.Head())
	}
}

func (m *MultiWorkspaceSubscriber) rememberHeadLocked(wsID, head string) {
	if head != "" {
		m.lastHeads[wsID] = head
	}
}

func (m *MultiWorkspaceSubscriber) rememberHead(wsID, head string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rememberHeadLocked(wsID, head)
}

func parseCursorMillis(cursor string) int64 {
	if cursor == "" {
		return 0
	}
	n, _ := strconv.ParseInt(cursor, 10, 64)
	return n
}
