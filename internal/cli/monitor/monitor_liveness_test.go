package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// A nil LastActivityAt must be omitted from the JSON entirely. A zero
// time.Time would serialize as "0001-01-01T00:00:00Z", which the kanban's
// AgentRow parses into a bogus "last seen 700000d ago" label — the field is
// a pointer specifically so "never reported" stays absent on the wire.
func TestAgentStatus_LastActivityAt_OmittedWhenNil(t *testing.T) {
	noActivity, err := json.Marshal(AgentStatus{Name: "worker", CurrentTaskID: "LOOM-11"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(noActivity), "last_activity_at") {
		t.Errorf("nil LastActivityAt leaked into JSON: %s", noActivity)
	}
	if !strings.Contains(string(noActivity), `"current_task_id":"LOOM-11"`) {
		t.Errorf("current_task_id missing from JSON: %s", noActivity)
	}

	at := time.Date(2026, 5, 21, 14, 0, 0, 0, time.UTC)
	withActivity, err := json.Marshal(AgentStatus{Name: "worker", LastActivityAt: &at})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withActivity), `"last_activity_at":"2026-05-21T14:00:00Z"`) {
		t.Errorf("non-nil LastActivityAt not serialized: %s", withActivity)
	}
}
