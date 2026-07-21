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
	// session_meta, empty reasoning, event_msg are all dropped.
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

func TestEvents_ModernCodexStream(t *testing.T) {
	input := `{"type":"thread.started","thread_id":"thread-1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"message-1","type":"agent_message","text":"hello"}}
{"type":"item.completed","item":{"id":"reasoning-1","type":"reasoning","text":"checking"}}
{"type":"item.completed","item":{"id":"command-1","type":"command_execution","command":"pwd","aggregated_output":"/repo","exit_code":0}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}
`
	events, err := Events([]byte(input))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want four meaningful events, got %d: %+v", len(events), events)
	}
	assertDenseSeq(t, events)
	if events[0].Role != transcript.RoleAssistant || events[0].Type != transcript.EventText || events[0].Text != "hello" {
		t.Fatalf("agent message = %+v", events[0])
	}
	if events[1].Role != transcript.RoleAssistant || events[1].Type != transcript.EventReasoning || events[1].Text != "checking" {
		t.Fatalf("reasoning = %+v", events[1])
	}
	if events[2].Role != transcript.RoleAssistant || events[2].Type != transcript.EventToolUse || events[2].ToolName != "command_execution" || events[2].ToolUseID != "command-1" {
		t.Fatalf("command use = %+v", events[2])
	}
	if events[3].Type != transcript.EventToolResult || events[3].ToolUseID != "command-1" || events[3].Output != "/repo" {
		t.Fatalf("command result = %+v", events[3])
	}
}

// TestEvents_SkipsMalformedLine confirms a malformed (non-JSON) rollout line is
// skipped (not fatal) — the wrapper's ParseRollout tolerance, exercised through
// the delegating Events. A valid message after it still surfaces.
func TestEvents_SkipsMalformedLine(t *testing.T) {
	input := `not json
{"timestamp":"2026-04-06T17:29:41.321Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}
`
	events, err := Events([]byte(input))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hi" {
		t.Fatalf("want 1 event 'hi' (malformed line skipped), got %+v", events)
	}
}

func TestEvents_SplicesReasoningSummaryInRolloutOrder(t *testing.T) {
	input := `{"timestamp":"2026-04-06T17:29:41.321Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"start"}]}}
{"timestamp":"2026-04-06T17:29:42.000Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"checking approach"},{"type":"summary_text","text":"choosing command"}]}}
{"timestamp":"2026-04-06T17:29:43.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"}}
{"timestamp":"2026-04-06T17:29:44.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"done"}}
{"timestamp":"2026-04-06T17:29:45.000Z","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"..."}}
`

	events, err := Events([]byte(input))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d: %+v", len(events), events)
	}
	assertDenseSeq(t, events)

	wantTypes := []string{
		transcript.EventText,
		transcript.EventReasoning,
		transcript.EventToolUse,
		transcript.EventToolResult,
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type: want %q, got %+v", i, want, events[i])
		}
	}

	reasoning := events[1]
	if reasoning.Role != transcript.RoleAssistant {
		t.Fatalf("reasoning role: want assistant, got %+v", reasoning)
	}
	if reasoning.Text != "checking approach\n\nchoosing command" {
		t.Fatalf("reasoning text: got %q", reasoning.Text)
	}
	wantTS := mustParseTime(t, "2026-04-06T17:29:42.000Z")
	if !reasoning.Timestamp.Equal(wantTS) {
		t.Fatalf("reasoning timestamp: want %s, got %s", wantTS, reasoning.Timestamp)
	}
	if events[2].ToolUseID != "call_1" || events[3].ToolUseID != "call_1" {
		t.Fatalf("tool events not preserved in order: %+v", events)
	}
}

func TestEvents_SplicesReasoningTextForms(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "plain string summary",
			payload: `{"type":"reasoning","summary":"thinking"}`,
			want:    "thinking",
		},
		{
			name:    "reasoning text content",
			payload: `{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"content thought"}]}`,
			want:    "content thought",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"timestamp":"2026-04-06T17:29:42.000Z","type":"response_item","payload":` + tt.payload + `}
`
			events, err := Events([]byte(input))
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("want 1 reasoning event, got %d: %+v", len(events), events)
			}
			assertDenseSeq(t, events)
			if events[0].Role != transcript.RoleAssistant || events[0].Type != transcript.EventReasoning || events[0].Text != tt.want {
				t.Fatalf("reasoning event mismatch: %+v", events[0])
			}
		})
	}
}

func assertDenseSeq(t *testing.T, events []transcript.Event) {
	t.Helper()
	for i, event := range events {
		if event.Seq != i {
			t.Fatalf("events[%d].Seq: want %d, got %d", i, i, event.Seq)
		}
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return ts
}
