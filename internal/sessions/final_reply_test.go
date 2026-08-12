package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

func assistantText(text string) transcript.Event {
	return transcript.Event{Role: transcript.RoleAssistant, Type: transcript.EventText, Text: text}
}

func TestTerminalAssistantText(t *testing.T) {
	tests := []struct {
		name   string
		events []transcript.Event
		want   string
	}{
		{
			name:   "empty transcript",
			events: nil,
			want:   "",
		},
		{
			// The canonical TS-leaf stream leaves every UUID empty, so the
			// boundary must be structural. Intermediate narration before the
			// tool cycle must not be merged into the final reply.
			name: "canonical TS-leaf with empty UUIDs and a tool cycle",
			events: []transcript.Event{
				{Role: transcript.RoleSystem, Type: transcript.EventSessionMeta},
				{Role: transcript.RoleUser, Type: transcript.EventText, Text: "review the plan"},
				assistantText("let me read the design"),
				{Role: transcript.RoleAssistant, Type: transcript.EventToolUse, ToolName: "Read"},
				{Role: transcript.RoleTool, Type: transcript.EventToolResult, Output: "design text"},
				assistantText("PLAN CRITIQUE:"),
				assistantText("The ordering invariant is unenforced."),
				{Role: transcript.RoleSystem, Type: transcript.EventResult, Output: `{"total_cost_usd":0.5}`},
			},
			want: "PLAN CRITIQUE:\n\nThe ordering invariant is unenforced.",
		},
		{
			name: "raw claude stream with no trailing result record",
			events: []transcript.Event{
				{Role: transcript.RoleUser, Type: transcript.EventText, Text: "hi"},
				assistantText("hello"),
			},
			want: "hello",
		},
		{
			// Ended on a tool call: there is no final prose to publish.
			name: "no assistant text after the last tool cycle",
			events: []transcript.Event{
				assistantText("working on it"),
				{Role: transcript.RoleAssistant, Type: transcript.EventToolUse, ToolName: "Bash"},
				{Role: transcript.RoleTool, Type: transcript.EventToolResult, Output: "ok"},
				{Role: transcript.RoleSystem, Type: transcript.EventResult},
			},
			want: "",
		},
		{
			name: "reasoning before the reply is not included",
			events: []transcript.Event{
				{Role: transcript.RoleAssistant, Type: transcript.EventReasoning, Text: "thinking hard"},
				assistantText("the answer"),
			},
			want: "the answer",
		},
		{
			name:   "whitespace-only assistant text is not substantive",
			events: []transcript.Event{assistantText("   \n  ")},
			want:   "",
		},
		{
			name: "a user turn bounds the terminal segment",
			events: []transcript.Event{
				assistantText("first answer"),
				{Role: transcript.RoleUser, Type: transcript.EventText, Text: "again"},
				assistantText("second answer"),
			},
			want: "second answer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalAssistantText(tt.events); got != tt.want {
				t.Errorf("terminalAssistantText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFinalAssistantReply_EndToEnd drives the Store method over a real
// canonical TS-leaf transcript: every event carries an empty UUID, so the
// terminal-segment boundary — not message identity — must isolate the reply.
func TestFinalAssistantReply_EndToEnd(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{AgentName: "critic", Backend: "codex"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	lines := []string{
		`{"timestamp":"2026-07-28T12:00:00Z","seq":0,"role":"system","type":"session_meta","text":"start"}`,
		`{"timestamp":"2026-07-28T12:00:01Z","seq":1,"role":"user","type":"text","text":"review the plan"}`,
		`{"timestamp":"2026-07-28T12:00:02Z","seq":2,"role":"assistant","type":"text","text":"let me read it"}`,
		`{"timestamp":"2026-07-28T12:00:03Z","seq":3,"role":"assistant","type":"tool_use","tool_name":"Read"}`,
		`{"timestamp":"2026-07-28T12:00:04Z","seq":4,"role":"tool","type":"tool_result","output":"the design"}`,
		`{"timestamp":"2026-07-28T12:00:05Z","seq":5,"role":"assistant","type":"text","text":"PLAN CRITIQUE:"}`,
		`{"timestamp":"2026-07-28T12:00:06Z","seq":6,"role":"assistant","type":"text","text":"The invariant is unenforced."}`,
		`{"timestamp":"2026-07-28T12:00:07Z","seq":7,"role":"system","type":"result","output":"{\"cost_usd\":1}"}`,
	}
	src := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(src, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), src, TranscriptFormatCanonical); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	got, err := store.FinalAssistantReply(sess.SessionID())
	if err != nil {
		t.Fatalf("FinalAssistantReply: %v", err)
	}
	want := "PLAN CRITIQUE:\n\nThe invariant is unenforced."
	if got != want {
		t.Errorf("FinalAssistantReply() = %q, want %q", got, want)
	}
}

// A live session whose transcript has not been flushed yet is not an error:
// the caller polls within a bounded window and then decides whether the
// absence is fatal. An unknown session id, by contrast, is a real error.
func TestFinalAssistantReply_TranscriptNotFlushedYet(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{AgentName: "critic", Backend: "codex"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.FinalAssistantReply(sess.SessionID())
	if err != nil {
		t.Fatalf("FinalAssistantReply: %v", err)
	}
	if got != "" {
		t.Errorf("FinalAssistantReply() = %q, want empty before the flush", got)
	}
}

func TestFinalAssistantReply_UnknownSession(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.FinalAssistantReply("no-such-session"); err == nil {
		t.Fatal("expected an error for an unknown session id")
	}
}
