package codex

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

const sampleRollout = `{"timestamp":"2026-04-06T17:29:41.320Z","type":"session_meta","payload":{"id":"sess-1"}}
{"timestamp":"2026-04-06T17:29:41.321Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"write a file"}]}}
{"timestamp":"2026-04-06T17:29:44.039Z","type":"response_item","payload":{"type":"reasoning","summary":[],"content":null,"encrypted_content":"..."}}
{"timestamp":"2026-04-06T17:29:51.062Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I'll write it."}]}}
{"timestamp":"2026-04-06T17:29:51.064Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"echo hi > /tmp/hi.txt\"}","call_id":"call_1"}}
{"timestamp":"2026-04-06T17:29:51.146Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Process exited with code 0\nOutput:\n"}}
{"timestamp":"2026-04-06T17:30:15.686Z","type":"event_msg","payload":{"type":"token_count"}}
`

func TestEvents_CodexFlow(t *testing.T) {
	events, err := Events([]byte(sampleRollout))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	// 4 meaningful events: user msg, assistant msg, function_call, function_call_output.
	// session_meta, reasoning, event_msg are all dropped.
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d: %+v", len(events), events)
	}

	if events[0].Role != transcript.RoleUser || events[0].Text != "write a file" {
		t.Errorf("events[0]: got %+v", events[0])
	}
	if events[1].Role != transcript.RoleAssistant || events[1].Text != "I'll write it." {
		t.Errorf("events[1]: got %+v", events[1])
	}
	if events[2].Type != transcript.EventToolUse || events[2].ToolName != "exec_command" || events[2].ToolUseID != "call_1" {
		t.Errorf("events[2]: got %+v", events[2])
	}
	if events[3].Type != transcript.EventToolResult || events[3].ToolUseID != "call_1" {
		t.Errorf("events[3]: got %+v", events[3])
	}
}

func TestParseRollout_SkipsMalformed(t *testing.T) {
	input := `not json
{"timestamp":"2026-04-06T17:29:41.320Z","type":"session_meta","payload":{}}
`
	envelopes, err := ParseRollout([]byte(input))
	if err != nil {
		t.Fatalf("ParseRollout: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envelopes))
	}
}

func TestEvents_CodexBranchyPayloads(t *testing.T) {
	input := `{"timestamp":"bad-time","type":"response_item","payload":}
{"timestamp":"2026-04-06T17:29:41.320Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<ide_context>hidden</ide_context>"}]}}
{"timestamp":"2026-04-06T17:29:42.320Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev note"}]}}
{"timestamp":"2026-04-06T17:29:43.320Z","type":"response_item","payload":{"type":"message","role":"system","content":[{"type":"input_text","text":"system note"}]}}
{"timestamp":"bad-time","type":"response_item","payload":{"type":"message","role":"unknown","content":[{"type":"input_text","text":""},{"type":"input_text","text":"fallback role"}]}}
{"timestamp":"2026-04-06T17:29:44.320Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_obj","output":{"ok":true}}}
`
	events, err := Events([]byte(input))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	if events[0].Role != transcript.RoleSystem || events[0].Text != "dev note" {
		t.Fatalf("developer event = %+v", events[0])
	}
	wantTS := time.Date(2026, 4, 6, 17, 29, 42, 320000000, time.UTC)
	if !events[0].Timestamp.Equal(wantTS) {
		t.Fatalf("timestamp = %s, want %s", events[0].Timestamp, wantTS)
	}
	if events[1].Role != transcript.RoleSystem || events[1].Text != "system note" {
		t.Fatalf("system event = %+v", events[1])
	}
	if events[2].Role != transcript.RoleSystem || events[2].Text != "fallback role" || !events[2].Timestamp.IsZero() {
		t.Fatalf("fallback role event = %+v", events[2])
	}
	if events[3].Role != transcript.RoleTool || events[3].Output != `{"ok":true}` {
		t.Fatalf("object output event = %+v", events[3])
	}
}
