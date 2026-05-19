package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDaemonManagedAgentsUnavailableStates(t *testing.T) {
	dir := t.TempDir()
	if got := LoadDaemonManagedAgents(filepath.Join(dir, "missing.json")); got != nil {
		t.Fatalf("missing file = %#v, want nil", got)
	}

	invalidJSON := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalidJSON, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	if got := LoadDaemonManagedAgents(invalidJSON); got != nil {
		t.Fatalf("invalid json = %#v, want nil", got)
	}

	stalePID := filepath.Join(dir, "stale.json")
	data, err := json.Marshal(DaemonAgentState{PID: -1})
	if err != nil {
		t.Fatalf("marshal stale state: %v", err)
	}
	if err := os.WriteFile(stalePID, data, 0o644); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}
	if got := LoadDaemonManagedAgents(stalePID); got != nil {
		t.Fatalf("stale pid = %#v, want nil", got)
	}
}

func TestLoadDaemonManagedAgentsLivePID(t *testing.T) {
	state := DaemonAgentState{
		PID: os.Getpid(),
		Agents: []DaemonAgentStateEntry{
			{Worktree: "/tmp/api", Role: "task", Repo: "api"},
			{Worktree: "", Role: "lead", Repo: "docs"},
			{Worktree: "/tmp/docs", Role: "review"},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	path := filepath.Join(t.TempDir(), "daemon-agents.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	got := LoadDaemonManagedAgents(path)
	if len(got) != 2 {
		t.Fatalf("managed agents = %#v, want 2 entries", got)
	}
	if api := got["/tmp/api"]; !api.Managed || api.Role != "task" || api.Repo != "api" {
		t.Fatalf("api agent = %+v", api)
	}
	if docs := got["/tmp/docs"]; !docs.Managed || docs.Role != "review" || docs.Repo != "" {
		t.Fatalf("docs agent = %+v", docs)
	}
}
