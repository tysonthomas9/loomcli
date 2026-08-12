package automode

import (
	"context"
	"encoding/json"
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

// ============================================================================
// AutoModeOptions Custom Fields Tests
// ============================================================================

func TestAutoModeOptions_CustomFields(t *testing.T) {
	promptCalled := false
	checkCalled := false

	opts := AutoModeOptions{
		Interval:     60,
		MaxTasks:     10,
		IdleTimeout:  30,
		AgentType:    "task",
		AgentName:    "falcon",
		WorktreePath: "/path/to/worktree",
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(agentName string) string {
			promptCalled = true
			return "custom prompt for " + agentName
		},
		CustomTaskCheck: func() (bool, error) {
			checkCalled = true
			return true, nil
		},
	}

	// Verify custom prompt gen works
	result := opts.Prompt("falcon")
	if !promptCalled {
		t.Error("Prompt was not called")
	}
	if result != "custom prompt for falcon" {
		t.Errorf("Prompt returned %q, want %q", result, "custom prompt for falcon")
	}

	// Verify custom task check works
	available, err := opts.CustomTaskCheck()
	if !checkCalled {
		t.Error("CustomTaskCheck was not called")
	}
	if err != nil {
		t.Errorf("CustomTaskCheck returned error: %v", err)
	}
	if !available {
		t.Error("CustomTaskCheck returned false, want true")
	}
}

// ============================================================================
// RunAutoModeLoop Custom Prompt/Task Check Tests
// ============================================================================

func TestRunAutoModeLoop_Prompt(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// execCommand should NOT be called for task checks when CustomTaskCheck is set
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "[]"}
	}})

	var receivedPrompt string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming
		UpdateLockTask(workDir, "mock-custom-1", "Mock Custom Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "custom-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(agentName string) string {
			return "custom prompt for " + agentName
		},
		CustomTaskCheck: func() (bool, error) {
			return true, nil
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Verify custom prompt was used (not default task prompt)
	expectedPrompt := "custom prompt for custom-agent"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Received prompt %q, want %q", receivedPrompt, expectedPrompt)
	}

	// Verify it's NOT the default task prompt
	defaultTaskPrompt := generateTestTaskPrompt("custom-agent")
	if receivedPrompt == defaultTaskPrompt {
		t.Error("Received default task prompt instead of custom prompt")
	}
}

func TestRunAutoModeLoop_CustomFieldsFallback(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITH design (ready for implementation via default task check)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Ready to implement", Status: "open", Design: "Design here"},
			}),
		}
	}})

	var receivedPrompt string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming
		UpdateLockTask(workDir, "mock-fallback-1", "Mock Fallback Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "fallback-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		// Only set Prompt, NOT CustomTaskCheck — Prompt works independently
		Prompt: func(agentName string) string {
			return "custom prompt for " + agentName
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Prompt is decoupled from CustomTaskCheck, so the custom prompt
	// should be used even when CustomTaskCheck is nil (default task check used).
	customPrompt := "custom prompt for fallback-agent"
	if receivedPrompt != customPrompt {
		t.Errorf("Received prompt %q, want custom prompt %q", receivedPrompt, customPrompt)
	}
}

func TestRunAutoModeLoop_CustomTaskCheckOnlyFallback(t *testing.T) {
	// When only CustomTaskCheck is set (not Prompt), CustomTaskCheck is used
	// for task availability, and the default AgentType-based prompt gen is used.
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return tasks (needed for default HasAvailableImplementationTasks fallback)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	var receivedPrompt string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		UpdateLockTask(workDir, "mock-taskcheck-1", "Mock Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "taskcheck-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		// CustomTaskCheck is set independently of Prompt
		CustomTaskCheck: func() (bool, error) {
			return true, nil
		},
		Prompt: func(name string) string {
			return "test-task-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should receive the Prompt prompt
	expectedPrompt := "test-task-prompt-for-taskcheck-agent"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Received prompt %q, want %q", receivedPrompt, expectedPrompt)
	}
}

// ============================================================================
// GetAvailable* Tests
// ============================================================================

func TestGetAvailablePlanningTasks(t *testing.T) {
	tests := []struct {
		name        string
		readyOutput string
		readyErr    error
		wantIDs     []string
		wantErr     bool
	}{
		{
			name: "returns task needing planning (no design)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with needs-revision label",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "existing design", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "excludes task with design and no revision label",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			wantIDs: nil,
		},
		{
			name: "skips in_progress tasks",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name:        "empty list returns empty slice",
			readyOutput: "[]",
			wantIDs:     nil,
		},
		{
			name: "multiple valid tasks returns all",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Has design no revision", Status: "open", Design: "Plan"},
				{ID: "T-3", Title: "Needs revision", Status: "open", Design: "Old plan", Labels: []string{"needs-revision"}},
				{ID: "T-4", Title: "Also no design", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1", "T-3", "T-4"},
		},
		{
			name: "backend pre-filters blocked tasks (only unblocked returned)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "Unblocked task", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0"},
		},
		{
			name: "previously blocked task now unblocked",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Was blocked", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "all returned tasks are unblocked (backend pre-filtered)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "Task A", Status: "open", Design: ""},
				{ID: "T-1", Title: "Task B", Status: "open", Design: ""},
				{ID: "T-2", Title: "Task C", Status: "open", Design: ""},
				{ID: "T-3", Title: "Task D", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0", "T-1", "T-2", "T-3"},
		},
		{
			name:     "issue-store error propagates",
			readyErr: fmt.Errorf("issue-store error"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			var issues []workitems.IssueSummary
			if tt.readyOutput != "" {
				json.Unmarshal([]byte(tt.readyOutput), &issues)
			}
			if tt.readyErr != nil {
				mock.ReadyErr = tt.readyErr
			} else {
				mock.ReadyResult = issues
			}
			setDefaultWorkItems(mock)

			got, err := GetAvailablePlanningTasks(t.Context(), "", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAvailablePlanningTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAvailablePlanningTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAvailablePlanningTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestGetAvailableImplementationTasks(t *testing.T) {
	tests := []struct {
		name        string
		readyOutput string
		readyErr    error
		wantIDs     []string
		wantErr     bool
	}{
		{
			name: "returns task with design ready for implementation",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Implementation plan"},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "excludes task without design",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "excludes task with needs-revision label",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
			}),
			wantIDs: nil,
		},
		{
			name: "skips in_progress tasks",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: "Has design"},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics even with design",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: "Has design"},
			}),
			wantIDs: nil,
		},
		{
			name:        "empty list returns empty slice",
			readyOutput: "[]",
			wantIDs:     nil,
		},
		{
			name: "multiple valid tasks returns all",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Has design", Status: "open", Design: "Plan A"},
				{ID: "T-3", Title: "Needs revision", Status: "open", Design: "Old plan", Labels: []string{"needs-revision"}},
				{ID: "T-4", Title: "Also has design", Status: "open", Design: "Plan B"},
			}),
			wantIDs: []string{"T-2", "T-4"},
		},
		{
			name: "backend pre-filters blocked tasks (only unblocked returned)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "No design", Status: "open", Design: ""},
				{ID: "T-1", Title: "Has design", Status: "open", Design: "Plan"},
			}),
			wantIDs: []string{"T-1"}, // T-0 has no design, not available for implementation
		},
		{
			name: "previously blocked task now unblocked with design",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Was blocked with design", Status: "open", Design: "Plan"},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "all returned tasks checked by predicate (backend pre-filtered blockers)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "No design", Status: "open", Design: ""},
				{ID: "T-1", Title: "Has design A", Status: "open", Design: "Plan A"},
				{ID: "T-2", Title: "Has design B", Status: "open", Design: "Plan B"},
				{ID: "T-3", Title: "Has design C", Status: "open", Design: "Plan C"},
			}),
			wantIDs: []string{"T-1", "T-2", "T-3"}, // T-0 has no design
		},
		{
			name:     "issue-store error propagates",
			readyErr: fmt.Errorf("issue-store error"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			var issues []workitems.IssueSummary
			if tt.readyOutput != "" {
				json.Unmarshal([]byte(tt.readyOutput), &issues)
			}
			if tt.readyErr != nil {
				mock.ReadyErr = tt.readyErr
			} else {
				mock.ReadyResult = issues
			}
			setDefaultWorkItems(mock)

			got, err := GetAvailableImplementationTasks(t.Context(), "", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAvailableImplementationTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAvailableImplementationTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAvailableImplementationTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestGetAnyAvailableTasks(t *testing.T) {
	tests := []struct {
		name        string
		readyOutput string
		readyErr    error
		wantIDs     []string
		wantErr     bool
	}{
		{
			name: "returns task without design",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with design",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with needs-revision label",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "skips in_progress tasks",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name:        "empty list returns empty slice",
			readyOutput: "[]",
			wantIDs:     nil,
		},
		{
			name: "multiple valid tasks returns all",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Big Epic", Status: "open", IssueType: "epic"},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Plan"},
				{ID: "T-4", Title: "In progress", Status: "in_progress", Design: ""},
				{ID: "T-5", Title: "Revision needed", Status: "open", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1", "T-3", "T-5"},
		},
		{
			name: "backend pre-filters blocked tasks (only unblocked returned)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "Unblocked task", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0"},
		},
		{
			name: "previously blocked task now unblocked",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Was blocked", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "all returned tasks are unblocked (backend pre-filtered)",
			readyOutput: mustJSON([]workitems.IssueSummary{
				{ID: "T-0", Title: "Task A", Status: "open", Design: ""},
				{ID: "T-1", Title: "Task B", Status: "open", Design: ""},
				{ID: "T-2", Title: "Task C", Status: "open", Design: "Plan"},
				{ID: "T-3", Title: "Task D", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0", "T-1", "T-2", "T-3"},
		},
		{
			name:     "issue-store error propagates",
			readyErr: fmt.Errorf("issue-store error"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultWorkItems()
			t.Cleanup(resetDefaultWorkItems)
			mock := NewMockWorkItems()
			var issues []workitems.IssueSummary
			if tt.readyOutput != "" {
				json.Unmarshal([]byte(tt.readyOutput), &issues)
			}
			if tt.readyErr != nil {
				mock.ReadyErr = tt.readyErr
			} else {
				mock.ReadyResult = issues
			}
			setDefaultWorkItems(mock)

			got, err := GetAnyAvailableTasks(t.Context(), "", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAnyAvailableTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAnyAvailableTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAnyAvailableTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestHasAvailableDelegatesToGet(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)
	issues := []workitems.IssueSummary{
		{ID: "T-1", Title: "No design", Status: "open", Design: ""},
		{ID: "T-2", Title: "Has design", Status: "open", Design: "Plan"},
	}
	mock := NewMockWorkItems()
	mock.ReadyResult = issues
	setDefaultWorkItems(mock)

	// HasAvailablePlanningTasks should return true (T-1 has no design)
	hasPlan, err := HasAvailablePlanningTasks(t.Context(), "", "")
	if err != nil {
		t.Fatalf("HasAvailablePlanningTasks() error = %v", err)
	}
	if !hasPlan {
		t.Error("HasAvailablePlanningTasks() = false, want true")
	}

	// HasAvailableImplementationTasks should return true (T-2 has design)
	hasImpl, err := HasAvailableImplementationTasks(t.Context(), "", "")
	if err != nil {
		t.Fatalf("HasAvailableImplementationTasks() error = %v", err)
	}
	if !hasImpl {
		t.Error("HasAvailableImplementationTasks() = false, want true")
	}

	// HasAnyAvailableTasks should return true (both T-1 and T-2 are open)
	hasAny, err := HasAnyAvailableTasks(t.Context(), "", "")
	if err != nil {
		t.Fatalf("HasAnyAvailableTasks() error = %v", err)
	}
	if !hasAny {
		t.Error("HasAnyAvailableTasks() = false, want true")
	}

	// Verify Get* returns correct counts
	planTasks, _ := GetAvailablePlanningTasks(t.Context(), "", "")
	if len(planTasks) != 1 || planTasks[0].ID != "T-1" {
		t.Errorf("GetAvailablePlanningTasks() = %v, want [T-1]", planTasks)
	}

	implTasks, _ := GetAvailableImplementationTasks(t.Context(), "", "")
	if len(implTasks) != 1 || implTasks[0].ID != "T-2" {
		t.Errorf("GetAvailableImplementationTasks() = %v, want [T-2]", implTasks)
	}

	anyTasks, _ := GetAnyAvailableTasks(t.Context(), "", "")
	if len(anyTasks) != 2 {
		t.Errorf("GetAnyAvailableTasks() returned %d tasks, want 2", len(anyTasks))
	}
}

func TestRunAutoModeLoop_CodexPlanAgentType(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_PlanAgentType but with codex backend.
	// Verifies that when codex is the active backend, RunAutoModeLoop dispatches
	// to codexNonInteractiveInvoker instead of claudeNonInteractiveInvoker.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	// Track that claude was NOT called
	claudeCalled := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	})

	// Mock codex invoker to capture args
	var receivedPrompt, receivedWorkDir, receivedAgentName string
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedWorkDir = workDir
		receivedPrompt = prompt
		receivedAgentName = agentName
		UpdateLockTask(workDir, "mock-codex-plan-1", "Mock Codex Plan Task")
		return nil
	})

	// Mock execCommand for issue-store ready (return task needing planning)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Needs planning", Status: "open", Design: ""},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "plan",
		AgentName:    "codex-planner",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		Prompt: func(name string) string {
			return "test-plan-prompt-for-" + name
		},
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(t.Context(), opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
	if receivedPrompt == "" {
		t.Error("Codex invoker should have been called")
	}
	// Verify planning prompt was generated
	expectedPrompt := "test-plan-prompt-for-codex-planner"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Codex did not receive planning prompt")
	}
	if receivedWorkDir != tmpDir {
		t.Errorf("Codex received workDir %q, want %q", receivedWorkDir, tmpDir)
	}
	if receivedAgentName != "codex-planner" {
		t.Errorf("Codex received agentName %q, want %q", receivedAgentName, "codex-planner")
	}
}

func TestRunAutoModeLoop_CodexMaxTasks(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_MaxTasksLimit but with codex backend.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	claudeCalled := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	})

	codexInvocations := 0
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		codexInvocations++
		UpdateLockTask(workDir, fmt.Sprintf("mock-codex-%d", codexInvocations), "Mock Codex Task")
		return nil
	})

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     3,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
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

	if codexInvocations != 3 {
		t.Errorf("Codex was invoked %d times, want 3", codexInvocations)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

func TestRunAutoModeLoop_CodexConsecutiveErrors(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_ConsecutiveErrors but with codex backend.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	claudeCalled := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	})

	errorCount := 0
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		errorCount++
		return fmt.Errorf("codex simulated error %d", errorCount)
	})

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
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
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after consecutive errors")
	}

	if errorCount != 3 {
		t.Errorf("Expected 3 consecutive codex errors, got %d", errorCount)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

func TestRunAutoModeLoop_CodexErrorRecovery(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_ErrorRecovery but with codex backend.
	// Pattern: error, error, success (with task claim), error, error, error → exits after 6.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	claudeCalled := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	})

	callNum := 0
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		if callNum == 3 {
			UpdateLockTask(workDir, "mock-codex-recovery", "Mock Codex Recovery Task")
			return nil // Success resets error counter
		}
		return fmt.Errorf("codex error %d", callNum)
	})

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]workitems.IssueSummary{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
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
	case <-time.After(60 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should exit after 6 calls (2 errors, 1 success, 3 consecutive errors)
	if callNum != 6 {
		t.Errorf("Expected 6 codex invocations, got %d", callNum)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

// waitForTmuxPaneStartCommand polls `tmux list-panes` until it returns a
// non-empty start command or the budget expires. Used to avoid races with
// tmux server startup under CI load — fixed sleeps are not enough on
// contended runners.
func waitForTmuxPaneStartCommand(t *testing.T, sessionName string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var lastErr error
	var lastOut string
	for time.Now().Before(deadline) {
		out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
		if err == nil {
			trimmed := strings.TrimSpace(string(out))
			if trimmed != "" {
				return trimmed
			}
			lastOut = trimmed
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for tmux pane start command on %q (budget=%s lastErr=%v lastOut=%q)", sessionName, budget, lastErr, lastOut)
	return ""
}

func TestStartTmuxSession_CodexBackend_NoTermDumb(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	sessionName := fmt.Sprintf("loom-test-codex-%d", os.Getpid())
	// Clean up tmux session after test
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "plan",
		AgentName:    "codex-test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	setTmuxRemainOnExit(t)

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	paneCmd := waitForTmuxPaneStartCommand(t, sessionName, 5*time.Second)

	// Command should NOT contain "TERM=dumb" for codex
	if strings.Contains(paneCmd, "TERM=dumb") {
		t.Errorf("Codex backend should NOT have TERM=dumb prefix, got: %s", paneCmd)
	}
	// Command should contain --backend 'codex' (or --backend codex)
	if !strings.Contains(paneCmd, "--backend") || !strings.Contains(paneCmd, "codex") {
		t.Errorf("Codex backend should have --backend codex flag, got: %s", paneCmd)
	}
	if strings.Contains(paneCmd, "--daemon-mode") {
		t.Errorf("command must not contain retired --daemon-mode, got: %s", paneCmd)
	}
}

func TestStartTmuxSession_ClaudeBackend_HasTermDumb(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	resetBackendState(t)
	RegisterBackend(&ClaudeBackend{})
	if err := SetBackend("claude"); err != nil {
		t.Fatalf("SetBackend('claude') failed: %v", err)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	sessionName := fmt.Sprintf("loom-test-claude-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "plan",
		AgentName:    "claude-test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	setTmuxRemainOnExit(t)

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	paneCmd := waitForTmuxPaneStartCommand(t, sessionName, 5*time.Second)

	// Command SHOULD contain "TERM=dumb" for claude
	if !strings.Contains(paneCmd, "TERM=dumb") {
		t.Errorf("Claude backend should have TERM=dumb prefix, got: %s", paneCmd)
	}
	// Command should always contain --backend flag (explicitly propagated to subprocess)
	if !strings.Contains(paneCmd, "--backend") {
		t.Errorf("Command should contain --backend flag, got: %s", paneCmd)
	}
	if strings.Contains(paneCmd, "--daemon-mode") {
		t.Errorf("command must not contain retired --daemon-mode, got: %s", paneCmd)
	}
}

// TestGetAvailablePlanningTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's AvailabilityQuery.
func TestGetAvailablePlanningTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "T-123",
			wantParentID: "T-123",
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

			_, err := GetAvailablePlanningTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAvailablePlanningTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
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

// TestGetAvailableImplementationTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's AvailabilityQuery.
func TestGetAvailableImplementationTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-456",
			wantParentID: "EPIC-456",
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

			_, err := GetAvailableImplementationTasks(t.Context(), tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAvailableImplementationTasks(t.Context(), %q) unexpected error: %v", tt.parentID, err)
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

// TestGetAnyAvailableTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's AvailabilityQuery.
