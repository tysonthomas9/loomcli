//go:build ignore

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// mockBackend implements Backend for testing.
type mockBackend struct {
	name                string
	interactiveCalls    []mockCall
	nonInteractiveCalls []mockNonInteractiveCall
	interactiveErr      error
	nonInteractiveErr   error
}

type mockCall struct {
	workDir, prompt, agentName string
}

type mockNonInteractiveCall struct {
	workDir, prompt, agentName string
	shutdown                   <-chan struct{}
	collector                  *usage.Collector
}

func (m *mockBackend) Name() string { return m.name }

func (m *mockBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	m.interactiveCalls = append(m.interactiveCalls, mockCall{workDir, prompt, agentName})
	return m.interactiveErr
}

func (m *mockBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	m.nonInteractiveCalls = append(m.nonInteractiveCalls, mockNonInteractiveCall{workDir, prompt, agentName, shutdown, collector})
	return m.nonInteractiveErr
}

// resetBackendState resets the global backend registry for test isolation.
func resetBackendState(t *testing.T) {
	t.Helper()
	origBackends := backends
	origActive := activeBackend
	t.Cleanup(func() {
		backendMu.Lock()
		defer backendMu.Unlock()
		backends = origBackends
		activeBackend = origActive
	})
	backendMu.Lock()
	defer backendMu.Unlock()
	backends = make(map[string]Backend)
	activeBackend = "claude"
}

func TestRegisterBackend(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "test"}
	RegisterBackend(m)

	backendMu.RLock()
	got, ok := backends["test"]
	backendMu.RUnlock()
	if !ok {
		t.Fatal("expected backend 'test' to be registered")
	}
	if got != m {
		t.Fatal("registered backend does not match")
	}
}

func TestRegisterBackendOverwrites(t *testing.T) {
	resetBackendState(t)

	m1 := &mockBackend{name: "dup"}
	m2 := &mockBackend{name: "dup"}
	RegisterBackend(m1)
	RegisterBackend(m2)

	backendMu.RLock()
	got := backends["dup"]
	backendMu.RUnlock()
	if got != m2 {
		t.Fatal("expected last-registered backend to win")
	}
}

func TestRegisterBackendNilPanics(t *testing.T) {
	resetBackendState(t)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil backend")
		}
	}()
	RegisterBackend(nil)
}

func TestSetBackendValid(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "codex"})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := GetBackendName(); got != "codex" {
		t.Fatalf("expected 'codex', got %q", got)
	}
}

func TestSetBackendInvalid(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "claude"})
	err := SetBackend("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	// Should list available backends in error message
	if got := err.Error(); got != `unknown backend "nonexistent"; available: [claude]` {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestGetBackendNameDefault(t *testing.T) {
	resetBackendState(t)

	if got := GetBackendName(); got != "claude" {
		t.Fatalf("expected default 'claude', got %q", got)
	}
}

func TestListBackendsSorted(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "codex"})
	RegisterBackend(&mockBackend{name: "aider"})
	RegisterBackend(&mockBackend{name: "claude"})

	got := ListBackends()
	expected := []string{"aider", "claude", "codex"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}

func TestListBackendsEmpty(t *testing.T) {
	resetBackendState(t)

	got := ListBackends()
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func TestInvokeAgentDispatches(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude"}
	RegisterBackend(m)

	err := InvokeAgent("/work", "hello", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.interactiveCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.interactiveCalls))
	}
	call := m.interactiveCalls[0]
	if call.workDir != "/work" || call.prompt != "hello" || call.agentName != "agent1" {
		t.Fatalf("unexpected call args: %+v", call)
	}
}

func TestInvokeAgentReturnsError(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude", interactiveErr: fmt.Errorf("invoke failed")}
	RegisterBackend(m)

	err := InvokeAgent("/work", "hello", "")
	if err == nil || err.Error() != "invoke failed" {
		t.Fatalf("expected 'invoke failed', got %v", err)
	}
}

func TestInvokeAgentUnregistered(t *testing.T) {
	resetBackendState(t)

	// activeBackend is "claude" but nothing is registered
	err := InvokeAgent("/work", "hello", "")
	if err == nil {
		t.Fatal("expected error for unregistered backend")
	}
	if got := err.Error(); got != `backend "claude" not registered` {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestInvokeAgentNonInteractiveDispatches(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude"}
	RegisterBackend(m)

	shutdown := make(chan struct{})
	err := InvokeAgentNonInteractive("/work", "task", "agent2", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.nonInteractiveCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.nonInteractiveCalls))
	}
	call := m.nonInteractiveCalls[0]
	if call.workDir != "/work" || call.prompt != "task" || call.agentName != "agent2" {
		t.Fatalf("unexpected call args: %+v", call)
	}
}

func TestInvokeAgentNonInteractiveReturnsError(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude", nonInteractiveErr: fmt.Errorf("non-interactive failed")}
	RegisterBackend(m)

	err := InvokeAgentNonInteractive("/work", "task", "", nil, nil)
	if err == nil || err.Error() != "non-interactive failed" {
		t.Fatalf("expected 'non-interactive failed', got %v", err)
	}
}

func TestInvokeAgentNonInteractiveUnregistered(t *testing.T) {
	resetBackendState(t)

	err := InvokeAgentNonInteractive("/work", "task", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for unregistered backend")
	}
}

func TestConcurrentAccess(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "claude"})
	RegisterBackend(&mockBackend{name: "codex"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = SetBackend("codex")
		}()
		go func() {
			defer wg.Done()
			_ = GetBackendName()
		}()
	}
	wg.Wait()

	// Just verify no race/panic occurred; final state is nondeterministic
	name := GetBackendName()
	if name != "claude" && name != "codex" {
		t.Fatalf("unexpected backend name: %s", name)
	}
}

func TestResolveBackendName_FlagPrecedence(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	// Set all three sources: flag, env, config
	backendFlag = "codex"
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": "opencode"})

	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{"LOOM_CONFIG_DIR": tmpDir})
	configData := []byte("backend: aider\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configData, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	got := ResolveBackendName()
	if got != "codex" {
		t.Fatalf("expected flag value 'codex' to take precedence, got %q", got)
	}
}

func TestResolveBackendName_EnvPrecedence(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	// No flag set, but env and config are set
	backendFlag = ""
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": "opencode"})

	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{"LOOM_CONFIG_DIR": tmpDir})
	configData := []byte("backend: aider\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configData, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	got := ResolveBackendName()
	if got != "opencode" {
		t.Fatalf("expected env value 'opencode' to take precedence over config, got %q", got)
	}
}

func TestResolveBackendName_ConfigFallback(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	// No flag, no env, but config is set
	backendFlag = ""
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": ""})

	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{"LOOM_CONFIG_DIR": tmpDir})
	configData := []byte("backend: aider\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configData, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	got := ResolveBackendName()
	if got != "aider" {
		t.Fatalf("expected config value 'aider', got %q", got)
	}
}

func TestResolveBackendName_Default(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	// No flag, no env, no config file
	backendFlag = ""
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": ""})

	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{"LOOM_CONFIG_DIR": tmpDir})
	// No config.yaml in tmpDir, so LoadConfig returns (nil, nil)

	got := ResolveBackendName()
	if got != "claude" {
		t.Fatalf("expected default 'claude', got %q", got)
	}
}

func TestValidBackendNames(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "claude"})
	RegisterBackend(&mockBackend{name: "codex"})
	RegisterBackend(&mockBackend{name: "opencode"})

	got := ValidBackendNames()
	// ValidBackendNames returns sorted, comma-separated names
	expected := "claude, codex, opencode"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestValidBackendNamesEmpty(t *testing.T) {
	resetBackendState(t)

	got := ValidBackendNames()
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestInvokeAgentForConflicts(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude"}
	RegisterBackend(m)

	conflicts := []string{"file1.go", "file2.go"}
	err := InvokeAgentForConflicts("/work", "feature-branch", "main", conflicts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.interactiveCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.interactiveCalls))
	}

	call := m.interactiveCalls[0]
	if call.workDir != "/work" {
		t.Errorf("expected workDir '/work', got %q", call.workDir)
	}
	// Prompt should contain conflict resolution content
	if !strings.Contains(call.prompt, "conflict") && !strings.Contains(call.prompt, "Conflict") {
		t.Errorf("expected prompt to mention conflicts, got %q", call.prompt)
	}
	// Prompt should mention the conflicted files
	if !strings.Contains(call.prompt, "file1.go") {
		t.Errorf("expected prompt to contain 'file1.go', got %q", call.prompt)
	}
	if !strings.Contains(call.prompt, "file2.go") {
		t.Errorf("expected prompt to contain 'file2.go', got %q", call.prompt)
	}
	// Prompt should mention the branches
	if !strings.Contains(call.prompt, "feature-branch") {
		t.Errorf("expected prompt to contain 'feature-branch', got %q", call.prompt)
	}
	if !strings.Contains(call.prompt, "main") {
		t.Errorf("expected prompt to contain 'main', got %q", call.prompt)
	}
	// agentName should be empty for conflict resolution
	if call.agentName != "" {
		t.Errorf("expected empty agentName for conflicts, got %q", call.agentName)
	}
}

func TestInvokeAgentForConflictsError(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude", interactiveErr: fmt.Errorf("conflict resolve failed")}
	RegisterBackend(m)

	err := InvokeAgentForConflicts("/work", "feat", "main", []string{"a.go"})
	if err == nil || err.Error() != "conflict resolve failed" {
		t.Fatalf("expected 'conflict resolve failed', got %v", err)
	}
}

func TestResolveAndSetBackend(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	// Register backends and set flag to codex
	RegisterBackend(&mockBackend{name: "claude"})
	RegisterBackend(&mockBackend{name: "codex"})
	backendFlag = "codex"
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": ""})

	err := ResolveAndSetBackend()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := GetBackendName(); got != "codex" {
		t.Fatalf("expected 'codex', got %q", got)
	}
}

func TestResolveAndSetBackendInvalid(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	RegisterBackend(&mockBackend{name: "claude"})
	backendFlag = "nonexistent"

	err := ResolveAndSetBackend()
	if err == nil {
		t.Fatal("expected error for unregistered resolved backend")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected error to mention 'nonexistent', got %q", err.Error())
	}
}

func TestResolveAndSetBackendDefault(t *testing.T) {
	resetBackendState(t)

	// Save and restore backendFlag
	origFlag := backendFlag
	t.Cleanup(func() { backendFlag = origFlag })

	RegisterBackend(&mockBackend{name: "claude"})
	backendFlag = ""
	SetupTestEnv(t, map[string]string{"LOOM_BACKEND": ""})

	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{"LOOM_CONFIG_DIR": tmpDir})

	err := ResolveAndSetBackend()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := GetBackendName(); got != "claude" {
		t.Fatalf("expected default 'claude', got %q", got)
	}
}
