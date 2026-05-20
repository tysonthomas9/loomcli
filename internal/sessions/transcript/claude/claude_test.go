package claude

import (
	"encoding/json"
	"testing"
	"time"

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

func TestSerializeTranscript_MarshalError(t *testing.T) {
	_, err := SerializeTranscript([]transcript.Line{{
		Type:    transcript.TypeUser,
		Message: json.RawMessage(`{"content":`),
	}})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestTruncateAtUUIDEmptyTarget(t *testing.T) {
	lines, err := transcript.ParseFromBytes([]byte(sampleTranscript))
	if err != nil {
		t.Fatalf("ParseFromBytes: %v", err)
	}
	got := TruncateAtUUID(lines, "")
	if len(got) != len(lines) {
		t.Fatalf("empty uuid should return all lines, got %d want %d", len(got), len(lines))
	}
}

func TestExtractModifiedFilesSkipsMalformedAndUsesNotebookPath(t *testing.T) {
	lines := []transcript.Line{
		{Type: transcript.TypeUser, Message: json.RawMessage(`{"content":"ignore"}`)},
		{Type: transcript.TypeAssistant, Message: json.RawMessage(`{"content":`)},
		{Type: transcript.TypeAssistant, Message: json.RawMessage(`{"content":[
			{"type":"text","text":"not a tool"},
			{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/read.txt"}},
			{"type":"tool_use","name":"Write","input":"not an object"},
			{"type":"tool_use","name":"NotebookEdit","input":{"notebook_path":"/tmp/book.ipynb"}},
			{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/book.ipynb"}}
		]}`)},
	}

	got := ExtractModifiedFiles(lines)
	if len(got) != 1 || got[0] != "/tmp/book.ipynb" {
		t.Fatalf("ExtractModifiedFiles() = %v, want [/tmp/book.ipynb]", got)
	}
}

func TestExtractSpawnedAgentIDsSkipsMalformedAndParsesTextBlocks(t *testing.T) {
	lines := []transcript.Line{
		{Type: transcript.TypeAssistant, Message: json.RawMessage(`{"content":[]}`)},
		{Type: transcript.TypeUser, Message: json.RawMessage(`{"content":`)},
		{Type: transcript.TypeUser, Message: json.RawMessage(`{"content":"not blocks"}`)},
		{Type: transcript.TypeUser, Message: json.RawMessage(`{"content":[{"type":"text","content":"agentId: ignored"}]}`)},
		{Type: transcript.TypeUser, Message: json.RawMessage(`{"content":[
			{"type":"tool_result","tool_use_id":"bad_content","content":{"unexpected":true}},
			{"type":"tool_result","tool_use_id":"missing_id","content":"agentId: !"},
			{"type":"tool_result","tool_use_id":"tool_task_2","content":[
				{"type":"image","text":"agentId: ignored"},
				{"type":"text","text":"started\nagentId: def456\n"}
			]}
		]}`)},
	}

	got := ExtractSpawnedAgentIDs(lines)
	if len(got) != 1 || got["def456"] != "tool_task_2" {
		t.Fatalf("ExtractSpawnedAgentIDs() = %v, want def456 -> tool_task_2", got)
	}
}

func TestEventsErrorAndBranchyLines(t *testing.T) {
	events, err := Events([]byte("{bad json\n"))
	if err != nil {
		t.Fatalf("malformed JSON lines should be skipped: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("malformed JSON events = %+v, want none", events)
	}

	data := []byte(`{"type":"user","uuid":"empty","message":{"content":"<ide_context>hidden</ide_context>"}}
{"type":"user","uuid":"bad-user","message":{"content":}}
{"type":"user","uuid":"array","timestamp":"2026-05-19T12:13:14.123456789Z","message":{"content":[{"type":"text","text":"hello from array"},{"type":"tool_result","tool_use_id":"toolu_array","content":[{"type":"text","text":"done"}]}]}}
{"type":"user","uuid":"bad-array","message":{"content":{"not":"an array"}}}
{"type":"assistant","uuid":"bad-assistant","message":{"content":}}
{"type":"assistant","uuid":"assistant","timestamp":"bad timestamp","message":{"content":[{"type":"text","text":""},{"type":"text","text":"assistant text"},{"type":"tool_use","id":"toolu_write","name":"Write","input":{"file_path":"/tmp/out.txt"}}]}}
`)
	events, err = Events(data)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	if events[0].UUID != "array" || events[0].Role != transcript.RoleUser || events[0].Text != "hello from array" {
		t.Fatalf("events[0] = %+v", events[0])
	}
	wantTS := time.Date(2026, 5, 19, 12, 13, 14, 123456789, time.UTC)
	if !events[0].Timestamp.Equal(wantTS) {
		t.Fatalf("timestamp = %s, want %s", events[0].Timestamp, wantTS)
	}
	if events[1].Role != transcript.RoleTool || events[1].Output != "done\n" || events[1].ToolUseID != "toolu_array" {
		t.Fatalf("events[1] = %+v", events[1])
	}
	if events[2].Role != transcript.RoleAssistant || events[2].Text != "assistant text" {
		t.Fatalf("events[2] = %+v", events[2])
	}
	if events[3].Type != transcript.EventToolUse || events[3].ToolUseID != "toolu_write" {
		t.Fatalf("events[3] = %+v", events[3])
	}
}
