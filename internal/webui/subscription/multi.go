package subscription

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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
)

// workspaceSubscriber abstracts a per-workspace Work Items mutation source.
type workspaceSubscriber interface {
	Start()
	Stop()
	GetMutationsAfter(cursor string) []workitems.Mutation
}

// subscriberEntry tracks a subscriber and when it last had SSE clients.
type subscriberEntry struct {
	sub       workspaceSubscriber
	idleSince time.Time // when client count first dropped to 0; zero if clients connected
}

// MultiWorkspaceSubscriber manages per-workspace backend subscribers and
// broadcasts workspace-tagged mutations to a shared SSE hub.
type MultiWorkspaceSubscriber struct {
	hub         *realtime.Hub
	logger      *slog.Logger
	subscribers map[string]*subscriberEntry // workspace ID → entry

	idleDeactivationInterval time.Duration
	idleDeactivationTimeout  time.Duration

	mu        sync.RWMutex
	closed    bool
	startOnce sync.Once
	idleWG    sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewStartedMultiWorkspaceSubscriber creates a MultiWorkspaceSubscriber and
// starts its process-lifetime manager loop.
func NewStartedMultiWorkspaceSubscriber(ctx context.Context, hub *realtime.Hub, logger *slog.Logger) *MultiWorkspaceSubscriber {
	m := NewMultiWorkspaceSubscriber(hub, logger)
	m.Start(ctx)
	return m
}

// NewMultiWorkspaceSubscriber creates a new MultiWorkspaceSubscriber.
func NewMultiWorkspaceSubscriber(hub *realtime.Hub, logger *slog.Logger) *MultiWorkspaceSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &MultiWorkspaceSubscriber{
		hub:                      hub,
		logger:                   logger,
		subscribers:              make(map[string]*subscriberEntry),
		idleDeactivationInterval: defaultIdleDeactivationInterval,
		idleDeactivationTimeout:  defaultIdleDeactivationTimeout,
	}
}

// AddWorkspaceWithStream creates and starts a WorkItemMutationSubscriber for
// the given workspace, sourcing mutations from the supplied Work Items stream. Mirrors
// AddWorkspace's contract: idempotent under wsID, takes the same write
// lock to close the TOCTOU window between HasSubscriber and insertion.
// Returns an error if b is nil.
func (m *MultiWorkspaceSubscriber) AddWorkspaceWithStream(wsID string, stream workitems.MutationStream) error {
	return m.EnsureActive(context.Background(), wsID, stream, ActivationReasonRegistry)
}

// EnsureActive creates and starts the per-workspace backend subscriber if one
// is not already active. SSE token/stream traffic activates subscribers;
// connected SSE clients retain them, and the manager idle loop removes
// subscribers with no SSE clients after the idle grace period.
func (m *MultiWorkspaceSubscriber) EnsureActive(ctx context.Context, wsID string, stream workitems.MutationStream, reason ActivationReason) error {
	if stream == nil {
		return fmt.Errorf("EnsureActive: mutation stream must not be nil for workspace %q", wsID)
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("EnsureActive: subscriber manager is stopped")
	}
	if _, ok := m.subscribers[wsID]; ok {
		return nil
	}

	sub := NewWorkItemMutationSubscriber(stream, m.hub, wsID)
	sub.Start()
	m.subscribers[wsID] = &subscriberEntry{sub: sub}

	m.logger.Info("workspace backend subscriber started", "workspace", wsID, "reason", reason)
	return nil
}

// HasSubscriber returns true if a subscriber is registered for the workspace.
func (m *MultiWorkspaceSubscriber) HasSubscriber(wsID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.subscribers[wsID]
	return ok
}

// RemoveWorkspace stops and removes the subscriber for the given workspace.
func (m *MultiWorkspaceSubscriber) RemoveWorkspace(wsID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.subscribers[wsID]; ok {
		entry.sub.Stop()
		delete(m.subscribers, wsID)
		m.logger.Info("workspace subscriber stopped and removed", "workspace", wsID)
	}
}

// Start starts the process-lifetime manager loop. Stop does not make the
// manager restartable.
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

// Stop gracefully stops all workspace subscribers.
func (m *MultiWorkspaceSubscriber) Stop() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.idleWG.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	for wsID, entry := range m.subscribers {
		entry.sub.Stop()
		m.logger.Info("workspace subscriber stopped", "workspace", wsID)
	}
	m.subscribers = make(map[string]*subscriberEntry)
}

// WorkspaceIDs returns a sorted list of active workspace subscription IDs.
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

// GetMutationsSince retrieves mutations since the given timestamp from all
// workspace subscribers. Used for SSE client reconnection catch-up.
func (m *MultiWorkspaceSubscriber) GetMutationsSince(since string) []realtime.MutationEvent {
	m.mu.RLock()
	entries := make(map[string]*subscriberEntry, len(m.subscribers))
	for k, v := range m.subscribers {
		entries[k] = v
	}
	m.mu.RUnlock()

	var all []realtime.MutationEvent
	for _, entry := range entries {
		muts := entry.sub.GetMutationsAfter(since)
		for _, m := range muts {
			all = append(all, realtime.WorkItemMutationToEvent(m))
		}
	}
	return all
}

// idleDeactivationLoop periodically deactivates subscribers with no SSE clients.
func (m *MultiWorkspaceSubscriber) idleDeactivationLoop() {
	ticker := time.NewTicker(m.idleDeactivationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			// Snapshot workspace IDs under read lock, then query hub WITHOUT holding m.mu
			// to avoid lock inversion (m.mu → h.mu).
			m.mu.RLock()
			wsIDs := make([]string, 0, len(m.subscribers))
			for id := range m.subscribers {
				wsIDs = append(wsIDs, id)
			}
			m.mu.RUnlock()

			clientCounts := make(map[string]int, len(wsIDs))
			for _, id := range wsIDs {
				clientCounts[id] = m.hub.ClientCountForWorkspace(id)
			}

			// Now take write lock and apply deactivation decisions.
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
				if now.Sub(entry.idleSince) >= m.idleDeactivationTimeout {
					m.logger.Info("deactivating idle subscriber",
						"workspace", wsID, "idle_for", now.Sub(entry.idleSince).Round(time.Second))
					entry.sub.Stop()
					delete(m.subscribers, wsID)
				}
			}
			m.mu.Unlock()
		}
	}
}

// GetMutationsSinceForWorkspace retrieves mutations since the given timestamp
// from a specific workspace's subscriber only. Returns nil if the workspace
// has no active subscriber.
func (m *MultiWorkspaceSubscriber) GetMutationsSinceForWorkspace(wsID string, since string) []realtime.MutationEvent {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	muts := entry.sub.GetMutationsAfter(since)
	if len(muts) == 0 {
		return nil
	}
	out := make([]realtime.MutationEvent, len(muts))
	for i, m := range muts {
		out[i] = realtime.WorkItemMutationToEvent(m)
	}
	return out
}
