package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDisplayStreamEvent_TextBlock(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// JSON for assistant message with text content
	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from Claude"}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "Hello from Claude" {
		t.Errorf("expected 'Hello from Claude', got %q", output)
	}
}

func TestDisplayStreamEvent_ToolUse_Bash(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "[Tool: Bash]") {
		t.Errorf("expected '[Tool: Bash]' in output, got %q", output)
	}
	if !strings.Contains(output, "git status") {
		t.Errorf("expected 'git status' in output, got %q", output)
	}
}

func TestDisplayStreamEvent_ToolUse_Read(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/path/to/file.go"}}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "[Tool: Read]") {
		t.Errorf("expected '[Tool: Read]' in output, got %q", output)
	}
	if !strings.Contains(output, "/path/to/file.go") {
		t.Errorf("expected '/path/to/file.go' in output, got %q", output)
	}
}

func TestDisplayStreamEvent_ToolUse_Write(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/path/to/output.txt"}}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "[Tool: Write]") {
		t.Errorf("expected '[Tool: Write]' in output, got %q", output)
	}
	if !strings.Contains(output, "/path/to/output.txt") {
		t.Errorf("expected '/path/to/output.txt' in output, got %q", output)
	}
}

func TestDisplayStreamEvent_ToolUse_Edit(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/path/to/edit.go"}}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "[Tool: Edit]") {
		t.Errorf("expected '[Tool: Edit]' in output, got %q", output)
	}
	if !strings.Contains(output, "/path/to/edit.go") {
		t.Errorf("expected '/path/to/edit.go' in output, got %q", output)
	}
}

func TestDisplayStreamEvent_Result(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"result"}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Result type should print a newline
	if output != "\n" {
		t.Errorf("expected newline for result event, got %q", output)
	}
}

func TestDisplayStreamEvent_InvalidJSON(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Invalid JSON should be silently ignored (no output)
	displayStreamEvent("not valid json {}")

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "" {
		t.Errorf("expected no output for invalid JSON, got %q", output)
	}
}

func TestDisplayStreamEvent_UnknownType(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Unknown event type should be silently ignored
	jsonEvent := `{"type":"unknown_event_type"}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "" {
		t.Errorf("expected no output for unknown event type, got %q", output)
	}
}

func TestDisplayStreamEvent_AssistantNoMessage(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Assistant event without message should be silently handled
	jsonEvent := `{"type":"assistant"}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "" {
		t.Errorf("expected no output for assistant without message, got %q", output)
	}
}

func TestDisplayStreamEvent_ToolUse_OtherTool(t *testing.T) {
	// Test a tool that's not Bash/Read/Write/Edit - should still show tool name
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	jsonEvent := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"foo"}}]}}`
	displayStreamEvent(jsonEvent)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "[Tool: Grep]") {
		t.Errorf("expected '[Tool: Grep]' in output, got %q", output)
	}
	// Should NOT show input since Grep is not in the special-cased list
	if strings.Contains(output, "foo") {
		t.Errorf("should not show input for non-special tools, got %q", output)
	}
}

func TestInvokeClaude_MockInvoker(t *testing.T) {
	recorder := SetupMockClaudeInvoker(t, nil)

	err := InvokeClaude("/test/workdir", "test prompt", "test-agent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(recorder.Invocations))
	}

	inv := recorder.Invocations[0]
	if inv.WorkDir != "/test/workdir" {
		t.Errorf("expected workDir '/test/workdir', got %q", inv.WorkDir)
	}
	if inv.Prompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %q", inv.Prompt)
	}
	if inv.AgentName != "test-agent" {
		t.Errorf("expected agentName 'test-agent', got %q", inv.AgentName)
	}
}

func TestInvokeClaude_MockInvokerError(t *testing.T) {
	expectedErr := errors.New("claude invocation failed")
	SetupMockClaudeInvoker(t, expectedErr)

	err := InvokeClaude("/test/workdir", "test prompt", "")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestInvokeClaudeForConflicts_MockInvoker(t *testing.T) {
	recorder := SetupMockClaudeInvoker(t, nil)

	conflicts := []string{"file1.go", "file2.go"}
	err := InvokeClaudeForConflicts("/test/workdir", "feature-branch", "main", conflicts)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(recorder.Invocations))
	}

	inv := recorder.Invocations[0]
	if inv.WorkDir != "/test/workdir" {
		t.Errorf("expected workDir '/test/workdir', got %q", inv.WorkDir)
	}
	// Prompt should contain conflict resolution content
	if !strings.Contains(inv.Prompt, "conflict") && !strings.Contains(inv.Prompt, "merge") {
		t.Errorf("expected prompt to contain conflict resolution content, got %q", inv.Prompt)
	}
	// AgentName should be empty for conflict resolution
	if inv.AgentName != "" {
		t.Errorf("expected empty agentName for conflicts, got %q", inv.AgentName)
	}
}

func TestClaudeBackendName(t *testing.T) {
	b := &ClaudeBackend{}
	if got := b.Name(); got != "claude" {
		t.Errorf("expected 'claude', got %q", got)
	}
}

func TestClaudeBackendRegistered(t *testing.T) {
	// After init(), the Claude backend should be registered
	backendMu.RLock()
	b, ok := backends["claude"]
	backendMu.RUnlock()

	if !ok {
		t.Fatal("expected 'claude' backend to be registered via init()")
	}
	if _, isClaudeBackend := b.(*ClaudeBackend); !isClaudeBackend {
		t.Fatalf("expected *ClaudeBackend, got %T", b)
	}
}

func TestClaudeBackendInvokeInteractive(t *testing.T) {
	recorder := SetupMockClaudeInvoker(t, nil)

	b := &ClaudeBackend{}
	err := b.InvokeInteractive("/work", "do stuff", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(recorder.Invocations))
	}
	inv := recorder.Invocations[0]
	if inv.WorkDir != "/work" || inv.Prompt != "do stuff" || inv.AgentName != "agent1" {
		t.Errorf("unexpected invocation args: %+v", inv)
	}
}

func TestClaudeBackendInvokeNonInteractive(t *testing.T) {
	// Save and restore the non-interactive invoker
	orig := claudeNonInteractiveInvoker
	var called bool
	var gotWorkDir, gotPrompt, gotAgent string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		called = true
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgent = agentName
		return nil
	}
	t.Cleanup(func() { claudeNonInteractiveInvoker = orig })

	b := &ClaudeBackend{}
	shutdown := make(chan struct{})
	err := b.InvokeNonInteractive("/work", "task prompt", "agent2", shutdown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected claudeNonInteractiveInvoker to be called")
	}
	if gotWorkDir != "/work" || gotPrompt != "task prompt" || gotAgent != "agent2" {
		t.Errorf("unexpected args: workDir=%q prompt=%q agent=%q", gotWorkDir, gotPrompt, gotAgent)
	}
}
