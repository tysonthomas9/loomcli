package backends

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestDisplayStreamEvent_TextBlock(t *testing.T) {
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	// not parallel: captures os.Stdout
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
	t.Parallel()
	b := &ClaudeBackend{}
	if got := b.Name(); got != "claude" {
		t.Errorf("expected 'claude', got %q", got)
	}
}

func TestClaudeBackendRegistered(t *testing.T) {
	t.Parallel()
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
	// Not parallel: mutates global claudeInvoker.
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
	// Not parallel: mutates global claudeNonInteractiveInvoker.
	var called bool
	var gotWorkDir, gotPrompt, gotAgent string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgent = agentName
		return nil
	})

	b := &ClaudeBackend{}
	shutdown := make(chan struct{})
	err := b.InvokeNonInteractive("/work", "task prompt", "agent2", shutdown, nil)
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

func TestBuildClaudeResumeArgsArePositional(t *testing.T) {
	got := buildClaudeNonInteractiveArgs("session-123", "prompt")
	wantPrefix := []string{"--resume", "session-123"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("args too short: %v", got)
	}
	for i, want := range wantPrefix {
		if got[i] != want {
			t.Fatalf("arg[%d] = %q, want %q; args=%v", i, got[i], want, got)
		}
	}
	for _, arg := range got {
		if arg == "--session-id" {
			t.Fatalf("args contain invalid --session-id resume form: %v", got)
		}
	}
}

func TestBuildClaudeContinueSessionArgsArePositional(t *testing.T) {
	got := buildClaudeContinueSessionArgs("session-123")
	want := []string{"--resume", "session-123", "--dangerously-skip-permissions"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q; args=%v", i, got[i], want[i], got)
		}
	}
}

// TestShutdownRace_NoSignalAfterExit verifies that no SIGTERM is sent when
// the shutdown channel is triggered after the process has already exited.
// This reproduces the race condition that the processGuard prevents:
// without the guard, sending SIGTERM to a reaped PID could hit an unrelated
// process that reused the same PID.
func TestShutdownRace_NoSignalAfterExit(t *testing.T) {
	t.Parallel()
	t.Helper()

	// "true" exits immediately with status 0.
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	guard := newProcessGuard(cmd.Process)
	shutdown := make(chan struct{})

	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	// Wait for the process to finish, then mark it as exited.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("process exited with error: %v", err)
	}
	guard.WaitAndMark()

	// Now trigger shutdown after the process is already gone.
	close(shutdown)

	// Give a small window for any stray goroutine to run.
	time.Sleep(10 * time.Millisecond)

	// Verify Signal returns false after WaitAndMark.
	if guard.Signal(syscall.SIGTERM) {
		t.Error("Signal returned true after WaitAndMark; the guard should have prevented this")
	}
}

// TestShutdownRace_SignalDuringRun verifies that SIGTERM IS delivered when the
// shutdown channel fires while the process is still running. This confirms the
// normal (non-race) shutdown path works correctly.
func TestShutdownRace_SignalDuringRun(t *testing.T) {
	t.Parallel()
	t.Helper()

	// "sleep 60" will run until killed.
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	guard := newProcessGuard(cmd.Process)
	shutdown := make(chan struct{})

	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
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
		guard.WaitAndMark()

		// On Linux/Darwin, SIGTERM causes an exit with a signal-based error.
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
		guard.WaitAndMark()
		t.Fatal("timed out waiting for process to be terminated by SIGTERM")
	}
}

func TestCollectClaudeStreamUsage_MessageStart(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	c := usage.NewCollector("claude", "test")

	// Should not panic on invalid JSON
	collectClaudeStreamUsage("not json at all", c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 after invalid JSON", su.InputTokens)
	}
}

func TestCollectClaudeStreamUsage_NoUsage(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	c := usage.NewCollector("claude", "test")

	// Event with both top-level usage AND message.usage -- top-level should be preferred
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

func TestExtractClaudeSessionID_SystemInit(t *testing.T) {
	t.Parallel()
	line := `{"type":"system","subtype":"init","session_id":"sess-abc-123-def"}`
	sid, ok := extractClaudeSessionID(line)
	if !ok {
		t.Fatal("expected ok=true for system init event")
	}
	if sid != "sess-abc-123-def" {
		t.Errorf("session_id = %q, want %q", sid, "sess-abc-123-def")
	}
}

func TestExtractClaudeSessionID_NonSystemEvent(t *testing.T) {
	t.Parallel()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`
	_, ok := extractClaudeSessionID(line)
	if ok {
		t.Error("expected ok=false for non-system event")
	}
}

func TestExtractClaudeSessionID_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, ok := extractClaudeSessionID("not valid json at all")
	if ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestExtractClaudeSessionID_MissingSessionID(t *testing.T) {
	t.Parallel()
	line := `{"type":"system","subtype":"init"}`
	_, ok := extractClaudeSessionID(line)
	if ok {
		t.Error("expected ok=false when session_id is missing")
	}
}

func TestExtractClaudeSessionID_EmptySessionID(t *testing.T) {
	t.Parallel()
	line := `{"type":"system","subtype":"init","session_id":""}`
	_, ok := extractClaudeSessionID(line)
	if ok {
		t.Error("expected ok=false when session_id is empty string")
	}
}

func TestExtractClaudeSessionID_WrongSubtype(t *testing.T) {
	t.Parallel()
	line := `{"type":"system","subtype":"error","session_id":"sess-123"}`
	_, ok := extractClaudeSessionID(line)
	if ok {
		t.Error("expected ok=false for non-init subtype")
	}
}

// TestExtractClaudeSessionID_PTYEchoPrefix reproduces the headless daemon-resume
// bug: under a non-raw PTY the wrapper's stdin-EOF bytes (\x04\x04) are echoed
// and prepended (as "^D\b\b^D\b\b") to claude's first stream-json line — the
// system:init event carrying session_id. Without trimToJSONObject the leading
// control bytes break json.Unmarshal, the session id is never captured, and
// daemon --resume can never arm. This locks in the strip-to-'{' fix.
func TestExtractClaudeSessionID_PTYEchoPrefix(t *testing.T) {
	t.Parallel()
	// \x04 = EOT (^D), \x08 = backspace (\b): the exact prefix observed on the
	// wire when the daemon runs claude under a non-raw PTY.
	const echoPrefix = "\x04\b\b\x04\b\b"
	line := echoPrefix + `{"type":"system","subtype":"init","session_id":"sess-pty-echo-7dbd"}`
	sid, ok := extractClaudeSessionID(line)
	if !ok {
		t.Fatal("expected ok=true: a PTY-echoed control prefix must not defeat session capture")
	}
	if sid != "sess-pty-echo-7dbd" {
		t.Errorf("session_id = %q, want %q", sid, "sess-pty-echo-7dbd")
	}
}

func TestTrimToJSONObject(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`{"a":1}`:           `{"a":1}`,      // already clean — unchanged
		"\x04\b\b{\"a\":1}": `{"a":1}`,      // control prefix stripped
		"  {\"a\":1}":       `{"a":1}`,      // leading whitespace stripped
		"no json here":      "no json here", // no '{' — unchanged
		"":                  "",             // empty — unchanged
	}
	for in, want := range cases {
		if got := trimToJSONObject(in); got != want {
			t.Errorf("trimToJSONObject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutputRingBuffer(t *testing.T) {
	t.Parallel()

	t.Run("basic add and string", func(t *testing.T) {
		t.Parallel()
		buf := newOutputRingBuffer(5)
		buf.Add("line1")
		buf.Add("line2")
		buf.Add("line3")

		got := buf.String()
		want := "line1\nline2\nline3"
		if got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	})

	t.Run("cap enforcement drops oldest", func(t *testing.T) {
		t.Parallel()
		buf := newOutputRingBuffer(3)
		buf.Add("a")
		buf.Add("b")
		buf.Add("c")
		buf.Add("d")
		buf.Add("e")

		got := buf.String()
		want := "c\nd\ne"
		if got != want {
			t.Errorf("String() = %q, want %q (oldest should be dropped)", got, want)
		}
	})

	t.Run("50 line cap", func(t *testing.T) {
		t.Parallel()
		buf := newOutputRingBuffer(50)
		for i := 0; i < 75; i++ {
			buf.Add(fmt.Sprintf("line-%d", i))
		}

		lines := strings.Split(buf.String(), "\n")
		if len(lines) != 50 {
			t.Errorf("got %d lines, want 50", len(lines))
		}
		// First line should be line-25 (oldest 25 were dropped)
		if lines[0] != "line-25" {
			t.Errorf("first line = %q, want %q", lines[0], "line-25")
		}
		// Last line should be line-74
		if lines[49] != "line-74" {
			t.Errorf("last line = %q, want %q", lines[49], "line-74")
		}
	})

	t.Run("empty buffer", func(t *testing.T) {
		t.Parallel()
		buf := newOutputRingBuffer(10)
		if got := buf.String(); got != "" {
			t.Errorf("String() on empty buffer = %q, want empty", got)
		}
	})

	t.Run("single element", func(t *testing.T) {
		t.Parallel()
		buf := newOutputRingBuffer(5)
		buf.Add("only-line")
		if got := buf.String(); got != "only-line" {
			t.Errorf("String() = %q, want %q", got, "only-line")
		}
	})
}

func TestWrapInvocationError(t *testing.T) {
	baseErr := errors.New("process exited with failure")
	err := wrapInvocationError(baseErr, "Error: 429 too many requests")

	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("wrapInvocationError() did not return *InvocationError: %T", err)
	}
	if invErr.OutputTail != "process exited with failure\nError: 429 too many requests" {
		t.Errorf("OutputTail = %q", invErr.OutputTail)
	}
	if invErr.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", invErr.ExitCode)
	}
}

func TestResolveMaxBudgetUSD_Default(t *testing.T) {
	// Not parallel: mutates env vars.
	// Use t.Setenv to register cleanup, then unset so the var is truly absent.
	t.Setenv("LOOM_MAX_BUDGET_USD", "placeholder")
	os.Unsetenv("LOOM_MAX_BUDGET_USD")

	got := resolveMaxBudgetUSD()
	if got != "50.00" {
		t.Errorf("resolveMaxBudgetUSD() = %q, want %q", got, "50.00")
	}
}

func TestResolveMaxBudgetUSD_CustomValue(t *testing.T) {
	// Not parallel: mutates env vars.
	t.Setenv("LOOM_MAX_BUDGET_USD", "10.50")

	got := resolveMaxBudgetUSD()
	if got != "10.50" {
		t.Errorf("resolveMaxBudgetUSD() = %q, want %q", got, "10.50")
	}
}

func TestResolveMaxBudgetUSD_ZeroOptOut(t *testing.T) {
	// Not parallel: mutates env vars.
	t.Setenv("LOOM_MAX_BUDGET_USD", "0")

	got := resolveMaxBudgetUSD()
	if got != "" {
		t.Errorf("resolveMaxBudgetUSD() = %q, want empty string (opt-out)", got)
	}
}

func TestResolveMaxBudgetUSD_Invalid(t *testing.T) {
	// Not parallel: mutates env vars.
	t.Setenv("LOOM_MAX_BUDGET_USD", "abc")

	got := resolveMaxBudgetUSD()
	if got != "50.00" {
		t.Errorf("resolveMaxBudgetUSD() = %q, want %q (default fallback)", got, "50.00")
	}
}

func TestResolveMaxBudgetUSD_Negative(t *testing.T) {
	// Not parallel: mutates env vars.
	t.Setenv("LOOM_MAX_BUDGET_USD", "-1")

	got := resolveMaxBudgetUSD()
	if got != "50.00" {
		t.Errorf("resolveMaxBudgetUSD() = %q, want %q (default fallback)", got, "50.00")
	}
}

func TestScanStreamOutputReturnsTail(t *testing.T) {
	stdout := strings.NewReader("line-1\nline-2\nline-3\n")
	var seen []string

	got := scanStreamOutput(stdout, func(line string) {
		seen = append(seen, line)
	})

	if got != "line-1\nline-2\nline-3" {
		t.Errorf("scanStreamOutput() = %q", got)
	}
	if strings.Join(seen, "\n") != got {
		t.Errorf("seen lines = %q, want %q", strings.Join(seen, "\n"), got)
	}
}
