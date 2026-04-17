package opencode

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

const sampleExport = `{
  "info": {"id":"sess-1","createdAt":1700000000000},
  "messages": [
    {
      "info": {"id":"m1","role":"user","time":{"created":1700000000000}},
      "parts": [{"type":"text","text":"write hello"}]
    },
    {
      "info": {"id":"m2","role":"assistant","time":{"created":1700000001000},"tokens":{"input":10,"output":5,"cache":{"read":0,"write":0}}},
      "parts": [
        {"type":"text","text":"I'll do it."},
        {"type":"tool","tool":"write","callID":"c1","state":{"status":"completed","input":{"filePath":"/tmp/hello.txt","content":"hello"},"output":"wrote 5 bytes"}}
      ]
    }
  ]
}`

func TestEvents_OpenCodeFlow(t *testing.T) {
	events, err := Events([]byte(sampleExport))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d", len(events))
	}

	if events[0].Role != transcript.RoleUser || events[0].Text != "write hello" {
		t.Errorf("events[0]: got %+v", events[0])
	}
	if events[1].Role != transcript.RoleAssistant || events[1].Text != "I'll do it." {
		t.Errorf("events[1]: got %+v", events[1])
	}
	if events[2].Type != transcript.EventToolUse || events[2].ToolName != "write" || events[2].ToolUseID != "c1" {
		t.Errorf("events[2]: got %+v", events[2])
	}
	if events[3].Type != transcript.EventToolResult || events[3].Output != "wrote 5 bytes" {
		t.Errorf("events[3]: got %+v", events[3])
	}
}

func TestExtractModifiedFiles(t *testing.T) {
	files, err := ExtractModifiedFiles([]byte(sampleExport))
	if err != nil {
		t.Fatalf("ExtractModifiedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "/tmp/hello.txt" {
		t.Errorf("got %v, want [/tmp/hello.txt]", files)
	}
}

func TestParseExportSession_Empty(t *testing.T) {
	session, err := ParseExportSession(nil)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if session != nil {
		t.Errorf("want nil session for empty data")
	}
}
