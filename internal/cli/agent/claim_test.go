//go:build ignore

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

	"github.com/tysonthomas9/loomcli/internal/backend"
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
	mockTracker := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Title: "Test Task Title"}}, nil
		},
	}
	setDefaultIssueBackend(mockTracker)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

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

	// Mock IssueTracker returning error
	mockTracker := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			return nil, errors.New("bd error")
		},
	}
	setDefaultIssueBackend(mockTracker)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

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
	mock := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Title: "My Task Title"}}, nil
		},
	}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

	title := getTaskTitle("bd-789")
	if title != "My Task Title" {
		t.Errorf("expected 'My Task Title', got %q", title)
	}
}

func TestGetTaskTitle_BdError(t *testing.T) {
	mock := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			return nil, errors.New("bd error")
		},
	}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

	title := getTaskTitle("bd-error")
	if title != "" {
		t.Errorf("expected empty string on error, got %q", title)
	}
}

func TestGetTaskTitle_NilIssue(t *testing.T) {
	mock := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			return nil, nil
		},
	}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

	title := getTaskTitle("bd-empty")
	if title != "" {
		t.Errorf("expected empty string on nil issue, got %q", title)
	}
}

func TestGetTaskTitle_PassesCorrectID(t *testing.T) {
	var capturedID string
	mock := &MockIssueBackend{
		GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
			capturedID = id
			return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Title: "Test"}}, nil
		},
	}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { setDefaultIssueBackend(nil) })

	getTaskTitle("bd-123")
	if capturedID != "bd-123" {
		t.Errorf("expected GetIssue called with 'bd-123', got %q", capturedID)
	}
}
