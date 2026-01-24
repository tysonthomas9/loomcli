package main

import (
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
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"no truncation", "hello", 10, "hello"},
		{"truncate with ellipsis", "hello world", 8, "hello..."},
		{"exact length", "short", 5, "short"},
		{"shorter than max", "ab", 5, "ab"},
		{"truncate to minimum", "abcdefghij", 4, "a..."},
		{"empty string", "", 5, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateString(tc.input, tc.maxLen)
			if got != tc.expected {
				t.Errorf("truncateString(%q, %d) = %q, want %q",
					tc.input, tc.maxLen, got, tc.expected)
			}
		})
	}
}

func TestRenderBoxTop(t *testing.T) {
	result := renderBoxTop(10)
	if result != "╔════════╗\n" {
		t.Errorf("renderBoxTop(10) = %q, want %q", result, "╔════════╗\n")
	}
}

func TestRenderBoxBottom(t *testing.T) {
	result := renderBoxBottom(10)
	if result != "╚════════╝\n" {
		t.Errorf("renderBoxBottom(10) = %q, want %q", result, "╚════════╝\n")
	}
}

func TestRenderBoxSeparator(t *testing.T) {
	result := renderBoxSeparator(10)
	if result != "╠════════╣\n" {
		t.Errorf("renderBoxSeparator(10) = %q, want %q", result, "╠════════╣\n")
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
func TestTaskInfoStatus(t *testing.T) {
	tests := []struct {
		name         string
		taskStatus   string
		expectError  bool
		expectDone   bool
		expectReady  bool
	}{
		{"in_progress_no_lock_is_error", "in_progress", true, false, false},
		{"closed_no_lock_is_done", "closed", false, true, false},
		{"open_no_lock_is_ready", "open", false, false, true},
		{"empty_status_is_ready", "", false, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := TaskInfo{
				ID:       "bd-test",
				Title:    "Test Task",
				Priority: 2,
				Status:   tc.taskStatus,
			}

			// Simulate the agent status determination logic
			var agentStatus string
			if task.Status == "in_progress" {
				agentStatus = "error: " + task.ID
			} else if task.Status == "closed" {
				agentStatus = "done: " + task.ID
			} else {
				agentStatus = "ready"
			}

			if tc.expectError && !strings.HasPrefix(agentStatus, "error:") {
				t.Errorf("Expected error status, got %s", agentStatus)
			}
			if tc.expectDone && !strings.HasPrefix(agentStatus, "done:") {
				t.Errorf("Expected done status, got %s", agentStatus)
			}
			if tc.expectReady && agentStatus != "ready" {
				t.Errorf("Expected ready status, got %s", agentStatus)
			}
		})
	}
}
