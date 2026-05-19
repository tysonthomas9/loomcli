package backends

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEvents_EmptyInput(t *testing.T) {
	for _, backend := range []string{"claude", "codex", "opencode", "unknown"} {
		events, err := ParseEvents(backend, nil)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", backend, err)
		}
		if events != nil {
			t.Errorf("%s: want nil events, got %+v", backend, events)
		}
	}
}

func TestParseEvents_RoutesToClaude(t *testing.T) {
	data := []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}` + "\n")
	events, err := ParseEvents("claude", data)
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hi" {
		t.Errorf("got %+v", events)
	}
}

func TestParseEvents_RoutesToCodex(t *testing.T) {
	data := []byte(`{"timestamp":"2026-04-06T17:29:41.320Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n")
	events, err := ParseEvents("codex", data)
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hello" {
		t.Errorf("got %+v", events)
	}
}

func TestParseEvents_RoutesToOpenCode(t *testing.T) {
	data := []byte(`{"info":{"id":"s1"},"messages":[{"info":{"id":"m1","role":"user","time":{"created":0}},"parts":[{"type":"text","text":"howdy"}]}]}`)
	events, err := ParseEvents("opencode", data)
	if err != nil {
		t.Fatalf("opencode: %v", err)
	}
	if len(events) != 1 || events[0].Text != "howdy" {
		t.Errorf("got %+v", events)
	}
}

func TestParseEvents_UnknownBackendFallsBackToClaude(t *testing.T) {
	data := []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}` + "\n")
	events, err := ParseEvents("mystery", data)
	if err != nil {
		t.Fatalf("mystery backend: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hi" {
		t.Errorf("fallback did not route to claude: %+v", events)
	}
}

func TestParseEventsFromFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	events, err := ParseEventsFromFile("claude", missing)
	if err != nil || events != nil {
		t.Fatalf("missing file events=%+v err=%v, want nil nil", events, err)
	}

	path := filepath.Join(t.TempDir(), "claude.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","uuid":"u1","message":{"content":"from file"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	events, err = ParseEventsFromFile("claude", path)
	if err != nil {
		t.Fatalf("ParseEventsFromFile: %v", err)
	}
	if len(events) != 1 || events[0].Text != "from file" {
		t.Fatalf("events = %+v", events)
	}

	if _, err := ParseEventsFromFile("claude", t.TempDir()); err == nil {
		t.Fatal("directory read returned nil error")
	}
}
