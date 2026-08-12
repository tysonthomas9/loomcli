package serve

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const monitorCollectionLimit = 10000

// buildCollectDataFn returns the on-demand monitor collector used by serve.
func buildCollectDataFn(
	workspaceHint string,
	workItemsFn metricscmd.WorkItemsFn,
	cacheTTL time.Duration,
) metricscmd.CollectDataFn {
	collectFn := func(ctx context.Context) *monitor.MonitorData {
		if workspaceHint != "" && workItemsFn != nil {
			ctx = middleware.WithWorkspace(ctx, workspaceHint)
			if items := workItemsFn(ctx); items != nil {
				return monitor.CollectMonitorDataWithWorkItems(ctx, items, monitorCollectionLimit, "")
			}
		}
		return monitor.CollectMonitorData(ctx, monitorCollectionLimit, "")
	}
	return metricscmd.NewCollectorFunc(cacheTTL, collectFn)
}

// buildUsageHandler opens the local usage store and returns its HTTP handler.
func buildUsageHandler(runtimeDir string) http.HandlerFunc {
	if runtimeDir == "" {
		runtimeDir = "."
	}
	return metricscmd.HandleUsage(metricscmd.InitUsageStore(runtimeDir))
}

// composeMonitorHandlers composes the monitoring handlers from their exact data ports.
func composeMonitorHandlers(
	collectDataFn metricscmd.CollectDataFn,
	staleDetectorHandler http.HandlerFunc,
	workItemsFn metricscmd.WorkItemsFn,
	defaultWorkspace string,
	usageHandler http.HandlerFunc,
	monitorStoreDataSource *metricscmd.MonitorStoreDataSource,
	workspacesHandler http.HandlerFunc,
	driverRuns store.DriverRunStore,
) webui.MonitorHandlers {
	eventsDir := metricscmd.ResolveEventsDir()
	monitorDataSource := metricscmd.NewMonitorDataSourceWithDefaultWorkspace(collectDataFn, workItemsFn, defaultWorkspace)
	driverRunMetrics := durableDriverRunMetricsReader(driverRuns)
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
		ObservabilityMetrics: metricscmd.HandleWorkspaceMetrics(eventsDir, metricscmd.NewWorkspaceMetricsCacheWithDriverRuns(eventsDir, defaultWorkspace, driverRunMetrics)),
		ObservabilityEvents:  metricscmd.HandleEvents(eventsDir),
	}
}

func durableDriverRunMetricsReader(runs store.DriverRunStore) metricscmd.DriverRunMetricsReader {
	if runs == nil {
		return nil
	}
	return func(ctx context.Context, workspace string, limit int) ([]metricscmd.DriverRunMetric, error) {
		values, err := runs.List(ctx, workspace, store.DriverRunFilter{Limit: limit})
		if err != nil {
			return nil, err
		}
		metrics := make([]metricscmd.DriverRunMetric, 0, len(values))
		for _, run := range values {
			if run == nil || run.FinishedAt == nil {
				continue
			}
			metrics = append(metrics, metricscmd.DriverRunMetric{
				RunID: run.RunID, TaskID: durableRunTaskID(run), Agent: run.AgentServiceID,
				Role: run.DriverID, EpicID: run.EpicID, Status: string(run.Status),
				StartedAt: run.StartedAt, FinishedAt: *run.FinishedAt,
				FilesChanged: outputInt(run.Output, "files_changed"),
				LinesAdded:   outputInt(run.Output, "lines_added"), LinesRemoved: outputInt(run.Output, "lines_removed"),
			})
		}
		return metrics, nil
	}
}

func durableRunTaskID(run *domain.DriverRun) string {
	if run.SourceKind == "issue" || run.SourceKind == "task" {
		return run.SourceRef
	}
	return run.RunID
}

func outputInt(output map[string]string, key string) int {
	value, _ := strconv.Atoi(output[key])
	return value
}
