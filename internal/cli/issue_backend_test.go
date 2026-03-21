package cli

import (
	"sync"
	"testing"
)

func TestDefaultTracker_ReturnsNonNil(t *testing.T) {
	// Ensure any previous test override is cleared so defaultTracker()
	// falls back to defaultDeps.Tracker (which DefaultDeps sets to a bdBackend).
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	tracker := defaultTracker()
	if tracker == nil {
		t.Fatal("defaultTracker() returned nil; expected non-nil from defaultDeps.Tracker")
	}
}

func TestSetDefaultTracker_OverridesDefault(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.BackendNameResult = "test-override"
	setDefaultTracker(mock)

	got := defaultTracker()
	if got == nil {
		t.Fatal("defaultTracker() returned nil after setDefaultTracker")
	}
	if got.BackendName() != "test-override" {
		t.Errorf("BackendName() = %q, want %q", got.BackendName(), "test-override")
	}
}

func TestResetDefaultTracker_ClearsOverride(t *testing.T) {
	t.Cleanup(resetDefaultTracker)

	// Set an override first
	mock := NewMockTracker()
	mock.BackendNameResult = "will-be-cleared"
	setDefaultTracker(mock)

	// Verify the override is active
	if defaultTracker().BackendName() != "will-be-cleared" {
		t.Fatal("override not active before reset")
	}

	// Reset and verify it falls back to defaultDeps.Tracker
	resetDefaultTracker()

	got := defaultTracker()
	if got == nil {
		t.Fatal("defaultTracker() returned nil after resetDefaultTracker")
	}
	// After reset, it should reinitialize from defaultDeps.Tracker which is a bdBackend ("beads").
	if got.BackendName() == "will-be-cleared" {
		t.Error("resetDefaultTracker did not clear the override; still returning mock")
	}
}

func TestSetDefaultTracker_SubsequentCallsOverride(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock1 := NewMockTracker()
	mock1.BackendNameResult = "first"
	setDefaultTracker(mock1)

	if defaultTracker().BackendName() != "first" {
		t.Fatalf("expected first override, got %q", defaultTracker().BackendName())
	}

	mock2 := NewMockTracker()
	mock2.BackendNameResult = "second"
	setDefaultTracker(mock2)

	if defaultTracker().BackendName() != "second" {
		t.Errorf("expected second override, got %q", defaultTracker().BackendName())
	}
}

func TestDefaultTracker_ConcurrentAccess(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.BackendNameResult = "concurrent-test"
	setDefaultTracker(mock)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Half the goroutines read, half write — tests for data races.
	for i := 0; i < goroutines; i++ {
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				tracker := defaultTracker()
				if tracker == nil {
					t.Error("defaultTracker() returned nil during concurrent access")
				}
				_ = tracker.BackendName()
			}()
		} else {
			go func(n int) {
				defer wg.Done()
				m := NewMockTracker()
				m.BackendNameResult = "writer"
				setDefaultTracker(m)
			}(i)
		}
	}

	wg.Wait()

	// After all goroutines finish, defaultTracker should still return non-nil.
	if defaultTracker() == nil {
		t.Error("defaultTracker() is nil after concurrent access")
	}
}

func TestDefaultTracker_ConcurrentResetAndRead(t *testing.T) {
	t.Cleanup(resetDefaultTracker)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		if i%3 == 0 {
			go func() {
				defer wg.Done()
				resetDefaultTracker()
			}()
		} else {
			go func() {
				defer wg.Done()
				tracker := defaultTracker()
				if tracker == nil {
					t.Error("defaultTracker() returned nil during concurrent reset/read")
				}
			}()
		}
	}

	wg.Wait()
}

func TestDefaultTracker_LazyInitFromDefaultDeps(t *testing.T) {
	// Verify that when trackerInst is nil, defaultTracker() initializes
	// from defaultDeps.Tracker (double-checked locking path).
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	// defaultDeps.Tracker is initialized by DefaultDeps() to a bdBackend.
	tracker := defaultTracker()
	if tracker == nil {
		t.Fatal("defaultTracker() returned nil on lazy init")
	}
	// Should be the same object as defaultDeps.Tracker.
	if tracker != defaultDeps.Tracker {
		t.Error("defaultTracker() did not return defaultDeps.Tracker on lazy init")
	}
}
