package webui

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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
	registry, _, _ := newTestRegistry(t)

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

	// Verify the warning mentions daemon/registration
	found := false
	for _, w := range warnings {
		if w == "Could not register workspace with daemon — workspace may not auto-connect until restart" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected daemon registration warning, got: %v", warnings)
	}
}

func TestWrapWorkspaceCreateFn_NilInner(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	wrapped := wrapWorkspaceCreateFn(nil, registry)
	if wrapped != nil {
		t.Fatal("expected nil wrapper when innerCreate is nil")
	}
}

func TestWrapWorkspaceCreateFn_EmptyWorkspaceID_AbortsRegistration(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

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
	registry, multiPool, _ := newTestRegistry(t)

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
	registry, multiPool, _ := newTestRegistry(t)

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
	registry, multiPool, multiSub := newTestRegistry(t)

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

	// Verify subscriber registered.
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d: %v", len(subIDs), subIDs)
	}
	if subIDs[0] != wsUUID {
		t.Errorf("expected subscriber keyed by UUID %q, got %q", wsUUID, subIDs[0])
	}
}

func TestWrapWorkspaceCreateFn_InnerCreateFails_NoRegistration(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

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

func TestWrapWorkspaceDeleteFn_NilInner(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	wrapped := wrapWorkspaceDeleteFn(nil, registry, nil)
	if wrapped != nil {
		t.Fatal("expected nil wrapper when innerDelete is nil")
	}
}

func TestWrapWorkspaceDeleteFn_Success(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	// Register a workspace so we can verify deregistration.
	wsID := "aaaaaaaa-1111-2222-3333-444444444444"
	wsName := "my-workspace"
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Sanity check: workspace is registered.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Fatal("expected pool to be registered before delete")
	}
	if len(multiSub.WorkspaceIDs()) != 1 {
		t.Fatal("expected subscriber to have 1 workspace before delete")
	}

	var innerCalled bool
	innerDelete := func(name string) error {
		innerCalled = true
		if name != wsName {
			t.Errorf("expected innerDelete called with %q, got %q", wsName, name)
		}
		return nil
	}

	resolveID := func(name string) (string, error) {
		if name == wsName {
			return wsID, nil
		}
		return "", fmt.Errorf("unknown workspace %q", name)
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}

	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if !innerCalled {
		t.Error("expected innerDelete to be called")
	}

	// Verify registry.Deregister was called (pool and subscriber removed).
	if multiPool.PoolForWorkspace(wsID) != nil {
		t.Error("expected pool to be deregistered after delete")
	}
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 0 {
		t.Errorf("expected 0 subscriber IDs after delete, got %d: %v", len(subIDs), subIDs)
	}
}

func TestWrapWorkspaceDeleteFn_InnerFailSkipsCleanup(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "bbbbbbbb-1111-2222-3333-444444444444"
	wsName := "fail-workspace"
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	deleteErr := fmt.Errorf("permission denied")
	innerDelete := func(name string) error {
		return deleteErr
	}

	resolveID := func(name string) (string, error) {
		return wsID, nil
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	err := wrapped(wsName)
	if err == nil {
		t.Fatal("expected error from wrapped delete")
	}
	if err != deleteErr {
		t.Errorf("expected error %v, got %v", deleteErr, err)
	}

	// Verify NO cleanup happened: pool and subscriber should still be present.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pool to still be registered after inner delete failure")
	}
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 {
		t.Errorf("expected 1 subscriber ID after inner delete failure, got %d: %v", len(subIDs), subIDs)
	}
}

func TestWrapWorkspaceDeleteFn_NilRegistry(t *testing.T) {
	var innerCalled bool
	innerDelete := func(name string) error {
		innerCalled = true
		return nil
	}

	// Both registry and fleetRegistry are nil. Should not panic.
	wrapped := wrapWorkspaceDeleteFn(innerDelete, nil, nil)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}

	if err := wrapped("some-ws"); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if !innerCalled {
		t.Error("expected innerDelete to be called")
	}
}

func TestWrapWorkspaceDeleteFn_NilFleetRegistry(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "cccccccc-1111-2222-3333-444444444444"
	wsName := "fleet-nil-workspace"
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	innerDelete := func(name string) error {
		return nil
	}

	resolveID := func(name string) (string, error) {
		return wsID, nil
	}

	// Valid registry, nil fleetRegistry. Should clean up pool but not panic on nil fleet.
	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	// Verify pool cleanup happened.
	if multiPool.PoolForWorkspace(wsID) != nil {
		t.Error("expected pool to be deregistered after delete")
	}
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 0 {
		t.Errorf("expected 0 subscriber IDs after delete, got %d: %v", len(subIDs), subIDs)
	}
}

func TestWrapWorkspaceDeleteFn_UUIDResolutionFails(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "eeeeeeee-1111-2222-3333-444444444444"
	wsName := "unresolvable-workspace"

	// Register under UUID (production behavior).
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	var innerCalled bool
	innerDelete := func(name string) error {
		innerCalled = true
		return nil
	}

	// resolveID returns an error; cleanup should be skipped (not called with name).
	resolveID := func(name string) (string, error) {
		return "", fmt.Errorf("config not found")
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if !innerCalled {
		t.Error("expected innerDelete to be called even when UUID resolution fails")
	}

	// Cleanup was skipped — pool should still be registered under the UUID (leak accepted).
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pool to still be registered (cleanup skipped on resolution failure)")
	}
	if len(multiSub.WorkspaceIDs()) != 1 {
		t.Error("expected subscriber to still have 1 workspace (cleanup skipped)")
	}
}

func TestWrapWorkspaceDeleteFn_NilResolveID(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "ffffffff-1111-2222-3333-444444444444"
	wsName := "nil-resolver-workspace"

	// Register under UUID (production behavior).
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	var innerCalled bool
	innerDelete := func(name string) error {
		innerCalled = true
		return nil
	}

	// Pass nil resolveID. Cleanup should be skipped entirely.
	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if !innerCalled {
		t.Error("expected innerDelete to be called even when resolveID is nil")
	}

	// Cleanup was skipped — pool should still be registered under the UUID (leak accepted).
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pool to still be registered (cleanup skipped when resolveID is nil)")
	}
	if len(multiSub.WorkspaceIDs()) != 1 {
		t.Error("expected subscriber to still have 1 workspace (cleanup skipped)")
	}
}

func TestWrapWorkspaceDeleteFn_ResolveIDEmptyString(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "11111111-aaaa-bbbb-cccc-dddddddddddd"
	wsName := "empty-resolve-workspace"

	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	var innerCalled bool
	innerDelete := func(name string) error {
		innerCalled = true
		return nil
	}

	// resolveID returns ("", nil) — empty string is not a valid UUID.
	resolveID := func(name string) (string, error) {
		return "", nil
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if !innerCalled {
		t.Error("expected innerDelete to be called")
	}

	// Cleanup should be skipped — empty string is not a valid UUID.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pool to still be registered (cleanup skipped on empty resolve)")
	}
	if len(multiSub.WorkspaceIDs()) != 1 {
		t.Error("expected subscriber to still have 1 workspace (cleanup skipped)")
	}
}

func TestWrapWorkspaceDeleteFn_SkipsCleanupOnResolutionFailure(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "22222222-aaaa-bbbb-cccc-dddddddddddd"
	wsName := "skip-cleanup-workspace"

	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	var innerCalledWith string
	innerDelete := func(name string) error {
		innerCalledWith = name
		return nil
	}

	// resolveID fails.
	resolveID := func(name string) (string, error) {
		return "", fmt.Errorf("workspace config corrupted")
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	err := wrapped(wsName)

	// Function should return nil (delete succeeded, leak is logged not returned).
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// innerDelete should have been called with the workspace name.
	if innerCalledWith != wsName {
		t.Errorf("expected innerDelete called with %q, got %q", wsName, innerCalledWith)
	}

	// Pool should still be registered — cleanup was skipped.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pool to still be registered under UUID (cleanup skipped)")
	}
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 {
		t.Errorf("expected 1 subscriber ID (cleanup skipped), got %d: %v", len(subIDs), subIDs)
	}
}

func TestWrapWorkspaceDeleteFn_UUIDResolvedBeforeDelete(t *testing.T) {
	// This test verifies that resolveID is called BEFORE innerDelete.
	// This ordering is critical because innerDelete removes the config entry,
	// which would make UUID resolution impossible after the fact.
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	t.Cleanup(func() { multiSub.Stop() })

	registry := NewWorkspaceRegistry(multiPool, multiSub, 10, slog.Default())

	wsID := "dddddddd-1111-2222-3333-444444444444"
	wsName := "order-test-workspace"
	wsPath := t.TempDir()
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Track call ordering with a monotonic counter.
	var seq atomic.Int64
	var resolveSeq, deleteSeq int64

	resolveID := func(name string) (string, error) {
		resolveSeq = seq.Add(1)
		return wsID, nil
	}

	innerDelete := func(name string) error {
		deleteSeq = seq.Add(1)
		return nil
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, resolveID)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	if resolveSeq == 0 {
		t.Fatal("resolveID was never called")
	}
	if deleteSeq == 0 {
		t.Fatal("innerDelete was never called")
	}
	if resolveSeq >= deleteSeq {
		t.Errorf("resolveID (seq=%d) was called AFTER innerDelete (seq=%d); must be called before",
			resolveSeq, deleteSeq)
	}
}
