package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewLogRouter_CreatesAgentLogFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Check that agent log file was created
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	if _, err := os.Stat(agentLogPath); os.IsNotExist(err) {
		t.Errorf("agent log file was not created at %s", agentLogPath)
	}
}

func TestWrite_RoutesToAgentLogAlways(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Write some data
	testData := []byte("hello world")
	n, err := router.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Write returned %d, want %d", n, len(testData))
	}

	// Close to flush buffers
	if err := router.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify agent log content
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	content, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("agent log content = %q, want %q", string(content), "hello world")
	}
}

func TestSetTask_OpensTaskLogFileWithCorrectPathAndPhase(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task with planning phase
	if err := router.SetTask("task-123", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Check that task log directory and file were created
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-123", "planning.log")
	if _, err := os.Stat(taskLogPath); os.IsNotExist(err) {
		t.Errorf("task log file was not created at %s", taskLogPath)
	}

	// Clear task and set with implementation phase
	if err := router.ClearTask(); err != nil {
		t.Fatalf("ClearTask failed: %v", err)
	}

	if err := router.SetTask("task-456", "implementation"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Check implementation log file
	implLogPath := filepath.Join(tmpDir, "tasks", "task-456", "implementation.log")
	if _, err := os.Stat(implLogPath); os.IsNotExist(err) {
		t.Errorf("task log file was not created at %s", implLogPath)
	}
}

func TestWrite_RoutesToBothAgentAndTaskLogsWhenTaskActive(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set task
	if err := router.SetTask("task-123", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write some data
	testData := []byte("task log data")
	n, err := router.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Write returned %d, want %d", n, len(testData))
	}

	// Close to flush buffers
	if err := router.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify agent log content
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	agentContent, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(agentContent) != "task log data" {
		t.Errorf("agent log content = %q, want %q", string(agentContent), "task log data")
	}

	// Verify task log content
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-123", "planning.log")
	taskContent, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "task log data" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "task log data")
	}
}

func TestClearTask_ClosesTaskLog(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task
	if err := router.SetTask("task-123", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write some data while task is active
	_, err = router.Write([]byte("before clear"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Clear task
	if err := router.ClearTask(); err != nil {
		t.Fatalf("ClearTask failed: %v", err)
	}

	// Write more data after clear - should only go to agent log
	_, err = router.Write([]byte("after clear"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Flush and close
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify task log only contains data from before clear
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-123", "planning.log")
	taskContent, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "before clear" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "before clear")
	}

	// Verify agent log contains all data
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	agentContent, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(agentContent) != "before clearafter clear" {
		t.Errorf("agent log content = %q, want %q", string(agentContent), "before clearafter clear")
	}
}

func TestClose_FlushesAllBuffers(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set task
	if err := router.SetTask("task-123", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write data to both logs
	testData := []byte("test data for flushing")
	if _, err := router.Write(testData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Before close, files might not have all data due to buffering
	// After close, all data should be flushed
	if err := router.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify agent log was flushed
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	agentContent, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(agentContent) != "test data for flushing" {
		t.Errorf("agent log content = %q, want %q", string(agentContent), "test data for flushing")
	}

	// Verify task log was flushed
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-123", "planning.log")
	taskContent, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != "test data for flushing" {
		t.Errorf("task log content = %q, want %q", string(taskContent), "test data for flushing")
	}
}
