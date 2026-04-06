//go:build ignore

package daemon

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrencyTracker_Unlimited(t *testing.T) {
	// Roles with no limit should always acquire
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"plan": {Description: "no limit set"},
	})

	for i := 0; i < 100; i++ {
		if !ct.Acquire("plan") {
			t.Fatalf("Acquire(plan) returned false on iteration %d", i)
		}
	}

	if got := ct.ActiveCount("plan"); got != 100 {
		t.Errorf("ActiveCount(plan) = %d, want 100", got)
	}
}

func TestConcurrencyTracker_UnknownRole(t *testing.T) {
	// Unknown roles (not in config) should be unlimited
	ct := NewConcurrencyTracker(map[string]RoleConfig{})

	if !ct.Acquire("unknown") {
		t.Fatal("Acquire(unknown) returned false")
	}
	if !ct.TryAcquire("unknown") {
		t.Fatal("TryAcquire(unknown) returned false")
	}

	if got := ct.ActiveCount("unknown"); got != 2 {
		t.Errorf("ActiveCount(unknown) = %d, want 2", got)
	}
}

func TestConcurrencyTracker_LimitEnforced(t *testing.T) {
	limit := 2
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	// Acquire up to limit
	if !ct.Acquire("task") {
		t.Fatal("Acquire(task) #1 returned false")
	}
	if !ct.Acquire("task") {
		t.Fatal("Acquire(task) #2 returned false")
	}

	// TryAcquire should fail at limit
	if ct.TryAcquire("task") {
		t.Fatal("TryAcquire(task) returned true at limit")
	}

	if got := ct.ActiveCount("task"); got != 2 {
		t.Errorf("ActiveCount(task) = %d, want 2", got)
	}

	// Release one slot, TryAcquire should succeed
	ct.Release("task")
	if !ct.TryAcquire("task") {
		t.Fatal("TryAcquire(task) returned false after Release")
	}
}

func TestConcurrencyTracker_TryAcquire(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	if !ct.TryAcquire("task") {
		t.Fatal("first TryAcquire returned false")
	}
	if ct.TryAcquire("task") {
		t.Fatal("second TryAcquire returned true (should be at limit)")
	}
}

func TestConcurrencyTracker_MultipleRoles(t *testing.T) {
	taskLimit := 2
	planLimit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &taskLimit},
		"plan": {MaxConcurrency: &planLimit},
	})

	// Fill task slots
	ct.Acquire("task")
	ct.Acquire("task")
	if ct.TryAcquire("task") {
		t.Fatal("task should be at limit")
	}

	// Plan should be independent
	if !ct.TryAcquire("plan") {
		t.Fatal("plan should have an available slot")
	}
	if ct.TryAcquire("plan") {
		t.Fatal("plan should be at limit")
	}
}

func TestConcurrencyTracker_ZeroLimit(t *testing.T) {
	// Explicit MaxConcurrency=0 means unlimited
	zero := 0
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &zero},
	})

	for i := 0; i < 50; i++ {
		if !ct.Acquire("task") {
			t.Fatalf("Acquire(task) returned false on iteration %d", i)
		}
	}
}

func TestConcurrencyTracker_NilConfig(t *testing.T) {
	// nil MaxConcurrency means unlimited
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: nil},
	})

	for i := 0; i < 50; i++ {
		if !ct.Acquire("task") {
			t.Fatalf("Acquire(task) returned false on iteration %d", i)
		}
	}
}

func TestConcurrencyTracker_BlockAndRelease(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	// Fill the slot
	ct.Acquire("task")

	acquired := make(chan struct{})

	go func() {
		if !ct.Acquire("task") {
			t.Error("blocked Acquire returned false (expected true)")
		}
		close(acquired)
	}()

	// Wait until the goroutine is actually blocked inside cond.Wait().
	// When blocked on cond.Wait(), it releases ct.mu. We can detect this
	// by locking ct.mu and checking the count is still at the limit
	// (meaning the goroutine hasn't acquired yet but has entered Wait).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for goroutine to block")
		case <-acquired:
			t.Fatal("goroutine acquired before Release")
		default:
		}
		// If we can grab the lock and count is still 1, the goroutine
		// must be in cond.Wait() (which released the lock for us to grab).
		ct.mu.Lock()
		count := ct.counts["task"]
		ct.mu.Unlock()
		if count == 1 {
			// Goroutine is blocked in cond.Wait — proceed to release
			break
		}
		runtime.Gosched()
	}

	// Release the slot — should unblock the goroutine
	ct.Release("task")

	select {
	case <-acquired:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not acquire after Release (timeout)")
	}
}

func TestConcurrencyTracker_ShutdownUnblocks(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	// Fill the slot
	ct.Acquire("task")

	done := make(chan bool, 1)

	go func() {
		result := ct.Acquire("task")
		done <- result
	}()

	// Wait until goroutine is blocked in cond.Wait()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for goroutine to block")
		default:
		}
		ct.mu.Lock()
		count := ct.counts["task"]
		ct.mu.Unlock()
		if count == 1 {
			break
		}
		runtime.Gosched()
	}

	// Close should unblock with false
	ct.Close()

	select {
	case result := <-done:
		if result {
			t.Fatal("Acquire returned true after Close (expected false)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not unblock after Close (timeout)")
	}
}

func TestConcurrencyTracker_ConcurrentAccess(t *testing.T) {
	limit := 3
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	var maxSeen atomic.Int32
	var wg sync.WaitGroup
	const numGoroutines = 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !ct.Acquire("task") {
				return
			}
			// Record active count while holding slot
			current := int32(ct.ActiveCount("task"))
			for {
				old := maxSeen.Load()
				if current <= old || maxSeen.CompareAndSwap(old, current) {
					break
				}
			}

			// Simulate work
			time.Sleep(5 * time.Millisecond)
			ct.Release("task")
		}()
	}

	wg.Wait()

	if got := maxSeen.Load(); got > int32(limit) {
		t.Errorf("max concurrent observed = %d, want <= %d", got, limit)
	}
}

func TestConcurrencyTracker_Counts(t *testing.T) {
	limit := 5
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	ct.Acquire("task")
	ct.Acquire("task")
	ct.Acquire("plan") // unlimited role

	counts := ct.Counts()

	// Verify it's a copy
	counts["task"] = 999
	if ct.ActiveCount("task") == 999 {
		t.Fatal("Counts() returned reference, not copy")
	}

	// Verify values
	fresh := ct.Counts()
	if fresh["task"] != 2 {
		t.Errorf("counts[task] = %d, want 2", fresh["task"])
	}
	if fresh["plan"] != 1 {
		t.Errorf("counts[plan] = %d, want 1", fresh["plan"])
	}
}

func TestConcurrencyTracker_ReleaseWithoutAcquire(t *testing.T) {
	ct := NewConcurrencyTracker(map[string]RoleConfig{})

	// Release on a role with count 0 should not go negative
	ct.Release("task")

	if got := ct.ActiveCount("task"); got != 0 {
		t.Errorf("ActiveCount(task) = %d after Release without Acquire, want 0", got)
	}
}

func TestConcurrencyTracker_DoubleClose(t *testing.T) {
	ct := NewConcurrencyTracker(map[string]RoleConfig{})

	// Should not panic
	ct.Close()
	ct.Close()
}

func TestConcurrencyTracker_TryAcquireAfterClose(t *testing.T) {
	ct := NewConcurrencyTracker(map[string]RoleConfig{})
	ct.Close()

	if ct.TryAcquire("task") {
		t.Fatal("TryAcquire returned true after Close")
	}
}

func TestConcurrencyTracker_EmptyRoles(t *testing.T) {
	// nil roles map
	ct := NewConcurrencyTracker(nil)

	if !ct.Acquire("anything") {
		t.Fatal("Acquire returned false with nil roles")
	}
	ct.Release("anything")
}

// TestConcurrencyTracker_NilReceiver verifies nil receiver safety for all methods.
func TestConcurrencyTracker_NilReceiver(t *testing.T) {
	var ct *ConcurrencyTracker

	// All should be no-ops or return safe defaults
	if !ct.Acquire("task") {
		t.Error("Acquire on nil should return true")
	}
	if !ct.TryAcquire("task") {
		t.Error("TryAcquire on nil should return true")
	}
	ct.Release("task") // should not panic
	ct.Close()         // should not panic
}

func TestConcurrencyTracker_NilActiveCount(t *testing.T) {
	var ct *ConcurrencyTracker
	if got := ct.ActiveCount("any"); got != 0 {
		t.Errorf("nil ActiveCount = %d, want 0", got)
	}
}

func TestConcurrencyTracker_NilCounts(t *testing.T) {
	var ct *ConcurrencyTracker
	counts := ct.Counts()
	if counts == nil {
		t.Error("nil Counts() returned nil, want empty map")
	}
	if len(counts) != 0 {
		t.Errorf("nil Counts() len = %d, want 0", len(counts))
	}
}

func TestConcurrencyTracker_AcquireUnlimitedAfterClose(t *testing.T) {
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"unlimited": {}, // no MaxConcurrency = unlimited
	})
	ct.Close()
	if ct.Acquire("unlimited") {
		t.Error("Acquire on closed tracker should return false for unlimited roles")
	}
}
