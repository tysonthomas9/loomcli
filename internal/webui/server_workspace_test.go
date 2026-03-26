package webui

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestWrapWorkspaceCreateFn_NilInner(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	wrapped := wrapWorkspaceCreateFn(nil, registry, nil, nil)
	if wrapped != nil {
		t.Fatal("expected nil wrapper when innerCreate is nil")
	}
}

func TestWrapWorkspaceCreateFn_ResolveIDNil_AbortsRegistration(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

	var innerCalled bool
	innerCreate := func(ctx context.Context, req WorkspaceCreateRequest) error {
		innerCalled = true
		return nil
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry, nil, nil)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}

	err := wrapped(context.Background(), WorkspaceCreateRequest{Name: "my-ws"})
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

func TestWrapWorkspaceCreateFn_ResolveIDFails_AbortsRegistration(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

	var innerCalled bool
	innerCreate := func(ctx context.Context, req WorkspaceCreateRequest) error {
		innerCalled = true
		return nil
	}

	resolveID := func(name string) (string, error) {
		return "", fmt.Errorf("config not readable")
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry, resolveID, nil)
	err := wrapped(context.Background(), WorkspaceCreateRequest{Name: "my-ws"})
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

func TestWrapWorkspaceCreateFn_ResolveIDEmpty_AbortsRegistration(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

	innerCreate := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return nil
	}

	resolveID := func(name string) (string, error) {
		return "", nil // empty string, no error
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry, resolveID, nil)
	err := wrapped(context.Background(), WorkspaceCreateRequest{Name: "my-ws"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// No registration should have happened.
	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs in MultiPool, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceCreateFn_ResolveIDSucceeds_RegistersByUUID(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsUUID := "eeeeeeee-1111-2222-3333-444444444444"
	wsName := "new-workspace"
	wsPath := t.TempDir()

	innerCreate := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return nil
	}

	resolveID := func(name string) (string, error) {
		if name == wsName {
			return wsUUID, nil
		}
		return "", fmt.Errorf("unknown workspace %q", name)
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry, resolveID, nil)
	err := wrapped(context.Background(), WorkspaceCreateRequest{Name: wsName, Path: wsPath})
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
	innerCreate := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return createErr
	}

	resolveID := func(name string) (string, error) {
		t.Error("resolveID should not be called when innerCreate fails")
		return "some-uuid", nil
	}

	wrapped := wrapWorkspaceCreateFn(innerCreate, registry, resolveID, nil)
	err := wrapped(context.Background(), WorkspaceCreateRequest{Name: "my-ws"})
	if err != createErr {
		t.Fatalf("expected createErr, got %v", err)
	}

	if ids := multiPool.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs after inner failure, got %d: %v", len(ids), ids)
	}
}

func TestWrapWorkspaceDeleteFn_NilInner(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	wrapped := wrapWorkspaceDeleteFn(nil, registry, nil, nil)
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

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, resolveID)
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

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, resolveID)
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
	wrapped := wrapWorkspaceDeleteFn(innerDelete, nil, nil, nil)
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
	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, resolveID)
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
	registry, multiPool, _ := newTestRegistry(t)

	wsName := "unresolvable-workspace"

	// Register with the name as key (simulating fallback behavior).
	wsPath := t.TempDir()
	if err := registry.Register(wsName, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	innerDelete := func(name string) error {
		return nil
	}

	// resolveID returns an error; wrapper should fall back to using the workspace name.
	resolveID := func(name string) (string, error) {
		return "", fmt.Errorf("config not found")
	}

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, resolveID)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	// When UUID resolution fails, the wrapper uses the name as deregistration key.
	// Since we registered with the name, the pool should now be removed.
	if multiPool.PoolForWorkspace(wsName) != nil {
		t.Error("expected pool to be deregistered using workspace name as fallback key")
	}
}

func TestWrapWorkspaceDeleteFn_NilResolveID(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

	wsName := "nil-resolver-workspace"

	// Register with the name as key (simulating no UUID available).
	wsPath := t.TempDir()
	if err := registry.Register(wsName, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	innerDelete := func(name string) error {
		return nil
	}

	// Pass nil resolveID. Wrapper should use the workspace name directly.
	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, nil)
	if err := wrapped(wsName); err != nil {
		t.Fatalf("wrapped delete returned error: %v", err)
	}

	// Pool should be deregistered using the name (fallback when resolveID is nil).
	if multiPool.PoolForWorkspace(wsName) != nil {
		t.Error("expected pool to be deregistered using workspace name when resolveID is nil")
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

	wrapped := wrapWorkspaceDeleteFn(innerDelete, registry, nil, resolveID)
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
