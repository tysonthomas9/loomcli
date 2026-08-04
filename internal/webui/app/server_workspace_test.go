package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// newTestRegistry creates a coordinator.WorkspaceRegistry with real hooks and
// supporting infrastructure for testing. Returns the registry, the underlying
// observable hook state. Cleanup is registered automatically.
func newTestRegistry(t *testing.T) (*coordinator.WorkspaceRegistry, *testRegistryState) {
	t.Helper()
	state := &testRegistryState{registered: map[string]int64{}}

	reg := coordinator.NewWorkspaceRegistry(slog.Default())
	_ = reg.AddHook(&testPoolHook{state: state})
	t.Cleanup(func() { _ = reg.Close() })

	return reg, state
}

type testPoolHook struct {
	state *testRegistryState
}

func (h *testPoolHook) Name() string   { return "test-pool" }
func (h *testPoolHook) Critical() bool { return true }
func (h *testPoolHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	h.state.register(ctx.WorkspaceID)
	return nil
}
func (h *testPoolHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.state.deregister(ctx.WorkspaceID)
}
func (h *testPoolHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}

type testRegistryState struct {
	mu         sync.Mutex
	registered map[string]int64
	next       int64
}

func (s *testRegistryState) register(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.registered[id] = s.next
}
func (s *testRegistryState) deregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registered, id)
}
func (s *testRegistryState) WorkspaceIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.registered))
	for id := range s.registered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (s *testRegistryState) PoolForWorkspace(id string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation, ok := s.registered[id]; ok {
		return generation
	}
	return nil
}
func (s *testRegistryState) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registered = map[string]int64{}
}

func TestCreateWarnings_ContextHelpers(t *testing.T) {
	t.Run("full lifecycle", func(t *testing.T) {
		ctx := context.Background()
		ctx = service.WithCreateWarnings(ctx)

		// Initially no warnings
		if w := service.GetCreateWarnings(ctx); w != nil {
			t.Fatalf("expected nil warnings initially, got %v", w)
		}

		// Add a warning
		service.AddCreateWarning(ctx, "warning one")
		warnings := service.GetCreateWarnings(ctx)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
		}
		if warnings[0] != "warning one" {
			t.Errorf("expected %q, got %q", "warning one", warnings[0])
		}

		// Add another warning
		service.AddCreateWarning(ctx, "warning two")
		warnings = service.GetCreateWarnings(ctx)
		if len(warnings) != 2 {
			t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
		}
		if warnings[1] != "warning two" {
			t.Errorf("expected %q, got %q", "warning two", warnings[1])
		}
	})

	t.Run("AddCreateWarning is no-op on plain context", func(t *testing.T) {
		ctx := context.Background()
		// Should not panic
		service.AddCreateWarning(ctx, "should be ignored")

		// GetCreateWarnings returns nil on plain context
		if w := service.GetCreateWarnings(ctx); w != nil {
			t.Errorf("expected nil from plain context, got %v", w)
		}
	})

	t.Run("GetCreateWarnings returns nil on plain context", func(t *testing.T) {
		ctx := context.Background()
		if w := service.GetCreateWarnings(ctx); w != nil {
			t.Errorf("expected nil from plain context, got %v", w)
		}
	})
}

func TestWrapWorkspaceCreateFn_CollectsWarnings(t *testing.T) {
	registry, _ := newTestRegistry(t)

	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{}, nil
	}

	// Empty WorkspaceID triggers a warning in the wrapped function
	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}

	ctx := service.WithCreateWarnings(context.Background())
	_, err := wrapped(ctx, service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	warnings := service.GetCreateWarnings(ctx)
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning from empty WorkspaceID path")
	}

	// Verify the warning identifies runtime registration.
	found := false
	for _, w := range warnings {
		if w == "Could not register workspace runtime — workspace may not auto-connect until restart" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected daemon registration warning, got: %v", warnings)
	}
}

func TestWrapWorkspaceCreateFn_NilInner(t *testing.T) {
	registry, _ := newTestRegistry(t)

	wrapped := wrapWorkspaceCreateFn(nil, registry)
	if wrapped != nil {
		t.Fatal("expected nil wrapper when innerCreate is nil")
	}
}

func TestWrapWorkspaceCreateFn_EmptyWorkspaceID_AbortsRegistration(t *testing.T) {
	registry, multiPool := newTestRegistry(t)

	var innerCalled bool
	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		innerCalled = true
		return service.WorkspaceCreateResult{}, nil // empty WorkspaceID
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}

	_, err := wrapped(context.Background(), service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !innerCalled {
		t.Error("expected innerCreate to be called")
	}

	// No registration should have happened.
	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs in MultiPool, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceCreateFn_EmptyWorkspaceID_NoError_AbortsRegistration(t *testing.T) {
	registry, multiPool := newTestRegistry(t)

	var innerCalled bool
	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		innerCalled = true
		return service.WorkspaceCreateResult{WorkspaceID: ""}, nil // empty ID, no error
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	_, err := wrapped(context.Background(), service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !innerCalled {
		t.Error("expected innerCreate to be called")
	}

	// No registration should have happened.
	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs in MultiPool, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceCreateFn_ZeroResult_AbortsRegistration(t *testing.T) {
	registry, multiPool := newTestRegistry(t)

	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{}, nil // zero-value result
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	_, err := wrapped(context.Background(), service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// No registration should have happened.
	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs in MultiPool, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceCreateFn_ResultWithID_RegistersByUUID(t *testing.T) {
	registry, multiPool := newTestRegistry(t)

	wsUUID := "eeeeeeee-1111-2222-3333-444444444444"
	wsName := "new-workspace"
	wsPath := t.TempDir()

	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{WorkspaceID: wsUUID, WorkspacePath: wsPath}, nil
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	_, err := wrapped(context.Background(), service.WorkspaceCreateRequest{Name: wsName, Path: wsPath})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Verify registered under UUID.
	poolIDs := multiPool.WorkspaceIDs()
	if len(poolIDs) != 1 {
		t.Fatalf("expected 1 workspace ID in MultiPool, got %d: %v", len(poolIDs), poolIDs)
	}
	if poolIDs[0] != wsUUID {
		t.Errorf("expected pool keyed by UUID %q, got %q", wsUUID, poolIDs[0])
	}

	// Verify NOT registered under name.
	if multiPool.PoolForWorkspace(wsName) != nil {
		t.Error("workspace should NOT be registered under name key")
	}

}

func TestWrapWorkspaceCreateFn_InnerCreateFails_NoRegistration(t *testing.T) {
	registry, multiPool := newTestRegistry(t)

	createErr := fmt.Errorf("disk full")
	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{}, createErr
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)
	_, err := wrapped(context.Background(), service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != createErr {
		t.Fatalf("expected createErr, got %v", err)
	}

	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs after inner failure, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceCreateFn_RegisterFails_SurfacesWarning(t *testing.T) {
	registry, _ := newTestRegistry(t)

	wsUUID := "aaaaaaaa-2222-3333-4444-555555555555"
	wsPath := t.TempDir()

	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{WorkspaceID: wsUUID, WorkspacePath: wsPath}, nil
	}

	// A closed registry rejects new runtime registrations.
	if err := registry.Close(); err != nil {
		t.Fatalf("close registry: %v", err)
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)

	ctx := service.WithCreateWarnings(context.Background())
	_, err := wrapped(ctx, service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error (workspace creation succeeded), got %v", err)
	}

	warnings := service.GetCreateWarnings(ctx)
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning from Register failure")
	}

	found := false
	for _, w := range warnings {
		if w == "Workspace created but runtime registration failed — some features may be unavailable until restart" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected runtime registration failure warning, got: %v", warnings)
	}
}

func TestWrapWorkspaceCreateFn_RegisterFails_PlainContext_NoPanic(t *testing.T) {
	registry, _ := newTestRegistry(t)

	wsUUID := "bbbbbbbb-2222-3333-4444-555555555555"
	wsPath := t.TempDir()

	innerCreate := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{WorkspaceID: wsUUID, WorkspacePath: wsPath}, nil
	}

	// A closed registry rejects new runtime registrations.
	if err := registry.Close(); err != nil {
		t.Fatalf("close registry: %v", err)
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry)

	// Use plain context without WithCreateWarnings — should not panic.
	ctx := context.Background()
	_, err := wrapped(ctx, service.WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error (workspace creation succeeded), got %v", err)
	}

	// No warnings context — GetCreateWarnings should return nil.
	if w := service.GetCreateWarnings(ctx); w != nil {
		t.Errorf("expected nil warnings from plain context, got %v", w)
	}
}

func TestWrapWorkspaceDeleteCleanupFnUsesCanonicalKey(t *testing.T) {
	registry, state := newTestRegistry(t)
	const key = "ALPHA"
	if err := registry.Register(key, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cleaned := ""
	cleanup := wrapWorkspaceDeleteCleanupFn(func(value string) error {
		cleaned = value
		return nil
	}, registry)
	if err := cleanup(key); err != nil {
		t.Fatal(err)
	}
	if cleaned != key || state.PoolForWorkspace(key) != nil {
		t.Fatalf("cleaned=%q registered=%v", cleaned, state.PoolForWorkspace(key))
	}
}
