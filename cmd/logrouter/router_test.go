package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLogRouter_CreatesAgentLogFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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

func TestNewLogRouter_RejectsInvalidAgentName(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	tests := []struct {
		name      string
		agentName string
		wantErr   bool
	}{
		{"path traversal", "../../etc", true},
		{"forward slash", "agent/name", true},
		{"space", "agent name", true},
		{"empty string", "", true},
		{"single dot", ".", true},
		{"double dot", "..", true},
		{"valid hyphen", "valid-agent", false},
		{"valid underscore", "agent_v2", false},
		{"valid dot", "agent.v2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, err := NewLogRouter(tt.agentName, tmpDir, 0)
			if tt.wantErr {
				if err == nil {
					router.Close()
					t.Errorf("NewLogRouter(%q) succeeded, want error", tt.agentName)
				} else if !strings.Contains(err.Error(), "invalid agent name") {
					t.Errorf("NewLogRouter(%q) error = %v, want 'invalid agent name'", tt.agentName, err)
				}
			} else {
				if err != nil {
					t.Errorf("NewLogRouter(%q) failed: %v", tt.agentName, err)
				} else {
					router.Close()
				}
			}
		})
	}
}

func TestSetTask_RejectsInvalidTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	tests := []struct {
		name    string
		taskID  string
		wantErr bool
	}{
		{"path traversal", "../../etc", true},
		{"forward slash", "task/id", true},
		{"space", "task id", true},
		{"single dot", ".", true},
		{"double dot", "..", true},
		{"valid hyphen", "valid-task-123", false},
		{"valid dot", "loomcli-mp5.33", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := router.SetTask(tt.taskID, "planning")
			if tt.wantErr {
				if err == nil {
					t.Errorf("SetTask(%q) succeeded, want error", tt.taskID)
				} else if !strings.Contains(err.Error(), "invalid task ID") {
					t.Errorf("SetTask(%q) error = %v, want 'invalid task ID'", tt.taskID, err)
				}
			} else {
				if err != nil {
					t.Errorf("SetTask(%q) failed: %v", tt.taskID, err)
				}
				// Clear task for next iteration
				router.ClearTask()
			}
		})
	}
}

func TestRotatingWriter_RotatesWhenMaxSizeExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Create a rotating writer with a small max size (100 bytes)
	w, err := newRotatingWriter(logPath, 100, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter failed: %v", err)
	}

	// Write 80 bytes — should not rotate
	data80 := make([]byte, 80)
	for i := range data80 {
		data80[i] = 'A'
	}
	if _, err := w.Write(data80); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Write another 30 bytes — should trigger rotation (80+30 > 100)
	data30 := make([]byte, 30)
	for i := range data30 {
		data30[i] = 'B'
	}
	if _, err := w.Write(data30); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	w.Close()

	// Backup file .1 should exist with the first 80 bytes
	backup1 := logPath + ".1"
	content, err := os.ReadFile(backup1)
	if err != nil {
		t.Fatalf("failed to read backup file: %v", err)
	}
	if len(content) != 80 {
		t.Errorf("backup file size = %d, want 80", len(content))
	}

	// Current file should have the 30 bytes
	current, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read current log: %v", err)
	}
	if len(current) != 30 {
		t.Errorf("current file size = %d, want 30", len(current))
	}
}

func TestRotatingWriter_RespectsMaxBackups(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Create with maxBackups=2
	w, err := newRotatingWriter(logPath, 50, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter failed: %v", err)
	}

	// Write enough to trigger 3 rotations
	chunk := make([]byte, 60)
	for i := 0; i < 4; i++ {
		for j := range chunk {
			chunk[j] = byte('A' + i)
		}
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}
	w.Close()

	// Should have .1 and .2 backups, but NOT .3
	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error("backup .1 does not exist")
	}
	if _, err := os.Stat(logPath + ".2"); os.IsNotExist(err) {
		t.Error("backup .2 does not exist")
	}
	if _, err := os.Stat(logPath + ".3"); !os.IsNotExist(err) {
		t.Error("backup .3 should not exist (maxBackups=2)")
	}
}

func TestRotatingWriter_DisabledWhenMaxSizeZero(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	w, err := newRotatingWriter(logPath, 0, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter failed: %v", err)
	}

	// Write a large amount — should NOT trigger rotation
	data := make([]byte, 10000)
	for i := range data {
		data[i] = 'X'
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	w.Close()

	// No backup files should exist
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Error("backup .1 should not exist when rotation is disabled")
	}

	// All data should be in the current file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if len(content) != 10000 {
		t.Errorf("file size = %d, want 10000", len(content))
	}
}

func TestWrite_RotatesAgentLogWhenMaxSizeExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// Create router with 200 byte max log size
	router, err := NewLogRouter("test-agent", tmpDir, 200)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Write 150 bytes
	data := make([]byte, 150)
	for i := range data {
		data[i] = 'A'
	}
	if _, err := router.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	// Flush so the bufio writer pushes data through to rotatingWriter
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Write another 100 bytes — should trigger rotation
	data2 := make([]byte, 100)
	for i := range data2 {
		data2[i] = 'B'
	}
	if _, err := router.Write(data2); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := router.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Backup file should exist
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	backup := agentLogPath + ".1"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		t.Error("agent log backup .1 does not exist after rotation")
	}

	// Current file should be smaller than 200 bytes
	info, err := os.Stat(agentLogPath)
	if err != nil {
		t.Fatalf("failed to stat agent log: %v", err)
	}
	if info.Size() > 200 {
		t.Errorf("agent log size = %d, want <= 200", info.Size())
	}
}

func TestWrite_RotatesTaskLogWhenMaxSizeExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// Create router with 200 byte max log size
	router, err := NewLogRouter("test-agent", tmpDir, 200)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set task
	if err := router.SetTask("task-rot", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write 150 bytes
	data := make([]byte, 150)
	for i := range data {
		data[i] = 'A'
	}
	if _, err := router.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Write another 100 bytes — should trigger rotation
	data2 := make([]byte, 100)
	for i := range data2 {
		data2[i] = 'B'
	}
	if _, err := router.Write(data2); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := router.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Task log backup should exist
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-rot", "planning.log")
	backup := taskLogPath + ".1"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		t.Error("task log backup .1 does not exist after rotation")
	}

	// Current task log should be smaller than 200 bytes
	info, err := os.Stat(taskLogPath)
	if err != nil {
		t.Fatalf("failed to stat task log: %v", err)
	}
	if info.Size() > 200 {
		t.Errorf("task log size = %d, want <= 200", info.Size())
	}
}

func TestLogFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Check agent log file permissions
	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	info, err := os.Stat(agentLogPath)
	if err != nil {
		t.Fatalf("stat agent log: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("agent log permissions = %o, want 0600", perm)
	}

	// Set task to create task log
	if err := router.SetTask("task-123", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Check task directory permissions
	taskDir := filepath.Join(tmpDir, "tasks", "task-123")
	dirInfo, err := os.Stat(taskDir)
	if err != nil {
		t.Fatalf("stat task dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("task dir permissions = %o, want 0700", perm)
	}

	// Check task log file permissions
	taskLogPath := filepath.Join(taskDir, "planning.log")
	taskInfo, err := os.Stat(taskLogPath)
	if err != nil {
		t.Fatalf("stat task log: %v", err)
	}
	if perm := taskInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("task log permissions = %o, want 0600", perm)
	}
}

func TestClose_FlushesAllBuffers(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agents subdirectory
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
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
