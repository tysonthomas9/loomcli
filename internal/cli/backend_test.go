package cli

import (
	"fmt"
	"sync"
	"testing"
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
}

func (m *mockBackend) Name() string { return m.name }

func (m *mockBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	m.interactiveCalls = append(m.interactiveCalls, mockCall{workDir, prompt, agentName})
	return m.interactiveErr
}

func (m *mockBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	m.nonInteractiveCalls = append(m.nonInteractiveCalls, mockNonInteractiveCall{workDir, prompt, agentName, shutdown})
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

func TestInvokeBackendDispatches(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude"}
	RegisterBackend(m)

	err := InvokeBackend("/work", "hello", "agent1")
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

func TestInvokeBackendReturnsError(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude", interactiveErr: fmt.Errorf("invoke failed")}
	RegisterBackend(m)

	err := InvokeBackend("/work", "hello", "")
	if err == nil || err.Error() != "invoke failed" {
		t.Fatalf("expected 'invoke failed', got %v", err)
	}
}

func TestInvokeBackendUnregistered(t *testing.T) {
	resetBackendState(t)

	// activeBackend is "claude" but nothing is registered
	err := InvokeBackend("/work", "hello", "")
	if err == nil {
		t.Fatal("expected error for unregistered backend")
	}
	if got := err.Error(); got != `backend "claude" not registered` {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestInvokeBackendNonInteractiveDispatches(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude"}
	RegisterBackend(m)

	shutdown := make(chan struct{})
	err := InvokeBackendNonInteractive("/work", "task", "agent2", shutdown)
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

func TestInvokeBackendNonInteractiveReturnsError(t *testing.T) {
	resetBackendState(t)

	m := &mockBackend{name: "claude", nonInteractiveErr: fmt.Errorf("non-interactive failed")}
	RegisterBackend(m)

	err := InvokeBackendNonInteractive("/work", "task", "", nil)
	if err == nil || err.Error() != "non-interactive failed" {
		t.Fatalf("expected 'non-interactive failed', got %v", err)
	}
}

func TestInvokeBackendNonInteractiveUnregistered(t *testing.T) {
	resetBackendState(t)

	err := InvokeBackendNonInteractive("/work", "task", "", nil)
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
