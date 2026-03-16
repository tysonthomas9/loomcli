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
	// Set LOOM_EVENTS_DIR to avoid git rev-parse call in emitTaskClaimedEvent
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(tmpDir, "events"))
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

	// Mock issue tracker
	tracker := &MockIssueTracker{
		GetIssueResult: &BdIssue{Title: "Test Task Title"},
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

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
	// When GetIssue returns error, claim should still work but without title
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	// Set LOOM_EVENTS_DIR to avoid git rev-parse call in emitTaskClaimedEvent
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(tmpDir, "events"))
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

	// Mock issue tracker with error
	tracker := &MockIssueTracker{
		GetIssueErr: errors.New("bd error"),
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

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
		t.Errorf("should not show title when GetIssue fails, got: %s", output)
	}
}

func TestGetTaskTitle_Success(t *testing.T) {
	tracker := &MockIssueTracker{
		GetIssueResult: &BdIssue{Title: "My Task Title"},
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	title := getTaskTitle("bd-789")
	if title != "My Task Title" {
		t.Errorf("expected 'My Task Title', got %q", title)
	}
}

func TestGetTaskTitle_BdError(t *testing.T) {
	tracker := &MockIssueTracker{
		GetIssueErr: errors.New("bd error"),
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	title := getTaskTitle("bd-error")
	if title != "" {
		t.Errorf("expected empty string on error, got %q", title)
	}
}

func TestGetTaskTitle_ParseError(t *testing.T) {
	// GetIssue returning error (replaces invalid JSON scenario)
	tracker := &MockIssueTracker{
		GetIssueErr: errors.New("parse error"),
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	title := getTaskTitle("bd-bad")
	if title != "" {
		t.Errorf("expected empty string on parse error, got %q", title)
	}
}

func TestGetTaskTitle_NilIssue(t *testing.T) {
	// GetIssue returning nil issue with no error (replaces empty array scenario)
	tracker := &MockIssueTracker{
		GetIssueResult: nil,
		GetIssueErr:    nil,
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	title := getTaskTitle("bd-empty")
	if title != "" {
		t.Errorf("expected empty string on nil issue, got %q", title)
	}
}

func TestGetTaskTitle_VerifiesTrackerCall(t *testing.T) {
	// Verify the tracker's GetIssue is called with the correct ID
	tracker := &MockIssueTracker{
		GetIssueResult: &BdIssue{Title: "Dir Test"},
	}
	setDefaultTracker(tracker)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	title := getTaskTitle("bd-123")
	if title != "Dir Test" {
		t.Errorf("expected 'Dir Test', got %q", title)
	}

	// Verify tracker was called with correct args
	if tracker.CallCount("GetIssue") != 1 {
		t.Fatalf("expected 1 GetIssue call, got %d", tracker.CallCount("GetIssue"))
	}
	lastCall := tracker.LastCall("GetIssue")
	if id, ok := lastCall.Args[1].(string); !ok || id != "bd-123" {
		t.Errorf("expected GetIssue called with 'bd-123', got %v", lastCall.Args[1])
	}
}
