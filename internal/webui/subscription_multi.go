package webui

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

const idleDeactivationTimeout = 60 * time.Second

// subscriberEntry tracks a subscriber and when it last had SSE clients.
type subscriberEntry struct {
	sub       *DaemonSubscriber
	idleSince time.Time // when client count first dropped to 0; zero if clients connected
}

// MultiWorkspaceSubscriber manages per-workspace DaemonSubscribers, each polling
// its own daemon and broadcasting workspace-tagged mutations to a shared SSEHub.
type MultiWorkspaceSubscriber struct {
	hub         *SSEHub
	multiPool   *daemon.MultiPool
	logger      *slog.Logger
	subscribers map[string]*subscriberEntry // workspace ID → entry
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewMultiWorkspaceSubscriber creates a new MultiWorkspaceSubscriber.
func NewMultiWorkspaceSubscriber(hub *SSEHub, multiPool *daemon.MultiPool, logger *slog.Logger) *MultiWorkspaceSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &MultiWorkspaceSubscriber{
		hub:         hub,
		multiPool:   multiPool,
		logger:      logger,
		subscribers: make(map[string]*subscriberEntry),
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
		existing.sub.Stop()
		delete(m.subscribers, wsID)
	}

	pool := m.multiPool.PoolForWorkspace(wsID)
	if pool == nil {
		return fmt.Errorf("no pool registered for workspace %q", wsID)
	}

	sub := NewDaemonSubscriber(pool, m.hub)
	sub.workspaceID = wsID
	sub.Start()
	m.subscribers[wsID] = &subscriberEntry{sub: sub}

	m.logger.Info("workspace subscriber started", "workspace", wsID)
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

// Start starts all registered workspace subscribers and the idle deactivation loop.
func (m *MultiWorkspaceSubscriber) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ctx, m.cancel = context.WithCancel(ctx)

	for wsID, entry := range m.subscribers {
		entry.sub.Start()
		m.logger.Info("workspace subscriber started", "workspace", wsID)
	}

	go m.idleDeactivationLoop()
}

// Stop gracefully stops all workspace subscribers.
func (m *MultiWorkspaceSubscriber) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}

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
// workspace subscribers. This is used for SSE client reconnection catch-up.
func (m *MultiWorkspaceSubscriber) GetMutationsSince(since int64) []rpc.MutationEvent {
	m.mu.RLock()
	entries := make(map[string]*subscriberEntry, len(m.subscribers))
	for k, v := range m.subscribers {
		entries[k] = v
	}
	m.mu.RUnlock()

	var all []rpc.MutationEvent
	for _, entry := range entries {
		events := entry.sub.GetMutationsSince(since)
		all = append(all, events...)
	}
	return all
}

// idleDeactivationLoop runs every 15s and deactivates subscribers with no
// SSE clients for >60s.
func (m *MultiWorkspaceSubscriber) idleDeactivationLoop() {
	ticker := time.NewTicker(15 * time.Second)
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
				if now.Sub(entry.idleSince) >= idleDeactivationTimeout {
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

func (m *MultiWorkspaceSubscriber) GetMutationsSinceForWorkspace(wsID string, since int64) []rpc.MutationEvent {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return entry.sub.GetMutationsSince(since)
}
