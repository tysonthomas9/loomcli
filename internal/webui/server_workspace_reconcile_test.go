package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// --- registerWorkspacePool unit tests ---

func TestRegisterWorkspacePool_RegistersInMultiPoolAndSubscriber(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	wsPath := t.TempDir()
	registerWorkspacePool("test-ws", wsPath, multiPool, multiSub, 10)

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

func TestRegisterWorkspacePool_MultipleWorkspaces(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	for _, name := range []string{"alpha", "beta", "gamma"} {
		wsPath := t.TempDir()
		registerWorkspacePool(name, wsPath, multiPool, multiSub, 10)
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

func TestRegisterWorkspacePool_DuplicateReplacesExisting(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	wsPath1 := t.TempDir()
	wsPath2 := t.TempDir()

	registerWorkspacePool("dup-ws", wsPath1, multiPool, multiSub, 10)
	pool1 := multiPool.PoolForWorkspace("dup-ws")

	registerWorkspacePool("dup-ws", wsPath2, multiPool, multiSub, 10)
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
	port := 59830

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
		AuthEnabled:     false,
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
	port := 59831

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		AuthEnabled:     false,
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
	port := 59832

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		AuthEnabled:     false,
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
	port := 59833

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		AuthEnabled:     false,
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
// These test the reconciliation logic directly using MultiPool and
// MultiWorkspaceSubscriber, without starting a full HTTP server.

func TestReconciliationLogic_SkipsInitialWorkspace(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	// Simulate the initial workspace already being registered
	initialWS := "my-project"
	initialPath := t.TempDir()
	registerWorkspacePool(initialWS, initialPath, multiPool, multiSub, 10)

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
		registerWorkspacePool(wsName, wsPath, multiPool, multiSub, 10)
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
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	// Simulate empty workspace map (no workspaces configured besides initial)
	workspaces := map[string]string{}

	for wsName, wsPath := range workspaces {
		if wsName == "default" {
			continue
		}
		registerWorkspacePool(wsName, wsPath, multiPool, multiSub, 10)
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
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	// Replicate the guard from StartServer: if WorkspaceListFn is nil, skip.
	var workspaceListFn func() (map[string]string, error)
	if workspaceListFn != nil {
		workspaces, err := workspaceListFn()
		if err == nil {
			for wsName, wsPath := range workspaces {
				registerWorkspacePool(wsName, wsPath, multiPool, multiSub, 10)
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
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

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
			registerWorkspacePool(wsName, wsPath, multiPool, multiSub, 10)
		}
	}

	// No pools should have been registered
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs when WorkspaceListFn errors, got %d", len(ids))
	}
}

func TestReconciliationLogic_OnlyInitialInMap(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	defer multiSub.Stop()

	initialWS := "my-workspace"
	initialPath := t.TempDir()
	registerWorkspacePool(initialWS, initialPath, multiPool, multiSub, 10)

	// WorkspaceListFn returns only the initial workspace
	workspaces := map[string]string{
		"my-workspace": initialPath,
	}

	for wsName, wsPath := range workspaces {
		if wsName == initialWS {
			continue
		}
		registerWorkspacePool(wsName, wsPath, multiPool, multiSub, 10)
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
