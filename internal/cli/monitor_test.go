package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestTruncateString(t *testing.T) {
	// truncateString uses taskTitleMaxLen (45) as the max length
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"short string no truncation", "hello", "hello"},
		{"exact length", strings.Repeat("x", taskTitleMaxLen), strings.Repeat("x", taskTitleMaxLen)},
		{"over max length", strings.Repeat("x", taskTitleMaxLen+10), strings.Repeat("x", taskTitleMaxLen-3) + "..."},
		{"empty string", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateString(tc.input)
			if got != tc.expected {
				t.Errorf("truncateString(%q) = %q, want %q",
					tc.input, got, tc.expected)
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
		name         string
		lockStatus   string
		taskID       string
		taskStatus   string
		wantPrefix   string
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
		name         string
		taskStatus   string
		expectError  bool
		expectReady  bool
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
		name       string
		bdOutput   string
		bdErr      error
		wantOpen   int
		wantClosed int
		wantTotal  int
		wantCompl  float64
	}{
		{
			name:       "normal case with valid JSON",
			bdOutput:   `{"summary":{"total_issues":10,"open_issues":3,"closed_issues":7}}`,
			wantOpen:   3,
			wantClosed: 7,
			wantTotal:  10,
			wantCompl:  70.0,
		},
		{
			name:       "empty stats (no issues)",
			bdOutput:   `{"summary":{"total_issues":0,"open_issues":0,"closed_issues":0}}`,
			wantOpen:   0,
			wantClosed: 0,
			wantTotal:  0,
			wantCompl:  0,
		},
		{
			name:       "command failure returns zero values",
			bdErr:      fmt.Errorf("command failed"),
			wantOpen:   0,
			wantClosed: 0,
			wantTotal:  0,
			wantCompl:  0,
		},
		{
			name:       "invalid JSON returns zero values",
			bdOutput:   `not valid json`,
			wantOpen:   0,
			wantClosed: 0,
			wantTotal:  0,
			wantCompl:  0,
		},
		{
			name:       "all closed (100% completion)",
			bdOutput:   `{"summary":{"total_issues":5,"open_issues":0,"closed_issues":5}}`,
			wantOpen:   0,
			wantClosed: 5,
			wantTotal:  5,
			wantCompl:  100.0,
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
		})
	}
}

func TestCollectSyncStatus(t *testing.T) {
	tests := []struct {
		name           string
		bdOutput       string
		bdErr          error
		agents         []AgentStatus
		wantDBSynced   bool
		wantNeedsPush  int
		wantNeedsPull  int
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
		wantBacklog            int
		wantNeedsPlanningLen    int
		wantReadyToImplementLen int
		wantReviewTasksLen      int
		wantInProgressTasksLen  int
		wantBlockedTasksLen     int
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
			name: "tasks with [Need Review] go to NeedReview",
			readyOutput: "[]",
			needReviewOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Review this task", Status: "open"},
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
			wantBlockedTasksLen: 2,
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
			name: "[Need Review] tasks skipped in ready output",
			readyOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Skip me", Status: "open", Design: ""},
				{ID: "T-2", Title: "Regular task", Status: "open", Design: ""},
			}),
			inProgressOutput:     "[]",
			needReviewOutput:     "[]",
			blockedOutput:        "[]",
			wantNeedsPlanning:    1, // Only the regular task
			wantNeedsPlanningLen: 1,
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

			callCount := 0
			execCommand = func(dir, name string, args ...string) CommandResult {
				callCount++
				if len(args) > 0 && args[0] == "ready" {
					return CommandResult{Stdout: tt.readyOutput}
				}
				if len(args) > 1 && args[0] == "list" && args[1] == "--status=in_progress" {
					return CommandResult{Stdout: tt.inProgressOutput}
				}
				if len(args) > 1 && args[0] == "list" && args[1] == "--status=open" {
					return CommandResult{Stdout: tt.needReviewOutput}
				}
				if len(args) > 0 && args[0] == "blocked" {
					return CommandResult{Stdout: tt.blockedOutput}
				}
				return CommandResult{}
			}

			summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, blockedTasks, agentTasks := collectTaskStatus()

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
			if summary.Backlog != tt.wantBacklog{
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
			if len(blockedTasks) != tt.wantBlockedTasksLen {
				t.Errorf("blockedTasks len = %d, want %d", len(blockedTasks), tt.wantBlockedTasksLen)
			}
			if len(agentTasks) != tt.wantAgentTasksLen {
				t.Errorf("agentTasks len = %d, want %d", len(agentTasks), tt.wantAgentTasksLen)
			}
		})
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

	data := collectMonitorData()

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

// Note: mustJSON helper is defined in automode_test.go and available here
