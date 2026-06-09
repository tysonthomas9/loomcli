package codex_test

import (
	"testing"

	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	loomcodex "github.com/tysonthomas9/loomcli/internal/sessions/transcript/codex"
)

// parityCorpus exercises the shared code paths: a user message (with an IDE tag
// to strip), an assistant message, a function_call, a function_call_output, and
// non-surfaced lines (session_meta, reasoning, event_msg).
const parityCorpus = `{"timestamp":"2026-04-06T17:29:41.320Z","type":"session_meta","payload":{"id":"sess-1"}}
{"timestamp":"2026-04-06T17:29:41.321Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"write a file <ide_selection>x</ide_selection>"}]}}
{"timestamp":"2026-04-06T17:29:44.039Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}
{"timestamp":"2026-04-06T17:29:51.062Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"timestamp":"2026-04-06T17:29:51.064Z","type":"response_item","payload":{"type":"function_call","name":"exec","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"}}
{"timestamp":"2026-04-06T17:29:51.146Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"done"}}
{"timestamp":"2026-04-06T17:30:15.686Z","type":"event_msg","payload":{"type":"token_count"}}
`

// TestWrapperCodexParserParity is the P2c gate for Codex: loomcli's delegating
// codex.Events must produce events whose PUBLIC fields are identical to
// harness-wrapper's codex.Events on the same corpus (the wrapper additionally
// sets Source/NativeID, which are not part of loom's Event, so they're not
// compared). Locks the delegation mapping against field drift.
func TestWrapperCodexParserParity(t *testing.T) {
	loomEvents, err := loomcodex.Events([]byte(parityCorpus))
	if err != nil {
		t.Fatalf("loom codex.Events: %v", err)
	}
	wrapEvents, err := hwcodex.Events([]byte(parityCorpus))
	if err != nil {
		t.Fatalf("wrapper codex.Events: %v", err)
	}
	if len(loomEvents) != len(wrapEvents) {
		t.Fatalf("event count differs: loom=%d wrapper=%d", len(loomEvents), len(wrapEvents))
	}
	if len(loomEvents) == 0 {
		t.Fatal("corpus produced zero events — fixture/parser regression")
	}
	for i := range loomEvents {
		l, w := loomEvents[i], wrapEvents[i]
		if l.Seq != w.Seq || !l.Timestamp.Equal(w.Timestamp) || l.Role != w.Role || l.Type != w.Type ||
			l.Text != w.Text || l.ToolName != w.ToolName || l.ToolUseID != w.ToolUseID ||
			string(l.ToolInput) != string(w.ToolInput) || l.Output != w.Output || l.UUID != w.UUID {
			t.Errorf("event %d differs:\n loom=%+v\n wrap=%+v", i, l, w)
		}
	}
}
