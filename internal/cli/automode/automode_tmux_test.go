package automode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestGetAnyAvailableTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-789",
			wantParentID: "EPIC-789",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			issues := []workitems.IssueSummary{{ID: "T-3", Title: "Any task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			var capturedOpts workitems.AvailabilityQuery
			mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultWorkItems(mock)

			_, err := GetAnyAvailableTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAnyAvailableTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("AvailabilityQuery.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
			if capturedOpts.Limit != 10000 {
				t.Errorf("AvailabilityQuery.Limit = %d, want 10000", capturedOpts.Limit)
			}
		})
	}
}

// TestHasAvailablePlanningTasks_WithParentID verifies that HasAvailablePlanningTasks
// properly passes the parentID through to the tracker's AvailabilityQuery.
func TestHasAvailablePlanningTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-100",
			wantParentID: "EPIC-100",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			issues := []workitems.IssueSummary{{ID: "T-1", Title: "Task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			var capturedOpts workitems.AvailabilityQuery
			mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultWorkItems(mock)

			got, err := HasAvailablePlanningTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAvailablePlanningTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAvailablePlanningTasks(t.Context(), %q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("AvailabilityQuery.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestHasAvailableImplementationTasks_WithParentID verifies that HasAvailableImplementationTasks
// properly passes the parentID through to the tracker's AvailabilityQuery.
func TestHasAvailableImplementationTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-200",
			wantParentID: "EPIC-200",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			issues := []workitems.IssueSummary{{ID: "T-2", Title: "Task with design", Status: "open", Design: "Implementation plan"}}
			mock.ReadyResult = issues
			var capturedOpts workitems.AvailabilityQuery
			mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultWorkItems(mock)

			got, err := HasAvailableImplementationTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAvailableImplementationTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAvailableImplementationTasks(t.Context(), %q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("AvailabilityQuery.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestHasAnyAvailableTasks_WithParentID verifies that HasAnyAvailableTasks
// properly passes the parentID through to the tracker's AvailabilityQuery.
func TestHasAnyAvailableTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-300",
			wantParentID: "EPIC-300",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			issues := []workitems.IssueSummary{{ID: "T-3", Title: "Any task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			var capturedOpts workitems.AvailabilityQuery
			mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultWorkItems(mock)

			got, err := HasAnyAvailableTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAnyAvailableTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAnyAvailableTasks(t.Context(), %q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("AvailabilityQuery.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestStartTmuxSession_WithParentID verifies that when ParentID is set in AutoModeOptions,
// the --parent flag is included in the loom command passed to tmux.
func TestStartTmuxSession_WithParentID(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	tests := []struct {
		name           string
		parentID       string
		wantParentFlag bool
	}{
		{
			name:           "with parent ID includes --parent flag",
			parentID:       "EPIC-999",
			wantParentFlag: true,
		},
		{
			name:           "empty parent ID excludes --parent flag",
			parentID:       "",
			wantParentFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logFile := filepath.Join(tmpDir, "test.log")

			sessionName := fmt.Sprintf("loom-test-parent-%d-%d", os.Getpid(), time.Now().UnixNano())
			// Clean up tmux session after test
			t.Cleanup(func() {
				exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
			})

			opts := AutoModeOptions{
				AgentType:    "plan",
				AgentName:    "test-agent",
				WorktreePath: tmpDir,
				ParentID:     tt.parentID,
				BackoffBase:  10 * time.Millisecond,
				TaskPause:    10 * time.Millisecond,
			}

			setTmuxRemainOnExit(t)

			err := startTmuxSession(sessionName, opts, logFile)
			if err != nil {
				t.Fatalf("startTmuxSession failed: %v", err)
			}

			// Give tmux a moment to set up
			time.Sleep(300 * time.Millisecond)

			out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
			if err != nil {
				t.Fatalf("failed to get tmux pane start command: %v", err)
			}
			paneCmd := strings.TrimSpace(string(out))

			if tt.wantParentFlag {
				// The parentID may be shell-quoted, so check for either format
				expectedFlag1 := fmt.Sprintf("--parent %s", tt.parentID)
				expectedFlag2 := fmt.Sprintf("--parent '%s'", tt.parentID)
				if !strings.Contains(paneCmd, expectedFlag1) && !strings.Contains(paneCmd, expectedFlag2) {
					t.Errorf("Expected command to contain --parent flag with %q, got: %s", tt.parentID, paneCmd)
				}
			} else if strings.Contains(paneCmd, "--parent") {
				t.Errorf("Expected command NOT to contain --parent flag, got: %s", paneCmd)
			}
		})
	}
}

// ============================================================================
// fetchReadyIssues Tests (via MockWorkItems)
// ============================================================================

func TestFetchReadyIssues_EmptyResult(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	mock.ReadyResult = []workitems.IssueSummary{}
	setDefaultWorkItems(mock)

	issues, err := fetchReadyIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("fetchReadyIssues() returned %d issues, want 0", len(issues))
	}
}

func TestFetchReadyIssues_ReturnsTrackerResult(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	mock.ReadyResult = []workitems.IssueSummary{
		{ID: "T-1", Title: "First", Status: "open"},
		{ID: "T-2", Title: "Second", Status: "open", Design: "plan"},
		{ID: "T-3", Title: "Third", Status: "open", IssueType: "epic"},
	}
	setDefaultWorkItems(mock)

	issues, err := fetchReadyIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("fetchReadyIssues() returned %d issues, want 3", len(issues))
	}
	if issues[0].ID != "T-1" || issues[1].ID != "T-2" || issues[2].ID != "T-3" {
		t.Errorf("fetchReadyIssues() returned unexpected issue IDs")
	}
}

func TestFetchReadyIssues_TrackerError(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	mock.ReadyErr = fmt.Errorf("command failed")
	setDefaultWorkItems(mock)

	_, err := fetchReadyIssues(t.Context(), "", "")
	if err == nil {
		t.Fatal("fetchReadyIssues() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to check ready tasks") {
		t.Errorf("fetchReadyIssues() error = %v, want to contain 'failed to check ready tasks'", err)
	}
}

func TestFetchReadyIssues_PassesParentID(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)

	_, err := fetchReadyIssues(t.Context(), "epic-123", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-123" {
		t.Errorf("AvailabilityQuery.ParentID = %q, want epic-123", capturedOpts.ParentID)
	}
}

// ============================================================================
// fetchReadyIssues - Repo Label Filtering Tests (via MockWorkItems)
// ============================================================================

func TestFetchReadyIssues_PassesRepoLabel(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)

	_, err := fetchReadyIssues(t.Context(), "", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.Labels) != 1 || capturedOpts.Labels[0] != "repo:frontend" {
		t.Errorf("AvailabilityQuery.Labels = %v, want [repo:frontend]", capturedOpts.Labels)
	}
}

func TestFetchReadyIssues_NoRepoLabel(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)

	_, err := fetchReadyIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.Labels) != 0 {
		t.Errorf("AvailabilityQuery.Labels = %v, want nil/empty", capturedOpts.Labels)
	}
}

func TestFetchReadyIssues_PassesBothFilters(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)

	_, err := fetchReadyIssues(t.Context(), "E-1", "backend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "E-1" {
		t.Errorf("AvailabilityQuery.ParentID = %q, want E-1", capturedOpts.ParentID)
	}
	if len(capturedOpts.Labels) != 1 || capturedOpts.Labels[0] != "repo:backend" {
		t.Errorf("AvailabilityQuery.Labels = %v, want [repo:backend]", capturedOpts.Labels)
	}
}

// ============================================================================
// fetchReadyIssues - Source Repos Filtering Tests (via MockWorkItems)
// ============================================================================

func TestFetchReadyIssues_PassesSourceRepos(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "repo-a,repo-b")

	_, err := fetchReadyIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.SourceRepos) != 2 || capturedOpts.SourceRepos[0] != "repo-a" || capturedOpts.SourceRepos[1] != "repo-b" {
		t.Errorf("AvailabilityQuery.SourceRepos = %v, want [repo-a repo-b]", capturedOpts.SourceRepos)
	}
}

func TestFetchReadyIssues_SourceReposWithParent(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "repo-a")

	_, err := fetchReadyIssues(t.Context(), "epic-123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-123" {
		t.Errorf("AvailabilityQuery.ParentID = %q, want epic-123", capturedOpts.ParentID)
	}
	if len(capturedOpts.SourceRepos) != 1 || capturedOpts.SourceRepos[0] != "repo-a" {
		t.Errorf("AvailabilityQuery.SourceRepos = %v, want [repo-a]", capturedOpts.SourceRepos)
	}
}

func TestFetchReadyIssues_NoSourceRepos(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	mock := NewMockWorkItems()
	var capturedOpts workitems.AvailabilityQuery
	mock.ReadyFn = func(_ context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultWorkItems(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "")

	_, err := fetchReadyIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.SourceRepos) != 0 {
		t.Errorf("AvailabilityQuery.SourceRepos = %v, want nil/empty", capturedOpts.SourceRepos)
	}
}

// fetchUnclosedIssueIDs tests removed: function was removed in WorkItems migration.
// The backend now pre-filters blocked issues in Ready/Blocked endpoints.

// ============================================================================
// RunAutoModeLoop - ConsecutiveNoProgress Tests
// ============================================================================

func TestRunAutoModeLoop_ConsecutiveNoProgress(t *testing.T) {
	// Test that the no-progress path is entered when agent exits without claiming a task.
	// The no-progress backoff is 30s/60s/120s which makes testing 3 full iterations slow,
	// so we verify one iteration and then shutdown during the backoff.
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Agent succeeds but does NOT claim a task (no UpdateLockTask call)
	shutdown := make(chan struct{})
	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdownCh <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Don't write a TaskID — simulates agent that exits without claiming work.
		// After first invocation, send shutdown during the backoff to exit promptly.
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(shutdown)
		}()
		return nil
	})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  500 * time.Millisecond, // Long enough for 100ms shutdown to arrive during backoff
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited after shutdown interrupted the backoff
	case <-time.After(10 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit after no-progress + shutdown")
	}

	// Agent should have been invoked exactly once before shutdown interrupted the backoff
	if claudeInvocations != 1 {
		t.Errorf("Expected 1 Claude invocation, got %d", claudeInvocations)
	}
}

func TestRunAutoModeLoop_NoProgressCounterResetOnSuccess(t *testing.T) {
	// Verify that claiming a task after no-progress resets the counter.
	// We test: no-progress → success (claim) → shutdown during next no-progress backoff.
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	shutdown := make(chan struct{})
	callNum := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdownCh <-chan struct{}, _ *usage.Collector) error {
		callNum++
		if callNum == 1 {
			// First call: no progress (don't claim)
			return nil
		}
		if callNum == 2 {
			// Second call: claim a task → resets counter
			UpdateLockTask(workDir, "mock-progress", "Mock Task")
			return nil
		}
		// Third call: no progress again, signal shutdown so backoff is interrupted
		close(shutdown)
		return nil
	})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have been called 3 times: no-progress(backoff) → claim(pause) → no-progress(shutdown)
	if callNum != 3 {
		t.Errorf("Expected 3 Claude invocations, got %d", callNum)
	}
}

// ============================================================================
// NoProgressBackoff Calculation Tests
// ============================================================================

func TestNoProgressBackoff_Calculation(t *testing.T) {
	tests := []struct {
		consecutiveNoProgress int
		expectedBackoff       time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second}, // Capped
		{4, 120 * time.Second}, // Still capped
		{5, 120 * time.Second}, // Still capped
	}

	for _, tt := range tests {
		backoff := time.Duration(30<<(tt.consecutiveNoProgress-1)) * time.Second
		if backoff > 120*time.Second {
			backoff = 120 * time.Second
		}
		if backoff != tt.expectedBackoff {
			t.Errorf("noProgress=%d: backoff=%v, want %v", tt.consecutiveNoProgress, backoff, tt.expectedBackoff)
		}
	}
}

// ============================================================================
// AutoModeState Field Tests
// ============================================================================

func TestAutoModeState_Fields(t *testing.T) {
	now := time.Now()
	state := AutoModeState{
		TasksCompleted:        5,
		ConsecutiveErrors:     2,
		ConsecutiveNoProgress: 1,
		LastTaskTime:          now,
		IdleStartTime:         now.Add(-time.Minute),
		ShouldExit:            true,
		ExitReason:            "test reason",
	}

	if state.TasksCompleted != 5 {
		t.Errorf("TasksCompleted = %d, want 5", state.TasksCompleted)
	}
	if state.ConsecutiveErrors != 2 {
		t.Errorf("ConsecutiveErrors = %d, want 2", state.ConsecutiveErrors)
	}
	if state.ConsecutiveNoProgress != 1 {
		t.Errorf("ConsecutiveNoProgress = %d, want 1", state.ConsecutiveNoProgress)
	}
	if !state.LastTaskTime.Equal(now) {
		t.Errorf("LastTaskTime = %v, want %v", state.LastTaskTime, now)
	}
	if !state.IdleStartTime.Equal(now.Add(-time.Minute)) {
		t.Errorf("IdleStartTime not set correctly")
	}
	if !state.ShouldExit {
		t.Error("ShouldExit = false, want true")
	}
	if state.ExitReason != "test reason" {
		t.Errorf("ExitReason = %q, want %q", state.ExitReason, "test reason")
	}
}

// ============================================================================
// streamUntilExit - Log File Rotation Tests
// ============================================================================

func TestStreamUntilExit_LogFileRotation(t *testing.T) {
	// Test the truncation detection logic used in streamUntilExit's polling loop
	tmpFile, err := os.CreateTemp("", "loom-rotation-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	fileName := tmpFile.Name()
	defer os.Remove(fileName)

	// Write initial content
	initialContent := "first session output that is long enough\n"
	if _, err := tmpFile.WriteString(initialContent); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}
	tmpFile.Close()

	// Record the offset as if we've read all content
	var lastOffset int64 = int64(len(initialContent))

	// Now truncate and write shorter content (simulates log rotation)
	if err := os.WriteFile(fileName, []byte("new\n"), 0644); err != nil {
		t.Fatalf("failed to truncate and rewrite: %v", err)
	}

	// Use streamRemainingLogContent which handles truncation
	streamRemainingLogContent(fileName, &lastOffset)

	// After truncation handling, offset should be at end of new content
	if lastOffset != 4 { // len("new\n")
		t.Errorf("lastOffset after rotation = %d, want 4", lastOffset)
	}
}

// ============================================================================
// streamUntilExit - Shutdown During Stream (requires tmux)
// ============================================================================

func TestStreamUntilExit_ShutdownDuringStream(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-shutdown-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a long-running tmux session
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 300").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	// Create a log file
	tmpFile, err := os.CreateTemp("", "loom-stream-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	// Send shutdown after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - returned promptly after shutdown
	case <-time.After(5 * time.Second):
		t.Error("streamUntilExit did not return after shutdown signal")
	}
}

// ============================================================================
// streamUntilExit - Session Exit Detection (requires tmux)
// ============================================================================

func TestStreamUntilExit_SessionExitDetection(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-exit-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a tmux session with a short-lived command
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "echo done && sleep 0.5").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "loom-stream-exit-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected session exit
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect session exit")
	}
}

// ============================================================================
// streamUntilExit - Signal File Detection (requires tmux)
// ============================================================================

func TestStreamUntilExit_SignalFileDetection(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-signal-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a long-running tmux session
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 300").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "loom-stream-signal-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	// Use a temp dir as the worktree path so the signal file path is deterministic.
	// Resolve symlinks to match what streamUntilExit does internally
	// (macOS: /var/folders → /private/var/folders).
	worktreePath := t.TempDir()
	if absPath, err := filepath.Abs(worktreePath); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			worktreePath = resolved
		}
	}

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	// Create the signal file after a short delay using the resolved path
	go func() {
		time.Sleep(500 * time.Millisecond)
		signalFile := GetSignalFilePath(worktreePath)
		signalDir := filepath.Dir(signalFile)
		os.MkdirAll(signalDir, 0700)
		os.WriteFile(signalFile, []byte("done"), 0644)
	}()

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, worktreePath, attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected signal file
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect signal file")
	}
}

// ============================================================================
// RunAutoModeTmux - No Tasks Available Tests
// ============================================================================

func TestRunAutoModeTmux_NoTasks(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	shutdown := make(chan struct{})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-no-tasks",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomTaskCheck: func() (bool, error) {
			return false, nil // No tasks
		},
	}

	// Send shutdown after a short delay to exit the idle wait loop
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		RunAutoModeTmux(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited without creating a session
	case <-time.After(5 * time.Second):
		t.Error("RunAutoModeTmux did not exit with no tasks")
	}
}

func TestRunAutoModeTmux_TaskCheckError(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	shutdown := make(chan struct{})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-err",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomTaskCheck: func() (bool, error) {
			return false, fmt.Errorf("simulated task check error")
		},
	}

	// The error path in RunAutoModeTmux uses interruptibleSleep, so shutdown
	// will be honored immediately without waiting for the full 5s backoff.
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		RunAutoModeTmux(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - handled error and exited
	case <-time.After(5 * time.Second):
		t.Error("RunAutoModeTmux did not exit after task check error")
	}
}

// ============================================================================
// RunAutoModeLoop - Lock State Transitions
// ============================================================================

func TestRunAutoModeLoop_LockStateTransitions(t *testing.T) {
	// Verify UpdateLockState is called with StateIdle at loop start, StateActive
	// before agent invocation, and StateIdle after agent completes.
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// In the mock invoker: read lock file and verify State == StateActive
	var stateBeforeAgent string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		info, err := ReadLockFile(workDir)
		if err != nil {
			t.Errorf("ReadLockFile failed during agent invocation: %v", err)
		} else {
			stateBeforeAgent = info.State
		}
		// Simulate claiming a task so the loop counts progress
		UpdateLockTask(workDir, "mock-1", "Mock Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	// Verify state was active during agent invocation
	if stateBeforeAgent != StateActive {
		t.Errorf("State during agent invocation = %q, want %q", stateBeforeAgent, StateActive)
	}

	// After RunAutoModeLoop returns: verify state is idle
	info, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed after loop: %v", err)
	}
	if info.State != StateIdle {
		t.Errorf("State after loop = %q, want %q", info.State, StateIdle)
	}
}

// ============================================================================
// RunAutoModeLoop - ClearsTaskIDBeforeEachSession
// ============================================================================

func TestRunAutoModeLoop_ClearsTaskIDBeforeEachSession(t *testing.T) {
	// Verify ClearLockTaskID is called before each agent invocation, so
	// leftover task IDs from previous sessions don't cause false progress.
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	callNum := 0
	taskIDOnEntry := make([]string, 0, 2)
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		// Read the current TaskID to see if it was cleared before invocation
		info, err := ReadLockFile(workDir)
		if err != nil {
			t.Errorf("ReadLockFile failed on call %d: %v", callNum, err)
		} else {
			taskIDOnEntry = append(taskIDOnEntry, info.TaskID)
		}
		// Claim a task to simulate progress
		UpdateLockTask(workDir, fmt.Sprintf("task-%d", callNum), fmt.Sprintf("Task %d", callNum))
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     2,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	if callNum != 2 {
		t.Fatalf("Expected 2 invocations, got %d", callNum)
	}

	// Both invocations should see an empty TaskID (cleared before each session)
	for i, tid := range taskIDOnEntry {
		if tid != "" {
			t.Errorf("Invocation %d: TaskID on entry = %q, want empty (ClearLockTaskID should have cleared it)", i+1, tid)
		}
	}
}

// ============================================================================
// startTmuxSession - Terminal Dimensions
// ============================================================================

func TestStartTmuxSession_PassesTerminalDimensions(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-dims-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	t.Cleanup(func() {
		cleanupTmuxSession(sessionName)
		deadline := time.Now().Add(2 * time.Second)
		for tmuxSessionExists(sessionName) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	})

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Give tmux a moment to set up
	time.Sleep(300 * time.Millisecond)

	// Query the window dimensions from the created tmux session
	out, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_width} #{window_height}").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to get window dimensions: %v", err)
	}

	dims := strings.TrimSpace(string(out))
	var width, height int
	n, err := fmt.Sscanf(dims, "%d %d", &width, &height)
	if err != nil || n != 2 {
		t.Fatalf("failed to parse tmux window dimensions: %q (err=%v)", dims, err)
	}

	// Verify dimensions are reasonable (> 0)
	if width <= 0 || height <= 0 {
		t.Errorf("tmux window dimensions should be positive: width=%d, height=%d", width, height)
	}

	// If we can get the terminal size, verify the tmux window matches
	if termWidth, termHeight, termErr := getTerminalSize(); termErr == nil && termWidth > 0 && termHeight > 0 {
		if width != termWidth {
			t.Errorf("tmux window width = %d, terminal width = %d (should match)", width, termWidth)
		}
		if height != termHeight {
			t.Errorf("tmux window height = %d, terminal height = %d (should match)", height, termHeight)
		}
	}
}

// ============================================================================
// startTmuxSession - Pipe-Pane and Focus Events
// ============================================================================

func TestStartTmuxSession_PipePaneAndFocusEvents(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("tmux pipe-pane cleanup is process-boundary flaky under -race")
	}
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("loom-test-pipe-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	sessionName := fmt.Sprintf("loom-test-pipe-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	t.Cleanup(func() {
		cleanupTmuxSession(sessionName)
		deadline := time.Now().Add(2 * time.Second)
		for tmuxSessionExists(sessionName) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(tmpDir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Verify focus-events is off (prevents ^[[I and ^[[O in output)
	out, err := exec.Command("tmux", "show-options", "-t", sessionName, "focus-events").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to query focus-events: %v", err)
	}

	focusEvents := strings.TrimSpace(string(out))
	if !strings.Contains(focusEvents, "off") {
		t.Errorf("focus-events should be off, got: %q", focusEvents)
	}
}

// ============================================================================
// streamUntilExit - Zombie Session Cleanup (requires tmux)
// ============================================================================

func TestStreamUntilExit_ZombieSessionCleanup(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-zombie-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a tmux session with a short-lived command. Use "sleep 2" to give
	// us time to set remain-on-exit before the command exits.
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 2").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}
	// Set remain-on-exit BEFORE the command exits so session becomes zombie
	if setErr := exec.Command("tmux", "set", "-t", sessionName, "remain-on-exit", "on").Run(); setErr != nil { //nolint:norawexec
		t.Fatalf("failed to set remain-on-exit: %v", setErr)
	}

	// Wait for command to exit (pane becomes dead)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tmuxPaneDead(sessionName) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !tmuxPaneDead(sessionName) {
		t.Fatal("pane did not die within timeout")
	}

	// Session should still exist (zombie state)
	if !tmuxSessionExists(sessionName) {
		t.Fatal("session should still exist in zombie state")
	}

	tmpFile, err := os.CreateTemp("", "loom-zombie-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected zombie session and cleaned up
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect zombie session")
	}

	// Session should have been cleaned up
	if tmuxSessionExists(sessionName) {
		t.Error("zombie session should have been cleaned up")
	}
}

// ============================================================================
// RunAutoModeLoop - Three Consecutive No-Progress Exits
// ============================================================================

func TestRunAutoModeLoop_ThreeConsecutiveNoProgressExits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test in short mode (requires ~90s for backoff waits)")
	}

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Agent succeeds but does NOT claim a task (no UpdateLockTask call)
	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Don't write a TaskID — simulates agent that exits without claiming work
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - should have exited after 3 consecutive no-progress sessions
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after 3 consecutive no-progress sessions")
	}

	// Should have been invoked exactly 3 times before exiting
	if claudeInvocations != 3 {
		t.Errorf("Expected 3 Claude invocations, got %d", claudeInvocations)
	}
}
