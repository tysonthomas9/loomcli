package backends

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestOpenCodeBackendName(t *testing.T) {
	t.Parallel()
	b := &OpenCodeBackend{}
	if got := b.Name(); got != "opencode" {
		t.Errorf("expected 'opencode', got %q", got)
	}
}

func TestOpenCodeBackendRegistered(t *testing.T) {
	t.Parallel()
	// After init(), the OpenCode backend should be registered
	backendMu.RLock()
	b, ok := backends["opencode"]
	backendMu.RUnlock()

	if !ok {
		t.Fatal("expected 'opencode' backend to be registered via init()")
	}
	if _, isOpenCodeBackend := b.(*OpenCodeBackend); !isOpenCodeBackend {
		t.Fatalf("expected *OpenCodeBackend, got %T", b)
	}
}

func TestOpenCodeInvokeInteractive_MockInvoker(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker.
	var gotWorkDir, gotPrompt, gotAgentName string
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		return nil
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/test/dir", "do stuff", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotWorkDir != "/test/dir" {
		t.Errorf("expected workDir '/test/dir', got %q", gotWorkDir)
	}
	if gotPrompt != "do stuff" {
		t.Errorf("expected prompt 'do stuff', got %q", gotPrompt)
	}
	if gotAgentName != "agent1" {
		t.Errorf("expected agentName 'agent1', got %q", gotAgentName)
	}
}

func TestOpenCodeInvokeInteractive_MockInvokerError(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker.
	expectedErr := errors.New("opencode invocation failed")
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		return expectedErr
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/test/dir", "prompt", "")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestOpenCodeInvokeNonInteractive_MockInvoker(t *testing.T) {
	// Not parallel: mutates global openCodeNonInteractiveInvoker.
	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	installOpenCodeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractive("/work", "task prompt", "agent2", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotWorkDir != "/work" {
		t.Errorf("expected workDir '/work', got %q", gotWorkDir)
	}
	if gotPrompt != "task prompt" {
		t.Errorf("expected prompt 'task prompt', got %q", gotPrompt)
	}
	if gotAgentName != "agent2" {
		t.Errorf("expected agentName 'agent2', got %q", gotAgentName)
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestOpenCodeInvokeNonInteractive_MockInvokerError(t *testing.T) {
	// Not parallel: mutates global openCodeNonInteractiveInvoker.
	expectedErr := errors.New("opencode non-interactive failed")
	installOpenCodeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		return expectedErr
	})

	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractive("/work", "prompt", "", make(chan struct{}), nil)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestOpenCodeInvokeInteractive_EnvVars(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker.
	var capturedEnv []string
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "LOOM_AGENT_NAME="+agentName)
		}
		capturedEnv = env
		return nil
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/my/work", "prompt", "test-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsSubstring(capturedEnv, "LOOM_WORKTREE_PATH=/my/work") {
		t.Error("expected LOOM_WORKTREE_PATH=/my/work in env")
	}
	if !containsSubstring(capturedEnv, "LOOM_AGENT_NAME=test-agent") {
		t.Error("expected LOOM_AGENT_NAME=test-agent in env")
	}
}

func TestOpenCodeInvokeInteractive_NoAgentName(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker and env vars.
	// Clear any existing LOOM_AGENT_NAME from the environment first.
	origBDActor, hadBDActor := os.LookupEnv("LOOM_AGENT_NAME")
	os.Unsetenv("LOOM_AGENT_NAME")
	t.Cleanup(func() {
		if hadBDActor {
			os.Setenv("LOOM_AGENT_NAME", origBDActor)
		}
	})

	var capturedEnv []string
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "LOOM_AGENT_NAME="+agentName)
		}
		capturedEnv = env
		return nil
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/my/work", "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsSubstring(capturedEnv, "LOOM_AGENT_NAME=") {
		t.Error("expected LOOM_AGENT_NAME to NOT be in env when agentName is empty")
	}
}

func TestOpenCodeBackendInvokeInteractive(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker.
	var called bool
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		called = true
		return nil
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/work", "do stuff", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected openCodeInvoker to be called")
	}
}

func TestOpenCodeBackendInvokeNonInteractive(t *testing.T) {
	// Not parallel: mutates global openCodeNonInteractiveInvoker.
	var called bool
	var gotShutdown <-chan struct{}
	installOpenCodeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractive("/work", "task", "agent2", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected openCodeNonInteractiveInvoker to be called")
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestOpenCodeBackendInvokeNonInteractiveResumed(t *testing.T) {
	// Not parallel: mutates global openCodeNonInteractiveResumedInvoker.
	var called bool
	var gotWorkDir, gotPrompt, gotAgentName, gotProviderSessionID string
	var gotShutdown <-chan struct{}
	installOpenCodeNonInteractiveResumedMock(t, func(workDir, prompt, agentName, providerSessionID string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotProviderSessionID = providerSessionID
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractiveResumed("/work", "follow-up task", "agent2", "opencode-session-123", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected openCodeNonInteractiveResumedInvoker to be called")
	}
	if gotWorkDir != "/work" || gotPrompt != "follow-up task" || gotAgentName != "agent2" || gotProviderSessionID != "opencode-session-123" {
		t.Fatalf("resumed invocation = workDir=%q prompt=%q agent=%q session=%q", gotWorkDir, gotPrompt, gotAgentName, gotProviderSessionID)
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestBuildOpenCodeNonInteractiveArgs(t *testing.T) {
	t.Setenv("LOOM_OPENCODE_MODEL", "")

	got := buildOpenCodeNonInteractiveArgs("/work", "")
	want := []string{"run", "--format", "json", "--dir", "/work", "--dangerously-skip-permissions"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("buildOpenCodeNonInteractiveArgs() = %+v, want %+v", got, want)
	}

	got = buildOpenCodeNonInteractiveArgs("/work", "opencode-session-123")
	want = []string{"run", "--format", "json", "--dir", "/work", "--dangerously-skip-permissions", "--session", "opencode-session-123"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("buildOpenCodeNonInteractiveArgs(resume) = %+v, want %+v", got, want)
	}
}

func TestCollectOpenCodeStreamUsage_WithUsage(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("opencode", "test")

	line := `{"usage":{"input_tokens":800,"output_tokens":400}}`
	collectOpenCodeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 800 {
		t.Errorf("InputTokens = %d, want 800", su.InputTokens)
	}
	if su.OutputTokens != 400 {
		t.Errorf("OutputTokens = %d, want 400", su.OutputTokens)
	}
}

func TestCollectOpenCodeStreamUsage_NoUsage(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("opencode", "test")

	line := `{"type":"message","content":"hello"}`
	collectOpenCodeStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", su.InputTokens)
	}
}

func TestCollectOpenCodeStreamUsage_InvalidJSON(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("opencode", "test")
	collectOpenCodeStreamUsage("not json", c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", su.InputTokens)
	}
}

func TestExtractOpenCodeStreamError_DataMessage(t *testing.T) {
	t.Parallel()

	line := `{"type":"error","error":{"name":"UnknownError","data":{"message":"Model not found: fake/fake-model."}}}`
	got, ok := extractOpenCodeStreamError(line)
	if !ok {
		t.Fatal("extractOpenCodeStreamError() = not found, want found")
	}
	if got != "Model not found: fake/fake-model." {
		t.Fatalf("extractOpenCodeStreamError() = %q", got)
	}
}

func TestExtractOpenCodeStreamError_Message(t *testing.T) {
	t.Parallel()

	line := `{"type":"error","error":{"message":"401 Unauthorized"}}`
	got, ok := extractOpenCodeStreamError(line)
	if !ok {
		t.Fatal("extractOpenCodeStreamError() = not found, want found")
	}
	if got != "401 Unauthorized" {
		t.Fatalf("extractOpenCodeStreamError() = %q", got)
	}
}

func TestExtractOpenCodeStreamError_NonErrorEvent(t *testing.T) {
	t.Parallel()

	line := `{"type":"text","text":"hello"}`
	if got, ok := extractOpenCodeStreamError(line); ok {
		t.Fatalf("extractOpenCodeStreamError() = (%q, true), want not found", got)
	}
}

func TestFinalizeOpenCodeRun_StreamErrorWithoutExitFailure(t *testing.T) {
	t.Parallel()

	outputTail := `{"type":"error","error":{"data":{"message":"Model not found: fake/fake-model."}}}`
	err := finalizeOpenCodeRun(nil, outputTail, "Model not found: fake/fake-model.")
	if err == nil {
		t.Fatal("finalizeOpenCodeRun() = nil, want error")
	}

	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("finalizeOpenCodeRun() returned %T, want *InvocationError", err)
	}
	if invErr.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", invErr.ExitCode)
	}
	if !strings.Contains(invErr.OutputTail, "Model not found: fake/fake-model.") {
		t.Fatalf("OutputTail = %q, want streamed error evidence", invErr.OutputTail)
	}
}

func TestFinalizeOpenCodeRun_PreservesEarlyStreamErrorOnExitFailure(t *testing.T) {
	t.Parallel()

	outputTail := `{"type":"text","text":"later output only"}`
	err := finalizeOpenCodeRun(errors.New("exit status 1"), outputTail, "Model not found: fake/fake-model.")
	if err == nil {
		t.Fatal("finalizeOpenCodeRun() = nil, want error")
	}

	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("finalizeOpenCodeRun() returned %T, want *InvocationError", err)
	}
	if !strings.Contains(invErr.OutputTail, "Model not found: fake/fake-model.") {
		t.Fatalf("OutputTail = %q, want preserved streamed error evidence", invErr.OutputTail)
	}
	if !strings.Contains(invErr.OutputTail, "later output only") {
		t.Fatalf("OutputTail = %q, want later output retained", invErr.OutputTail)
	}
}

func TestOpenCodeBackendProviderMetadataReporter(t *testing.T) {
	b := &OpenCodeBackend{}
	openCodeProviderMetadata.Clear("opencode")
	t.Cleanup(func() { openCodeProviderMetadata.Clear("opencode") })

	openCodeProviderMetadata.IngestLine(`{"type":"session","session":{"id":"opencode-session-1"},"model":"openai/gpt-5"}`)

	if got := b.LastSessionID("/work"); got != "opencode-session-1" {
		t.Fatalf("LastSessionID() = %q, want opencode-session-1", got)
	}
	meta := b.LastProviderMetadata("/work")
	if meta["provider"] != "opencode" || meta["provider_session_id"] != "opencode-session-1" || meta["provider_model"] != "openai/gpt-5" {
		t.Fatalf("LastProviderMetadata() = %#v, want opencode provider metadata", meta)
	}
}
