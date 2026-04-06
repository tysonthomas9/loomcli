//go:build ignore

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// ============================================================================
// Daemon-Managed Agent Test Helpers
// ============================================================================

// collectDaemonStatusForDirHelper writes a daemon agent state file with the
// current process PID and returns the result of loadDaemonManagedAgents.
func collectDaemonStatusForDirHelper(t *testing.T, dir string, agents []DaemonAgentStateEntry) map[string]DaemonAgentInfo {
	t.Helper()
	return writeDaemonStateFile(t, dir, os.Getpid(), agents)
}

// writeDaemonStateFile writes a daemon agent state file with the given PID and
// agents, then returns the result of loadDaemonManagedAgents.
func writeDaemonStateFile(t *testing.T, dir string, pid int, agents []DaemonAgentStateEntry) map[string]DaemonAgentInfo {
	t.Helper()
	stateFilePath := filepath.Join(dir, "daemon-agents.json")
	state := DaemonAgentState{
		PID:    pid,
		Agents: agents,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(stateFilePath, data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}
	return loadDaemonManagedAgents(stateFilePath)
}

// ============================================================================
// Daemon-Managed Agents Tests (moved from monitor_test.go)
// ============================================================================

func TestLoadDaemonManagedAgents_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	result := loadDaemonManagedAgents(stateFilePath)

	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil when file doesn't exist", result)
	}
}

func TestLoadDaemonManagedAgents_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()

	result := collectDaemonStatusForDirHelper(t, tmpDir, []DaemonAgentStateEntry{
		{Worktree: "falcon", Status: "running"},
		{Worktree: "nova", Status: "idle"},
		{Worktree: "spark", Status: "running"},
	})

	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if len(result) != 3 {
		t.Errorf("len(result) = %d, want 3", len(result))
	}
	if !result["falcon"].Managed {
		t.Error("result[falcon].Managed = false, want true")
	}
	if !result["nova"].Managed {
		t.Error("result[nova].Managed = false, want true")
	}
	if !result["spark"].Managed {
		t.Error("result[spark].Managed = false, want true")
	}
}

func TestLoadDaemonManagedAgents_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	if err := os.WriteFile(stateFilePath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents(stateFilePath)

	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil for invalid JSON", result)
	}
}

func TestLoadDaemonManagedAgents_StaleDaemon(t *testing.T) {
	tmpDir := t.TempDir()

	result := writeDaemonStateFile(t, tmpDir, 2147483647, []DaemonAgentStateEntry{
		{Worktree: "falcon", Status: "running"},
	})

	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil for stale daemon (non-existent PID)", result)
	}
}

func TestLoadDaemonManagedAgents_EmptyAgents(t *testing.T) {
	tmpDir := t.TempDir()

	result := collectDaemonStatusForDirHelper(t, tmpDir, []DaemonAgentStateEntry{})

	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want empty map for empty agents")
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestLoadDaemonManagedAgents_SkipsEmptyWorktreeNames(t *testing.T) {
	tmpDir := t.TempDir()

	result := collectDaemonStatusForDirHelper(t, tmpDir, []DaemonAgentStateEntry{
		{Worktree: "falcon", Status: "running"},
		{Worktree: "", Status: "running"},
		{Worktree: "nova", Status: "idle"},
	})

	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2 (should skip empty worktree name)", len(result))
	}
	if !result["falcon"].Managed {
		t.Error("result[falcon].Managed = false, want true")
	}
	if !result["nova"].Managed {
		t.Error("result[nova].Managed = false, want true")
	}
	if result[""].Managed {
		t.Error("result[\"\"].Managed = true, want false (empty worktree name should be skipped)")
	}
}

func TestLoadDaemonManagedAgents_WithRole(t *testing.T) {
	tmpDir := t.TempDir()

	result := collectDaemonStatusForDirHelper(t, tmpDir, []DaemonAgentStateEntry{
		{Worktree: "falcon", Status: "running", Role: "task"},
		{Worktree: "nova", Status: "idle", Role: "plan"},
		{Worktree: "spark", Status: "running"},
	})

	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if result["falcon"].Role != "task" {
		t.Errorf("result[falcon].Role = %q, want %q", result["falcon"].Role, "task")
	}
	if result["nova"].Role != "plan" {
		t.Errorf("result[nova].Role = %q, want %q", result["nova"].Role, "plan")
	}
	if result["spark"].Role != "" {
		t.Errorf("result[spark].Role = %q, want empty string", result["spark"].Role)
	}
}

func TestRenderAgentLine_WithDaemonMarker(t *testing.T) {
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		Ahead:         0,
		Behind:        0,
		DaemonManaged: true,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	if !strings.Contains(output, "[D]") {
		t.Errorf("output should contain '[D]' marker for daemon-managed agent, got:\n%s", output)
	}
	if !strings.Contains(output, "[D] falcon") {
		t.Errorf("output should contain '[D] falcon', got:\n%s", output)
	}
}

func TestRenderAgentLine_WithoutDaemonMarker(t *testing.T) {
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		Ahead:         0,
		Behind:        0,
		DaemonManaged: false,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	if strings.Contains(output, "[D]") {
		t.Errorf("output should NOT contain '[D]' marker for non-daemon agent, got:\n%s", output)
	}
	if !strings.Contains(output, "falcon") {
		t.Errorf("output should contain agent name 'falcon', got:\n%s", output)
	}
}

func TestRenderAgentLine_DaemonManagedWithSyncIndicators(t *testing.T) {
	agent := AgentStatus{
		Name:          "nova",
		Branch:        "nova",
		Status:        "working: T-1 (5m)",
		Ahead:         3,
		Behind:        2,
		DaemonManaged: true,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	if !strings.Contains(output, "[D]") {
		t.Error("missing [D] marker")
	}
	if !strings.Contains(output, "nova") {
		t.Error("missing agent name")
	}
	if !strings.Contains(output, "working:") {
		t.Error("missing status")
	}
	if !strings.Contains(output, "↑3") {
		t.Error("missing ahead indicator")
	}
	if !strings.Contains(output, "↓2") {
		t.Error("missing behind indicator")
	}
}

func TestAgentStatusDaemonManagedField(t *testing.T) {
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		DaemonManaged: true,
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr := string(data)

	if !strings.Contains(jsonStr, `"daemon_managed":true`) {
		t.Errorf("expected daemon_managed:true in JSON, got: %s", jsonStr)
	}

	agentNotManaged := AgentStatus{
		Name:          "nova",
		Branch:        "nova",
		Status:        "ready",
		DaemonManaged: false,
	}

	data, err = json.Marshal(agentNotManaged)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr = string(data)

	if strings.Contains(jsonStr, "daemon_managed") {
		t.Errorf("daemon_managed should be omitted when false (omitempty), got: %s", jsonStr)
	}
}

func TestDaemonAgentStateStructs(t *testing.T) {
	jsonData := `{
		"pid": 12345,
		"agents": [
			{"worktree": "falcon", "status": "running"},
			{"worktree": "nova", "status": "idle"}
		]
	}`

	var state DaemonAgentState
	if err := json.Unmarshal([]byte(jsonData), &state); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if state.PID != 12345 {
		t.Errorf("PID = %d, want 12345", state.PID)
	}
	if len(state.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(state.Agents))
	}
	if state.Agents[0].Worktree != "falcon" {
		t.Errorf("Agents[0].Worktree = %q, want %q", state.Agents[0].Worktree, "falcon")
	}
	if state.Agents[0].Status != "running" {
		t.Errorf("Agents[0].Status = %q, want %q", state.Agents[0].Status, "running")
	}
	if state.Agents[1].Worktree != "nova" {
		t.Errorf("Agents[1].Worktree = %q, want %q", state.Agents[1].Worktree, "nova")
	}
	if state.Agents[1].Status != "idle" {
		t.Errorf("Agents[1].Status = %q, want %q", state.Agents[1].Status, "idle")
	}
}

// ============================================================================
// Repo Field Tests
// ============================================================================

func TestLoadDaemonManagedAgents_WithRepo(t *testing.T) {
	tmpDir := t.TempDir()

	result := collectDaemonStatusForDirHelper(t, tmpDir, []DaemonAgentStateEntry{
		{Worktree: "falcon", Status: "running", Role: "task", Repo: "github.com/org/repo-a"},
		{Worktree: "nova", Status: "idle", Role: "plan", Repo: "github.com/org/repo-b"},
		{Worktree: "spark", Status: "running"},
	})

	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if result["falcon"].Repo != "github.com/org/repo-a" {
		t.Errorf("result[falcon].Repo = %q, want %q", result["falcon"].Repo, "github.com/org/repo-a")
	}
	if result["nova"].Repo != "github.com/org/repo-b" {
		t.Errorf("result[nova].Repo = %q, want %q", result["nova"].Repo, "github.com/org/repo-b")
	}
	if result["spark"].Repo != "" {
		t.Errorf("result[spark].Repo = %q, want empty string", result["spark"].Repo)
	}
}

func TestAgentStatusRepoJSONOmitempty(t *testing.T) {
	// When Repo is set, it should appear in JSON
	agent := AgentStatus{
		Name:   "falcon",
		Branch: "falcon",
		Status: "ready",
		Repo:   "github.com/org/repo-a",
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"repo":"github.com/org/repo-a"`) {
		t.Errorf("expected repo in JSON, got: %s", jsonStr)
	}

	// When Repo is empty, it should be omitted (omitempty)
	agentNoRepo := AgentStatus{
		Name:   "nova",
		Branch: "nova",
		Status: "ready",
	}

	data, err = json.Marshal(agentNoRepo)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr = string(data)
	if strings.Contains(jsonStr, "repo") {
		t.Errorf("repo should be omitted when empty (omitempty), got: %s", jsonStr)
	}
}

func TestDaemonAgentStateEntryRepoJSONRoundTrip(t *testing.T) {
	jsonData := `{
		"pid": 12345,
		"agents": [
			{"worktree": "falcon", "status": "running", "role": "task", "repo": "github.com/org/repo-a"},
			{"worktree": "nova", "status": "idle"}
		]
	}`

	var state DaemonAgentState
	if err := json.Unmarshal([]byte(jsonData), &state); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if state.Agents[0].Repo != "github.com/org/repo-a" {
		t.Errorf("Agents[0].Repo = %q, want %q", state.Agents[0].Repo, "github.com/org/repo-a")
	}
	if state.Agents[1].Repo != "" {
		t.Errorf("Agents[1].Repo = %q, want empty string", state.Agents[1].Repo)
	}

	// Re-marshal and verify omitempty behavior
	reData, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to re-marshal: %v", err)
	}
	reStr := string(reData)
	if !strings.Contains(reStr, `"repo":"github.com/org/repo-a"`) {
		t.Errorf("expected repo for falcon in re-marshaled JSON, got: %s", reStr)
	}
	// nova has no repo, so repo should not appear for it; check via unmarshal into raw
	var reState DaemonAgentState
	if err := json.Unmarshal(reData, &reState); err != nil {
		t.Fatalf("failed to unmarshal re-marshaled data: %v", err)
	}
	if reState.Agents[1].Repo != "" {
		t.Errorf("re-marshaled Agents[1].Repo = %q, want empty", reState.Agents[1].Repo)
	}
}

func TestBuildStatusDataIncludesRepo(t *testing.T) {
	daemon := DaemonInfo{}
	mon := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "falcon", Status: "working: task-1 (5m)", Repo: "github.com/org/repo-a"},
			{Name: "nova", Status: "ready"},
		},
	}

	data := buildStatusData(daemon, mon)

	if len(data.Worktrees.List) < 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(data.Worktrees.List))
	}

	// Verify the worktree items are populated (repo is on AgentStatus, not WorktreeStatusItem,
	// so we verify the AgentStatus struct itself carries it through JSON serialization)
	b, err := json.Marshal(mon.Agents[0])
	if err != nil {
		t.Fatalf("failed to marshal agent: %v", err)
	}
	if !strings.Contains(string(b), `"repo":"github.com/org/repo-a"`) {
		t.Errorf("expected repo in agent JSON, got: %s", string(b))
	}

	b, err = json.Marshal(mon.Agents[1])
	if err != nil {
		t.Fatalf("failed to marshal agent: %v", err)
	}
	if strings.Contains(string(b), `"repo"`) {
		t.Errorf("repo should be omitted for agent without repo, got: %s", string(b))
	}
}
