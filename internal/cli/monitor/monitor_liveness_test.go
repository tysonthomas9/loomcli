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
	if worker.Status != "running" {
		t.Errorf("worker.Status = %q, want running", worker.Status)
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

func TestCollectAgentStatus_ActiveLockTaskIDPopulatesCurrentTask(t *testing.T) {
	// not parallel: uses os.Chdir and defaultResolver global
	deps, _, _, _, _ := NewTestDeps(t)
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "alpha")

	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "alpha",
		TaskID:    "T-active",
		State:     "active",
		StartedAt: time.Now(),
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".agent.lock"), lockData, 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "alpha"}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}}
	deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

	agents, taskIDToAgents := collectAgentStatusDeps(deps, nil, "")
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if got := agents[0].CurrentTaskID; got != "T-active" {
		t.Fatalf("CurrentTaskID = %q, want active lock task ID", got)
	}
	gotAgents := taskIDToAgents["T-active"]
	if len(gotAgents) != 1 || gotAgents[0] != "alpha" {
		t.Fatalf("taskIDToAgents[T-active] = %#v, want [alpha]", gotAgents)
	}
}

func TestCollectAgentStatus_IgnoresIdleLockTaskIDForCurrentTask(t *testing.T) {
	// not parallel: uses os.Chdir and defaultResolver global
	deps, _, _, _, _ := NewTestDeps(t)
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "alpha")

	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "alpha",
		TaskID:    "T-stale",
		State:     "idle",
		StartedAt: time.Now(),
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".agent.lock"), lockData, 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "alpha"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}}
	deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

	agents, taskIDToAgents := collectAgentStatusDeps(deps, nil, "")
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if got := agents[0].CurrentTaskID; got != "" {
		t.Fatalf("CurrentTaskID = %q, want empty for idle lock with stale task ID", got)
	}
	if _, ok := taskIDToAgents["T-stale"]; ok {
		t.Fatalf("idle lock task ID was counted as an active claim: %#v", taskIDToAgents["T-stale"])
	}
	if !strings.HasPrefix(agents[0].Status, "idle ") {
		t.Fatalf("Status = %q, want idle status", agents[0].Status)
	}
}
