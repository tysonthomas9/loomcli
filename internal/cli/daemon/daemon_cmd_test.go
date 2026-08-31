package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// ============================================================================
// isLoomDaemonRunning Tests
// ============================================================================

func TestIsLoomDaemonRunning_NoPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	pid, running := IsLoomDaemonRunning(pidFilePath)

	if running {
		t.Error("IsLoomDaemonRunning() = true, want false when PID file doesn't exist")
	}
	if pid != 0 {
		t.Errorf("IsLoomDaemonRunning() pid = %d, want 0 when PID file doesn't exist", pid)
	}
}

func TestIsLoomDaemonRunning_StalePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	// Write a PID that definitely doesn't exist
	stalePID := 999999999
	if err := os.WriteFile(pidFilePath, []byte(strconv.Itoa(stalePID)+"\n"), 0644); err != nil {
		t.Fatalf("failed to write stale PID file: %v", err)
	}

	pid, running := IsLoomDaemonRunning(pidFilePath)

	if running {
		t.Error("IsLoomDaemonRunning() = true, want false for stale PID")
	}
	if pid != stalePID {
		t.Errorf("IsLoomDaemonRunning() pid = %d, want %d", pid, stalePID)
	}
}

func TestIsLoomDaemonRunning_ValidPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	// Write current process PID (which is running)
	currentPID := os.Getpid()
	if err := os.WriteFile(pidFilePath, []byte(strconv.Itoa(currentPID)+"\n"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	pid, running := IsLoomDaemonRunning(pidFilePath)

	if !running {
		t.Error("IsLoomDaemonRunning() = false, want true for running process")
	}
	if pid != currentPID {
		t.Errorf("IsLoomDaemonRunning() pid = %d, want %d", pid, currentPID)
	}
}

func TestIsLoomDaemonRunning_InvalidPIDContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"empty file", ""},
		{"non-numeric content", "not-a-pid"},
		{"floating point", "12345.67"},
		{"whitespace only", "   \n\t"},
		{"mixed content", "12345abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			pidFilePath := filepath.Join(tmpDir, "daemon.pid")

			if err := os.WriteFile(pidFilePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write PID file: %v", err)
			}

			pid, running := IsLoomDaemonRunning(pidFilePath)

			if running {
				t.Errorf("IsLoomDaemonRunning() = true, want false for invalid content %q", tt.content)
			}
			if pid != 0 {
				t.Errorf("IsLoomDaemonRunning() pid = %d, want 0 for invalid content", pid)
			}
		})
	}
}

func TestIsLoomDaemonRunning_NegativePID(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	// Negative PIDs are invalid and should return (0, false)
	if err := os.WriteFile(pidFilePath, []byte("-1\n"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	pid, running := IsLoomDaemonRunning(pidFilePath)

	// Negative PIDs are rejected as invalid
	if pid != 0 {
		t.Errorf("IsLoomDaemonRunning() pid = %d, want 0 for negative PID", pid)
	}
	if running {
		t.Error("IsLoomDaemonRunning() = true, want false for negative PID")
	}
}

func TestIsLoomDaemonRunning_PIDWithWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	// PID with leading/trailing whitespace (common in PID files)
	currentPID := os.Getpid()
	content := "  " + strconv.Itoa(currentPID) + "  \n"
	if err := os.WriteFile(pidFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	pid, running := IsLoomDaemonRunning(pidFilePath)

	if !running {
		t.Error("IsLoomDaemonRunning() = false, want true (should trim whitespace)")
	}
	if pid != currentPID {
		t.Errorf("IsLoomDaemonRunning() pid = %d, want %d", pid, currentPID)
	}
}

// ============================================================================
// writePIDFile Tests
// ============================================================================

func TestWritePIDFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	testPID := 12345
	err := writePIDFile(pidFilePath, testPID)

	if err != nil {
		t.Fatalf("writePIDFile() error = %v", err)
	}

	// Verify file contents
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		t.Fatalf("failed to read PID file: %v", err)
	}

	want := "12345\n"
	if string(data) != want {
		t.Errorf("PID file content = %q, want %q", string(data), want)
	}
}

func TestWritePIDFile_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	// Write initial content
	initialPID := 11111
	if err := writePIDFile(pidFilePath, initialPID); err != nil {
		t.Fatalf("failed to write initial PID: %v", err)
	}

	// Overwrite with new PID
	newPID := 22222
	if err := writePIDFile(pidFilePath, newPID); err != nil {
		t.Fatalf("failed to write new PID: %v", err)
	}

	// Verify final content
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		t.Fatalf("failed to read PID file: %v", err)
	}

	want := "22222\n"
	if string(data) != want {
		t.Errorf("PID file content = %q, want %q", string(data), want)
	}

	// Verify temp file was cleaned up
	tempFile := pidFilePath + ".tmp"
	if _, err := os.Stat(tempFile); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
}

func TestWritePIDFile_NoParentDirectory(t *testing.T) {
	// Try to write to a path where parent doesn't exist
	pidFilePath := "/nonexistent/path/to/daemon.pid"

	err := writePIDFile(pidFilePath, 12345)

	if err == nil {
		t.Error("writePIDFile() should fail when parent directory doesn't exist")
	}
}

func TestWritePIDFile_Permissions(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, "daemon.pid")

	if err := writePIDFile(pidFilePath, 12345); err != nil {
		t.Fatalf("writePIDFile() error = %v", err)
	}

	info, err := os.Stat(pidFilePath)
	if err != nil {
		t.Fatalf("failed to stat PID file: %v", err)
	}

	// Should be owner-only (0600)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("PID file permissions = %o, want 0600", perm)
	}
}

// ============================================================================
// readStateFile Tests
// ============================================================================

func TestReadStateFile_ValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	// Create valid state file
	state := DaemonState{
		PID:       12345,
		StartedAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Agents: []DaemonAgentStatus{
			{
				Worktree:     "falcon",
				Role:         "plan",
				PID:          12346,
				Status:       "running",
				TaskID:       "loom-123",
				RestartCount: 0,
				LastStart:    time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			},
			{
				Worktree:     "nova",
				Role:         "task",
				PID:          12347,
				Status:       "stopped",
				RestartCount: 2,
			},
		},
	}

	data, _ := json.Marshal(state)
	if err := os.WriteFile(stateFilePath, data, 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	result, err := ReadStateFile(stateFilePath)

	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if result.PID != 12345 {
		t.Errorf("PID = %d, want 12345", result.PID)
	}
	if len(result.Agents) != 2 {
		t.Errorf("len(Agents) = %d, want 2", len(result.Agents))
	}
	if result.Agents[0].Worktree != "falcon" {
		t.Errorf("Agents[0].Worktree = %q, want %q", result.Agents[0].Worktree, "falcon")
	}
	if result.Agents[0].TaskID != "loom-123" {
		t.Errorf("Agents[0].TaskID = %q, want %q", result.Agents[0].TaskID, "loom-123")
	}
	if result.Agents[1].RestartCount != 2 {
		t.Errorf("Agents[1].RestartCount = %d, want 2", result.Agents[1].RestartCount)
	}
}

func TestWriteStateFile_RoundTripsQuarantinedTasks(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	quarantined := []supervisor.QuarantinedTaskInfo{
		{
			TaskID:         "WEB-49",
			Count:          3,
			QuarantinedAt:  time.Date(2026, 6, 5, 14, 40, 0, 0, time.UTC),
			LastKillReason: "watchdog/Timeout",
		},
		{
			TaskID:         "WEB-50",
			Count:          4,
			LastKillReason: "crash/Unknown",
			WriteFailed:    true,
		},
	}
	if err := writeStateFile(stateFilePath, time.Now(), nil, nil, quarantined, nil, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	result, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if len(result.QuarantinedTasks) != 2 {
		t.Fatalf("len(QuarantinedTasks) = %d, want 2", len(result.QuarantinedTasks))
	}
	got := result.QuarantinedTasks[0]
	if got.TaskID != "WEB-49" || got.Count != 3 || got.LastKillReason != "watchdog/Timeout" || got.WriteFailed {
		t.Errorf("QuarantinedTasks[0] = %+v, want WEB-49/3/watchdog-Timeout/!failed", got)
	}
	if !got.QuarantinedAt.Equal(quarantined[0].QuarantinedAt) {
		t.Errorf("QuarantinedAt = %v, want %v", got.QuarantinedAt, quarantined[0].QuarantinedAt)
	}
	if pending := result.QuarantinedTasks[1]; !pending.WriteFailed || !pending.QuarantinedAt.IsZero() {
		t.Errorf("QuarantinedTasks[1] = %+v, want pending write-failed with zero QuarantinedAt", pending)
	}

	// Empty quarantine list keeps the field out of the JSON entirely.
	if err := writeStateFile(stateFilePath, time.Now(), nil, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}
	raw, err := os.ReadFile(stateFilePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.Contains(string(raw), "quarantined_tasks") {
		t.Errorf("empty quarantine list must be omitted from JSON:\n%s", raw)
	}
}

func TestReadStateFile_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "nonexistent.json")

	result, err := ReadStateFile(stateFilePath)

	if err == nil {
		t.Error("ReadStateFile() should return error for missing file")
	}
	if result != nil {
		t.Error("ReadStateFile() should return nil state for missing file")
	}
}

func TestReadStateFile_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	if err := os.WriteFile(stateFilePath, []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	result, err := ReadStateFile(stateFilePath)

	if err == nil {
		t.Error("ReadStateFile() should return error for invalid JSON")
	}
	if result != nil {
		t.Error("ReadStateFile() should return nil state for invalid JSON")
	}
}

func TestReadStateFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	if err := os.WriteFile(stateFilePath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	result, err := ReadStateFile(stateFilePath)

	if err == nil {
		t.Error("ReadStateFile() should return error for empty file")
	}
	if result != nil {
		t.Error("ReadStateFile() should return nil state for empty file")
	}
}

func TestReadStateFile_EmptyAgentsList(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")

	state := DaemonState{
		PID:       12345,
		StartedAt: time.Now(),
		Agents:    []DaemonAgentStatus{},
	}

	data, _ := json.Marshal(state)
	if err := os.WriteFile(stateFilePath, data, 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	result, err := ReadStateFile(stateFilePath)

	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if len(result.Agents) != 0 {
		t.Errorf("len(Agents) = %d, want 0", len(result.Agents))
	}
}

// ============================================================================
// resolveDaemonPath Tests
// ============================================================================

func TestResolveDaemonPath_RelativePath(t *testing.T) {
	tests := []struct {
		projectDir string
		path       string
		want       string
	}{
		{"/home/user/project", ".loom/daemon.pid", "/home/user/project/.loom/daemon.pid"},
		{"/home/user/project", "logs/daemon.log", "/home/user/project/logs/daemon.log"},
		{"/project", "daemon.pid", "/project/daemon.pid"},
		{"/", ".loom/daemon.pid", "/.loom/daemon.pid"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ResolveDaemonPath(tt.projectDir, tt.path)
			if got != tt.want {
				t.Errorf("ResolveDaemonPath(%q, %q) = %q, want %q", tt.projectDir, tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveDaemonPath_AbsolutePath(t *testing.T) {
	tests := []struct {
		projectDir string
		path       string
	}{
		{"/home/user/project", "/var/run/loom/daemon.pid"},
		{"/project", "/tmp/daemon.pid"},
		{"/", "/absolute/path/daemon.pid"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ResolveDaemonPath(tt.projectDir, tt.path)
			// Absolute paths should be returned as-is
			if got != tt.path {
				t.Errorf("ResolveDaemonPath(%q, %q) = %q, want %q", tt.projectDir, tt.path, got, tt.path)
			}
		})
	}
}

func TestResolveDaemonPath_EmptyPath(t *testing.T) {
	got := ResolveDaemonPath("/home/user/project", "")
	want := "/home/user/project"

	if got != want {
		t.Errorf("ResolveDaemonPath(%q, %q) = %q, want %q", "/home/user/project", "", got, want)
	}
}

// ============================================================================
// statusToIcon Tests
// ============================================================================

func TestStatusToIcon(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"running", "●"},
		{"starting", "◐"},
		{"stopped", "○"},
		{"failed", "✗"},
		{"unknown", "?"},
		{"", "?"},
		{"RUNNING", "?"},  // case-sensitive
		{"Running", "?"},  // case-sensitive
		{"stopping", "?"}, // not a valid status
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := statusToIcon(tt.status)
			if got != tt.want {
				t.Errorf("statusToIcon(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// ============================================================================
// computeAgentStatus Tests
// ============================================================================

func TestComputeAgentStatus_Running(t *testing.T) {
	// Use current process PID as a running process
	ap := SupervisedAgentStatus{
		PID:          os.Getpid(),
		RestartCount: 0,
	}

	status := computeAgentStatus(ap, 3)

	if status != "running" {
		t.Errorf("computeAgentStatus() = %q, want %q for running process", status, "running")
	}
}

func TestComputeAgentStatus_Stopped(t *testing.T) {
	// PID 0 means not running
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 0,
	}

	status := computeAgentStatus(ap, 3)

	if status != "stopped" {
		t.Errorf("computeAgentStatus() = %q, want %q when PID is 0", status, "stopped")
	}
}

func TestComputeAgentStatus_StoppedWithDeadPID(t *testing.T) {
	// Non-running PID
	ap := SupervisedAgentStatus{
		PID:          999999999,
		RestartCount: 2,
	}

	status := computeAgentStatus(ap, 3)

	if status != "stopped" {
		t.Errorf("computeAgentStatus() = %q, want %q for dead process with low restart count", status, "stopped")
	}
}

func TestComputeAgentStatus_Failed(t *testing.T) {
	// High restart count indicates failure
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 4, // > 3 (default max retries)
	}

	status := computeAgentStatus(ap, 3)

	if status != "failed" {
		t.Errorf("computeAgentStatus() = %q, want %q for high restart count", status, "failed")
	}
}

func TestComputeAgentStatus_FailedBoundary(t *testing.T) {
	tests := []struct {
		name         string
		restartCount int
		want         string
	}{
		{"at limit (3)", 3, "stopped"},
		{"just over limit (4)", 4, "failed"},
		{"well over limit (10)", 10, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := SupervisedAgentStatus{
				PID:          0, // not running
				RestartCount: tt.restartCount,
			}

			status := computeAgentStatus(ap, 3)

			if status != tt.want {
				t.Errorf("computeAgentStatus() = %q, want %q for restartCount=%d", status, tt.want, tt.restartCount)
			}
		})
	}
}

func TestComputeAgentStatus_RunningOverridesRestartCount(t *testing.T) {
	// Even with high restart count, if process is running, status should be "running"
	ap := SupervisedAgentStatus{
		PID:          os.Getpid(),
		RestartCount: 10, // high restart count
	}

	status := computeAgentStatus(ap, 3)

	if status != "running" {
		t.Errorf("computeAgentStatus() = %q, want %q - running should override restart count", status, "running")
	}
}

func TestComputeAgentStatus_CustomMaxRetries(t *testing.T) {
	tests := []struct {
		name         string
		restartCount int
		maxRetries   int
		want         string
	}{
		{"below custom limit", 4, 10, "stopped"},
		{"at custom limit", 10, 10, "stopped"},
		{"above custom limit", 11, 10, "failed"},
		{"zero max retries, no restarts", 0, 0, "stopped"},
		{"zero max retries, one restart", 1, 0, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := SupervisedAgentStatus{
				PID:          0,
				RestartCount: tt.restartCount,
			}
			status := computeAgentStatus(ap, tt.maxRetries)
			if status != tt.want {
				t.Errorf("computeAgentStatus(restartCount=%d, maxRetries=%d) = %q, want %q",
					tt.restartCount, tt.maxRetries, status, tt.want)
			}
		})
	}
}

// ============================================================================
// writeStateFile Tests
// ============================================================================

func TestWriteStateFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")
	startedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	agents := []SupervisedAgentStatus{
		{
			Worktree:     "falcon",
			Role:         "plan",
			PID:          os.Getpid(), // running
			RestartCount: 0,
			LastStart:    startedAt,
		},
		{
			Worktree:     "nova",
			Role:         "task",
			PID:          0, // not running
			RestartCount: 2,
		},
	}

	err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3)

	if err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	// Read back and verify
	result, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	if result.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", result.PID, os.Getpid())
	}
	if len(result.Agents) != 2 {
		t.Errorf("len(Agents) = %d, want 2", len(result.Agents))
	}
	if result.Agents[0].Status != "running" {
		t.Errorf("Agents[0].Status = %q, want %q", result.Agents[0].Status, "running")
	}
	if result.Agents[1].Status != "stopped" {
		t.Errorf("Agents[1].Status = %q, want %q", result.Agents[1].Status, "stopped")
	}
}

func TestWriteStateFile_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")
	startedAt := time.Now()

	// Write state
	agents := []SupervisedAgentStatus{{Worktree: "test", Role: "plan"}}
	if err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	// Verify temp file was cleaned up
	tempFile := stateFilePath + ".tmp"
	if _, err := os.Stat(tempFile); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
}

// ============================================================================
// DaemonState and DaemonAgentStatus struct Tests
// ============================================================================

func TestDaemonAgentStatus_JSONTags(t *testing.T) {
	status := DaemonAgentStatus{
		Worktree:     "falcon",
		Role:         "plan",
		PID:          12345,
		Status:       "running",
		TaskID:       "loom-123",
		RestartCount: 2,
		LastStart:    time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		LastExit:     time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
		LastExitCode: 1,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Parse as generic map to check JSON keys
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	expectedKeys := []string{"worktree", "role", "pid", "status", "task_id", "restart_count", "last_start", "last_exit", "last_exit_code"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("expected JSON key %q not found", key)
		}
	}
}

func TestDaemonAgentStatus_OmitEmpty(t *testing.T) {
	// TaskID with omitempty should not appear when empty
	status := DaemonAgentStatus{
		Worktree: "falcon",
		Role:     "plan",
		Status:   "running",
		TaskID:   "", // empty
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := m["task_id"]; ok {
		t.Error("task_id should be omitted when empty")
	}
}

func TestDaemonState_JSONRoundTrip(t *testing.T) {
	original := DaemonState{
		PID:       12345,
		StartedAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Agents: []DaemonAgentStatus{
			{
				Worktree:     "falcon",
				Role:         "plan",
				PID:          12346,
				Status:       "running",
				RestartCount: 0,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result DaemonState
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.PID != original.PID {
		t.Errorf("PID = %d, want %d", result.PID, original.PID)
	}
	if !result.StartedAt.Equal(original.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", result.StartedAt, original.StartedAt)
	}
	if len(result.Agents) != len(original.Agents) {
		t.Errorf("len(Agents) = %d, want %d", len(result.Agents), len(original.Agents))
	}
}

// ============================================================================
// Integration-style tests for daemon command helpers
// ============================================================================

func TestDaemonPIDFileLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	pidFilePath := filepath.Join(tmpDir, ".loom", "daemon.pid")

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	// Initially no daemon running
	_, running := IsLoomDaemonRunning(pidFilePath)
	if running {
		t.Error("should not be running initially")
	}

	// Write PID file
	currentPID := os.Getpid()
	if err := writePIDFile(pidFilePath, currentPID); err != nil {
		t.Fatalf("writePIDFile() error = %v", err)
	}

	// Now daemon should appear running
	pid, running := IsLoomDaemonRunning(pidFilePath)
	if !running {
		t.Error("should be running after writing PID file")
	}
	if pid != currentPID {
		t.Errorf("pid = %d, want %d", pid, currentPID)
	}

	// Remove PID file (simulating daemon stop)
	if err := os.Remove(pidFilePath); err != nil {
		t.Fatalf("failed to remove PID file: %v", err)
	}

	// No longer running
	_, running = IsLoomDaemonRunning(pidFilePath)
	if running {
		t.Error("should not be running after removing PID file")
	}
}

func TestStateFileLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")
	startedAt := time.Now()

	// Initially no state file
	_, err := ReadStateFile(stateFilePath)
	if err == nil {
		t.Error("should return error when state file doesn't exist")
	}

	// Write initial state
	agents := []SupervisedAgentStatus{
		{Worktree: "falcon", Role: "plan", PID: os.Getpid()},
	}
	if err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	// Read state
	state, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if len(state.Agents) != 1 {
		t.Errorf("len(Agents) = %d, want 1", len(state.Agents))
	}

	// Update state (add agent)
	agents = append(agents, SupervisedAgentStatus{Worktree: "nova", Role: "task"})
	if err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile() update error = %v", err)
	}

	// Re-read
	state, err = ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if len(state.Agents) != 2 {
		t.Errorf("len(Agents) = %d, want 2", len(state.Agents))
	}
}

// ============================================================================
// validateDaemonPaths Tests
// ============================================================================

// captureLogOutput runs fn while capturing log output to a buffer, then
// restores the original log output and returns the captured string.
func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	// Capture stdlib log output.
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	// Capture slog output (slog.Warn etc. write to slog's default handler, not stdlib log).
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
		slog.SetDefault(origLogger)
	}()
	fn()
	return buf.String()
}

func TestValidateDaemonPaths_WithinProjectDir(t *testing.T) {
	projectDir := t.TempDir()
	pidFile := filepath.Join(projectDir, ".loom", "daemon.pid")
	logDir := filepath.Join(projectDir, ".loom", "logs")

	output := captureLogOutput(t, func() {
		ValidateDaemonPaths(projectDir, pidFile, logDir)
	})

	if strings.Contains(output, "WARN") {
		t.Errorf("expected no warnings for paths within project dir, got: %s", output)
	}
}

func TestValidateDaemonPaths_WithinConfigDir(t *testing.T) {
	projectDir := t.TempDir()
	configDir := t.TempDir() // simulate ~/.loom/

	// Override LOOM_CONFIG_DIR so GetConfigDir() returns our temp dir
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	pidFile := filepath.Join(configDir, "daemon.pid")
	logDir := filepath.Join(configDir, "logs")

	output := captureLogOutput(t, func() {
		ValidateDaemonPaths(projectDir, pidFile, logDir)
	})

	if strings.Contains(output, "WARN") {
		t.Errorf("expected no warnings for paths within config dir, got: %s", output)
	}
}

func TestValidateDaemonPaths_PIDFileOutsideBoundaries(t *testing.T) {
	projectDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	// PID file in /tmp (outside both project and config dirs)
	outsideDir := t.TempDir()
	pidFile := filepath.Join(outsideDir, "daemon.pid")
	logDir := filepath.Join(projectDir, ".loom", "logs")

	output := captureLogOutput(t, func() {
		ValidateDaemonPaths(projectDir, pidFile, logDir)
	})

	if !strings.Contains(output, "WARN") {
		t.Error("expected warning for pid_file outside boundaries, got no warning")
	}
	if !strings.Contains(output, "pid_file") {
		t.Errorf("expected warning to mention pid_file, got: %s", output)
	}
	// log_dir is inside project, so no warning for it
	if strings.Contains(output, "log_dir") {
		t.Errorf("expected no warning for log_dir (inside project dir), got: %s", output)
	}
}

func TestValidateDaemonPaths_LogDirPathTraversal(t *testing.T) {
	projectDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	pidFile := filepath.Join(projectDir, ".loom", "daemon.pid")
	// Path traversal that escapes the project directory
	logDir := filepath.Join(projectDir, "../../outside")

	output := captureLogOutput(t, func() {
		ValidateDaemonPaths(projectDir, pidFile, logDir)
	})

	if !strings.Contains(output, "WARN") {
		t.Error("expected warning for log_dir with path traversal, got no warning")
	}
	if !strings.Contains(output, "log_dir") {
		t.Errorf("expected warning to mention log_dir, got: %s", output)
	}
}

func TestValidateDaemonPaths_AbsolutePathOutsideBoundaries(t *testing.T) {
	projectDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	pidFile := filepath.Join(projectDir, ".loom", "daemon.pid")
	// Absolute path outside both boundaries
	logDir := "/tmp/loom-logs"

	output := captureLogOutput(t, func() {
		ValidateDaemonPaths(projectDir, pidFile, logDir)
	})

	if !strings.Contains(output, "WARN") {
		t.Error("expected warning for absolute log_dir outside boundaries, got no warning")
	}
	if !strings.Contains(output, "log_dir") {
		t.Errorf("expected warning to mention log_dir, got: %s", output)
	}
}

// ============================================================================
// computeAgentStatus Tests — StopReason integration
// ============================================================================

func TestComputeAgentStatus_FatalError(t *testing.T) {
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 0,
		StopReason:   StopReasonFatalError,
	}

	status := computeAgentStatus(ap, 3)

	if status != "failed" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonFatalError with RestartCount=0", status, "failed")
	}
}

func TestComputeAgentStatus_MaxRetries_StopReason(t *testing.T) {
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 4,
		StopReason:   StopReasonMaxRetries,
	}

	status := computeAgentStatus(ap, 3)

	if status != "failed" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonMaxRetries", status, "failed")
	}
}

func TestComputeAgentStatus_Blocked(t *testing.T) {
	// A blocked agent (exhausted budget, retrying) is not running and not
	// failed: RestartCount is reset to 0 and the blocked stop reason yields
	// "blocked".
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 0,
		StopReason:   StopReasonMaxRetriesBlocked,
	}

	status := computeAgentStatus(ap, 3)

	if status != "blocked" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonMaxRetriesBlocked", status, "blocked")
	}
}

func TestComputeAgentStatus_Blocked_RunningTakesPrecedence(t *testing.T) {
	// A re-spawned blocked agent (PID alive) reads as "running" — the running
	// guard is checked before the blocked stop reason.
	ap := SupervisedAgentStatus{
		PID:          os.Getpid(), // a live process
		RestartCount: 0,
		StopReason:   StopReasonMaxRetriesBlocked,
	}

	status := computeAgentStatus(ap, 3)

	if status != "running" {
		t.Errorf("computeAgentStatus() = %q, want %q (running must override a stale blocked reason)", status, "running")
	}
}

func TestComputeAgentStatus_FastFail(t *testing.T) {
	// A fast-failed agent (deterministic failure the policy refused to block)
	// is terminal: surfaced as "failed".
	ap := SupervisedAgentStatus{
		PID:        0,
		StopReason: StopReasonFastFail,
	}

	status := computeAgentStatus(ap, 3)

	if status != "failed" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonFastFail", status, "failed")
	}
}

func TestComputeAgentStatus_Shutdown(t *testing.T) {
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 0,
		StopReason:   StopReasonShutdown,
	}

	status := computeAgentStatus(ap, 3)

	if status != "stopped" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonShutdown (should not be failed)", status, "stopped")
	}
}

func TestComputeAgentStatus_ConfigRemoved(t *testing.T) {
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 0,
		StopReason:   StopReasonConfigRemoved,
	}

	status := computeAgentStatus(ap, 3)

	if status != "stopped" {
		t.Errorf("computeAgentStatus() = %q, want %q for StopReasonConfigRemoved", status, "stopped")
	}
}

func TestComputeAgentStatus_BackwardCompat(t *testing.T) {
	// No StopReason set, but RestartCount exceeds maxRetries — old behavior should still work
	ap := SupervisedAgentStatus{
		PID:          0,
		RestartCount: 5,
		StopReason:   "", // empty — pre-StopReason agent
	}

	status := computeAgentStatus(ap, 3)

	if status != "failed" {
		t.Errorf("computeAgentStatus() = %q, want %q for backward-compat (empty StopReason, high RestartCount)", status, "failed")
	}
}

// ============================================================================
// DaemonAgentStatus JSON Tests — StopReason and StoppedAt
// ============================================================================

func TestDaemonAgentStatus_StopReasonJSON(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
		wantKey    bool
	}{
		{"present when set", "fatal_error", true},
		{"omitted when empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := DaemonAgentStatus{
				Worktree:   "falcon",
				Role:       "plan",
				Status:     "failed",
				StopReason: tt.stopReason,
			}

			data, err := json.Marshal(status)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			_, ok := m["stop_reason"]
			if ok != tt.wantKey {
				t.Errorf("stop_reason present = %v, want %v (json: %s)", ok, tt.wantKey, string(data))
			}
		})
	}
}

func TestDaemonAgentStatus_StoppedAtJSON(t *testing.T) {
	// When StoppedAt is set, it should appear with the correct value.
	// When StoppedAt is zero, it still appears in JSON (time.Time zero value
	// is not suppressed by omitempty in encoding/json) but with the zero time.
	t.Run("present with correct value when set", func(t *testing.T) {
		ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
		status := DaemonAgentStatus{
			Worktree:  "falcon",
			Role:      "plan",
			Status:    "stopped",
			StoppedAt: ts,
		}

		data, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		raw, ok := m["stopped_at"]
		if !ok {
			t.Fatal("stopped_at key should be present when set")
		}

		// Verify the value round-trips correctly
		var roundTrip DaemonAgentStatus
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal into struct: %v", err)
		}
		if !roundTrip.StoppedAt.Equal(ts) {
			t.Errorf("stopped_at round-trip = %v, want %v (raw: %v)", roundTrip.StoppedAt, ts, raw)
		}
	})

	t.Run("zero value distinguishable from set value", func(t *testing.T) {
		status := DaemonAgentStatus{
			Worktree:  "falcon",
			Role:      "plan",
			Status:    "stopped",
			StoppedAt: time.Time{}, // zero
		}

		data, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var roundTrip DaemonAgentStatus
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if !roundTrip.StoppedAt.IsZero() {
			t.Errorf("stopped_at should be zero after round-trip, got %v", roundTrip.StoppedAt)
		}
	})
}

// ============================================================================
// writeStateFile Tests — StopReason round-trip
// ============================================================================

func TestWriteStateFile_WithStopReason(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")
	startedAt := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	lastExit := time.Date(2024, 6, 15, 11, 30, 0, 0, time.UTC)

	agents := []SupervisedAgentStatus{
		{
			Worktree:     "falcon",
			Role:         "plan",
			PID:          0,
			RestartCount: 0,
			StopReason:   StopReasonFatalError,
			LastExit:     lastExit,
			LastExitCode: 1,
		},
		{
			Worktree:     "nova",
			Role:         "task",
			PID:          0,
			RestartCount: 4,
			StopReason:   StopReasonMaxRetries,
			LastExit:     lastExit,
		},
		{
			Worktree:   "comet",
			Role:       "plan",
			PID:        0,
			StopReason: StopReasonShutdown,
			LastExit:   lastExit,
		},
		{
			Worktree:   "orbit",
			Role:       "task",
			PID:        0,
			StopReason: StopReasonConfigRemoved,
			LastExit:   lastExit,
		},
		{
			Worktree:     "star",
			Role:         "task",
			PID:          os.Getpid(), // running — should have no StoppedAt
			RestartCount: 0,
			StopReason:   "", // empty — running agent
		},
	}

	err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3)
	if err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	// Read back
	state, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}

	if len(state.Agents) != 5 {
		t.Fatalf("len(Agents) = %d, want 5", len(state.Agents))
	}

	// falcon: fatal_error → "failed", StopReason set, StoppedAt set
	falcon := state.Agents[0]
	if falcon.Status != "failed" {
		t.Errorf("falcon.Status = %q, want %q", falcon.Status, "failed")
	}
	if falcon.StopReason != string(StopReasonFatalError) {
		t.Errorf("falcon.StopReason = %q, want %q", falcon.StopReason, StopReasonFatalError)
	}
	if falcon.StoppedAt.IsZero() {
		t.Error("falcon.StoppedAt should be set for stopped agent with StopReason")
	}

	// nova: max_retries → "failed", StopReason set
	nova := state.Agents[1]
	if nova.Status != "failed" {
		t.Errorf("nova.Status = %q, want %q", nova.Status, "failed")
	}
	if nova.StopReason != string(StopReasonMaxRetries) {
		t.Errorf("nova.StopReason = %q, want %q", nova.StopReason, StopReasonMaxRetries)
	}
	if nova.StoppedAt.IsZero() {
		t.Error("nova.StoppedAt should be set for stopped agent with StopReason")
	}

	// comet: shutdown → "stopped" (not "failed")
	comet := state.Agents[2]
	if comet.Status != "stopped" {
		t.Errorf("comet.Status = %q, want %q", comet.Status, "stopped")
	}
	if comet.StopReason != string(StopReasonShutdown) {
		t.Errorf("comet.StopReason = %q, want %q", comet.StopReason, StopReasonShutdown)
	}

	// orbit: config_removed → "stopped"
	orbit := state.Agents[3]
	if orbit.Status != "stopped" {
		t.Errorf("orbit.Status = %q, want %q", orbit.Status, "stopped")
	}
	if orbit.StopReason != string(StopReasonConfigRemoved) {
		t.Errorf("orbit.StopReason = %q, want %q", orbit.StopReason, StopReasonConfigRemoved)
	}

	// star: running, no StopReason → "running", no StoppedAt
	star := state.Agents[4]
	if star.Status != "running" {
		t.Errorf("star.Status = %q, want %q", star.Status, "running")
	}
	if star.StopReason != "" {
		t.Errorf("star.StopReason = %q, want empty string", star.StopReason)
	}
	if !star.StoppedAt.IsZero() {
		t.Error("star.StoppedAt should be zero for running agent")
	}
}

// ============================================================================
// formatDaemonDuration Tests
// ============================================================================

func TestFormatDaemonDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "<1s"},
		{"negative", -5 * time.Second, "<1s"},
		{"sub-second", 500 * time.Millisecond, "<1s"},
		{"one second", time.Second, "1s"},
		{"30 seconds", 30 * time.Second, "30s"},
		{"59 seconds", 59 * time.Second, "59s"},
		{"90 seconds", 90 * time.Second, "1m 30s"},
		{"5 minutes", 5 * time.Minute, "5m 0s"},
		{"5m30s", 5*time.Minute + 30*time.Second, "5m 30s"},
		{"1 hour", time.Hour, "1h 0m"},
		{"1h1m1s", time.Hour + time.Minute + time.Second, "1h 1m"},
		{"2h30m", 2*time.Hour + 30*time.Minute, "2h 30m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDaemonDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDaemonDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// ============================================================================
// DaemonAgentStatus JSON Tests — New Fields
// ============================================================================

func TestDaemonAgentStatus_NewFields_JSON(t *testing.T) {
	backoffTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	status := DaemonAgentStatus{
		Worktree:       "falcon",
		Role:           "plan",
		PID:            12345,
		Status:         "running",
		WorktreePath:   "/path/to/falcon",
		LastErrorClass: "RateLimited",
		NoWorkCount:    3,
		BackoffUntil:   backoffTime,
		RemoteBranch:   "origin/main",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	expectedKeys := []string{"worktree_path", "last_error_class", "no_work_count", "backoff_until", "remote_branch"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("expected JSON key %q not found", key)
		}
	}

	// Verify round-trip
	var roundTrip DaemonAgentStatus
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("failed to unmarshal into struct: %v", err)
	}
	if roundTrip.WorktreePath != "/path/to/falcon" {
		t.Errorf("WorktreePath = %q, want %q", roundTrip.WorktreePath, "/path/to/falcon")
	}
	if roundTrip.LastErrorClass != "RateLimited" {
		t.Errorf("LastErrorClass = %q, want %q", roundTrip.LastErrorClass, "RateLimited")
	}
	if roundTrip.NoWorkCount != 3 {
		t.Errorf("NoWorkCount = %d, want 3", roundTrip.NoWorkCount)
	}
	if roundTrip.RemoteBranch != "origin/main" {
		t.Errorf("RemoteBranch = %q, want %q", roundTrip.RemoteBranch, "origin/main")
	}
}

func TestDaemonAgentStatus_NewFields_OmitEmpty(t *testing.T) {
	status := DaemonAgentStatus{
		Worktree: "falcon",
		Role:     "plan",
		Status:   "running",
		// All new fields are zero/empty
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	omittedKeys := []string{"worktree_path", "last_error_class", "no_work_count", "remote_branch"}
	for _, key := range omittedKeys {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted when zero/empty", key)
		}
	}
}

func TestWriteStateFile_NewFields_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "daemon-agents.json")
	startedAt := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	backoffTime := time.Date(2024, 6, 15, 10, 5, 0, 0, time.UTC)

	agents := []SupervisedAgentStatus{
		{
			Worktree:       "falcon",
			Role:           "plan",
			PID:            0,
			WorktreePath:   "/path/to/falcon",
			LastErrorClass: "Timeout",
			NoWorkCount:    7,
			BackoffUntil:   backoffTime,
			RemoteBranch:   "origin/develop",
		},
	}

	err := writeStateFile(stateFilePath, startedAt, agents, nil, nil, nil, 3)
	if err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	state, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}

	if len(state.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(state.Agents))
	}

	a := state.Agents[0]
	if a.WorktreePath != "/path/to/falcon" {
		t.Errorf("WorktreePath = %q, want %q", a.WorktreePath, "/path/to/falcon")
	}
	if a.LastErrorClass != "Timeout" {
		t.Errorf("LastErrorClass = %q, want %q", a.LastErrorClass, "Timeout")
	}
	if a.NoWorkCount != 7 {
		t.Errorf("NoWorkCount = %d, want 7", a.NoWorkCount)
	}
	if !a.BackoffUntil.Equal(backoffTime) {
		t.Errorf("BackoffUntil = %v, want %v", a.BackoffUntil, backoffTime)
	}
	if a.RemoteBranch != "origin/develop" {
		t.Errorf("RemoteBranch = %q, want %q", a.RemoteBranch, "origin/develop")
	}
}

// ============================================================================
// PUPPET-57: daemon status targets the DETECTED daemon, not the cwd
// ============================================================================

// TestStatePathForTarget_UsesDetectedDir is the one-line core of the fix,
// asserted at the path level: with the process sitting in directory A and
// detection having resolved a daemon in directory B, the state file that gets
// read must be B's. Reading A's is how a live PID ended up printed next to a
// dead daemon's snapshot.
func TestStatePathForTarget_UsesDetectedDir(t *testing.T) {
	cwd := t.TempDir()
	target := t.TempDir()

	rt := cli.DaemonRuntimeInfo{Running: true, PID: 75714, Source: "workspace-lock", Dir: target}

	got := statePathForTarget(rt, cwd)
	if !strings.HasPrefix(got, target) {
		t.Errorf("state path = %q, want a path under the detected daemon's dir %q", got, target)
	}
	if strings.HasPrefix(got, cwd) {
		t.Errorf("state path = %q must not be derived from the cwd %q", got, cwd)
	}
}

// TestStatePathForTarget_FallsBackToCwd keeps hand-built DaemonRuntimeInfo
// values (older callers, tests) working: an empty Dir means "the cwd's daemon",
// which is the historical behavior.
func TestStatePathForTarget_FallsBackToCwd(t *testing.T) {
	cwd := t.TempDir()

	got := statePathForTarget(cli.DaemonRuntimeInfo{Running: true, PID: 1}, cwd)
	if !strings.HasPrefix(got, cwd) {
		t.Errorf("state path = %q, want a path under the cwd %q when Dir is empty", got, cwd)
	}
}

// TestReadStateForTarget reports both the snapshot and its mtime, and degrades
// to (nil, zero) rather than an error when there is nothing to read.
func TestReadStateForTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")

	if state, mtime := readStateForTarget(path); state != nil || !mtime.IsZero() {
		t.Errorf("missing file: got (%v, %v), want (nil, zero)", state, mtime)
	}

	data, err := json.Marshal(DaemonState{PID: 4242, StartedAt: time.Now(), Agents: make([]DaemonAgentStatus, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	state, mtime := readStateForTarget(path)
	if state == nil || state.PID != 4242 || len(state.Agents) != 2 {
		t.Fatalf("state = %+v, want PID 4242 with 2 agents", state)
	}
	if mtime.IsZero() {
		t.Error("mtime is zero for an existing file; freshness could never be judged")
	}
}

// TestRunDaemonStatus_PUPPET57 drives the command end to end against the
// reported shape: a live daemon detected in one directory, whose state file
// on disk belongs to a long-dead PID. The output must describe the live
// daemon and must not print the corpse's start time or "Agents: 0".
func TestRunDaemonStatus_PUPPET57(t *testing.T) {
	// Isolate from any real daemon on the dev box.
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_FLEET_DB_URL", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_DESKTOP_DATA_DIR", t.TempDir())

	// The daemon's real directory, with a state file left by an earlier,
	// now-dead daemon (the dogfood corpse from the incident).
	targetDir := t.TempDir()
	loomDir := filepath.Join(targetDir, ".loom")
	if err := os.MkdirAll(loomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deadSnapshot := []byte(`{"pid":61906,"started_at":"2026-08-09T18:23:08+02:00","agents":[]}`)
	if err := os.WriteFile(filepath.Join(loomDir, "daemon-agents.json"), deadSnapshot, 0o644); err != nil {
		t.Fatal(err)
	}

	liveStart := time.Now().Add(-90 * time.Minute).Round(time.Second)
	rt := cli.DaemonRuntimeInfo{
		Running:   true,
		PID:       os.Getpid(),
		Source:    "workspace-lock",
		StartedAt: liveStart,
		Dir:       targetDir,
	}

	statePath := statePathForTarget(rt, t.TempDir())
	state, mtime := readStateForTarget(statePath)

	view := buildDaemonStatusView(daemonStatusInputs{
		RT:         rt,
		State:      state,
		StatePath:  statePath,
		StateMTime: mtime,
		LiveCount:  agentCountUnknown,
		Now:        time.Now(),
	})

	var buf bytes.Buffer
	for _, line := range view.HeaderLines() {
		buf.WriteString(line + "\n")
	}
	out := buf.String()

	if !strings.Contains(out, "Daemon: running (PID "+strconv.Itoa(os.Getpid())+")") {
		t.Errorf("output should name the live daemon's PID:\n%s", out)
	}
	if !strings.Contains(out, "Started: "+liveStart.Format(time.RFC3339)) {
		t.Errorf("output should report the live daemon's start time:\n%s", out)
	}
	if strings.Contains(out, "2026-08-09") {
		t.Errorf("output leaks the dead snapshot's start time:\n%s", out)
	}
	if strings.Contains(out, "Agents: 0") {
		t.Errorf("output reports an unverifiable agent count as zero:\n%s", out)
	}
	if view.Trusted {
		t.Error("the mismatched state file must not be trusted, so no agent table is rendered")
	}
}

// TestRunDaemonStatus_NotRunningWritesToCommandOut pins the redirection the
// regression test above depends on: status renders through cmd.OutOrStdout(),
// not the process's real stdout.
func TestRunDaemonStatus_NotRunningWritesToCommandOut(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_DESKTOP_DATA_DIR", t.TempDir())

	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	runDaemonStatus(cmd, nil)

	if !strings.Contains(buf.String(), "Daemon: not running") {
		t.Errorf("output = %q, want it captured from the command's writer", buf.String())
	}
}

// TestStateFileCarriesTheWalls: `loom daemon status` must be able to answer
// "why is nothing spawning", and — the part the 2026-08-31 outage needed —
// whether the wall is the whole account or one broken profile. That answer
// only exists if the walls survive the state file and reach the printer.
func TestStateFileCarriesTheWalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-agents.json")
	until := time.Now().Add(12 * time.Minute)
	walls := []supervisor.WallInfo{
		{Scope: "account", Credential: "shared", Class: "BillingError", Message: "credit balance too low", Until: until},
		{Scope: "profile", Credential: "/w/.loom/agent-profiles/worker-2", Class: "AuthFailure", Until: until},
	}

	if err := writeStateFile(path, time.Now(), nil, nil, nil, nil, 3, stateExtras{Walls: walls}); err != nil {
		t.Fatalf("writeStateFile: %v", err)
	}
	state, err := ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if len(state.Walls) != 2 {
		t.Fatalf("Walls = %#v, want both walls round-tripped", state.Walls)
	}

	out := captureStdout(t, func() { printWalls(state.Walls) })
	for _, want := range []string{"account", "shared", "profile", "worker-2", "AuthFailure", "credit balance too low"} {
		if !strings.Contains(out, want) {
			t.Errorf("printWalls output missing %q:\n%s", want, out)
		}
	}

	// No walls, no section: a healthy daemon's status must stay quiet.
	if got := captureStdout(t, func() { printWalls(nil) }); got != "" {
		t.Errorf("printWalls(nil) printed %q, want nothing", got)
	}
}

// State-file staleness (written_at + degradations) Tests
// ============================================================================

// captureDaemonStdout runs fn with os.Stdout redirected to a pipe and returns
// everything it printed. The display helpers print with fmt, so this is the
// only way to assert on them.
func captureDaemonStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

func TestWriteStateFile_RoundTripsWrittenAtAndDegradations(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "daemon-agents.json")

	degradations := []supervisor.Degradation{
		{
			Kind:    supervisor.DegradationStateWrite,
			Since:   time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC),
			Count:   7,
			LastErr: "write /tmp/daemon-agents.json.tmp: no space left on device",
		},
		{Kind: supervisor.DegradationLogWrite, Since: time.Date(2026, 8, 31, 9, 5, 0, 0, time.UTC), Count: 1},
	}

	before := time.Now()
	if err := writeStateFile(stateFilePath, before, nil, nil, nil, degradations, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}

	result, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if result.WrittenAt.Before(before.Truncate(time.Second)) || result.WrittenAt.After(time.Now()) {
		t.Errorf("WrittenAt = %v, want between %v and now", result.WrittenAt, before)
	}
	if len(result.Degradations) != 2 {
		t.Fatalf("len(Degradations) = %d, want 2", len(result.Degradations))
	}
	got := result.Degradations[0]
	if got.Kind != supervisor.DegradationStateWrite || got.Count != 7 {
		t.Errorf("Degradations[0] = %+v, want state_write/7", got)
	}
	if !got.Since.Equal(degradations[0].Since) {
		t.Errorf("Degradations[0].Since = %v, want %v", got.Since, degradations[0].Since)
	}
	if got.LastErr != degradations[0].LastErr {
		t.Errorf("Degradations[0].LastErr = %q, want %q", got.LastErr, degradations[0].LastErr)
	}

	// A healthy daemon writes no degradations key at all.
	if err := writeStateFile(stateFilePath, before, nil, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile() error = %v", err)
	}
	raw, err := os.ReadFile(stateFilePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.Contains(string(raw), "degradations") {
		t.Errorf("healthy daemon must omit degradations from JSON:\n%s", raw)
	}
	if !strings.Contains(string(raw), "written_at") {
		t.Errorf("written_at must always be present:\n%s", raw)
	}
}

func TestPrintStateFreshness_StaleAndFresh(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "daemon-agents.json")

	stale := &DaemonState{WrittenAt: time.Now().Add(-2 * time.Hour)}
	out := captureDaemonStdout(t, func() { printStateFreshness(stale, stateFilePath) })
	if !strings.Contains(out, "STALE: daemon-agents.json last written") {
		t.Errorf("stale state must print the banner, got %q", out)
	}
	if !strings.Contains(out, "2h0m0s ago") {
		t.Errorf("banner must report the age, got %q", out)
	}

	fresh := &DaemonState{WrittenAt: time.Now().Add(-2 * time.Second)}
	out = captureDaemonStdout(t, func() { printStateFreshness(fresh, stateFilePath) })
	if out != "" {
		t.Errorf("fresh state must print nothing, got %q", out)
	}
}

func TestPrintStateFreshness_OneLinePerDegradation(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "daemon-agents.json")

	state := &DaemonState{
		WrittenAt: time.Now(),
		Degradations: []supervisor.Degradation{
			{
				Kind:    supervisor.DegradationStateWrite,
				Since:   time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC),
				Count:   7,
				LastErr: "no space left on device",
			},
			{Kind: supervisor.DegradationLogWrite, Since: time.Date(2026, 8, 31, 9, 5, 0, 0, time.UTC), Count: 2, LastErr: "permission denied"},
		},
	}

	out := captureDaemonStdout(t, func() { printStateFreshness(state, stateFilePath) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per degradation, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "DEGRADED: state_write since 2026-08-31T09:00:00Z (7 failures): no space left on device") {
		t.Errorf("degradation line 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "DEGRADED: log_write since 2026-08-31T09:05:00Z (2 failures): permission denied") {
		t.Errorf("degradation line 1 = %q", lines[1])
	}
}

// A state file written by a binary predating written_at deserializes with a
// zero WrittenAt. Reporting time.Since(zero) would claim the file is 56 years
// old on every single status call, so the reader falls back to mtime.
func TestPrintStateFreshness_ZeroWrittenAtFallsBackToMtime(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "daemon-agents.json")
	if err := os.WriteFile(stateFilePath, []byte(`{"pid":1,"agents":[]}`), 0600); err != nil {
		t.Fatalf("write legacy state file: %v", err)
	}

	legacy, err := ReadStateFile(stateFilePath)
	if err != nil {
		t.Fatalf("ReadStateFile() error = %v", err)
	}
	if !legacy.WrittenAt.IsZero() {
		t.Fatalf("legacy state file must deserialize with a zero WrittenAt, got %v", legacy.WrittenAt)
	}

	out := captureDaemonStdout(t, func() { printStateFreshness(legacy, stateFilePath) })
	if out != "" {
		t.Errorf("a just-written legacy file is fresh by mtime; want no banner, got %q", out)
	}

	// Backdate the file: mtime is now the only staleness signal there is.
	old := time.Now().Add(-90 * time.Minute)
	if err := os.Chtimes(stateFilePath, old, old); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}
	out = captureDaemonStdout(t, func() { printStateFreshness(legacy, stateFilePath) })
	if !strings.Contains(out, "STALE") || !strings.Contains(out, "1h30m0s ago") {
		t.Errorf("backdated legacy file must be reported stale by mtime, got %q", out)
	}
}

// An unreadable state file yields no age at all — better silence than a
// fabricated staleness claim.
func TestStateFileAge_UnreadableFileHasNoAge(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	if _, ok := stateFileAge(&DaemonState{}, missing); ok {
		t.Error("stateFileAge() ok = true for a missing file, want false")
	}
	out := captureDaemonStdout(t, func() { printStateFreshness(&DaemonState{}, missing) })
	if out != "" {
		t.Errorf("want no output when the age is unknown, got %q", out)
	}
}
