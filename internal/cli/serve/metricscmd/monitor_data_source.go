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
// share one expensive Work Items collection.
type MonitorDataSource struct {
	collectDataFn    CollectDataFn
	workItemsFn      WorkItemsFn
	defaultWorkspace string
	workspace        *workspaceMonitorDataCache
}

// NewMonitorDataSource returns a request-aware monitor data source.
func NewMonitorDataSource(collectDataFn CollectDataFn, workItemsFn WorkItemsFn) *MonitorDataSource {
	return NewMonitorDataSourceWithTTL(collectDataFn, workItemsFn, defaultWorkspaceMonitorCacheTTL)
}

// NewMonitorDataSourceWithDefaultWorkspace returns a data source that reuses
// the pre-warmed collector for requests targeting the same default workspace.
func NewMonitorDataSourceWithDefaultWorkspace(collectDataFn CollectDataFn, workItemsFn WorkItemsFn, defaultWorkspace string) *MonitorDataSource {
	ds := NewMonitorDataSourceWithTTL(collectDataFn, workItemsFn, defaultWorkspaceMonitorCacheTTL)
	ds.defaultWorkspace = defaultWorkspace
	return ds
}

// NewMonitorDataSourceWithTTL returns a request-aware monitor data source with
// a configurable workspace cache TTL for tests and focused callers.
func NewMonitorDataSourceWithTTL(collectDataFn CollectDataFn, workItemsFn WorkItemsFn, ttl time.Duration) *MonitorDataSource {
	if ttl <= 0 {
		ttl = defaultWorkspaceMonitorCacheTTL
	}
	return &MonitorDataSource{
		collectDataFn: collectDataFn,
		workItemsFn:   workItemsFn,
		workspace:     newWorkspaceMonitorDataCache(collectDataFn, workItemsFn, ttl),
	}
}

// Resolve returns monitor data for r, honoring any workspace query parameter.
func (s *MonitorDataSource) Resolve(r *http.Request) *monitor.MonitorData {
	if s == nil {
		return nil
	}
	workspaceHint := r.URL.Query().Get("workspace")
	if workspaceHint == "" || s.workItemsFn == nil {
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
	workItemsFn   WorkItemsFn

	mu         sync.Mutex
	collectors map[string]*cachedCollector
}

func newWorkspaceMonitorDataCache(collectDataFn CollectDataFn, workItemsFn WorkItemsFn, ttl time.Duration) *workspaceMonitorDataCache {
	return &workspaceMonitorDataCache{
		ttl:           ttl,
		collectDataFn: collectDataFn,
		workItemsFn:   workItemsFn,
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
	if items := c.workItemsFn(ctx); items != nil {
		return monitor.CollectMonitorDataWithWorkItems(ctx, items, 10000, "")
	}
	return c.collectDataFn(ctx)
}
