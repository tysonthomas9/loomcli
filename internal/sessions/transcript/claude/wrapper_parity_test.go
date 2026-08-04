package claude_test

import (
	"testing"

	wrapperclaude "github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	loomclaude "github.com/tysonthomas9/loomcli/internal/sessions/transcript/claude"
)

// parityCorpus exercises every code path the two parsers share: a plain-string
// user prompt (with IDE/system tags that must be stripped), an assistant line
// with a text block + a tool_use block, a user line with a tool_result block +
// a text block, and line types that must be skipped (system, summary).
const parityCorpus = `{"type":"user","uuid":"u1","timestamp":"2026-05-14T12:00:00.100Z","message":{"role":"user","content":"hello <ide_selection>noise</ide_selection> world<system-reminder>drop me</system-reminder>"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-05-14T12:00:01.200Z","message":{"role":"assistant","content":[{"type":"text","text":"let me run that"},{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-05-14T12:00:02.300Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"text","text":"total 0\nfile.go"}]},{"type":"text","text":"thanks"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-05-14T12:00:03.400Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
{"type":"system","uuid":"s1"}
{"type":"summary","summary":"a summary line"}
`

// TestWrapperClaudeParserParity is the P2c golden-corpus gate: the wrapper's
// ported claudecode.Events must produce events whose PUBLIC fields are identical
// to loomcli's claude.Events on the same corpus. (The wrapper additionally sets
// Source/NativeID, which are json:"-" / not present on loom's Event, so they're
// outside the public DTO and not compared.) This must pass before loom switches
// its read path to the wrapper reader and deletes its own parser.
func TestWrapperClaudeParserParity(t *testing.T) {
	loomEvents, err := loomclaude.Events([]byte(parityCorpus))
	if err != nil {
		t.Fatalf("loom claude.Events: %v", err)
	}
	wrapEvents, err := wrapperclaude.Events([]byte(parityCorpus))
	if err != nil {
		t.Fatalf("wrapper claudecode.Events: %v", err)
	}

	if len(loomEvents) != len(wrapEvents) {
		t.Fatalf("event count differs: loom=%d wrapper=%d\nloom=%+v\nwrap=%+v",
			len(loomEvents), len(wrapEvents), loomEvents, wrapEvents)
	}
	if len(loomEvents) == 0 {
		t.Fatal("corpus produced zero events — fixture/parser regression")
	}

	for i := range loomEvents {
		l, w := loomEvents[i], wrapEvents[i]
		if l.Seq != w.Seq {
			t.Errorf("event %d Seq: loom=%d wrapper=%d", i, l.Seq, w.Seq)
		}
		if !l.Timestamp.Equal(w.Timestamp) {
			t.Errorf("event %d Timestamp: loom=%s wrapper=%s", i, l.Timestamp, w.Timestamp)
		}
		if l.Role != w.Role {
			t.Errorf("event %d Role: loom=%q wrapper=%q", i, l.Role, w.Role)
		}
		if l.Type != w.Type {
			t.Errorf("event %d Type: loom=%q wrapper=%q", i, l.Type, w.Type)
		}
		if l.Text != w.Text {
			t.Errorf("event %d Text: loom=%q wrapper=%q", i, l.Text, w.Text)
		}
		if l.ToolName != w.ToolName {
			t.Errorf("event %d ToolName: loom=%q wrapper=%q", i, l.ToolName, w.ToolName)
		}
		if l.ToolUseID != w.ToolUseID {
			t.Errorf("event %d ToolUseID: loom=%q wrapper=%q", i, l.ToolUseID, w.ToolUseID)
		}
		if string(l.ToolInput) != string(w.ToolInput) {
			t.Errorf("event %d ToolInput: loom=%q wrapper=%q", i, l.ToolInput, w.ToolInput)
		}
		if l.Output != w.Output {
			t.Errorf("event %d Output: loom=%q wrapper=%q", i, l.Output, w.Output)
		}
		if l.UUID != w.UUID {
			t.Errorf("event %d UUID: loom=%q wrapper=%q", i, l.UUID, w.UUID)
		}
	}
}
