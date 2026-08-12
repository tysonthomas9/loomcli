package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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

	// Mock IssueTracker for getTaskTitle
	mockTracker := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return &workitems.IssueDetail{ID: query.IssueID, Title: "Test Task Title"}, nil
		},
	}
	setDefaultWorkItems(mockTracker)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runClaim(commandWithContext(t), []string{"loom-123"})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify output
	if !strings.Contains(output, "Claimed task: loom-123") {
		t.Errorf("expected 'Claimed task: loom-123' in output, got: %s", output)
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
	if updatedLock.TaskID != "loom-123" {
		t.Errorf("expected TaskID 'loom-123', got %q", updatedLock.TaskID)
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

	// Mock IssueTracker returning error
	mockTracker := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return nil, errors.New("issue lookup error")
		},
	}
	setDefaultWorkItems(mockTracker)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runClaim(commandWithContext(t), []string{"loom-456"})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should show task ID but not title
	if !strings.Contains(output, "Claimed task: loom-456") {
		t.Errorf("expected 'Claimed task: loom-456' in output, got: %s", output)
	}
	if strings.Contains(output, "Title:") {
		t.Errorf("should not show title when issue lookup fails, got: %s", output)
	}
}

func TestGetTaskTitle_Success(t *testing.T) {
	mock := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return &workitems.IssueDetail{ID: query.IssueID, Title: "My Task Title"}, nil
		},
	}
	setDefaultWorkItems(mock)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	title := getTaskTitle(t.Context(), "loom-789")
	if title != "My Task Title" {
		t.Errorf("expected 'My Task Title', got %q", title)
	}
}

func TestGetTaskTitle_IssueLookupError(t *testing.T) {
	mock := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return nil, errors.New("issue lookup error")
		},
	}
	setDefaultWorkItems(mock)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	title := getTaskTitle(t.Context(), "loom-error")
	if title != "" {
		t.Errorf("expected empty string on error, got %q", title)
	}
}

func TestGetTaskTitle_NilIssue(t *testing.T) {
	mock := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return nil, nil
		},
	}
	setDefaultWorkItems(mock)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	title := getTaskTitle(t.Context(), "loom-empty")
	if title != "" {
		t.Errorf("expected empty string on nil issue, got %q", title)
	}
}

func TestGetTaskTitle_PassesCorrectID(t *testing.T) {
	var capturedID string
	mock := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			capturedID = query.IssueID
			return &workitems.IssueDetail{ID: query.IssueID, Title: "Test"}, nil
		},
	}
	setDefaultWorkItems(mock)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	getTaskTitle(t.Context(), "loom-123")
	if capturedID != "loom-123" {
		t.Errorf("expected GetIssue called with 'loom-123', got %q", capturedID)
	}
}

func TestRunClaim_LockUpdateFailureIsNonFatal(t *testing.T) {
	// No lock file is created: UpdateLockTask fails with "no active lock to
	// update". The claim must still report success — the lock file is monitor
	// bookkeeping, not the task claim itself. Before this behavior change the
	// error path called cli.ExitWithFlush(1) (os.Exit), so this test also pins
	// that runClaim returns normally.
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(tmpDir, "events"))
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	mockTracker := &MockWorkItems{
		GetFn: func(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
			return &workitems.IssueDetail{ID: query.IssueID, Title: "Test Task Title"}, nil
		},
	}
	setDefaultWorkItems(mockTracker)
	t.Cleanup(func() { setDefaultWorkItems(nil) })

	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	runClaim(commandWithContext(t), []string{"loom-789"})

	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr
	var bufOut, bufErr bytes.Buffer
	bufOut.ReadFrom(rOut)
	bufErr.ReadFrom(rErr)

	if !strings.Contains(bufOut.String(), "Claimed task: loom-789") {
		t.Errorf("expected 'Claimed task: loom-789' in stdout, got: %s", bufOut.String())
	}
	if !strings.Contains(bufErr.String(), "lock file") || !strings.Contains(bufErr.String(), "bookkeeping") {
		t.Errorf("expected non-fatal lock-file bookkeeping note on stderr, got: %s", bufErr.String())
	}
}
