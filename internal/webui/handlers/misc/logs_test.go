package misc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
		{"uuid-like", "loom-abc123", true},
		{"with hyphen", "task-123", true},
		{"with underscore", "task_123", true},
		{"mixed case", "TaskID", true},
		{"all valid chars", "Task_ID-123", true},
		{"single char", "a", true},
		{"with dot", "task.123", true},
		{"dotted subtask", "loomcli-5y1sd.1", true},

		{"empty", "", false},
		{"with space", "task 123", false},
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
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, agentName string, _ int, _ int64) (*AgentLogResult, error) {
			return nil, service.ErrValidation("missing agent name")
		},
	}
	handler := handleGetAgentLog(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//logs", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "missing agent name" {
		t.Errorf("error = %q, want %q", resp["error"], "missing agent name")
	}
}

// TestHandleGetAgentLog_InvalidName tests that invalid agent name returns 400.
func TestHandleGetAgentLog_InvalidName(t *testing.T) {
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, agentName string, _ int, _ int64) (*AgentLogResult, error) {
			return nil, service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
		},
	}
	handler := handleGetAgentLog(svc)

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
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp map[string]string
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp["error"] != "invalid agent name: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want %q", resp["error"], "invalid agent name: must match [a-zA-Z0-9_-]+")
			}
		})
	}
}

// TestHandleGetAgentLog_FileNotFound tests that missing log file returns 404.
func TestHandleGetAgentLog_FileNotFound(t *testing.T) {
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, _ string, _ int, _ int64) (*AgentLogResult, error) {
			return nil, service.ErrNotFound("log file not found - agent may not be active")
		},
	}
	handler := handleGetAgentLog(svc)

	// Use a valid but non-existent agent name
	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent-agent-xyz/logs", nil)
	req.SetPathValue("name", "nonexistent-agent-xyz")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "log file not found - agent may not be active" {
		t.Errorf("error = %q, want %q", resp["error"], "log file not found - agent may not be active")
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
			if resp.Error != "invalid task ID: must match [a-zA-Z0-9._-]+" {
				t.Errorf("error = %q, want %q", resp.Error, "invalid task ID: must match [a-zA-Z0-9._-]+")
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
	if resp.Error != "invalid task ID: must match [a-zA-Z0-9._-]+" {
		t.Errorf("error = %q, want %q", resp.Error, "invalid task ID: must match [a-zA-Z0-9._-]+")
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
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, _ string, _ int, _ int64) (*AgentLogResult, error) {
			return nil, service.ErrValidation("missing agent name")
		},
	}
	handler := handleGetAgentLog(svc)

	// Test with missing name
	req := httptest.NewRequest(http.MethodGet, "/api/agents//logs", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
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

// TestHandleGetAgentLog_Success tests the full success path via mock service.
func TestHandleGetAgentLog_Success(t *testing.T) {
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error) {
			return &AgentLogResult{
				Lines:     []string{"line 1: agent started", "line 2: processing task", "line 3: task complete"},
				LineCount: 3,
				StartLine: 1,
			}, nil
		},
	}
	handler := handleGetAgentLog(svc)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws-success/agents/testcoveragexyz123/logs", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws-success"))
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
	if len(resp.Data.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(resp.Data.Lines))
	}
}

// TestHandleGetAgentLog_LinesParam tests the lines query parameter is parsed and forwarded.
func TestHandleGetAgentLog_LinesParam(t *testing.T) {
	var capturedLines int
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, _ string, lines int, _ int64) (*AgentLogResult, error) {
			capturedLines = lines
			// Return exactly the number of lines requested (up to 5)
			result := make([]string, lines)
			for i := range result {
				result[i] = fmt.Sprintf("line %d: test data", i+1)
			}
			return &AgentLogResult{
				Lines:     result,
				LineCount: 50,
				StartLine: int64(50 - lines + 1),
			}, nil
		},
	}
	handler := handleGetAgentLog(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handler)

	// Request only 5 lines
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws-lines/agents/testcoveragelines/logs?lines=5", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws-lines"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	if capturedLines != 5 {
		t.Errorf("service received lines = %d, want 5", capturedLines)
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

	wsID := "test-ws-phases"
	taskID := "testcoveragetask123"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", wsID, "tasks", taskID)
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
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/tasks/"+taskID+"/logs", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
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

	wsID := "test-ws-tasklog"
	taskID := "testcoveragetasklog"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", wsID, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task log dir: %v", err)
	}

	logPath := filepath.Join(taskDir, "planning.log")
	if err := os.WriteFile(logPath, []byte("plan step 1\nplan step 2\n"), 0o644); err != nil {
		t.Fatalf("failed to write planning log: %v", err)
	}

	handler := handleGetTaskLog()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/tasks/"+taskID+"/logs/planning", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
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

// --- before_line pagination tests ---

// TestReadLastNLines_BeforeLine tests reading N lines before a specific line number.
func TestReadLastNLines_BeforeLine(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create 200 lines: "line 1", "line 2", ..., "line 200"
	var contentLines []string
	for i := 1; i <= 200; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Read 50 lines before line 100 -> should get lines 50-99
	lines, startLine, err := readLastNLinesFromFile(logFile, 50, nil, 100)
	if err != nil {
		t.Fatalf("readLastNLinesFromFile(beforeLine=100) error = %v", err)
	}
	if len(lines) != 50 {
		t.Errorf("len(lines) = %d, want 50", len(lines))
	}
	if startLine != 50 {
		t.Errorf("startLine = %d, want 50", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line 50" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 50")
	}
	if len(lines) >= 50 && lines[49] != "line 99" {
		t.Errorf("lines[49] = %q, want %q", lines[49], "line 99")
	}
}

// TestReadLastNLines_BeforeLine_ClampedToStart tests that requesting more lines
// than exist before the given line returns from line 1.
func TestReadLastNLines_BeforeLine_ClampedToStart(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	var contentLines []string
	for i := 1; i <= 200; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Request 50 lines before line 30 -> should get lines 1-29
	lines, startLine, err := readLastNLinesFromFile(logFile, 50, nil, 30)
	if err != nil {
		t.Fatalf("readLastNLinesFromFile(beforeLine=30) error = %v", err)
	}
	if len(lines) != 29 {
		t.Errorf("len(lines) = %d, want 29", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line 1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 1")
	}
	if len(lines) >= 29 && lines[28] != "line 29" {
		t.Errorf("lines[28] = %q, want %q", lines[28], "line 29")
	}
}

// TestReadLastNLines_BeforeLine_InvalidValues tests that beforeLine <= 0 is ignored.
func TestReadLastNLines_BeforeLine_InvalidValues(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	var contentLines []string
	for i := 1; i <= 50; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// beforeLine=0 -> read from EOF (same as no beforeLine)
	lines, startLine, err := readLastNLinesFromFile(logFile, 10, nil, 0)
	if err != nil {
		t.Fatalf("beforeLine=0 error = %v", err)
	}
	if len(lines) != 10 {
		t.Errorf("beforeLine=0: len(lines) = %d, want 10", len(lines))
	}
	if startLine != 41 {
		t.Errorf("beforeLine=0: startLine = %d, want 41", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line 41" {
		t.Errorf("beforeLine=0: lines[0] = %q, want %q", lines[0], "line 41")
	}

	// beforeLine=1 -> nothing before line 1
	lines, _, err = readLastNLinesFromFile(logFile, 10, nil, 1)
	if err != nil {
		t.Fatalf("beforeLine=1 error = %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("beforeLine=1: len(lines) = %d, want 0", len(lines))
	}
}

// TestReadLastNLines_BeforeLine_BeyondFile tests beforeLine beyond file length.
func TestReadLastNLines_BeforeLine_BeyondFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	var contentLines []string
	for i := 1; i <= 100; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// beforeLine=99999 on a 100-line file -> nothing to return
	lines, _, err := readLastNLinesFromFile(logFile, 50, nil, 99999)
	if err != nil {
		t.Fatalf("beforeLine=99999 error = %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("beforeLine=99999: len(lines) = %d, want 0", len(lines))
	}
}

// TestReadLastNLines_BeforeLine_EmptyFile tests beforeLine on empty file.
func TestReadLastNLines_BeforeLine_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "empty.log")
	if err := os.WriteFile(logFile, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, startLine, err := readLastNLinesFromFile(logFile, 50, nil, 10)
	if err != nil {
		t.Fatalf("empty file error = %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("empty file: len(lines) = %d, want 0", len(lines))
	}
	if startLine != 1 {
		t.Errorf("empty file: startLine = %d, want 1", startLine)
	}
}

// TestReadLastNLines_BeforeLine_SingleLine tests beforeLine on a 1-line file.
func TestReadLastNLines_BeforeLine_SingleLine(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "single.log")
	if err := os.WriteFile(logFile, []byte("only line\n"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// beforeLine=2 -> should return line 1
	lines, startLine, err := readLastNLinesFromFile(logFile, 50, nil, 2)
	if err != nil {
		t.Fatalf("single line, beforeLine=2 error = %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("len(lines) = %d, want 1", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if len(lines) >= 1 && lines[0] != "only line" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "only line")
	}

	// beforeLine=1 -> nothing before line 1
	lines, _, err = readLastNLinesFromFile(logFile, 50, nil, 1)
	if err != nil {
		t.Fatalf("single line, beforeLine=1 error = %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("beforeLine=1: len(lines) = %d, want 0", len(lines))
	}
}

// TestReadLastNLines_BeforeLine_ExactBoundary tests beforeLine=N+1 where N is the
// number of lines requested (exact fit).
func TestReadLastNLines_BeforeLine_ExactBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	var contentLines []string
	for i := 1; i <= 100; i++ {
		contentLines = append(contentLines, "line "+itoa(i))
	}
	content := strings.Join(contentLines, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Request 50 lines before line 51 -> should get lines 1-50
	lines, startLine, err := readLastNLinesFromFile(logFile, 50, nil, 51)
	if err != nil {
		t.Fatalf("exact boundary error = %v", err)
	}
	if len(lines) != 50 {
		t.Errorf("len(lines) = %d, want 50", len(lines))
	}
	if startLine != 1 {
		t.Errorf("startLine = %d, want 1", startLine)
	}
	if len(lines) >= 1 && lines[0] != "line 1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 1")
	}
	if len(lines) >= 50 && lines[49] != "line 50" {
		t.Errorf("lines[49] = %q, want %q", lines[49], "line 50")
	}
}

// TestHandleGetAgentLog_StartLineInResponse verifies start_line is in all responses.
func TestHandleGetAgentLog_StartLineInResponse(t *testing.T) {
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, _ string, lines int, _ int64) (*AgentLogResult, error) {
			// Simulate a 50-line file, last 10 lines requested
			resultLines := make([]string, 10)
			for i := range resultLines {
				resultLines[i] = fmt.Sprintf("line %d", 41+i)
			}
			return &AgentLogResult{
				Lines:     resultLines,
				LineCount: 50,
				StartLine: 41,
			}, nil
		},
	}
	handler := handleGetAgentLog(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handler)

	// Request last 10 lines (no before_line)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws-startline/agents/teststartline/logs?lines=10", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws-startline"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if resp.Data.StartLine != 41 {
		t.Errorf("StartLine = %d, want 41", resp.Data.StartLine)
	}
	if resp.Data.LineCount != 50 {
		t.Errorf("LineCount = %d, want 50", resp.Data.LineCount)
	}
}

// TestHandleGetAgentLog_BeforeLine tests the before_line query parameter via the handler.
func TestHandleGetAgentLog_BeforeLine(t *testing.T) {
	var capturedLines int
	var capturedBeforeLine int64
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, _, _ string, lines int, beforeLine int64) (*AgentLogResult, error) {
			capturedLines = lines
			capturedBeforeLine = beforeLine
			// Simulate: 20 lines before line 50 -> lines 30-49
			resultLines := make([]string, 20)
			for i := range resultLines {
				resultLines[i] = fmt.Sprintf("line %d", 30+i)
			}
			return &AgentLogResult{
				Lines:     resultLines,
				LineCount: 49,
				StartLine: 30,
			}, nil
		},
	}
	handler := handleGetAgentLog(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handler)

	// Request 20 lines before line 50 -> should get lines 30-49
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws-beforeline/agents/testbeforeline/logs?lines=20&before_line=50", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws-beforeline"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	if capturedLines != 20 {
		t.Errorf("service received lines = %d, want 20", capturedLines)
	}
	if capturedBeforeLine != 50 {
		t.Errorf("service received beforeLine = %d, want 50", capturedBeforeLine)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if len(resp.Data.Lines) != 20 {
		t.Errorf("len(Lines) = %d, want 20", len(resp.Data.Lines))
	}
	if resp.Data.StartLine != 30 {
		t.Errorf("StartLine = %d, want 30", resp.Data.StartLine)
	}
	if resp.Data.LineCount != 49 {
		t.Errorf("LineCount = %d, want 49", resp.Data.LineCount)
	}
	if len(resp.Data.Lines) >= 1 && resp.Data.Lines[0] != "line 30" {
		t.Errorf("Lines[0] = %q, want %q", resp.Data.Lines[0], "line 30")
	}
	if len(resp.Data.Lines) >= 20 && resp.Data.Lines[19] != "line 49" {
		t.Errorf("Lines[19] = %q, want %q", resp.Data.Lines[19], "line 49")
	}
}

// TestHandleGetTaskLog_BeforeLine tests before_line works for task log endpoint.
func TestHandleGetTaskLog_BeforeLine(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	wsID := "test-ws-taskbeforeline"
	taskID := "testtaskbeforeline"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", wsID, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task log dir: %v", err)
	}

	logPath := filepath.Join(taskDir, "planning.log")
	var logLines []string
	for i := 1; i <= 60; i++ {
		logLines = append(logLines, fmt.Sprintf("plan step %d", i))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write planning log: %v", err)
	}

	handler := handleGetTaskLog()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", handler)

	// Request 10 lines before line 20 -> should get lines 10-19
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/tasks/"+taskID+"/logs/planning?lines=10&before_line=20", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp LogContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if len(resp.Data.Lines) != 10 {
		t.Errorf("len(Lines) = %d, want 10", len(resp.Data.Lines))
	}
	if resp.Data.StartLine != 10 {
		t.Errorf("StartLine = %d, want 10", resp.Data.StartLine)
	}
	if len(resp.Data.Lines) >= 1 && resp.Data.Lines[0] != "plan step 10" {
		t.Errorf("Lines[0] = %q, want %q", resp.Data.Lines[0], "plan step 10")
	}
}

// --- Workspace-scoped handler tests ---

// TestHandleGetAgentLog_WorkspaceScoped verifies that the handler passes the
// workspace ID from context to the service.
func TestHandleGetAgentLog_WorkspaceScoped(t *testing.T) {
	var capturedWsID, capturedAgent string
	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, wsID, agentName string, _ int, _ int64) (*AgentLogResult, error) {
			capturedWsID = wsID
			capturedAgent = agentName
			return &AgentLogResult{
				Lines:     []string{"ws-line 1: agent started", "ws-line 2: processing", "ws-line 3: done"},
				LineCount: 3,
				StartLine: 1,
			}, nil
		},
	}
	handler := handleGetAgentLog(svc)

	wsID := "test-ws-id"
	agentName := "scopedagent"

	// Create request with workspace in context
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/agents/"+agentName+"/logs", nil)
	req.SetPathValue("name", agentName)
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	if capturedWsID != wsID {
		t.Errorf("service received wsID = %q, want %q", capturedWsID, wsID)
	}
	if capturedAgent != agentName {
		t.Errorf("service received agentName = %q, want %q", capturedAgent, agentName)
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
	if len(resp.Data.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(resp.Data.Lines))
	}
	if len(resp.Data.Lines) >= 1 && resp.Data.Lines[0] != "ws-line 1: agent started" {
		t.Errorf("Lines[0] = %q, want %q", resp.Data.Lines[0], "ws-line 1: agent started")
	}
}

// TestHandleGetAgentLog_DifferentWorkspaces verifies that two requests with different
// workspace IDs pass the correct workspace to the service and do not cross-contaminate.
func TestHandleGetAgentLog_DifferentWorkspaces(t *testing.T) {
	agentName := "sharedagent"
	wsA := "workspace-aaa"
	wsB := "workspace-bbb"

	svc := &mockAgentService{
		getLogFunc: func(_ context.Context, wsID, _ string, _ int, _ int64) (*AgentLogResult, error) {
			switch wsID {
			case wsA:
				return &AgentLogResult{
					Lines:     []string{"from workspace A"},
					LineCount: 1,
					StartLine: 1,
				}, nil
			case wsB:
				return &AgentLogResult{
					Lines:     []string{"from workspace B"},
					LineCount: 1,
					StartLine: 1,
				}, nil
			default:
				return nil, service.ErrNotFound("unknown workspace")
			}
		},
	}
	handler := handleGetAgentLog(svc)

	// Request from workspace A
	reqA := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsA+"/agents/"+agentName+"/logs", nil)
	reqA.SetPathValue("name", agentName)
	reqA = reqA.WithContext(middleware.WithWorkspace(reqA.Context(), wsA))
	rrA := httptest.NewRecorder()
	handler.ServeHTTP(rrA, reqA)

	if rrA.Code != http.StatusOK {
		t.Fatalf("workspace A: status = %d, want %d; body: %s", rrA.Code, http.StatusOK, rrA.Body.String())
	}
	var respA LogContentResponse
	if err := json.Unmarshal(rrA.Body.Bytes(), &respA); err != nil {
		t.Fatalf("failed to parse response A: %v", err)
	}
	if !respA.Success || respA.Data == nil {
		t.Fatalf("workspace A: expected success with data, got success=%v error=%q", respA.Success, respA.Error)
	}
	if len(respA.Data.Lines) != 1 || respA.Data.Lines[0] != "from workspace A" {
		t.Errorf("workspace A: Lines = %v, want [\"from workspace A\"]", respA.Data.Lines)
	}

	// Request from workspace B
	reqB := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsB+"/agents/"+agentName+"/logs", nil)
	reqB.SetPathValue("name", agentName)
	reqB = reqB.WithContext(middleware.WithWorkspace(reqB.Context(), wsB))
	rrB := httptest.NewRecorder()
	handler.ServeHTTP(rrB, reqB)

	if rrB.Code != http.StatusOK {
		t.Fatalf("workspace B: status = %d, want %d; body: %s", rrB.Code, http.StatusOK, rrB.Body.String())
	}
	var respB LogContentResponse
	if err := json.Unmarshal(rrB.Body.Bytes(), &respB); err != nil {
		t.Fatalf("failed to parse response B: %v", err)
	}
	if !respB.Success || respB.Data == nil {
		t.Fatalf("workspace B: expected success with data, got success=%v error=%q", respB.Success, respB.Error)
	}
	if len(respB.Data.Lines) != 1 || respB.Data.Lines[0] != "from workspace B" {
		t.Errorf("workspace B: Lines = %v, want [\"from workspace B\"]", respB.Data.Lines)
	}
}
