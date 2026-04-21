package metricscmd

import (
	"net/http"
	"path/filepath"
	"sync"
	"time"

	beadsbackend "github.com/tysonthomas9/loomcli/internal/backend/beads"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/usagecmd"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// scopedMonitorReadyLimit mirrors the non-scoped collector's readyLimit so
// the returned task counts reflect the full queue rather than bd's 50-row
// CLI default.
const scopedMonitorReadyLimit = 10000

// scopedCollectorTTL keeps the same freshness budget the legacy global
// collector used (collector.go NewCollectorWithBackground: 10s TTL). Each
// per-workspace entry coalesces concurrent requests via cachedCollector so
// the five scoped routes share one collection per poll cycle.
const scopedCollectorTTL = 10 * time.Second

// BuildScopedMonitorHandlers returns the factory passed to
// webui.ServerConfig.ScopedMonitorHandlersFn. It wires
// monitor.CollectMonitorDataScoped behind the five per-workspace routes
// (status/tasks/stats/sync/usage) so each request talks to the target
// workspace's bd pool and reads usage from that workspace's usage.jsonl.
//
// nameFn resolves the URL wsID back to the workspace name used inside
// MonitorData — mirrors the argument to HandleAgentsScoped so the response
// payload carries a consistent {workspace: {mode, name}} envelope.
func BuildScopedMonitorHandlers(nameFn func(wsID string) string) func(
	pathFn func(wsID string) string,
	poolFn func(wsID string) beadsbackend.Pool,
) map[string]http.HandlerFunc {
	return func(
		pathFn func(wsID string) string,
		poolFn func(wsID string) beadsbackend.Pool,
	) map[string]http.HandlerFunc {
		usageStores := newScopedUsageStores()
		collectors := newScopedCollectorCache(scopedCollectorTTL)
		collect := func(wsID string) *monitor.MonitorData {
			return collectors.get(wsID, pathFn, poolFn, nameFn)
		}
		return map[string]http.HandlerFunc{
			"GET /api/workspaces/{ws}/monitor/status": scopedHandler(collect, nameFn, func(data *monitor.MonitorData, wsName string) any {
				return StatusResponse{
					Workspace:      WorkspaceInfo{Mode: "workspace", Name: wsName},
					Agents:         data.Agents,
					Tasks:          data.Tasks,
					InProgressList: data.InProgressTasks,
					AgentTasks:     data.AgentTasks,
					Stats:          data.Stats,
					Sync:           data.SyncStatus,
					Timestamp:      data.Timestamp,
				}
			}),
			"GET /api/workspaces/{ws}/monitor/tasks": scopedHandler(collect, nameFn, func(data *monitor.MonitorData, _ string) any {
				return TasksResponse{
					Summary:          data.Tasks,
					NeedsPlanning:    data.NeedsPlanningTasks,
					ReadyToImplement: data.ReadyToImplement,
					NeedsReview:      data.ReviewTasks,
					InProgress:       data.InProgressTasks,
					Backlog:          data.BacklogTasks,
					Closed:           data.ClosedTasks,
					Timestamp:        data.Timestamp,
				}
			}),
			"GET /api/workspaces/{ws}/monitor/stats": scopedHandler(collect, nameFn, func(data *monitor.MonitorData, _ string) any {
				return StatsResponse{Stats: data.Stats, Timestamp: data.Timestamp}
			}),
			"GET /api/workspaces/{ws}/monitor/sync": scopedHandler(collect, nameFn, func(data *monitor.MonitorData, _ string) any {
				return SyncResponse{Sync: data.SyncStatus, Timestamp: data.Timestamp}
			}),
			"GET /api/workspaces/{ws}/monitor/usage": usageStores.handle(pathFn),
		}
	}
}

// scopedHandler is the common request path for monitor collection routes.
// Workspace-specific data comes from collect(wsID), which hits the
// per-workspace cache. Encode slices out the per-route JSON payload.
func scopedHandler(
	collect func(wsID string) *monitor.MonitorData,
	nameFn func(wsID string) string,
	encode func(*monitor.MonitorData, string) any,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := workspaceIDFromPath(r)
		writeJSON(w, encode(collect(wsID), nameFn(wsID)))
	}
}

// scopedCollectorCache holds one cachedCollector per workspace. Polling the
// five scoped /monitor/* routes shares the same collection per wsID within
// the TTL, mirroring the legacy global collector's amortization. Without
// this, the 5s frontend poll fired a fresh bd + git fan-out per (route,
// workspace) — ~8× the pre-scoping load for a two-workspace user.
//
// Pool resolution happens inside the collectFn closure so a workspace
// pool-swap (e.g. daemon restart) is picked up on the next collection
// instead of being pinned at cache-entry creation.
type scopedCollectorCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*cachedCollector
}

func newScopedCollectorCache(ttl time.Duration) *scopedCollectorCache {
	return &scopedCollectorCache{ttl: ttl, entries: make(map[string]*cachedCollector)}
}

func (c *scopedCollectorCache) get(
	wsID string,
	pathFn func(string) string,
	poolFn func(string) beadsbackend.Pool,
	nameFn func(string) string,
) *monitor.MonitorData {
	c.mu.Lock()
	cc, ok := c.entries[wsID]
	if !ok {
		cc = &cachedCollector{
			ttl: c.ttl,
			collectFn: func() *monitor.MonitorData {
				return monitor.CollectMonitorDataScoped(
					pathFn(wsID), nameFn(wsID), poolFn(wsID),
					scopedMonitorReadyLimit, "",
				)
			},
		}
		c.entries[wsID] = cc
	}
	c.mu.Unlock()
	return cc.get()
}

// scopedUsageStores lazily constructs and caches one usage.Store per
// workspace path. Usage files live at {wsPath}/.loom/usage.jsonl (the same
// layout usage.NewCollector writes into via initUsageStore in serve.go).
// Caching avoids re-running os.MkdirAll on every poll. The map is bounded
// by the user's workspace count, so no eviction is wired in.
type scopedUsageStores struct {
	mu     sync.Mutex
	stores map[string]*usage.Store
}

func newScopedUsageStores() *scopedUsageStores {
	return &scopedUsageStores{stores: make(map[string]*usage.Store)}
}

// storeFor returns a cached store, creating it on first use. Returns nil if
// wsPath is empty (caller treats that as a 404).
func (s *scopedUsageStores) storeFor(wsPath string) *usage.Store {
	if wsPath == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[wsPath]; ok {
		return store
	}
	// Workspace usage lives under <wsPath>/.loom/usage.jsonl so it doesn't
	// collide with any non-loom files at the workspace root.
	loomDir := filepath.Join(wsPath, ".loom")
	store := usagecmd.InitStore(loomDir)
	s.stores[wsPath] = store
	return store
}

// handle returns the HTTP handler for GET /api/workspaces/{ws}/monitor/usage.
// Delegates to the same HandleUsage that powered the legacy global endpoint,
// but bound to a workspace-specific store rather than the launch dir.
func (s *scopedUsageStores) handle(pathFn func(wsID string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := workspaceIDFromPath(r)
		usagecmd.HandleUsage(s.storeFor(pathFn(wsID))).ServeHTTP(w, r)
	}
}
