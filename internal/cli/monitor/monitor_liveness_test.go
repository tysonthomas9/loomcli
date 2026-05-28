package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDaemonManagedAgents_SurfacesTaskIDAndLastActivity(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "daemon-agents.json")

	// PID of the test process is guaranteed alive while the test runs;
	// LoadDaemonManagedAgents discards state owned by a dead PID.
	at := time.Date(2026, 5, 21, 14, 0, 0, 0, time.UTC)
	state := map[string]any{
		"pid": os.Getpid(),
		"agents": []map[string]any{
			{
				"worktree":      "worker",
				"status":        "running",
				"role":          "task",
				"task_id":       "LOOM-11",
				"last_activity": at.Format(time.RFC3339Nano),
			},
			{
				"worktree": "planner",
				"status":   "running",
				"role":     "plan",
				// no task_id, no last_activity — between tasks
			},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	got := LoadDaemonManagedAgents(stateFile)
	if got == nil {
		t.Fatal("LoadDaemonManagedAgents returned nil")
	}

	worker, ok := got["worker"]
	if !ok {
		t.Fatal("missing worker entry")
	}
	if worker.CurrentTaskID != "LOOM-11" {
		t.Errorf("worker.CurrentTaskID = %q, want %q", worker.CurrentTaskID, "LOOM-11")
	}
	if !worker.LastActivity.Equal(at) {
		t.Errorf("worker.LastActivity = %v, want %v", worker.LastActivity, at)
	}

	planner, ok := got["planner"]
	if !ok {
		t.Fatal("missing planner entry")
	}
	if planner.CurrentTaskID != "" {
		t.Errorf("planner.CurrentTaskID = %q, want empty", planner.CurrentTaskID)
	}
	if !planner.LastActivity.IsZero() {
		t.Errorf("planner.LastActivity = %v, want zero", planner.LastActivity)
	}
}
