package metricscmd

import (
	"bytes"
	"net/http"
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
// workspace's bd pool and reads usage from that workspace's
// {wsPath}/usage.jsonl (the path automode writes through
// usage.NewStore(cli.GetBeadsDir())).
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
		handlers := buildCollectorRoutes(collect, nameFn)
		handlers["GET /api/workspaces/{ws}/monitor/usage"] = usageStores.handle(pathFn)
		return handlers
	}
}

// buildCollectorRoutes wires the five routes backed by CollectMonitorDataScoped.
// /monitor/usage lives outside this map because it reads from usage.jsonl
// rather than the cached MonitorData.
func buildCollectorRoutes(
	collect func(wsID string) *monitor.MonitorData,
	nameFn func(wsID string) string,
) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/workspaces/{ws}/monitor/agents": scopedHandler(collect, nameFn, func(data *monitor.MonitorData, wsName string) any {
			return AgentsResponse{
				Workspace: WorkspaceInfo{Mode: "workspace", Name: wsName},
				Agents:    data.Agents,
				Timestamp: data.Timestamp,
			}
		}),
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
// workspace path. Usage files live at {wsPath}/usage.jsonl — the same
// layout automode writes through usage.NewStore(cli.GetBeadsDir()), so a
// nested .loom/ directory here would silently read an empty file.
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
	store := usagecmd.InitStore(wsPath)
	s.stores[wsPath] = store
	return store
}

// handle returns the HTTP handler for GET /api/workspaces/{ws}/monitor/usage.
// Delegates to HandleUsage but memoizes responses per (wsID, query) for
// scopedCollectorTTL so 5s polling doesn't re-scan usage.jsonl on every
// tick — parity with scopedCollectorCache for the other four scoped routes.
// Only 200-OK bodies are cached so error responses surface fresh.
func (s *scopedUsageStores) handle(pathFn func(wsID string) string) http.HandlerFunc {
	cache := newScopedUsageResponseCache(scopedCollectorTTL)
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := workspaceIDFromPath(r)
		key := wsID + "?" + r.URL.RawQuery

		if body, ok := cache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}

		rec := &responseCapture{header: http.Header{}, status: http.StatusOK}
		usagecmd.HandleUsage(s.storeFor(pathFn(wsID))).ServeHTTP(rec, r)

		body := rec.body.Bytes()
		if rec.status == http.StatusOK {
			cache.put(key, body)
		}

		for k, vs := range rec.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if rec.status != http.StatusOK {
			w.WriteHeader(rec.status)
		}
		_, _ = w.Write(body)
	}
}

// scopedUsageResponseCache memoizes usage endpoint responses per
// (wsID, query) key for a bounded TTL. Keyed on raw query string so
// different filter queries each get their own cache slot.
type scopedUsageResponseCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*cachedUsageBody
}

type cachedUsageBody struct {
	expiresAt time.Time
	body      []byte
}

func newScopedUsageResponseCache(ttl time.Duration) *scopedUsageResponseCache {
	return &scopedUsageResponseCache{ttl: ttl, entries: make(map[string]*cachedUsageBody)}
}

func (c *scopedUsageResponseCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.body, true
}

func (c *scopedUsageResponseCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cachedUsageBody{
		expiresAt: time.Now().Add(c.ttl),
		body:      body,
	}
}

// responseCapture buffers an HTTP response so the outer handler can
// inspect the status before deciding whether to cache it.
type responseCapture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *responseCapture) Header() http.Header         { return r.header }
func (r *responseCapture) WriteHeader(code int)        { r.status = code }
func (r *responseCapture) Write(b []byte) (int, error) { return r.body.Write(b) }
