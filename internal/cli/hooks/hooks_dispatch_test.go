package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

const testHooksWorkspaceID = "00000000-0000-4000-8000-000000000aaa"

// seedCentralSession creates the session dir under the HOME-resolved central
// store and writes a minimal metadata.json. Returns the session dir path.
func seedCentralSession(t *testing.T, sessionID string) string {
	t.Helper()
	sessDir := filepath.Join(os.Getenv("HOME"), ".loom", "sessions", testHooksWorkspaceID, sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
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
	return sessDir
}

func TestDispatchHookEvent_NilEvent(t *testing.T) {
	if err := dispatchHookEvent(nil, testHooksWorkspaceID, "sess-123"); err != nil {
		t.Fatalf("expected nil error for nil event, got: %v", err)
	}
}

func TestDispatchHookEvent_EmptyWorkspaceID(t *testing.T) {
	event := &HookEvent{Type: HookTurnStart, Prompt: "hello", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, "", "sess-123"); err != nil {
		t.Fatalf("expected nil error for empty workspaceID, got: %v", err)
	}
}

func TestDispatchHookEvent_EmptySessionID(t *testing.T) {
	event := &HookEvent{Type: HookTurnStart, Prompt: "hello", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, ""); err != nil {
		t.Fatalf("expected nil error for empty sessionID, got: %v", err)
	}
}

func TestDispatchHookEvent_SessionEndCapturesTokenUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sessionID := "test-session-token-capture"
	seedCentralSession(t, sessionID)

	// Write a Claude transcript JSONL file with usage data.
	txDir := t.TempDir()
	txPath := filepath.Join(txDir, "claude-transcript.jsonl")
	txLines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_creation_input_tokens":100}}}
{"type":"assistant","message":{"id":"msg_002","role":"assistant","content":[],"usage":{"input_tokens":2000,"output_tokens":800,"cache_read_input_tokens":300,"cache_creation_input_tokens":50}}}
`
	if err := os.WriteFile(txPath, []byte(txLines), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	event := &HookEvent{Type: HookSessionEnd, SessionRef: txPath, Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store, err := sessions.NewStoreForWorkspace(testHooksWorkspaceID)
	if err != nil {
		t.Fatalf("NewStoreForWorkspace: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	if loaded.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", loaded.InputTokens)
	}
	if loaded.OutputTokens != 1300 {
		t.Errorf("OutputTokens = %d, want 1300", loaded.OutputTokens)
	}
	if loaded.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", loaded.CacheReadTokens)
	}
	if loaded.CacheWriteTokens != 150 {
		t.Errorf("CacheWriteTokens = %d, want 150", loaded.CacheWriteTokens)
	}
	if loaded.EstimatedCostUSD <= 0 {
		t.Errorf("EstimatedCostUSD = %f, want > 0", loaded.EstimatedCostUSD)
	}
}

func TestDispatchHookEvent_SessionEndMissingTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sessionID := "test-session-missing-tx"
	seedCentralSession(t, sessionID)

	event := &HookEvent{Type: HookSessionEnd, SessionRef: "/nonexistent/transcript.jsonl", Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store, err := sessions.NewStoreForWorkspace(testHooksWorkspaceID)
	if err != nil {
		t.Fatalf("NewStoreForWorkspace: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.InputTokens != 0 || loaded.OutputTokens != 0 {
		t.Errorf("expected zero tokens, got in=%d out=%d", loaded.InputTokens, loaded.OutputTokens)
	}
}

func TestDispatchHookEvent_SessionEndEmptySessionRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sessionID := "test-session-empty-ref"
	seedCentralSession(t, sessionID)

	event := &HookEvent{Type: HookSessionEnd, SessionRef: "", Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store, err := sessions.NewStoreForWorkspace(testHooksWorkspaceID)
	if err != nil {
		t.Fatalf("NewStoreForWorkspace: %v", err)
	}
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.InputTokens != 0 || loaded.OutputTokens != 0 {
		t.Errorf("expected zero tokens (captureTokenUsage should not have run), got in=%d out=%d", loaded.InputTokens, loaded.OutputTokens)
	}
}

func TestDispatchHookEvent_SyncsNativeTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sessionID := "20260417-120000-nova-abcd-0123abcd"
	sessDir := seedCentralSession(t, sessionID)

	native := filepath.Join(t.TempDir(), "native.jsonl")
	payload := `{"type":"user","uuid":"u1","message":{"content":"hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	if err := os.WriteFile(native, []byte(payload), 0o600); err != nil {
		t.Fatalf("write native: %v", err)
	}

	event := &HookEvent{Type: HookTurnStart, Prompt: "hello", SessionRef: native, Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
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
	t.Setenv("HOME", t.TempDir())
	sessionID := "20260417-120000-nova-abcd-0123abcd"
	sessDir := seedCentralSession(t, sessionID)

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

	event := &HookEvent{Type: HookSubagentEnd, SessionRef: parent, SubagentID: subID, Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
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
	t.Setenv("HOME", t.TempDir())
	sessionID := "20260417-120000-nova-abcd-0123abcd"
	sessDir := seedCentralSession(t, sessionID)

	event := &HookEvent{Type: HookTurnStart, Prompt: "hello", Backend: "claude", Timestamp: time.Now()}
	if err := dispatchHookEvent(event, testHooksWorkspaceID, sessionID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessDir, sessions.NativeTranscriptFile)); !os.IsNotExist(err) {
		t.Errorf("expected no native transcript file, got err=%v", err)
	}
}
