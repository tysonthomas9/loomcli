package cli

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Collect task data
	data := collectDataFunc()

	// Get ready tasks broken down by priority
	readyByPriority := collectReadyTasksByPriority(50)

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
	workerCounts := collectWorkerStatusCounts()
	_, _ = fmt.Fprintf(w, "\n# HELP loom_fleet_workers Number of fleet workers by status\n")
	_, _ = fmt.Fprintf(w, "# TYPE loom_fleet_workers gauge\n")
	for _, status := range []string{"active", "idle", "blocked"} {
		_, _ = fmt.Fprintf(w, "loom_fleet_workers{status=\"%s\"} %d\n", status, workerCounts[status])
	}
}

// collectWorkerStatusCounts connects to the daemon via RPC and aggregates
// worker counts by status. Returns zeros if daemon is unavailable.
func collectWorkerStatusCounts() map[string]int {
	counts := map[string]int{"active": 0, "idle": 0, "blocked": 0}

	beadsDir := GetBeadsDir()
	if beadsDir == "" {
		beadsDir = "."
	}

	// Resolve absolute path for socket discovery
	absPath, err := filepath.Abs(beadsDir)
	if err != nil {
		log.Printf("metrics: failed to resolve beads dir: %v", err)
		return counts
	}

	socketPath := rpc.ShortSocketPath(absPath)
	client, err := rpc.TryConnect(socketPath)
	if err != nil || client == nil {
		// Daemon not running - return zeros
		return counts
	}
	defer func() { _ = client.Close() }()

	resp, err := client.GetWorkerStatus(&rpc.GetWorkerStatusArgs{})
	if err != nil || resp == nil {
		log.Printf("metrics: failed to get worker status: %v", err)
		return counts
	}

	for _, worker := range resp.Workers {
		switch worker.Status {
		case "in_progress", "active":
			counts["active"]++
		case "idle", "":
			counts["idle"]++
		case "blocked":
			counts["blocked"]++
		default:
			log.Printf("metrics: unknown worker status %q for %s", worker.Status, worker.Assignee)
		}
	}

	return counts
}
