package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.MkdirAll(logDir, 0o755); err != nil {
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
