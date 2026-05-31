package backends

import (
	"context"
	"testing"
)

func TestProviderTypedToolBridgeExecutesClaudeStreamToolUse(t *testing.T) {
	backend := &ClaudeBackend{}
	executor := &recordingTypedToolExecutor{}
	if err := backend.SetTypedTools([]TypedToolDefinition{{Name: "create_channel"}}); err != nil {
		t.Fatalf("SetTypedTools() error = %v", err)
	}
	if err := backend.SetTypedToolExecutor(executor); err != nil {
		t.Fatalf("SetTypedToolExecutor() error = %v", err)
	}
	backend.BeginTypedToolInvocation()

	backend.IngestTypedToolProviderLine(context.Background(), `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-create","name":"create_channel","input":{"name":"triage"}}]}}`)

	if executor.request.Name != "create_channel" ||
		executor.request.CallID != "call-create" ||
		executor.request.Arguments["name"] != "triage" {
		t.Fatalf("executor request = %+v, want provider tool call request", executor.request)
	}
	calls := backend.TypedToolCalls("")
	if len(calls) != 1 ||
		calls[0].Name != "create_channel" ||
		calls[0].CallID != "call-create" ||
		calls[0].Status != "completed" ||
		calls[0].AuthorizationStatus != "authorized" ||
		calls[0].Result != "created triage" {
		t.Fatalf("typed tool calls = %+v, want trusted execution evidence", calls)
	}
}

func TestProviderTypedToolBridgeExtractsProviderNativeToolCallShapes(t *testing.T) {
	cases := []struct {
		name    string
		backend interface {
			SetTypedTools([]TypedToolDefinition) error
			SetTypedToolExecutor(TypedToolExecutor) error
			BeginTypedToolInvocation()
			IngestTypedToolProviderLine(context.Context, string)
			TypedToolCalls(string) []TypedToolCallEvent
		}
		line string
	}{
		{
			name:    "codex responses function call",
			backend: &CodexBackend{},
			line:    `{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-create","name":"create_channel","arguments":"{\"name\":\"triage\"}"}}`,
		},
		{
			name:    "gemini function call",
			backend: &GeminiBackend{},
			line:    `{"type":"content","candidates":[{"content":{"parts":[{"functionCall":{"name":"create_channel","args":{"name":"triage"}}}]}}]}`,
		},
		{
			name:    "opencode explicit loom typed tool call",
			backend: &OpenCodeBackend{},
			line:    `{"type":"loom.typed_tool.call","call_id":"call-create","name":"create_channel","arguments":{"name":"triage"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recordingTypedToolExecutor{}
			if err := tc.backend.SetTypedTools([]TypedToolDefinition{{Name: "create_channel"}}); err != nil {
				t.Fatalf("SetTypedTools() error = %v", err)
			}
			if err := tc.backend.SetTypedToolExecutor(executor); err != nil {
				t.Fatalf("SetTypedToolExecutor() error = %v", err)
			}
			tc.backend.BeginTypedToolInvocation()

			tc.backend.IngestTypedToolProviderLine(context.Background(), tc.line)

			if executor.request.Name != "create_channel" || executor.request.Arguments["name"] != "triage" {
				t.Fatalf("executor request = %+v, want provider-native typed tool call", executor.request)
			}
			calls := tc.backend.TypedToolCalls("")
			if len(calls) != 1 || calls[0].Name != "create_channel" || calls[0].Status != "completed" {
				t.Fatalf("typed tool calls = %+v, want one completed trusted tool call", calls)
			}
		})
	}
}

func TestProviderTypedToolBridgeExtractsExplicitCallsFromTextBlocks(t *testing.T) {
	backend := &ClaudeBackend{}
	executor := &recordingTypedToolExecutor{}
	if err := backend.SetTypedTools([]TypedToolDefinition{{Name: "create_channel"}}); err != nil {
		t.Fatalf("SetTypedTools() error = %v", err)
	}
	if err := backend.SetTypedToolExecutor(executor); err != nil {
		t.Fatalf("SetTypedToolExecutor() error = %v", err)
	}
	backend.BeginTypedToolInvocation()

	backend.IngestTypedToolProviderLine(context.Background(), `{"type":"assistant","message":{"content":[{"type":"text","text":"{\"type\":\"loom.typed_tool.call\",\"call_id\":\"call-create\",\"name\":\"create_channel\",\"arguments\":{\"name\":\"triage\"}}"}]}}`)

	if executor.request.Name != "create_channel" ||
		executor.request.CallID != "call-create" ||
		executor.request.Arguments["name"] != "triage" {
		t.Fatalf("executor request = %+v, want explicit typed tool call extracted from provider text block", executor.request)
	}
	calls := backend.TypedToolCalls("")
	if len(calls) != 1 ||
		calls[0].Name != "create_channel" ||
		calls[0].CallID != "call-create" ||
		calls[0].Status != "completed" ||
		calls[0].Result != "created triage" {
		t.Fatalf("typed tool calls = %+v, want trusted execution evidence from text block", calls)
	}
}

func TestProviderTypedToolBridgeExtractsPrefixedCodexJSONL(t *testing.T) {
	backend := &CodexBackend{}
	executor := &recordingTypedToolExecutor{}
	if err := backend.SetTypedTools([]TypedToolDefinition{{Name: "create_channel"}}); err != nil {
		t.Fatalf("SetTypedTools() error = %v", err)
	}
	if err := backend.SetTypedToolExecutor(executor); err != nil {
		t.Fatalf("SetTypedToolExecutor() error = %v", err)
	}
	backend.BeginTypedToolInvocation()

	backend.IngestTypedToolProviderLine(context.Background(), "\x04\b\b"+`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"type\":\"loom.typed_tool.call\",\"call_id\":\"call-create\",\"name\":\"create_channel\",\"arguments\":{\"name\":\"triage\"}}"}}`)

	if executor.request.Name != "create_channel" ||
		executor.request.CallID != "call-create" ||
		executor.request.Arguments["name"] != "triage" {
		t.Fatalf("executor request = %+v, want explicit typed tool call extracted from prefixed Codex JSONL", executor.request)
	}
	calls := backend.TypedToolCalls("")
	if len(calls) != 1 ||
		calls[0].Name != "create_channel" ||
		calls[0].CallID != "call-create" ||
		calls[0].Status != "completed" {
		t.Fatalf("typed tool calls = %+v, want trusted execution evidence from prefixed Codex JSONL", calls)
	}
}

func TestProviderTypedToolBridgeRecordsExplicitUndeclaredCallsAsDenied(t *testing.T) {
	backend := &CodexBackend{}
	if err := backend.SetTypedTools([]TypedToolDefinition{{Name: "create_channel"}}); err != nil {
		t.Fatalf("SetTypedTools() error = %v", err)
	}
	if err := backend.SetTypedToolExecutor(&recordingTypedToolExecutor{}); err != nil {
		t.Fatalf("SetTypedToolExecutor() error = %v", err)
	}
	backend.BeginTypedToolInvocation()

	backend.IngestTypedToolProviderLine(context.Background(), `{"type":"loom.typed_tool.call","call_id":"call-delete","name":"delete_workspace","arguments":{"id":"prod"}}`)

	calls := backend.TypedToolCalls("")
	if len(calls) != 1 ||
		calls[0].Name != "delete_workspace" ||
		calls[0].Status != "failed" ||
		calls[0].AuthorizationStatus != "denied" ||
		!calls[0].Redacted {
		t.Fatalf("typed tool calls = %+v, want denied explicit undeclared call", calls)
	}
}

type recordingTypedToolExecutor struct {
	request TypedToolExecutionRequest
}

func (e *recordingTypedToolExecutor) ExecuteTypedTool(_ context.Context, request TypedToolExecutionRequest) (TypedToolCallEvent, error) {
	e.request = request
	return TypedToolCallEvent{
		CallID:              request.CallID,
		Name:                request.Name,
		Status:              "completed",
		Arguments:           request.Arguments,
		Result:              "created " + stringValue(request.Arguments["name"]),
		AuthorizationStatus: "authorized",
	}, nil
}

func stringValue(value any) string {
	if out, ok := value.(string); ok {
		return out
	}
	return ""
}
