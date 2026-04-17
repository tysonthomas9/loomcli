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
