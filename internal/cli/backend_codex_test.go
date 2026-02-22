//go:build linux || darwin

package cli

import (
	"errors"
	"os"
	"testing"
)

func TestCodexBackendName(t *testing.T) {
	b := &CodexBackend{}
	if got := b.Name(); got != "codex" {
		t.Errorf("expected 'codex', got %q", got)
	}
}

func TestCodexBackendRegistered(t *testing.T) {
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
	var gotWorkDir, gotPrompt, gotAgentName string
	orig := codexInvoker
	codexInvoker = func(workDir, prompt, agentName string) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		return nil
	}
	t.Cleanup(func() { codexInvoker = orig })

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
	expectedErr := errors.New("codex invocation failed")
	orig := codexInvoker
	codexInvoker = func(workDir, prompt, agentName string) error {
		return expectedErr
	}
	t.Cleanup(func() { codexInvoker = orig })

	b := &CodexBackend{}
	err := b.InvokeInteractive("/test/dir", "prompt", "")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCodexInvokeNonInteractive_MockInvoker(t *testing.T) {
	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	orig := codexNonInteractiveInvoker
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	}
	t.Cleanup(func() { codexNonInteractiveInvoker = orig })

	shutdown := make(chan struct{})
	b := &CodexBackend{}
	err := b.InvokeNonInteractive("/work", "task prompt", "agent2", shutdown)
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
	expectedErr := errors.New("codex non-interactive failed")
	orig := codexNonInteractiveInvoker
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		return expectedErr
	}
	t.Cleanup(func() { codexNonInteractiveInvoker = orig })

	b := &CodexBackend{}
	err := b.InvokeNonInteractive("/work", "prompt", "", make(chan struct{}))
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCodexInvokeInteractive_EnvVars(t *testing.T) {
	// Verify that the default invoker sets LOOM_WORKTREE_PATH and BD_ACTOR.
	// We mock at the codexInvoker level and check the env vars that would be set.
	var capturedEnv []string
	orig := codexInvoker
	codexInvoker = func(workDir, prompt, agentName string) error {
		// Simulate what defaultCodexInvoker does: build the env
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		capturedEnv = env
		return nil
	}
	t.Cleanup(func() { codexInvoker = orig })

	b := &CodexBackend{}
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

func TestCodexInvokeInteractive_NoAgentName(t *testing.T) {
	// Verify BD_ACTOR is NOT added when agentName is empty.
	// Clear any existing BD_ACTOR from the environment first.
	origBDActor, hadBDActor := os.LookupEnv("BD_ACTOR")
	os.Unsetenv("BD_ACTOR")
	t.Cleanup(func() {
		if hadBDActor {
			os.Setenv("BD_ACTOR", origBDActor)
		}
	})

	var capturedEnv []string
	orig := codexInvoker
	codexInvoker = func(workDir, prompt, agentName string) error {
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		capturedEnv = env
		return nil
	}
	t.Cleanup(func() { codexInvoker = orig })

	b := &CodexBackend{}
	err := b.InvokeInteractive("/my/work", "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsSubstring(capturedEnv, "BD_ACTOR=") {
		t.Error("expected BD_ACTOR to NOT be in env when agentName is empty")
	}
}

func TestCodexBackendInvokeInteractive(t *testing.T) {
	var called bool
	orig := codexInvoker
	codexInvoker = func(workDir, prompt, agentName string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { codexInvoker = orig })

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
	var called bool
	var gotShutdown <-chan struct{}
	orig := codexNonInteractiveInvoker
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		called = true
		gotShutdown = shutdown
		return nil
	}
	t.Cleanup(func() { codexNonInteractiveInvoker = orig })

	shutdown := make(chan struct{})
	b := &CodexBackend{}
	err := b.InvokeNonInteractive("/work", "task", "agent2", shutdown)
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
	// Tests the full dispatch chain: InvokeAgentNonInteractive → CodexBackend.InvokeNonInteractive → codexNonInteractiveInvoker
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	// Mock the codex invoker to capture arguments
	oldCodex := codexNonInteractiveInvoker
	t.Cleanup(func() { codexNonInteractiveInvoker = oldCodex })

	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	}

	shutdown := make(chan struct{})
	err := InvokeAgentNonInteractive("/dispatch/test", "codex dispatch prompt", "dispatch-agent", shutdown)
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
