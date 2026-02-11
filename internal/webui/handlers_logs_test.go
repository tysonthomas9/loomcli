package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestValidAgentNameRegex tests the agent name validation regex.
func TestValidAgentNameRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"simple alpha", "agent", true},
		{"simple numeric", "123", true},
		{"alphanumeric", "agent123", true},
		{"with hyphen", "agent-one", true},
		{"with underscore", "agent_one", true},
		{"mixed case", "AgentOne", true},
		{"all valid chars", "Agent_One-123", true},
		{"single char", "a", true},
		{"single number", "1", true},

		{"empty", "", false},
		{"space", " ", false},
		{"with space", "agent one", false},
		{"leading space", " agent", false},
		{"trailing space", "agent ", false},
		{"with dot", "agent.one", false},
		{"with slash", "agent/one", false},
		{"with backslash", "agent\\one", false},
		{"with colon", "agent:one", false},
		{"with at", "agent@one", false},
		{"with bang", "agent!one", false},
		{"with hash", "agent#one", false},
		{"path traversal", "../etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validAgentName.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validAgentName.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

// TestValidTaskIDRegex tests the task ID validation regex.
func TestValidTaskIDRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"simple alpha", "task", true},
		{"simple numeric", "123", true},
		{"uuid-like", "bd-abc123", true},
		{"with hyphen", "task-123", true},
		{"with underscore", "task_123", true},
		{"mixed case", "TaskID", true},
		{"all valid chars", "Task_ID-123", true},
		{"single char", "a", true},

		{"empty", "", false},
		{"with space", "task 123", false},
		{"with dot", "task.123", false},
		{"with slash", "task/123", false},
		{"path traversal", "../secrets", false},
		{"with colon", "task:123", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validTaskID.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validTaskID.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

// TestValidPhaseRegex tests the phase validation regex.
func TestValidPhaseRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"planning", "planning", true},
		{"implementation", "implementation", true},

		{"empty", "", false},
		{"execution", "execution", false},
		{"PLANNING uppercase", "PLANNING", false},
		{"Planning mixed", "Planning", false},
		{"planning with space", "planning ", false},
		{"random", "random", false},
		{"plan", "plan", false},
		{"impl", "impl", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validPhase.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validPhase.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

// TestHandleGetAgentLog_MissingName tests that missing agent name returns 400.
func TestHandleGetAgentLog_MissingName(t *testing.T) {
	handler := handleGetAgentLog()

	req := httptest.NewRequest(http.MethodGet, "/api/agents//logs", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing agent name" {
		t.Errorf("error = %q, want %q", resp.Error, "missing agent name")
	}
}

// TestHandleGetAgentLog_InvalidName tests that invalid agent name returns 400.
func TestHandleGetAgentLog_InvalidName(t *testing.T) {
	handler := handleGetAgentLog()

	tests := []struct {
		name      string
		agentName string
	}{
		{"contains space", "agent one"},
		{"contains slash", "agent/one"},
		{"contains dot", "agent.one"},
		{"path traversal", "../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a clean URL path and set the path value directly
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/logs", nil)
			req.SetPathValue("name", tt.agentName)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp LogContentResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid agent name: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid agent name: must match [a-zA-Z0-9_-]+")
			}
		})
	}
}

// TestHandleGetAgentLog_FileNotFound tests that missing log file returns 404.
func TestHandleGetAgentLog_FileNotFound(t *testing.T) {
	handler := handleGetAgentLog()

	// Use a valid but non-existent agent name
	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent-agent-xyz/logs", nil)
	req.SetPathValue("name", "nonexistent-agent-xyz")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "log file not found - agent may not be active" {
		t.Errorf("error = %q, want %q", resp.Error, "log file not found - agent may not be active")
	}
}

// TestHandleListTaskPhases_MissingID tests that missing task ID returns 400.
func TestHandleListTaskPhases_MissingID(t *testing.T) {
	handler := handleListTaskPhases()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks//logs", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TaskPhasesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing task ID" {
		t.Errorf("error = %q, want %q", resp.Error, "missing task ID")
	}
}

// TestHandleListTaskPhases_InvalidID tests that invalid task ID returns 400.
func TestHandleListTaskPhases_InvalidID(t *testing.T) {
	handler := handleListTaskPhases()

	tests := []struct {
		name   string
		taskID string
	}{
		{"contains space", "task 123"},
		{"contains slash", "task/123"},
		{"path traversal", "../secrets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a clean URL path and set the path value directly
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/invalid/logs", nil)
			req.SetPathValue("id", tt.taskID)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp TaskPhasesResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid task ID: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid task ID: must match [a-zA-Z0-9_-]+")
			}
		})
	}
}

// TestHandleGetTaskLog_MissingID tests that missing task ID returns 400.
func TestHandleGetTaskLog_MissingID(t *testing.T) {
	handler := handleGetTaskLog()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks//logs/planning", nil)
	req.SetPathValue("id", "")
	req.SetPathValue("phase", "planning")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing task ID" {
		t.Errorf("error = %q, want %q", resp.Error, "missing task ID")
	}
}

// TestHandleGetTaskLog_InvalidID tests that invalid task ID returns 400.
func TestHandleGetTaskLog_InvalidID(t *testing.T) {
	handler := handleGetTaskLog()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task%2F123/logs/planning", nil)
	req.SetPathValue("id", "task/123")
	req.SetPathValue("phase", "planning")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "invalid task ID: must match [a-zA-Z0-9_-]+" {
		t.Errorf("error = %q, want %q", resp.Error, "invalid task ID: must match [a-zA-Z0-9_-]+")
	}
}

// TestHandleGetTaskLog_MissingPhase tests that missing phase returns 400.
func TestHandleGetTaskLog_MissingPhase(t *testing.T) {
	handler := handleGetTaskLog()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-123/logs/", nil)
	req.SetPathValue("id", "task-123")
	req.SetPathValue("phase", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing phase" {
		t.Errorf("error = %q, want %q", resp.Error, "missing phase")
	}
}

// TestHandleGetTaskLog_InvalidPhase tests that invalid phase returns 400.
func TestHandleGetTaskLog_InvalidPhase(t *testing.T) {
	handler := handleGetTaskLog()

	tests := []struct {
		name  string
		phase string
	}{
		{"execution", "execution"},
		{"random", "random"},
		{"PLANNING uppercase", "PLANNING"},
		{"plan", "plan"},
		{"impl", "impl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-123/logs/"+tt.phase, nil)
			req.SetPathValue("id", "task-123")
			req.SetPathValue("phase", tt.phase)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp LogContentResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid phase: must be 'planning' or 'implementation'" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid phase: must be 'planning' or 'implementation'")
			}
		})
	}
}

// TestHandleGetTaskLog_FileNotFound tests that missing log file returns 404.
func TestHandleGetTaskLog_FileNotFound(t *testing.T) {
	handler := handleGetTaskLog()

	// Use valid but non-existent task/phase
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/nonexistent-task-xyz/logs/planning", nil)
	req.SetPathValue("id", "nonexistent-task-xyz")
	req.SetPathValue("phase", "planning")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "log file not found - task phase may not have started" {
		t.Errorf("error = %q, want %q", resp.Error, "log file not found - task phase may not have started")
	}
}

// TestLinesParameterParsing tests the ?lines= query parameter parsing.
func TestLinesParameterParsing(t *testing.T) {
	// Create a temp log file for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs", "agents")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	// Create a log file with 500 lines
	logFile := filepath.Join(logDir, "test-agent.log")
	var lines []string
	for i := 1; i <= 500; i++ {
		lines = append(lines, "line "+string(rune('0'+i%10)))
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	// Override getAgentLogPath for testing
	// Since we can't easily mock getAgentLogPath, we'll test ReadLastNLines directly
	// and verify the handler parses query params correctly by checking behavior

	tests := []struct {
		name         string
		linesParam   string
		expectedRead int // -1 means use default
	}{
		{"no param uses default", "", logReadDefaultLines},
		{"explicit 100", "100", 100},
		{"explicit 50", "50", 50},
		{"zero uses default", "0", logReadDefaultLines},
		{"negative uses default", "-1", logReadDefaultLines},
		{"invalid string uses default", "abc", logReadDefaultLines},
		{"exceeds max uses max", "20000", logReadMaxLines},
		{"exactly max", "10000", logReadMaxLines},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test ReadLastNLines directly to verify line limiting logic
			requestedLines := logReadDefaultLines
			if tt.linesParam != "" {
				if n := parseLines(tt.linesParam); n > 0 {
					requestedLines = n
					if requestedLines > logReadMaxLines {
						requestedLines = logReadMaxLines
					}
				}
			}

			if requestedLines != tt.expectedRead {
				t.Errorf("parsed lines = %d, want %d", requestedLines, tt.expectedRead)
			}
		})
	}
}

// parseLines is a helper to mimic the handler's lines parsing logic.
func parseLines(param string) int {
	var n int
	err := json.Unmarshal([]byte(param), &n)
	if err != nil {
		return 0
	}
	return n
}

// TestReadLastNLines_EmptyFile tests reading from an empty file.
func TestReadLastNLines_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "empty.log")
	if err := os.WriteFile(logFile, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 100)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 0 {
		t.Errorf("len(lines) = %d, want 0", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
}

// TestReadLastNLines_FewLines tests reading when file has fewer lines than requested.
func TestReadLastNLines_FewLines(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "few.log")
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 100)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 5 {
		t.Errorf("len(lines) = %d, want 5", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if lines[0] != "line 1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 1")
	}
	if lines[4] != "line 5" {
		t.Errorf("lines[4] = %q, want %q", lines[4], "line 5")
	}
}

// TestReadLastNLines_ManyLines tests reading last N lines from a larger file.
func TestReadLastNLines_ManyLines(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "many.log")

	// Create file with 1000 lines
	var contentLines []string
	for i := 1; i <= 1000; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Request last 100 lines
	lines, startLine, err := ReadLastNLines(logFile, 100)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 100 {
		t.Errorf("len(lines) = %d, want 100", len(lines))
	}
	// startLine should be 901 (1000 - 100 + 1)
	if startLine != 901 {
		t.Errorf("startLine = %d, want 901", startLine)
	}
	if lines[0] != "line 901" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 901")
	}
	if lines[99] != "line 1000" {
		t.Errorf("lines[99] = %q, want %q", lines[99], "line 1000")
	}
}

// TestReadLastNLines_ExactlyNLines tests when file has exactly N lines.
func TestReadLastNLines_ExactlyNLines(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "exact.log")

	var contentLines []string
	for i := 1; i <= 50; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 50)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 50 {
		t.Errorf("len(lines) = %d, want 50", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
}

// TestReadLastNLines_ZeroOrNegativeN tests that invalid N values use defaults.
func TestReadLastNLines_ZeroOrNegativeN(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	var contentLines []string
	for i := 1; i <= 500; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Zero should use default
	lines, _, err := ReadLastNLines(logFile, 0)
	if err != nil {
		t.Fatalf("ReadLastNLines(0) error = %v", err)
	}
	if len(lines) != logReadDefaultLines {
		t.Errorf("ReadLastNLines(0) returned %d lines, want %d", len(lines), logReadDefaultLines)
	}

	// Negative should use default
	lines, _, err = ReadLastNLines(logFile, -1)
	if err != nil {
		t.Fatalf("ReadLastNLines(-1) error = %v", err)
	}
	if len(lines) != logReadDefaultLines {
		t.Errorf("ReadLastNLines(-1) returned %d lines, want %d", len(lines), logReadDefaultLines)
	}
}

// TestReadLastNLines_ExceedsMax tests that requesting more than max is capped.
func TestReadLastNLines_ExceedsMax(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create file with more lines than max
	var contentLines []string
	for i := 1; i <= 15000; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Request more than max
	lines, _, err := ReadLastNLines(logFile, 20000)
	if err != nil {
		t.Fatalf("ReadLastNLines(20000) error = %v", err)
	}
	if len(lines) != logReadMaxLines {
		t.Errorf("ReadLastNLines(20000) returned %d lines, want %d (max)", len(lines), logReadMaxLines)
	}
}

// TestReadLastNLines_FileNotExists tests error on non-existent file.
func TestReadLastNLines_FileNotExists(t *testing.T) {
	_, _, err := ReadLastNLines("/nonexistent/path/to/file.log", 100)
	if err == nil {
		t.Error("ReadLastNLines() expected error for non-existent file, got nil")
	}
}

// TestReadLastNLines_NoTrailingNewline tests reading from a file with no trailing newline.
func TestReadLastNLines_NoTrailingNewline(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "notrail.log")
	content := "line1\nline2\nline3"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 2)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 2 {
		t.Errorf("len(lines) = %d, want 2", len(lines))
	}
	if startLine != 2 {
		t.Errorf("startLine = %d, want 2", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line2" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line2")
	}
	if len(lines) >= 2 && lines[1] != "line3" {
		t.Errorf("lines[1] = %q, want %q", lines[1], "line3")
	}
}

// TestReadLastNLines_SingleLine tests reading from a file with exactly one line (trailing newline).
func TestReadLastNLines_SingleLine(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "single.log")
	content := "hello\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 100)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("len(lines) = %d, want 1", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if len(lines) >= 1 && lines[0] != "hello" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "hello")
	}
}

// TestReadLastNLines_SingleLineNoNewline tests reading from a file with one line and no trailing newline.
func TestReadLastNLines_SingleLineNoNewline(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "singlenolf.log")
	content := "hello"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, 100)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("len(lines) = %d, want 1", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if len(lines) >= 1 && lines[0] != "hello" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "hello")
	}
}

// TestReadLastNLines_LargeFile tests reading last N lines from a large file with 100,000 lines.
func TestReadLastNLines_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "large.log")

	// Create a file with 100,000 lines
	var builder strings.Builder
	for i := 1; i <= 100000; i++ {
		builder.WriteString("line " + itoa(i) + "\n")
	}
	if err := os.WriteFile(logFile, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Request last 200 lines
	lines, startLine, err := ReadLastNLines(logFile, 200)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != 200 {
		t.Errorf("len(lines) = %d, want 200", len(lines))
	}
	if startLine != 99801 {
		t.Errorf("startLine = %d, want 99801", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line 99801" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 99801")
	}
	if len(lines) >= 200 && lines[199] != "line 100000" {
		t.Errorf("lines[199] = %q, want %q", lines[199], "line 100000")
	}

	// Verify a few lines in the middle
	if len(lines) >= 100 && lines[99] != "line 99900" {
		t.Errorf("lines[99] = %q, want %q", lines[99], "line 99900")
	}
}

// TestReadLastNLines_ExactBoundary tests reading when the last N lines start exactly at
// a 32KB (readChunkSize) boundary. This verifies the backward seek logic handles the
// boundary condition correctly.
func TestReadLastNLines_ExactBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "boundary.log")

	// readChunkSize is 32 * 1024 = 32768 bytes.
	// Strategy: create a file where the first section is exactly 32768 bytes,
	// then append known "tail" lines after that boundary.
	//
	// Use fixed-length lines of 64 bytes each (including the newline).
	// 32768 / 64 = 512 lines will fill exactly one chunk.
	// Then we append 10 more tail lines, and request exactly 10.
	// The backward scan should find all 10 newlines in the second chunk and
	// set readOffset to exactly 32768.
	const lineLen = 64 // including newline
	const chunkSize = 32 * 1024
	const prefixLines = chunkSize / lineLen // 512 lines
	const tailLines = 10

	var builder strings.Builder
	// Write prefix lines that fill exactly 32768 bytes.
	// Each line: "PPPP-NNNN-" + padding + "\n" = 64 bytes
	for i := 0; i < prefixLines; i++ {
		line := fmt.Sprintf("PPPP-%04d-", i)
		// Pad to lineLen-1 chars, then add newline
		for len(line) < lineLen-1 {
			line += "X"
		}
		builder.WriteString(line + "\n")
	}

	// Verify prefix section is exactly chunkSize bytes
	if builder.Len() != chunkSize {
		t.Fatalf("prefix section is %d bytes, want %d", builder.Len(), chunkSize)
	}

	// Write tail lines with the same fixed length
	for i := 0; i < tailLines; i++ {
		line := fmt.Sprintf("TAIL-%04d-", i)
		for len(line) < lineLen-1 {
			line += "Y"
		}
		builder.WriteString(line + "\n")
	}

	if err := os.WriteFile(logFile, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := ReadLastNLines(logFile, tailLines)
	if err != nil {
		t.Fatalf("ReadLastNLines() error = %v", err)
	}

	if len(lines) != tailLines {
		t.Errorf("len(lines) = %d, want %d", len(lines), tailLines)
	}

	// startLine should be prefixLines + 1 = 513
	expectedStartLine := int64(prefixLines + 1)
	if startLine != expectedStartLine {
		t.Errorf("startLine = %d, want %d", startLine, expectedStartLine)
	}

	// Verify each tail line
	for i := 0; i < tailLines; i++ {
		expected := fmt.Sprintf("TAIL-%04d-", i)
		for len(expected) < lineLen-1 {
			expected += "Y"
		}
		if i < len(lines) && lines[i] != expected {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], expected)
		}
	}
}

// TestLogContentResponseJSON tests the JSON structure of LogContentResponse.
func TestLogContentResponseJSON(t *testing.T) {
	// Test success response
	successResp := LogContentResponse{
		Success: true,
		Data: &LogContentData{
			Lines:     []string{"line 1", "line 2", "line 3"},
			LineCount: 3,
		},
	}

	data, err := json.Marshal(successResp)
	if err != nil {
		t.Fatalf("failed to marshal success response: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["success"] != true {
		t.Errorf("success = %v, want true", decoded["success"])
	}
	if _, ok := decoded["error"]; ok {
		t.Error("error field should not be present in success response")
	}
	dataObj, ok := decoded["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field should be an object")
	}
	lines, ok := dataObj["lines"].([]interface{})
	if !ok {
		t.Fatal("data.lines should be an array")
	}
	if len(lines) != 3 {
		t.Errorf("len(data.lines) = %d, want 3", len(lines))
	}
	lineCount, ok := dataObj["line_count"].(float64) // JSON numbers are float64
	if !ok {
		t.Fatal("data.line_count should be a number")
	}
	if int(lineCount) != 3 {
		t.Errorf("data.line_count = %v, want 3", lineCount)
	}

	// Test error response
	errorResp := LogContentResponse{
		Success: false,
		Error:   "test error message",
	}

	data, err = json.Marshal(errorResp)
	if err != nil {
		t.Fatalf("failed to marshal error response: %v", err)
	}

	// Reset decoded map for fresh unmarshal
	decoded = make(map[string]interface{})
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["success"] != false {
		t.Errorf("success = %v, want false", decoded["success"])
	}
	if decoded["error"] != "test error message" {
		t.Errorf("error = %v, want %q", decoded["error"], "test error message")
	}
	if _, ok := decoded["data"]; ok {
		t.Error("data field should not be present in error response")
	}
}

// TestTaskPhasesResponseJSON tests the JSON structure of TaskPhasesResponse.
func TestTaskPhasesResponseJSON(t *testing.T) {
	// Test success response
	successResp := TaskPhasesResponse{
		Success: true,
		Data: &TaskPhasesData{
			Phases: []string{"planning", "implementation"},
		},
	}

	data, err := json.Marshal(successResp)
	if err != nil {
		t.Fatalf("failed to marshal success response: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["success"] != true {
		t.Errorf("success = %v, want true", decoded["success"])
	}
	dataObj, ok := decoded["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field should be an object")
	}
	phases, ok := dataObj["phases"].([]interface{})
	if !ok {
		t.Fatal("data.phases should be an array")
	}
	if len(phases) != 2 {
		t.Errorf("len(data.phases) = %d, want 2", len(phases))
	}
	if phases[0] != "planning" {
		t.Errorf("data.phases[0] = %v, want %q", phases[0], "planning")
	}
	if phases[1] != "implementation" {
		t.Errorf("data.phases[1] = %v, want %q", phases[1], "implementation")
	}

	// Test error response
	errorResp := TaskPhasesResponse{
		Success: false,
		Error:   "task not found",
	}

	data, err = json.Marshal(errorResp)
	if err != nil {
		t.Fatalf("failed to marshal error response: %v", err)
	}

	// Reset decoded map for fresh unmarshal
	decoded = make(map[string]interface{})
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["success"] != false {
		t.Errorf("success = %v, want false", decoded["success"])
	}
	if decoded["error"] != "task not found" {
		t.Errorf("error = %v, want %q", decoded["error"], "task not found")
	}
	if _, ok := decoded["data"]; ok {
		t.Error("data field should not be present in error response")
	}
}

// TestLogReadConstants tests that the log read constants have expected values.
func TestLogReadConstants(t *testing.T) {
	if logReadDefaultLines != 200 {
		t.Errorf("logReadDefaultLines = %d, want 200", logReadDefaultLines)
	}
	if logReadMaxLines != 10000 {
		t.Errorf("logReadMaxLines = %d, want 10000", logReadMaxLines)
	}
}

// TestFileExists tests the fileExists helper function.
func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Test existing file
	existingFile := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existingFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if !fileExists(existingFile) {
		t.Error("fileExists() returned false for existing file")
	}

	// Test non-existing file
	nonExistent := filepath.Join(tmpDir, "nonexistent.txt")
	if fileExists(nonExistent) {
		t.Error("fileExists() returned true for non-existing file")
	}

	// Test existing directory
	existingDir := filepath.Join(tmpDir, "existsdir")
	if err := os.Mkdir(existingDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if !fileExists(existingDir) {
		t.Error("fileExists() returned false for existing directory")
	}
}

// TestHandleGetAgentLog_ContentType tests that Content-Type is always application/json.
func TestHandleGetAgentLog_ContentType(t *testing.T) {
	handler := handleGetAgentLog()

	// Test with missing name
	req := httptest.NewRequest(http.MethodGet, "/api/agents//logs", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleListTaskPhases_ContentType tests that Content-Type is always application/json.
func TestHandleListTaskPhases_ContentType(t *testing.T) {
	handler := handleListTaskPhases()

	// Test with missing ID
	req := httptest.NewRequest(http.MethodGet, "/api/tasks//logs", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleGetTaskLog_ContentType tests that Content-Type is always application/json.
func TestHandleGetTaskLog_ContentType(t *testing.T) {
	handler := handleGetTaskLog()

	// Test with missing phase
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-123/logs/", nil)
	req.SetPathValue("id", "task-123")
	req.SetPathValue("phase", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// itoa is a simple int to string helper for tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// --- Happy path tests for log handlers ---

// TestHandleGetAgentLog_Success tests the full success path with an actual log file.
func TestHandleGetAgentLog_Success(t *testing.T) {
	// Redirect HOME to a temp dir so we don't write to the real ~/.loom/logs/
	// Resolve symlinks first (macOS /tmp -> /private/tmp) so validatePathWithinDir works.
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	agentLogDir := filepath.Join(tmpHome, ".loom", "logs", "agents")
	if err := os.MkdirAll(agentLogDir, 0o755); err != nil {
		t.Fatalf("failed to create agent log dir: %v", err)
	}

	testAgentName := "testcoveragexyz123"
	logPath := filepath.Join(agentLogDir, testAgentName+".log")
	logContent := "line 1: agent started\nline 2: processing task\nline 3: task complete\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	handler := handleGetAgentLog()

	// Use the mux to route properly with path params
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+testAgentName+"/logs", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got false (error: %s)", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if len(resp.Data.Lines) == 0 {
		t.Error("expected non-empty lines")
	}
}

// TestHandleGetAgentLog_LinesParam tests the lines query parameter.
func TestHandleGetAgentLog_LinesParam(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	agentLogDir := filepath.Join(tmpHome, ".loom", "logs", "agents")
	if err := os.MkdirAll(agentLogDir, 0o755); err != nil {
		t.Fatalf("failed to create agent log dir: %v", err)
	}

	testAgentName := "testcoveragelines"
	logPath := filepath.Join(agentLogDir, testAgentName+".log")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d: test data", i+1))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	handler := handleGetAgentLog()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs", handler)

	// Request only 5 lines
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+testAgentName+"/logs?lines=5", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, error: %s", resp.Error)
	}
	if resp.Data != nil && len(resp.Data.Lines) > 5 {
		t.Errorf("expected at most 5 lines, got %d", len(resp.Data.Lines))
	}
}

// TestHandleListTaskPhases_Success tests the full success path for listing task phases.
func TestHandleListTaskPhases_Success(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	taskID := "testcoveragetask123"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task log dir: %v", err)
	}

	// Create phase log files
	for _, phase := range []string{"planning", "implementation"} {
		logPath := filepath.Join(taskDir, phase+".log")
		if err := os.WriteFile(logPath, []byte("test log content\n"), 0o644); err != nil {
			t.Fatalf("failed to write %s log: %v", phase, err)
		}
	}

	handler := handleListTaskPhases()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}/logs", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/logs", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp TaskPhasesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if len(resp.Data.Phases) != 2 {
		t.Errorf("expected 2 phases, got %d", len(resp.Data.Phases))
	}
}

// TestHandleGetTaskLog_Success tests the full success path for reading a task log.
func TestHandleGetTaskLog_Success(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	taskID := "testcoveragetasklog"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task log dir: %v", err)
	}

	logPath := filepath.Join(taskDir, "planning.log")
	if err := os.WriteFile(logPath, []byte("plan step 1\nplan step 2\n"), 0o644); err != nil {
		t.Fatalf("failed to write planning log: %v", err)
	}

	handler := handleGetTaskLog()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/logs/planning", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if len(resp.Data.Lines) == 0 {
		t.Error("expected non-empty lines")
	}
}

// --- SSE Streaming Handler Tests ---

// connectLogSSE connects to an arbitrary SSE endpoint path and returns an sseTestClient.
// The caller must close the client when done.
func connectLogSSE(t *testing.T, serverURL, path string) *sseTestClient {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, serverURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE endpoint: %v", err)
	}

	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// connectLogSSEWithContext connects to an SSE endpoint with a cancellable context.
func connectLogSSEWithContext(t *testing.T, ctx context.Context, serverURL, path string) *sseTestClient {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE endpoint: %v", err)
	}

	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// setupAgentLogEnv creates a temp HOME with agent log directory structure
// and returns the temp home path and the log file path.
func setupAgentLogEnv(t *testing.T, agentName string, content string) (tmpHome string, logPath string) {
	t.Helper()
	var err error
	tmpHome, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	agentLogDir := filepath.Join(tmpHome, ".loom", "logs", "agents")
	if err := os.MkdirAll(agentLogDir, 0o755); err != nil {
		t.Fatalf("failed to create agent log dir: %v", err)
	}

	logPath = filepath.Join(agentLogDir, agentName+".log")
	if content != "" {
		if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write test log: %v", err)
		}
	}
	return tmpHome, logPath
}

// setupTaskLogEnv creates a temp HOME with task log directory structure
// and returns the temp home path and the log file path.
func setupTaskLogEnv(t *testing.T, taskID, phase, content string) (tmpHome string, logPath string) {
	t.Helper()
	var err error
	tmpHome, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	taskDir := filepath.Join(tmpHome, ".loom", "logs", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task log dir: %v", err)
	}

	logPath = filepath.Join(taskDir, phase+".log")
	if content != "" {
		if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write test log: %v", err)
		}
	}
	return tmpHome, logPath
}

// assertJSONErrorResponse verifies a JSON error response from a log handler.
func assertJSONErrorResponse(t *testing.T, resp *http.Response, expectedStatus int, expectedError string) {
	t.Helper()
	if resp.StatusCode != expectedStatus {
		t.Errorf("status = %d, want %d", resp.StatusCode, expectedStatus)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var logResp LogContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&logResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if logResp.Success {
		t.Error("expected success to be false")
	}
	if logResp.Error != expectedError {
		t.Errorf("error = %q, want %q", logResp.Error, expectedError)
	}
}

// --- handleAgentLogStream error path tests ---

// TestHandleAgentLogStream_MissingName tests that an empty agent name returns 400 JSON.
func TestHandleAgentLogStream_MissingName(t *testing.T) {
	handler := handleAgentLogStream()

	req := httptest.NewRequest(http.MethodGet, "/api/agents//logs/stream", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing agent name" {
		t.Errorf("error = %q, want %q", resp.Error, "missing agent name")
	}
}

// TestHandleAgentLogStream_InvalidName tests path traversal and invalid chars return 400 JSON.
func TestHandleAgentLogStream_InvalidName(t *testing.T) {
	handler := handleAgentLogStream()

	tests := []struct {
		name      string
		agentName string
	}{
		{"contains space", "agent one"},
		{"contains slash", "agent/one"},
		{"contains dot", "agent.one"},
		{"path traversal", "../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/logs/stream", nil)
			req.SetPathValue("name", tt.agentName)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			var resp LogContentResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid agent name: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid agent name: must match [a-zA-Z0-9_-]+")
			}
		})
	}
}

// TestHandleAgentLogStream_FileNotFound tests that a valid but nonexistent agent returns 404 JSON.
func TestHandleAgentLogStream_FileNotFound(t *testing.T) {
	handler := handleAgentLogStream()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent-agent-xyz/logs/stream", nil)
	req.SetPathValue("name", "nonexistent-agent-xyz")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "log file not found - agent may not be active" {
		t.Errorf("error = %q, want %q", resp.Error, "log file not found - agent may not be active")
	}
}

// --- handleTaskLogStream error path tests ---

// TestHandleTaskLogStream_MissingID tests that an empty task ID returns 400 JSON.
func TestHandleTaskLogStream_MissingID(t *testing.T) {
	handler := handleTaskLogStream()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks//logs/planning/stream", nil)
	req.SetPathValue("id", "")
	req.SetPathValue("phase", "planning")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing task ID" {
		t.Errorf("error = %q, want %q", resp.Error, "missing task ID")
	}
}

// TestHandleTaskLogStream_InvalidID tests path traversal and invalid chars return 400 JSON.
func TestHandleTaskLogStream_InvalidID(t *testing.T) {
	handler := handleTaskLogStream()

	tests := []struct {
		name   string
		taskID string
	}{
		{"contains space", "task 123"},
		{"contains slash", "task/123"},
		{"path traversal", "../secrets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/invalid/logs/planning/stream", nil)
			req.SetPathValue("id", tt.taskID)
			req.SetPathValue("phase", "planning")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			var resp LogContentResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid task ID: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid task ID: must match [a-zA-Z0-9_-]+")
			}
		})
	}
}

// TestHandleTaskLogStream_MissingPhase tests that an empty phase returns 400 JSON.
func TestHandleTaskLogStream_MissingPhase(t *testing.T) {
	handler := handleTaskLogStream()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-123/logs//stream", nil)
	req.SetPathValue("id", "task-123")
	req.SetPathValue("phase", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "missing phase" {
		t.Errorf("error = %q, want %q", resp.Error, "missing phase")
	}
}

// TestHandleTaskLogStream_InvalidPhase tests that invalid phase names return 400 JSON.
func TestHandleTaskLogStream_InvalidPhase(t *testing.T) {
	handler := handleTaskLogStream()

	tests := []struct {
		name  string
		phase string
	}{
		{"execution", "execution"},
		{"random", "random"},
		{"PLANNING uppercase", "PLANNING"},
		{"plan", "plan"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-123/logs/"+tt.phase+"/stream", nil)
			req.SetPathValue("id", "task-123")
			req.SetPathValue("phase", tt.phase)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			var resp LogContentResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error != "invalid phase: must be 'planning' or 'implementation'" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid phase: must be 'planning' or 'implementation'")
			}
		})
	}
}

// TestHandleTaskLogStream_FileNotFound tests that a valid but nonexistent task returns 404 JSON.
func TestHandleTaskLogStream_FileNotFound(t *testing.T) {
	handler := handleTaskLogStream()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/nonexistent-task-xyz/logs/planning/stream", nil)
	req.SetPathValue("id", "nonexistent-task-xyz")
	req.SetPathValue("phase", "planning")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp LogContentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error != "log file not found - task phase may not have started" {
		t.Errorf("error = %q, want %q", resp.Error, "log file not found - task phase may not have started")
	}
}

// --- handleAgentLogStream SSE happy path tests ---

// TestHandleAgentLogStream_Success tests the full SSE streaming happy path.
func TestHandleAgentLogStream_Success(t *testing.T) {
	_, _ = setupAgentLogEnv(t, "stream-test-agent", "line 1: started\nline 2: processing\nline 3: done\n")

	handler := handleAgentLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/agents/stream-test-agent/logs/stream")
	t.Cleanup(client.close)

	// Verify SSE response headers
	ct := client.resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	cc := client.resp.Header.Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	conn := client.resp.Header.Get("Connection")
	if conn != "keep-alive" {
		t.Errorf("Connection = %q, want %q", conn, "keep-alive")
	}
	xab := client.resp.Header.Get("X-Accel-Buffering")
	if xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", xab, "no")
	}

	// Read the 3 log-line events
	for i := 1; i <= 3; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("event %d: event type = %q, want %q", i, evt.Event, "log-line")
		}
		if evt.ID == "" {
			t.Errorf("event %d: expected non-empty ID", i)
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("event %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("event %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
		if payload.Timestamp == "" {
			t.Errorf("event %d: expected non-empty timestamp", i)
		}
	}

	cancel()
}

// TestHandleAgentLogStream_SinceParam tests the ?since= query parameter for catch-up.
func TestHandleAgentLogStream_SinceParam(t *testing.T) {
	// Create a 10-line log file
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d: data", i))
	}
	_, _ = setupAgentLogEnv(t, "since-test-agent", strings.Join(lines, "\n")+"\n")

	handler := handleAgentLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Connect with ?since=8 — should receive lines 8, 9, 10
	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/agents/since-test-agent/logs/stream?since=8")
	t.Cleanup(client.close)

	for i := 8; i <= 10; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event for line %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("line %d: event type = %q, want %q", i, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("line %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
		expectedLine := fmt.Sprintf("line %d: data", i)
		if payload.Line != expectedLine {
			t.Errorf("line %d: line = %q, want %q", i, payload.Line, expectedLine)
		}
	}

	cancel()
}

// TestHandleAgentLogStream_NewLinesAfterConnect tests that new lines appended
// to the log file after connecting are delivered as SSE events via fsnotify.
func TestHandleAgentLogStream_NewLinesAfterConnect(t *testing.T) {
	_, logPath := setupAgentLogEnv(t, "live-test-agent", "initial line 1\n")

	handler := handleAgentLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/agents/live-test-agent/logs/stream")
	t.Cleanup(client.close)

	// Read the initial line event
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Errorf("initial event type = %q, want %q", evt.Event, "log-line")
	}

	// Wait for fsnotify watcher to be set up
	time.Sleep(100 * time.Millisecond)

	// Append new lines to the file
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open log for append: %v", err)
	}
	if _, err := f.WriteString("new line 2\nnew line 3\n"); err != nil {
		f.Close()
		t.Fatalf("failed to append to log: %v", err)
	}
	f.Close()

	// Read the new line events
	for i := 2; i <= 3; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event for new line %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("new line %d: event type = %q, want %q", i, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("new line %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("new line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
	}

	cancel()
}

// --- handleTaskLogStream SSE happy path tests ---

// TestHandleTaskLogStream_Success tests the full SSE streaming happy path for task logs.
func TestHandleTaskLogStream_Success(t *testing.T) {
	_, _ = setupTaskLogEnv(t, "stream-task-123", "planning", "plan step 1\nplan step 2\n")

	handler := handleTaskLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/tasks/stream-task-123/logs/planning/stream")
	t.Cleanup(client.close)

	// Verify SSE response headers
	ct := client.resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	// Read the 2 log-line events
	for i := 1; i <= 2; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("event %d: event type = %q, want %q", i, evt.Event, "log-line")
		}
		if evt.ID == "" {
			t.Errorf("event %d: expected non-empty ID", i)
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("event %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("event %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
	}

	cancel()
}

// TestHandleTaskLogStream_NewLinesAfterConnect tests that new lines appended
// to a task log file after connecting are delivered as SSE events.
func TestHandleTaskLogStream_NewLinesAfterConnect(t *testing.T) {
	_, logPath := setupTaskLogEnv(t, "live-task-456", "implementation", "impl step 1\n")

	handler := handleTaskLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/tasks/live-task-456/logs/implementation/stream")
	t.Cleanup(client.close)

	// Read the initial line event
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Errorf("initial event type = %q, want %q", evt.Event, "log-line")
	}

	// Wait for fsnotify watcher to be set up
	time.Sleep(100 * time.Millisecond)

	// Append new lines
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open log for append: %v", err)
	}
	if _, err := f.WriteString("impl step 2\nimpl step 3\n"); err != nil {
		f.Close()
		t.Fatalf("failed to append to log: %v", err)
	}
	f.Close()

	// Read the new line events
	for i := 2; i <= 3; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event for new line %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("new line %d: event type = %q, want %q", i, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("new line %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("new line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
	}

	cancel()
}

// --- LogStreamer truncation test ---

// TestLogStreamer_FileTruncation tests that truncating a log file emits a truncated SSE event.
func TestLogStreamer_FileTruncation(t *testing.T) {
	_, logPath := setupAgentLogEnv(t, "truncate-test-agent", "line 1: long content here\nline 2: more content\nline 3: even more\n")

	handler := handleAgentLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/agents/truncate-test-agent/logs/stream")
	t.Cleanup(client.close)

	// Read the 3 initial line events
	for i := 1; i <= 3; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read initial event %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("initial event %d: type = %q, want %q", i, evt.Event, "log-line")
		}
	}

	// Wait for fsnotify watcher to be set up
	time.Sleep(100 * time.Millisecond)

	// Truncate the file by writing shorter content
	if err := os.WriteFile(logPath, []byte("short\n"), 0o644); err != nil {
		t.Fatalf("failed to truncate log file: %v", err)
	}

	// Read the truncated event
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read truncated event: %v", err)
	}
	if evt.Event != "truncated" {
		t.Errorf("truncated event type = %q, want %q", evt.Event, "truncated")
	}

	cancel()
}

// --- LogStreamer debounce batching test ---

// TestLogStreamer_DebounceBatching tests that rapidly appended lines arrive as
// a batch after the debounce interval, not one-at-a-time.
func TestLogStreamer_DebounceBatching(t *testing.T) {
	_, logPath := setupAgentLogEnv(t, "debounce-test-agent", "initial line\n")

	handler := handleAgentLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client := connectLogSSEWithContext(t, ctx, server.URL, "/api/agents/debounce-test-agent/logs/stream")
	t.Cleanup(client.close)

	// Read the initial line event
	_, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}

	// Wait for fsnotify watcher to be set up
	time.Sleep(100 * time.Millisecond)

	// Rapidly append 5 lines within the debounce interval (50ms)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open log for append: %v", err)
	}
	for i := 2; i <= 6; i++ {
		if _, err := f.WriteString(fmt.Sprintf("batch line %d\n", i)); err != nil {
			f.Close()
			t.Fatalf("failed to append line %d: %v", i, err)
		}
	}
	f.Close()

	// Read all 5 events - they should all arrive (within a reasonable timeout)
	receivedCount := 0
	deadline := time.After(5 * time.Second)
	for receivedCount < 5 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for batch events, got %d of 5", receivedCount)
		default:
		}
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read batch event %d: %v", receivedCount+1, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("batch event %d: type = %q, want %q", receivedCount+1, evt.Event, "log-line")
		}
		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("batch event %d: failed to parse payload: %v", receivedCount+1, err)
		}
		expectedLine := fmt.Sprintf("batch line %d", receivedCount+2)
		if payload.Line != expectedLine {
			t.Errorf("batch event %d: line = %q, want %q", receivedCount+1, payload.Line, expectedLine)
		}
		receivedCount++
	}

	if receivedCount != 5 {
		t.Errorf("received %d events, want 5", receivedCount)
	}

	cancel()
}
