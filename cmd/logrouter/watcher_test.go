package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestWatch_ProcessesWriteEvents(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Watch(ctx)

	// Write lock file with a task
	lockInfo := AgentLockInfo{
		PID:       1234,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "watch-task-1",
		TaskTitle: "Watch Task",
		State:     "running",
	}
	lockData, _ := json.Marshal(lockInfo)
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Poll for task log creation with timeout
	taskLogPath := filepath.Join(tmpDir, "tasks", "watch-task-1", "implementation.log")
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	found := false
	for !found {
		select {
		case <-deadline:
			t.Fatalf("task log file was not created at %s within timeout", taskLogPath)
		case <-ticker.C:
			if _, err := os.Stat(taskLogPath); err == nil {
				found = true
			}
		}
	}

	cancel()
}

func TestWatch_ProcessesRemoveEvent(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write lock file with task BEFORE creating watcher
	lockInfo := AgentLockInfo{
		PID:       1234,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "remove-task",
		TaskTitle: "Remove Task",
		State:     "running",
	}
	lockData, _ := json.Marshal(lockInfo)
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Watch(ctx)

	// Write data while task is active
	if _, err := router.Write([]byte("before remove")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Remove the lock file
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("failed to remove lock file: %v", err)
	}

	// Wait for fsnotify to deliver the remove event
	time.Sleep(200 * time.Millisecond)

	// Write data after task cleared — should only go to agent log
	if _, err := router.Write([]byte("after remove")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Task log should only have "before remove"
	taskContent, err := os.ReadFile(filepath.Join(tmpDir, "tasks", "remove-task", "implementation.log"))
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "before remove" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "before remove")
	}

	cancel()
}

func TestWatch_IgnoresUnrelatedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Watch(ctx)

	// Write a different file in the same directory
	otherPath := filepath.Join(tmpDir, "other.txt")
	if err := os.WriteFile(otherPath, []byte("unrelated"), 0644); err != nil {
		t.Fatalf("failed to write other file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// No task log should have been created since the lock file was never written
	tasksDir := filepath.Join(tmpDir, "tasks")
	if _, err := os.Stat(tasksDir); !os.IsNotExist(err) {
		t.Errorf("tasks directory should not exist, but it does")
	}

	cancel()
}

func TestWatch_StopsOnContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		watcher.Watch(ctx)
		close(done)
	}()

	// Cancel context immediately
	cancel()

	// Watch goroutine should exit
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not stop after context cancellation")
	}
}

func TestWatch_StopsWhenChannelClosed(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		watcher.Watch(ctx)
		close(done)
	}()

	// Close the fsnotify watcher to close channels
	watcher.Close()

	// Watch goroutine should exit
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not stop when watcher channels closed")
	}
}

func TestReadLockFile_ReturnsErrorForMalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write invalid JSON
	if err := os.WriteFile(lockPath, []byte("not valid json{{{"), 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	_, err = watcher.readLockFile()
	if err == nil {
		t.Fatal("readLockFile should return error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("error = %v, want to contain 'failed to parse'", err)
	}
}

func TestReadAndUpdateLock_HandlesNonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// readAndUpdateLock when file doesn't exist — should not panic
	watcher.readAndUpdateLock()

	// No task directory should have been created
	tasksDir := filepath.Join(tmpDir, "tasks")
	if _, err := os.Stat(tasksDir); !os.IsNotExist(err) {
		t.Errorf("tasks directory should not exist")
	}
}

func TestReadAndUpdateLock_HandlesInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write invalid JSON
	if err := os.WriteFile(lockPath, []byte("{invalid}"), 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// readAndUpdateLock with invalid JSON — should print warning but not crash
	watcher.readAndUpdateLock()

	// No task directory should have been created (invalid JSON means no valid task ID)
	tasksDir := filepath.Join(tmpDir, "tasks")
	if _, err := os.Stat(tasksDir); !os.IsNotExist(err) {
		t.Errorf("tasks directory should not exist after invalid JSON")
	}
}

func TestWatch_ProcessesErrorChannel(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		watcher.Watch(ctx)
		close(done)
	}()

	// Give the goroutine time to start
	time.Sleep(50 * time.Millisecond)

	// Close the watcher (which closes the Errors channel and Events channel)
	watcher.Close()

	// Watch should exit when channels close
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Watch did not stop when error channel closed")
	}
	cancel()
}

func TestWatch_ClearTaskErrorOnRemove(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write lock file with task
	lockInfo := AgentLockInfo{
		PID:       1234,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "clear-err-task",
		TaskTitle: "Clear Error Task",
		State:     "running",
	}
	lockData, _ := json.Marshal(lockInfo)
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Write some data then close the task rotator to make ClearTask fail
	if _, err := router.Write([]byte("data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	router.taskRotator.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Watch(ctx)

	// Remove the lock file — Watch will try to ClearTask which will encounter an error
	// It should print a warning but not crash
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("failed to remove lock file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	cancel()
}

func TestReadAndUpdateLock_SetTaskErrorWarning(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write lock file with a task ID that will cause SetTask to fail
	// We'll block the tasks directory by creating a file there
	tasksPath := filepath.Join(tmpDir, "tasks")
	if err := os.WriteFile(tasksPath, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	lockInfo := AgentLockInfo{
		PID:       1234,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "set-err-task",
		TaskTitle: "Set Error Task",
		State:     "running",
	}
	lockData, _ := json.Marshal(lockInfo)
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// readAndUpdateLock should print warning about SetTask failure but not crash
	watcher.readAndUpdateLock()
}

func TestReadAndUpdateLock_ClearTaskErrorWarning(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	lockPath := filepath.Join(tmpDir, ".agent.lock")

	// Write lock file WITHOUT a task ID — will trigger ClearTask path
	lockInfo := AgentLockInfo{
		PID:       1234,
		Command:   "task",
		AgentName: "test-agent",
		TaskID:    "",
		TaskTitle: "",
		State:     "running",
	}
	lockData, _ := json.Marshal(lockInfo)
	if err := os.WriteFile(lockPath, lockData, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set a task first, then break the task rotator so ClearTask errors
	if err := router.SetTask("prev-task", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}
	if _, err := router.Write([]byte("data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	router.taskRotator.Close()

	watcher, err := NewLockWatcher(lockPath, router)
	if err != nil {
		t.Fatalf("NewLockWatcher failed: %v", err)
	}
	defer watcher.Close()

	// readAndUpdateLock with empty TaskID triggers ClearTask
	// which will encounter an error — should print warning but not crash
	watcher.readAndUpdateLock()
}

