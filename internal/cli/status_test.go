package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// ============================================================================
// collectDaemonStatusForDir Tests
// ============================================================================

func TestStatusDaemonRunning(t *testing.T) {
	tmpDir := t.TempDir()
	loomDir := filepath.Join(tmpDir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write PID file with current process PID (which is running)
	pidFile := filepath.Join(loomDir, "daemon.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write state file
	state := DaemonState{
		PID:       os.Getpid(),
		StartedAt: time.Now().Add(-2 * time.Hour),
		Agents:    nil,
	}
	stateData, _ := json.Marshal(state)
	stateFile := filepath.Join(loomDir, "daemon-agents.json")
	if err := os.WriteFile(stateFile, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	info := collectDaemonStatusForDir(tmpDir)

	if !info.Running {
		t.Error("expected daemon to be running")
	}
	if info.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
	if info.StalePID {
		t.Error("expected stale_pid to be false")
	}
}

func TestStatusDaemonStopped(t *testing.T) {
	tmpDir := t.TempDir()

	info := collectDaemonStatusForDir(tmpDir)

	if info.Running {
		t.Error("expected daemon to not be running")
	}
	if info.PID != 0 {
		t.Errorf("pid = %d, want 0", info.PID)
	}
	if info.StalePID {
		t.Error("expected stale_pid to be false when no pid file exists")
	}
}

func TestStatusDaemonStalePID(t *testing.T) {
	tmpDir := t.TempDir()
	loomDir := filepath.Join(tmpDir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write PID file with non-existent PID
	pidFile := filepath.Join(loomDir, "daemon.pid")
	if err := os.WriteFile(pidFile, []byte("999999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	info := collectDaemonStatusForDir(tmpDir)

	if info.Running {
		t.Error("expected daemon to not be running")
	}
	if !info.StalePID {
		t.Error("expected stale_pid to be true")
	}
}

// ============================================================================
// collectBackendInfo Tests
// ============================================================================

func TestStatusBackendDefault(t *testing.T) {
	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })
	backendFlag = ""

	origEnv := os.Getenv("LOOM_BACKEND")
	t.Cleanup(func() { os.Setenv("LOOM_BACKEND", origEnv) })
	os.Unsetenv("LOOM_BACKEND")

	info := collectBackendInfo()

	if info.Name != "claude" {
		t.Errorf("name = %q, want %q", info.Name, "claude")
	}
	if info.Source != "default" {
		t.Errorf("source = %q, want %q", info.Source, "default")
	}
}

func TestStatusBackendFromFlag(t *testing.T) {
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })
	backendFlag = "codex"

	info := collectBackendInfo()

	if info.Name != "codex" {
		t.Errorf("name = %q, want %q", info.Name, "codex")
	}
	if info.Source != "flag" {
		t.Errorf("source = %q, want %q", info.Source, "flag")
	}
}

func TestStatusBackendFromEnv(t *testing.T) {
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })
	backendFlag = ""

	origEnv := os.Getenv("LOOM_BACKEND")
	t.Cleanup(func() { os.Setenv("LOOM_BACKEND", origEnv) })
	os.Setenv("LOOM_BACKEND", "opencode")

	info := collectBackendInfo()

	if info.Name != "opencode" {
		t.Errorf("name = %q, want %q", info.Name, "opencode")
	}
	if info.Source != "env" {
		t.Errorf("source = %q, want %q", info.Source, "env")
	}
}

// ============================================================================
// detectIssues Tests
// ============================================================================

func TestStatusOrphanedTaskDetection(t *testing.T) {
	mon := &MonitorData{
		Timestamp: time.Now(),
		InProgressTasks: []TaskInfo{
			{ID: "task-1", Title: "Test task"},
		},
		Agents: []AgentStatus{
			{Name: "falcon", Status: "ready"},
		},
	}

	issues := detectIssues(mon)

	found := false
	for _, issue := range issues {
		if issue.Level == "warning" && issue.Message == "task task-1 is in_progress with no running agent" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected orphaned task warning, got issues: %+v", issues)
	}
}

func TestStatusNoOrphanedTaskWhenAgentRunning(t *testing.T) {
	mon := &MonitorData{
		Timestamp: time.Now(),
		InProgressTasks: []TaskInfo{
			{ID: "task-1", Title: "Test task"},
		},
		Agents: []AgentStatus{
			{Name: "falcon", Status: "working: task-1 (5m)"},
		},
	}

	issues := detectIssues(mon)

	for _, issue := range issues {
		if issue.Message == "task task-1 is in_progress with no running agent" {
			t.Error("should not report orphaned task when agent is running")
		}
	}
}

// ============================================================================
// buildStatusData Tests
// ============================================================================

func TestBuildStatusDataWorktreeCounts(t *testing.T) {
	daemon := DaemonInfo{Running: false}
	mon := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "falcon", Status: "working: task-1 (5m)"},
			{Name: "nova", Status: "planning: task-2 (3m)"},
			{Name: "ember", Status: "ready"},
			{Name: "spark", Status: "2 changes"},
		},
		Stats: MonitorStats{
			Open:       10,
			InProgress: 3,
			Review:     2,
			Closed:     50,
		},
		SyncStatus: SyncInfo{
			GitNeedsPush: 1,
			GitNeedsPull: 2,
		},
	}

	data := buildStatusData(daemon, mon)

	if data.Worktrees.Active != 2 {
		t.Errorf("active = %d, want 2", data.Worktrees.Active)
	}
	if data.Worktrees.Idle != 2 {
		t.Errorf("idle = %d, want 2", data.Worktrees.Idle)
	}
	if len(data.Worktrees.List) != 4 {
		t.Errorf("list len = %d, want 4", len(data.Worktrees.List))
	}
	if data.Beads.Open != 10 {
		t.Errorf("beads.open = %d, want 10", data.Beads.Open)
	}
	if data.Beads.InProgress != 3 {
		t.Errorf("beads.in_progress = %d, want 3", data.Beads.InProgress)
	}
	if data.Git.NeedsPush != 1 {
		t.Errorf("git.needs_push = %d, want 1", data.Git.NeedsPush)
	}
	if data.Git.NeedsPull != 2 {
		t.Errorf("git.needs_pull = %d, want 2", data.Git.NeedsPull)
	}
}

func TestBuildStatusDataNilMonitor(t *testing.T) {
	daemon := DaemonInfo{Running: false}
	data := buildStatusData(daemon, nil)

	if data.Worktrees.Active != 0 {
		t.Errorf("active = %d, want 0", data.Worktrees.Active)
	}
	if data.Worktrees.Idle != 0 {
		t.Errorf("idle = %d, want 0", data.Worktrees.Idle)
	}
	if data.Beads.Open != 0 {
		t.Errorf("beads.open = %d, want 0", data.Beads.Open)
	}
}

// ============================================================================
// StatusData JSON Tests
// ============================================================================

func TestStatusJSON(t *testing.T) {
	data := StatusData{
		Daemon: DaemonInfo{
			Running: true,
			PID:     12345,
			Uptime:  "2h3m",
		},
		Backend: BackendInfo{
			Name:   "claude",
			Source: "default",
		},
		Worktrees: WorktreesSummary{
			Active: 2,
			Idle:   1,
			List: []WorktreeStatusItem{
				{Name: "falcon", Status: "working: task-1 (5m)", TaskID: "task-1"},
				{Name: "nova", Status: "planning: task-2 (3m)", TaskID: "task-2"},
				{Name: "ember", Status: "ready"},
			},
		},
		Beads: BeadsSummary{
			Open:       12,
			InProgress: 5,
			Review:     3,
			Closed:     28,
		},
		Git: GitSummary{
			NeedsPush: 2,
			NeedsPull: 1,
		},
		Redis: RedisInfo{
			Configured: true,
			Connected:  true,
		},
		Issues: []StatusIssue{
			{Level: "warning", Message: "test issue"},
		},
	}

	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	var parsed StatusData
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if parsed.Daemon.PID != 12345 {
		t.Errorf("daemon.pid = %d, want 12345", parsed.Daemon.PID)
	}
	if parsed.Backend.Name != "claude" {
		t.Errorf("backend.name = %q, want %q", parsed.Backend.Name, "claude")
	}
	if parsed.Worktrees.Active != 2 {
		t.Errorf("worktrees.active = %d, want 2", parsed.Worktrees.Active)
	}
	if len(parsed.Issues) != 1 {
		t.Errorf("issues len = %d, want 1", len(parsed.Issues))
	}
	if parsed.Redis.Connected != true {
		t.Error("redis.connected = false, want true")
	}
}

func TestStatusJSONOmitsEmptyIssues(t *testing.T) {
	data := StatusData{
		Issues: nil,
	}

	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if _, ok := raw["issues"]; ok {
		t.Error("expected issues to be omitted when nil")
	}
}

// ============================================================================
// isActiveStatus Tests
// ============================================================================

func TestIsActiveStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"working: task-1 (5m)", true},
		{"planning: task-2 (3m)", true},
		{"review: task-3 (1m)", true},
		{"done: task-4 (10s)", true},
		{"ready", false},
		{"2 changes", false},
		{"dirty", false},
		{"idle (5m)", false},
		{"error: task-5", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := isActiveStatus(tt.status)
			if got != tt.want {
				t.Errorf("isActiveStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

// ============================================================================
// formatDuration Tests
// ============================================================================

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Second, "3s"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{45 * time.Minute, "45m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{1*time.Hour + 1*time.Minute, "1h1m"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// ============================================================================
// resolveBackendSource Tests
// ============================================================================

func TestResolveBackendSourcePrecedence(t *testing.T) {
	// Save and restore
	origFlag := backendFlag
	origEnv := os.Getenv("LOOM_BACKEND")
	t.Cleanup(func() {
		backendFlag = origFlag
		os.Setenv("LOOM_BACKEND", origEnv)
	})

	// Flag takes precedence
	backendFlag = "codex"
	os.Setenv("LOOM_BACKEND", "opencode")
	if got := resolveBackendSource(); got != "flag" {
		t.Errorf("with flag+env, source = %q, want %q", got, "flag")
	}

	// Env next
	backendFlag = ""
	if got := resolveBackendSource(); got != "env" {
		t.Errorf("with env only, source = %q, want %q", got, "env")
	}

	// Default fallback
	os.Unsetenv("LOOM_BACKEND")
	if got := resolveBackendSource(); got != "default" {
		// Note: might be "project" or "config" if config files exist
		// so we just check it's not "flag" or "env"
		if got == "flag" || got == "env" {
			t.Errorf("with nothing, source = %q, should not be flag or env", got)
		}
	}
}

// ============================================================================
// collectRedisStatus Tests
// ============================================================================

func TestCollectRedisStatusNotConfigured(t *testing.T) {
	origEnv := os.Getenv("LOOM_REDIS_ADDR")
	t.Cleanup(func() { os.Setenv("LOOM_REDIS_ADDR", origEnv) })
	os.Unsetenv("LOOM_REDIS_ADDR")

	info := collectRedisStatus()

	if info.Configured {
		t.Error("expected configured = false when LOOM_REDIS_ADDR not set")
	}
	if info.Connected {
		t.Error("expected connected = false when not configured")
	}
}

func TestCollectRedisStatusUnreachable(t *testing.T) {
	origEnv := os.Getenv("LOOM_REDIS_ADDR")
	t.Cleanup(func() { os.Setenv("LOOM_REDIS_ADDR", origEnv) })
	// Use an unreachable address
	os.Setenv("LOOM_REDIS_ADDR", "localhost:59999")

	origPassword := os.Getenv("LOOM_REDIS_PASSWORD")
	t.Cleanup(func() { os.Setenv("LOOM_REDIS_PASSWORD", origPassword) })
	os.Unsetenv("LOOM_REDIS_PASSWORD")

	info := collectRedisStatus()

	if !info.Configured {
		t.Error("expected configured = true")
	}
	if info.Connected {
		t.Error("expected connected = false for unreachable Redis")
	}
	if info.Error == "" {
		t.Error("expected non-empty error for unreachable Redis")
	}
}

// ============================================================================
// WorktreeStatusItem task ID extraction
// ============================================================================

func TestBuildStatusDataExtractsTaskID(t *testing.T) {
	daemon := DaemonInfo{}
	mon := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "falcon", Status: "working: loomcli-abc.1 (5m)"},
			{Name: "nova", Status: "ready"},
		},
	}

	data := buildStatusData(daemon, mon)

	if len(data.Worktrees.List) < 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(data.Worktrees.List))
	}

	if data.Worktrees.List[0].TaskID != "loomcli-abc.1" {
		t.Errorf("task_id = %q, want %q", data.Worktrees.List[0].TaskID, "loomcli-abc.1")
	}
	if data.Worktrees.List[1].TaskID != "" {
		t.Errorf("task_id for idle = %q, want empty", data.Worktrees.List[1].TaskID)
	}
}
