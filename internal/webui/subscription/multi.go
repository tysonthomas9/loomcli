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
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const idleDeactivationTimeout = 60 * time.Second

// workspaceSubscriber abstracts a per-workspace mutation source so the
// MultiWorkspaceSubscriber can host both DaemonSubscriber (beads/bd daemon
// path) and BackendMutationSubscriber (fleet IssueBackend long-poll path)
// behind a single registry. The interface is intentionally minimal: the
// hub is shared across both implementations, so subscribers don't expose
// it; only the catch-up projection differs (hence GetMutationDataSince
// returning backend.MutationData, with the daemon adapter projecting
// rpc.MutationEvent into MutationData via realtime.RPCEventToMutationData).
//
// The method is named GetMutationDataSince (not GetMutationsSince) because
// DaemonSubscriber already has a public GetMutationsSince that returns
// []rpc.MutationEvent and is depended on by other tests; Go does not permit
// two methods with the same name on the same receiver. The adapter on
// DaemonSubscriber wraps the rpc-typed method and projects each event via
// realtime.RPCEventToMutationData.
type workspaceSubscriber interface {
	Start()
	Stop()
	GetMutationDataSince(since string) []backend.MutationData
}

// subscriberEntry tracks a subscriber and when it last had SSE clients.
type subscriberEntry struct {
	sub       workspaceSubscriber
	idleSince time.Time // when client count first dropped to 0; zero if clients connected
}

// MultiWorkspaceSubscriber manages per-workspace DaemonSubscribers, each polling
// its own daemon and broadcasting workspace-tagged mutations to a shared SSEHub.
type MultiWorkspaceSubscriber struct {
	hub         *realtime.Hub
	multiPool   *daemon.MultiPool
	logger      *slog.Logger
	subscribers map[string]*subscriberEntry // workspace ID → entry
	mu          sync.RWMutex
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
		subscribers: make(map[string]*subscriberEntry),
	}
}

// AddWorkspace creates and starts a DaemonSubscriber for the given workspace.
// The subscriber uses the pool from MultiPool and tags all MutationPayloads
// with the workspace ID. Idempotent: if a subscriber already exists for wsID,
// returns nil without replacing it (safe to call on every request from the
// workspace middleware). Returns an error if the workspace pool is not registered.
func (m *MultiWorkspaceSubscriber) AddWorkspace(wsID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.subscribers[wsID]; ok {
		return nil
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

// AddWorkspaceWithBackend creates and starts a BackendMutationSubscriber
// for the given workspace, sourcing mutations from the supplied
// IssueBackend (typically a *fleet.FleetBackend in fleet mode). Mirrors
// AddWorkspace's contract: idempotent under wsID, takes the same write
// lock to close the TOCTOU window between HasSubscriber and insertion.
// Returns an error if b is nil.
func (m *MultiWorkspaceSubscriber) AddWorkspaceWithBackend(wsID string, b backend.IssueBackend) error {
	if b == nil {
		return fmt.Errorf("AddWorkspaceWithBackend: backend must not be nil for workspace %q", wsID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.subscribers[wsID]; ok {
		return nil
	}

	sub := NewBackendMutationSubscriber(b, m.hub, wsID)
	sub.Start()
	m.subscribers[wsID] = &subscriberEntry{sub: sub}

	m.logger.Info("workspace backend subscriber started", "workspace", wsID)
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
// workspace subscribers. Used for SSE client reconnection catch-up.
//
// Beads subscribers take the direct shortcut (rpc.MutationEvent → return);
// fleet subscribers project from backend.MutationData via the workspaceSubscriber
// interface. The SSE handler's signature is []rpc.MutationEvent regardless.
func (m *MultiWorkspaceSubscriber) GetMutationsSince(since string) []rpc.MutationEvent {
	m.mu.RLock()
	entries := make(map[string]*subscriberEntry, len(m.subscribers))
	for k, v := range m.subscribers {
		entries[k] = v
	}
	m.mu.RUnlock()

	var all []rpc.MutationEvent
	for _, entry := range entries {
		if ds, ok := entry.sub.(*DaemonSubscriber); ok {
			all = append(all, ds.GetMutationsSince(parseCursorMillis(since))...)
			continue
		}
		muts := entry.sub.GetMutationDataSince(since)
		for _, m := range muts {
			all = append(all, realtime.BackendMutationToRPCEvent(m))
		}
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

// GetMutationsSinceForWorkspace retrieves mutations since the given timestamp
// from a specific workspace's subscriber only. Returns nil if the workspace
// has no active subscriber.
//
// Beads path returns rpc.MutationEvent directly; fleet path projects from
// backend.MutationData. Avoids a wasteful rpc → backend → rpc round-trip
// on every beads-side reconnect.
func (m *MultiWorkspaceSubscriber) GetMutationsSinceForWorkspace(wsID string, since string) []rpc.MutationEvent {
	m.mu.RLock()
	entry, ok := m.subscribers[wsID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	if ds, ok := entry.sub.(*DaemonSubscriber); ok {
		return ds.GetMutationsSince(parseCursorMillis(since))
	}
	muts := entry.sub.GetMutationDataSince(since)
	if len(muts) == 0 {
		return nil
	}
	out := make([]rpc.MutationEvent, len(muts))
	for i, m := range muts {
		out[i] = realtime.BackendMutationToRPCEvent(m)
	}
	return out
}

func parseCursorMillis(cursor string) int64 {
	if cursor == "" {
		return 0
	}
	n, _ := strconv.ParseInt(cursor, 10, 64)
	return n
}
