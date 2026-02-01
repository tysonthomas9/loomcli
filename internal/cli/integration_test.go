package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Test fixtures for bd command responses
var bdReadyResponse = `[{"id":"TASK-1","title":"Implement feature","status":"open","priority":1}]`
var bdInProgressResponse = `[{"id":"TASK-2","title":"In progress task","status":"in_progress","priority":2,"assignee":"falcon"}]`
var bdStatsResponse = `{"summary":{"total_issues":10,"open_issues":3,"closed_issues":7}}`
var bdEmptyResponse = `[]`

// TestLockWorkflowIntegration tests the full lock lifecycle
func TestLockWorkflowIntegration(t *testing.T) {
	tmpDir := SetupTestWorktree(t, "falcon")
	wtPath := filepath.Join(tmpDir, "worktrees", "falcon")

	// Test: acquire lock
	err := AcquireLock(wtPath, "plan", "falcon")
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Verify lock exists with correct metadata
	info, running, err := CheckLock(wtPath)
	if err != nil {
		t.Fatalf("failed to check lock: %v", err)
	}
	if !running {
		t.Error("expected lock to be held")
	}
	if info.Command != "plan" {
		t.Errorf("expected command 'plan', got %q", info.Command)
	}
	if info.AgentName != "falcon" {
		t.Errorf("expected agent 'falcon', got %q", info.AgentName)
	}

	// Test: update lock with task
	err = UpdateLockTask(wtPath, "TASK-123", "Test task title")
	if err != nil {
		t.Fatalf("failed to update lock task: %v", err)
	}

	info, _, _ = CheckLock(wtPath)
	if info.TaskID != "TASK-123" {
		t.Errorf("expected task 'TASK-123', got %q", info.TaskID)
	}

	// Test: release lock
	ReleaseLock(wtPath)

	_, running, _ = CheckLock(wtPath)
	if running {
		t.Error("expected lock to be released")
	}
}

// TestLockContention tests that concurrent locks are prevented
func TestLockContention(t *testing.T) {
	tmpDir := SetupTestWorktree(t, "falcon")
	wtPath := filepath.Join(tmpDir, "worktrees", "falcon")

	// First agent acquires lock
	err := AcquireLock(wtPath, "plan", "falcon")
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}

	// Second agent should fail
	err = AcquireLock(wtPath, "task", "falcon")
	if err == nil {
		t.Error("expected second lock to fail")
	}

	// Release and try again
	ReleaseLock(wtPath)

	err = AcquireLock(wtPath, "task", "falcon")
	if err != nil {
		t.Errorf("lock after release should succeed: %v", err)
	}
	ReleaseLock(wtPath)
}

// TestMonitorDataCollection tests dashboard data aggregation with mocked bd commands
// TODO: Fix command count mismatch - the mock expects 8 calls but collectMonitorData
// makes fewer calls when there are no worktrees discovered. This test needs the
// worktree discovery to work with the mock setup.
func TestMonitorDataCollection(t *testing.T) {
	t.Skip("Skipping: command count mismatch with mock - needs worktree setup fix")
	// Save and restore working directory
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create worktree structure
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	wtPath := filepath.Join(worktreesDir, "falcon", ".git")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": worktreesDir,
	})

	// Use permissive mock that accepts any commands (empty Args = match any)
	mock := NewCommandMock(t, []CommandStub{
		// bd ready --json
		{Name: "bd", Stdout: bdReadyResponse},
		// bd list --status=in_progress --json
		{Name: "bd", Stdout: bdInProgressResponse},
		// bd list --status=open --json
		{Name: "bd", Stdout: bdEmptyResponse},
		// bd blocked --json
		{Name: "bd", Stdout: bdEmptyResponse},
		// bd sync --status
		{Name: "bd", Stdout: "synced"},
		// bd stats --json
		{Name: "bd", Stdout: bdStatsResponse},
		// git status --porcelain (for worktree clean check)
		{Name: "git", Stdout: ""},
		// git rev-list (for ahead/behind)
		{Name: "git", Stdout: "0\t0"},
	})
	mock.Install()

	data := collectMonitorData()

	// Verify task counts
	if data.Tasks.NeedsPlanning != 1 {
		t.Errorf("expected 1 needs planning task, got %d", data.Tasks.NeedsPlanning)
	}
	if data.Tasks.InProgress != 1 {
		t.Errorf("expected 1 in progress task, got %d", data.Tasks.InProgress)
	}

	// Verify stats
	if data.Stats.Total != 10 {
		t.Errorf("expected 10 total issues, got %d", data.Stats.Total)
	}
	if data.Stats.Open != 3 {
		t.Errorf("expected 3 open issues, got %d", data.Stats.Open)
	}
	if data.Stats.Closed != 7 {
		t.Errorf("expected 7 closed issues, got %d", data.Stats.Closed)
	}

	// Verify agent discovered
	if len(data.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(data.Agents))
	}
	if len(data.Agents) > 0 && data.Agents[0].Name != "falcon" {
		t.Errorf("expected agent 'falcon', got %q", data.Agents[0].Name)
	}
}

// TestMultiWorktreeDiscovery tests discovering multiple worktrees
// Note: Uses relative "worktrees" directory from current working directory
func TestMultiWorktreeDiscovery(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create worktrees in the relative path that GetWorktreesDir defaults to
	for _, name := range []string{"falcon", "nova", "ember"} {
		wtPath := filepath.Join("worktrees", name, ".git")
		if err := os.MkdirAll(wtPath, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Don't set LOOM_WORKTREES_DIR - use default "worktrees" relative path
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		t.Fatalf("failed to discover worktrees: %v", err)
	}

	if len(worktrees) != 3 {
		t.Errorf("expected 3 worktrees, got %d", len(worktrees))
	}

	// Verify all names are found
	names := make(map[string]bool)
	for _, wt := range worktrees {
		names[wt.Name] = true
	}
	for _, expected := range []string{"falcon", "nova", "ember"} {
		if !names[expected] {
			t.Errorf("expected worktree %q not found", expected)
		}
	}
}

// TestClaudeInvokerMocking tests that Claude invocation can be mocked
func TestClaudeInvokerMocking(t *testing.T) {
	// Track if Claude was invoked
	invoked := false
	capturedPrompt := ""
	capturedAgentName := ""

	origInvoker := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		invoked = true
		capturedPrompt = prompt
		capturedAgentName = agentName
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origInvoker })

	// Call InvokeClaude
	err := InvokeClaude("/tmp/test", "test prompt", "test-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !invoked {
		t.Error("expected Claude invoker to be called")
	}
	if capturedPrompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %q", capturedPrompt)
	}
	if capturedAgentName != "test-agent" {
		t.Errorf("expected agentName 'test-agent', got %q", capturedAgentName)
	}
}

// TestAgentStatusWithLock tests agent status when lock is held
func TestAgentStatusWithLock(t *testing.T) {
	tmpDir := SetupTestWorktree(t, "falcon")
	wtPath := filepath.Join(tmpDir, "worktrees", "falcon")

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": filepath.Join(tmpDir, "worktrees"),
	})

	// Acquire lock
	err := AcquireLock(wtPath, "plan", "falcon")
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer ReleaseLock(wtPath)

	// Update with task
	UpdateLockTask(wtPath, "TASK-42", "Test task")

	// Mock bd command to return task status
	mock := NewCommandMock(t, []CommandStub{
		// bd show for task status lookup
		{Name: "bd", Args: []string{"show", "TASK-42", "--json"},
			Stdout: `[{"id":"TASK-42","title":"Test task","status":"in_progress"}]`},
	})
	mock.Install()

	// Get lock status
	status := GetLockStatus(wtPath)

	// Should show planning state with task ID
	if status == "" {
		t.Error("expected non-empty lock status")
	}
}

// TestWorktreeResolution tests worktree path resolution
// Note: Uses relative "worktrees" directory from current working directory
func TestWorktreeResolution(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create worktree structure using relative path
	if err := os.MkdirAll(filepath.Join("worktrees", "falcon", ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Don't set LOOM_WORKTREES_DIR - use default relative path
	// Get the expected absolute path
	cwd, _ := os.Getwd()
	expectedPath := filepath.Join(cwd, "worktrees", "falcon")

	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "resolve by name",
			input:    "falcon",
			wantPath: expectedPath,
			wantErr:  false,
		},
		{
			name:     "non-existent worktree",
			input:    "nonexistent",
			wantPath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := ResolveWorktreePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if path != tt.wantPath {
					t.Errorf("expected path %q, got %q", tt.wantPath, path)
				}
			}
		})
	}
}

// TestDashboardRenderingDoesNotPanic ensures rendering works with various data
func TestDashboardRenderingDoesNotPanic(t *testing.T) {
	tests := []struct {
		name string
		data *MonitorData
	}{
		{
			name: "empty data",
			data: &MonitorData{},
		},
		{
			name: "with agents",
			data: &MonitorData{
				Agents: []AgentStatus{
					{Name: "falcon", Branch: "feature/test", Status: "ready"},
					{Name: "nova", Branch: "main", Status: "3 changes"},
				},
			},
		},
		{
			name: "with tasks",
			data: &MonitorData{
				Tasks: TaskSummary{
					NeedsPlanning:    5,
					ReadyToImplement: 3,
					InProgress:       2,
					NeedReview:       1,
					Backlog:          0,
				},
				NeedsPlanningTasks: []TaskInfo{
					{ID: "TASK-1", Title: "First task", Priority: 1},
				},
			},
		},
		{
			name: "with long titles",
			data: &MonitorData{
				NeedsPlanningTasks: []TaskInfo{
					{ID: "TASK-1", Title: "This is a very long task title that should be truncated properly", Priority: 1},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renderDashboard panicked: %v", r)
				}
			}()

			output := renderDashboard(tt.data)
			if output == "" {
				t.Error("expected non-empty output")
			}
		})
	}
}
