package subscription

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// MultiWorkspaceSubscriber manages per-workspace DaemonSubscribers, each polling
// its own daemon and broadcasting workspace-tagged mutations to a shared SSEHub.
type MultiWorkspaceSubscriber struct {
	hub         *realtime.Hub
	multiPool   *daemon.MultiPool
	logger      *slog.Logger
	subscribers map[string]*DaemonSubscriber // workspace ID → subscriber
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewMultiWorkspaceSubscriber creates a new MultiWorkspaceSubscriber.
func NewMultiWorkspaceSubscriber(hub *realtime.Hub, multiPool *daemon.MultiPool, logger *slog.Logger) *MultiWorkspaceSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &MultiWorkspaceSubscriber{
		hub:         hub,
		multiPool:   multiPool,
		logger:      logger,
		subscribers: make(map[string]*DaemonSubscriber),
	}
}

// AddWorkspace creates and starts a DaemonSubscriber for the given workspace.
// The subscriber uses the pool from MultiPool and tags all MutationPayloads
// with the workspace ID. Returns an error if the workspace pool is not registered.
func (m *MultiWorkspaceSubscriber) AddWorkspace(wsID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If a subscriber already exists for this workspace, stop it first
	if existing, ok := m.subscribers[wsID]; ok {
		m.logger.Info("replacing existing subscriber for workspace", "workspace", wsID)
		existing.Stop()
		delete(m.subscribers, wsID)
	}

	pool := m.multiPool.PoolForWorkspace(wsID)
	if pool == nil {
		return fmt.Errorf("no pool registered for workspace %q", wsID)
	}

	sub := NewDaemonSubscriber(pool, m.hub)
	sub.workspaceID = wsID
	sub.Start()
	m.subscribers[wsID] = sub

	m.logger.Info("workspace subscriber started", "workspace", wsID)
	return nil
}

// RemoveWorkspace stops and removes the subscriber for the given workspace.
func (m *MultiWorkspaceSubscriber) RemoveWorkspace(wsID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sub, ok := m.subscribers[wsID]; ok {
		sub.Stop()
		delete(m.subscribers, wsID)
		m.logger.Info("workspace subscriber stopped and removed", "workspace", wsID)
	}
}

// Start starts all registered workspace subscribers. It also stores a context
// for managing the lifetime of the multi-subscriber.
func (m *MultiWorkspaceSubscriber) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ctx, m.cancel = context.WithCancel(ctx)

	for wsID, sub := range m.subscribers {
		sub.Start()
		m.logger.Info("workspace subscriber started", "workspace", wsID)
	}
}

// Stop gracefully stops all workspace subscribers.
func (m *MultiWorkspaceSubscriber) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}

	for wsID, sub := range m.subscribers {
		sub.Stop()
		m.logger.Info("workspace subscriber stopped", "workspace", wsID)
	}
	// Clear the map so Stop is idempotent
	m.subscribers = make(map[string]*DaemonSubscriber)
}

// WorkspaceIDs returns a sorted list of active workspace subscription IDs.
func (m *MultiWorkspaceSubscriber) WorkspaceIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.subscribers))
	for id := range m.subscribers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetMutationsSince retrieves mutations since the given timestamp from all
// workspace subscribers. This is used for SSE client reconnection catch-up.
func (m *MultiWorkspaceSubscriber) GetMutationsSince(since int64) []rpc.MutationEvent {
	m.mu.Lock()
	subs := make(map[string]*DaemonSubscriber, len(m.subscribers))
	for k, v := range m.subscribers {
		subs[k] = v
	}
	m.mu.Unlock()

	var all []rpc.MutationEvent
	for _, sub := range subs {
		events := sub.GetMutationsSince(since)
		all = append(all, events...)
	}
	return all
}

// GetMutationsSinceForWorkspace retrieves mutations since the given timestamp
// from a specific workspace's subscriber only. Returns nil if the workspace
// has no active subscriber. This is used by the SSE handler for workspace-scoped
// reconnection catch-up.
func (m *MultiWorkspaceSubscriber) GetMutationsSinceForWorkspace(wsID string, since int64) []rpc.MutationEvent {
	m.mu.Lock()
	sub, ok := m.subscribers[wsID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return sub.GetMutationsSince(since)
}
