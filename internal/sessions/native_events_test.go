package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNativeEvents_ReturnsNilWhenMissing(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{
		AgentName: "nova",
		Backend:   "claude",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	events, err := store.LoadNativeEvents(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if events != nil {
		t.Errorf("want nil, got %+v", events)
	}
}

func TestLoadNativeEvents_ParsesClaude(t *testing.T) {
	// Disable redaction for this test so the assertion on event text is deterministic.
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{
		AgentName: "nova",
		Backend:   "claude",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	src := filepath.Join(t.TempDir(), "native.jsonl")
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), src); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	events, err := store.LoadNativeEvents(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].Role != "user" || events[0].Text != "hi" {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].Role != "assistant" || events[1].Text != "hello" {
		t.Errorf("events[1] = %+v", events[1])
	}
}

// TestLoadNativeEvents_ParsesCanonicalTSLeafTranscript covers the daemon TS leaf,
// which writes its transcript ALREADY in the canonical transcript.Event format
// rather than the raw backend stream the Go-leaf hooks capture. The backend-keyed
// parser yields zero events for it; LoadNativeEvents must fall back to a direct
// canonical decode so serve's GET /transcript surfaces the entries (and the
// session_meta head + terminal result entry survive — aether #5d/#M2).
func TestLoadNativeEvents_ParsesCanonicalTSLeafTranscript(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Backend is codex — the codex parser only understands raw `exec --json`
	// (response_item lines) and returns 0 for canonical input, so this exercises
	// the canonical fallback, not the codex path.
	sess, err := store.CreateSession(CreateOptions{AgentName: "codex-coder", Backend: "codex"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	src := filepath.Join(t.TempDir(), "canonical.jsonl")
	payload := []byte(`{"role":"system","type":"session_meta","text":"local-cli-codex session for TASK-1"}` + "\n" +
		`{"role":"assistant","type":"text","text":"working on it"}` + "\n" +
		`{"role":"assistant","type":"tool_use","tool_name":"bash","tool_use_id":"t1"}` + "\n" +
		`{"role":"tool","type":"tool_result","tool_use_id":"t1","output":"done"}` + "\n" +
		`{"role":"system","type":"result","text":"completed | in=10 out=5"}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), src); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	events, err := store.LoadNativeEvents(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("want 5 canonical events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "session_meta" || events[0].Seq != 0 {
		t.Errorf("session_meta must lead at seq 0, got %+v", events[0])
	}
	if last := events[len(events)-1]; last.Type != "result" {
		t.Errorf("terminal entry must be type=result, got %+v", last)
	}
}
