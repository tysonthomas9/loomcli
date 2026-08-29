package metricscmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// workerStatuses is the fixed label set for loom_fleet_workers. All three are
// emitted for every workspace so a series never disappears between scrapes.
var workerStatuses = []string{"active", "idle", "blocked"}

// HandleMetrics returns an http.HandlerFunc that renders the loom_* Prometheus
// gauges, one label set per workspace.
//
// Every gauge here used to read a constant zero: the writer had no request
// workspace, so it collected from an empty scope, and the worker counts came
// from a per-worktree daemon RPC socket that never resolves inside `loom serve`.
// Both paths are gone; the numbers now come from the same scoped collectors the
// monitor API endpoints use, per workspace listed in the store.
func HandleMetrics(ds *MonitorDataSource, storeDS *MonitorStoreDataSource, st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		ctx := r.Context()
		workspaces := listMetricsWorkspaces(ctx, st)

		writeMetricsHeaders(w)

		if len(workspaces) == 0 {
			// A scrape endpoint that errors is worse than one reporting "not
			// ok": emit the sentinel and nothing else.
			_, _ = fmt.Fprintf(w, "loom_monitor_collection_ok{workspace=\"\"} 0\n")
			return
		}

		for _, ws := range workspaces {
			writeWorkspaceMetrics(ctx, w, ds, storeDS, ws)
		}
	}
}

// listMetricsWorkspaces returns the workspace keys to scrape, sorted so the
// exposition is stable between scrapes. An empty result means "report not ok".
func listMetricsWorkspaces(ctx context.Context, st store.Store) []string {
	if st == nil || st.Workspaces() == nil {
		return nil
	}
	workspaces, err := st.Workspaces().List(ctx)
	if err != nil {
		slog.Warn("metrics: list workspaces failed", "err", err)
		return nil
	}
	keys := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil || ws.Key == "" {
			continue
		}
		keys = append(keys, ws.Key)
	}
	sort.Strings(keys)
	return keys
}

// writeMetricsHeaders emits every HELP/TYPE block exactly once, before any
// sample. Prometheus rejects a repeated HELP or TYPE for the same family, and
// the workspace loop below would otherwise repeat them per workspace. They are
// written even when no workspace produces a sample: a family with a header and
// no samples parses fine, a sample without a header does not.
func writeMetricsHeaders(w http.ResponseWriter) {
	_, _ = fmt.Fprintf(w, "# HELP loom_ready_tasks Number of tasks ready to be claimed\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_ready_tasks gauge\n")
	_, _ = fmt.Fprintf(w, "# HELP loom_in_progress_tasks Number of tasks currently being worked on\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_in_progress_tasks gauge\n")
	_, _ = fmt.Fprintf(w, "# HELP loom_fleet_workers Number of fleet workers by status\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_fleet_workers gauge\n")
	_, _ = fmt.Fprintf(w, "# HELP loom_monitor_collection_ok Whether the last monitor collection for this workspace succeeded\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_monitor_collection_ok gauge\n")
	_, _ = fmt.Fprintf(w, "# HELP loom_monitor_collection_timestamp_seconds Unix timestamp of the monitor collection backing these samples\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_monitor_collection_timestamp_seconds gauge\n")
}

// writeWorkspaceMetrics emits one workspace's samples. When collection failed
// it emits collection_ok 0 and OMITS the task and worker samples rather than
// writing zeros — a zero is indistinguishable from a real empty queue, and one
// broken workspace must not blank out the others.
func writeWorkspaceMetrics(ctx context.Context, w http.ResponseWriter, ds *MonitorDataSource, storeDS *MonitorStoreDataSource, ws string) {
	label := escapeLabel(ws)

	data := ds.ResolveWorkspace(ws)
	if data == nil {
		_, _ = fmt.Fprintf(w, "loom_monitor_collection_ok{workspace=\"%s\"} 0\n", label)
		return
	}

	for p := 0; p <= 4; p++ {
		_, _ = fmt.Fprintf(w, "loom_ready_tasks{workspace=\"%s\",priority=\"%d\"} %d\n", label, p, data.Tasks.ReadyByPriority[p])
	}
	_, _ = fmt.Fprintf(w, "loom_in_progress_tasks{workspace=\"%s\"} %d\n", label, data.Tasks.InProgress)

	workerCounts := workerStatusCounts(storeDS.Resolve(ctx, ws))
	for _, status := range workerStatuses {
		_, _ = fmt.Fprintf(w, "loom_fleet_workers{workspace=\"%s\",status=\"%s\"} %d\n", label, status, workerCounts[status])
	}

	_, _ = fmt.Fprintf(w, "loom_monitor_collection_ok{workspace=\"%s\"} 1\n", label)
	_, _ = fmt.Fprintf(w, "loom_monitor_collection_timestamp_seconds{workspace=\"%s\"} %d\n", label, data.Timestamp.Unix())
}

// workerStatusCounts buckets a workspace's store agents into the three
// loom_fleet_workers statuses. Liveness wins over the stored state: an agent
// with a live session is working whatever its record says.
func workerStatusCounts(data monitorStoreData) map[string]int {
	counts := map[string]int{"active": 0, "idle": 0, "blocked": 0}
	for _, agent := range data.Agents {
		if domain.AgentLiveStatus(agent.LiveStatus) == domain.AgentLiveWorking {
			counts["active"]++
			continue
		}
		switch data.AgentStates[agent.Name] {
		case domain.AgentStateIdle, domain.AgentStateActive:
			counts["idle"]++
		case domain.AgentStateStopped, domain.AgentStateBackendUnavailable:
			counts["blocked"]++
		}
	}
	return counts
}

// escapeLabel escapes a value for the Prometheus text exposition format, which
// requires backslashes and double quotes inside a label value to be escaped.
// Workspace keys reach the output verbatim, so an unescaped quote would produce
// a body no parser accepts.
func escapeLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}
