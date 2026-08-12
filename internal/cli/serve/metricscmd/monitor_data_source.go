package metricscmd

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const (
	defaultWorkspaceMonitorCacheTTL = 10 * time.Second
	maxWorkspaceMonitorCollectors   = 128
)

// MonitorDataSource resolves monitor data for a request. Workspace-scoped
// requests use a per-workspace cached collector so adjacent monitor endpoints
// share one expensive issue-backend collection.
type MonitorDataSource struct {
	collectDataFn    CollectDataFn
	backendFn        IssueBackendFn
	defaultWorkspace string
	workspace        *workspaceMonitorDataCache
}

// NewMonitorDataSource returns a request-aware monitor data source.
func NewMonitorDataSource(collectDataFn CollectDataFn, backendFn IssueBackendFn) *MonitorDataSource {
	return NewMonitorDataSourceWithTTL(collectDataFn, backendFn, defaultWorkspaceMonitorCacheTTL)
}

// NewMonitorDataSourceWithDefaultWorkspace returns a data source that reuses
// the pre-warmed collector for requests targeting the same default workspace.
func NewMonitorDataSourceWithDefaultWorkspace(collectDataFn CollectDataFn, backendFn IssueBackendFn, defaultWorkspace string) *MonitorDataSource {
	ds := NewMonitorDataSourceWithTTL(collectDataFn, backendFn, defaultWorkspaceMonitorCacheTTL)
	ds.defaultWorkspace = defaultWorkspace
	return ds
}

// NewMonitorDataSourceWithTTL returns a request-aware monitor data source with
// a configurable workspace cache TTL for tests and focused callers.
func NewMonitorDataSourceWithTTL(collectDataFn CollectDataFn, backendFn IssueBackendFn, ttl time.Duration) *MonitorDataSource {
	if ttl <= 0 {
		ttl = defaultWorkspaceMonitorCacheTTL
	}
	return &MonitorDataSource{
		collectDataFn: collectDataFn,
		backendFn:     backendFn,
		workspace:     newWorkspaceMonitorDataCache(collectDataFn, backendFn, ttl),
	}
}

// Resolve returns monitor data for r, honoring any workspace query parameter.
func (s *MonitorDataSource) Resolve(r *http.Request) *monitor.MonitorData {
	if s == nil {
		return nil
	}
	workspaceHint := r.URL.Query().Get("workspace")
	if workspaceHint == "" || s.backendFn == nil {
		return s.collectDataFn(r.Context())
	}
	if s.defaultWorkspace != "" && workspaceHint == s.defaultWorkspace {
		return s.collectDataFn(r.Context())
	}
	return s.workspace.get(r.Context(), workspaceHint)
}

type workspaceMonitorDataCache struct {
	ttl           time.Duration
	collectDataFn CollectDataFn
	backendFn     IssueBackendFn

	mu         sync.Mutex
	collectors map[string]*cachedCollector
}

func newWorkspaceMonitorDataCache(collectDataFn CollectDataFn, backendFn IssueBackendFn, ttl time.Duration) *workspaceMonitorDataCache {
	return &workspaceMonitorDataCache{
		ttl:           ttl,
		collectDataFn: collectDataFn,
		backendFn:     backendFn,
		collectors:    make(map[string]*cachedCollector),
	}
}

func (c *workspaceMonitorDataCache) get(ctx context.Context, workspaceHint string) *monitor.MonitorData {
	c.mu.Lock()
	collector := c.collectors[workspaceHint]
	if collector == nil && len(c.collectors) >= maxWorkspaceMonitorCollectors {
		c.mu.Unlock()
		return c.collectWorkspace(ctx, workspaceHint)
	}
	if collector == nil {
		workspace := workspaceHint
		collector = &cachedCollector{
			ttl:       c.ttl,
			collectFn: func(ctx context.Context) *monitor.MonitorData { return c.collectWorkspace(ctx, workspace) },
		}
		c.collectors[workspaceHint] = collector
	}
	c.mu.Unlock()

	return collector.get(ctx)
}

func (c *workspaceMonitorDataCache) collectWorkspace(parent context.Context, workspaceHint string) *monitor.MonitorData {
	ctx := middleware.WithWorkspace(parent, workspaceHint)
	if be := c.backendFn(ctx); be != nil {
		return monitor.CollectMonitorDataWithIssueBackend(ctx, be, 10000, "")
	}
	return c.collectDataFn(ctx)
}
