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

func TestDispatchHookEvent_SessionEndCapturesTokenUsage(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-token-usage"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write metadata.json (required for LoadMetadata)
	metaJSON := `{
		"session_id": "test-session-token-usage",
		"agent_name": "nova",
		"backend": "claude",
		"started_at": "2026-03-23T00:00:00Z",
		"status": "running",
		"exit_code": 0
	}`
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), []byte(metaJSON), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	// Write a fake Claude transcript file with usage data
	transcriptDir := t.TempDir()
	transcriptPath := filepath.Join(transcriptDir, "transcript.jsonl")
	transcriptContent := `{"type":"message","role":"assistant","usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_creation_input_tokens":100}}
{"type":"message","role":"assistant","usage":{"input_tokens":2000,"output_tokens":300,"cache_read_input_tokens":50,"cache_creation_input_tokens":25}}
`
	if err := os.WriteFile(transcriptPath, []byte(transcriptContent), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: transcriptPath,
		Backend:    "claude",
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read back metadata and verify token usage was captured
	store, storeErr := sessions.NewStore(beadsDir)
	if storeErr != nil {
		t.Fatalf("create store: %v", storeErr)
	}
	meta, metaErr := store.LoadMetadata(sessionID)
	if metaErr != nil {
		t.Fatalf("load metadata: %v", metaErr)
	}

	if meta.InputTokens != 3000 {
		t.Errorf("InputTokens: expected 3000, got %d", meta.InputTokens)
	}
	if meta.OutputTokens != 800 {
		t.Errorf("OutputTokens: expected 800, got %d", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 250 {
		t.Errorf("CacheReadTokens: expected 250, got %d", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 125 {
		t.Errorf("CacheWriteTokens: expected 125, got %d", meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD <= 0 {
		t.Errorf("EstimatedCostUSD should be positive, got %f", meta.EstimatedCostUSD)
	}
}

func TestDispatchHookEvent_SessionEndMissingTranscript(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-no-transcript"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write metadata.json
	metaJSON := `{
		"session_id": "test-session-no-transcript",
		"agent_name": "nova",
		"backend": "claude",
		"started_at": "2026-03-23T00:00:00Z",
		"status": "running",
		"exit_code": 0
	}`
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), []byte(metaJSON), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: "/nonexistent/transcript.jsonl",
		Backend:    "claude",
		Timestamp:  time.Now(),
	}

	// Should not error — missing transcript is graceful
	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Metadata tokens should remain 0
	store, _ := sessions.NewStore(beadsDir)
	meta, metaErr := store.LoadMetadata(sessionID)
	if metaErr != nil {
		t.Fatalf("load metadata: %v", metaErr)
	}
	if meta.InputTokens != 0 || meta.OutputTokens != 0 {
		t.Errorf("expected zero tokens for missing transcript, got input=%d output=%d",
			meta.InputTokens, meta.OutputTokens)
	}
}

func TestDispatchHookEvent_SessionEndEmptyRef(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-empty-ref"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: "", // empty — should skip capture
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
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
