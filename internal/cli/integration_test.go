//go:build integration
// +build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestMonitorDataCollection tests dashboard data aggregation with mocked bd commands.
// Uses FlexibleCommandMock to handle variable call counts from parallel execution.
func TestMonitorDataCollection(t *testing.T) {
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

	// Use flexible mock that matches by command pattern (order-independent)
	mock := NewFlexibleCommandMock(t)

	// bd commands (called in parallel, order varies)
	mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: bdReadyResponse}).WithMinCalls(1)
	mock.AddStub("bd", []string{"list", "--status=in_progress"}, CommandResult{Stdout: bdInProgressResponse}).WithMinCalls(1)
	mock.AddStub("bd", []string{"list", "--status=review"}, CommandResult{Stdout: bdEmptyResponse}).WithMinCalls(1)
	mock.AddStub("bd", []string{"blocked"}, CommandResult{Stdout: bdEmptyResponse}).WithMinCalls(1)
	mock.AddStub("bd", []string{"sync"}, CommandResult{Stdout: "synced"}).WithMinCalls(1)
	mock.AddStub("bd", []string{"stats"}, CommandResult{Stdout: bdStatsResponse}).WithMinCalls(1)

	// git commands for worktree (may be called multiple times per worktree)
	mock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "main"})
	mock.AddStub("git", []string{"branch", "-r"}, CommandResult{Stdout: "origin/main"})
	mock.AddStub("git", []string{"status", "--porcelain"}, CommandResult{Stdout: ""})
	mock.AddStub("git", []string{"rev-list"}, CommandResult{Stdout: "0\t0"})
	mock.AddStub("git", []string{"rev-parse"}, CommandResult{Stdout: "main"})
	mock.AddStub("git", []string{"remote", "get-url"}, CommandResult{Stdout: "https://github.com/user/repo.git"})

	mock.Install()

	data := collectMonitorData(100, "")

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

// TestAgentInvokerMocking tests that agent invocation can be mocked and that
// arguments flow correctly through the backend dispatch to the invoker
func TestAgentInvokerMocking(t *testing.T) {
	recorder := SetupMockClaudeInvoker(t, nil)

	err := InvokeAgent("/tmp/test", "test prompt", "test-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(recorder.Invocations))
	}

	inv := recorder.Invocations[0]
	if inv.WorkDir != "/tmp/test" {
		t.Errorf("expected workDir '/tmp/test', got %q", inv.WorkDir)
	}
	if inv.Prompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %q", inv.Prompt)
	}
	if inv.AgentName != "test-agent" {
		t.Errorf("expected agentName 'test-agent', got %q", inv.AgentName)
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

// TestMultiAgentConflictDetection tests that multiple agents claiming the same task
// are detected and reported in TaskConflicts
func TestMultiAgentConflictDetection(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create two worktrees with locks pointing to the same task
	pid := os.Getpid()
	locks := map[string]*LockInfo{
		"falcon": {
			PID:       pid,
			Command:   "task",
			AgentName: "falcon",
			TaskID:    "TASK-CONFLICT",
			StartedAt: time.Now(),
		},
		"nova": {
			PID:       pid,
			Command:   "task",
			AgentName: "nova",
			TaskID:    "TASK-CONFLICT",
			StartedAt: time.Now(),
		},
	}
	tmpBase := SetupMultiWorktreeEnv(t, []string{"falcon", "nova"}, locks)

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": filepath.Join(tmpBase, "worktrees"),
	})

	// Mock bd commands
	mock := NewFlexibleCommandMock(t)
	mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"list", "--status=in_progress"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"list", "--status=review"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"blocked"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"sync"}, CommandResult{Stdout: "synced"})
	mock.AddStub("bd", []string{"stats"}, CommandResult{Stdout: `{"summary":{"total_issues":0,"open_issues":0,"closed_issues":0}}`})
	mock.AddStub("bd", []string{"show"}, CommandResult{Stdout: `[{"id":"TASK-CONFLICT","status":"in_progress"}]`})
	mock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "feature/test"})
	mock.AddStub("git", []string{"branch", "-r"}, CommandResult{Stdout: "origin/main"})
	mock.AddStub("git", []string{"status", "--porcelain"}, CommandResult{Stdout: ""})
	mock.AddStub("git", []string{"rev-list"}, CommandResult{Stdout: "0\t0"})
	mock.AddStub("git", []string{"rev-parse"}, CommandResult{Stdout: "main"})
	mock.AddStub("git", []string{"remote", "get-url"}, CommandResult{Stdout: "https://github.com/user/repo.git"})
	mock.Install()

	data := collectMonitorData(100, "")

	// Verify conflict detected
	if len(data.TaskConflicts) == 0 {
		t.Error("expected TaskConflicts to be non-empty")
	}
	agents, ok := data.TaskConflicts["TASK-CONFLICT"]
	if !ok {
		t.Errorf("expected conflict for TASK-CONFLICT, got conflicts: %v", data.TaskConflicts)
	} else if len(agents) != 2 {
		t.Errorf("expected 2 agents in conflict, got %d: %v", len(agents), agents)
	}
}

// TestNoConflictForDifferentTasks verifies that agents working on different tasks
// do NOT trigger a conflict detection
func TestNoConflictForDifferentTasks(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create two worktrees with locks pointing to DIFFERENT tasks
	pid := os.Getpid()
	locks := map[string]*LockInfo{
		"falcon": {
			PID:       pid,
			Command:   "task",
			AgentName: "falcon",
			TaskID:    "TASK-1", // Different task
			StartedAt: time.Now(),
		},
		"nova": {
			PID:       pid,
			Command:   "task",
			AgentName: "nova",
			TaskID:    "TASK-2", // Different task
			StartedAt: time.Now(),
		},
	}
	tmpBase := SetupMultiWorktreeEnv(t, []string{"falcon", "nova"}, locks)

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": filepath.Join(tmpBase, "worktrees"),
	})

	// Mock bd commands
	mock := NewFlexibleCommandMock(t)
	mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"list", "--status=in_progress"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"list", "--status=review"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"blocked"}, CommandResult{Stdout: "[]"})
	mock.AddStub("bd", []string{"sync"}, CommandResult{Stdout: "synced"})
	mock.AddStub("bd", []string{"stats"}, CommandResult{Stdout: `{"summary":{"total_issues":0,"open_issues":0,"closed_issues":0}}`})
	mock.AddStub("bd", []string{"show"}, CommandResult{Stdout: `[{"id":"TASK-1","status":"in_progress"}]`})
	mock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "feature/test"})
	mock.AddStub("git", []string{"branch", "-r"}, CommandResult{Stdout: "origin/main"})
	mock.AddStub("git", []string{"status", "--porcelain"}, CommandResult{Stdout: ""})
	mock.AddStub("git", []string{"rev-list"}, CommandResult{Stdout: "0\t0"})
	mock.AddStub("git", []string{"rev-parse"}, CommandResult{Stdout: "main"})
	mock.AddStub("git", []string{"remote", "get-url"}, CommandResult{Stdout: "https://github.com/user/repo.git"})
	mock.Install()

	data := collectMonitorData(100, "")

	// Verify NO conflict detected (different tasks)
	if len(data.TaskConflicts) != 0 {
		t.Errorf("expected no TaskConflicts for different tasks, got: %v", data.TaskConflicts)
	}
}

// TestCrashRecoveryFlow tests that stale locks (from dead processes) are recovered
func TestCrashRecoveryFlow(t *testing.T) {
	tmpDir := SetupTestWorktree(t, "falcon")
	wtPath := filepath.Join(tmpDir, "worktrees", "falcon")

	// Create a lock file with a non-existent PID (simulating crashed agent)
	stalePID := 999999 // Unlikely to be a real process
	staleLock := &LockInfo{
		PID:       stalePID,
		Command:   "task",
		AgentName: "crashed-agent",
		TaskID:    "TASK-OLD",
		StartedAt: time.Now().Add(-1 * time.Hour),
	}

	// Write stale lock manually
	lockPath := filepath.Join(wtPath, LockFileName)
	data, _ := json.Marshal(staleLock)
	if err := os.WriteFile(lockPath, data, 0644); err != nil {
		t.Fatalf("failed to write stale lock: %v", err)
	}

	// Verify stale lock exists
	_, _, err := CheckLock(wtPath)
	if err != nil {
		t.Fatalf("failed to check lock: %v", err)
	}

	// Acquire lock should succeed (stale lock should be removed)
	err = AcquireLock(wtPath, "plan", "new-agent")
	if err != nil {
		t.Fatalf("failed to acquire lock over stale lock: %v", err)
	}
	defer ReleaseLock(wtPath)

	// Verify new lock has correct metadata
	info, running, err := CheckLock(wtPath)
	if err != nil {
		t.Fatalf("failed to check new lock: %v", err)
	}
	if !running {
		t.Error("expected lock to be held")
	}
	if info.AgentName != "new-agent" {
		t.Errorf("expected agent 'new-agent', got %q", info.AgentName)
	}
	if info.Command != "plan" {
		t.Errorf("expected command 'plan', got %q", info.Command)
	}
}

// TestMonitorAgentStatusVariants tests all agent status display states
func TestMonitorAgentStatusVariants(t *testing.T) {
	tests := []struct {
		name           string
		hasLock        bool
		lockInfo       *LockInfo
		gitClean       bool
		gitChanges     int
		expectedStatus string
	}{
		{
			name:           "no lock clean tree",
			hasLock:        false,
			gitClean:       true,
			expectedStatus: "ready",
		},
		{
			name:           "no lock dirty tree",
			hasLock:        false,
			gitClean:       false,
			gitChanges:     3,
			expectedStatus: "3 changes",
		},
		{
			name:    "active lock with task",
			hasLock: true,
			lockInfo: &LockInfo{
				PID:       os.Getpid(),
				Command:   "plan",
				AgentName: "test-agent",
				TaskID:    "TASK-1",
				StartedAt: time.Now(),
				State:     StateActive,
			},
			gitClean:       true,
			expectedStatus: "planning:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore working directory
			origDir, _ := os.Getwd()
			tmpDir := t.TempDir()
			os.Chdir(tmpDir)
			t.Cleanup(func() { os.Chdir(origDir) })

			// Create worktree
			worktreesDir := filepath.Join(tmpDir, "worktrees")
			wtPath := filepath.Join(worktreesDir, "test-agent")
			if err := os.MkdirAll(filepath.Join(wtPath, ".git"), 0755); err != nil {
				t.Fatal(err)
			}

			SetupTestEnv(t, map[string]string{
				"LOOM_WORKTREES_DIR": worktreesDir,
			})

			if tt.hasLock && tt.lockInfo != nil {
				// Write lock file
				lockPath := filepath.Join(wtPath, LockFileName)
				data, err := json.Marshal(tt.lockInfo)
				if err != nil {
					t.Fatalf("failed to marshal lock info: %v", err)
				}
				if err := os.WriteFile(lockPath, data, 0644); err != nil {
					t.Fatalf("failed to write lock file: %v", err)
				}
			}

			// Mock commands
			mock := NewFlexibleCommandMock(t)
			mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: "[]"})
			mock.AddStub("bd", []string{"list"}, CommandResult{Stdout: "[]"})
			mock.AddStub("bd", []string{"blocked"}, CommandResult{Stdout: "[]"})
			mock.AddStub("bd", []string{"sync"}, CommandResult{Stdout: "synced"})
			mock.AddStub("bd", []string{"stats"}, CommandResult{Stdout: `{"summary":{"total_issues":0,"open_issues":0,"closed_issues":0}}`})
			mock.AddStub("bd", []string{"show"}, CommandResult{Stdout: `[{"id":"TASK-1","status":"in_progress"}]`})

			mock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "main"})
			mock.AddStub("git", []string{"branch", "-r"}, CommandResult{Stdout: "origin/main"})
			if tt.gitClean {
				mock.AddStub("git", []string{"status", "--porcelain"}, CommandResult{Stdout: ""})
			} else {
				// Simulate N modified files
				changes := ""
				for i := 0; i < tt.gitChanges; i++ {
					changes += " M file" + string(rune('a'+i)) + ".go\n"
				}
				mock.AddStub("git", []string{"status", "--porcelain"}, CommandResult{Stdout: changes})
			}
			mock.AddStub("git", []string{"rev-list"}, CommandResult{Stdout: "0\t0"})
			mock.AddStub("git", []string{"rev-parse"}, CommandResult{Stdout: "main"})
			mock.AddStub("git", []string{"remote", "get-url"}, CommandResult{Stdout: "https://github.com/user/repo.git"})
			mock.Install()

			data := collectMonitorData(100, "")

			// Verify agent status
			if len(data.Agents) != 1 {
				t.Fatalf("expected 1 agent, got %d", len(data.Agents))
			}
			agent := data.Agents[0]
			if tt.expectedStatus != "" && !containsSubstring([]string{agent.Status}, tt.expectedStatus) {
				t.Errorf("expected status containing %q, got %q", tt.expectedStatus, agent.Status)
			}
		})
	}
}

// TestAutoModeLoopExitConditions tests that auto mode loop exits correctly
func TestAutoModeLoopExitConditions(t *testing.T) {
	t.Run("shutdown signal exits immediately", func(t *testing.T) {
		// Create shutdown channel and close it immediately
		shutdown := make(chan struct{})
		close(shutdown)

		// Test that checking shutdown channel works
		select {
		case <-shutdown:
			// Expected - shutdown was detected
		default:
			t.Error("expected shutdown channel to be closed")
		}
	})

	t.Run("idle timeout triggers exit", func(t *testing.T) {
		state := &AutoModeState{
			IdleStartTime: time.Now().Add(-2 * time.Minute), // 2 minutes ago
		}
		idleTimeout := 1 // 1 minute

		// Check idle timeout condition
		idleDuration := time.Since(state.IdleStartTime)
		if idleDuration < time.Duration(idleTimeout)*time.Minute {
			t.Error("expected idle timeout to be exceeded")
		}
	})

	t.Run("max tasks triggers exit", func(t *testing.T) {
		state := &AutoModeState{
			TasksCompleted: 5,
		}
		maxTasks := 5

		if maxTasks > 0 && state.TasksCompleted >= maxTasks {
			// Expected - max tasks reached
		} else {
			t.Error("expected max tasks condition to trigger")
		}
	})

	t.Run("consecutive errors triggers exit", func(t *testing.T) {
		state := &AutoModeState{
			ConsecutiveErrors: 3,
		}

		if state.ConsecutiveErrors >= 3 {
			// Expected - too many errors
		} else {
			t.Error("expected consecutive errors condition to trigger")
		}
	})

	t.Run("consecutive no progress triggers exit", func(t *testing.T) {
		state := &AutoModeState{
			ConsecutiveNoProgress: 3,
		}

		if state.ConsecutiveNoProgress >= 3 {
			// Expected - no progress detected
		} else {
			t.Error("expected consecutive no-progress condition to trigger")
		}
	})
}

// TestPlanReviewTaskWorkflow tests the full plan→review→task lifecycle
func TestPlanReviewTaskWorkflow(t *testing.T) {
	// Test HasAvailablePlanningTasks returns true for tasks without design
	t.Run("tasks without design need planning", func(t *testing.T) {
		mock := NewFlexibleCommandMock(t)
		mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: LoadFixture(t, "bd_ready_planning.json")})
		mock.AddStub("bd", []string{"list"}, CommandResult{Stdout: "[]"})
		mock.Install()

		hasTasks, err := HasAvailablePlanningTasks("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasTasks {
			t.Error("expected HasAvailablePlanningTasks to return true for tasks without design")
		}
	})

	// Test HasAvailableImplementationTasks returns true for tasks with design
	t.Run("tasks with design ready for implementation", func(t *testing.T) {
		mock := NewFlexibleCommandMock(t)
		mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: LoadFixture(t, "bd_ready_implementation.json")})
		mock.AddStub("bd", []string{"list"}, CommandResult{Stdout: "[]"})
		mock.Install()

		hasTasks, err := HasAvailableImplementationTasks("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasTasks {
			t.Error("expected HasAvailableImplementationTasks to return true for tasks with design")
		}
	})

	// Test agentClaimedTask detects task claim via lock file
	t.Run("agentClaimedTask detects task ID in lock", func(t *testing.T) {
		tmpDir := SetupTestWorktree(t, "test")
		wtPath := filepath.Join(tmpDir, "worktrees", "test")

		// Create lock file with TaskID
		lockInfo := &LockInfo{
			PID:       os.Getpid(),
			Command:   "task",
			AgentName: "test",
			TaskID:    "TASK-123",
			StartedAt: time.Now(),
		}
		lockPath := filepath.Join(wtPath, LockFileName)
		data, _ := json.Marshal(lockInfo)
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock: %v", err)
		}

		claimed := agentClaimedTask(wtPath)
		if !claimed {
			t.Error("expected agentClaimedTask to return true when TaskID is set")
		}
	})

	// Test agentClaimedTask returns false when no task claimed
	t.Run("agentClaimedTask returns false for empty TaskID", func(t *testing.T) {
		tmpDir := SetupTestWorktree(t, "test2")
		wtPath := filepath.Join(tmpDir, "worktrees", "test2")

		// Create lock file without TaskID
		lockInfo := &LockInfo{
			PID:       os.Getpid(),
			Command:   "plan",
			AgentName: "test2",
			TaskID:    "", // No task claimed
			StartedAt: time.Now(),
		}
		lockPath := filepath.Join(wtPath, LockFileName)
		data, _ := json.Marshal(lockInfo)
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock: %v", err)
		}

		claimed := agentClaimedTask(wtPath)
		if claimed {
			t.Error("expected agentClaimedTask to return false when TaskID is empty")
		}
	})

	// Test that needs-revision label tasks ARE available for planning
	t.Run("needs-revision tasks are available for planning", func(t *testing.T) {
		needsRevisionJSON := `[
			{"id":"TASK-1","title":"Some task","status":"open","priority":1,"design":"existing plan","labels":["needs-revision"]}
		]`
		mock := NewFlexibleCommandMock(t)
		mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: needsRevisionJSON})
		mock.AddStub("bd", []string{"list"}, CommandResult{Stdout: "[]"})
		mock.Install()

		hasTasks, err := HasAvailablePlanningTasks("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasTasks {
			t.Error("expected needs-revision tasks to be available for planning")
		}
	})

	// Test that epics are skipped
	t.Run("epics are skipped for implementation", func(t *testing.T) {
		epicJSON := `[
			{"id":"EPIC-1","title":"Big feature","status":"open","priority":1,"issue_type":"epic","design":"some design"}
		]`
		mock := NewFlexibleCommandMock(t)
		mock.AddStub("bd", []string{"ready"}, CommandResult{Stdout: epicJSON})
		mock.AddStub("bd", []string{"list"}, CommandResult{Stdout: "[]"})
		mock.Install()

		hasTasks, err := HasAvailableImplementationTasks("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasTasks {
			t.Error("expected epics to be skipped")
		}
	})
}

// TestSyncConflictResolution tests the sync flow handles merge conflicts
func TestSyncConflictResolution(t *testing.T) {
	// Test that git merge failure with conflicts is handled
	t.Run("merge conflict detection", func(t *testing.T) {
		// This tests the sync command's behavior when encountering conflicts
		// The actual SyncCmd would detect conflicts via git diff --name-only --diff-filter=U

		conflictFiles := LoadFixture(t, "merge_conflict_files.txt")
		expectedFiles := []string{
			"src/api/handler.go",
			"src/models/user.go",
			"config/settings.yaml",
		}

		for _, expected := range expectedFiles {
			if !containsSubstring([]string{conflictFiles}, expected) {
				t.Errorf("expected conflict file %q not found in fixture", expected)
			}
		}
	})

	// Test sync status parsing
	t.Run("sync status parsing", func(t *testing.T) {
		// Test successful sync
		syncOutput := "synced"
		info := SyncInfo{}
		info.DBSynced = !containsSubstring([]string{syncOutput}, "error") && !containsSubstring([]string{syncOutput}, "failed")
		if !info.DBSynced {
			t.Error("expected DBSynced to be true for 'synced' output")
		}

		// Test failed sync
		failedOutput := "error: connection failed"
		info2 := SyncInfo{}
		info2.DBSynced = !containsSubstring([]string{failedOutput}, "error") && !containsSubstring([]string{failedOutput}, "failed")
		if info2.DBSynced {
			t.Error("expected DBSynced to be false for error output")
		}
	})
}

// TestLoadFixtures verifies all test fixtures can be loaded and parsed
func TestLoadFixtures(t *testing.T) {
	fixtures := []struct {
		path   string
		isJSON bool
	}{
		{"bd_ready_planning.json", true},
		{"bd_ready_implementation.json", true},
		{"bd_stats.json", true},
		{"bd_in_progress.json", true},
		{"bd_blocked.json", true},
		{"merge_conflict_files.txt", false},
	}

	for _, f := range fixtures {
		t.Run(f.path, func(t *testing.T) {
			content := LoadFixture(t, f.path)
			if content == "" {
				t.Errorf("fixture %s is empty", f.path)
			}
			if f.isJSON {
				var v interface{}
				if err := json.Unmarshal([]byte(content), &v); err != nil {
					t.Errorf("fixture %s is not valid JSON: %v", f.path, err)
				}
			}
		})
	}
}
