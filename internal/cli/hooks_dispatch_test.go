package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestDispatchHookEvent_NilEvent(t *testing.T) {
	err := dispatchHookEvent(nil, "/tmp/beads", "sess-123")
	if err != nil {
		t.Fatalf("expected nil error for nil event, got: %v", err)
	}
}

func TestDispatchHookEvent_EmptyBeadsDir(t *testing.T) {
	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "hello",
		Timestamp: time.Now(),
	}
	err := dispatchHookEvent(event, "", "sess-123")
	if err != nil {
		t.Fatalf("expected nil error for empty beadsDir, got: %v", err)
	}
}

func TestDispatchHookEvent_EmptySessionID(t *testing.T) {
	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "hello",
		Timestamp: time.Now(),
	}
	err := dispatchHookEvent(event, "/tmp/beads", "")
	if err != nil {
		t.Fatalf("expected nil error for empty sessionID, got: %v", err)
	}
}

func TestDispatchHookEvent_TurnStart(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-turn-start"

	// Create the session directory so AppendTranscript can find it.
	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "implement the feature",
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "user" {
		t.Errorf("expected role %q, got %q", "user", entry.Role)
	}
	if entry.Type != "text" {
		t.Errorf("expected type %q, got %q", "text", entry.Type)
	}
	if entry.Content != "implement the feature" {
		t.Errorf("expected content %q, got %q", "implement the feature", entry.Content)
	}
}

func TestDispatchHookEvent_TurnEnd(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-turn-end"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookTurnEnd,
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "assistant" {
		t.Errorf("expected role %q, got %q", "assistant", entry.Role)
	}
	if entry.Type != "turn_end" {
		t.Errorf("expected type %q, got %q", "turn_end", entry.Type)
	}
	if entry.Content != "Turn completed" {
		t.Errorf("expected content %q, got %q", "Turn completed", entry.Content)
	}
}

func TestDispatchHookEvent_SessionStart(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-start"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookSessionStart,
		Model:     "claude-sonnet-4-20250514",
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "system" {
		t.Errorf("expected role %q, got %q", "system", entry.Role)
	}
	if entry.Type != "session_start" {
		t.Errorf("expected type %q, got %q", "session_start", entry.Type)
	}
	if !strings.Contains(entry.Content, "claude-sonnet-4-20250514") {
		t.Errorf("expected content to contain model name, got %q", entry.Content)
	}
}

func TestDispatchHookEvent_SessionEnd(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-end"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookSessionEnd,
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "system" {
		t.Errorf("expected role %q, got %q", "system", entry.Role)
	}
	if entry.Type != "session_end" {
		t.Errorf("expected type %q, got %q", "session_end", entry.Type)
	}
	if entry.Content != "Session ended" {
		t.Errorf("expected content %q, got %q", "Session ended", entry.Content)
	}
}

func TestDispatchHookEvent_SubagentStart(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-subagent-start"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookSubagentStart,
		ToolUseID: "toolu_abc123",
		ToolInput: json.RawMessage(`{"description":"run tests","prompt":"test all"}`),
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "system" {
		t.Errorf("expected role %q, got %q", "system", entry.Role)
	}
	if entry.Type != "subagent_start" {
		t.Errorf("expected type %q, got %q", "subagent_start", entry.Type)
	}
	if entry.ToolName != "Task" {
		t.Errorf("expected tool_name %q, got %q", "Task", entry.ToolName)
	}
	if !strings.Contains(entry.Content, "toolu_abc123") {
		t.Errorf("expected content to contain tool_use_id, got %q", entry.Content)
	}
}

func TestDispatchHookEvent_SubagentEnd(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-subagent-end"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:       HookSubagentEnd,
		ToolUseID:  "toolu_xyz789",
		SubagentID: "agent-sub-001",
		ToolInput:  json.RawMessage(`{}`),
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "system" {
		t.Errorf("expected role %q, got %q", "system", entry.Role)
	}
	if entry.Type != "subagent_end" {
		t.Errorf("expected type %q, got %q", "subagent_end", entry.Type)
	}
	if entry.ToolName != "Task" {
		t.Errorf("expected tool_name %q, got %q", "Task", entry.ToolName)
	}
	if !strings.Contains(entry.Content, "agent-sub-001") {
		t.Errorf("expected content to contain agent_id, got %q", entry.Content)
	}
}

func TestDispatchHookEvent_SubagentEnd_NoAgentID(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-subagent-end-no-id"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookSubagentEnd,
		ToolUseID: "toolu_xyz789",
		ToolInput: json.RawMessage(`{}`),
		Timestamp: time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := readSingleTranscriptEntry(t, sessDir)
	if entry.Role != "system" {
		t.Errorf("expected role %q, got %q", "system", entry.Role)
	}
	if entry.Type != "subagent_end" {
		t.Errorf("expected type %q, got %q", "subagent_end", entry.Type)
	}
	if !strings.Contains(entry.Content, "toolu_xyz789") {
		t.Errorf("expected content to contain tool_use_id as fallback, got %q", entry.Content)
	}
}

func TestDispatchHookEvent_StoreError(t *testing.T) {
	// Use a valid beads dir but a session ID that doesn't have a directory.
	// AppendTranscript will fail because the session dir doesn't exist.
	// dispatchHookEvent should log to stderr and still return nil.
	beadsDir := t.TempDir()

	// Create the sessions/ directory (NewStore needs it) but NOT the session subdirectory.
	if err := os.MkdirAll(filepath.Join(beadsDir, "sessions"), 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "this will fail",
		Timestamp: time.Now(),
	}

	// Capture stderr to verify the error is logged.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := dispatchHookEvent(event, beadsDir, "nonexistent-session")

	_ = w.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("expected nil error even on store failure, got: %v", err)
	}

	// Read stderr output.
	scanner := bufio.NewScanner(r)
	var stderrOutput string
	for scanner.Scan() {
		stderrOutput += scanner.Text()
	}
	_ = r.Close()

	if !strings.Contains(stderrOutput, "failed to append transcript") {
		t.Errorf("expected stderr to contain error message, got: %q", stderrOutput)
	}
}

// readSingleTranscriptEntry reads the first (and expected only) entry from
// transcript.jsonl in the given session directory.
func readSingleTranscriptEntry(t *testing.T, sessDir string) sessions.TranscriptEntry {
	t.Helper()

	txPath := filepath.Join(sessDir, "transcript.jsonl")
	data, err := os.ReadFile(txPath)
	if err != nil {
		t.Fatalf("read transcript.jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d", len(lines))
	}

	var entry sessions.TranscriptEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal transcript entry: %v", err)
	}

	return entry
}
