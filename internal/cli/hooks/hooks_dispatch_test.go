package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestDispatchHookEvent_NilEvent(t *testing.T) {
	err := dispatchHookEvent(t.Context(), nil, "/tmp/loom-runtime", "sess-123")
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
	err := dispatchHookEvent(t.Context(), event, "", "sess-123")
	if err != nil {
		t.Fatalf("expected nil error for empty runtimeDir, got: %v", err)
	}
}

func TestDispatchHookEvent_EmptySessionID(t *testing.T) {
	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "hello",
		Timestamp: time.Now(),
	}
	err := dispatchHookEvent(t.Context(), event, "/tmp/loom-runtime", "")
	if err != nil {
		t.Fatalf("expected nil error for empty sessionID, got: %v", err)
	}
}

func TestDispatchHookEvent_SessionEndCapturesTokenUsage(t *testing.T) {
	runtimeDir := t.TempDir()
	sessionID := "test-session-token-capture"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
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

	err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify metadata was patched with token usage.
	store, err := sessions.NewStore(t.Context(), runtimeDir)
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
	runtimeDir := t.TempDir()
	sessionID := "test-session-missing-tx"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
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

	err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata should remain unchanged (zero tokens → captureTokenUsage skips save).
	store, err := sessions.NewStore(t.Context(), runtimeDir)
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
	runtimeDir := t.TempDir()
	sessionID := "test-session-empty-ref"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
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

	err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata should be unchanged.
	store, err := sessions.NewStore(t.Context(), runtimeDir)
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

func TestDispatchHookEvent_SyncsNativeTranscript(t *testing.T) {
	runtimeDir := t.TempDir()
	sessionID := "20260417-120000-nova-abcd-0123abcd"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Native transcript file that Claude (or another backend) is writing.
	native := filepath.Join(t.TempDir(), "native.jsonl")
	payload := `{"type":"user","uuid":"u1","message":{"content":"hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	if err := os.WriteFile(native, []byte(payload), 0o600); err != nil {
		t.Fatalf("write native: %v", err)
	}

	event := &HookEvent{
		Type:       HookTurnStart,
		Prompt:     "hello",
		SessionRef: native,
		Backend:    "claude",
		Timestamp:  time.Now(),
	}
	if err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(sessDir, sessions.NativeTranscriptFile))
	if err != nil {
		t.Fatalf("read synced native transcript: %v", err)
	}
	if string(got) != payload {
		t.Errorf("native transcript mismatch: got %q, want %q", got, payload)
	}
}

func TestDispatchHookEvent_SyncsSubagentTranscript(t *testing.T) {
	runtimeDir := t.TempDir()
	sessionID := "20260417-120000-nova-abcd-0123abcd"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Stage a parent transcript + subagent transcript in the layout Claude Code uses.
	parentDir := t.TempDir()
	parent := filepath.Join(parentDir, "parent.jsonl")
	if err := os.WriteFile(parent, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	subDir := filepath.Join(parentDir, "subagents")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	subID := "abc123xyz"
	subPayload := []byte(`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"sub work"}]}}` + "\n")
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+subID+".jsonl"), subPayload, 0o600); err != nil {
		t.Fatalf("write sub: %v", err)
	}

	event := &HookEvent{
		Type:       HookSubagentEnd,
		SessionRef: parent,
		SubagentID: subID,
		Backend:    "claude",
		Timestamp:  time.Now(),
	}
	if err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(sessDir, "subagents", "agent-"+subID+".jsonl"))
	if err != nil {
		t.Fatalf("read captured subagent transcript: %v", err)
	}
	if string(got) != string(subPayload) {
		t.Errorf("subagent capture mismatch: got %q, want %q", got, subPayload)
	}
}

func TestDispatchHookEvent_NoSessionRefSkipsSync(t *testing.T) {
	runtimeDir := t.TempDir()
	sessionID := "20260417-120000-nova-abcd-0123abcd"

	sessDir := filepath.Join(runtimeDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	event := &HookEvent{
		Type:      HookTurnStart,
		Prompt:    "hello",
		Backend:   "claude",
		Timestamp: time.Now(),
	}
	if err := dispatchHookEvent(t.Context(), event, runtimeDir, sessionID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessDir, sessions.NativeTranscriptFile)); !os.IsNotExist(err) {
		t.Errorf("expected no native transcript file, got err=%v", err)
	}
}
