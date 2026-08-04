package claude

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

const sampleTranscript = `{"type":"user","uuid":"u1","message":{"content":"write hello.txt"}}
{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"I'll create that file."},{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/hello.txt","content":"hello"}}]}}
{"type":"user","uuid":"u2","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"File created"}]}}
`

func TestEvents_ParsesUserAssistantToolFlow(t *testing.T) {
	events, err := Events([]byte(sampleTranscript))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d", len(events))
	}

	if events[0].Role != transcript.RoleUser || events[0].Type != transcript.EventText {
		t.Errorf("events[0]: got %+v", events[0])
	}
	if events[1].Role != transcript.RoleAssistant || events[1].Type != transcript.EventText {
		t.Errorf("events[1]: got %+v", events[1])
	}
	if events[2].Role != transcript.RoleAssistant || events[2].Type != transcript.EventToolUse || events[2].ToolName != "Write" {
		t.Errorf("events[2]: got %+v", events[2])
	}
	if events[3].Role != transcript.RoleTool || events[3].Type != transcript.EventToolResult {
		t.Errorf("events[3]: got %+v", events[3])
	}
}

func TestExtractModifiedFiles(t *testing.T) {
	lines, err := transcript.ParseFromBytes([]byte(sampleTranscript))
	if err != nil {
		t.Fatalf("ParseFromBytes: %v", err)
	}
	files := ExtractModifiedFiles(lines)
	if len(files) != 1 || files[0] != "/tmp/hello.txt" {
		t.Errorf("got %v, want [/tmp/hello.txt]", files)
	}
}

func TestTruncateAtUUID(t *testing.T) {
	lines, err := transcript.ParseFromBytes([]byte(sampleTranscript))
	if err != nil {
		t.Fatalf("ParseFromBytes: %v", err)
	}
	got := TruncateAtUUID(lines, "a1")
	if len(got) != 2 {
		t.Errorf("want 2 lines up to a1, got %d", len(got))
	}
	got = TruncateAtUUID(lines, "nonexistent")
	if len(got) != 3 {
		t.Errorf("unknown uuid should return all, got %d", len(got))
	}
}

func TestExtractSpawnedAgentIDs(t *testing.T) {
	input := `{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"tool_task_1","content":"Task done.\nagentId: abc123xyz (use SendMessage)"}]}}` + "\n"
	lines, err := transcript.ParseFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ids := ExtractSpawnedAgentIDs(lines)
	if ids["abc123xyz"] != "tool_task_1" {
		t.Errorf("want abc123xyz -> tool_task_1, got %v", ids)
	}
}

func TestSerializeTranscript_Roundtrip(t *testing.T) {
	lines, err := transcript.ParseFromBytes([]byte(sampleTranscript))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := SerializeTranscript(lines)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	roundtrip, err := transcript.ParseFromBytes(data)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(roundtrip) != len(lines) {
		t.Errorf("roundtrip: %d lines, want %d", len(roundtrip), len(lines))
	}
}
