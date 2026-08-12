package cli

import (
	"sync"
	"testing"
)

func TestDefaultTracker_ReturnsNonNil(t *testing.T) {
	// Ensure any previous test override is cleared so defaultWorkItems()
	// falls back to defaultDeps.WorkItems.
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)

	tracker := defaultWorkItems()
	if tracker == nil {
		t.Fatal("defaultWorkItems() returned nil; expected non-nil from defaultDeps.WorkItems")
	}
}

func TestSetDefaultTracker_OverridesDefault(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)

	mock := NewMockWorkItems()
	mock.BackendNameResult = "test-override"
	setDefaultWorkItems(mock)

	got := defaultWorkItems()
	if got == nil {
		t.Fatal("defaultWorkItems() returned nil after setDefaultWorkItems")
	}
	if workItemsName(got) != "test-override" {
		t.Errorf("BackendName() = %q, want %q", workItemsName(got), "test-override")
	}
}

func TestResetDefaultWorkItems_ClearsOverride(t *testing.T) {
	t.Cleanup(resetDefaultWorkItems)

	// Set an override first
	mock := NewMockWorkItems()
	mock.BackendNameResult = "will-be-cleared"
	setDefaultWorkItems(mock)

	// Verify the override is active
	if workItemsName(defaultWorkItems()) != "will-be-cleared" {
		t.Fatal("override not active before reset")
	}

	// Reset and verify it falls back to defaultDeps.WorkItems
	resetDefaultWorkItems()

	got := defaultWorkItems()
	if got == nil {
		t.Fatal("defaultWorkItems() returned nil after resetDefaultWorkItems")
	}
	if workItemsName(got) == "will-be-cleared" {
		t.Error("resetDefaultWorkItems did not clear the override; still returning mock")
	}
}

func TestSetDefaultTracker_SubsequentCallsOverride(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)

	mock1 := NewMockWorkItems()
	mock1.BackendNameResult = "first"
	setDefaultWorkItems(mock1)

	if workItemsName(defaultWorkItems()) != "first" {
		t.Fatalf("expected first override, got %q", workItemsName(defaultWorkItems()))
	}

	mock2 := NewMockWorkItems()
	mock2.BackendNameResult = "second"
	setDefaultWorkItems(mock2)

	if workItemsName(defaultWorkItems()) != "second" {
		t.Errorf("expected second override, got %q", workItemsName(defaultWorkItems()))
	}
}

func TestDefaultTracker_ConcurrentAccess(t *testing.T) {
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)

	mock := NewMockWorkItems()
	mock.BackendNameResult = "concurrent-test"
	setDefaultWorkItems(mock)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Half the goroutines read, half write — tests for data races.
	for i := 0; i < goroutines; i++ {
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				tracker := defaultWorkItems()
				if tracker == nil {
					t.Error("defaultWorkItems() returned nil during concurrent access")
				}
				_ = workItemsName(tracker)
			}()
		} else {
			go func(n int) {
				defer wg.Done()
				m := NewMockWorkItems()
				m.BackendNameResult = "writer"
				setDefaultWorkItems(m)
			}(i)
		}
	}

	wg.Wait()

	// After all goroutines finish, defaultWorkItems should still return non-nil.
	if defaultWorkItems() == nil {
		t.Error("defaultWorkItems() is nil after concurrent access")
	}
}

func TestDefaultTracker_ConcurrentResetAndRead(t *testing.T) {
	t.Cleanup(resetDefaultWorkItems)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		if i%3 == 0 {
			go func() {
				defer wg.Done()
				resetDefaultWorkItems()
			}()
		} else {
			go func() {
				defer wg.Done()
				tracker := defaultWorkItems()
				if tracker == nil {
					t.Error("defaultWorkItems() returned nil during concurrent reset/read")
				}
			}()
		}
	}

	wg.Wait()
}

func TestDefaultTracker_LazyInitFromDefaultDeps(t *testing.T) {
	// Verify that when trackerInst is nil, defaultWorkItems() initializes
	// from defaultDeps.WorkItems (double-checked locking path).
	t.Setenv("LOOM_DAEMON_SOCKET", "") // ensure IPC path is not triggered
	resetDefaultWorkItems()
	t.Cleanup(resetDefaultWorkItems)

	tracker := defaultWorkItems()
	if tracker == nil {
		t.Fatal("defaultWorkItems() returned nil on lazy init")
	}
	// Should be the same object as defaultDeps.WorkItems.
	if tracker != defaultDeps.WorkItems {
		t.Error("defaultWorkItems() did not return defaultDeps.WorkItems on lazy init")
	}
}
