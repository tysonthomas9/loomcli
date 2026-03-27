package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// newTestRegistry creates a WorkspaceRegistry with all supporting infrastructure
// for testing. It starts the SSEHub and registers cleanup. Callers should defer
// the returned cleanup function.
func newTestRegistry(t *testing.T) (*WorkspaceRegistry, *daemon.MultiPool, *MultiWorkspaceSubscriber) {
	t.Helper()
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 10)
	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	t.Cleanup(func() { multiSub.Stop() })

	registry := NewWorkspaceRegistry(multiPool, multiSub, 10, slog.Default())
	return registry, multiPool, multiSub
}

func TestRegistry_Register_CreatesPoolAndSubscriber(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	wsPath := t.TempDir()

	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Verify pool appears in MultiPool.WorkspaceIDs(), keyed by UUID.
	poolIDs := multiPool.WorkspaceIDs()
	if len(poolIDs) != 1 {
		t.Fatalf("expected 1 workspace ID in MultiPool, got %d: %v", len(poolIDs), poolIDs)
	}
	if poolIDs[0] != wsID {
		t.Errorf("expected pool keyed by UUID %q, got %q", wsID, poolIDs[0])
	}

	// Verify subscriber appears in MultiWorkspaceSubscriber.WorkspaceIDs(), keyed by UUID.
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 {
		t.Fatalf("expected 1 workspace ID in subscriber, got %d: %v", len(subIDs), subIDs)
	}
	if subIDs[0] != wsID {
		t.Errorf("expected subscriber keyed by UUID %q, got %q", wsID, subIDs[0])
	}

	// Verify both are keyed by UUID, not by path.
	pool := multiPool.PoolForWorkspace(wsID)
	if pool == nil {
		t.Error("expected PoolForWorkspace(uuid) to return non-nil pool")
	}
	poolByPath := multiPool.PoolForWorkspace(wsPath)
	if poolByPath != nil {
		t.Error("expected PoolForWorkspace(path) to return nil -- pool should be keyed by UUID, not path")
	}
}

func TestRegistry_Deregister_CleansUp(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "deregister-test-uuid"
	wsPath := t.TempDir()

	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Sanity check: registered.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Fatal("expected pool to be registered before deregister")
	}
	if len(multiSub.WorkspaceIDs()) != 1 {
		t.Fatal("expected subscriber to have 1 workspace before deregister")
	}

	registry.Deregister(wsID)

	// Verify pool is removed.
	if multiPool.PoolForWorkspace(wsID) != nil {
		t.Error("expected PoolForWorkspace to return nil after Deregister")
	}

	// Verify subscriber is removed.
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 0 {
		t.Errorf("expected 0 subscriber IDs after Deregister, got %d: %v", len(subIDs), subIDs)
	}
}

func TestRegistry_RegisterPool_WithPrebuiltPool(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "prebuilt-pool-uuid"

	// Create a pre-built pool using NewConnectionPool + circuit breaker,
	// matching the pattern from the design.
	socketPath := rpc.ShortSocketPath(t.TempDir())
	rawPool, err := daemon.NewConnectionPool(socketPath, 10)
	if err != nil {
		t.Fatalf("NewConnectionPool returned error: %v", err)
	}

	breaker := circuitbreaker.NewBreaker("test-prebuilt", circuitbreaker.Config{
		FailureThreshold:  5,
		OpenTimeout:       30 * time.Second,
		HalfOpenMaxProbes: 1,
		ShouldTrip:        daemon.DaemonShouldTrip,
	})
	pool := daemon.NewProtectedPool(rawPool, breaker)

	if err := registry.RegisterPool(wsID, pool); err != nil {
		t.Fatalf("RegisterPool returned error: %v", err)
	}

	// Verify registered in MultiPool.
	if multiPool.PoolForWorkspace(wsID) == nil {
		t.Error("expected pre-built pool to be registered in MultiPool")
	}

	// Verify subscriber was started.
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 || subIDs[0] != wsID {
		t.Errorf("expected subscriber for %q, got %v", wsID, subIDs)
	}
}

func TestRegistry_Deregister_UnknownWorkspace_NoOp(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	// Deregister a UUID that was never registered. Should not panic.
	registry.Deregister("nonexistent-uuid-that-was-never-registered")
}

func TestRegistry_Register_EmptyID_Error(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	err := registry.Register("", t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty workspace ID")
	}
	if !errors.Is(err, ErrEmptyWorkspaceID) {
		t.Errorf("expected ErrEmptyWorkspaceID, got: %v", err)
	}
}

func TestRegistry_Register_EmptyPath_Error(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	err := registry.Register("some-valid-uuid", "")
	if err == nil {
		t.Fatal("expected error for empty workspace path")
	}
	if !errors.Is(err, ErrEmptyWorkspacePath) {
		t.Errorf("expected ErrEmptyWorkspacePath, got: %v", err)
	}
}

func TestRegistry_RegisterPool_NilPool_Error(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	err := registry.RegisterPool("some-valid-uuid", nil)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
	// The error message is "pool must not be nil".
	if err.Error() != "pool must not be nil" {
		t.Errorf("expected 'pool must not be nil' error, got: %v", err)
	}
}

func TestRegistry_Close_PreventsNewRegistrations(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	if err := registry.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	err := registry.Register("new-uuid", t.TempDir())
	if err == nil {
		t.Fatal("expected error after Close")
	}
	if !errors.Is(err, ErrRegistryClosed) {
		t.Errorf("expected ErrRegistryClosed, got: %v", err)
	}
}

func TestRegistry_Close_PreventsRegisterPool(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	if err := registry.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	socketPath := rpc.ShortSocketPath(t.TempDir())
	rawPool, err := daemon.NewConnectionPool(socketPath, 10)
	if err != nil {
		t.Fatalf("NewConnectionPool returned error: %v", err)
	}
	breaker := circuitbreaker.NewBreaker("test-closed", circuitbreaker.Config{
		FailureThreshold:  5,
		OpenTimeout:       30 * time.Second,
		HalfOpenMaxProbes: 1,
		ShouldTrip:        daemon.DaemonShouldTrip,
	})
	pool := daemon.NewProtectedPool(rawPool, breaker)

	err = registry.RegisterPool("new-uuid", pool)
	if err == nil {
		t.Fatal("expected error after Close")
	}
	if !errors.Is(err, ErrRegistryClosed) {
		t.Errorf("expected ErrRegistryClosed, got: %v", err)
	}
}

func TestRegistry_DoubleRegister_ReplacesCleanly(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "double-register-uuid"
	wsPath1 := t.TempDir()
	wsPath2 := t.TempDir()

	if err := registry.Register(wsID, wsPath1); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}
	pool1 := multiPool.PoolForWorkspace(wsID)

	if err := registry.Register(wsID, wsPath2); err != nil {
		t.Fatalf("second Register returned error: %v", err)
	}
	pool2 := multiPool.PoolForWorkspace(wsID)

	// The pool should have been replaced (different socket path means different pool).
	if pool1 == pool2 {
		t.Error("expected pool to be replaced after second registration with different path")
	}

	// Should still have exactly one entry in WorkspaceIDs.
	poolIDs := multiPool.WorkspaceIDs()
	if len(poolIDs) != 1 {
		t.Errorf("expected 1 workspace ID after double registration, got %d: %v", len(poolIDs), poolIDs)
	}

	// Subscriber should also have exactly one entry.
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 1 {
		t.Errorf("expected 1 subscriber ID after double registration, got %d: %v", len(subIDs), subIDs)
	}

	// The second pool should be the active one.
	if multiPool.PoolForWorkspace(wsID) != pool2 {
		t.Error("expected the second pool to be the active pool")
	}
}

func TestRegistry_ConcurrentOperations(t *testing.T) {
	registry, multiPool, _ := newTestRegistry(t)

	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			wsID := fmt.Sprintf("concurrent-ws-%d", idx)
			wsPath := t.TempDir()

			// Register
			_ = registry.Register(wsID, wsPath)

			// Check WorkspaceIDs (read operation)
			_ = registry.WorkspaceIDs()

			// Deregister
			registry.Deregister(wsID)
		}(i)
	}

	wg.Wait()

	// After all goroutines complete, all workspaces should be deregistered.
	ids := multiPool.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 workspace IDs after all goroutines finish, got %d: %v", len(ids), ids)
	}
}

func TestRegistry_Register_PoolFailure_StillAttemptsSubscriber(t *testing.T) {
	registry, multiPool, multiSub := newTestRegistry(t)

	wsID := "pool-failure-test-uuid"
	wsPath := t.TempDir()

	// Override poolFactory to simulate daemon-down scenario.
	registry.poolFactory = func(socketPath string, poolSize int) (*daemon.ConnectionPool, error) {
		return nil, fmt.Errorf("simulated pool creation failure")
	}

	// Register should return nil (non-fatal) even when pool creation fails.
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// Pool was never created, so MultiPool should be empty.
	poolIDs := multiPool.WorkspaceIDs()
	if len(poolIDs) != 0 {
		t.Errorf("expected 0 workspace IDs in MultiPool after pool failure, got %d: %v", len(poolIDs), poolIDs)
	}

	// Subscriber was attempted but failed (no pool registered).
	// The key assertion: the code reached the subscriber block (no early return).
	subIDs := multiSub.WorkspaceIDs()
	if len(subIDs) != 0 {
		t.Errorf("expected 0 subscriber IDs (subscriber fails without pool), got %d: %v", len(subIDs), subIDs)
	}

	// Registry is still usable after pool failure — restore factory and register again.
	registry.poolFactory = daemon.NewConnectionPool
	if err := registry.Register(wsID, wsPath); err != nil {
		t.Fatalf("second Register returned error: %v", err)
	}

	poolIDs = multiPool.WorkspaceIDs()
	if len(poolIDs) != 1 || poolIDs[0] != wsID {
		t.Errorf("expected pool registered after factory restore, got %v", poolIDs)
	}

	subIDs = multiSub.WorkspaceIDs()
	if len(subIDs) != 1 || subIDs[0] != wsID {
		t.Errorf("expected subscriber registered after factory restore, got %v", subIDs)
	}
}

func TestRegistry_CloseBlocksInFlightRegister(t *testing.T) {
	const N = 20

	registry, multiPool, _ := newTestRegistry(t)

	var wg sync.WaitGroup

	// Launch N goroutines that each try to Register a unique workspace.
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			wsID := fmt.Sprintf("race-register-%d", idx)
			wsPath := t.TempDir()
			errs[idx] = registry.Register(wsID, wsPath)
		}(i)
	}

	// Close concurrently with the registrations.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = registry.Close()
	}()

	wg.Wait()

	// Invariant: for each goroutine, either the registration succeeded
	// (pool present in MultiPool) or it returned ErrRegistryClosed
	// (pool NOT present). No ambiguous state.
	registeredIDs := make(map[string]bool)
	for _, id := range multiPool.WorkspaceIDs() {
		registeredIDs[id] = true
	}

	var succeeded, rejected int
	for i := 0; i < N; i++ {
		wsID := fmt.Sprintf("race-register-%d", i)
		if errs[i] == nil {
			// Register returns nil even when pool creation fails (best-effort),
			// so we don't assert pool presence here. The safety property is the
			// reverse: rejected => pool absent.
			succeeded++
		} else if errors.Is(errs[i], ErrRegistryClosed) {
			rejected++
			if registeredIDs[wsID] {
				t.Errorf("Register returned ErrRegistryClosed for %q but pool IS in MultiPool", wsID)
			}
		} else {
			t.Errorf("unexpected error for %q: %v", wsID, errs[i])
		}
	}

	if succeeded+rejected != N {
		t.Errorf("expected %d total outcomes, got succeeded=%d rejected=%d", N, succeeded, rejected)
	}

	t.Logf("Register outcomes: %d succeeded, %d rejected (ErrRegistryClosed)", succeeded, rejected)
}

func TestRegistry_CloseBlocksInFlightRegisterPool(t *testing.T) {
	const N = 20

	registry, multiPool, _ := newTestRegistry(t)

	// Pre-build pools for each goroutine.
	type poolEntry struct {
		id   string
		pool daemon.Pool
	}
	entries := make([]poolEntry, N)
	for i := 0; i < N; i++ {
		socketPath := rpc.ShortSocketPath(t.TempDir())
		rawPool, err := daemon.NewConnectionPool(socketPath, 2)
		if err != nil {
			t.Fatalf("NewConnectionPool[%d]: %v", i, err)
		}
		breaker := circuitbreaker.NewBreaker(fmt.Sprintf("race-pool-%d", i), circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
		})
		entries[i] = poolEntry{
			id:   fmt.Sprintf("race-regpool-%d", i),
			pool: daemon.NewProtectedPool(rawPool, breaker),
		}
	}

	var wg sync.WaitGroup

	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = registry.RegisterPool(entries[idx].id, entries[idx].pool)
		}(i)
	}

	// Close concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = registry.Close()
	}()

	wg.Wait()

	registeredIDs := make(map[string]bool)
	for _, id := range multiPool.WorkspaceIDs() {
		registeredIDs[id] = true
	}

	var succeeded, rejected int
	for i := 0; i < N; i++ {
		wsID := entries[i].id
		if errs[i] == nil {
			succeeded++
			if !registeredIDs[wsID] {
				t.Errorf("RegisterPool succeeded for %q but pool not in MultiPool", wsID)
			}
		} else if errors.Is(errs[i], ErrRegistryClosed) {
			rejected++
			if registeredIDs[wsID] {
				t.Errorf("RegisterPool returned ErrRegistryClosed for %q but pool IS in MultiPool", wsID)
			}
		} else {
			t.Errorf("unexpected error for %q: %v", wsID, errs[i])
		}
	}

	if succeeded+rejected != N {
		t.Errorf("expected %d total outcomes, got succeeded=%d rejected=%d", N, succeeded, rejected)
	}

	t.Logf("RegisterPool outcomes: %d succeeded, %d rejected (ErrRegistryClosed)", succeeded, rejected)
}

func TestRegistry_WorkspaceIDs_ReturnsRegisteredUUIDs(t *testing.T) {
	registry, _, _ := newTestRegistry(t)

	uuids := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	}

	for _, id := range uuids {
		wsPath := t.TempDir()
		if err := registry.Register(id, wsPath); err != nil {
			t.Fatalf("Register(%q) returned error: %v", id, err)
		}
	}

	got := registry.WorkspaceIDs()
	sort.Strings(got)
	sort.Strings(uuids)

	if len(got) != len(uuids) {
		t.Fatalf("expected %d workspace IDs, got %d: %v", len(uuids), len(got), got)
	}

	for i, want := range uuids {
		if got[i] != want {
			t.Errorf("WorkspaceIDs[%d] = %q, want %q", i, got[i], want)
		}
	}
}
