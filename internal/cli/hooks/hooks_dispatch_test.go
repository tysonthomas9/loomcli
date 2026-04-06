package hooks

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
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
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
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
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
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
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
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
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
	if err := os.MkdirAll(filepath.Join(beadsDir, "sessions"), 0o700); err != nil {
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
	sessionID := "test-session-token-capture"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write initial metadata.json so LoadMetadata works.
	meta := map[string]interface{}{
		"schema_version": 1,
		"session_id":     sessionID,
		"agent_name":     "nova",
		"backend":        "claude",
		"status":         "running",
		"started_at":     "2026-03-28T10:00:00Z",
		"exit_code":      0,
		"input_tokens":   0,
		"output_tokens":  0,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaBytes, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	// Write a Claude transcript JSONL file with usage data.
	txDir := t.TempDir()
	txPath := filepath.Join(txDir, "claude-transcript.jsonl")
	txLines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_creation_input_tokens":100}}}
{"type":"assistant","message":{"id":"msg_002","role":"assistant","content":[],"usage":{"input_tokens":2000,"output_tokens":800,"cache_read_input_tokens":300,"cache_creation_input_tokens":50}}}
`
	if err := os.WriteFile(txPath, []byte(txLines), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: txPath,
		Backend:    "claude",
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify metadata was patched with token usage.
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	if loaded.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000 (1000+2000)", loaded.InputTokens)
	}
	if loaded.OutputTokens != 1300 {
		t.Errorf("OutputTokens = %d, want 1300 (500+800)", loaded.OutputTokens)
	}
	if loaded.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500 (200+300)", loaded.CacheReadTokens)
	}
	if loaded.CacheWriteTokens != 150 {
		t.Errorf("CacheWriteTokens = %d, want 150 (100+50)", loaded.CacheWriteTokens)
	}
	if loaded.EstimatedCostUSD <= 0 {
		t.Errorf("EstimatedCostUSD = %f, want > 0", loaded.EstimatedCostUSD)
	}
}

func TestDispatchHookEvent_SessionEndMissingTranscript(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-missing-tx"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write initial metadata with zero tokens.
	meta := map[string]interface{}{
		"schema_version": 1,
		"session_id":     sessionID,
		"agent_name":     "nova",
		"backend":        "claude",
		"status":         "running",
		"started_at":     "2026-03-28T10:00:00Z",
		"exit_code":      0,
		"input_tokens":   0,
		"output_tokens":  0,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaBytes, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	// Point to a nonexistent transcript — SumTranscriptUsage returns zero, nil.
	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: "/nonexistent/transcript.jsonl",
		Backend:    "claude",
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata should remain unchanged (zero tokens → captureTokenUsage skips save).
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (unchanged)", loaded.InputTokens)
	}
	if loaded.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (unchanged)", loaded.OutputTokens)
	}
}

func TestDispatchHookEvent_SessionEndEmptySessionRef(t *testing.T) {
	beadsDir := t.TempDir()
	sessionID := "test-session-empty-ref"

	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write initial metadata with zero tokens.
	meta := map[string]interface{}{
		"schema_version": 1,
		"session_id":     sessionID,
		"agent_name":     "nova",
		"backend":        "claude",
		"status":         "running",
		"started_at":     "2026-03-28T10:00:00Z",
		"exit_code":      0,
		"input_tokens":   0,
		"output_tokens":  0,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaBytes, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	// SessionEnd with empty SessionRef — captureTokenUsage should NOT be called.
	event := &HookEvent{
		Type:       HookSessionEnd,
		SessionRef: "", // empty
		Backend:    "claude",
		Timestamp:  time.Now(),
	}

	err := dispatchHookEvent(event, beadsDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata should be unchanged.
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (captureTokenUsage should not have been called)", loaded.InputTokens)
	}
	if loaded.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (captureTokenUsage should not have been called)", loaded.OutputTokens)
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
