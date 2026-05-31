package tsfirst

import (
	"context"
	"strings"
	"testing"
)

func TestLocalBackendResponseFromOutputParsesCodexJSONL(t *testing.T) {
	toolOnly := strings.Join([]string{
		"Launching Codex agent (non-interactive)...",
		`{"type":"thread.started","thread_id":"thread-123"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"type\":\"loom.typed_tool.call\",\"call_id\":\"create-triage-channel\",\"name\":\"create_channel\",\"arguments\":{\"name\":\"triage\"}}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":14067,"cached_input_tokens":11136,"output_tokens":218,"reasoning_output_tokens":182}}`,
		"",
	}, "\n")
	if got := localBackendResponseFromOutput(toolOnly); got != "" {
		t.Fatalf("tool-only response = %q, want empty response so typed-tool follow-up can run", got)
	}

	answer := strings.Join([]string{
		"Launching Codex agent (non-interactive)...",
		`{"type":"thread.started","thread_id":"thread-123"}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"LIVE_TYPED_TOOL_DONE: created triage"}}`,
		`{"type":"turn.completed"}`,
		"",
	}, "\n")
	if got, want := localBackendResponseFromOutput(answer), "LIVE_TYPED_TOOL_DONE: created triage"; got != want {
		t.Fatalf("answer response = %q, want %q", got, want)
	}

	if got, want := localBackendResponseFromOutput("plain provider answer\n"), "plain provider answer"; got != want {
		t.Fatalf("plain response = %q, want %q", got, want)
	}
}

func TestLocalBackendResponseFromOutputIgnoresNonAgentCodexItems(t *testing.T) {
	output := strings.Join([]string{
		"Launching Codex agent (non-interactive)...",
		`{"type":"item.completed","item":{"id":"item_0","type":"tool_call","text":"internal tool payload"}}`,
		"",
	}, "\n")
	if got := localBackendResponseFromOutput(output); got != "" {
		t.Fatalf("non-agent item response = %q, want empty response", got)
	}
}

func TestCaptureStreamingResponseIgnoresFallbackWhenStructuredEventsArePresent(t *testing.T) {
	input := strings.Join([]string{
		"Launching Codex agent (non-interactive)...",
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"type\":\"loom.typed_tool.call\",\"call_id\":\"create-triage-channel\",\"name\":\"create_channel\",\"arguments\":{\"name\":\"triage\"}}"}}`,
		"",
	}, "\n")
	result, err := captureStreamingResponse(context.Background(), strings.NewReader(input), nil, nil)
	if err != nil {
		t.Fatalf("captureStreamingResponse() error = %v", err)
	}
	if result.Response != "" {
		t.Fatalf("streaming response = %q, want empty response without provider preamble fallback", result.Response)
	}
}
