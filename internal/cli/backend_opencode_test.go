package cli

import (
	"errors"
	"os"
	"testing"
)

func TestOpenCodeBackendName(t *testing.T) {
	b := &OpenCodeBackend{}
	if got := b.Name(); got != "opencode" {
		t.Errorf("expected 'opencode', got %q", got)
	}
}

func TestOpenCodeBackendRegistered(t *testing.T) {
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
	var gotWorkDir, gotPrompt, gotAgentName string
	orig := openCodeInvoker
	openCodeInvoker = func(workDir, prompt, agentName string) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		return nil
	}
	t.Cleanup(func() { openCodeInvoker = orig })

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
	expectedErr := errors.New("opencode invocation failed")
	orig := openCodeInvoker
	openCodeInvoker = func(workDir, prompt, agentName string) error {
		return expectedErr
	}
	t.Cleanup(func() { openCodeInvoker = orig })

	b := &OpenCodeBackend{}
	err := b.InvokeInteractive("/test/dir", "prompt", "")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestOpenCodeInvokeNonInteractive_MockInvoker(t *testing.T) {
	var gotWorkDir, gotPrompt, gotAgentName string
	var gotShutdown <-chan struct{}
	orig := openCodeNonInteractiveInvoker
	openCodeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotShutdown = shutdown
		return nil
	}
	t.Cleanup(func() { openCodeNonInteractiveInvoker = orig })

	shutdown := make(chan struct{})
	b := &OpenCodeBackend{}
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

func TestOpenCodeInvokeNonInteractive_MockInvokerError(t *testing.T) {
	expectedErr := errors.New("opencode non-interactive failed")
	orig := openCodeNonInteractiveInvoker
	openCodeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		return expectedErr
	}
	t.Cleanup(func() { openCodeNonInteractiveInvoker = orig })

	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractive("/work", "prompt", "", make(chan struct{}))
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestOpenCodeInvokeInteractive_EnvVars(t *testing.T) {
	var capturedEnv []string
	orig := openCodeInvoker
	openCodeInvoker = func(workDir, prompt, agentName string) error {
		env := append(os.Environ(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		capturedEnv = env
		return nil
	}
	t.Cleanup(func() { openCodeInvoker = orig })

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
	// Clear any existing BD_ACTOR from the environment first.
	origBDActor, hadBDActor := os.LookupEnv("BD_ACTOR")
	os.Unsetenv("BD_ACTOR")
	t.Cleanup(func() {
		if hadBDActor {
			os.Setenv("BD_ACTOR", origBDActor)
		}
	})

	var capturedEnv []string
	orig := openCodeInvoker
	openCodeInvoker = func(workDir, prompt, agentName string) error {
		env := append(os.Environ(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		capturedEnv = env
		return nil
	}
	t.Cleanup(func() { openCodeInvoker = orig })

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
	var called bool
	orig := openCodeInvoker
	openCodeInvoker = func(workDir, prompt, agentName string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { openCodeInvoker = orig })

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
	var called bool
	var gotShutdown <-chan struct{}
	orig := openCodeNonInteractiveInvoker
	openCodeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		called = true
		gotShutdown = shutdown
		return nil
	}
	t.Cleanup(func() { openCodeNonInteractiveInvoker = orig })

	shutdown := make(chan struct{})
	b := &OpenCodeBackend{}
	err := b.InvokeNonInteractive("/work", "task", "agent2", shutdown)
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
