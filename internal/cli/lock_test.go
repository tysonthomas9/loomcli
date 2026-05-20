package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

	err = UpdateLockTask(tmpDir, "loom-123", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	info, _, _ := CheckLock(tmpDir)
	if info.TaskID != "loom-123" {
		t.Errorf("Expected TaskID 'loom-123', got '%s'", info.TaskID)
	}
	if info.TaskTitle != "Test Task" {
		t.Errorf("Expected TaskTitle 'Test Task', got '%s'", info.TaskTitle)
	}
}

func TestUpdateLockTaskNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Should fail when no lock exists
	err := UpdateLockTask(tmpDir, "loom-123", "Test Task")
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
	UpdateLockTask(tmpDir, "loom-123", "Test Task")
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
	// Implementation agent without TaskID is not actually working on anything,
	// so the status surfaces as idle rather than "working: ...".
	if !strings.HasPrefix(status, "idle (") {
		t.Errorf("Expected 'idle (' prefix, got '%s'", status)
	}
}

func TestGetLockStatus_PlanningAgentWithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	UpdateLockTask(tmpDir, "loom-test", "Test Task")
	status := GetLockStatus(tmpDir)

	// Should contain the task ID; the status prefix depends on issue status.
	if !strings.Contains(status, "loom-test") {
		t.Errorf("Expected status to contain 'loom-test', got '%s'", status)
	}
}

func TestGetLockStatus_WorkingAgentWithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "nova")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	UpdateLockTask(tmpDir, "loom-test", "Test Task")
	status := GetLockStatus(tmpDir)

	// Should contain the task ID
	if !strings.Contains(status, "loom-test") {
		t.Errorf("Expected status to contain 'loom-test', got '%s'", status)
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
	UpdateLockTask(tmpDir, "loom-123", "Test Task")
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
// ClearLockTaskID Tests
// ============================================================================

func TestClearLockTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set a task first
	err = UpdateLockTask(tmpDir, "loom-123", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	// Verify task is set
	info, _ := ReadLockFile(tmpDir)
	if info.TaskID != "loom-123" {
		t.Fatalf("Expected TaskID 'loom-123', got '%s'", info.TaskID)
	}

	// Clear task ID
	err = ClearLockTaskID(tmpDir)
	if err != nil {
		t.Fatalf("ClearLockTaskID failed: %v", err)
	}

	// Verify cleared
	info, _ = ReadLockFile(tmpDir)
	if info.TaskID != "" {
		t.Errorf("Expected empty TaskID after clear, got '%s'", info.TaskID)
	}
	if info.TaskTitle != "" {
		t.Errorf("Expected empty TaskTitle after clear, got '%s'", info.TaskTitle)
	}
	if !info.TaskStartedAt.IsZero() {
		t.Errorf("Expected zero TaskStartedAt after clear, got '%v'", info.TaskStartedAt)
	}

	// Other fields should be preserved
	if info.PID != os.Getpid() {
		t.Errorf("PID should be preserved, got %d", info.PID)
	}
	if info.Command != "plan" {
		t.Errorf("Command should be preserved, got '%s'", info.Command)
	}
	if info.AgentName != "falcon" {
		t.Errorf("AgentName should be preserved, got '%s'", info.AgentName)
	}
}

func TestClearLockTaskIDNoLock(t *testing.T) {
	tmpDir := t.TempDir()

	err := ClearLockTaskID(tmpDir)
	if err == nil {
		t.Error("Expected error when clearing non-existent lock")
	}
}

func TestClearLockTaskIDDifferentPID(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file owned by a different process
	otherLock := `{"pid":999999999,"command":"plan","agent_name":"falcon","started_at":"2024-01-01T00:00:00Z","task_id":"loom-123","task_title":"Test"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(otherLock), 0644); err != nil {
		t.Fatalf("failed to write lock: %v", err)
	}

	err := ClearLockTaskID(tmpDir)
	if err == nil {
		t.Error("Expected error when clearing lock owned by different PID")
	}
	if !strings.Contains(err.Error(), "different process") {
		t.Errorf("Expected 'different process' in error, got: %v", err)
	}

	// Verify task ID was NOT cleared
	info, _ := ReadLockFile(tmpDir)
	if info.TaskID != "loom-123" {
		t.Errorf("TaskID should not be cleared when PID doesn't match, got '%s'", info.TaskID)
	}
}

func TestClearLockTaskIDInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{invalid json}`), 0644); err != nil {
		t.Fatalf("failed to write lock: %v", err)
	}

	err := ClearLockTaskID(tmpDir)
	if err == nil {
		t.Error("Expected error for invalid JSON lock file")
	}
	if !strings.Contains(err.Error(), "invalid lock file") {
		t.Errorf("Expected 'invalid lock file' in error, got: %v", err)
	}
}

func TestClearLockTaskIDAlreadyEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Clear when already empty — should succeed without error
	err = ClearLockTaskID(tmpDir)
	if err != nil {
		t.Fatalf("ClearLockTaskID should succeed when TaskID is already empty: %v", err)
	}

	info, _ := ReadLockFile(tmpDir)
	if info.TaskID != "" {
		t.Errorf("Expected empty TaskID, got '%s'", info.TaskID)
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
				lockData := `{"pid":12345,"command":"task","agent_name":"nova","started_at":"2024-01-01T00:00:00Z","task_id":"loom-123","task_title":"Test Task","task_started_at":"2024-01-01T01:00:00Z","state":"active"}`
				os.WriteFile(filepath.Join(tmpDir, LockFileName), []byte(lockData), 0644)
			},
			wantErr: false,
			checkInfo: func(t *testing.T, info *LockInfo) {
				if info.TaskID != "loom-123" {
					t.Errorf("expected task_id 'loom-123', got %q", info.TaskID)
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
// TOCTOU Race Condition Tests
// ============================================================================

func TestAcquireLockStale_ConcurrentRetry(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, LockFileName)

	staleLock := `{"pid":999999999,"command":"stale","agent_name":"dead-agent","started_at":"2024-01-01T00:00:00Z"}`
	if err := os.WriteFile(lockPath, []byte(staleLock), 0644); err != nil {
		t.Fatalf("failed to write stale lock: %v", err)
	}

	go func() {
		for {
			_, err := os.Stat(lockPath)
			if os.IsNotExist(err) {
				secondStale := `{"pid":999999998,"command":"stale2","agent_name":"dead-agent-2","started_at":"2024-01-01T00:00:00Z"}`
				os.WriteFile(lockPath, []byte(secondStale), 0644)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	err := AcquireLock(tmpDir, "new-command", "new-agent")
	if err != nil {
		t.Fatalf("AcquireLock should succeed after retrying stale lock race: %v", err)
	}
	defer ReleaseLock(tmpDir)

	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running process after acquiring lock")
	}
	if info.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
}

func TestAcquireLockStale_RetryFindsLiveAgent(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, LockFileName)

	liveLock := fmt.Sprintf(`{"pid":%d,"command":"live-cmd","agent_name":"live-agent","started_at":"2024-01-01T00:00:00Z"}`, os.Getpid())
	if err := os.WriteFile(lockPath, []byte(liveLock), 0644); err != nil {
		t.Fatalf("failed to write live lock: %v", err)
	}

	err := AcquireLock(tmpDir, "new-command", "new-agent")
	if err == nil {
		ReleaseLock(tmpDir)
		t.Fatal("AcquireLock should fail when lock is held by live agent")
	}
	if !strings.Contains(err.Error(), "agent already running") {
		t.Errorf("expected 'agent already running' error, got: %v", err)
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
	err := UpdateLockTask(tmpDir, "loom-123", "Test Task")
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
		issue      *backend.IssueDetailData
		issueErr   error
		wantStatus string
	}{
		{
			name:       "closed task",
			taskID:     "loom-123",
			issue:      &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-123", Status: "closed"}},
			wantStatus: "closed",
		},
		{
			name:       "review task maps to needs_review",
			taskID:     "loom-456",
			issue:      &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-456", Status: "review"}},
			wantStatus: "needs_review",
		},
		{
			name:       "open task",
			taskID:     "loom-789",
			issue:      &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-789", Status: "open"}},
			wantStatus: "open",
		},
		{
			name:       "in progress task",
			taskID:     "loom-101",
			issue:      &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-101", Status: "in_progress"}},
			wantStatus: "in_progress",
		},
		{
			name:       "GetIssue returns error",
			taskID:     "loom-error",
			issue:      nil,
			issueErr:   errors.New("not found"),
			wantStatus: "",
		},
		{
			name:       "GetIssue returns nil issue",
			taskID:     "loom-empty",
			issue:      nil,
			issueErr:   nil,
			wantStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockIssueBackend{
				GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
					return tt.issue, tt.issueErr
				},
			}
			setDefaultIssueBackend(mock)
			t.Cleanup(func() { setDefaultIssueBackend(nil) })

			status := getTaskStatus(tt.taskID)
			if status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, status)
			}
		})
	}
}

func TestGetTaskStatus_ReviewStatus(t *testing.T) {
	tests := []struct {
		name       string
		rawStatus  string
		wantStatus string
	}{
		{
			name:       "review status maps to needs_review",
			rawStatus:  "review",
			wantStatus: "needs_review",
		},
		{
			name:       "open status unchanged",
			rawStatus:  "open",
			wantStatus: "open",
		},
		{
			name:       "in_progress status unchanged",
			rawStatus:  "in_progress",
			wantStatus: "in_progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockIssueBackend{
				GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
					return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: tt.rawStatus}}, nil
				},
			}
			setDefaultIssueBackend(mock)
			t.Cleanup(func() { setDefaultIssueBackend(nil) })

			status := getTaskStatus("loom-test")
			if status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, status)
			}
		})
	}
}

func TestGetLockStatusTaskOutcomeBranches(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		status     string
		wantPrefix string
	}{
		{name: "closed", command: "task", status: "closed", wantPrefix: "done: loom-status"},
		{name: "planner review", command: "plan", status: "review", wantPrefix: "review: loom-status"},
		{name: "worker review stays working", command: "task", status: "review", wantPrefix: "working: loom-status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := AcquireLock(dir, tt.command, "nova"); err != nil {
				t.Fatalf("AcquireLock: %v", err)
			}
			t.Cleanup(func() { _ = ReleaseLock(dir) })
			if err := UpdateLockTask(dir, "loom-status", "title"); err != nil {
				t.Fatalf("UpdateLockTask: %v", err)
			}
			setDefaultIssueBackend(&MockIssueBackend{
				GetFn: func(context.Context, string) (*backend.IssueDetailData, error) {
					return &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-status", Status: tt.status}}, nil
				},
			})
			t.Cleanup(func() { setDefaultIssueBackend(nil) })

			if got := GetLockStatus(dir); !strings.HasPrefix(got, tt.wantPrefix) {
				t.Fatalf("GetLockStatus = %q, want prefix %q", got, tt.wantPrefix)
			}
		})
	}
}

func TestAcquireLockRetryReplacesStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockFileName)
	stale := LockInfo{
		PID:       -1,
		Command:   "task",
		AgentName: "old",
		StartedAt: time.Now().Add(-time.Hour),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale lock: %v", err)
	}
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	if err := AcquireLock(dir, "task", "new"); err != nil {
		t.Fatalf("AcquireLock over stale lock: %v", err)
	}
	t.Cleanup(func() { _ = ReleaseLock(dir) })
	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.AgentName != "new" || info.PID != os.Getpid() {
		t.Fatalf("lock after retry = %+v", info)
	}
}

// ============================================================================
// Single-Task Mode Lock State Pattern Tests
// ============================================================================

// TestSingleTaskModeLockStatePattern simulates the single-task mode workflow
// for command="plan": AcquireLock -> UpdateLockState(StateActive) -> verify.
func TestSingleTaskModeLockStatePattern(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: AcquireLock (as plan.go does)
	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Step 2: UpdateLockState to active (the new line added in plan.go)
	err = UpdateLockState(tmpDir, StateActive)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	// Step 3: Verify lock state is StateActive via CheckLock
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running process")
	}
	if info.State != StateActive {
		t.Errorf("expected State %q, got %q", StateActive, info.State)
	}

	// Step 4: Verify other lock fields are preserved
	if info.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if info.Command != "plan" {
		t.Errorf("expected Command 'plan', got %q", info.Command)
	}
	if info.AgentName != "falcon" {
		t.Errorf("expected AgentName 'falcon', got %q", info.AgentName)
	}

	// Also verify via ReadLockFile
	readInfo, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if readInfo.State != StateActive {
		t.Errorf("ReadLockFile: expected State %q, got %q", StateActive, readInfo.State)
	}
	if readInfo.PID != os.Getpid() {
		t.Errorf("ReadLockFile: expected PID %d, got %d", os.Getpid(), readInfo.PID)
	}
	if readInfo.Command != "plan" {
		t.Errorf("ReadLockFile: expected Command 'plan', got %q", readInfo.Command)
	}
	if readInfo.AgentName != "falcon" {
		t.Errorf("ReadLockFile: expected AgentName 'falcon', got %q", readInfo.AgentName)
	}
}

// TestSingleTaskModeLockStatePattern_Task simulates the single-task mode workflow
// for command="task": AcquireLock -> UpdateLockState(StateActive) -> verify.
func TestSingleTaskModeLockStatePattern_Task(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: AcquireLock (as task.go does)
	err := AcquireLock(tmpDir, "task", "nova")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Step 2: UpdateLockState to active (the new line added in task.go)
	err = UpdateLockState(tmpDir, StateActive)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	// Step 3: Verify lock state is StateActive via CheckLock
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running process")
	}
	if info.State != StateActive {
		t.Errorf("expected State %q, got %q", StateActive, info.State)
	}

	// Step 4: Verify other lock fields are preserved
	if info.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if info.Command != "task" {
		t.Errorf("expected Command 'task', got %q", info.Command)
	}
	if info.AgentName != "nova" {
		t.Errorf("expected AgentName 'nova', got %q", info.AgentName)
	}

	// Also verify via ReadLockFile
	readInfo, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if readInfo.State != StateActive {
		t.Errorf("ReadLockFile: expected State %q, got %q", StateActive, readInfo.State)
	}
	if readInfo.PID != os.Getpid() {
		t.Errorf("ReadLockFile: expected PID %d, got %d", os.Getpid(), readInfo.PID)
	}
	if readInfo.Command != "task" {
		t.Errorf("ReadLockFile: expected Command 'task', got %q", readInfo.Command)
	}
	if readInfo.AgentName != "nova" {
		t.Errorf("ReadLockFile: expected AgentName 'nova', got %q", readInfo.AgentName)
	}
}

// TestSingleTaskModeLockStatePattern_Agent simulates the single-task mode workflow
// for command="agent": AcquireLock -> UpdateLockState(StateActive) -> verify.
func TestSingleTaskModeLockStatePattern_Agent(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: AcquireLock (as agent_cmd.go does)
	err := AcquireLock(tmpDir, "agent", "spark")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Step 2: UpdateLockState to active (the new line added in agent_cmd.go)
	err = UpdateLockState(tmpDir, StateActive)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	// Step 3: Verify lock state is StateActive via CheckLock
	info, running, err := CheckLock(tmpDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running process")
	}
	if info.State != StateActive {
		t.Errorf("expected State %q, got %q", StateActive, info.State)
	}

	// Step 4: Verify other lock fields are preserved
	if info.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if info.Command != "agent" {
		t.Errorf("expected Command 'agent', got %q", info.Command)
	}
	if info.AgentName != "spark" {
		t.Errorf("expected AgentName 'spark', got %q", info.AgentName)
	}

	// Also verify via ReadLockFile
	readInfo, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if readInfo.State != StateActive {
		t.Errorf("ReadLockFile: expected State %q, got %q", StateActive, readInfo.State)
	}
	if readInfo.PID != os.Getpid() {
		t.Errorf("ReadLockFile: expected PID %d, got %d", os.Getpid(), readInfo.PID)
	}
	if readInfo.Command != "agent" {
		t.Errorf("ReadLockFile: expected Command 'agent', got %q", readInfo.Command)
	}
	if readInfo.AgentName != "spark" {
		t.Errorf("ReadLockFile: expected AgentName 'spark', got %q", readInfo.AgentName)
	}
}

func TestUpdateLockClaudeSessionID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "agent1")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	if err := UpdateLockClaudeSessionID(tmpDir, "sess-abc-123"); err != nil {
		t.Fatalf("UpdateLockClaudeSessionID: %v", err)
	}

	info, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.ClaudeSessionID != "sess-abc-123" {
		t.Errorf("ClaudeSessionID = %q, want %q", info.ClaudeSessionID, "sess-abc-123")
	}
}

func TestUpdateLockClaudeSessionID_PIDMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	otherLock := `{"pid":999999999,"command":"task","agent_name":"agent1","started_at":"2024-01-01T00:00:00Z"}`
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(otherLock), 0644); err != nil {
		t.Fatalf("failed to write lock: %v", err)
	}

	err := UpdateLockClaudeSessionID(tmpDir, "sess-abc-123")
	if err == nil {
		t.Error("Expected error when PID doesn't match")
	}
	if !strings.Contains(err.Error(), "different process") {
		t.Errorf("Expected 'different process' in error, got: %v", err)
	}
}

func TestClearLockClaudeSessionID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "task", "agent1")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set then clear
	if err := UpdateLockClaudeSessionID(tmpDir, "sess-abc-123"); err != nil {
		t.Fatalf("UpdateLockClaudeSessionID: %v", err)
	}
	if err := ClearLockClaudeSessionID(tmpDir); err != nil {
		t.Fatalf("ClearLockClaudeSessionID: %v", err)
	}

	info, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID after clear = %q, want empty", info.ClaudeSessionID)
	}

	// Verify omitempty: field should not appear in JSON
	data, _ := os.ReadFile(filepath.Join(tmpDir, LockFileName))
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, exists := raw["claude_session_id"]; exists {
		t.Error("claude_session_id should not be in JSON after clear (omitempty)")
	}
}
