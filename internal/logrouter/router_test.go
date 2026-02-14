package logrouter

import (
	"context"
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

func TestRouteStdin_RoutesDataToAgentLog(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Replace os.Stdin with a pipe
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	// Write data and close the write end to signal EOF
	testData := "hello from stdin"
	go func() {
		w.Write([]byte(testData))
		w.Close()
	}()

	// RouteStdin should return nil on EOF
	if err := router.RouteStdin(context.Background()); err != nil {
		t.Fatalf("RouteStdin returned error: %v", err)
	}

	// Flush and verify agent log content
	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	agentLogPath := filepath.Join(tmpDir, "agents", "test-agent.log")
	content, err := os.ReadFile(agentLogPath)
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(content) != testData {
		t.Errorf("agent log content = %q, want %q", string(content), testData)
	}
}

func TestRouteStdin_RoutesDataToBothLogsWhenTaskActive(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task before routing stdin
	if err := router.SetTask("task-stdin", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Replace os.Stdin with a pipe
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	testData := "data for both logs"
	go func() {
		w.Write([]byte(testData))
		w.Close()
	}()

	if err := router.RouteStdin(context.Background()); err != nil {
		t.Fatalf("RouteStdin returned error: %v", err)
	}

	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify agent log
	agentContent, err := os.ReadFile(filepath.Join(tmpDir, "agents", "test-agent.log"))
	if err != nil {
		t.Fatalf("failed to read agent log: %v", err)
	}
	if string(agentContent) != testData {
		t.Errorf("agent log content = %q, want %q", string(agentContent), testData)
	}

	// Verify task log
	taskContent, err := os.ReadFile(filepath.Join(tmpDir, "tasks", "task-stdin", "planning.log"))
	if err != nil {
		t.Fatalf("failed to read task log: %v", err)
	}
	if string(taskContent) != testData {
		t.Errorf("task log content = %q, want %q", string(taskContent), testData)
	}
}

func TestRouteStdin_ReturnsNilOnContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Replace os.Stdin with a pipe that stays open
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = origStdin
		w.Close()
		r.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- router.RouteStdin(ctx)
	}()

	// Cancel context to stop RouteStdin
	cancel()

	// Close the pipe to unblock the read
	w.Close()

	err = <-done
	if err != nil {
		t.Errorf("RouteStdin returned error %v, want nil on context cancel", err)
	}
}

func TestSetTask_RejectsInvalidPhase(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	err = router.SetTask("task-1", "invalid")
	if err == nil {
		t.Fatal("SetTask with invalid phase should return error")
	}
	if !strings.Contains(err.Error(), "invalid phase") {
		t.Errorf("error = %v, want to contain 'invalid phase'", err)
	}
}

func TestSetTask_NoOpWhenSameTaskAndPhase(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task first time
	if err := router.SetTask("task-1", "planning"); err != nil {
		t.Fatalf("first SetTask failed: %v", err)
	}

	// Set same task and phase again — should be no-op
	if err := router.SetTask("task-1", "planning"); err != nil {
		t.Fatalf("second SetTask failed: %v", err)
	}

	// Verify task log exists (was created on first call)
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-1", "planning.log")
	if _, err := os.Stat(taskLogPath); os.IsNotExist(err) {
		t.Errorf("task log file was not created at %s", taskLogPath)
	}
}

func TestSetTask_SwitchesTask(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task A and write data
	if err := router.SetTask("task-a", "planning"); err != nil {
		t.Fatalf("SetTask task-a failed: %v", err)
	}
	if _, err := router.Write([]byte("data-a")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Switch to task B and write data
	if err := router.SetTask("task-b", "implementation"); err != nil {
		t.Fatalf("SetTask task-b failed: %v", err)
	}
	if _, err := router.Write([]byte("data-b")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := router.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify task A log has only its data
	taskAContent, err := os.ReadFile(filepath.Join(tmpDir, "tasks", "task-a", "planning.log"))
	if err != nil {
		t.Fatalf("failed to read task-a log: %v", err)
	}
	if string(taskAContent) != "data-a" {
		t.Errorf("task-a log content = %q, want %q", string(taskAContent), "data-a")
	}

	// Verify task B log has only its data
	taskBContent, err := os.ReadFile(filepath.Join(tmpDir, "tasks", "task-b", "implementation.log"))
	if err != nil {
		t.Fatalf("failed to read task-b log: %v", err)
	}
	if string(taskBContent) != "data-b" {
		t.Errorf("task-b log content = %q, want %q", string(taskBContent), "data-b")
	}
}

func TestClose_CalledTwice(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set a task so closeTaskLogLocked is exercised
	if err := router.SetTask("task-1", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// First close should succeed
	if err := router.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	// Second close should not panic (closeTaskLogLocked with nil taskWriter)
	// It may return an error from double-closing the file, but should not panic.
	router.Close()
}

func TestFlush_ReturnsErrorWhenAgentWriterBroken(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Write data to fill the bufio buffer partially
	if _, err := router.Write([]byte("some data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close the underlying agent rotator to break the writer
	router.agentRotator.Close()

	// Flush should return error since the underlying file is closed
	err = router.Flush()
	if err == nil {
		t.Error("Flush should return error when agent writer is broken")
	}
}

func TestFlush_ReturnsErrorWhenTaskWriterBroken(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set task
	if err := router.SetTask("task-flush", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write data to fill both buffers
	if _, err := router.Write([]byte("some task data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close the underlying task rotator to break the task writer
	router.taskRotator.Close()

	// Flush should return error from task writer
	err = router.Flush()
	if err == nil {
		t.Error("Flush should return error when task writer is broken")
	}
}

func TestClose_ReturnsAggregateErrors(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Set task
	if err := router.SetTask("task-err", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write data to fill buffers
	if _, err := router.Write([]byte("data for errors")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close the underlying task rotator to cause close errors
	router.taskRotator.Close()

	// Also close the agent rotator
	router.agentRotator.Close()

	// Close should return errors from both
	err = router.Close()
	if err == nil {
		t.Error("Close should return aggregate errors")
	}
}

func TestSetTask_MkdirAllError(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create a file at the tasks path to block MkdirAll
	tasksPath := filepath.Join(tmpDir, "tasks")
	if err := os.WriteFile(tasksPath, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	// SetTask should fail because it can't create directory
	err = router.SetTask("task-1", "planning")
	if err == nil {
		t.Error("SetTask should return error when MkdirAll fails")
	}
}

func TestRouteStdin_HandlesWriteError(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Close the underlying agent rotator to cause write errors
	router.agentRotator.Close()

	// Replace os.Stdin with a pipe
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	go func() {
		w.Write([]byte("data that will fail to write"))
		w.Close()
	}()

	// RouteStdin should still return nil on EOF even with write errors
	// (write errors are printed to stderr but don't stop routing)
	if err := router.RouteStdin(context.Background()); err != nil {
		t.Errorf("RouteStdin returned error: %v", err)
	}
}

func TestSetTask_ClosePreviousTaskLogWarning(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Set task first
	if err := router.SetTask("task-1", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Write data to fill buffer
	if _, err := router.Write([]byte("buffered data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close the task rotator to cause closeTaskLogLocked to fail when switching
	router.taskRotator.Close()

	// Switch to a different task — should trigger closeTaskLogLocked which will
	// encounter errors, print warning, and then proceed to open new task
	err = router.SetTask("task-2", "implementation")
	if err != nil {
		t.Fatalf("SetTask to task-2 should still succeed despite close warning: %v", err)
	}

	// Verify the new task log was created
	taskLogPath := filepath.Join(tmpDir, "tasks", "task-2", "implementation.log")
	if _, err := os.Stat(taskLogPath); os.IsNotExist(err) {
		t.Errorf("new task log file was not created at %s", taskLogPath)
	}
}

func TestNewLogRouter_RejectsInvalidBaseDir(t *testing.T) {
	// Use a path that can't be opened (non-existent deep path)
	router, err := NewLogRouter("test-agent", "/nonexistent/deeply/nested/path", 0)
	if err == nil {
		router.Close()
		t.Error("NewLogRouter should fail with invalid base dir")
	}
}

func TestWrite_ReturnsErrorWhenAgentWriteFails(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Close the agent rotator
	router.agentRotator.Close()

	// Write enough data to exceed the 64KB bufio buffer and trigger a flush
	bigData := make([]byte, 128*1024)
	for i := range bigData {
		bigData[i] = 'X'
	}

	_, err = router.Write(bigData)
	if err == nil {
		t.Error("Write should return error when agent writer flush fails")
	}
	if !strings.Contains(err.Error(), "failed to write to agent log") {
		t.Errorf("error = %v, want to contain 'failed to write to agent log'", err)
	}
}

func TestWrite_TaskWriteErrorWarning(t *testing.T) {
	tmpDir := t.TempDir()
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
	if err := router.SetTask("task-warn", "planning"); err != nil {
		t.Fatalf("SetTask failed: %v", err)
	}

	// Close the task rotator to make task writes fail
	router.taskRotator.Close()

	// Write enough data to trigger task writer flush error
	bigData := make([]byte, 128*1024)
	for i := range bigData {
		bigData[i] = 'Y'
	}

	// Write should succeed (agent log works) but print warning for task log
	_, err = router.Write(bigData)
	if err != nil {
		t.Errorf("Write should succeed for agent log even if task log fails: %v", err)
	}
}

func TestSetTask_NewRotatingWriterError(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}
	defer router.Close()

	// Create task directory but put a directory where the log file should be
	taskLogDir := filepath.Join(tmpDir, "tasks", "task-blocked")
	if err := os.MkdirAll(taskLogDir, 0755); err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}
	// Create a directory named "planning.log" to block file creation
	blockPath := filepath.Join(taskLogDir, "planning.log")
	if err := os.MkdirAll(blockPath, 0755); err != nil {
		t.Fatalf("failed to create blocker dir: %v", err)
	}

	err = router.SetTask("task-blocked", "planning")
	if err == nil {
		t.Error("SetTask should return error when log file can't be created")
	}
	if !strings.Contains(err.Error(), "failed to open task log file") {
		t.Errorf("error = %v, want to contain 'failed to open task log file'", err)
	}
}

func TestRouteStdin_HandlesWriteErrorInLoop(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	router, err := NewLogRouter("test-agent", tmpDir, 0)
	if err != nil {
		t.Fatalf("NewLogRouter failed: %v", err)
	}

	// Close the agent rotator to cause write errors
	router.agentRotator.Close()

	// Replace os.Stdin with a pipe
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	// Write enough data to exceed buffer and trigger write error in RouteStdin
	go func() {
		bigData := make([]byte, 128*1024)
		for i := range bigData {
			bigData[i] = 'Z'
		}
		w.Write(bigData)
		w.Close()
	}()

	// RouteStdin should still return nil on EOF even with write errors
	if err := router.RouteStdin(context.Background()); err != nil {
		t.Errorf("RouteStdin returned error: %v", err)
	}
}
