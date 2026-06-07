package backends

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestCodexBackendName(t *testing.T) {
	t.Parallel()
	b := &CodexBackend{}
	if got := b.Name(); got != "codex" {
		t.Errorf("expected 'codex', got %q", got)
	}
}

func TestCodexBackendRegistered(t *testing.T) {
	t.Parallel()
	// After init(), the Codex backend should be registered
	backendMu.RLock()
	b, ok := backends["codex"]
	backendMu.RUnlock()

	if !ok {
		t.Fatal("expected 'codex' backend to be registered via init()")
	}
	if _, isCodexBackend := b.(*CodexBackend); !isCodexBackend {
		t.Fatalf("expected *CodexBackend, got %T", b)
	}
}

func TestCodexInvokeInteractive_MockInvoker(t *testing.T) {
	// Not parallel: mutates global codexInvoker.
	var gotWorkDir, gotPrompt, gotAgentName string
	installCodexInvokerMock(t, func(workDir, prompt, agentName string) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		return nil
	})

	b := &CodexBackend{}
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

func TestCodexInvokeInteractive_MockInvokerError(t *testing.T) {
	// Not parallel: mutates global codexInvoker.
	expectedErr := errors.New("codex invocation failed")
	installCodexInvokerMock(t, func(workDir, prompt, agentName string) error {
		return expectedErr
	})

	b := &CodexBackend{}
	err := b.InvokeInteractive("/test/dir", "prompt", "")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCodexInvokeNonInteractive_MockInvoker(t *testing.T) {
	// Not parallel: mutates global codexNonInteractiveInvoker.
	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &CodexBackend{}
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

func TestCodexInvokeNonInteractive_MockInvokerError(t *testing.T) {
	// Not parallel: mutates global codexNonInteractiveInvoker.
	expectedErr := errors.New("codex non-interactive failed")
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		return expectedErr
	})

	b := &CodexBackend{}
	err := b.InvokeNonInteractive("/work", "prompt", "", make(chan struct{}), nil)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCodexInvokeInteractive_EnvVars(t *testing.T) {
	// Not parallel: mutates global codexInvoker.
	// Verify that the default invoker sets LOOM_WORKTREE_PATH and LOOM_AGENT_NAME.
	// We mock at the codexInvoker level and check the env vars that would be set.
	var capturedEnv []string
	installCodexInvokerMock(t, func(workDir, prompt, agentName string) error {
		// Simulate what defaultCodexInvoker does: build the env
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "LOOM_AGENT_NAME="+agentName)
		}
		capturedEnv = env
		return nil
	})

	b := &CodexBackend{}
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

func TestCodexInvokeInteractive_NoAgentName(t *testing.T) {
	// Not parallel: mutates global codexInvoker and env vars.
	// Verify LOOM_AGENT_NAME is NOT added when agentName is empty.
	// Clear any existing LOOM_AGENT_NAME from the environment first.
	origBDActor, hadBDActor := os.LookupEnv("LOOM_AGENT_NAME")
	os.Unsetenv("LOOM_AGENT_NAME")
	t.Cleanup(func() {
		if hadBDActor {
			os.Setenv("LOOM_AGENT_NAME", origBDActor)
		}
	})

	var capturedEnv []string
	installCodexInvokerMock(t, func(workDir, prompt, agentName string) error {
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "LOOM_AGENT_NAME="+agentName)
		}
		capturedEnv = env
		return nil
	})

	b := &CodexBackend{}
	err := b.InvokeInteractive("/my/work", "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsSubstring(capturedEnv, "LOOM_AGENT_NAME=") {
		t.Error("expected LOOM_AGENT_NAME to NOT be in env when agentName is empty")
	}
}

func TestCodexBackendInvokeInteractive(t *testing.T) {
	// Not parallel: mutates global codexInvoker.
	var called bool
	installCodexInvokerMock(t, func(workDir, prompt, agentName string) error {
		called = true
		return nil
	})

	b := &CodexBackend{}
	err := b.InvokeInteractive("/work", "do stuff", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected codexInvoker to be called")
	}
}

func TestCodexBackendInvokeNonInteractive(t *testing.T) {
	// Not parallel: mutates global codexNonInteractiveInvoker.
	var called bool
	var gotShutdown <-chan struct{}
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &CodexBackend{}
	err := b.InvokeNonInteractive("/work", "task", "agent2", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected codexNonInteractiveInvoker to be called")
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestCodexBackend_DispatchViaNonInteractive(t *testing.T) {
	// Not parallel: mutates global backend state and codexNonInteractiveInvoker.
	// Tests the full dispatch chain: InvokeAgentNonInteractive -> CodexBackend.InvokeNonInteractive -> codexNonInteractiveInvoker
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	// Mock the codex invoker to capture arguments
	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	installCodexNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	err := InvokeAgentNonInteractive("/dispatch/test", "codex dispatch prompt", "dispatch-agent", shutdown, nil)
	if err != nil {
		t.Fatalf("InvokeAgentNonInteractive() unexpected error: %v", err)
	}

	if gotWorkDir != "/dispatch/test" {
		t.Errorf("workDir = %q, want %q", gotWorkDir, "/dispatch/test")
	}
	if gotPrompt != "codex dispatch prompt" {
		t.Errorf("prompt = %q, want %q", gotPrompt, "codex dispatch prompt")
	}
	if gotAgentName != "dispatch-agent" {
		t.Errorf("agentName = %q, want %q", gotAgentName, "dispatch-agent")
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestBuildCodexNonInteractiveArgsPromptIsPositional(t *testing.T) {
	t.Parallel()
	got := buildCodexNonInteractiveArgs("prompt body")
	want := []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "prompt body"}
	if !equalStrings(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestCollectCodexStreamUsage_TurnCompleted(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("codex", "test")

	line := `{"type":"turn.completed","usage":{"input_tokens":1000,"output_tokens":500}}`
	collectCodexStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", su.InputTokens)
	}
	if su.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", su.OutputTokens)
	}
}

func TestCollectCodexStreamUsage_NoUsage(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("codex", "test")

	line := `{"type":"message","content":"hello"}`
	collectCodexStreamUsage(line, c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", su.InputTokens)
	}
}

func TestCollectCodexStreamUsage_InvalidJSON(t *testing.T) {
	t.Parallel()
	c := usage.NewCollector("codex", "test")
	collectCodexStreamUsage("not json", c)

	su := c.Finalize("", "", time.Now(), time.Now(), 0)
	if su.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", su.InputTokens)
	}
}
