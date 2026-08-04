package fleet

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// setupRegistryTest creates a StoreRegistry backed by a miniredis instance.
func setupRegistryTest(t *testing.T) (*StoreRegistry, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   30 * time.Minute,
			CheckInterval: 1 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg, mr
}

// ---------------------------------------------------------------------------
// Register + Get
// ---------------------------------------------------------------------------

func TestStoreRegistry_RegisterAndGet(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	if err := reg.Register("ws-1"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	store, ok := reg.Get("ws-1")
	if !ok {
		t.Fatal("expected Get to return true for registered workspace")
	}
	if store == nil {
		t.Fatal("expected non-nil Store")
	}
}

// ---------------------------------------------------------------------------
// Register with workspace prefix — verify Redis key namespacing
// ---------------------------------------------------------------------------

func TestStoreRegistry_RegisterWorkspacePrefix(t *testing.T) {
	reg, mr := setupRegistryTest(t)
	ctx := context.Background()

	if err := reg.Register("myws"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	store, ok := reg.Get("myws")
	if !ok {
		t.Fatal("expected Get to return true")
	}

	// Write via the Store (TryClaim writes a key)
	ok, err := store.TryClaim(ctx, "task-1", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Verify the key starts with "fleet:myws:"
	expectedKeyPrefix := "fleet:myws:"
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if strings.HasPrefix(k, expectedKeyPrefix) && strings.Contains(k, "task-1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a Redis key starting with %q, got keys: %v", expectedKeyPrefix, keys)
	}
}

// ---------------------------------------------------------------------------
// Register is idempotent
// ---------------------------------------------------------------------------

func TestStoreRegistry_RegisterIdempotent(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	if err := reg.Register("ws-idem"); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	store1, _ := reg.Get("ws-idem")

	// Register again — should be a no-op
	if err := reg.Register("ws-idem"); err != nil {
		t.Fatalf("second Register failed: %v", err)
	}

	store2, _ := reg.Get("ws-idem")

	if store1 != store2 {
		t.Error("expected idempotent Register to return the same Store pointer")
	}
}

// ---------------------------------------------------------------------------
// Deregister removes Store
// ---------------------------------------------------------------------------

func TestStoreRegistry_Deregister(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	if err := reg.Register("ws-del"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	reg.Deregister("ws-del")

	store, ok := reg.Get("ws-del")
	if ok {
		t.Error("expected Get to return false after Deregister")
	}
	if store != nil {
		t.Error("expected nil Store after Deregister")
	}
}

// ---------------------------------------------------------------------------
// Deregister stops the TimeoutEnforcer (no goroutine leak)
// ---------------------------------------------------------------------------

func TestStoreRegistry_DeregisterStopsEnforcer(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   100 * time.Millisecond,
			CheckInterval: 50 * time.Millisecond,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	t.Cleanup(func() { reg.Close() })

	if err := reg.Register("ws-stop"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Let the enforcer tick at least once
	time.Sleep(80 * time.Millisecond)

	// Deregister should stop the enforcer without hanging
	done := make(chan struct{})
	go func() {
		reg.Deregister("ws-stop")
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Deregister hung — TimeoutEnforcer.Stop() likely blocked")
	}
}

// ---------------------------------------------------------------------------
// Deregister unknown workspace is a no-op (no panic)
// ---------------------------------------------------------------------------

func TestStoreRegistry_DeregisterUnknown(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	// Should not panic
	reg.Deregister("nonexistent")
}

// ---------------------------------------------------------------------------
// Get for unknown workspace returns (nil, false)
// ---------------------------------------------------------------------------

func TestStoreRegistry_GetUnknown(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	store, ok := reg.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for unknown workspace")
	}
	if store != nil {
		t.Error("expected nil Store for unknown workspace")
	}
}

// ---------------------------------------------------------------------------
// Close cleans up all enforcers and closes the Redis client
// ---------------------------------------------------------------------------

func TestStoreRegistry_Close(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   100 * time.Millisecond,
			CheckInterval: 50 * time.Millisecond,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}

	if err := reg.Register("ws-a"); err != nil {
		t.Fatalf("Register ws-a failed: %v", err)
	}
	if err := reg.Register("ws-b"); err != nil {
		t.Fatalf("Register ws-b failed: %v", err)
	}

	// Let enforcers tick
	time.Sleep(80 * time.Millisecond)

	// Close should stop enforcers and close the client without hanging
	done := make(chan struct{})
	go func() {
		reg.Close()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Close() hung")
	}

	// After Close, the internal maps should be empty
	store, ok := reg.Get("ws-a")
	if ok || store != nil {
		t.Error("expected Get to return (nil, false) after Close")
	}
}

// ---------------------------------------------------------------------------
// Two workspaces have isolated Redis key namespaces
// ---------------------------------------------------------------------------

func TestStoreRegistry_IsolatedNamespaces(t *testing.T) {
	reg, mr := setupRegistryTest(t)
	ctx := context.Background()

	if err := reg.Register("alpha"); err != nil {
		t.Fatalf("Register alpha failed: %v", err)
	}
	if err := reg.Register("beta"); err != nil {
		t.Fatalf("Register beta failed: %v", err)
	}

	storeAlpha, _ := reg.Get("alpha")
	storeBeta, _ := reg.Get("beta")

	// Both workspaces claim the same task ID
	ok, err := storeAlpha.TryClaim(ctx, "task-shared", "worker-a")
	if err != nil {
		t.Fatalf("alpha TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("alpha claim should succeed")
	}

	ok, err = storeBeta.TryClaim(ctx, "task-shared", "worker-b")
	if err != nil {
		t.Fatalf("beta TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("beta claim should succeed (isolated namespace)")
	}

	// Verify distinct keys in Redis
	alphaKey := "fleet:alpha:tasks:claimed:task-shared"
	betaKey := "fleet:beta:tasks:claimed:task-shared"

	alphaOwner, err := mr.Get(alphaKey)
	if err != nil {
		t.Fatalf("alpha key missing: %v", err)
	}
	if alphaOwner != "worker-a" {
		t.Errorf("expected alpha owner=worker-a, got %s", alphaOwner)
	}

	betaOwner, err := mr.Get(betaKey)
	if err != nil {
		t.Fatalf("beta key missing: %v", err)
	}
	if betaOwner != "worker-b" {
		t.Errorf("expected beta owner=worker-b, got %s", betaOwner)
	}
}

// ---------------------------------------------------------------------------
// Register after Close returns error
// ---------------------------------------------------------------------------

func TestStoreRegistry_RegisterAfterClose(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   30 * time.Minute,
			CheckInterval: 1 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}

	if err := reg.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err = reg.Register("ws-after-close")
	if err == nil {
		t.Fatal("expected error when registering after Close")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected error to mention 'closed', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access (Register/Get/Deregister) with -race
// ---------------------------------------------------------------------------

func TestStoreRegistry_ConcurrentAccess(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()
			wsID := fmt.Sprintf("ws-%d", i%5) // 5 workspaces, contention expected

			// Register
			_ = reg.Register(wsID)

			// Get
			reg.Get(wsID)

			// Deregister (only some goroutines)
			if i%3 == 0 {
				reg.Deregister(wsID)
			}

			// Re-register
			_ = reg.Register(wsID)

			// GetTotalTimeoutCount
			reg.GetTotalTimeoutCount()
		}(i)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// GetTotalTimeoutCount aggregates across workspaces
// ---------------------------------------------------------------------------

func TestStoreRegistry_GetTotalTimeoutCount(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	// With no registered workspaces, count should be 0
	if count := reg.GetTotalTimeoutCount(); count != 0 {
		t.Errorf("expected 0 total timeout count, got %d", count)
	}

	// Register a couple of workspaces
	if err := reg.Register("ws-tc-1"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := reg.Register("ws-tc-2"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// No timeouts have occurred, so count should still be 0
	if count := reg.GetTotalTimeoutCount(); count != 0 {
		t.Errorf("expected 0 total timeout count (no timeouts yet), got %d", count)
	}
}

// ---------------------------------------------------------------------------
// NewStoreForClient Close() does NOT close the shared client
// ---------------------------------------------------------------------------

func TestStoreForClient_CloseDoesNotCloseSharedClient(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })

	store := NewStoreForClient(client, "ws-shared", nil)

	// Close the store — should be a no-op for the client
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() failed: %v", err)
	}

	// The shared client should still be usable
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("shared client should still work after Store.Close(), got error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Register with empty workspace ID returns error
// ---------------------------------------------------------------------------

func TestStoreRegistry_RegisterEmptyID(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	err := reg.Register("")
	if err == nil {
		t.Fatal("expected error for empty workspace ID")
	}
}

// ---------------------------------------------------------------------------
// Close is idempotent
// ---------------------------------------------------------------------------

func TestStoreRegistry_CloseIdempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   30 * time.Minute,
			CheckInterval: 1 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}

	if err := reg.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	// Second close should be a no-op, not an error
	if err := reg.Close(); err != nil {
		t.Fatalf("second Close should be no-op, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Client() returns the shared Redis client
// ---------------------------------------------------------------------------

func TestStoreRegistry_Client(t *testing.T) {
	reg, _ := setupRegistryTest(t)

	client := reg.Client()
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// Verify it's usable
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Client().Ping() failed: %v", err)
	}
}
