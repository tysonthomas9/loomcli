package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

func TestLoadNativeEvents_ReturnsNilWhenMissing(t *testing.T) {
	store, err := NewStore(t.Context(), t.TempDir())
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

	store, err := NewStore(t.Context(), t.TempDir())
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
	if err := store.SyncNativeTranscript(sess.SessionID(), src, TranscriptFormatRaw); err != nil {
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

func TestLoadNativeEvents_ParsesCodex(t *testing.T) {
	payload := []byte(`{"timestamp":"2026-04-06T17:29:41.321Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"write a file"}]}}` + "\n" +
		`{"timestamp":"2026-04-06T17:29:51.062Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n" +
		`{"timestamp":"2026-04-06T17:29:51.064Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"true\"}","call_id":"call_1"}}` + "\n" +
		`{"timestamp":"2026-04-06T17:29:51.146Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"ok"}}` + "\n")
	events, err := loadRawNativeEvents(t, "codex", payload)
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d: %+v", len(events), events)
	}
	if events[0].Role != "user" || events[0].Text != "write a file" {
		t.Errorf("user event = %+v", events[0])
	}
	if events[2].Type != "tool_use" || events[2].ToolName != "exec_command" || events[2].ToolUseID != "call_1" {
		t.Errorf("tool event = %+v", events[2])
	}
	if events[3].Type != "tool_result" || events[3].ToolUseID != "call_1" {
		t.Errorf("tool result = %+v", events[3])
	}
}

func TestLoadNativeEvents_ParsesOpenCode(t *testing.T) {
	payload := []byte(`{"info":{"id":"s1"},"messages":[{"info":{"id":"m1","role":"user","time":{"created":0}},"parts":[{"type":"text","text":"howdy"}]}]}`)
	events, err := loadRawNativeEvents(t, "opencode", payload)
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) != 1 || events[0].Role != "user" || events[0].Text != "howdy" {
		t.Fatalf("events = %+v, want one OpenCode user event", events)
	}
}

func TestLoadNativeEvents_RejectsUnknownBackend(t *testing.T) {
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"must not be guessed"}}` + "\n")
	events, err := loadRawNativeEvents(t, "mystery", payload)
	if err == nil || !strings.Contains(err.Error(), `unsupported native transcript backend "mystery"`) {
		t.Fatalf("error = %v, want unsupported backend", err)
	}
	if events != nil {
		t.Fatalf("events = %+v, want nil", events)
	}
}

func loadRawNativeEvents(t *testing.T, backend string, payload []byte) ([]transcript.Event, error) {
	t.Helper()
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	store, err := NewStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{AgentName: "parser-test", Backend: backend})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	src := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write native transcript: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), src, TranscriptFormatRaw); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}
	return store.LoadNativeEvents(sess.SessionID())
}

// TestLoadNativeEvents_ParsesCanonicalTSLeafTranscript covers the daemon TS leaf,
// which writes its transcript ALREADY in the canonical transcript.Event format
// rather than the raw backend stream the Go-leaf hooks capture. The canonical
// marker (recorded by SyncNativeTranscript) routes LoadNativeEvents straight to a
// canonical decode — NOT the backend-keyed parser — so serve's GET /transcript
// surfaces the entries (and the session_meta head + terminal result entry survive
// — aether #5d/#M2). The backend is codex precisely to prove the marker, not a
// parser heuristic, decides: the codex parser would yield 0 events for this input.
func TestLoadNativeEvents_ParsesCanonicalTSLeafTranscript(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store, err := NewStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Backend is codex — the codex parser only understands raw `exec --json`
	// (response_item lines) and returns 0 for canonical input, so this exercises
	// the canonical path, not the codex path.
	sess, err := store.CreateSession(CreateOptions{AgentName: "codex-coder", Backend: "codex"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	src := filepath.Join(t.TempDir(), "canonical.jsonl")
	payload := []byte(`{"timestamp":"2026-07-28T12:00:00Z","role":"system","type":"session_meta","text":"local-cli-codex session for TASK-1"}` + "\n" +
		`{"timestamp":"2026-07-28T12:00:01Z","role":"assistant","type":"text","text":"working on it"}` + "\n" +
		`{"timestamp":"2026-07-28T12:00:02Z","role":"assistant","type":"tool_use","tool_name":"bash","tool_use_id":"t1"}` + "\n" +
		`{"timestamp":"2026-07-28T12:00:03Z","role":"tool","type":"tool_result","tool_use_id":"t1","output":"done"}` + "\n" +
		`{"timestamp":"2026-07-28T12:00:04Z","role":"system","type":"result","text":"completed | in=10 out=5"}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), src, TranscriptFormatCanonical); err != nil {
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

// TestLoadNativeEvents_MarkerIsAuthoritative proves the format marker — not a
// parse-then-guess heuristic — decides the decode. Canonical-shaped bytes recorded
// with the RAW marker must route to the (codex) backend parser, which yields 0
// events; the deleted heuristic would instead have canonical-decoded them to 5.
func TestLoadNativeEvents_MarkerIsAuthoritative(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store, err := NewStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{AgentName: "codex-coder", Backend: "codex"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	src := filepath.Join(t.TempDir(), "canonical.jsonl")
	payload := []byte(`{"role":"system","type":"session_meta","text":"local-cli-codex session"}` + "\n" +
		`{"role":"assistant","type":"text","text":"hi"}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// RAW marker on canonical bytes → backend parser, not canonical decode.
	if err := store.SyncNativeTranscript(sess.SessionID(), src, TranscriptFormatRaw); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	events, err := store.LoadNativeEvents(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("raw marker must route to the codex parser (0 events for canonical bytes), got %d: %+v", len(events), events)
	}
}
