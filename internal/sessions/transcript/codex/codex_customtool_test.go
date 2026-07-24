package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// codex-cli >= 0.144 records its freeform `exec` tool as response_item payloads
// of type custom_tool_call / custom_tool_call_output (not the legacy
// function_call schema harness-wrapper understands). Events must normalize these
// into tool_use / tool_result so the transcript renders tool calls.
const customToolRollout = `{"timestamp":"2026-07-14T18:58:40.000Z","type":"response_item","payload":{"type":"custom_tool_call","id":"ctc_1","status":"completed","call_id":"call_A","name":"exec","input":"const r = await tools.exec_command({cmd:[\"bash\",\"-lc\",\"loom data show LOCALMODE-3\"]});"}}
{"timestamp":"2026-07-14T18:58:41.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_A","output":[{"type":"input_text","text":"Script completed\nWall time 0.1s\nOutput:\n"},{"type":"input_text","text":"{\"exit_code\":0}"}]}}
`

func TestEvents_CodexCustomToolCall(t *testing.T) {
	events, err := Events([]byte(customToolRollout))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events (tool_use + tool_result), got %d: %+v", len(events), events)
	}

	use := events[0]
	if use.Type != transcript.EventToolUse {
		t.Errorf("events[0].Type = %q, want %q", use.Type, transcript.EventToolUse)
	}
	if use.ToolName != "exec" {
		t.Errorf("tool_name = %q, want exec", use.ToolName)
	}
	if use.ToolUseID != "call_A" {
		t.Errorf("tool_use_id = %q, want call_A", use.ToolUseID)
	}
	// tool_input MUST be valid JSON — otherwise marshaling the transcript API
	// response fails (the original naive input->arguments copy produced invalid
	// JSON because the exec input is a JS script, not JSON).
	if !json.Valid(use.ToolInput) {
		t.Fatalf("tool_input is not valid JSON: %s", use.ToolInput)
	}
	var script string
	if err := json.Unmarshal(use.ToolInput, &script); err != nil {
		t.Fatalf("tool_input is not a JSON string: %s (%v)", use.ToolInput, err)
	}
	if !strings.Contains(script, "tools.exec_command") {
		t.Errorf("round-tripped script missing exec_command call: %q", script)
	}

	res := events[1]
	if res.Type != transcript.EventToolResult {
		t.Errorf("events[1].Type = %q, want %q", res.Type, transcript.EventToolResult)
	}
	if res.ToolUseID != "call_A" {
		t.Errorf("result tool_use_id = %q, want call_A (must pair with the call)", res.ToolUseID)
	}
	// output array of {text} blocks flattened to readable text.
	if !strings.Contains(res.Output, "Script completed") || !strings.Contains(res.Output, "exit_code") {
		t.Errorf("result output missing flattened text: %q", res.Output)
	}
}

func TestFlattenToolOutputEdgeCases(t *testing.T) {
	if got := flattenToolOutput(json.RawMessage(`[]`)); got != "" {
		t.Fatalf("empty output = %q, want empty", got)
	}
	if got := flattenToolOutput(json.RawMessage(`{"type":"input_text","text":"done"}`)); got != "done" {
		t.Fatalf("object output = %q, want done", got)
	}
	unknown := `[{"type":"output_image","url":"image.png"}]`
	if got := flattenToolOutput(json.RawMessage(unknown)); got != unknown {
		t.Fatalf("unknown structured output = %q, want raw JSON", got)
	}
}

func TestNormalizeCustomToolCallsKnowsWebSearch(t *testing.T) {
	line := []byte(`{"type":"response_item","payload":{"type":"web_search_call","id":"w1"}}`)
	_, unknown := normalizeCustomToolCalls(line)
	if len(unknown) != 0 {
		t.Fatalf("web_search_call reported as schema drift: %v", unknown)
	}
}

// Legacy function_call rollouts must be untouched by the normalization pass.
func TestEvents_CodexLegacyFunctionCallUnaffected(t *testing.T) {
	const legacy = `{"timestamp":"2026-04-06T17:29:51.064Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"echo hi\"}","call_id":"call_1"}}
{"timestamp":"2026-04-06T17:29:51.146Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"done"}}
`
	events, err := Events([]byte(legacy))
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 || events[0].Type != transcript.EventToolUse || events[1].Type != transcript.EventToolResult {
		t.Fatalf("legacy function_call path changed: %+v", events)
	}
	if events[0].ToolName != "exec_command" || events[1].Output != "done" {
		t.Errorf("legacy mapping altered: %+v", events)
	}
}
