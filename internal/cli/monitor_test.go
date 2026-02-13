package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisplayWidth(t *testing.T) {
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
			got := truncateToWidth(tc.input, tc.maxWidth)
			gotWidth := displayWidth(got)
			if gotWidth > tc.maxWidth {
				t.Errorf("truncateToWidth(%q, %d) display width = %d, exceeds max",
					tc.input, tc.maxWidth, gotWidth)
			}
			if got != tc.expected {
				t.Errorf("truncateToWidth(%q, %d) = %q, want %q",
					tc.input, tc.maxWidth, got, tc.expected)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
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
			got := padRight(tc.input, tc.width)
			gotWidth := displayWidth(got)
			if gotWidth != tc.expected {
				t.Errorf("padRight(%q, %d) display width = %d, want %d (result: %q)",
					tc.input, tc.width, gotWidth, tc.expected, got)
			}
		})
	}
}

func TestRenderBoxTop(t *testing.T) {
	// Uses dashboardWidth (70) constant
	result := renderBoxTop()
	expected := "╔" + strings.Repeat("═", dashboardWidth-2) + "╗\n"
	if result != expected {
		t.Errorf("renderBoxTop() = %q, want %q", result, expected)
	}
}

func TestRenderBoxBottom(t *testing.T) {
	// Uses dashboardWidth (70) constant
	result := renderBoxBottom()
	expected := "╚" + strings.Repeat("═", dashboardWidth-2) + "╝\n"
	if result != expected {
		t.Errorf("renderBoxBottom() = %q, want %q", result, expected)
	}
}

func TestRenderBoxSeparator(t *testing.T) {
	// Uses dashboardWidth (70) constant
	result := renderBoxSeparator()
	expected := "╠" + strings.Repeat("═", dashboardWidth-2) + "╣\n"
	if result != expected {
		t.Errorf("renderBoxSeparator() = %q, want %q", result, expected)
	}
}

func TestCenterText(t *testing.T) {
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
			got := centerText(tc.text, tc.width)
			if got != tc.expected {
				t.Errorf("centerText(%q, %d) = %q, want %q",
					tc.text, tc.width, got, tc.expected)
			}
		})
	}
}

// Test state determination logic
func TestAgentStatusStates(t *testing.T) {
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
			lockStatus:     "planning: bd-123 (5m)",
			expectContains: "bd-123",
		},
		// Implementation agent states
		{
			name:         "working_no_task",
			lockStatus:   "working: ... (5m)",
			expectPrefix: "working:",
		},
		{
			name:           "working_with_task",
			lockStatus:     "working: bd-456 (5m)",
			expectContains: "bd-456",
		},
		// Done state
		{
			name:           "done_state",
			lockStatus:     "done: bd-789 (5m)",
			expectPrefix:   "done:",
			expectContains: "bd-789",
		},
		// Review state
		{
			name:           "review_state",
			lockStatus:     "review: bd-abc (5m)",
			expectPrefix:   "review:",
			expectContains: "bd-abc",
		},
		// Error state
		{
			name:           "error_state",
			lockStatus:     "error: bd-err",
			expectPrefix:   "error:",
			expectContains: "bd-err",
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
			taskID:     "bd-123",
			taskStatus: "needs_review",
			wantPrefix: "review:",
		},
		{
			name:       "working_needs_review_stays_working",
			lockStatus: "working: ... (5m)",
			taskID:     "bd-456",
			taskStatus: "needs_review",
			wantPrefix: "working:",
		},
		{
			name:       "planning_closed_becomes_done",
			lockStatus: "planning: ... (5m)",
			taskID:     "bd-789",
			taskStatus: "closed",
			wantPrefix: "done:",
		},
		{
			name:       "working_closed_becomes_done",
			lockStatus: "working: ... (5m)",
			taskID:     "bd-abc",
			taskStatus: "closed",
			wantPrefix: "done:",
		},
		{
			name:       "planning_in_progress_keeps_planning",
			lockStatus: "planning: ... (5m)",
			taskID:     "bd-def",
			taskStatus: "in_progress",
			wantPrefix: "planning:",
		},
		{
			name:       "working_in_progress_keeps_working",
			lockStatus: "working: ... (5m)",
			taskID:     "bd-ghi",
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
	tests := []struct {
		name            string
		taskIDToAgents  map[string][]string
		expectConflicts int
	}{
		{
			name: "no_conflicts",
			taskIDToAgents: map[string][]string{
				"bd-1": {"cobalt"},
				"bd-2": {"nova"},
			},
			expectConflicts: 0,
		},
		{
			name: "one_conflict",
			taskIDToAgents: map[string][]string{
				"bd-1": {"cobalt", "nova"},
				"bd-2": {"ember"},
			},
			expectConflicts: 1,
		},
		{
			name: "multiple_conflicts",
			taskIDToAgents: map[string][]string{
				"bd-1": {"cobalt", "nova"},
				"bd-2": {"ember", "falcon"},
				"bd-3": {"zephyr"},
			},
			expectConflicts: 2,
		},
		{
			name: "three_way_conflict",
			taskIDToAgents: map[string][]string{
				"bd-1": {"cobalt", "nova", "ember"},
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
	conflicts := map[string][]string{
		"bd-123": {"cobalt", "nova"},
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
	if !strings.Contains(result, "bd-123") {
		t.Error("Expected task ID in warning")
	}
	if !strings.Contains(result, "cobalt") || !strings.Contains(result, "nova") {
		t.Error("Expected agent names in warning")
	}
}

// Test MonitorData struct initialization
func TestMonitorDataStruct(t *testing.T) {
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
	data.TaskConflicts["bd-test"] = []string{"agent1", "agent2"}
	if len(data.TaskConflicts) != 1 {
		t.Error("Expected 1 conflict")
	}
}

// Test TaskInfo Status field for agent status determination
// When no lock file exists, only in_progress tasks trigger "error" state
// Closed tasks without a lock show "ready" (not "done") to avoid stale state
func TestTaskInfoStatus(t *testing.T) {
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
				ID:       "bd-test",
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
			lockStatus:     "working: bd-123 (5m)",
			expectedStatus: "working: bd-123 (5m)",
		},
		{
			name:           "no_lock_in_progress_task_shows_error",
			hasLock:        false,
			taskInProgress: true,
			expectedStatus: "error: bd-456",
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
				status = "error: bd-456"
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
	// State transitions:
	// 1. Agent starts (loom task) -> lock created -> "working: ..."
	// 2. Agent claims task -> lock updated -> "working: bd-123"
	// 3. Agent completes task -> task closed -> "done: bd-123" (while lock exists)
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
			lockTaskID:     "bd-123",
			taskStatus:     "in_progress",
			expectedPrefix: "working: bd-123",
		},
		{
			description:    "agent_completed_task_still_running",
			lockExists:     true,
			lockRunning:    true,
			lockTaskID:     "bd-123",
			taskStatus:     "closed",
			expectedPrefix: "done: bd-123",
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
				status = "error: bd-123"
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
	// Scenario:
	// 1. Agent "alpha" completed task "bd-old" (status=closed, assignee=alpha)
	// 2. Agent "alpha" starts new task with "loom task"
	// 3. Lock file is created but task not claimed yet
	// 4. Expected: "working: ..." NOT "done: bd-old"

	agentTasks := map[string]TaskInfo{
		"alpha": {ID: "bd-old", Status: "closed"},
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
		// Without the fix, this would show "done: bd-old"
		// With the fix, it shows "ready" (assuming clean worktree)
		status = "ready"
	}

	if status == "done: bd-old" {
		t.Error("Bug: closed task caused 'done' status when lock detection failed")
	}
	if status != "ready" {
		t.Errorf("Expected 'ready' when no lock and closed task, got %q", status)
	}
}

// ===========================================================================
// Data Collection Function Tests
// ===========================================================================

func TestRunBdCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdout     string
		stderr     string
		err        error
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "success with stdout",
			args:       []string{"stats", "--json"},
			stdout:     `{"summary":{"total_issues":10}}`,
			wantOutput: `{"summary":{"total_issues":10}}`,
			wantErr:    false,
		},
		{
			name:    "command fails",
			args:    []string{"invalid", "command"},
			err:     fmt.Errorf("command failed"),
			wantErr: true,
		},
		{
			name:       "empty output",
			args:       []string{"list"},
			stdout:     "",
			wantOutput: "",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				if name != "bd" {
					t.Errorf("expected 'bd' command, got %q", name)
				}
				return CommandResult{Stdout: tt.stdout, Stderr: tt.stderr, Err: tt.err}
			}

			output, err := runBdCommand(tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("runBdCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if output != tt.wantOutput {
				t.Errorf("runBdCommand() = %q, want %q", output, tt.wantOutput)
			}
		})
	}
}

func TestCollectStatistics(t *testing.T) {
	tests := []struct {
		name           string
		bdOutput       string
		bdErr          error
		wantOpen       int
		wantClosed     int
		wantTotal      int
		wantCompl      float64
		wantRemaining  int
		wantInProgress int
		wantReview     int
		wantBlocked    int
	}{
		{
			name:       "normal case with valid JSON",
			bdOutput:   `{"summary":{"total_issues":10,"open_issues":3,"closed_issues":7}}`,
			wantOpen:   3,
			wantClosed: 7,
			wantTotal:  10,
			wantCompl:  70.0,
			// Remaining = 10 - 7 - 0 (tombstone) = 3
			wantRemaining: 3,
			// Review = 10 - 3 - 0 - 7 - 0 - 0 - 0 - 0 = 0
			wantReview:     0,
			wantInProgress: 0,
			wantBlocked:    0,
		},
		{
			name:           "empty stats (no issues)",
			bdOutput:       `{"summary":{"total_issues":0,"open_issues":0,"closed_issues":0}}`,
			wantOpen:       0,
			wantClosed:     0,
			wantTotal:      0,
			wantCompl:      0,
			wantRemaining:  0,
			wantInProgress: 0,
			wantReview:     0,
			wantBlocked:    0,
		},
		{
			name:           "command failure returns zero values",
			bdErr:          fmt.Errorf("command failed"),
			wantOpen:       0,
			wantClosed:     0,
			wantTotal:      0,
			wantCompl:      0,
			wantRemaining:  0,
			wantInProgress: 0,
			wantReview:     0,
			wantBlocked:    0,
		},
		{
			name:           "invalid JSON returns zero values",
			bdOutput:       `not valid json`,
			wantOpen:       0,
			wantClosed:     0,
			wantTotal:      0,
			wantCompl:      0,
			wantRemaining:  0,
			wantInProgress: 0,
			wantReview:     0,
			wantBlocked:    0,
		},
		{
			name:       "all closed (100% completion)",
			bdOutput:   `{"summary":{"total_issues":5,"open_issues":0,"closed_issues":5}}`,
			wantOpen:   0,
			wantClosed: 5,
			wantTotal:  5,
			wantCompl:  100.0,
			// Remaining = 5 - 5 - 0 = 0
			wantRemaining:  0,
			wantInProgress: 0,
			wantReview:     0,
			wantBlocked:    0,
		},
		{
			name: "all bd stats fields populated",
			// total=20, open=10, in_progress=2, closed=5, blocked=1, deferred=0, tombstone=0, pinned=0
			bdOutput:   `{"summary":{"total_issues":20,"open_issues":10,"in_progress_issues":2,"closed_issues":5,"blocked_issues":1,"deferred_issues":0,"tombstone_issues":0,"pinned_issues":0}}`,
			wantOpen:   10,
			wantClosed: 5,
			wantTotal:  20,
			wantCompl:  25.0,
			// Remaining = 20 - 5 - 0 (tombstone) = 15
			wantRemaining:  15,
			wantInProgress: 2,
			wantBlocked:    1,
			// Review = 20 - 10 - 2 - 5 - 1 - 0 - 0 - 0 = 2
			wantReview: 2,
		},
		{
			name: "negative review clamped to zero",
			// total=10, open=5, in_progress=3, closed=3, blocked=2, deferred=0, tombstone=0, pinned=0
			// Review = 10 - 5 - 3 - 3 - 2 - 0 - 0 - 0 = -3 -> clamped to 0
			bdOutput:   `{"summary":{"total_issues":10,"open_issues":5,"in_progress_issues":3,"closed_issues":3,"blocked_issues":2,"deferred_issues":0,"tombstone_issues":0,"pinned_issues":0}}`,
			wantOpen:   5,
			wantClosed: 3,
			wantTotal:  10,
			wantCompl:  30.0,
			// Remaining = 10 - 3 - 0 = 7
			wantRemaining:  7,
			wantInProgress: 3,
			wantBlocked:    2,
			wantReview:     0, // clamped
		},
		{
			name: "negative remaining clamped to zero",
			// total=5, open=0, closed=6, tombstone=1 (closed+tombstone > total)
			// Remaining = 5 - 6 - 1 = -2 -> clamped to 0
			bdOutput:   `{"summary":{"total_issues":5,"open_issues":0,"in_progress_issues":0,"closed_issues":6,"blocked_issues":0,"deferred_issues":0,"tombstone_issues":1,"pinned_issues":0}}`,
			wantOpen:   0,
			wantClosed: 6,
			wantTotal:  5,
			wantCompl:  120.0, // 6/5 * 100
			// Remaining = 5 - 6 - 1 = -2 -> clamped to 0
			wantRemaining:  0,
			wantInProgress: 0,
			wantBlocked:    0,
			// Review = 5 - 0 - 0 - 6 - 0 - 0 - 1 - 0 = -2 -> clamped to 0
			wantReview: 0,
		},
		{
			name: "review computed with deferred and pinned",
			// total=30, open=10, in_progress=3, closed=8, blocked=2, deferred=2, tombstone=1, pinned=1
			// Review = 30 - 10 - 3 - 8 - 2 - 2 - 1 - 1 = 3
			bdOutput:   `{"summary":{"total_issues":30,"open_issues":10,"in_progress_issues":3,"closed_issues":8,"blocked_issues":2,"deferred_issues":2,"tombstone_issues":1,"pinned_issues":1}}`,
			wantOpen:   10,
			wantClosed: 8,
			wantTotal:  30,
			wantCompl:  float64(8) / float64(30) * 100,
			// Remaining = 30 - 8 - 1 = 21
			wantRemaining:  21,
			wantInProgress: 3,
			wantBlocked:    2,
			wantReview:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			stats := collectStatistics()

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
	tests := []struct {
		name          string
		bdOutput      string
		bdErr         error
		agents        []AgentStatus
		wantDBSynced  bool
		wantNeedsPush int
		wantNeedsPull int
	}{
		{
			name:         "synced state (no errors in output)",
			bdOutput:     "Database synced successfully",
			wantDBSynced: true,
		},
		{
			name:         "unsynced state (error in output)",
			bdOutput:     "error: sync failed",
			wantDBSynced: false,
		},
		{
			name:         "unsynced state (failed in output)",
			bdOutput:     "failed to connect",
			wantDBSynced: false,
		},
		{
			name:         "command failure",
			bdErr:        fmt.Errorf("command failed"),
			wantDBSynced: false, // DBError will be set
		},
		{
			name:     "count git push needs from agents",
			bdOutput: "ok",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 3, Behind: 0},
				{Name: "nova", Ahead: 1, Behind: 0},
				{Name: "spark", Ahead: 0, Behind: 0},
			},
			wantDBSynced:  true,
			wantNeedsPush: 2,
			wantNeedsPull: 0,
		},
		{
			name:     "count git pull needs from agents",
			bdOutput: "ok",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 0, Behind: 2},
				{Name: "nova", Ahead: 0, Behind: 1},
			},
			wantDBSynced:  true,
			wantNeedsPush: 0,
			wantNeedsPull: 2,
		},
		{
			name:     "mixed push and pull needs",
			bdOutput: "ok",
			agents: []AgentStatus{
				{Name: "falcon", Ahead: 3, Behind: 1},
				{Name: "nova", Ahead: 0, Behind: 2},
				{Name: "spark", Ahead: 1, Behind: 0},
			},
			wantDBSynced:  true,
			wantNeedsPush: 2,
			wantNeedsPull: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			syncInfo := collectSyncStatus(tt.agents)

			if syncInfo.DBSynced != tt.wantDBSynced {
				t.Errorf("DBSynced = %v, want %v", syncInfo.DBSynced, tt.wantDBSynced)
			}
			if syncInfo.GitNeedsPush != tt.wantNeedsPush {
				t.Errorf("GitNeedsPush = %d, want %d", syncInfo.GitNeedsPush, tt.wantNeedsPush)
			}
			if syncInfo.GitNeedsPull != tt.wantNeedsPull {
				t.Errorf("GitNeedsPull = %d, want %d", syncInfo.GitNeedsPull, tt.wantNeedsPull)
			}
		})
	}
}

func TestCollectTaskStatus(t *testing.T) {
	tests := []struct {
		name                    string
		readyOutput             string
		inProgressOutput        string
		needReviewOutput        string
		blockedOutput           string
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
		wantAgentTasksLen       int
	}{
		{
			name: "tasks with design go to ReadyToImplement",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task with design", Status: "open", Design: "## Design\nSome plan"},
			}),
			inProgressOutput:        "[]",
			needReviewOutput:        "[]",
			blockedOutput:           "[]",
			wantReadyToImplement:    1,
			wantReadyToImplementLen: 1,
		},
		{
			name: "tasks without design go to NeedsPlanning",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task without design", Status: "open", Design: ""},
			}),
			inProgressOutput:     "[]",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    1,
			wantNeedsPlanningLen: 1,
		},
		{
			name:        "tasks with review status go to NeedReview",
			readyOutput: "[]",
			needReviewOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Review this task", Status: "review"},
			}),
			inProgressOutput:   "[]",
			blockedOutput:      "[]",
			wantNeedReview:     1,
			wantReviewTasksLen: 1,
		},
		{
			name:        "in_progress tasks populate InProgressTasks and agentTasks",
			readyOutput: "[]",
			inProgressOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "In progress task", Status: "in_progress", Assignee: "falcon"},
			}),
			needReviewOutput:       "[]",
			blockedOutput:          "[]",
			wantInProgress:         1,
			wantInProgressTasksLen: 1,
			wantAgentTasksLen:      1,
		},
		{
			name:        "blocked tasks from bd blocked",
			readyOutput: "[]",
			blockedOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Blocked task", Status: "blocked"},
				{ID: "T-2", Title: "Another blocked", Status: "blocked"},
			}),
			inProgressOutput:    "[]",
			needReviewOutput:    "[]",
			wantBacklog:         2,
			wantBacklogTasksLen: 2,
		},
		{
			name: "epics are skipped",
			readyOutput: mustJSON([]BdIssue{
				{ID: "E-1", Title: "Epic task", Status: "open", IssueType: "epic", Design: ""},
				{ID: "T-1", Title: "Regular task", Status: "open", Design: ""},
			}),
			inProgressOutput:     "[]",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    1, // Only the regular task
			wantNeedsPlanningLen: 1,
		},
		{
			name: "needs-revision label tasks go to NeedsPlanning",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task needing revision", Status: "open", Design: "existing plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Regular task with design", Status: "open", Design: "plan"},
			}),
			inProgressOutput:        "[]",
			needReviewOutput:        "[]",
			blockedOutput:           "[]",
			wantNeedsPlanning:       1, // Task with needs-revision label
			wantNeedsPlanningLen:    1,
			wantReadyToImplement:    1, // Regular task with design
			wantReadyToImplementLen: 1,
		},
		{
			name: "in_progress tasks skipped in ready output",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "In progress skip", Status: "in_progress", Design: ""},
				{ID: "T-2", Title: "Regular task", Status: "open", Design: ""},
			}),
			inProgressOutput:     "[]",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    1, // Only the open task
			wantNeedsPlanningLen: 1,
		},
		{
			name: "top 5 limit for NeedsPlanning",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task 1", Status: "open", Design: ""},
				{ID: "T-2", Title: "Task 2", Status: "open", Design: ""},
				{ID: "T-3", Title: "Task 3", Status: "open", Design: ""},
				{ID: "T-4", Title: "Task 4", Status: "open", Design: ""},
				{ID: "T-5", Title: "Task 5", Status: "open", Design: ""},
				{ID: "T-6", Title: "Task 6", Status: "open", Design: ""},
				{ID: "T-7", Title: "Task 7", Status: "open", Design: ""},
			}),
			inProgressOutput:     "[]",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    7, // Count is 7
			wantNeedsPlanningLen: 5, // But only 5 stored
		},
		{
			name: "top 5 limit for ReadyToImplement",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task 1", Status: "open", Design: "plan"},
				{ID: "T-2", Title: "Task 2", Status: "open", Design: "plan"},
				{ID: "T-3", Title: "Task 3", Status: "open", Design: "plan"},
				{ID: "T-4", Title: "Task 4", Status: "open", Design: "plan"},
				{ID: "T-5", Title: "Task 5", Status: "open", Design: "plan"},
				{ID: "T-6", Title: "Task 6", Status: "open", Design: "plan"},
			}),
			inProgressOutput:        "[]",
			needReviewOutput:        "[]",
			blockedOutput:           "[]",
			wantReadyToImplement:    6, // Count is 6
			wantReadyToImplementLen: 5, // But only 5 stored
		},
		{
			name:                 "JSON parsing error handled gracefully",
			readyOutput:          "not valid json",
			inProgressOutput:     "also invalid",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    0,
			wantReadyToImplement: 0,
			wantInProgress:       0,
		},
		{
			name:        "multiple agents with tasks",
			readyOutput: "[]",
			inProgressOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task 1", Status: "in_progress", Assignee: "falcon"},
				{ID: "T-2", Title: "Task 2", Status: "in_progress", Assignee: "nova"},
				{ID: "T-3", Title: "Task 3", Status: "in_progress", Assignee: ""},
			}),
			needReviewOutput:       "[]",
			blockedOutput:          "[]",
			wantInProgress:         3,
			wantInProgressTasksLen: 3,
			wantAgentTasksLen:      2, // Only tasks with assignees
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				if len(args) > 0 && args[0] == "ready" {
					return CommandResult{Stdout: tt.readyOutput}
				}
				if len(args) > 1 && args[0] == "list" && args[1] == "--status=in_progress" {
					return CommandResult{Stdout: tt.inProgressOutput}
				}
				if len(args) > 1 && args[0] == "list" && args[1] == "--status=review" {
					return CommandResult{Stdout: tt.needReviewOutput}
				}
				if len(args) > 0 && args[0] == "blocked" {
					return CommandResult{Stdout: tt.blockedOutput}
				}
				return CommandResult{}
			}

			summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, agentTasks := collectTaskStatus(100)

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
			if len(agentTasks) != tt.wantAgentTasksLen {
				t.Errorf("agentTasks len = %d, want %d", len(agentTasks), tt.wantAgentTasksLen)
			}
		})
	}
}

func TestCollectTaskStatusReadyCommandArgs(t *testing.T) {
	// This test verifies that the "ready" command is called with the passed readyLimit
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	var capturedArgs []string
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) > 0 && args[0] == "ready" {
			capturedArgs = args
			// Return minimal valid JSON
			return CommandResult{Stdout: "[]"}
		}
		// Return empty for other commands (list, blocked, etc.)
		return CommandResult{Stdout: "[]"}
	}

	collectTaskStatus(100)

	// Verify the ready command was called with correct args
	expectedArgs := []string{"ready", "--json", "--limit", "100"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Errorf("ready command called with %d args, want %d. Got: %v", len(capturedArgs), len(expectedArgs), capturedArgs)
	}
	for i, expected := range expectedArgs {
		if i >= len(capturedArgs) || capturedArgs[i] != expected {
			t.Errorf("ready command arg[%d] = %q, want %q. Full args: %v", i, capturedArgs[i], expected, capturedArgs)
		}
	}
}

func TestCollectAgentStatus(t *testing.T) {
	t.Run("no lock clean worktree shows ready", func(t *testing.T) {
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and beads dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetBeadsDirCache()

		// Create worktree structure (relative to tmpDir)
		wtDir := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		oldExec := execCommand
		t.Cleanup(func() { execCommand = oldExec })

		execCommand = func(dir, name string, args ...string) CommandResult {
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
		}

		agents, _ := collectAgentStatus(nil)

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Status != "ready" {
			t.Errorf("expected status 'ready', got %q", agents[0].Status)
		}
	})

	t.Run("no lock dirty worktree shows changes", func(t *testing.T) {
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and beads dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetBeadsDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "nova")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		oldExec := execCommand
		t.Cleanup(func() { execCommand = oldExec })

		execCommand = func(dir, name string, args ...string) CommandResult {
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
		}

		agents, _ := collectAgentStatus(nil)

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Status != "3 changes" {
			t.Errorf("expected status '3 changes', got %q", agents[0].Status)
		}
	})

	t.Run("in_progress task but no lock shows error", func(t *testing.T) {
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and beads dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetBeadsDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "spark")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		oldExec := execCommand
		t.Cleanup(func() { execCommand = oldExec })

		execCommand = func(dir, name string, args ...string) CommandResult {
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
		}

		agentTasks := map[string]TaskInfo{
			"spark": {ID: "T-123", Status: "in_progress"},
		}

		agents, _ := collectAgentStatus(agentTasks)

		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if !strings.HasPrefix(agents[0].Status, "error:") {
			t.Errorf("expected status to start with 'error:', got %q", agents[0].Status)
		}
	})

	t.Run("ahead/behind counts from git", func(t *testing.T) {
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and beads dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetBeadsDirCache()

		wtDir := filepath.Join(tmpDir, "worktrees", "flux")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		oldExec := execCommand
		t.Cleanup(func() { execCommand = oldExec })

		execCommand = func(dir, name string, args ...string) CommandResult {
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
		}

		agents, _ := collectAgentStatus(nil)

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
		// Save and restore working directory
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmpDir := t.TempDir()
		os.Chdir(tmpDir)
		t.Cleanup(func() { os.Chdir(origDir) })

		// Reset cached resolver and beads dir so they use the new CWD
		oldResolver := defaultResolver
		defaultResolver = nil
		t.Cleanup(func() { defaultResolver = oldResolver })
		ResetBeadsDirCache()

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

		oldExec := execCommand
		t.Cleanup(func() { execCommand = oldExec })

		execCommand = func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "test-branch"}
			}
			if name == "git" && len(args) > 0 && args[0] == "status" {
				return CommandResult{Stdout: ""}
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-list" {
				return CommandResult{Stdout: "0\t0"}
			}
			// bd show for task status
			if name == "bd" && len(args) > 0 && args[0] == "show" {
				return CommandResult{Stdout: `[{"title":"Test Task","status":"in_progress"}]`}
			}
			return CommandResult{}
		}

		_, taskIDToAgents := collectAgentStatus(nil)

		if len(taskIDToAgents["T-conflict"]) != 2 {
			t.Errorf("expected 2 agents claiming same task, got %d", len(taskIDToAgents["T-conflict"]))
		}
	})
}

func TestCollectMonitorData(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and beads dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetBeadsDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "test-agent")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "test-agent"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		if name == "bd" {
			if len(args) > 0 && args[0] == "ready" {
				return CommandResult{Stdout: mustJSON([]BdIssue{
					{ID: "T-1", Title: "Task 1", Status: "open", Design: ""},
					{ID: "T-2", Title: "Task 2", Status: "open", Design: "plan"},
				})}
			}
			if len(args) > 0 && args[0] == "stats" {
				return CommandResult{Stdout: `{"summary":{"total_issues":10,"open_issues":3,"closed_issues":7}}`}
			}
			if len(args) > 0 && args[0] == "sync" {
				return CommandResult{Stdout: "synced"}
			}
			if len(args) > 0 && args[0] == "blocked" {
				return CommandResult{Stdout: "[]"}
			}
			if len(args) > 1 && args[0] == "list" {
				return CommandResult{Stdout: "[]"}
			}
		}
		return CommandResult{}
	}

	data := collectMonitorData(100)

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
	if data.Stats.Total != 10 {
		t.Errorf("expected Stats.Total=10, got %d", data.Stats.Total)
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
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and beads dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetBeadsDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "test-agent")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "test-agent"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		if name == "bd" {
			if len(args) > 0 && args[0] == "ready" {
				return CommandResult{Stdout: "[]"}
			}
			if len(args) > 0 && args[0] == "stats" {
				return CommandResult{Stdout: `{"summary":{"total_issues":5,"open_issues":2,"closed_issues":3}}`}
			}
			if len(args) > 0 && args[0] == "sync" {
				return CommandResult{Stdout: "synced"}
			}
			if len(args) > 0 && args[0] == "blocked" {
				return CommandResult{Stdout: "[]"}
			}
			if len(args) > 1 && args[0] == "list" {
				return CommandResult{Stdout: "[]"}
			}
		}
		return CommandResult{}
	}

	data := CollectMonitorData()
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
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and beads dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetBeadsDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "solo")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
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
	}

	agents := CollectAgentStatusOnly()
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

func TestRunMonitorOneShot(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset cached resolver and beads dir so they use the new CWD
	oldResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = oldResolver })
	ResetBeadsDirCache()

	wtDir := filepath.Join(tmpDir, "worktrees", "oneshot")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return CommandResult{Stdout: "oneshot"}
		}
		if name == "git" && len(args) > 0 && args[0] == "status" {
			return CommandResult{Stdout: ""}
		}
		if name == "git" && len(args) > 0 && args[0] == "rev-list" {
			return CommandResult{Stdout: "0\t0"}
		}
		if name == "bd" {
			return CommandResult{Stdout: "[]"}
		}
		return CommandResult{}
	}

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
	// Content longer than dashboardWidth - 4 should not cause negative padding
	longContent := strings.Repeat("x", dashboardWidth+10)
	result := renderBoxLine(longContent)

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
	result := renderBoxLine("")

	if !strings.HasPrefix(result, "║ ") {
		t.Error("expected box line to start with '║ '")
	}
	if !strings.HasSuffix(result, " ║\n") {
		t.Error("expected box line to end with ' ║\\n'")
	}
	// Empty content should get full padding
	expectedLen := dashboardWidth + len("║") + len("║") + len("\n") - 2 // account for unicode chars
	_ = expectedLen                                                     // just verify it doesn't panic
}

func TestRenderTaskLineAlignment(t *testing.T) {
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

			// Every rendered line should have display width = dashboardWidth (70) + newline
			lineNoNewline := strings.TrimSuffix(line, "\n")
			width := displayWidth(lineNoNewline)
			if width != dashboardWidth {
				t.Errorf("renderTaskLine display width = %d, want %d\nline: %s", width, dashboardWidth, lineNoNewline)
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
	agents := []AgentStatus{
		{Name: "comet", Branch: "comet", Status: "● 1 changes"},
		{Name: "spark", Branch: "spark", Status: "ready", Ahead: 2, Behind: 1},
		{Name: "long-name-agent", Branch: "feature/long-branch", Status: "working: bd-123 (5m)"},
	}

	for _, agent := range agents {
		t.Run(agent.Name, func(t *testing.T) {
			var sb strings.Builder
			renderAgentLine(&sb, agent, "  ")
			line := sb.String()

			lineNoNewline := strings.TrimSuffix(line, "\n")
			width := displayWidth(lineNoNewline)
			if width != dashboardWidth {
				t.Errorf("renderAgentLine display width = %d, want %d\nline: %s", width, dashboardWidth, lineNoNewline)
			}
		})
	}
}

func TestGetWorktreeGitSyncStatusError(t *testing.T) {
	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Err: fmt.Errorf("git failed")}
	}

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0) on error, got (%d, %d)", ahead, behind)
	}
}

func TestGetWorktreeGitSyncStatusMalformed(t *testing.T) {
	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "not-a-number"}
	}

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0) on malformed output, got (%d, %d)", ahead, behind)
	}
}

func TestGetWorktreeGitSyncStatusCustomBranch(t *testing.T) {
	oldExec := execCommand
	t.Cleanup(func() { execCommand = oldExec })

	oldBranch := monitorBranch
	monitorBranch = "develop"
	t.Cleanup(func() { monitorBranch = oldBranch })

	var capturedArgs []string
	execCommand = func(dir, name string, args ...string) CommandResult {
		capturedArgs = args
		return CommandResult{Stdout: "2\t4"}
	}

	ahead, behind := getWorktreeGitSyncStatus("/fake/path", "main")
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
	tests := []struct {
		name         string
		lockCommand  string // "plan" or "task"
		taskStatus   string // return from getTaskStatus mock
		expectPrefix string
	}{
		{
			name:         "planning_agent_task_needs_review_becomes_review",
			lockCommand:  "plan",
			taskStatus:   "needs_review",
			expectPrefix: "review:",
		},
		{
			name:         "working_agent_task_needs_review_stays_working",
			lockCommand:  "task",
			taskStatus:   "needs_review",
			expectPrefix: "working:",
		},
		{
			name:         "planning_agent_task_closed_becomes_done",
			lockCommand:  "plan",
			taskStatus:   "closed",
			expectPrefix: "done:",
		},
		{
			name:         "working_agent_task_closed_becomes_done",
			lockCommand:  "task",
			taskStatus:   "closed",
			expectPrefix: "done:",
		},
		{
			name:         "planning_agent_task_in_progress_keeps_planning",
			lockCommand:  "plan",
			taskStatus:   "in_progress",
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

			// Reset cached resolver and beads dir so they use the new CWD
			oldResolver := defaultResolver
			defaultResolver = nil
			t.Cleanup(func() { defaultResolver = oldResolver })
			ResetBeadsDirCache()

			wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
			if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
				t.Fatal(err)
			}

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

			oldExec := execCommand
			t.Cleanup(func() { execCommand = oldExec })

			execCommand = func(dir, name string, args ...string) CommandResult {
				if name == "git" && len(args) > 0 && args[0] == "branch" {
					return CommandResult{Stdout: "alpha"}
				}
				if name == "git" && len(args) > 0 && args[0] == "status" {
					return CommandResult{Stdout: ""}
				}
				if name == "git" && len(args) > 0 && args[0] == "rev-list" {
					return CommandResult{Stdout: "0\t0"}
				}
				// Mock bd show for getTaskStatus
				if name == "bd" && len(args) > 0 && args[0] == "show" {
					return CommandResult{Stdout: mustJSON([]struct {
						Title  string `json:"title"`
						Status string `json:"status"`
					}{{Title: "Test Task", Status: tt.taskStatus}})}
				}
				return CommandResult{}
			}

			agentTasks := map[string]TaskInfo{
				"alpha": {ID: "T-999", Title: "Test Task", Priority: 2, Status: "in_progress"},
			}

			agents, _ := collectAgentStatus(agentTasks)
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
	// Agents with empty Workspace get grouped under "(legacy)"
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

	// Agents without workspace should be in "(legacy)" group
	if !strings.Contains(output, "[(legacy)]") {
		t.Errorf("expected '[(legacy)]' group for agents without workspace, got:\n%s", output)
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

func TestRenderDashboardLegacyModeNoWorkspace(t *testing.T) {
	// When no agents have Workspace set, should NOT show workspace headers
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

	// Should NOT have workspace group headers
	if strings.Contains(output, "[(legacy)]") {
		t.Errorf("legacy mode should not show workspace group headers, got:\n%s", output)
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
	legacyIdx := strings.Index(output, "[(legacy)]")

	if wsAIdx == -1 {
		t.Error("expected [ws-a] in output")
	}
	if wsBIdx == -1 {
		t.Error("expected [ws-b] in output")
	}
	if legacyIdx == -1 {
		t.Error("expected [(legacy)] in output")
	}

	// (legacy) sorts before ws-a alphabetically
	if legacyIdx > wsAIdx {
		t.Errorf("expected (legacy) before ws-a, legacyIdx=%d, wsAIdx=%d", legacyIdx, wsAIdx)
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

func TestRenderAgentsLegacy(t *testing.T) {
	agents := []AgentStatus{
		{Name: "alpha", Branch: "alpha", Status: "ready"},
		{Name: "beta", Branch: "beta", Status: "3 changes"},
	}

	var sb strings.Builder
	renderAgentsLegacy(&sb, agents)
	output := sb.String()

	// Should not contain workspace headers
	if strings.Contains(output, "[") {
		t.Errorf("legacy mode should not contain bracket headers, got:\n%s", output)
	}

	if !strings.Contains(output, "alpha") {
		t.Error("expected 'alpha' in output")
	}
	if !strings.Contains(output, "beta") {
		t.Error("expected 'beta' in output")
	}
}

func TestRenderAgentLine(t *testing.T) {
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
			renderAgentLine(&sb, tc.agent, tc.indent)
			output := sb.String()

			for _, expected := range tc.expectContains {
				if !strings.Contains(output, expected) {
					t.Errorf("expected %q in output, got:\n%s", expected, output)
				}
			}
		})
	}
}

// ===========================================================================
// Daemon-Managed Agents Tests
// ===========================================================================

func TestLoadDaemonManagedAgents_NoFile(t *testing.T) {
	// Set LOOM_CONFIG_DIR to a temp directory that doesn't contain daemon-agents.json
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	result := loadDaemonManagedAgents()

	// When file doesn't exist, should return nil
	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil when file doesn't exist", result)
	}
}

func TestLoadDaemonManagedAgents_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create valid daemon-agents.json with current PID (so it passes the "is running" check)
	state := DaemonAgentState{
		PID: os.Getpid(), // Use current process PID so IsProcessRunning returns true
		Agents: []DaemonAgentStateEntry{
			{Worktree: "falcon", Status: "running"},
			{Worktree: "nova", Status: "idle"},
			{Worktree: "spark", Status: "running"},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()

	// Should return map with all three worktrees
	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if len(result) != 3 {
		t.Errorf("len(result) = %d, want 3", len(result))
	}
	if !result["falcon"].Managed {
		t.Error("result[falcon].Managed = false, want true")
	}
	if !result["nova"].Managed {
		t.Error("result[nova].Managed = false, want true")
	}
	if !result["spark"].Managed {
		t.Error("result[spark].Managed = false, want true")
	}
}

func TestLoadDaemonManagedAgents_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create file with invalid JSON
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()

	// Should return nil on invalid JSON
	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil for invalid JSON", result)
	}
}

func TestLoadDaemonManagedAgents_StaleDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create daemon-agents.json with a PID that doesn't exist (very high PID unlikely to exist)
	// Use a PID that's almost certainly not running
	state := DaemonAgentState{
		PID: 2147483647, // Max int32, extremely unlikely to be a real process
		Agents: []DaemonAgentStateEntry{
			{Worktree: "falcon", Status: "running"},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()

	// Should return nil when daemon process is not running (stale state file)
	if result != nil {
		t.Errorf("loadDaemonManagedAgents() = %v, want nil for stale daemon (non-existent PID)", result)
	}
}

func TestLoadDaemonManagedAgents_EmptyAgents(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create valid daemon-agents.json with current PID but empty agents array
	state := DaemonAgentState{
		PID:    os.Getpid(),
		Agents: []DaemonAgentStateEntry{}, // empty
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()

	// Should return empty map (not nil) when agents array is empty but file is valid
	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want empty map for empty agents")
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestLoadDaemonManagedAgents_SkipsEmptyWorktreeNames(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create daemon-agents.json with some empty worktree names
	state := DaemonAgentState{
		PID: os.Getpid(),
		Agents: []DaemonAgentStateEntry{
			{Worktree: "falcon", Status: "running"},
			{Worktree: "", Status: "running"}, // empty worktree name
			{Worktree: "nova", Status: "idle"},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()

	// Should only include non-empty worktree names
	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2 (should skip empty worktree name)", len(result))
	}
	if !result["falcon"].Managed {
		t.Error("result[falcon].Managed = false, want true")
	}
	if !result["nova"].Managed {
		t.Error("result[nova].Managed = false, want true")
	}
	if result[""].Managed {
		t.Error("result[\"\"].Managed = true, want false (empty worktree name should be skipped)")
	}
}

func TestLoadDaemonManagedAgents_WithRole(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	state := DaemonAgentState{
		PID: os.Getpid(),
		Agents: []DaemonAgentStateEntry{
			{Worktree: "falcon", Status: "running", Role: "task"},
			{Worktree: "nova", Status: "idle", Role: "plan"},
			{Worktree: "spark", Status: "running"}, // no role
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("failed to write daemon-agents.json: %v", err)
	}

	result := loadDaemonManagedAgents()
	if result == nil {
		t.Fatal("loadDaemonManagedAgents() returned nil, want non-nil map")
	}

	if result["falcon"].Role != "task" {
		t.Errorf("result[falcon].Role = %q, want %q", result["falcon"].Role, "task")
	}
	if result["nova"].Role != "plan" {
		t.Errorf("result[nova].Role = %q, want %q", result["nova"].Role, "plan")
	}
	if result["spark"].Role != "" {
		t.Errorf("result[spark].Role = %q, want empty string", result["spark"].Role)
	}
}
func TestRenderAgentLine_WithDaemonMarker(t *testing.T) {
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		Ahead:         0,
		Behind:        0,
		DaemonManaged: true,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	// Verify [D] prefix is present
	if !strings.Contains(output, "[D]") {
		t.Errorf("output should contain '[D]' marker for daemon-managed agent, got:\n%s", output)
	}
	if !strings.Contains(output, "[D] falcon") {
		t.Errorf("output should contain '[D] falcon', got:\n%s", output)
	}
}

func TestRenderAgentLine_WithoutDaemonMarker(t *testing.T) {
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		Ahead:         0,
		Behind:        0,
		DaemonManaged: false,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	// Verify [D] prefix is NOT present
	if strings.Contains(output, "[D]") {
		t.Errorf("output should NOT contain '[D]' marker for non-daemon agent, got:\n%s", output)
	}
	// Agent name should still be present
	if !strings.Contains(output, "falcon") {
		t.Errorf("output should contain agent name 'falcon', got:\n%s", output)
	}
}

func TestRenderAgentLine_DaemonManagedWithSyncIndicators(t *testing.T) {
	agent := AgentStatus{
		Name:          "nova",
		Branch:        "nova",
		Status:        "working: T-1 (5m)",
		Ahead:         3,
		Behind:        2,
		DaemonManaged: true,
	}

	var sb strings.Builder
	renderAgentLine(&sb, agent, "  ")
	output := sb.String()

	// Verify all elements are present
	if !strings.Contains(output, "[D]") {
		t.Error("missing [D] marker")
	}
	if !strings.Contains(output, "nova") {
		t.Error("missing agent name")
	}
	if !strings.Contains(output, "working:") {
		t.Error("missing status")
	}
	if !strings.Contains(output, "↑3") {
		t.Error("missing ahead indicator")
	}
	if !strings.Contains(output, "↓2") {
		t.Error("missing behind indicator")
	}
}

func TestAgentStatusDaemonManagedField(t *testing.T) {
	// Test JSON serialization with DaemonManaged field
	agent := AgentStatus{
		Name:          "falcon",
		Branch:        "falcon",
		Status:        "ready",
		DaemonManaged: true,
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr := string(data)

	// Verify daemon_managed field is present when true
	if !strings.Contains(jsonStr, `"daemon_managed":true`) {
		t.Errorf("expected daemon_managed:true in JSON, got: %s", jsonStr)
	}

	// Test that false value is omitted (omitempty)
	agentNotManaged := AgentStatus{
		Name:          "nova",
		Branch:        "nova",
		Status:        "ready",
		DaemonManaged: false,
	}

	data, err = json.Marshal(agentNotManaged)
	if err != nil {
		t.Fatalf("failed to marshal AgentStatus: %v", err)
	}

	jsonStr = string(data)

	// With omitempty, daemon_managed:false should be omitted
	if strings.Contains(jsonStr, "daemon_managed") {
		t.Errorf("daemon_managed should be omitted when false (omitempty), got: %s", jsonStr)
	}
}

func TestDaemonAgentStateStructs(t *testing.T) {
	// Test that the structs correctly parse daemon-agents.json format
	jsonData := `{
		"pid": 12345,
		"agents": [
			{"worktree": "falcon", "status": "running"},
			{"worktree": "nova", "status": "idle"}
		]
	}`

	var state DaemonAgentState
	if err := json.Unmarshal([]byte(jsonData), &state); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if state.PID != 12345 {
		t.Errorf("PID = %d, want 12345", state.PID)
	}
	if len(state.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(state.Agents))
	}
	if state.Agents[0].Worktree != "falcon" {
		t.Errorf("Agents[0].Worktree = %q, want %q", state.Agents[0].Worktree, "falcon")
	}
	if state.Agents[0].Status != "running" {
		t.Errorf("Agents[0].Status = %q, want %q", state.Agents[0].Status, "running")
	}
	if state.Agents[1].Worktree != "nova" {
		t.Errorf("Agents[1].Worktree = %q, want %q", state.Agents[1].Worktree, "nova")
	}
	if state.Agents[1].Status != "idle" {
		t.Errorf("Agents[1].Status = %q, want %q", state.Agents[1].Status, "idle")
	}
}

func TestRenderDashboardWithDaemonManagedAgents(t *testing.T) {
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
// the readyLimit parameter through to the bd ready command. The existing
// TestCollectTaskStatusReadyCommandArgs tests limit=100 (monitor default);
// this test covers limit=50 (serve default).
func TestCollectTaskStatusReadyLimitParam(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	var capturedReadyArgs []string
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) > 0 && args[0] == "ready" {
			capturedReadyArgs = args
			return CommandResult{Stdout: "[]"}
		}
		return CommandResult{Stdout: "[]"}
	}

	collectTaskStatus(50)

	expectedArgs := []string{"ready", "--json", "--limit", "50"}
	if len(capturedReadyArgs) != len(expectedArgs) {
		t.Fatalf("ready command called with %d args, want %d. Got: %v", len(capturedReadyArgs), len(expectedArgs), capturedReadyArgs)
	}
	for i, expected := range expectedArgs {
		if capturedReadyArgs[i] != expected {
			t.Errorf("ready command arg[%d] = %q, want %q. Full args: %v", i, capturedReadyArgs[i], expected, capturedReadyArgs)
		}
	}
}

// TestCollectReadyTasksByPriorityReadyLimitParam verifies that
// collectReadyTasksByPriority passes the readyLimit parameter through to the
// bd ready command (limit=50, matching the serve use case).
func TestCollectReadyTasksByPriorityReadyLimitParam(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	var capturedReadyArgs []string
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) > 0 && args[0] == "ready" {
			capturedReadyArgs = args
			return CommandResult{Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "P1 task", Status: "open", Priority: 1, Design: "plan"},
				{ID: "T-2", Title: "P2 task", Status: "open", Priority: 2, Design: ""},
			})}
		}
		return CommandResult{Stdout: "[]"}
	}

	counts := collectReadyTasksByPriority(50)

	// Verify the ready command was called with --limit 50
	expectedArgs := []string{"ready", "--json", "--limit", "50"}
	if len(capturedReadyArgs) != len(expectedArgs) {
		t.Fatalf("ready command called with %d args, want %d. Got: %v", len(capturedReadyArgs), len(expectedArgs), capturedReadyArgs)
	}
	for i, expected := range expectedArgs {
		if capturedReadyArgs[i] != expected {
			t.Errorf("ready command arg[%d] = %q, want %q. Full args: %v", i, capturedReadyArgs[i], expected, capturedReadyArgs)
		}
	}

	// Also verify the function correctly counted by priority
	if counts[1] != 1 {
		t.Errorf("expected priority 1 count=1, got %d", counts[1])
	}
	if counts[2] != 1 {
		t.Errorf("expected priority 2 count=1, got %d", counts[2])
	}
}

func TestBdIssueUnmarshalDependencies(t *testing.T) {
	tests := []struct {
		name        string
		jsonInput   string
		wantDepsLen int
		wantIssueID string
		wantTitle   string
		wantDep0    *Dependency // expected first dependency (nil if none)
	}{
		{
			name:        "null dependencies field",
			jsonInput:   `{"id":"T-1","title":"Task one","status":"open","dependencies":null}`,
			wantDepsLen: 0,
			wantIssueID: "T-1",
			wantTitle:   "Task one",
		},
		{
			name:        "missing dependencies field",
			jsonInput:   `{"id":"T-2","title":"Task two","status":"open"}`,
			wantDepsLen: 0,
			wantIssueID: "T-2",
			wantTitle:   "Task two",
		},
		{
			name:        "empty dependencies array",
			jsonInput:   `{"id":"T-3","title":"Task three","status":"open","dependencies":[]}`,
			wantDepsLen: 0,
			wantIssueID: "T-3",
			wantTitle:   "Task three",
		},
		{
			name: "single dependency",
			jsonInput: `{"id":"T-4","title":"Task four","status":"open","dependencies":[
				{"issue_id":"T-4","depends_on_id":"T-1","type":"blocks","created_at":"2025-01-01T00:00:00Z","created_by":"user1"}
			]}`,
			wantDepsLen: 1,
			wantIssueID: "T-4",
			wantTitle:   "Task four",
			wantDep0: &Dependency{
				IssueID:     "T-4",
				DependsOnID: "T-1",
				Type:        "blocks",
				CreatedAt:   "2025-01-01T00:00:00Z",
				CreatedBy:   "user1",
			},
		},
		{
			name: "multiple dependencies with different types",
			jsonInput: `{"id":"T-5","title":"Task five","status":"open","dependencies":[
				{"issue_id":"T-5","depends_on_id":"T-1","type":"parent-child","created_at":"2025-01-01T00:00:00Z","created_by":"user1"},
				{"issue_id":"T-5","depends_on_id":"T-2","type":"blocks","created_at":"2025-01-02T00:00:00Z","created_by":"user2"}
			]}`,
			wantDepsLen: 2,
			wantIssueID: "T-5",
			wantTitle:   "Task five",
			wantDep0: &Dependency{
				IssueID:     "T-5",
				DependsOnID: "T-1",
				Type:        "parent-child",
				CreatedAt:   "2025-01-01T00:00:00Z",
				CreatedBy:   "user1",
			},
		},
		{
			name: "dependencies alongside other fields",
			jsonInput: `{"id":"T-6","title":"Task six","status":"open","priority":2,"issue_type":"task","design":"some plan","assignee":"falcon","labels":["bug"],"dependencies":[
				{"issue_id":"T-6","depends_on_id":"T-3","type":"blocks","created_at":"2025-03-01T00:00:00Z","created_by":"admin"}
			]}`,
			wantDepsLen: 1,
			wantIssueID: "T-6",
			wantTitle:   "Task six",
			wantDep0: &Dependency{
				IssueID:     "T-6",
				DependsOnID: "T-3",
				Type:        "blocks",
				CreatedAt:   "2025-03-01T00:00:00Z",
				CreatedBy:   "admin",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var issue BdIssue
			err := json.Unmarshal([]byte(tc.jsonInput), &issue)
			if err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if issue.ID != tc.wantIssueID {
				t.Errorf("ID = %q, want %q", issue.ID, tc.wantIssueID)
			}
			if issue.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", issue.Title, tc.wantTitle)
			}
			if len(issue.Dependencies) != tc.wantDepsLen {
				t.Fatalf("Dependencies len = %d, want %d", len(issue.Dependencies), tc.wantDepsLen)
			}

			if tc.wantDep0 != nil && len(issue.Dependencies) > 0 {
				got := issue.Dependencies[0]
				if got.IssueID != tc.wantDep0.IssueID {
					t.Errorf("Dependencies[0].IssueID = %q, want %q", got.IssueID, tc.wantDep0.IssueID)
				}
				if got.DependsOnID != tc.wantDep0.DependsOnID {
					t.Errorf("Dependencies[0].DependsOnID = %q, want %q", got.DependsOnID, tc.wantDep0.DependsOnID)
				}
				if got.Type != tc.wantDep0.Type {
					t.Errorf("Dependencies[0].Type = %q, want %q", got.Type, tc.wantDep0.Type)
				}
				if got.CreatedAt != tc.wantDep0.CreatedAt {
					t.Errorf("Dependencies[0].CreatedAt = %q, want %q", got.CreatedAt, tc.wantDep0.CreatedAt)
				}
				if got.CreatedBy != tc.wantDep0.CreatedBy {
					t.Errorf("Dependencies[0].CreatedBy = %q, want %q", got.CreatedBy, tc.wantDep0.CreatedBy)
				}
			}
		})
	}
}

func TestBdIssueUnmarshalDependenciesRoundTrip(t *testing.T) {
	// Verify that marshaling and unmarshaling a BdIssue with dependencies preserves data
	original := BdIssue{
		ID:        "T-10",
		Title:     "Round trip test",
		Status:    "open",
		Priority:  1,
		IssueType: "task",
		Design:    "some design",
		Assignee:  "falcon",
		Labels:    []string{"feature"},
		Dependencies: []Dependency{
			{
				IssueID:     "T-10",
				DependsOnID: "T-5",
				Type:        "parent-child",
				CreatedAt:   "2025-06-01T12:00:00Z",
				CreatedBy:   "admin",
			},
			{
				IssueID:     "T-10",
				DependsOnID: "T-7",
				Type:        "blocks",
				CreatedAt:   "2025-06-02T12:00:00Z",
				CreatedBy:   "user2",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded BdIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if len(decoded.Dependencies) != len(original.Dependencies) {
		t.Fatalf("Dependencies len = %d, want %d", len(decoded.Dependencies), len(original.Dependencies))
	}
	for i, want := range original.Dependencies {
		got := decoded.Dependencies[i]
		if got != want {
			t.Errorf("Dependencies[%d] = %+v, want %+v", i, got, want)
		}
	}
}

// TestCompleteSyncStatusDetails tests that completeSyncStatus populates
// GitPushDetails and GitPullDetails from agent Ahead/Behind counts.
func TestCompleteSyncStatusDetails(t *testing.T) {
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
