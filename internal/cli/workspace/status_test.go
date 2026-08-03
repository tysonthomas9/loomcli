package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildStatusDataSummarizesWorktreesAndTasks(t *testing.T) {
	runtime := RuntimeInfo{Applicable: true, Healthy: true, PID: 42, URL: "http://127.0.0.1:8080"}
	mon := &MonitorData{
		Agents: []AgentStatus{
			{Name: "falcon", Status: "working: task-1 (5m)", Repo: "github.com/org/repo-a"},
			{Name: "nova", Status: "planning: task-2 (3m)"},
			{Name: "ember", Status: "ready"},
			{Name: "spark", Status: "2 changes"},
		},
		Stats:      MonitorStats{Open: 10, InProgress: 3, Review: 2, Closed: 50},
		SyncStatus: SyncInfo{GitNeedsPush: 1, GitNeedsPull: 2},
	}

	data := buildStatusData(runtime, mon)
	if data.Runtime != runtime {
		t.Fatalf("runtime = %#v, want %#v", data.Runtime, runtime)
	}
	if data.Worktrees.Active != 2 || data.Worktrees.Idle != 2 {
		t.Fatalf("worktree counts = active:%d idle:%d, want 2/2", data.Worktrees.Active, data.Worktrees.Idle)
	}
	if data.Tasks.Open != 10 || data.Tasks.InProgress != 3 || data.Tasks.Review != 2 || data.Tasks.Closed != 50 {
		t.Fatalf("task summary = %#v", data.Tasks)
	}
	if data.Git.NeedsPush != 1 || data.Git.NeedsPull != 2 {
		t.Fatalf("git summary = %#v", data.Git)
	}
	if got := data.Worktrees.List[0].TaskID; got != "task-1" {
		t.Fatalf("task id = %q, want task-1", got)
	}
}

func TestBuildStatusDataNilMonitor(t *testing.T) {
	data := buildStatusData(RuntimeInfo{Applicable: false, Reason: "headless"}, nil)
	if data.Worktrees.Active != 0 || data.Worktrees.Idle != 0 || data.Tasks.Open != 0 {
		t.Fatalf("nil monitor produced nonzero data: %#v", data)
	}
}

func TestStatusJSONUsesRuntimeContractAndOmitsEmptyIssues(t *testing.T) {
	data := StatusData{
		Runtime: RuntimeInfo{Applicable: true, Healthy: true, PID: 12345, URL: "http://127.0.0.1:8181"},
		Backend: BackendInfo{Name: "codex", Source: "default"},
		Redis:   RedisInfo{Configured: true, Connected: true},
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"daemon"`) {
		t.Fatalf("retired daemon contract leaked into status JSON: %s", b)
	}
	if strings.Contains(string(b), `"issues"`) {
		t.Fatalf("empty issues should be omitted: %s", b)
	}
	var parsed StatusData
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Runtime.PID != 12345 || !parsed.Runtime.Healthy {
		t.Fatalf("runtime did not round-trip: %#v", parsed.Runtime)
	}
}

func TestIsActiveStatus(t *testing.T) {
	for _, status := range []string{"working: t-1", "planning: t-1", "review: t-1", "done: t-1"} {
		if !isActiveStatus(status) {
			t.Errorf("%q should be active", status)
		}
	}
	for _, status := range []string{"ready", "dirty", "2 changes", "error: t-1"} {
		if isActiveStatus(status) {
			t.Errorf("%q should be idle", status)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := map[time.Duration]string{
		3 * time.Second:             "3s",
		45 * time.Minute:            "45m",
		2 * time.Hour:               "2h",
		2*time.Hour + 3*time.Minute: "2h3m",
		1500 * time.Millisecond:     "2s",
	}
	for input, want := range tests {
		if got := formatDuration(input); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveBackendSourcePrecedence(t *testing.T) {
	origFlag := *backendFlagPtr
	t.Cleanup(func() { *backendFlagPtr = origFlag })
	t.Setenv("LOOM_BACKEND", "")
	*backendFlagPtr = ""
	if got := resolveBackendSource(); got != "default" {
		t.Fatalf("source = %q, want default", got)
	}
	t.Setenv("LOOM_BACKEND", "codex")
	if got := resolveBackendSource(); got != "env" {
		t.Fatalf("source = %q, want env", got)
	}
	*backendFlagPtr = "claude"
	if got := resolveBackendSource(); got != "flag" {
		t.Fatalf("source = %q, want flag", got)
	}
}

func TestCollectRedisStatusNotConfigured(t *testing.T) {
	t.Setenv("LOOM_REDIS_ADDR", "")
	if got := collectRedisStatus(); got.Configured || got.Connected || got.Error != "" {
		t.Fatalf("redis status = %#v, want not configured", got)
	}
}

func TestDetectIssuesFindsOrphanedInProgressTask(t *testing.T) {
	mon := &MonitorData{
		InProgressTasks: []TaskInfo{{ID: "task-1", Status: "in_progress"}},
		Agents:          []AgentStatus{{Name: "nova", Status: "ready"}},
	}
	issues := detectIssues(mon)
	found := false
	for _, issue := range issues {
		if issue.Message == "task task-1 is in_progress with no running agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan warning missing: %#v", issues)
	}
}

func TestAgentStatusRepoJSONOmitempty(t *testing.T) {
	withRepo, err := json.Marshal(AgentStatus{Name: "falcon", Repo: "github.com/org/repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withRepo), `"repo":"github.com/org/repo-a"`) {
		t.Fatalf("repo missing: %s", withRepo)
	}
	withoutRepo, err := json.Marshal(AgentStatus{Name: "nova"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutRepo), `"repo"`) {
		t.Fatalf("empty repo should be omitted: %s", withoutRepo)
	}
}
