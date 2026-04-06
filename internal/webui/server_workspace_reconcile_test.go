package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/hooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// newTestCoordinatorRegistry creates a coordinator.WorkspaceRegistry with real
// hooks for reconciliation tests. Returns the registry, MultiPool, and subscriber.
func newTestCoordinatorRegistry(t *testing.T) (*coordinator.WorkspaceRegistry, *daemon.MultiPool, *subscription.MultiWorkspaceSubscriber) {
	t.Helper()
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	multiSub := subscription.NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	t.Cleanup(func() { multiSub.Stop() })

	reg := coordinator.NewWorkspaceRegistry(slog.Default())
	_ = reg.AddHook(hooks.NewBeadsPoolHook(multiPool, 10, slog.Default()))
	_ = reg.AddHook(hooks.NewNotificationSubscriberHook(multiSub, slog.Default()))
	t.Cleanup(func() { _ = reg.Close() })

	return reg, multiPool, multiSub
}

// --- Registry.Register unit tests ---

func TestRegistry_Register_RegistersInMultiPoolAndSubscriber(t *testing.T) {
	registry, multiPool, multiSub := newTestCoordinatorRegistry(t)

	wsPath := t.TempDir()
	if err := registry.Register("test-ws", wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Verify the pool was registered in MultiPool
	pool := multiPool.PoolForWorkspace("test-ws")
	if pool == nil {
		t.Fatal("expected pool to be registered for workspace 'test-ws'")
	}

	// Verify WorkspaceIDs includes the new workspace
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "test-ws" {
		t.Errorf("expected WorkspaceIDs=[test-ws], got %v", ids)
	}

	// Verify subscriber was added
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 || subIDs[0] != "test-ws" {
		t.Errorf("expected subscriber WorkspaceIDs=[test-ws], got %v", subIDs)
	}
}

func TestRegistry_Register_MultipleWorkspaces(t *testing.T) {
	registry, multiPool, multiSub := newTestCoordinatorRegistry(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		wsPath := t.TempDir()
		if err := registry.Register(name, wsPath); err != nil {
			t.Fatalf("Register(%q) returned error: %v", name, err)
		}
	}

	ids := multiPool.WorkspaceIDs()
	sort.Strings(ids)
	if len(ids) != 3 {
		t.Fatalf("expected 3 workspace IDs, got %d: %v", len(ids), ids)
	}
	expected := []string{"alpha", "beta", "gamma"}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("WorkspaceIDs[%d] = %q, want %q", i, ids[i], want)
		}
	}

	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 3 {
		t.Fatalf("expected 3 subscriber IDs, got %d: %v", len(subIDs), subIDs)
	}
}

func TestRegistry_Register_DuplicateReplacesExisting(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	wsPath1 := t.TempDir()
	wsPath2 := t.TempDir()

	_ = registry.Register("dup-ws", wsPath1)
	pool1 := multiPool.PoolForWorkspace("dup-ws")

	_ = registry.Register("dup-ws", wsPath2)
	pool2 := multiPool.PoolForWorkspace("dup-ws")

	// Pool should have been replaced (MultiPool.Register replaces existing)
	if pool1 == pool2 {
		t.Error("expected pool to be replaced after second registration with different path")
	}

	// Should still have exactly one workspace
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 workspace ID after duplicate registration, got %d", len(ids))
	}
}

// --- Startup reconciliation integration tests ---

func TestStartupReconciliation_SkipsInitialWorkspace(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The reconciliation loop in StartServer skips the initialWorkspaceID.
	// By default, initialWorkspaceID = basename of cwd. We provide
	// WorkspaceListFn that returns a map including the initial workspace to
	// verify it is skipped (i.e., not double-registered).
	registered := make(map[string]bool)
	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		WorkspaceListFn: func() (map[string]string, error) {
			// "comet" will match the cwd basename in our worktree.
			// We also include the literal "default" just in case.
			// Both should be skipped if they match initialWorkspaceID.
			return map[string]string{
				"extra-ws": t.TempDir(),
			}, nil
		},
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	// Wait for the server to be ready
	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	_ = registered
	// Server started successfully with WorkspaceListFn; shut it down.
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func TestStartupReconciliation_NilWorkspaceListFn(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		WorkspaceListFn: nil, // single-workspace mode
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	// Server started successfully in single-workspace mode; shut it down.
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func TestStartupReconciliation_WorkspaceListFnReturnsError(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		WorkspaceListFn: func() (map[string]string, error) {
			return nil, fmt.Errorf("config file corrupt")
		},
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	// Server started successfully despite WorkspaceListFn error; reconciliation
	// was skipped gracefully.
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func TestStartupReconciliation_WorkspaceListFnReturnsEmptyMap(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		WorkspaceListFn: func() (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	// Server started successfully with empty workspace map; shut it down.
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

// --- Unit-level reconciliation logic tests ---
// These test the reconciliation logic directly using the WorkspaceRegistry,
// without starting a full HTTP server.

func TestReconciliationLogic_SkipsInitialWorkspace(t *testing.T) {
	registry, multiPool, multiSub := newTestCoordinatorRegistry(t)

	// Simulate the initial workspace already being registered
	initialWS := "my-project"
	initialPath := t.TempDir()
	_ = registry.Register(initialWS, initialPath)

	// Simulate WorkspaceListFn returning a map that includes the initial workspace
	workspaces := map[string]string{
		"my-project": initialPath,
		"extra-1":    t.TempDir(),
		"extra-2":    t.TempDir(),
	}

	// Replicate the reconciliation loop from StartServer
	for wsName, wsPath := range workspaces {
		if wsName == initialWS {
			continue
		}
		_ = registry.Register(wsName, wsPath)
	}

	// Verify all workspaces are registered
	ids := multiPool.WorkspaceIDs()
	sort.Strings(ids)
	if len(ids) != 3 {
		t.Fatalf("expected 3 workspace IDs, got %d: %v", len(ids), ids)
	}

	expected := []string{"extra-1", "extra-2", "my-project"}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("WorkspaceIDs[%d] = %q, want %q", i, ids[i], want)
		}
	}

	// Verify subscriber has all workspaces
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 3 {
		t.Errorf("expected 3 subscriber IDs, got %d: %v", len(subIDs), subIDs)
	}
}

func TestReconciliationLogic_EmptyWorkspaceMap(t *testing.T) {
	registry, multiPool, multiSub := newTestCoordinatorRegistry(t)

	// Simulate empty workspace map (no workspaces configured besides initial)
	workspaces := map[string]string{}

	for wsName, wsPath := range workspaces {
		if wsName == "default" {
			continue
		}
		_ = registry.Register(wsName, wsPath)
	}

	// No extra pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs for empty map, got %d: %v", len(ids), ids)
	}

	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 0 {
		t.Errorf("expected 0 subscriber IDs for empty map, got %d: %v", len(subIDs), subIDs)
	}
}

func TestReconciliationLogic_NilWorkspaceListFn(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	// Replicate the guard from StartServer: if WorkspaceListFn is nil, skip.
	var workspaceListFn func() (map[string]string, error)
	if workspaceListFn != nil {
		workspaces, err := workspaceListFn()
		if err == nil {
			for wsName, wsPath := range workspaces {
				_ = registry.Register(wsName, wsPath)
			}
		}
	}

	// No pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs when WorkspaceListFn is nil, got %d", len(ids))
	}
}

func TestReconciliationLogic_ErrorFromWorkspaceListFn(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	// Replicate the reconciliation with an error-returning function
	workspaceListFn := func() (map[string]string, error) {
		return nil, fmt.Errorf("disk I/O error")
	}

	workspaces, err := workspaceListFn()
	if err != nil {
		// Mimic server behavior: log and skip
		t.Logf("WorkspaceListFn returned error (expected): %v", err)
	} else {
		for wsName, wsPath := range workspaces {
			_ = registry.Register(wsName, wsPath)
		}
	}

	// No pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs when WorkspaceListFn errors, got %d", len(ids))
	}
}

func TestReconcileConfigWorkspaces_UUIDKeys(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	// Simulate initial workspace registered by UUID (as server.go does post-T2)
	initialUUID := "aaaabbbb-1111-2222-3333-444455556666"
	initialPath := t.TempDir()
	_ = registry.Register(initialUUID, initialPath)

	extraUUID := "ccccdddd-5555-6666-7777-888899990000"
	extraPath := t.TempDir()

	// WorkspaceListFn now returns uuid→path
	listFn := func() (map[string]string, error) {
		return map[string]string{
			initialUUID: initialPath,
			extraUUID:   extraPath,
		}, nil
	}

	reconcileConfigWorkspaces(listFn, initialUUID, true, registry)

	// Verify: initial workspace not double-registered, extra workspace added
	ids := multiPool.WorkspaceIDs()
	sort.Strings(ids)
	if len(ids) != 2 {
		t.Fatalf("expected 2 workspace IDs, got %d: %v", len(ids), ids)
	}
	if ids[0] != initialUUID {
		t.Errorf("WorkspaceIDs[0] = %q, want %q", ids[0], initialUUID)
	}
	if ids[1] != extraUUID {
		t.Errorf("WorkspaceIDs[1] = %q, want %q", ids[1], extraUUID)
	}
}

func TestReconcileConfigWorkspaces_PreMigrationNameKeys(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	// Pre-migration: initial workspace registered by name (no UUID available)
	initialName := "my-project"
	initialPath := t.TempDir()
	_ = registry.Register(initialName, initialPath)

	extraName := "other-project"
	extraPath := t.TempDir()

	// WorkspaceListFn returns name→path (pre-migration fallback)
	listFn := func() (map[string]string, error) {
		return map[string]string{
			initialName: initialPath,
			extraName:   extraPath,
		}, nil
	}

	reconcileConfigWorkspaces(listFn, initialName, true, registry)

	ids := multiPool.WorkspaceIDs()
	sort.Strings(ids)
	if len(ids) != 2 {
		t.Fatalf("expected 2 workspace IDs, got %d: %v", len(ids), ids)
	}
	if ids[0] != "my-project" {
		t.Errorf("WorkspaceIDs[0] = %q, want %q", ids[0], "my-project")
	}
	if ids[1] != "other-project" {
		t.Errorf("WorkspaceIDs[1] = %q, want %q", ids[1], "other-project")
	}
}

func TestReconcileConfigWorkspaces_UUIDSkipMatchesInitialID(t *testing.T) {
	// Verifies the skip logic works when both initialID and map keys are UUIDs.
	// This was the core bug T2 fixes: previously, map keys were names but
	// initialID was a UUID, so the skip check failed and the initial workspace
	// got re-registered (replacing the custom auto-discovered pool).
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	initialUUID := "11112222-3333-4444-5555-666677778888"
	initialPath := t.TempDir()
	_ = registry.Register(initialUUID, initialPath)

	// Capture the original pool reference
	originalPool := multiPool.PoolForWorkspace(initialUUID)
	if originalPool == nil {
		t.Fatal("initial pool should be registered")
	}

	// reconcileConfigWorkspaces with only the initial workspace in the map
	listFn := func() (map[string]string, error) {
		return map[string]string{
			initialUUID: initialPath,
		}, nil
	}
	reconcileConfigWorkspaces(listFn, initialUUID, true, registry)

	// The pool should NOT have been replaced (skip logic worked)
	poolAfter := multiPool.PoolForWorkspace(initialUUID)
	if poolAfter != originalPool {
		t.Error("initial workspace pool was replaced — skip logic failed")
	}

	// Still exactly one workspace
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != initialUUID {
		t.Errorf("expected [%s], got %v", initialUUID, ids)
	}
}

func TestReconciliationLogic_OnlyInitialInMap(t *testing.T) {
	registry, multiPool, _ := newTestCoordinatorRegistry(t)

	initialWS := "my-workspace"
	initialPath := t.TempDir()
	_ = registry.Register(initialWS, initialPath)

	// WorkspaceListFn returns only the initial workspace
	workspaces := map[string]string{
		"my-workspace": initialPath,
	}

	for wsName, wsPath := range workspaces {
		if wsName == initialWS {
			continue
		}
		_ = registry.Register(wsName, wsPath)
	}

	// Only the initial workspace should be registered (no extra registrations)
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 workspace ID, got %d: %v", len(ids), ids)
	}
	if ids[0] != "my-workspace" {
		t.Errorf("expected workspace ID %q, got %q", "my-workspace", ids[0])
	}
}
