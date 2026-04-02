package cli

import (
	"errors"
	"os"
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
			env = append(env, "BD_ACTOR="+agentName)
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
	if !containsSubstring(capturedEnv, "BD_ACTOR=test-agent") {
		t.Error("expected BD_ACTOR=test-agent in env")
	}
}

func TestOpenCodeInvokeInteractive_NoAgentName(t *testing.T) {
	// Not parallel: mutates global openCodeInvoker and env vars.
	// Clear any existing BD_ACTOR from the environment first.
	origBDActor, hadBDActor := os.LookupEnv("BD_ACTOR")
	os.Unsetenv("BD_ACTOR")
	t.Cleanup(func() {
		if hadBDActor {
			os.Setenv("BD_ACTOR", origBDActor)
		}
	})

	var capturedEnv []string
	installOpenCodeInvokerMock(t, func(workDir, prompt, agentName string) error {
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		capturedEnv = env
		return nil
	})

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/my/work", "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsSubstring(capturedEnv, "BD_ACTOR=") {
		t.Error("expected BD_ACTOR to NOT be in env when agentName is empty")
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
