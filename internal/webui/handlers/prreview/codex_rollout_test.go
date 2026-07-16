package prreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

func TestReviewerMessagesFromCodexRolloutIncludesTools(t *testing.T) {
	// Codex ≥0.144 records tools as custom_tool_call*; the rollout parser
	// normalizes those into tool_use / tool_result for the chat pills.
	data := []byte(strings.Join([]string{
		`{"timestamp":"2026-07-14T18:58:40.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"## READ-ONLY PR REVIEWER\nreview"}]}}`,
		`{"timestamp":"2026-07-14T18:58:40.100Z","type":"response_item","payload":{"type":"custom_tool_call","id":"ctc_1","status":"completed","call_id":"call_A","name":"exec","input":"const r = await tools.exec_command({cmd:[\"bash\",\"-lc\",\"git diff\"]});"}}`,
		`{"timestamp":"2026-07-14T18:58:41.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_A","output":[{"type":"input_text","text":"diff --git a/x.go"}]}}`,
		`{"timestamp":"2026-07-14T18:58:42.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I reviewed the diff."}]}}`,
	}, "\n") + "\n")

	msgs, err := reviewerMessagesFromCodexRollout("thread-1", data)
	if err != nil {
		t.Fatalf("reviewerMessagesFromCodexRollout: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v, want tool + assistant (prompt trimmed)", msgs)
	}
	if msgs[0].Kind != "tool_use" || msgs[0].ToolName != "exec" {
		t.Fatalf("tool message = %+v", msgs[0])
	}
	if !strings.Contains(msgs[0].ToolInput, "git diff") {
		t.Fatalf("tool_input = %q", msgs[0].ToolInput)
	}
	if !strings.Contains(msgs[0].ToolResult, "diff --git") {
		t.Fatalf("tool_result = %q", msgs[0].ToolResult)
	}
	if msgs[1].Text != "I reviewed the diff." || msgs[1].Kind != "" {
		t.Fatalf("assistant = %+v", msgs[1])
	}
}

func TestFindCodexRolloutPathByGlob(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	threadID := "019f636d-627a-7512-969d-51fc86038604"
	dir := filepath.Join(home, "sessions", "2026", "07", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-07-15T01-39-00-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findCodexRolloutPath(leadcontrol.CodexRuntimeMetadata{ThreadID: threadID})
	if err != nil {
		t.Fatalf("findCodexRolloutPath: %v", err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
}

func TestFindCodexRolloutPathRequiresExactThreadIDSuffix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	threadID := "thread-123"
	dir := filepath.Join(home, "sessions", "2026", "07", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	nearMatch := filepath.Join(dir, "rollout-2026-07-15T01-39-00-prefix-"+threadID+"-suffix.jsonl")
	if err := os.WriteFile(nearMatch, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findCodexRolloutPath(leadcontrol.CodexRuntimeMetadata{ThreadID: threadID}); err == nil {
		t.Fatal("near-match rollout should not be selected")
	}
}
