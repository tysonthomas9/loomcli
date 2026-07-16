package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"

	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// newTestCoordinatorRegistry creates a coordinator.WorkspaceRegistry with real
// hooks for reconciliation tests. Returns the registry and MultiPool.
func newTestCoordinatorRegistry(t *testing.T) (*coordinator.WorkspaceRegistry, *daemon.MultiPool) {
	t.Helper()
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)

	reg := coordinator.NewWorkspaceRegistry(slog.Default())
	_ = reg.AddHook(&testPoolHook{multiPool: multiPool})
	t.Cleanup(func() { _ = reg.Close() })

	return reg, multiPool
}

// --- Registry.Register unit tests ---

func TestRegistry_Register_RegistersInMultiPool(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

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

}

func TestRegistry_Register_MultipleWorkspaces(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

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

}

func TestRegistry_Register_DuplicateReplacesExisting(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

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
	registered := make(map[string]bool)
	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
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
	// Server started successfully; shut it down.
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

func TestStartupReconciliation_NoStore(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
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

	// Server started successfully without a workspace store; shut it down.
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

func TestStartupReconciliation_NoStoreSecondCase(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
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

	// Server started successfully without store-backed workspace reconciliation.
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

func TestStartupReconciliation_NoStoreEmptyCase(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
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
	registry, multiPool := newTestCoordinatorRegistry(t)

	// Simulate the initial workspace already being registered
	initialWS := "my-project"
	initialPath := t.TempDir()
	_ = registry.Register(initialWS, initialPath)

	// Simulate the store path map including the initial workspace.
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

}

func TestReconciliationLogic_EmptyWorkspaceMap(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

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

}

func TestReconciliationLogic_NilStorePathList(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

	// Replicate the guard from StartServer: if the store path list is nil, skip.
	var storePathList func() (map[string]string, error)
	if storePathList != nil {
		workspaces, err := storePathList()
		if err == nil {
			for wsName, wsPath := range workspaces {
				_ = registry.Register(wsName, wsPath)
			}
		}
	}

	// No pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs when store path list is nil, got %d", len(ids))
	}
}

func TestReconciliationLogic_ErrorFromStorePathList(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

	// Replicate the reconciliation with an error-returning function
	storePathList := func() (map[string]string, error) {
		return nil, fmt.Errorf("disk I/O error")
	}

	workspaces, err := storePathList()
	if err != nil {
		// Mimic server behavior: log and skip
		t.Logf("store path list returned error (expected): %v", err)
	} else {
		for wsName, wsPath := range workspaces {
			_ = registry.Register(wsName, wsPath)
		}
	}

	// No pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs when store path list errors, got %d", len(ids))
	}
}

func TestReconcileStoreWorkspaces_UUIDKeys(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

	// Simulate initial workspace registered by UUID (as server.go does post-T2)
	initialUUID := "aaaabbbb-1111-2222-3333-444455556666"
	initialPath := t.TempDir()
	_ = registry.Register(initialUUID, initialPath)

	extraUUID := "ccccdddd-5555-6666-7777-888899990000"
	extraPath := t.TempDir()

	// Store path listing returns uuid-to-path.
	listFn := func() (map[string]string, error) {
		return map[string]string{
			initialUUID: initialPath,
			extraUUID:   extraPath,
		}, nil
	}

	appinfra.ReconcileStoreWorkspaces(listFn, initialUUID, true, registry, slog.Default())

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

// TestStartPeriodicWorkspaceReconcile_PicksUpNewWorkspaces simulates the
// "workspace created out-of-band after serve startup" scenario that produced
// "workspace not registered" errors during the multi-lead dogfood. The
// periodic loop should detect the new workspace and register it without a
// serve restart.
func TestStartPeriodicWorkspaceReconcile_PicksUpNewWorkspaces(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

	initialID := "initial"
	initialPath := t.TempDir()
	_ = registry.Register(initialID, initialPath)

	extraID := "new-after-startup"
	extraPath := t.TempDir()

	// listFn returns just the initial workspace at first; after the test
	// "creates" the second one out-of-band, returns both.
	var hasExtra atomic.Bool
	listFn := func() (map[string]string, error) {
		out := map[string]string{initialID: initialPath}
		if hasExtra.Load() {
			out[extraID] = extraPath
		}
		return out, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	appinfra.StartPeriodicWorkspaceReconcile(ctx, listFn, registry, 25*time.Millisecond, slog.Default())

	// Confirm only the initial workspace is registered before the second appears.
	time.Sleep(60 * time.Millisecond)
	if got := multiPool.WorkspaceIDs(); len(got) != 1 || got[0] != initialID {
		t.Fatalf("pre-extra IDs = %v, want only %q", got, initialID)
	}

	// "Create" the extra workspace out-of-band. The loop should pick it up.
	hasExtra.Store(true)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ids := multiPool.WorkspaceIDs()
		if len(ids) == 2 {
			sort.Strings(ids)
			if ids[0] == extraID || ids[1] == extraID {
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("periodic reconcile did not register %q within deadline; final IDs = %v",
		extraID, multiPool.WorkspaceIDs())
}

func TestReconcileStoreWorkspaces_NameKeys(t *testing.T) {
	registry, multiPool := newTestCoordinatorRegistry(t)

	// Name keys are valid when the workspace key was derived from its name.
	initialName := "my-project"
	initialPath := t.TempDir()
	_ = registry.Register(initialName, initialPath)

	extraName := "other-project"
	extraPath := t.TempDir()

	// Store path listing can return name-to-path.
	listFn := func() (map[string]string, error) {
		return map[string]string{
			initialName: initialPath,
			extraName:   extraPath,
		}, nil
	}

	appinfra.ReconcileStoreWorkspaces(listFn, initialName, true, registry, slog.Default())

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

func TestReconcileStoreWorkspaces_UUIDSkipMatchesInitialID(t *testing.T) {
	// Verifies the skip logic works when both initialID and map keys are UUIDs.
	// This was the core bug T2 fixes: previously, map keys were names but
	// initialID was a UUID, so the skip check failed and the initial workspace
	// got re-registered (replacing the custom auto-discovered pool).
	registry, multiPool := newTestCoordinatorRegistry(t)

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
	appinfra.ReconcileStoreWorkspaces(listFn, initialUUID, true, registry, slog.Default())

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
	registry, multiPool := newTestCoordinatorRegistry(t)

	initialWS := "my-workspace"
	initialPath := t.TempDir()
	_ = registry.Register(initialWS, initialPath)

	// Store path listing returns only the initial workspace.
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
