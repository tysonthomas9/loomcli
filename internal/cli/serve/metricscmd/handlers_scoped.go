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
		return map[string]http.HandlerFunc{
			"GET /api/workspaces/{ws}/monitor/status": scopedHandler(pathFn, poolFn, nameFn, func(data *monitor.MonitorData, wsName string) any {
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
			"GET /api/workspaces/{ws}/monitor/tasks": scopedHandler(pathFn, poolFn, nameFn, func(data *monitor.MonitorData, _ string) any {
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
			"GET /api/workspaces/{ws}/monitor/stats": scopedHandler(pathFn, poolFn, nameFn, func(data *monitor.MonitorData, _ string) any {
				return StatsResponse{Stats: data.Stats, Timestamp: data.Timestamp}
			}),
			"GET /api/workspaces/{ws}/monitor/sync": scopedHandler(pathFn, poolFn, nameFn, func(data *monitor.MonitorData, _ string) any {
				return SyncResponse{Sync: data.SyncStatus, Timestamp: data.Timestamp}
			}),
			"GET /api/workspaces/{ws}/monitor/usage": usageStores.handle(pathFn),
		}
	}
}

// scopedHandler is the common request path for monitor collection routes.
// It resolves wsPath + pool, short-circuits to a zero-value MonitorData when
// either is missing (rather than falling back to the launch workspace), and
// hands the result to a per-route encoder. Each request triggers a fresh
// CollectMonitorDataScoped — the legacy global collector's cross-workspace
// cache is by design not reused here.
func scopedHandler(
	pathFn func(wsID string) string,
	poolFn func(wsID string) beadsbackend.Pool,
	nameFn func(wsID string) string,
	encode func(*monitor.MonitorData, string) any,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := r.PathValue("ws")
		wsPath := pathFn(wsID)
		wsName := nameFn(wsID)
		pool := poolFn(wsID)
		data := monitor.CollectMonitorDataScoped(wsPath, wsName, pool, scopedMonitorReadyLimit, "")
		if data == nil {
			// CollectMonitorDataScoped never returns nil, but keep the
			// defensive branch so a future refactor that changes that
			// contract doesn't silently serve empty JSON.
			data = &monitor.MonitorData{Timestamp: time.Now()}
		}
		writeJSON(w, encode(data, wsName))
	}
}

// scopedUsageStores lazily constructs and caches one usage.Store per
// workspace path. Usage files live at {wsPath}/.loom/usage.jsonl (the same
// layout usage.NewCollector writes into via initUsageStore in serve.go).
// Caching avoids re-running os.MkdirAll on every poll.
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
		wsID := r.PathValue("ws")
		wsPath := pathFn(wsID)
		store := s.storeFor(wsPath)
		usagecmd.HandleUsage(store).ServeHTTP(w, r)
	}
}
