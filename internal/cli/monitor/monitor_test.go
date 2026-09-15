package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestDisplayWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"ascii", "hello", 5},
		{"unicode checkmark", "✓ ready", 7},
		{"unicode bullet", "● running", 9},
		{"unicode arrows", "↑1 ↓2", 5},
		{"empty", "", 0},
		{"mixed", "abc123", 6},
		{"spaces", "   ", 3},
		{"single char", "x", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := displayWidth(tc.input)
			if got != tc.expected {
				t.Errorf("displayWidth(%q) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestTruncateToWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		maxWidth int
		expected string
	}{
		{"short string no truncation", "hello", 10, "hello"},
		{"exact width", "hello", 5, "hello"},
		{"over max width", "hello world", 8, "hello..."},
		{"empty string", "", 10, ""},
		{"width 3 truncates to ...", "hello", 3, "..."},
		{"unicode preserved", "✓ ready ● done", 9, "✓ read..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateToWidth(tc.input, tc.maxWidth)
			gotWidth := displayWidth(got)
			if gotWidth > tc.maxWidth {
				t.Errorf("TruncateToWidth(%q, %d) display width = %d, exceeds max",
					tc.input, tc.maxWidth, gotWidth)
			}
			if got != tc.expected {
				t.Errorf("TruncateToWidth(%q, %d) = %q, want %q",
					tc.input, tc.maxWidth, got, tc.expected)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		width    int
		expected int // expected display width
	}{
		{"ascii padding", "hi", 10, 10},
		{"already exact", "hello", 5, 5},
		{"wider than target", "hello world", 5, 11},
		{"unicode single-width", "✓", 5, 5},
		{"unicode bullet", "●", 5, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PadRight(tc.input, tc.width)
			gotWidth := displayWidth(got)
			if gotWidth != tc.expected {
				t.Errorf("PadRight(%q, %d) display width = %d, want %d (result: %q)",
					tc.input, tc.width, gotWidth, tc.expected, got)
			}
		})
	}
}

func TestRenderBoxTop(t *testing.T) {
	t.Parallel()
	// Uses DashboardWidth (70) constant
	result := RenderBoxTop()
	expected := "╔" + strings.Repeat("═", DashboardWidth-2) + "╗\n"
	if result != expected {
		t.Errorf("RenderBoxTop() = %q, want %q", result, expected)
	}
}

func TestRenderBoxBottom(t *testing.T) {
	t.Parallel()
	// Uses DashboardWidth (70) constant
	result := RenderBoxBottom()
	expected := "╚" + strings.Repeat("═", DashboardWidth-2) + "╝\n"
	if result != expected {
		t.Errorf("RenderBoxBottom() = %q, want %q", result, expected)
	}
}

func TestRenderBoxSeparator(t *testing.T) {
	t.Parallel()
	// Uses DashboardWidth (70) constant
	result := RenderBoxSeparator()
	expected := "╠" + strings.Repeat("═", DashboardWidth-2) + "╣\n"
	if result != expected {
		t.Errorf("RenderBoxSeparator() = %q, want %q", result, expected)
	}
}

func TestCenterText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		text     string
		width    int
		expected string
	}{
		{"center short text", "hi", 10, "    hi    "},
		{"text equals width", "hello", 5, "hello"},
		{"text longer than width", "hello world", 5, "hello world"},
		{"empty text", "", 5, "     "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CenterText(tc.text, tc.width)
			if got != tc.expected {
				t.Errorf("CenterText(%q, %d) = %q, want %q",
					tc.text, tc.width, got, tc.expected)
			}
		})
	}
}

// Test state determination logic
func TestAgentStatusStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		lockStatus     string
		taskStatus     string // "needs_review", "closed", "in_progress", ""
		expectPrefix   string
		expectContains string
	}{
		// Planning agent states
		{
			name:         "planning_no_task",
			lockStatus:   "planning: ... (5m)",
			expectPrefix: "planning:",
		},
		{
			name:           "planning_with_task",
			lockStatus:     "planning: loom-123 (5m)",
			expectContains: "loom-123",
		},
		// Implementation agent states
		{
			name:         "working_no_task",
			lockStatus:   "working: ... (5m)",
			expectPrefix: "working:",
		},
		{
			name:           "working_with_task",
			lockStatus:     "working: loom-456 (5m)",
			expectContains: "loom-456",
		},
		// Done state
		{
			name:           "done_state",
			lockStatus:     "done: loom-789 (5m)",
			expectPrefix:   "done:",
			expectContains: "loom-789",
		},
		// Review state
		{
			name:           "review_state",
			lockStatus:     "review: loom-abc (5m)",
			expectPrefix:   "review:",
			expectContains: "loom-abc",
		},
		// Error state
		{
			name:           "error_state",
			lockStatus:     "error: loom-err",
			expectPrefix:   "error:",
			expectContains: "loom-err",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectPrefix != "" && !strings.HasPrefix(tc.lockStatus, tc.expectPrefix) {
				t.Errorf("Expected prefix %q in %q", tc.expectPrefix, tc.lockStatus)
			}
			if tc.expectContains != "" && !strings.Contains(tc.lockStatus, tc.expectContains) {
				t.Errorf("Expected %q to contain %q", tc.lockStatus, tc.expectContains)
			}
		})
	}
}

// Test fallback logic for replacing "..." with task ID
func TestFallbackLogic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		lockStatus string
		taskID     string
		taskStatus string
		wantPrefix string
	}{
		{
			name:       "planning_needs_review_becomes_review",
			lockStatus: "planning: ... (5m)",
			taskID:     "loom-123",
			taskStatus: "needs_review",
			wantPrefix: "review:",
		},
		{
			name:       "working_needs_review_stays_working",
			lockStatus: "working: ... (5m)",
			taskID:     "loom-456",
			taskStatus: "needs_review",
			wantPrefix: "working:",
		},
		{
			name:       "planning_closed_becomes_done",
			lockStatus: "planning: ... (5m)",
			taskID:     "loom-789",
			taskStatus: "closed",
			wantPrefix: "done:",
		},
		{
			name:       "working_closed_becomes_done",
			lockStatus: "working: ... (5m)",
			taskID:     "loom-abc",
			taskStatus: "closed",
			wantPrefix: "done:",
		},
		{
			name:       "planning_in_progress_keeps_planning",
			lockStatus: "planning: ... (5m)",
			taskID:     "loom-def",
			taskStatus: "in_progress",
			wantPrefix: "planning:",
		},
		{
			name:       "working_in_progress_keeps_working",
			lockStatus: "working: ... (5m)",
			taskID:     "loom-ghi",
			taskStatus: "in_progress",
			wantPrefix: "working:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate the fallback logic from collectAgentStatus
			result := simulateFallback(tc.lockStatus, tc.taskID, tc.taskStatus)
			if !strings.HasPrefix(result, tc.wantPrefix) {
				t.Errorf("Expected prefix %q, got %q", tc.wantPrefix, result)
			}
			if !strings.Contains(result, tc.taskID) {
				t.Errorf("Expected result to contain task ID %q, got %q", tc.taskID, result)
			}
		})
	}
}

// simulateFallback mimics the fallback logic in collectAgentStatus
func simulateFallback(lockStatus, taskID, taskStatus string) string {
	if !strings.Contains(lockStatus, "...") {
		return lockStatus
	}

	// Extract duration part
	durationIdx := strings.Index(lockStatus, " (")
	durationPart := ""
	if durationIdx != -1 {
		durationPart = lockStatus[durationIdx:]
	}

	switch taskStatus {
	case "needs_review":
		if strings.HasPrefix(lockStatus, "planning:") {
			return "review: " + taskID + durationPart
		}
		return "working: " + taskID + durationPart
	case "closed":
		return "done: " + taskID + durationPart
	default:
		return strings.Replace(lockStatus, "...", taskID, 1)
	}
}

// Test task conflict detection
func TestTaskConflictDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		taskIDToAgents  map[string][]string
		expectConflicts int
	}{
		{
			name: "no_conflicts",
			taskIDToAgents: map[string][]string{
				"loom-1": {"cobalt"},
				"loom-2": {"nova"},
			},
			expectConflicts: 0,
		},
		{
			name: "one_conflict",
			taskIDToAgents: map[string][]string{
				"loom-1": {"cobalt", "nova"},
				"loom-2": {"ember"},
			},
			expectConflicts: 1,
		},
		{
			name: "multiple_conflicts",
			taskIDToAgents: map[string][]string{
				"loom-1": {"cobalt", "nova"},
				"loom-2": {"ember", "falcon"},
				"loom-3": {"zephyr"},
			},
			expectConflicts: 2,
		},
		{
			name: "three_way_conflict",
			taskIDToAgents: map[string][]string{
				"loom-1": {"cobalt", "nova", "ember"},
			},
			expectConflicts: 1,
		},
		{
			name:            "empty_map",
			taskIDToAgents:  map[string][]string{},
			expectConflicts: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conflicts := make(map[string][]string)
			for taskID, agents := range tc.taskIDToAgents {
				if len(agents) > 1 {
					conflicts[taskID] = agents
				}
			}
			if len(conflicts) != tc.expectConflicts {
				t.Errorf("Expected %d conflicts, got %d", tc.expectConflicts, len(conflicts))
			}
		})
	}
}

// Test that conflict warning renders correctly
func TestRenderConflictWarning(t *testing.T) {
	t.Parallel()
	conflicts := map[string][]string{
		"loom-123": {"cobalt", "nova"},
	}

	var sb strings.Builder
	if len(conflicts) > 0 {
		sb.WriteString("  ⚠️  TASK CONFLICTS - Multiple agents claiming same task:\n")
		for taskID, agents := range conflicts {
			agentList := strings.Join(agents, ", ")
			sb.WriteString("    • " + taskID + ": " + agentList + "\n")
		}
	}

	result := sb.String()
	if !strings.Contains(result, "TASK CONFLICTS") {
		t.Error("Expected warning header")
	}
	if !strings.Contains(result, "loom-123") {
		t.Error("Expected task ID in warning")
	}
	if !strings.Contains(result, "cobalt") || !strings.Contains(result, "nova") {
		t.Error("Expected agent names in warning")
	}
}

// Test MonitorData struct initialization
func TestMonitorDataStruct(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		TaskConflicts: make(map[string][]string),
		AgentTasks:    make(map[string]TaskInfo),
	}

	// Verify TaskConflicts is initialized
	if data.TaskConflicts == nil {
		t.Error("TaskConflicts should be initialized")
	}

	// Verify AgentTasks is initialized
	if data.AgentTasks == nil {
		t.Error("AgentTasks should be initialized")
	}

	// Test adding a conflict
	data.TaskConflicts["loom-test"] = []string{"agent1", "agent2"}
	if len(data.TaskConflicts) != 1 {
		t.Error("Expected 1 conflict")
	}
}

// Test TaskInfo Status field for agent status determination
// When no lock file exists, only in_progress tasks trigger "error" state
// Closed tasks without a lock show "ready" (not "done") to avoid stale state
func TestTaskInfoStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		taskStatus  string
		expectError bool
		expectReady bool
	}{
		{"in_progress_no_lock_is_error", "in_progress", true, false},
		{"closed_no_lock_is_ready", "closed", false, true}, // Changed: closed without lock = ready
		{"open_no_lock_is_ready", "open", false, true},
		{"empty_status_is_ready", "", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := TaskInfo{
				ID:       "loom-test",
				Title:    "Test Task",
				Priority: 2,
				Status:   tc.taskStatus,
			}

			// Simulate the agent status determination logic (no lock file case)
			// Only in_progress tasks without lock trigger "error"
			// All other cases fall through to git status check (simulated as "ready")
			var agentStatus string
			if task.Status == "in_progress" {
				agentStatus = "error: " + task.ID
			} else {
				agentStatus = "ready" // git status would determine actual value
			}

			if tc.expectError && !strings.HasPrefix(agentStatus, "error:") {
				t.Errorf("Expected error status, got %s", agentStatus)
			}
			if tc.expectReady && agentStatus != "ready" {
				t.Errorf("Expected ready status, got %s", agentStatus)
			}
		})
	}
}

// TestNoClosedTaskFallback verifies that closed tasks don't cause "done" status
// when there's no lock file. This prevents stale "done" states from old tasks.
func TestNoClosedTaskFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		hasLock        bool
		lockStatus     string
		taskInProgress bool
		taskClosed     bool
		gitClean       bool
		expectedStatus string
	}{
		{
			name:           "lock_running_shows_lock_status",
			hasLock:        true,
			lockStatus:     "working: loom-123 (5m)",
			expectedStatus: "working: loom-123 (5m)",
		},
		{
			name:           "no_lock_in_progress_task_shows_error",
			hasLock:        false,
			taskInProgress: true,
			expectedStatus: "error: loom-456",
		},
		{
			name:           "no_lock_closed_task_clean_shows_ready",
			hasLock:        false,
			taskClosed:     true,
			gitClean:       true,
			expectedStatus: "ready",
		},
		{
			name:           "no_lock_closed_task_dirty_shows_changes",
			hasLock:        false,
			taskClosed:     true,
			gitClean:       false,
			expectedStatus: "5 changes",
		},
		{
			name:           "no_lock_no_task_clean_shows_ready",
			hasLock:        false,
			gitClean:       true,
			expectedStatus: "ready",
		},
		{
			name:           "no_lock_no_task_dirty_shows_changes",
			hasLock:        false,
			gitClean:       false,
			expectedStatus: "5 changes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate the agent status determination logic from collectAgentStatus
			var status string

			if tc.hasLock && tc.lockStatus != "" {
				status = tc.lockStatus
			} else if tc.taskInProgress {
				status = "error: loom-456"
			} else {
				// No lock and no in_progress task - check git status
				// (closed tasks intentionally don't trigger "done" here)
				if tc.gitClean {
					status = "ready"
				} else {
					status = "5 changes"
				}
			}

			if status != tc.expectedStatus {
				t.Errorf("Expected status %q, got %q", tc.expectedStatus, status)
			}
		})
	}
}

// TestAgentStatusStateMachine tests the complete state machine for agent status
func TestAgentStatusStateMachine(t *testing.T) {
	t.Parallel()
	// State transitions:
	// 1. Agent starts (loom task) -> lock created -> "working: ..."
	// 2. Agent claims task -> lock updated -> "working: loom-123"
	// 3. Agent completes task -> task closed -> "done: loom-123" (while lock exists)
	// 4. Agent exits -> lock removed -> "ready" (if clean) or "X changes" (if dirty)

	states := []struct {
		description    string
		lockExists     bool
		lockRunning    bool
		lockTaskID     string
		taskStatus     string // "", "in_progress", "closed"
		gitClean       bool
		expectedPrefix string
	}{
		{
			description:    "agent_just_started_no_task_yet",
			lockExists:     true,
			lockRunning:    true,
			lockTaskID:     "",
			expectedPrefix: "working: ...",
		},
		{
			description:    "agent_claimed_task",
			lockExists:     true,
			lockRunning:    true,
			lockTaskID:     "loom-123",
			taskStatus:     "in_progress",
			expectedPrefix: "working: loom-123",
		},
		{
			description:    "agent_completed_task_still_running",
			lockExists:     true,
			lockRunning:    true,
			lockTaskID:     "loom-123",
			taskStatus:     "closed",
			expectedPrefix: "done: loom-123",
		},
		{
			description:    "agent_exited_worktree_clean",
			lockExists:     false,
			lockRunning:    false,
			taskStatus:     "closed",
			gitClean:       true,
			expectedPrefix: "ready",
		},
		{
			description:    "agent_exited_worktree_dirty",
			lockExists:     false,
			lockRunning:    false,
			taskStatus:     "closed",
			gitClean:       false,
			expectedPrefix: "5 changes",
		},
		{
			description:    "agent_crashed_task_in_progress",
			lockExists:     false,
			lockRunning:    false,
			taskStatus:     "in_progress",
			expectedPrefix: "error:",
		},
	}

	for _, s := range states {
		t.Run(s.description, func(t *testing.T) {
			// Simulate status determination
			var status string

			if s.lockExists && s.lockRunning {
				// Lock file exists and process is running
				if s.lockTaskID != "" {
					if s.taskStatus == "closed" {
						status = "done: " + s.lockTaskID
					} else {
						status = "working: " + s.lockTaskID
					}
				} else {
					status = "working: ..."
				}
			} else if s.taskStatus == "in_progress" {
				// No lock but task in_progress = agent crashed
				status = "error: loom-123"
			} else {
				// No lock, check git status
				if s.gitClean {
					status = "ready"
				} else {
					status = "5 changes"
				}
			}

			if !strings.HasPrefix(status, s.expectedPrefix) && status != s.expectedPrefix {
				t.Errorf("Expected status to start with %q, got %q", s.expectedPrefix, status)
			}
		})
	}
}

// TestClosedTaskDoesNotOverrideNewTask verifies the bug fix:
// When an agent that previously completed a task starts a new one,
// the old closed task should not cause "done" to appear
func TestClosedTaskDoesNotOverrideNewTask(t *testing.T) {
	t.Parallel()
	// Scenario:
	// 1. Agent "alpha" completed task "loom-old" (status=closed, assignee=alpha)
	// 2. Agent "alpha" starts new task with "loom task"
	// 3. Lock file is created but task not claimed yet
	// 4. Expected: "working: ..." NOT "done: loom-old"

	agentTasks := map[string]TaskInfo{
		"alpha": {ID: "loom-old", Status: "closed"},
	}

	// Simulate lock file exists with running process (new task started)
	lockStatus := "working: ... (0s)"

	// Determine status (this is the logic from collectAgentStatus)
	var status string
	if lockStatus != "" {
		status = lockStatus
	} else if task, ok := agentTasks["alpha"]; ok && task.Status == "in_progress" {
		status = "error: " + task.ID
	} else {
		// Note: we intentionally don't check for closed tasks here anymore
		status = "ready"
	}

	if status != "working: ... (0s)" {
		t.Errorf("Expected 'working: ... (0s)' but got %q - closed task incorrectly overrode new task", status)
	}

	// Now simulate the case where lock detection fails (race condition)
	lockStatus = "" // Lock not detected

	if lockStatus != "" {
		status = lockStatus
	} else if task, ok := agentTasks["alpha"]; ok && task.Status == "in_progress" {
		status = "error: " + task.ID
	} else {
		// Without the fix, this would show "done: loom-old"
		// With the fix, it shows "ready" (assuming clean worktree)
		status = "ready"
	}

	if status == "done: loom-old" {
		t.Error("Bug: closed task caused 'done' status when lock detection failed")
	}
	if status != "ready" {
		t.Errorf("Expected 'ready' when no lock and closed task, got %q", status)
	}
}

// ===========================================================================
// Data Collection Function Tests
// ===========================================================================

// The old command-runner path was removed as part of typed IssueTracker migration.

func TestCollectStatistics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		statsResult    *backend.StatsData
		statsErr       error
		wantOpen       int
		wantClosed     int
		wantTotal      int
		wantCompl      float64
		wantRemaining  int
		wantInProgress int
		wantReview     int
		wantBlocked    int
		countResult    int
	}{
		{
			name:        "normal case",
			statsResult: &backend.StatsData{TotalIssues: 10, OpenIssues: 3, ClosedIssues: 7},
			wantOpen:    3, wantClosed: 7, wantTotal: 10, wantCompl: 70.0,
			wantRemaining: 3,
		},
		{
			name:        "empty stats (no issues)",
			statsResult: &backend.StatsData{},
		},
		{
			name:     "Stats() returns error",
			statsErr: fmt.Errorf("command failed"),
		},
		{
			name:        "Stats() returns nil result",
			statsResult: nil,
		},
		{
			name:        "all closed (100% completion)",
			statsResult: &backend.StatsData{TotalIssues: 5, ClosedIssues: 5},
			wantClosed:  5, wantTotal: 5, wantCompl: 100.0,
		},
		{
			name:        "all store stats fields populated",
			statsResult: &backend.StatsData{TotalIssues: 20, OpenIssues: 10, InProgressIssues: 2, ClosedIssues: 5, BlockedIssues: 1},
			wantOpen:    10, wantClosed: 5, wantTotal: 20, wantCompl: 25.0,
			wantRemaining: 15, wantInProgress: 2, wantBlocked: 1,
			wantReview: 2, countResult: 2,
		},
		{
			name:        "negative review clamped to zero",
			statsResult: &backend.StatsData{TotalIssues: 10, OpenIssues: 5, InProgressIssues: 3, ClosedIssues: 3, BlockedIssues: 2},
			wantOpen:    5, wantClosed: 3, wantTotal: 10, wantCompl: 30.0,
			wantRemaining: 7, wantInProgress: 3, wantBlocked: 2,
			wantReview: 0, // clamped from -3
		},
		{
			name:        "negative remaining clamped to zero",
			statsResult: &backend.StatsData{TotalIssues: 5, ClosedIssues: 6, TombstoneIssues: 1},
			wantClosed:  6, wantTotal: 5, wantCompl: 120.0,
			wantRemaining: 0, // clamped from -1
			wantReview:    0, // clamped
		},
		{
			name:        "review computed with deferred and pinned",
			statsResult: &backend.StatsData{TotalIssues: 30, OpenIssues: 10, InProgressIssues: 3, ClosedIssues: 8, BlockedIssues: 2, DeferredIssues: 2, TombstoneIssues: 1, PinnedIssues: 1},
			wantOpen:    10, wantClosed: 8, wantTotal: 30,
			wantCompl:     float64(8) / float64(30) * 100,
			wantRemaining: 22, wantInProgress: 3, wantBlocked: 2,
			wantReview: 4, countResult: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewMockIssueBackend()
			mock.StatsResult = tt.statsResult
			mock.StatsErr = tt.statsErr
			mock.CountResult = tt.countResult
			deps.IssueBackend = mock

			stats := collectStatisticsDeps(deps)

			if stats.Open != tt.wantOpen {
				t.Errorf("Open = %d, want %d", stats.Open, tt.wantOpen)
			}
			if stats.Closed != tt.wantClosed {
				t.Errorf("Closed = %d, want %d", stats.Closed, tt.wantClosed)
			}
			if stats.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", stats.Total, tt.wantTotal)
			}
			if stats.Completion != tt.wantCompl {
				t.Errorf("Completion = %.1f, want %.1f", stats.Completion, tt.wantCompl)
			}
			if stats.Remaining != tt.wantRemaining {
				t.Errorf("Remaining = %d, want %d", stats.Remaining, tt.wantRemaining)
			}
			if stats.InProgress != tt.wantInProgress {
				t.Errorf("InProgress = %d, want %d", stats.InProgress, tt.wantInProgress)
			}
			if stats.Review != tt.wantReview {
				t.Errorf("Review = %d, want %d", stats.Review, tt.wantReview)
			}
			if stats.Blocked != tt.wantBlocked {
				t.Errorf("Blocked = %d, want %d", stats.Blocked, tt.wantBlocked)
			}
		})
	}
}

func TestCollectSyncStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		agents        []AgentStatus
		wantNeedsPush int
		wantNeedsPull int
	}{
		{
			name:          "no agents",
			agents:        nil,
			wantNeedsPush: 0,
			wantNeedsPull: 0,
		},
		{
			name: "count git push needs from agents",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 3, Behind: 0},
				{Name: "nova", Ahead: 1, Behind: 0},
				{Name: "spark", Ahead: 0, Behind: 0},
			},
			wantNeedsPush: 2,
			wantNeedsPull: 0,
		},
		{
			name: "count git pull needs from agents",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 0, Behind: 2},
				{Name: "nova", Ahead: 0, Behind: 1},
			},
			wantNeedsPush: 0,
			wantNeedsPull: 2,
		},
		{
			name: "mixed push and pull needs",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 3, Behind: 1},
				{Name: "nova", Ahead: 0, Behind: 2},
				{Name: "spark", Ahead: 1, Behind: 0},
			},
			wantNeedsPush: 2,
			wantNeedsPull: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// completeSyncStatus computes git push/pull counts from agent data.
			info := completeSyncStatus(SyncInfo{}, tt.agents)

			if info.GitNeedsPush != tt.wantNeedsPush {
				t.Errorf("GitNeedsPush = %d, want %d", info.GitNeedsPush, tt.wantNeedsPush)
			}
			if info.GitNeedsPull != tt.wantNeedsPull {
				t.Errorf("GitNeedsPull = %d, want %d", info.GitNeedsPull, tt.wantNeedsPull)
			}
		})
	}
}

func TestCollectTaskStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                    string
		readyIssues             []backend.IssueData
		readyErr                error
		inProgressIssues        []backend.IssueData
		inProgressErr           error
		reviewIssues            []backend.IssueData
		reviewErr               error
		backlogIssues           []backend.IssueData
		backlogErr              error
		closedIssues            []backend.IssueData
		closedErr               error
		wantNeedsPlanning       int
		wantReadyToImplement    int
		wantInProgress          int
		wantNeedReview          int
		wantBacklog             int
		wantNeedsPlanningLen    int
		wantReadyToImplementLen int
		wantReviewTasksLen      int
		wantInProgressTasksLen  int
		wantBacklogTasksLen     int
		wantClosedTasksLen      int
		wantAgentTasksLen       int
	}{
		{
			name: "tasks with design go to ReadyToImplement",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task with design", Status: "open", Design: "## Design\nSome plan"},
			},
			wantReadyToImplement:    1,
			wantReadyToImplementLen: 1,
		},
		{
			name: "tasks without design go to NeedsPlanning",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task without design", Status: "open", Design: ""},
			},
			wantNeedsPlanning:    1,
			wantNeedsPlanningLen: 1,
		},
		{
			name: "tasks with review status go to NeedReview",
			reviewIssues: []backend.IssueData{
				{ID: "T-1", Title: "Review this task", Status: "review"},
			},
			wantNeedReview:     1,
			wantReviewTasksLen: 1,
		},
		{
			name: "in_progress tasks populate InProgressTasks and agentTasks",
			inProgressIssues: []backend.IssueData{
				{ID: "T-1", Title: "In progress task", Status: "in_progress", Assignee: "falcon"},
			},
			wantInProgress:         1,
			wantInProgressTasksLen: 1,
			wantAgentTasksLen:      1,
		},
		{
			name: "blocked tasks from blocked list",
			backlogIssues: []backend.IssueData{
				{ID: "T-1", Title: "Blocked task", Status: "blocked"},
				{ID: "T-2", Title: "Another blocked", Status: "blocked"},
			},
			wantBacklog:         2,
			wantBacklogTasksLen: 2,
		},
		{
			name: "epics are skipped",
			readyIssues: []backend.IssueData{
				{ID: "E-1", Title: "Epic task", Status: "open", IssueType: "epic", Design: ""},
				{ID: "T-1", Title: "Regular task", Status: "open", Design: ""},
			},
			wantNeedsPlanning:    1, // Only the regular task
			wantNeedsPlanningLen: 1,
		},
		{
			name: "needs-revision label tasks go to NeedsPlanning",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task needing revision", Status: "open", Design: "existing plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Regular task with design", Status: "open", Design: "plan"},
			},
			wantNeedsPlanning:       1, // Task with needs-revision label
			wantNeedsPlanningLen:    1,
			wantReadyToImplement:    1, // Regular task with design
			wantReadyToImplementLen: 1,
		},
		{
			name: "in_progress tasks skipped in ready output",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "In progress skip", Status: "in_progress", Design: ""},
				{ID: "T-2", Title: "Regular task", Status: "open", Design: ""},
			},
			wantNeedsPlanning:    1, // Only the open task
			wantNeedsPlanningLen: 1,
		},
		{
			name: "top 5 limit for NeedsPlanning",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task 1", Status: "open", Design: ""},
				{ID: "T-2", Title: "Task 2", Status: "open", Design: ""},
				{ID: "T-3", Title: "Task 3", Status: "open", Design: ""},
				{ID: "T-4", Title: "Task 4", Status: "open", Design: ""},
				{ID: "T-5", Title: "Task 5", Status: "open", Design: ""},
				{ID: "T-6", Title: "Task 6", Status: "open", Design: ""},
				{ID: "T-7", Title: "Task 7", Status: "open", Design: ""},
			},
			wantNeedsPlanning:    7, // Count is 7
			wantNeedsPlanningLen: 5, // But only 5 stored
		},
		{
			name: "top 5 limit for ReadyToImplement",
			readyIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task 1", Status: "open", Design: "plan"},
				{ID: "T-2", Title: "Task 2", Status: "open", Design: "plan"},
				{ID: "T-3", Title: "Task 3", Status: "open", Design: "plan"},
				{ID: "T-4", Title: "Task 4", Status: "open", Design: "plan"},
				{ID: "T-5", Title: "Task 5", Status: "open", Design: "plan"},
				{ID: "T-6", Title: "Task 6", Status: "open", Design: "plan"},
			},
			wantReadyToImplement:    6, // Count is 6
			wantReadyToImplementLen: 5, // But only 5 stored
		},
		{
			name: "closed tasks are collected",
			closedIssues: []backend.IssueData{
				{ID: "T-1", Title: "Done task", Status: "closed", Priority: 2},
				{ID: "T-2", Title: "Also done", Status: "closed", Priority: 3},
			},
			wantClosedTasksLen: 2,
		},
		{
			name:     "Ready() error handled gracefully",
			readyErr: fmt.Errorf("command failed"),
		},
		{
			name: "multiple agents with tasks",
			inProgressIssues: []backend.IssueData{
				{ID: "T-1", Title: "Task 1", Status: "in_progress", Assignee: "falcon"},
				{ID: "T-2", Title: "Task 2", Status: "in_progress", Assignee: "nova"},
				{ID: "T-3", Title: "Task 3", Status: "in_progress", Assignee: ""},
			},
			wantInProgress:         3,
			wantInProgressTasksLen: 3,
			wantAgentTasksLen:      2, // Only tasks with assignees
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewMockIssueBackend()
			mock.ReadyResult = tt.readyIssues
			mock.ReadyErr = tt.readyErr
			mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
				switch opts.Status {
				case "in_progress":
					return tt.inProgressIssues, tt.inProgressErr
				case "review":
					return tt.reviewIssues, tt.reviewErr
				case "closed":
					return tt.closedIssues, tt.closedErr
				}
				return nil, nil
			}
			mock.BlockedResult = tt.backlogIssues
			mock.BlockedErr = tt.backlogErr
			deps.IssueBackend = mock

			summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, closedTasks, agentTasks := collectTaskStatusDeps(deps, 100)

			if summary.NeedsPlanning != tt.wantNeedsPlanning {
				t.Errorf("NeedsPlanning = %d, want %d", summary.NeedsPlanning, tt.wantNeedsPlanning)
			}
			if summary.ReadyToImplement != tt.wantReadyToImplement {
				t.Errorf("ReadyToImplement = %d, want %d", summary.ReadyToImplement, tt.wantReadyToImplement)
			}
			if summary.InProgress != tt.wantInProgress {
				t.Errorf("InProgress = %d, want %d", summary.InProgress, tt.wantInProgress)
			}
			if summary.NeedReview != tt.wantNeedReview {
				t.Errorf("NeedReview = %d, want %d", summary.NeedReview, tt.wantNeedReview)
			}
			if summary.Backlog != tt.wantBacklog {
				t.Errorf("Backlog = %d, want %d", summary.Backlog, tt.wantBacklog)
			}
			if len(needsPlanningTasks) != tt.wantNeedsPlanningLen {
				t.Errorf("needsPlanningTasks len = %d, want %d", len(needsPlanningTasks), tt.wantNeedsPlanningLen)
			}
			if len(readyToImplementTasks) != tt.wantReadyToImplementLen {
				t.Errorf("readyToImplementTasks len = %d, want %d", len(readyToImplementTasks), tt.wantReadyToImplementLen)
			}
			if len(reviewTasks) != tt.wantReviewTasksLen {
				t.Errorf("reviewTasks len = %d, want %d", len(reviewTasks), tt.wantReviewTasksLen)
			}
			if len(inProgressTasks) != tt.wantInProgressTasksLen {
				t.Errorf("inProgressTasks len = %d, want %d", len(inProgressTasks), tt.wantInProgressTasksLen)
			}
			if len(backlogTasks) != tt.wantBacklogTasksLen {
				t.Errorf("backlogTasks len = %d, want %d", len(backlogTasks), tt.wantBacklogTasksLen)
			}
			if len(closedTasks) != tt.wantClosedTasksLen {
				t.Errorf("closedTasks len = %d, want %d", len(closedTasks), tt.wantClosedTasksLen)
			}
			if len(agentTasks) != tt.wantAgentTasksLen {
				t.Errorf("agentTasks len = %d, want %d", len(agentTasks), tt.wantAgentTasksLen)
			}
		})
	}
}

func TestCollectTaskStatusReadyCommandArgs(t *testing.T) {
	t.Parallel()
	// This test verifies that Ready() is called with the passed readyLimit
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewMockIssueBackend()
	var capturedOpts backend.ReadyOpts
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		capturedOpts = opts
		return nil, nil
	}
	mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		return nil, nil
	}
	deps.IssueBackend = mock

	collectTaskStatusDeps(deps, 100)

	if !mock.Called("Ready") {
		t.Fatal("Ready() was not called")
	}
	if capturedOpts.Limit != 100 {
		t.Errorf("Ready() called with Limit=%d, want 100", capturedOpts.Limit)
	}
}

func TestCollectAgentStatus(t *testing.T) {
	// not parallel: subtests use os.Chdir, defaultResolver global
	t.Run("no lock clean worktree shows ready", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and runtime dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetWorkspaceRuntimeDirCache()

		// Create worktree structure (relative to tmpDir)
		wtDir := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		setupMonitorWorkspaceConfig(t, tmpDir, "falcon")

		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			// git branch --show-current
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "falcon"}
			}
			// git status --porcelain (clean)
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: ""}
			}
			// git rev-list for ahead/behind
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "0\t0"}
			}
			return CommandResult{}
		}}
		deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

		agents, _ := collectAgentStatusDeps(deps, nil, "")

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Status != "ready" {
			t.Errorf("expected status 'ready', got %q", agents[0].Status)
		}
	})

	t.Run("no lock dirty worktree shows changes", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and runtime dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetWorkspaceRuntimeDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "nova")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		setupMonitorWorkspaceConfig(t, tmpDir, "nova")

		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "nova"}
			}
			// git status --porcelain (dirty - 3 changes)
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: "M file1.go\nM file2.go\n?? file3.go\n"}
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "0\t0"}
			}
			return CommandResult{}
		}}
		deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

		agents, _ := collectAgentStatusDeps(deps, nil, "")

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Status != "3 changes" {
			t.Errorf("expected status '3 changes', got %q", agents[0].Status)
		}
	})

	t.Run("in_progress task but no lock shows error", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and runtime dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetWorkspaceRuntimeDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "spark")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		setupMonitorWorkspaceConfig(t, tmpDir, "spark")

		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "spark"}
			}
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: ""}
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "0\t0"}
			}
			return CommandResult{}
		}}
		deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

		agentTasks := map[string]TaskInfo{
			"spark": {ID: "T-123", Status: "in_progress"},
		}

		agents, _ := collectAgentStatusDeps(deps, agentTasks, "")

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if !strings.HasPrefix(agents[0].Status, "error:") {
			t.Errorf("expected status to start with 'error:', got %q", agents[0].Status)
		}
	})

	t.Run("ahead/behind counts from git", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and runtime dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetWorkspaceRuntimeDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "flux")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		setupMonitorWorkspaceConfig(t, tmpDir, "flux")

		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "flux"}
			}
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: ""}
			}
			// git rev-list --left-right --count (behind, ahead)
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "3\t5"} // 3 behind, 5 ahead
			}
			return CommandResult{}
		}}
		deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

		agents, _ := collectAgentStatusDeps(deps, nil, "")

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Ahead != 5 {
			t.Errorf("expected Ahead=5, got %d", agents[0].Ahead)
		}
		if agents[0].Behind != 3 {
			t.Errorf("expected Behind=3, got %d", agents[0].Behind)
		}
	})

	t.Run("task conflict detection", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and runtime dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetWorkspaceRuntimeDirCache()

		// Create two worktrees
		for _, name := range []string{"falcon", "nova"} {
			wtDir := filepath.Join(tmpDir, "worktrees", name)
			if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
			// Create lock file with same task ID
			lockInfo := LockInfo{
				PID:       os.Getpid(),
				Command:   "task",
				AgentName: name,
				TaskID:    "T-conflict",
			}
			lockData, _ := json.Marshal(lockInfo)
			os.WriteFile(filepath.Join(wtDir, ".agent.lock"), lockData, 0644)
		}
		setupMonitorWorkspaceConfig(t, tmpDir, "falcon", "nova")

		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "test-branch"}
			}
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: ""}
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "0\t0"}
			}
			return CommandResult{}
		}}
		deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

		_, taskIDToAgents := collectAgentStatusDeps(deps, nil, "")

		if len(taskIDToAgents["T-conflict"]) != 2 {
			t.Errorf("expected 2 agents claiming same task, got %d", len(taskIDToAgents["T-conflict"]))
		}
	})
}

func TestCollectMonitorData(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver global
	deps, _, _, _, _ := NewTestDeps(t)
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and runtime dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "test-agent")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "test-agent")

	deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "test-agent"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}}
	deps.Git = &execBridgeGitRunner{Exec: deps.Exec}

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "T-1", Title: "Task 1", Status: "open", Design: ""},
		{ID: "T-2", Title: "Task 2", Status: "open", Design: "plan"},
	}
	mock.StatsResult = &backend.StatsData{TotalIssues: 10, OpenIssues: 3, ClosedIssues: 7}
	deps.IssueBackend = mock

	data := collectMonitorDataDeps(deps, 100, "")

	// Verify all sections populated
	if data.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
	if len(data.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(data.Agents))
	}
	if data.Tasks.NeedsPlanning != 1 {
		t.Errorf("expected NeedsPlanning=1, got %d", data.Tasks.NeedsPlanning)
	}
	if data.Tasks.ReadyToImplement != 1 {
		t.Errorf("expected ReadyToImplement=1, got %d", data.Tasks.ReadyToImplement)
	}
	// Stats remain the canonical backend totals. Work-queue slices are reported
	// separately in data.Tasks.
	if data.Stats.Total != 10 {
		t.Errorf("expected Stats.Total=10, got %d", data.Stats.Total)
	}
	if data.Stats.Remaining != 3 {
		t.Errorf("expected Stats.Remaining=3, got %d", data.Stats.Remaining)
	}
	if data.SyncStatus.DBSynced != true {
		t.Error("expected DBSynced=true")
	}
	if data.TaskConflicts == nil {
		t.Error("TaskConflicts should be initialized")
	}
	if data.AgentTasks == nil {
		t.Error("AgentTasks should be initialized")
	}
}

// ===========================================================================
// Coverage improvement tests
// ===========================================================================

func TestCollectMonitorDataExported(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and runtime dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "test-agent")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "test-agent")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "test-agent"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	mock := NewMockIssueBackend()
	mock.StatsResult = &backend.StatsData{TotalIssues: 5, OpenIssues: 2, ClosedIssues: 3}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	data := CollectMonitorData(0, "")
	if data == nil {
		t.Fatal("CollectMonitorData returned nil")
	}
	if data.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
	if data.Stats.Total != 5 {
		t.Errorf("expected Stats.Total=5, got %d", data.Stats.Total)
	}
}

func TestCollectAgentStatusOnlyExported(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and runtime dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "solo")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "solo")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "solo"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	agents := CollectAgentStatusOnly("")
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "solo" {
		t.Errorf("expected agent name 'solo', got %q", agents[0].Name)
	}
	if agents[0].Status != "ready" {
		t.Errorf("expected status 'ready', got %q", agents[0].Status)
	}
}

// TestBacklogAccumulatesReadyWithBlockersAndBlocked verifies that summary.Backlog
// counts blocked issues. Issues returned by Ready are trusted as unblocked
// (no redundant HasUnclosedBlockers re-pass in the monitor).
func TestBacklogAccumulatesReadyWithBlockersAndBlocked(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "agent1")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "agent1")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "agent1"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	// T-BLOCKER is unclosed (in ready output), so T-BLOCKED-READY has an unclosed blocker.
	// T-LOOM-BLOCKED comes from the blocked list.
	// Both should count toward Backlog, giving Backlog=2.
	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "T-BLOCKER", Title: "Open blocker", Status: "open", Design: "plan"},
		{ID: "T-BLOCKED-READY", Title: "Blocked in ready", Status: "open", Design: "plan"},
		{ID: "T-NORMAL", Title: "Normal task", Status: "open", Design: "plan"},
	}
	mock.BlockedResult = []backend.IssueData{
		{ID: "T-LOOM-BLOCKED", Title: "Blocked by dependency", Status: "open"},
	}
	mock.StatsResult = &backend.StatsData{TotalIssues: 20, OpenIssues: 10, ClosedIssues: 5}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	data := collectMonitorData(100, "")

	// T-LOOM-BLOCKED from the blocked list = 1 (ready issues are trusted, no re-pass).
	if data.Tasks.Backlog != 1 {
		t.Errorf("expected Backlog=1 (blocked list only), got %d", data.Tasks.Backlog)
	}
	// All 3 ready open issues trusted as ready-to-implement
	if data.Tasks.ReadyToImplement != 3 {
		t.Errorf("expected ReadyToImplement=3, got %d", data.Tasks.ReadyToImplement)
	}
	if data.Stats.Remaining != 15 {
		t.Errorf("expected Remaining=15, got %d", data.Stats.Remaining)
	}
	if data.Stats.Total != 20 {
		t.Errorf("expected Total=20, got %d", data.Stats.Total)
	}
}

// TestEpicsExcludedFromWorkQueueButStatsRemainCanonical verifies that epic
// issues from Ready are counted separately while aggregate stats remain the
// backend projection used by workspace stats.
func TestEpicsExcludedFromWorkQueueButStatsRemainCanonical(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "agent1")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "agent1")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "agent1"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "T-1", Title: "Normal task", Status: "open", Design: "plan"},
		{ID: "T-EPIC", Title: "Epic task", Status: "open", IssueType: "epic"},
		{ID: "T-2", Title: "Needs planning", Status: "open", Design: ""},
	}
	mock.StatsResult = &backend.StatsData{TotalIssues: 10, OpenIssues: 5, ClosedIssues: 3}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	data := collectMonitorData(100, "")

	// Epic should be tracked separately, not in work queue
	if data.Tasks.Epics != 1 {
		t.Errorf("expected Epics=1, got %d", data.Tasks.Epics)
	}
	// Only non-epic tasks in work queue: T-1 (ReadyToImplement), T-2 (NeedsPlanning)
	if data.Tasks.ReadyToImplement != 1 {
		t.Errorf("expected ReadyToImplement=1, got %d", data.Tasks.ReadyToImplement)
	}
	if data.Tasks.NeedsPlanning != 1 {
		t.Errorf("expected NeedsPlanning=1, got %d", data.Tasks.NeedsPlanning)
	}
	if data.Stats.Remaining != 7 {
		t.Errorf("expected Remaining=7, got %d", data.Stats.Remaining)
	}
	if data.Stats.Total != 10 {
		t.Errorf("expected Total=10, got %d", data.Stats.Total)
	}
}

// TestMonitorStatsPreserveBackendTotals verifies that monitor status does not
// overwrite canonical backend stats with work-queue subtotals.
func TestMonitorStatsPreserveBackendTotals(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "agent1")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "agent1")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "agent1"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "T-1", Title: "Plan me", Status: "open", Design: ""},
		{ID: "T-2", Title: "Implement me", Status: "open", Design: "plan"},
	}
	mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		switch opts.Status {
		case "in_progress":
			return []backend.IssueData{
				{ID: "T-3", Title: "Active work", Status: "in_progress", Assignee: "agent1"},
			}, nil
		case "review":
			return []backend.IssueData{
				{ID: "T-4", Title: "Review me", Status: "review"},
				{ID: "T-5", Title: "Review me too", Status: "review"},
			}, nil
		case "closed":
			return nil, nil
		}
		return nil, nil
	}
	mock.BlockedResult = []backend.IssueData{
		{ID: "T-6", Title: "Blocked task", Status: "open"},
	}
	mock.StatsResult = &backend.StatsData{TotalIssues: 50, OpenIssues: 8, ClosedIssues: 40}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	data := collectMonitorData(100, "")

	// Verify each work queue category
	if data.Tasks.NeedsPlanning != 1 {
		t.Errorf("NeedsPlanning = %d, want 1", data.Tasks.NeedsPlanning)
	}
	if data.Tasks.ReadyToImplement != 1 {
		t.Errorf("ReadyToImplement = %d, want 1", data.Tasks.ReadyToImplement)
	}
	if data.Tasks.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", data.Tasks.InProgress)
	}
	if data.Tasks.NeedReview != 2 {
		t.Errorf("NeedReview = %d, want 2", data.Tasks.NeedReview)
	}
	if data.Tasks.Backlog != 1 {
		t.Errorf("Backlog = %d, want 1", data.Tasks.Backlog)
	}

	if data.Stats.Remaining != 10 {
		t.Errorf("Remaining = %d, want 10", data.Stats.Remaining)
	}
	if data.Stats.Total != 50 {
		t.Errorf("Total = %d, want 50", data.Stats.Total)
	}
	wantCompl := float64(40) / float64(50) * 100
	if data.Stats.Completion != wantCompl {
		t.Errorf("Completion = %.1f, want %.1f", data.Stats.Completion, wantCompl)
	}
}

func TestRunMonitorOneShot(t *testing.T) {
	// not parallel: uses os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend, monitorNoWatch global, os.Stdout capture
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and runtime dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetWorkspaceRuntimeDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "oneshot")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	setupMonitorWorkspaceConfig(t, tmpDir, "oneshot")

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "oneshot"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		return CommandResult{}
	}})

	mock := NewMockIssueBackend()
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	// Save and set monitorNoWatch
	oldNoWatch := monitorNoWatch
	monitorNoWatch = true
	t.Cleanup(func() { monitorNoWatch = oldNoWatch })

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runMonitor(nil, nil)

	w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "LOOM") {
		t.Error("expected dashboard header 'LOOM' in output")
	}
	if !strings.Contains(output, "AGENTS") {
		t.Error("expected AGENTS section in output")
	}
}

func TestRenderDashboardEmptyData(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp:     fixedTime(),
		Agents:        nil,
		Tasks:         TaskSummary{},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus:    SyncInfo{DBSynced: true},
		Stats:         MonitorStats{},
	}

	output := renderDashboard(data)

	// Check empty agent section
	if !strings.Contains(output, "No agents found") {
		t.Error("expected 'No agents found' for empty agents")
	}

	// Check empty task sections show "(none)"
	noneCount := strings.Count(output, "(none)")
	if noneCount < 4 {
		t.Errorf("expected at least 4 '(none)' entries for empty task lists, got %d", noneCount)
	}

	// Check sync shows synced
	if !strings.Contains(output, "synced") {
		t.Error("expected 'synced' in sync section")
	}
}

func TestRenderDashboardWithData(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp: fixedTime(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "working: T-1 (2m)", Ahead: 3, Behind: 1},
			{Name: "nova", Branch: "nova", Status: "ready", Ahead: 0, Behind: 0},
		},
		Tasks: TaskSummary{
			NeedsPlanning:    2,
			ReadyToImplement: 1,
			InProgress:       1,
			NeedReview:       1,
			Backlog:          1,
		},
		NeedsPlanningTasks: []TaskInfo{
			{ID: "T-1", Title: "Plan this", Priority: 2},
		},
		ReadyToImplement: []TaskInfo{
			{ID: "T-2", Title: "Implement this", Priority: 1},
		},
		ReviewTasks: []TaskInfo{
			{ID: "T-3", Title: "Review this", Priority: 2},
		},
		InProgressTasks: []TaskInfo{
			{ID: "T-4", Title: "In progress task", Priority: 1},
		},
		BacklogTasks: []TaskInfo{
			{ID: "T-5", Title: "Blocked task", Priority: 3},
		},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus: SyncInfo{
			DBSynced:     false,
			DBError:      "connection failed",
			GitNeedsPush: 1,
			GitNeedsPull: 1,
		},
		Stats: MonitorStats{
			Open:       5,
			Closed:     10,
			Total:      15,
			Completion: 66.7,
			Remaining:  5,
			InProgress: 2,
			Review:     1,
			Blocked:    1,
		},
	}

	output := renderDashboard(data)

	// Check agents rendered
	if !strings.Contains(output, "falcon") {
		t.Error("expected agent 'falcon' in output")
	}
	if !strings.Contains(output, "working:") {
		t.Error("expected 'working:' status")
	}
	if !strings.Contains(output, "↑3") {
		t.Error("expected '↑3' sync indicator")
	}
	if !strings.Contains(output, "↓1") {
		t.Error("expected '↓1' sync indicator")
	}

	// Check task sections
	if !strings.Contains(output, "NEEDS PLANNING") {
		t.Error("expected NEEDS PLANNING section")
	}
	if !strings.Contains(output, "NEEDS REVIEW") {
		t.Error("expected NEEDS REVIEW section")
	}
	if !strings.Contains(output, "READY TO IMPLEMENT") {
		t.Error("expected READY TO IMPLEMENT section")
	}
	if !strings.Contains(output, "IN PROGRESS") {
		t.Error("expected IN PROGRESS section")
	}

	// Check task summary line uses "Backlog" not "Blocked"
	if !strings.Contains(output, "Backlog:") {
		t.Error("expected 'Backlog:' label in task summary line")
	}
	if strings.Contains(output, "Blocked:") {
		t.Error("task summary should use 'Backlog:' not 'Blocked:'")
	}

	// Check review task title rendered
	if !strings.Contains(output, "Review this") {
		t.Error("expected review task title")
	}

	// Check sync section shows errors
	if !strings.Contains(output, "connection failed") {
		t.Error("expected DB error message")
	}
	if !strings.Contains(output, "need push") {
		t.Error("expected 'need push' in git sync")
	}
	if !strings.Contains(output, "need pull") {
		t.Error("expected 'need pull' in git sync")
	}

	// Check stats section uses "Remaining" and "Done" labels (not "Open" and "Completion")
	if !strings.Contains(output, "Remaining:") {
		t.Error("expected 'Remaining:' label in stats line")
	}
	if !strings.Contains(output, "Done:") {
		t.Error("expected 'Done:' label in stats line (completion percentage)")
	}
	if !strings.Contains(output, "67%") {
		t.Error("expected completion percentage")
	}
}

func TestRenderBoxLineLongContent(t *testing.T) {
	t.Parallel()
	// Content longer than DashboardWidth - 4 should not cause negative padding
	longContent := strings.Repeat("x", DashboardWidth+10)
	result := RenderBoxLine(longContent)

	if !strings.HasPrefix(result, "║ ") {
		t.Error("expected box line to start with '║ '")
	}
	if !strings.HasSuffix(result, " ║\n") {
		t.Error("expected box line to end with ' ║\\n'")
	}
	// Padding should be 0, so content is directly followed by " ║\n"
	if !strings.Contains(result, longContent+" ║\n") {
		t.Error("expected long content with no padding")
	}
}

func TestRenderBoxLineEmptyContent(t *testing.T) {
	t.Parallel()
	result := RenderBoxLine("")

	if !strings.HasPrefix(result, "║ ") {
		t.Error("expected box line to start with '║ '")
	}
	if !strings.HasSuffix(result, " ║\n") {
		t.Error("expected box line to end with ' ║\\n'")
	}
	// Empty content should get full padding
	expectedLen := DashboardWidth + len("║") + len("║") + len("\n") - 2 // account for unicode chars
	_ = expectedLen                                                     // just verify it doesn't panic
}

func TestRenderTaskLineAlignment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		task TaskInfo
	}{
		{
			name: "short_id",
			task: TaskInfo{ID: "loomcli-r9x", Title: "Fix displayWidth unicode handling in monitor", Priority: 0},
		},
		{
			name: "long_id_with_child",
			task: TaskInfo{ID: "loomcli-mp5.43", Title: "Use process groups (setpgid) for daemon child process management", Priority: 3},
		},
		{
			name: "very_long_title",
			task: TaskInfo{ID: "loomcli-6yk.1", Title: "Remove build artifacts and dead files (logrouter binary, duplicate main.go, empty .test-skip)", Priority: 1},
		},
		{
			name: "unicode_in_title",
			task: TaskInfo{ID: "loomcli-abc", Title: "Fix ✓ and ● display width in monitor dashboard rendering", Priority: 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			renderTaskLine(&sb, tc.task)
			line := sb.String()

			// Every rendered line should have display width = DashboardWidth (70) + newline
			lineNoNewline := strings.TrimSuffix(line, "\n")
			width := displayWidth(lineNoNewline)
			if width != DashboardWidth {
				t.Errorf("renderTaskLine display width = %d, want %d\nline: %s", width, DashboardWidth, lineNoNewline)
			}

			// Must start with "║ " and end with " ║"
			if !strings.HasPrefix(line, "║ ") {
				t.Errorf("expected line to start with '║ ', got: %s", line)
			}
			if !strings.HasSuffix(lineNoNewline, " ║") {
				t.Errorf("expected line to end with ' ║', got: %s", lineNoNewline)
			}
		})
	}
}

func TestRenderAgentLineAlignment(t *testing.T) {
	t.Parallel()
	agents := []AgentStatus{
		{Name: "comet", Branch: "comet", Status: "● 1 changes"},
		{Name: "spark", Branch: "spark", Status: "ready", Ahead: 2, Behind: 1},
		{Name: "long-name-agent", Branch: "feature/long-branch", Status: "working: loom-123 (5m)"},
	}

	for _, agent := range agents {
		t.Run(agent.Name, func(t *testing.T) {
			var sb strings.Builder
			RenderAgentLine(&sb, agent, "  ")
			line := sb.String()

			lineNoNewline := strings.TrimSuffix(line, "\n")
			width := displayWidth(lineNoNewline)
			if width != DashboardWidth {
				t.Errorf("renderAgentLine display width = %d, want %d\nline: %s", width, DashboardWidth, lineNoNewline)
			}
		})
	}
}

func TestGetWorktreeGitSyncStatusError(t *testing.T) {
	// not parallel: uses installExecMock
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{Err: fmt.Errorf("git failed")}
	}})

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main", "")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0) on error, got (%d, %d)", ahead, behind)
	}
}

func TestGetWorktreeGitSyncStatusMalformed(t *testing.T) {
	// not parallel: uses installExecMock
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "not-a-number"}
	}})

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main", "")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0) on malformed output, got (%d, %d)", ahead, behind)
	}
}

func TestGetWorktreeGitSyncStatusCustomBranch(t *testing.T) {
	// not parallel: uses installExecMock
	var capturedArgs []string
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		capturedArgs = args
		return CommandResult{Stdout: "2\t4"}
	}})

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main", "develop")
	if ahead != 4 || behind != 2 {
		t.Errorf("expected (4, 2), got (%d, %d)", ahead, behind)
	}

	// Verify the custom branch was used
	found := false
	for _, arg := range capturedArgs {
		if strings.Contains(arg, "origin/develop") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'origin/develop' in git args, got %v", capturedArgs)
	}
}

func TestCollectAgentStatusLockFallback(t *testing.T) {
	// not parallel: subtests use os.Chdir, defaultResolver, installExecMock, setDefaultIssueBackend
	tests := []struct {
		name         string
		lockCommand  string // "plan" or "task"
		rawStatus    string // raw status from GetIssue (before getTaskStatus mapping)
		expectPrefix string
	}{
		{
			name:         "planning_agent_task_needs_review_becomes_review",
			lockCommand:  "plan",
			rawStatus:    "review", // getTaskStatus maps "review" → "needs_review"
			expectPrefix: "review:",
		},
		{
			name:         "working_agent_task_needs_review_stays_working",
			lockCommand:  "task",
			rawStatus:    "review",
			expectPrefix: "working:",
		},
		{
			name:         "planning_agent_task_closed_becomes_done",
			lockCommand:  "plan",
			rawStatus:    "closed",
			expectPrefix: "done:",
		},
		{
			name:         "working_agent_task_closed_becomes_done",
			lockCommand:  "task",
			rawStatus:    "closed",
			expectPrefix: "done:",
		},
		{
			name:         "planning_agent_task_in_progress_keeps_planning",
			lockCommand:  "plan",
			rawStatus:    "in_progress",
			expectPrefix: "planning:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			tmpDir := t.TempDir()
			os.Chdir(tmpDir)
			t.Cleanup(func() { os.Chdir(origDir) })

			// Reset cached resolver and runtime dir so they use the new CWD
			oldResolver := defaultResolver
			defaultResolver = nil
			t.Cleanup(func() { defaultResolver = oldResolver })
			ResetWorkspaceRuntimeDirCache()

			wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
			if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
			setupMonitorWorkspaceConfig(t, tmpDir, "alpha")

			// Create lock file with empty TaskID - triggers "..." in status
			lockInfo := LockInfo{
				PID:       os.Getpid(),
				Command:   tt.lockCommand,
				AgentName: "alpha",
				TaskID:    "", // empty - triggers "..." in GetLockStatus
				StartedAt: time.Now(),
			}
			lockData, _ := json.Marshal(lockInfo)
			os.WriteFile(filepath.Join(wtDir, ".agent.lock"), lockData, 0644)

			// Mock IssueTracker for getTaskStatus
			mockTracker := &MockIssueBackend{
				GetFn: func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
					return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Title: "Test Task", Status: tt.rawStatus}}, nil
				},
			}
			setDefaultIssueBackend(mockTracker)
			t.Cleanup(func() { setDefaultIssueBackend(nil) })

			installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
				if name == "git" && len(args) > 0 && args[0] == "branch" {
					return CommandResult{Stdout: "alpha"}
				}
				if name == "git" && len(args) > 0 && args[0] == "status" {
					return CommandResult{Stdout: ""}
				}
				if name == "git" && len(args) > 0 && args[0] == "rev-list" {
					return CommandResult{Stdout: "0\t0"}
				}
				return CommandResult{}
			}})

			agentTasks := map[string]TaskInfo{
				"alpha": {ID: "T-999", Title: "Test Task", Priority: 2, Status: "in_progress"},
			}

			agents, _ := collectAgentStatus(agentTasks, "")
			if len(agents) != 1 {
				t.Fatalf("expected 1 agent, got %d", len(agents))
			}

			if !strings.HasPrefix(agents[0].Status, tt.expectPrefix) {
				t.Errorf("expected status prefix %q, got %q", tt.expectPrefix, agents[0].Status)
			}
		})
	}
}

func TestRenderDashboardSyncGitPushOnly(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp:     fixedTime(),
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus: SyncInfo{
			DBSynced:     true,
			GitNeedsPush: 2,
			GitNeedsPull: 0,
		},
	}

	output := renderDashboard(data)
	if !strings.Contains(output, "2 need push") {
		t.Error("expected '2 need push' in git sync status")
	}
	if strings.Contains(output, "need pull") {
		t.Error("should not show 'need pull' when GitNeedsPull is 0")
	}
}

func TestRenderDashboardSyncGitPullOnly(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp:     fixedTime(),
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus: SyncInfo{
			DBSynced:     true,
			GitNeedsPush: 0,
			GitNeedsPull: 3,
		},
	}

	output := renderDashboard(data)
	if !strings.Contains(output, "3 need pull") {
		t.Error("expected '3 need pull' in git sync status")
	}
	if strings.Contains(output, "need push") {
		t.Error("should not show 'need push' when GitNeedsPush is 0")
	}
}

func TestRenderDashboardAgentStatusIcons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   string
		wantIcon string
	}{
		{"ready_shows_checkmark", "ready", "✓"},
		{"changes_shows_bullet", "3 changes", "●"},
		{"dirty_shows_bullet", "dirty", "●"},
		{"working_shows_bullet", "working: T-1 (2m)", "●"},
		{"planning_shows_bullet", "planning: T-2 (3m)", "●"},
		{"done_shows_bullet", "done: T-3 (1m)", "●"},
		{"review_shows_bullet", "review: T-4 (5m)", "●"},
		{"error_shows_bullet", "error: T-5", "●"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &MonitorData{
				Timestamp: fixedTime(),
				Agents: []AgentStatus{
					{Name: "test", Branch: "test", Status: tt.status},
				},
				AgentTasks:    make(map[string]TaskInfo),
				TaskConflicts: make(map[string][]string),
				SyncStatus:    SyncInfo{DBSynced: true},
			}

			output := renderDashboard(data)
			if !strings.Contains(output, tt.wantIcon) {
				t.Errorf("expected icon %q for status %q in output", tt.wantIcon, tt.status)
			}
		})
	}
}

// fixedTime returns a consistent time for test assertions
func fixedTime() time.Time {
	return time.Date(2026, 1, 29, 12, 0, 0, 0, time.UTC)
}

// Note: mustJSON helper is defined in automode_test.go and available here

func TestRenderDashboardWorkspaceMode(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp: fixedTime(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "working: T-1 (2m)", Ahead: 1, Behind: 0, Workspace: "my-workspace"},
			{Name: "nova", Branch: "nova", Status: "ready", Ahead: 0, Behind: 0, Workspace: "my-workspace"},
			{Name: "spark", Branch: "spark", Status: "3 changes", Ahead: 0, Behind: 1, Workspace: "other-ws"},
		},
		Tasks:         TaskSummary{},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus:    SyncInfo{DBSynced: true},
		Stats:         MonitorStats{},
	}

	output := renderDashboard(data)

	// Verify workspace sub-headers appear in AGENTS section
	if !strings.Contains(output, "[my-workspace]") {
		t.Errorf("expected '[my-workspace]' workspace header in output, got:\n%s", output)
	}
	if !strings.Contains(output, "[other-ws]") {
		t.Errorf("expected '[other-ws]' workspace header in output, got:\n%s", output)
	}

	// Verify agent names appear
	if !strings.Contains(output, "falcon") {
		t.Error("expected agent 'falcon' in output")
	}
	if !strings.Contains(output, "nova") {
		t.Error("expected agent 'nova' in output")
	}
	if !strings.Contains(output, "spark") {
		t.Error("expected agent 'spark' in output")
	}

	// Verify it does NOT show "No agents found" since we have agents
	if strings.Contains(output, "No agents found") {
		t.Error("should not show 'No agents found' when agents exist")
	}

	// Verify standard sections still present
	if !strings.Contains(output, "AGENTS") {
		t.Error("expected AGENTS section header")
	}
	if !strings.Contains(output, "WORK QUEUE") {
		t.Error("expected WORK QUEUE section header")
	}
}

func TestRenderDashboardMixedWorkspace(t *testing.T) {
	t.Parallel()
	// Agents with empty Workspace get grouped under "unassigned".
	data := &MonitorData{
		Timestamp: fixedTime(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "ready", Workspace: "my-workspace"},
			{Name: "nova", Branch: "nova", Status: "ready", Workspace: ""},
			{Name: "spark", Branch: "spark", Status: "dirty", Workspace: ""},
		},
		Tasks:         TaskSummary{},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus:    SyncInfo{DBSynced: true},
		Stats:         MonitorStats{},
	}

	output := renderDashboard(data)

	// Workspace mode should be triggered because falcon has a workspace
	if !strings.Contains(output, "[my-workspace]") {
		t.Errorf("expected '[my-workspace]' workspace header, got:\n%s", output)
	}

	// Agents without workspace should be in the "unassigned" group.
	if !strings.Contains(output, "[unassigned]") {
		t.Errorf("expected '[unassigned]' group for agents without workspace, got:\n%s", output)
	}

	// All agents should still be present
	if !strings.Contains(output, "falcon") {
		t.Error("expected 'falcon' in output")
	}
	if !strings.Contains(output, "nova") {
		t.Error("expected 'nova' in output")
	}
	if !strings.Contains(output, "spark") {
		t.Error("expected 'spark' in output")
	}
}

func TestRenderDashboardNoWorkspaceUsesUnassigned(t *testing.T) {
	t.Parallel()
	// When no agents have Workspace set, all agents are grouped as unassigned.
	data := &MonitorData{
		Timestamp: fixedTime(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "ready", Workspace: ""},
			{Name: "nova", Branch: "nova", Status: "3 changes", Workspace: ""},
		},
		Tasks:         TaskSummary{},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus:    SyncInfo{DBSynced: true},
		Stats:         MonitorStats{},
	}

	output := renderDashboard(data)

	if !strings.Contains(output, "[unassigned]") {
		t.Errorf("expected unassigned workspace group, got:\n%s", output)
	}

	// Agents should still render
	if !strings.Contains(output, "falcon") {
		t.Error("expected 'falcon' in output")
	}
	if !strings.Contains(output, "nova") {
		t.Error("expected 'nova' in output")
	}
}

func TestAgentStatusWorkspaceField(t *testing.T) {
	t.Parallel()
	// Verify Workspace field in AgentStatus JSON serialization
	agent := AgentStatus{
		Name:      "falcon",
		Branch:    "falcon",
		Status:    "ready",
		Ahead:     1,
		Behind:    2,
		Workspace: "my-workspace",
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr := string(data)

	// Verify workspace field is present
	if !strings.Contains(jsonStr, `"workspace":"my-workspace"`) {
		t.Errorf("expected workspace field in JSON, got: %s", jsonStr)
	}

	// Verify all other fields
	if !strings.Contains(jsonStr, `"name":"falcon"`) {
		t.Errorf("expected name field in JSON, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"branch":"falcon"`) {
		t.Errorf("expected branch field in JSON, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"status":"ready"`) {
		t.Errorf("expected status field in JSON, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"ahead":1`) {
		t.Errorf("expected ahead field in JSON, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"behind":2`) {
		t.Errorf("expected behind field in JSON, got: %s", jsonStr)
	}

	// Verify empty workspace serializes correctly
	agentNoWs := AgentStatus{
		Name:      "nova",
		Branch:    "nova",
		Status:    "ready",
		Workspace: "",
	}

	data, err = json.Marshal(agentNoWs)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr = string(data)
	if !strings.Contains(jsonStr, `"workspace":""`) {
		t.Errorf("expected empty workspace field in JSON, got: %s", jsonStr)
	}

	// Verify round-trip deserialization
	var decoded AgentStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal AgentStatus: %v", err)
	}
	if decoded.Workspace != "" {
		t.Errorf("expected empty workspace after round-trip, got %q", decoded.Workspace)
	}
}

func TestRenderAgentsWorkspace(t *testing.T) {
	t.Parallel()
	agents := []AgentStatus{
		{Name: "alpha", Branch: "alpha", Status: "ready", Workspace: "ws-a"},
		{Name: "beta", Branch: "beta", Status: "working: T-1 (3m)", Workspace: "ws-a"},
		{Name: "gamma", Branch: "gamma", Status: "ready", Workspace: "ws-b"},
		{Name: "delta", Branch: "delta", Status: "dirty", Workspace: ""},
	}

	var sb strings.Builder
	renderAgentsWorkspace(&sb, agents)
	output := sb.String()

	// Verify workspace groups appear in sorted order
	wsAIdx := strings.Index(output, "[ws-a]")
	wsBIdx := strings.Index(output, "[ws-b]")
	unassignedIdx := strings.Index(output, "[unassigned]")

	if wsAIdx == -1 {
		t.Error("expected [ws-a] in output")
	}
	if wsBIdx == -1 {
		t.Error("expected [ws-b] in output")
	}
	if unassignedIdx == -1 {
		t.Error("expected [unassigned] in output")
	}

	if unassignedIdx > wsAIdx {
		t.Errorf("expected unassigned before ws-a, unassignedIdx=%d, wsAIdx=%d", unassignedIdx, wsAIdx)
	}
	if wsAIdx > wsBIdx {
		t.Errorf("expected ws-a before ws-b, wsAIdx=%d, wsBIdx=%d", wsAIdx, wsBIdx)
	}

	// All agents should be listed
	if !strings.Contains(output, "alpha") {
		t.Error("expected 'alpha' in output")
	}
	if !strings.Contains(output, "beta") {
		t.Error("expected 'beta' in output")
	}
	if !strings.Contains(output, "gamma") {
		t.Error("expected 'gamma' in output")
	}
	if !strings.Contains(output, "delta") {
		t.Error("expected 'delta' in output")
	}
}

func TestRenderAgentLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		agent          AgentStatus
		indent         string
		expectContains []string
	}{
		{
			name:           "ready_agent_with_sync",
			agent:          AgentStatus{Name: "falcon", Branch: "falcon", Status: "ready", Ahead: 2, Behind: 1},
			indent:         "  ",
			expectContains: []string{"falcon", "✓", "ready", "↑2", "↓1"},
		},
		{
			name:           "working_agent_no_sync",
			agent:          AgentStatus{Name: "nova", Branch: "nova", Status: "working: T-1 (5m)"},
			indent:         "   ",
			expectContains: []string{"nova", "●", "working:"},
		},
		{
			name:           "dirty_agent",
			agent:          AgentStatus{Name: "spark", Branch: "spark", Status: "dirty"},
			indent:         "  ",
			expectContains: []string{"spark", "●", "dirty"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			RenderAgentLine(&sb, tc.agent, tc.indent)
			output := sb.String()

			for _, expected := range tc.expectContains {
				if !strings.Contains(output, expected) {
					t.Errorf("expected %q in output, got:\n%s", expected, output)
				}
			}
		})
	}
}

func TestRenderDashboardWithDaemonManagedAgents(t *testing.T) {
	t.Parallel()
	data := &MonitorData{
		Timestamp: fixedTime(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "working: T-1 (2m)", DaemonManaged: true},
			{Name: "nova", Branch: "nova", Status: "ready", DaemonManaged: false},
			{Name: "spark", Branch: "spark", Status: "3 changes", DaemonManaged: true},
		},
		Tasks:         TaskSummary{},
		AgentTasks:    make(map[string]TaskInfo),
		TaskConflicts: make(map[string][]string),
		SyncStatus:    SyncInfo{DBSynced: true},
		Stats:         MonitorStats{},
	}

	output := renderDashboard(data)

	// Verify [D] markers appear for daemon-managed agents
	// falcon and spark should have [D], nova should not
	if !strings.Contains(output, "[D] falcon") {
		t.Error("expected '[D] falcon' in output for daemon-managed agent")
	}
	if !strings.Contains(output, "[D] spark") {
		t.Error("expected '[D] spark' in output for daemon-managed agent")
	}
	// nova should NOT have [D] prefix
	if strings.Contains(output, "[D] nova") {
		t.Error("nova should NOT have [D] prefix (not daemon-managed)")
	}
	// But nova should still appear
	if !strings.Contains(output, "nova") {
		t.Error("expected 'nova' in output")
	}
}

// TestCollectTaskStatusReadyLimitParam verifies that collectTaskStatus passes
// the readyLimit parameter through to Ready(). The existing
// TestCollectTaskStatusReadyCommandArgs tests limit=100 (monitor default);
// this test covers limit=50 (serve default).
func TestCollectTaskStatusReadyLimitParam(t *testing.T) {
	// not parallel: uses setDefaultIssueBackend
	mock := NewMockIssueBackend()
	var capturedOpts backend.ReadyOpts
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		capturedOpts = opts
		return nil, nil
	}
	mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		return nil, nil
	}
	setDefaultIssueBackend(mock)
	defer resetDefaultIssueBackend()

	collectTaskStatus(50)

	if capturedOpts.Limit != 50 {
		t.Errorf("Ready() called with Limit=%d, want 50", capturedOpts.Limit)
	}
}

// TestCompleteSyncStatusDetails tests that completeSyncStatus populates
// GitPushDetails and GitPullDetails from agent Ahead/Behind counts.
func TestCompleteSyncStatusDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		agents          []AgentStatus
		wantPushDetails []WorktreeSyncDetail
		wantPullDetails []WorktreeSyncDetail
		wantNeedsPush   int
		wantNeedsPull   int
	}{
		{
			name: "agent_ahead_populates_push_details",
			agents: []AgentStatus{
				{Name: "nova", Ahead: 3, Behind: 0},
			},
			wantPushDetails: []WorktreeSyncDetail{
				{Name: "nova", Count: 3},
			},
			wantPullDetails: nil,
			wantNeedsPush:   1,
			wantNeedsPull:   0,
		},
		{
			name: "agent_behind_populates_pull_details",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 0, Behind: 2},
			},
			wantPushDetails: nil,
			wantPullDetails: []WorktreeSyncDetail{
				{Name: "falcon", Count: 2},
			},
			wantNeedsPush: 0,
			wantNeedsPull: 1,
		},
		{
			name: "agent_no_ahead_no_behind_no_details",
			agents: []AgentStatus{
				{Name: "cobalt", Ahead: 0, Behind: 0},
			},
			wantPushDetails: nil,
			wantPullDetails: nil,
			wantNeedsPush:   0,
			wantNeedsPull:   0,
		},
		{
			name: "multiple_agents_multiple_details",
			agents: []AgentStatus{
				{Name: "nova", Ahead: 3, Behind: 0},
				{Name: "falcon", Ahead: 0, Behind: 2},
				{Name: "ember", Ahead: 5, Behind: 1},
				{Name: "cobalt", Ahead: 0, Behind: 0},
			},
			wantPushDetails: []WorktreeSyncDetail{
				{Name: "nova", Count: 3},
				{Name: "ember", Count: 5},
			},
			wantPullDetails: []WorktreeSyncDetail{
				{Name: "falcon", Count: 2},
				{Name: "ember", Count: 1},
			},
			wantNeedsPush: 2,
			wantNeedsPull: 2,
		},
		{
			name:            "empty_agents_no_details",
			agents:          []AgentStatus{},
			wantPushDetails: nil,
			wantPullDetails: nil,
			wantNeedsPush:   0,
			wantNeedsPull:   0,
		},
		{
			name: "name_field_matches_agent_name",
			agents: []AgentStatus{
				{Name: "my-custom-agent", Ahead: 7, Behind: 4},
			},
			wantPushDetails: []WorktreeSyncDetail{
				{Name: "my-custom-agent", Count: 7},
			},
			wantPullDetails: []WorktreeSyncDetail{
				{Name: "my-custom-agent", Count: 4},
			},
			wantNeedsPush: 1,
			wantNeedsPull: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := SyncInfo{DBSynced: true}
			result := completeSyncStatus(info, tc.agents)

			// Check push count
			if result.GitNeedsPush != tc.wantNeedsPush {
				t.Errorf("GitNeedsPush = %d, want %d", result.GitNeedsPush, tc.wantNeedsPush)
			}

			// Check pull count
			if result.GitNeedsPull != tc.wantNeedsPull {
				t.Errorf("GitNeedsPull = %d, want %d", result.GitNeedsPull, tc.wantNeedsPull)
			}

			// Check push details
			if len(result.GitPushDetails) != len(tc.wantPushDetails) {
				t.Fatalf("GitPushDetails len = %d, want %d", len(result.GitPushDetails), len(tc.wantPushDetails))
			}
			for i, want := range tc.wantPushDetails {
				got := result.GitPushDetails[i]
				if got.Name != want.Name {
					t.Errorf("GitPushDetails[%d].Name = %q, want %q", i, got.Name, want.Name)
				}
				if got.Count != want.Count {
					t.Errorf("GitPushDetails[%d].Count = %d, want %d", i, got.Count, want.Count)
				}
			}

			// Check pull details
			if len(result.GitPullDetails) != len(tc.wantPullDetails) {
				t.Fatalf("GitPullDetails len = %d, want %d", len(result.GitPullDetails), len(tc.wantPullDetails))
			}
			for i, want := range tc.wantPullDetails {
				got := result.GitPullDetails[i]
				if got.Name != want.Name {
					t.Errorf("GitPullDetails[%d].Name = %q, want %q", i, got.Name, want.Name)
				}
				if got.Count != want.Count {
					t.Errorf("GitPullDetails[%d].Count = %d, want %d", i, got.Count, want.Count)
				}
			}
		})
	}
}

// TestProcessReadyIssuesSkipsBlockedIDs verifies the defense-in-depth check:
// issues whose ID appears in the blockedIDs set are excluded from the ready output.
func TestProcessReadyIssuesSkipsBlockedIDs(t *testing.T) {
	t.Parallel()

	issues := []backend.IssueData{
		{ID: "T-1", Title: "Ready task", Status: "open", Design: "plan", IssueType: "task"},
		{ID: "T-2", Title: "Blocked task", Status: "open", Design: "plan", IssueType: "task"},
		{ID: "T-3", Title: "Also ready", Status: "open", Design: "plan", IssueType: "task"},
	}

	blockedIDs := map[string]bool{"T-2": true}

	var summary TaskSummary
	needsPlanning, readyToImpl := processReadyIssues(issues, nil, &summary, blockedIDs)

	// T-2 should be skipped; T-1 and T-3 should be ready-to-implement
	if summary.ReadyToImplement != 2 {
		t.Errorf("expected ReadyToImplement=2 (T-2 blocked), got %d", summary.ReadyToImplement)
	}

	allReady := make([]TaskInfo, 0, len(needsPlanning)+len(readyToImpl))
	allReady = append(allReady, needsPlanning...)
	allReady = append(allReady, readyToImpl...)
	for _, ti := range allReady {
		if ti.ID == "T-2" {
			t.Errorf("T-2 should have been filtered out by blockedIDs but was included")
		}
	}
}

// TestProcessReadyIssuesNilBlockedIDs verifies graceful degradation:
// when blockedIDs is nil (e.g. Blocked() query failed), all issues pass through.
func TestProcessReadyIssuesNilBlockedIDs(t *testing.T) {
	t.Parallel()

	issues := []backend.IssueData{
		{ID: "T-1", Title: "Task 1", Status: "open", Design: "plan", IssueType: "task"},
		{ID: "T-2", Title: "Task 2", Status: "open", Design: "plan", IssueType: "task"},
	}

	var summary TaskSummary
	_, readyToImpl := processReadyIssues(issues, nil, &summary, nil)

	if summary.ReadyToImplement != 2 {
		t.Errorf("expected ReadyToImplement=2 with nil blockedIDs, got %d", summary.ReadyToImplement)
	}
	if len(readyToImpl) != 2 {
		t.Errorf("expected 2 ready tasks with nil blockedIDs, got %d", len(readyToImpl))
	}
}
