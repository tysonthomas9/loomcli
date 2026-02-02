package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunClaim_Success(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create lock file (claim requires an existing lock)
	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "test-agent",
	}
	lockData, _ := json.Marshal(lockInfo)
	os.WriteFile(filepath.Join(tmpDir, LockFileName), lockData, 0644)

	// Mock bd show command
	taskJSON := `[{"id": "bd-123", "title": "Test Task Title"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-123", "--json"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runClaim(nil, []string{"bd-123"})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify output
	if !strings.Contains(output, "Claimed task: bd-123") {
		t.Errorf("expected 'Claimed task: bd-123' in output, got: %s", output)
	}
	if !strings.Contains(output, "Title: Test Task Title") {
		t.Errorf("expected 'Title: Test Task Title' in output, got: %s", output)
	}

	// Verify lock file was updated with task info
	data, err := os.ReadFile(filepath.Join(tmpDir, LockFileName))
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}
	var updatedLock LockInfo
	if err := json.Unmarshal(data, &updatedLock); err != nil {
		t.Fatalf("failed to parse lock file: %v", err)
	}
	if updatedLock.TaskID != "bd-123" {
		t.Errorf("expected TaskID 'bd-123', got %q", updatedLock.TaskID)
	}
	if updatedLock.TaskTitle != "Test Task Title" {
		t.Errorf("expected TaskTitle 'Test Task Title', got %q", updatedLock.TaskTitle)
	}
}

func TestRunClaim_NoTitle(t *testing.T) {
	// When bd show returns error, claim should still work but without title
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create lock file
	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "test-agent",
	}
	lockData, _ := json.Marshal(lockInfo)
	os.WriteFile(filepath.Join(tmpDir, LockFileName), lockData, 0644)

	// Mock bd show command with error
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-456", "--json"}, Err: errors.New("bd error")},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runClaim(nil, []string{"bd-456"})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should show task ID but not title
	if !strings.Contains(output, "Claimed task: bd-456") {
		t.Errorf("expected 'Claimed task: bd-456' in output, got: %s", output)
	}
	if strings.Contains(output, "Title:") {
		t.Errorf("should not show title when bd show fails, got: %s", output)
	}
}

func TestGetTaskTitle_Success(t *testing.T) {
	taskJSON := `[{"id": "bd-789", "title": "My Task Title"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-789", "--json"}, Stdout: taskJSON},
	})
	mock.Install()

	title := getTaskTitle("bd-789")
	if title != "My Task Title" {
		t.Errorf("expected 'My Task Title', got %q", title)
	}
}

func TestGetTaskTitle_BdError(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-error", "--json"}, Err: errors.New("bd error")},
	})
	mock.Install()

	title := getTaskTitle("bd-error")
	if title != "" {
		t.Errorf("expected empty string on error, got %q", title)
	}
}

func TestGetTaskTitle_InvalidJSON(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-bad", "--json"}, Stdout: "not valid json"},
	})
	mock.Install()

	title := getTaskTitle("bd-bad")
	if title != "" {
		t.Errorf("expected empty string on invalid JSON, got %q", title)
	}
}

func TestGetTaskTitle_EmptyArray(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"show", "bd-empty", "--json"}, Stdout: "[]"},
	})
	mock.Install()

	title := getTaskTitle("bd-empty")
	if title != "" {
		t.Errorf("expected empty string on empty array, got %q", title)
	}
}
