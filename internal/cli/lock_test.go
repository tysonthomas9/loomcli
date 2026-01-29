package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLock(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "test", "test-agent")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Verify lock file exists with correct content
	lockPath := filepath.Join(tmpDir, LockFileName)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("Lock file not created: %v", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("Invalid lock JSON: %v", err)
	}

	if info.PID != os.Getpid() {
		t.Errorf("Expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if info.Command != "test" {
		t.Errorf("Expected command 'test', got '%s'", info.Command)
	}
	if info.AgentName != "test-agent" {
		t.Errorf("Expected agent 'test-agent', got '%s'", info.AgentName)
	}
}

func TestAcquireLockFailsWhenLocked(t *testing.T) {
	tmpDir := t.TempDir()

	// Acquire first lock
	err := AcquireLock(tmpDir, "first", "agent1")
	if err != nil {
		t.Fatalf("First AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Second acquire should fail
	err = AcquireLock(tmpDir, "second", "agent2")
	if err == nil {
		t.Error("Expected error when lock already held")
	}
}

func TestCheckLockNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// No lock exists
	_, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if running {
		t.Error("Expected no running process when no lock exists")
	}
}

func TestCheckLockWithLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock
	err := AcquireLock(tmpDir, "test", "agent")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Lock exists and running
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("Expected running process")
	}
	if info.AgentName != "agent" {
		t.Errorf("Expected agent 'agent', got '%s'", info.AgentName)
	}
}

func TestUpdateLockTask(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	err = UpdateLockTask(tmpDir, "bd-123", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	info, _, _ := CheckLock(tmpDir)
	if info.TaskID != "bd-123" {
		t.Errorf("Expected TaskID 'bd-123', got '%s'", info.TaskID)
	}
	if info.TaskTitle != "Test Task" {
		t.Errorf("Expected TaskTitle 'Test Task', got '%s'", info.TaskTitle)
	}
}

func TestUpdateLockTaskNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Should fail when no lock exists
	err := UpdateLockTask(tmpDir, "bd-123", "Test Task")
	if err == nil {
		t.Error("Expected error when updating non-existent lock")
	}
}

func TestReleaseLock(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "test", "agent")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	err = ReleaseLock(tmpDir)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	lockPath := filepath.Join(tmpDir, LockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Lock file should be removed after release")
	}
}

func TestReleaseLockNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Should not error when no lock exists
	err := ReleaseLock(tmpDir)
	if err != nil {
		t.Errorf("ReleaseLock should not error when no lock: %v", err)
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Current process should be running
	if !IsProcessRunning(os.Getpid()) {
		t.Error("Current process should be running")
	}

	// Non-existent PID (very high number unlikely to exist)
	if IsProcessRunning(999999999) {
		t.Error("Non-existent PID should not be running")
	}

	// PID 0 is special (kernel), should return false for normal check
	// Note: This test might behave differently on some systems
}

func TestGetLockStatus(t *testing.T) {
	tmpDir := t.TempDir()

	// No lock - should return empty
	status := GetLockStatus(tmpDir)
	if status != "" {
		t.Errorf("Expected empty status when no lock, got '%s'", status)
	}

	// With lock
	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	status = GetLockStatus(tmpDir)
	if status == "" {
		t.Error("Expected non-empty status when lock exists")
	}

	// With task
	UpdateLockTask(tmpDir, "bd-123", "Test Task")
	status = GetLockStatus(tmpDir)
	if status == "" {
		t.Error("Expected non-empty status with task")
	}
}

func TestGetLockStatus_PlanningAgentNoTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	status := GetLockStatus(tmpDir)
	// Planning agent without TaskID should show "planning: ..."
	if !strings.HasPrefix(status, "planning: ...") {
		t.Errorf("Expected 'planning: ...' prefix, got '%s'", status)
	}
}

func TestGetLockStatus_WorkingAgentNoTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "nova")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	status := GetLockStatus(tmpDir)
	// Implementation agent without TaskID should show "working: ..."
	if !strings.HasPrefix(status, "working: ...") {
		t.Errorf("Expected 'working: ...' prefix, got '%s'", status)
	}
}

func TestGetLockStatus_PlanningAgentWithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	UpdateLockTask(tmpDir, "bd-test", "Test Task")
	status := GetLockStatus(tmpDir)

	// Should contain the task ID (actual prefix depends on task status from bd)
	if !strings.Contains(status, "bd-test") {
		t.Errorf("Expected status to contain 'bd-test', got '%s'", status)
	}
}

func TestGetLockStatus_WorkingAgentWithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "nova")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	UpdateLockTask(tmpDir, "bd-test", "Test Task")
	status := GetLockStatus(tmpDir)

	// Should contain the task ID
	if !strings.Contains(status, "bd-test") {
		t.Errorf("Expected status to contain 'bd-test', got '%s'", status)
	}
}

func TestGetLockStatus_DurationFormat(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "nova")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	status := GetLockStatus(tmpDir)
	// Should include duration in parentheses
	if !strings.Contains(status, "(") || !strings.Contains(status, ")") {
		t.Errorf("Expected status to include duration in parentheses, got '%s'", status)
	}
}

func TestUpdateLockState(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Update to idle state
	err = UpdateLockState(tmpDir, StateIdle)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	info, _, _ := CheckLock(tmpDir)
	if info.State != StateIdle {
		t.Errorf("Expected State '%s', got '%s'", StateIdle, info.State)
	}

	// Update to active state
	err = UpdateLockState(tmpDir, StateActive)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	info, _, _ = CheckLock(tmpDir)
	if info.State != StateActive {
		t.Errorf("Expected State '%s', got '%s'", StateActive, info.State)
	}
}

func TestUpdateLockStateNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Should fail when no lock exists
	err := UpdateLockState(tmpDir, StateIdle)
	if err == nil {
		t.Error("Expected error when updating non-existent lock")
	}
}

func TestGetLockStatus_IdleState(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set idle state
	err = UpdateLockState(tmpDir, StateIdle)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	status := GetLockStatus(tmpDir)
	// Should show "idle" instead of "planning: ..."
	if !strings.HasPrefix(status, "idle") {
		t.Errorf("Expected 'idle' prefix, got '%s'", status)
	}
}

func TestGetLockStatus_ActiveStateShowsCommand(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set active state
	err = UpdateLockState(tmpDir, StateActive)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	status := GetLockStatus(tmpDir)
	// Active state without TaskID should show "planning: ..." (normal behavior)
	if !strings.HasPrefix(status, "planning: ...") {
		t.Errorf("Expected 'planning: ...' prefix for active state, got '%s'", status)
	}
}

func TestGetLockStatus_IdleOverridesTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set task then idle state
	UpdateLockTask(tmpDir, "bd-123", "Test Task")
	err = UpdateLockState(tmpDir, StateIdle)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	status := GetLockStatus(tmpDir)
	// Idle state should take precedence
	if !strings.HasPrefix(status, "idle") {
		t.Errorf("Expected 'idle' prefix (should override task), got '%s'", status)
	}
}

// ============================================================================
// ReadLockFile Tests (NEW - no coverage existed before)
// ============================================================================

func TestReadLockFile(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(tmpDir string) // setup before test
		wantErr   bool
		checkInfo func(t *testing.T, info *LockInfo)
	}{
		{
			name: "valid lock file",
			setup: func(tmpDir string) {
				lockData := `{"pid":12345,"command":"plan","agent_name":"falcon","started_at":"2024-01-01T00:00:00Z"}`
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(lockData), 0644)
			},
			wantErr: false,
			checkInfo: func(t *testing.T, info *LockInfo) {
				if info.PID != 12345 {
					t.Errorf("expected PID 12345, got %d", info.PID)
				}
				if info.Command != "plan" {
					t.Errorf("expected command 'plan', got %q", info.Command)
				}
				if info.AgentName != "falcon" {
					t.Errorf("expected agent_name 'falcon', got %q", info.AgentName)
				}
			},
		},
		{
			name: "valid lock file with all fields",
			setup: func(tmpDir string) {
				lockData := `{"pid":12345,"command":"task","agent_name":"nova","started_at":"2024-01-01T00:00:00Z","task_id":"bd-123","task_title":"Test Task","task_started_at":"2024-01-01T01:00:00Z","state":"active"}`
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(lockData), 0644)
			},
			wantErr: false,
			checkInfo: func(t *testing.T, info *LockInfo) {
				if info.TaskID != "bd-123" {
					t.Errorf("expected task_id 'bd-123', got %q", info.TaskID)
				}
				if info.TaskTitle != "Test Task" {
					t.Errorf("expected task_title 'Test Task', got %q", info.TaskTitle)
				}
				if info.State != "active" {
					t.Errorf("expected state 'active', got %q", info.State)
				}
			},
		},
		{
			name:    "file not found",
			setup:   func(tmpDir string) {}, // no file created
			wantErr: true,
		},
		{
			name: "invalid JSON",
			setup: func(tmpDir string) {
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(`{invalid json`), 0644)
			},
			wantErr: true,
		},
		{
			name: "empty file",
			setup: func(tmpDir string) {
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(``), 0644)
			},
			wantErr: true,
		},
		{
			name: "truncated JSON",
			setup: func(tmpDir string) {
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(`{"pid":123,"command":"plan`), 0644)
			},
			wantErr: true,
		},
		{
			name: "unicode in fields",
			setup: func(tmpDir string) {
				lockData := `{"pid":12345,"command":"plan","agent_name":"鷹","started_at":"2024-01-01T00:00:00Z","task_title":"テストタスク"}`
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(lockData), 0644)
			},
			wantErr: false,
			checkInfo: func(t *testing.T, info *LockInfo) {
				if info.AgentName != "鷹" {
					t.Errorf("expected unicode agent_name '鷹', got %q", info.AgentName)
				}
				if info.TaskTitle != "テストタスク" {
					t.Errorf("expected unicode task_title 'テストタスク', got %q", info.TaskTitle)
				}
			},
		},
		{
			name: "zero PID in lock file",
			setup: func(tmpDir string) {
				lockData := `{"pid":0,"command":"plan","agent_name":"falcon","started_at":"2024-01-01T00:00:00Z"}`
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(lockData), 0644)
			},
			wantErr: false,
			checkInfo: func(t *testing.T, info *LockInfo) {
				if info.PID != 0 {
					t.Errorf("expected PID 0, got %d", info.PID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tt.setup(tmpDir)

			info, err := ReadLockFile(tmpDir)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkInfo != nil {
				tt.checkInfo(t, info)
			}
		})
	}
}

// ============================================================================
// Stale Lock Edge Cases
// ============================================================================

func TestAcquireLockStale(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a stale lock file with a non-existent PID
	staleLock := `{"pid":999999999,"command":"stale","agent_name":"dead-agent","started_at":"2024-01-01T00:00:00Z"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(staleLock), 0644); err != nil {
		t.Fatalf("failed to write stale lock: %v", err)
	}

	// AcquireLock should detect stale lock, remove it, and succeed
	err := AcquireLock(tmpDir, "new-command", "new-agent")
	if err != nil {
		t.Fatalf("AcquireLock should succeed with stale lock: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Verify new lock was created with correct info
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running process after acquiring lock")
	}
	if info.Command != "new-command" {
		t.Errorf("expected command 'new-command', got %q", info.Command)
	}
	if info.AgentName != "new-agent" {
		t.Errorf("expected agent 'new-agent', got %q", info.AgentName)
	}
}

func TestCheckLockStale(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a stale lock file with a non-existent PID
	staleLock := `{"pid":999999999,"command":"stale","agent_name":"dead-agent","started_at":"2024-01-01T00:00:00Z"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(staleLock), 0644); err != nil {
		t.Fatalf("failed to write stale lock: %v", err)
	}

	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}

	// Should return lock info but running=false
	if info == nil {
		t.Fatal("expected lock info, got nil")
	}
	if running {
		t.Error("expected running=false for stale lock")
	}
	if info.PID != 999999999 {
		t.Errorf("expected PID 999999999, got %d", info.PID)
	}
}

// ============================================================================
// Invalid JSON Edge Cases
// ============================================================================

func TestCheckLockInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file with invalid JSON
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{invalid json}`), 0644); err != nil {
		t.Fatalf("failed to write invalid lock: %v", err)
	}

	// Should return (nil, false, nil) - treat as no lock
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Errorf("CheckLock should not return error for invalid JSON: %v", err)
	}
	if info != nil {
		t.Error("expected nil info for invalid JSON")
	}
	if running {
		t.Error("expected running=false for invalid JSON")
	}
}

func TestUpdateLockTaskInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file with invalid JSON
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{invalid json}`), 0644); err != nil {
		t.Fatalf("failed to write invalid lock: %v", err)
	}

	// Should return error about invalid lock file
	err := UpdateLockTask(tmpDir, "bd-123", "Test Task")
	if err == nil {
		t.Error("expected error for invalid JSON lock file")
	}
	if !strings.Contains(err.Error(), "invalid lock file") {
		t.Errorf("expected 'invalid lock file' in error, got: %v", err)
	}
}

func TestUpdateLockStateInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file with invalid JSON
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{invalid json}`), 0644); err != nil {
		t.Fatalf("failed to write invalid lock: %v", err)
	}

	// Should return error about invalid lock file
	err := UpdateLockState(tmpDir, StateActive)
	if err == nil {
		t.Error("expected error for invalid JSON lock file")
	}
	if !strings.Contains(err.Error(), "invalid lock file") {
		t.Errorf("expected 'invalid lock file' in error, got: %v", err)
	}
}

// ============================================================================
// PID Ownership Edge Cases
// ============================================================================

func TestReleaseLockDifferentPID(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file owned by a different (non-existent) process
	otherLock := `{"pid":999999999,"command":"other","agent_name":"other-agent","started_at":"2024-01-01T00:00:00Z"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(otherLock), 0644); err != nil {
		t.Fatalf("failed to write other lock: %v", err)
	}

	// ReleaseLock should NOT remove the file (belongs to different PID)
	err := ReleaseLock(tmpDir)
	if err != nil {
		t.Errorf("ReleaseLock should not error: %v", err)
	}

	// Verify lock file still exists
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file should NOT be removed when owned by different PID")
	}
}

func TestUpdateLockStateDifferentPID(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file owned by a different (non-existent) process
	otherLock := `{"pid":999999999,"command":"other","agent_name":"other-agent","started_at":"2024-01-01T00:00:00Z"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(otherLock), 0644); err != nil {
		t.Fatalf("failed to write other lock: %v", err)
	}

	// UpdateLockState should return error about different process
	err := UpdateLockState(tmpDir, StateActive)
	if err == nil {
		t.Error("expected error when updating lock owned by different PID")
	}
	if !strings.Contains(err.Error(), "different process") {
		t.Errorf("expected 'different process' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "999999999") {
		t.Errorf("expected PID '999999999' in error, got: %v", err)
	}
}

// ============================================================================
// getTaskStatus Tests (Internal function, needs mocking)
// ============================================================================

func TestGetTaskStatus(t *testing.T) {
	tests := []struct {
		name       string
		taskID     string
		bdResponse string
		bdError    error
		wantStatus string
	}{
		{
			name:       "closed task",
			taskID:     "bd-123",
			bdResponse: `[{"title":"Test Task","status":"closed"}]`,
			wantStatus: "closed",
		},
		{
			name:       "need review task",
			taskID:     "bd-456",
			bdResponse: `[{"title":"[Need Review] Design Feature","status":"open"}]`,
			wantStatus: "needs_review",
		},
		{
			name:       "open task",
			taskID:     "bd-789",
			bdResponse: `[{"title":"Test Task","status":"open"}]`,
			wantStatus: "open",
		},
		{
			name:       "in progress task",
			taskID:     "bd-101",
			bdResponse: `[{"title":"Working on feature","status":"in_progress"}]`,
			wantStatus: "in_progress",
		},
		{
			name:       "bd command fails",
			taskID:     "bd-error",
			bdResponse: "",
			bdError:    os.ErrNotExist,
			wantStatus: "",
		},
		{
			name:       "invalid JSON response",
			taskID:     "bd-invalid",
			bdResponse: `{invalid json}`,
			wantStatus: "",
		},
		{
			name:       "empty array response",
			taskID:     "bd-empty",
			bdResponse: `[]`,
			wantStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewCommandMock(t, []CommandStub{
				{
					Name:   "bd",
					Args:   []string{"show", tt.taskID, "--json"},
					Stdout: tt.bdResponse,
					Err:    tt.bdError,
				},
			})
			mock.Install()

			status := getTaskStatus(tt.taskID)
			if status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, status)
			}
		})
	}
}

func TestGetTaskStatus_NeedReviewInTitle(t *testing.T) {
	// Test that [Need Review] detection works in various positions in the title
	tests := []struct {
		name       string
		title      string
		wantStatus string
	}{
		{
			name:       "at start of title",
			title:      "[Need Review] Feature implementation",
			wantStatus: "needs_review",
		},
		{
			name:       "in middle of title",
			title:      "Feature [Need Review] implementation",
			wantStatus: "needs_review",
		},
		{
			name:       "at end of title",
			title:      "Feature implementation [Need Review]",
			wantStatus: "needs_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bdResponse := `[{"title":"` + tt.title + `","status":"open"}]`
			mock := NewCommandMock(t, []CommandStub{
				{
					Name:   "bd",
					Stdout: bdResponse,
				},
			})
			mock.Install()

			status := getTaskStatus("bd-test")
			if status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, status)
			}
		})
	}
}
