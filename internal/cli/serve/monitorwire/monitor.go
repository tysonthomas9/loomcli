// Package monitorwire composes serve's monitoring and usage HTTP surfaces so
// the process root depends on one adapter rather than each implementation.
package monitorwire

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/observability"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/usagecmd"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const monitorCollectionLimit = 10000

// BuildCollectDataFn returns the on-demand monitor collector used by serve.
func BuildCollectDataFn(
	workspaceHint string,
	issueBackendFn metricscmd.IssueBackendFn,
	cacheTTL time.Duration,
) metricscmd.CollectDataFn {
	collectFn := func() *monitor.MonitorData {
		if workspaceHint != "" && issueBackendFn != nil {
			ctx := middleware.WithWorkspace(context.Background(), workspaceHint)
			if be := issueBackendFn(ctx); be != nil {
				return monitor.CollectMonitorDataWithIssueBackend(be, monitorCollectionLimit, "")
			}
		}
		return monitor.CollectMonitorData(monitorCollectionLimit, "")
	}
	return metricscmd.NewCollectorFunc(cacheTTL, collectFn)
}

// BuildUsageHandler opens the local usage store and returns its HTTP handler.
func BuildUsageHandler(runtimeDir string) http.HandlerFunc {
	if runtimeDir == "" {
		runtimeDir = "."
	}
	return usagecmd.HandleUsage(usagecmd.InitStore(runtimeDir))
}

// BuildHandlers composes the monitoring handlers from their exact data ports.
func BuildHandlers(
	collectDataFn metricscmd.CollectDataFn,
	staleDetectorHandler http.HandlerFunc,
	issueBackendFn metricscmd.IssueBackendFn,
	defaultWorkspace string,
	usageHandler http.HandlerFunc,
	monitorStoreDataSource *metricscmd.MonitorStoreDataSource,
	workspacesHandler http.HandlerFunc,
) webui.MonitorHandlers {
	eventsDir := observability.ResolveEventsDir()
	monitorDataSource := metricscmd.NewMonitorDataSourceWithDefaultWorkspace(collectDataFn, issueBackendFn, defaultWorkspace)
	return webui.MonitorHandlers{
		Status:               metricscmd.HandleStatusWithSources(monitorDataSource, monitorStoreDataSource),
		Agents:               metricscmd.HandleAgentsWithSources(monitorDataSource, monitorStoreDataSource),
		Tasks:                metricscmd.HandleTasksWithDataSource(monitorDataSource),
		Stats:                metricscmd.HandleStatsWithDataSource(monitorDataSource),
		Sync:                 metricscmd.HandleSync(collectDataFn),
		Workspaces:           workspacesHandler,
		StaleDetector:        staleDetectorHandler,
		Usage:                usageHandler,
		Metrics:              metricscmd.HandleMetrics(collectDataFn),
		ObservabilityMetrics: observability.HandleMetrics(eventsDir, observability.NewMetricsCache(eventsDir)),
		ObservabilityEvents:  observability.HandleEvents(eventsDir),
	}
}
