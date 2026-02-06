package main

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
