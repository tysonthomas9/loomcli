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
	sub        *DaemonSubscriber
	generation int64     // incremented on each activation, used for TOCTOU guard
	idleSince  time.Time // when client count first dropped to 0; zero if clients connected
}

// MultiWorkspaceSubscriber manages per-workspace DaemonSubscribers, each polling
// its own daemon and broadcasting workspace-tagged mutations to a shared SSEHub.
type MultiWorkspaceSubscriber struct {
	hub         *SSEHub
	multiPool   *daemon.MultiPool
	logger      *slog.Logger
	subscribers map[string]*subscriberEntry // workspace ID → entry
	generation  int64                       // global generation counter
	mu          sync.Mutex
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

	m.generation++
	sub := NewDaemonSubscriber(pool, m.hub)
	sub.workspaceID = wsID
	sub.Start()
	m.subscribers[wsID] = &subscriberEntry{
		sub:        sub,
		generation: m.generation,
	}

	m.logger.Info("workspace subscriber started", "workspace", wsID)
	return nil
}

// HasSubscriber returns true if a subscriber is registered for the workspace.
func (m *MultiWorkspaceSubscriber) HasSubscriber(wsID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	entries := make(map[string]*subscriberEntry, len(m.subscribers))
	for k, v := range m.subscribers {
		entries[k] = v
	}
	m.mu.Unlock()

	var all []rpc.MutationEvent
	for _, entry := range entries {
		events := entry.sub.GetMutationsSince(since)
		all = append(all, events...)
	}
	return all
}

// GetMutationsSinceForWorkspace retrieves mutations since the given timestamp
// from a specific workspace's subscriber only. Returns nil if the workspace
// has no active subscriber.
// idleDeactivationLoop runs every 15s and deactivates subscribers with no
// SSE clients for >60s. Uses generation counter as TOCTOU guard.
func (m *MultiWorkspaceSubscriber) idleDeactivationLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for wsID, entry := range m.subscribers {
				clients := m.hub.ClientCountForWorkspace(wsID)
				if clients > 0 {
					entry.idleSince = time.Time{} // reset
					continue
				}
				if entry.idleSince.IsZero() {
					entry.idleSince = now
					continue
				}
				if now.Sub(entry.idleSince) >= idleDeactivationTimeout {
					gen := entry.generation
					m.logger.Info("deactivating idle subscriber",
						"workspace", wsID, "idle_for", now.Sub(entry.idleSince).Round(time.Second))
					entry.sub.Stop()
					delete(m.subscribers, wsID)
					_ = gen // generation recorded for debug; re-activation via middleware creates new gen
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *MultiWorkspaceSubscriber) GetMutationsSinceForWorkspace(wsID string, since int64) []rpc.MutationEvent {
	m.mu.Lock()
	entry, ok := m.subscribers[wsID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return entry.sub.GetMutationsSince(since)
}
