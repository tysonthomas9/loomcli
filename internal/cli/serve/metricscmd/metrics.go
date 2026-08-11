package metricscmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

// HandleMetrics returns an http.HandlerFunc that renders Prometheus-style metrics.
// The collectDataFn is called to get task data on each request.
func HandleMetrics(collectDataFn CollectDataFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Collect task data
		data := collectDataFn(r.Context())

		// Get ready tasks broken down by priority
		readyByPriority := monitor.CollectReadyTasksByPriority(r.Context(), 10000)

		// Get in-progress count
		inProgress := 0
		if data != nil {
			inProgress = data.Tasks.InProgress
		}

		// Write loom_ready_tasks metric
		_, _ = fmt.Fprintf(w, "# HELP loom_ready_tasks Number of tasks ready to be claimed\n")
		_, _ = fmt.Fprintf(w, "# TYPE loom_ready_tasks gauge\n")
		for p := 0; p <= 4; p++ {
			_, _ = fmt.Fprintf(w, "loom_ready_tasks{priority=\"%d\"} %d\n", p, readyByPriority[p])
		}

		// Write loom_in_progress_tasks metric
		_, _ = fmt.Fprintf(w, "\n# HELP loom_in_progress_tasks Number of tasks currently being worked on\n")
		_, _ = fmt.Fprintf(w, "# TYPE loom_in_progress_tasks gauge\n")
		_, _ = fmt.Fprintf(w, "loom_in_progress_tasks %d\n", inProgress)

		// Write loom_fleet_workers metric
		workerCounts := collectWorkerStatusCounts(data)
		_, _ = fmt.Fprintf(w, "\n# HELP loom_fleet_workers Number of fleet workers by status\n")
		_, _ = fmt.Fprintf(w, "# TYPE loom_fleet_workers gauge\n")
		for _, status := range []string{"active", "idle", "blocked"} {
			_, _ = fmt.Fprintf(w, "loom_fleet_workers{status=\"%s\"} %d\n", status, workerCounts[status])
		}
	}
}

// collectWorkerStatusCounts aggregates the owned monitor projection. It never
// probes a process-local socket; agent identity and liveness are durable
// Agents/Interaction data assembled by the monitor data source.
func collectWorkerStatusCounts(data *monitor.MonitorData) map[string]int {
	counts := map[string]int{"active": 0, "idle": 0, "blocked": 0}
	if data == nil {
		return counts
	}
	for _, worker := range data.Agents {
		if worker.RoleKind == "interactive" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(worker.Status))
		live := strings.ToLower(strings.TrimSpace(worker.LiveStatus))
		switch {
		case worker.LastErrorClass != "", status == "blocked", status == "backend_unavailable":
			counts["blocked"]++
		case live == "working", worker.ActiveTaskID != "", worker.CurrentTaskID != "",
			status == "in_progress", status == "active", status == "working", status == "planning":
			counts["active"]++
		default:
			counts["idle"]++
		}
	}
	return counts
}
