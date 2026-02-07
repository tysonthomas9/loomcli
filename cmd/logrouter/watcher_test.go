package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandToPhase_ReturnsCorrectPhase(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"plan", "planning"},
		{"task", "implementation"},
		{"unknown", "implementation"},
		{"", "implementation"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := commandToPhase(tt.command)
			if got != tt.want {
				t.Errorf("commandToPhase(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestLockWatcher_ParsesLockFileJSONCorrectly(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory for router
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// Create lock file with test data
	lockPath := filepath.Join(tmpDir, ".agent.lock")
	lockInfo := AgentLockInfo{
		PID:       12345,
		Command:   "plan",
		AgentName: "test-agent",
		TaskID:    "task-789",
		TaskTitle: "Test Task",
		State:     "running",
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Create router
	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create watcher - this will do initial read
	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Test readLockFile directly
	parsedInfo, err := watcher.readLockFile()
	if err != nil {
		t.Fatalf("readLockFile failed: %v", err)
	}

	// Verify parsed fields
	if parsedInfo.PID != 12345 {
		t.Errorf("PID = %d, want 12345", parsedInfo.PID)
	}
	if parsedInfo.Command != "plan" {
		t.Errorf("Command = %q, want %q", parsedInfo.Command, "plan")
	}
	if parsedInfo.AgentName != "test-agent" {
		t.Errorf("AgentName = %q, want %q", parsedInfo.AgentName, "test-agent")
	}
	if parsedInfo.TaskID != "task-789" {
		t.Errorf("TaskID = %q, want %q", parsedInfo.TaskID, "task-789")
	}
	if parsedInfo.TaskTitle != "Test Task" {
		t.Errorf("TaskTitle = %q, want %q", parsedInfo.TaskTitle, "Test Task")
	}
	if parsedInfo.State != "running" {
		t.Errorf("State = %q, want %q", parsedInfo.State, "running")
	}
}

func TestLockWatcher_CallsRouterSetTaskWhenLockFileChanges(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory for router
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Create router
	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create watcher (lock file doesn't exist yet)
	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Write lock file with task info
	lockInfo := AgentLockInfo{
		PID:       12345,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "task-abc",
		TaskTitle: "Implementation Task",
		State:     "running",
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Manually trigger readAndUpdateLock (simulating fsnotify event)
	watcher.readAndUpdateLock()

	// Give a moment for file operations to complete
	time.Sleep(50 * time.Millisecond)

	// Verify task log was created at the correct path
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-abc", "implementation.log")
	if _, err := os.Stat(taskLogPath); os.IsNotExist(err) {
		t.Errorf("task log file was not created at %s", taskLogPath)
	}

	// Write to router to verify it goes to task log
	testData := []byte("test task output")
	if _, err := router.Write(testData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Flush to ensure data is written
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify task log content
	taskContent, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "test task output" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "test task output")
	}
}

func TestLockWatcher_InitialReadOnCreation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory for router
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// Create lock file BEFORE creating watcher
	lockPath := filepath.Join(tmpDir, ".agent.lock")
	lockInfo := AgentLockInfo{
		PID:       12345,
		Command:   "plan",
		AgentName: "test-agent",
		TaskID:    "initial-task",
		TaskTitle: "Initial Task",
		State:     "running",
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Create router
	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create watcher - should do initial read and set task
	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Verify task log was created (initial read happened)
	taskLogPath := filepath.Join(tmpDir, "tasks", "initial-task", "planning.log")
	if _, err := os.Stat(taskLogPath); os.IsNotExist(err) {
		t.Errorf("task log file was not created at %s after initial read", taskLogPath)
	}
}

func TestLockWatcher_ClearsTaskWhenTaskIDEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory for router
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// Create lock file with task
	lockPath := filepath.Join(tmpDir, ".agent.lock")
	lockInfo := AgentLockInfo{
		PID:       12345,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "task-to-clear",
		TaskTitle: "Task to Clear",
		State:     "running",
	}
	lockData, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Create router
	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create watcher (initial read sets task)
	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Write data while task is active
	if _, err := router.Write([]byte("with task")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Update lock file to clear task
	lockInfo.TaskID = ""
	lockInfo.TaskTitle = ""
	lockData, err = json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Trigger update
	watcher.readAndUpdateLock()

	// Write data after task cleared
	if _, err := router.Write([]byte("without task")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Flush
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Task log should only have "with task"
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-to-clear", "implementation.log")
	taskContent, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "with task" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "with task")
	}

	// Agent log should have both
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	agentContent, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(agentContent) != "with taskwithout task" {
		t.Errorf("agent log content = %q, want %q", string(agentContent), "with taskwithout task")
	}
}
