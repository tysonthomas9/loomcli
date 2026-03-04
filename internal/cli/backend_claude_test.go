package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
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

// TestShutdownRace_NoSignalAfterExit verifies that no SIGTERM is sent when
// the shutdown channel is triggered after the process has already exited.
// This reproduces the race condition that the atomic.Bool guard prevents:
// without the guard, sending SIGTERM to a reaped PID could hit an unrelated
// process that reused the same PID.
func TestShutdownRace_NoSignalAfterExit(t *testing.T) {
	t.Helper()

	// "true" exits immediately with status 0.
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Replicate the pattern from invokeClaudeNonInteractive.
	var exited atomic.Bool
	var signalSent atomic.Bool
	shutdown := make(chan struct{})
	done := make(chan struct{})

	go func() {
		select {
		case <-shutdown:
			if !exited.Load() {
				signalSent.Store(true)
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		case <-done:
		}
	}()

	// Wait for the process to finish, then mark it as exited.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("process exited with error: %v", err)
	}
	exited.Store(true)
	close(done)

	// Now trigger shutdown after the process is already gone.
	// The goroutine has already returned via <-done, but to be thorough
	// we also close shutdown to exercise the guard in case of scheduling
	// variation.
	close(shutdown)

	// Give a small window for any stray goroutine to run.
	time.Sleep(10 * time.Millisecond)

	if signalSent.Load() {
		t.Error("SIGTERM was sent after the process already exited; the atomic guard should have prevented this")
	}
}

// TestShutdownRace_SignalDuringRun verifies that SIGTERM IS delivered when the
// shutdown channel fires while the process is still running. This confirms the
// normal (non-race) shutdown path works correctly.
func TestShutdownRace_SignalDuringRun(t *testing.T) {
	t.Helper()

	// "sleep 60" will run until killed.
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	var exited atomic.Bool
	shutdown := make(chan struct{})
	done := make(chan struct{})

	go func() {
		select {
		case <-shutdown:
			if !exited.Load() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		case <-done:
		}
	}()

	// Trigger shutdown while the process is still alive.
	close(shutdown)

	// The process should be terminated by the SIGTERM. Wait for it, but
	// impose a deadline so the test doesn't hang if something goes wrong.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case err := <-waitDone:
		exited.Store(true)
		close(done)

		// On Linux, SIGTERM causes an exit with a signal-based error.
		if err == nil {
			t.Fatal("expected process to be killed by SIGTERM, but it exited cleanly")
		}
		// Verify the process was terminated by SIGTERM specifically.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if !status.Signaled() || status.Signal() != syscall.SIGTERM {
					t.Errorf("expected SIGTERM termination, got signal=%v exited=%v",
						status.Signal(), status.Exited())
				}
			}
		} else {
			t.Errorf("expected *exec.ExitError, got %T: %v", err, err)
		}

	case <-time.After(5 * time.Second):
		// Kill the process to avoid leaking it, then fail.
		_ = cmd.Process.Kill()
		exited.Store(true)
		close(done)
		t.Fatal("timed out waiting for process to be terminated by SIGTERM")
	}
}

func TestCollectClaudeStreamUsage_MessageStart(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// message_start event with usage nested in message
	line := `{"type":"message_start","message":{"id":"msg-123","usage":{"input_tokens":500,"output_tokens":0,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}`
	collectClaudeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", su.InputTokens)
	}
	if su.CacheReadTokens != 100 {
		t.Errorf("CacheReadTokens = %d, want 100", su.CacheReadTokens)
	}
	if su.CacheWriteTokens != 50 {
		t.Errorf("CacheWriteTokens = %d, want 50", su.CacheWriteTokens)
	}
}

func TestCollectClaudeStreamUsage_MessageDelta(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// message_delta event with top-level usage (cumulative final)
	line := `{"type":"message_delta","message":{"id":"msg-456"},"usage":{"input_tokens":1000,"output_tokens":300,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}`
	collectClaudeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", su.InputTokens)
	}
	if su.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want 300", su.OutputTokens)
	}
}

func TestCollectClaudeStreamUsage_Dedup(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// Same message ID in both message_start and message_delta
	// Only the first should be counted (dedup by message ID)
	line1 := `{"type":"message_start","message":{"id":"msg-789","usage":{"input_tokens":500,"output_tokens":0}}}`
	line2 := `{"type":"message_delta","message":{"id":"msg-789"},"usage":{"input_tokens":500,"output_tokens":200}}`
	collectClaudeStreamUsage(line1, c)
	collectClaudeStreamUsage(line2, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	// Only first occurrence counted
	if su.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500 (dedup)", su.InputTokens)
	}
}

func TestCollectClaudeStreamUsage_InvalidJSON(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// Should not panic on invalid JSON
	collectClaudeStreamUsage("not json at all", c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 after invalid JSON", su.InputTokens)
	}
}

func TestCollectClaudeStreamUsage_NoUsage(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// assistant event with no usage field
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`
	collectClaudeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (no usage in event)", su.InputTokens)
	}
}

func TestCollectClaudeStreamUsage_TopLevelUsagePreferred(t *testing.T) {
	c := usage.NewCollector("claude", "test")

	// Event with both top-level usage AND message.usage — top-level should be preferred
	line := `{"type":"message_delta","message":{"id":"msg-abc","usage":{"input_tokens":100,"output_tokens":10}},"usage":{"input_tokens":500,"output_tokens":200}}`
	collectClaudeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500 (top-level usage preferred)", su.InputTokens)
	}
	if su.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200 (top-level usage preferred)", su.OutputTokens)
	}
}
