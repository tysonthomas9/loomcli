package cli

import (
	"sync"
	"testing"
)

func TestDefaultTracker_ReturnsNonNil(t *testing.T) {
	// Ensure any previous test override is cleared so defaultIssueBackend()
	// falls back to defaultDeps.IssueBackend (which DefaultDeps sets to a bdBackend).
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	tracker := defaultIssueBackend()
	if tracker == nil {
		t.Fatal("defaultIssueBackend() returned nil; expected non-nil from defaultDeps.IssueBackend")
	}
}

func TestSetDefaultTracker_OverridesDefault(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.BackendNameResult = "test-override"
	setDefaultIssueBackend(mock)

	got := defaultIssueBackend()
	if got == nil {
		t.Fatal("defaultIssueBackend() returned nil after setDefaultIssueBackend")
	}
	if got.BackendName() != "test-override" {
		t.Errorf("BackendName() = %q, want %q", got.BackendName(), "test-override")
	}
}

func TestResetDefaultIssueBackend_ClearsOverride(t *testing.T) {
	t.Cleanup(resetDefaultIssueBackend)

	// Set an override first
	mock := NewMockIssueBackend()
	mock.BackendNameResult = "will-be-cleared"
	setDefaultIssueBackend(mock)

	// Verify the override is active
	if defaultIssueBackend().BackendName() != "will-be-cleared" {
		t.Fatal("override not active before reset")
	}

	// Reset and verify it falls back to defaultDeps.IssueBackend
	resetDefaultIssueBackend()

	got := defaultIssueBackend()
	if got == nil {
		t.Fatal("defaultIssueBackend() returned nil after resetDefaultIssueBackend")
	}
	// After reset, it should reinitialize from defaultDeps.IssueBackend which is a bdBackend ("beads").
	if got.BackendName() == "will-be-cleared" {
		t.Error("resetDefaultIssueBackend did not clear the override; still returning mock")
	}
}

func TestSetDefaultTracker_SubsequentCallsOverride(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock1 := NewMockIssueBackend()
	mock1.BackendNameResult = "first"
	setDefaultIssueBackend(mock1)

	if defaultIssueBackend().BackendName() != "first" {
		t.Fatalf("expected first override, got %q", defaultIssueBackend().BackendName())
	}

	mock2 := NewMockIssueBackend()
	mock2.BackendNameResult = "second"
	setDefaultIssueBackend(mock2)

	if defaultIssueBackend().BackendName() != "second" {
		t.Errorf("expected second override, got %q", defaultIssueBackend().BackendName())
	}
}

func TestDefaultTracker_ConcurrentAccess(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.BackendNameResult = "concurrent-test"
	setDefaultIssueBackend(mock)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Half the goroutines read, half write — tests for data races.
	for i := 0; i < goroutines; i++ {
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				tracker := defaultIssueBackend()
				if tracker == nil {
					t.Error("defaultIssueBackend() returned nil during concurrent access")
				}
				_ = tracker.BackendName()
			}()
		} else {
			go func(n int) {
				defer wg.Done()
				m := NewMockIssueBackend()
				m.BackendNameResult = "writer"
				setDefaultIssueBackend(m)
			}(i)
		}
	}

	wg.Wait()

	// After all goroutines finish, defaultIssueBackend should still return non-nil.
	if defaultIssueBackend() == nil {
		t.Error("defaultIssueBackend() is nil after concurrent access")
	}
}

func TestDefaultTracker_ConcurrentResetAndRead(t *testing.T) {
	t.Cleanup(resetDefaultIssueBackend)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		if i%3 == 0 {
			go func() {
				defer wg.Done()
				resetDefaultIssueBackend()
			}()
		} else {
			go func() {
				defer wg.Done()
				tracker := defaultIssueBackend()
				if tracker == nil {
					t.Error("defaultIssueBackend() returned nil during concurrent reset/read")
				}
			}()
		}
	}

	wg.Wait()
}

func TestDefaultTracker_LazyInitFromDefaultDeps(t *testing.T) {
	// Verify that when trackerInst is nil, defaultIssueBackend() initializes
	// from defaultDeps.IssueBackend (double-checked locking path).
	t.Setenv("LOOM_DAEMON_SOCKET", "") // ensure IPC path is not triggered
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	// defaultDeps.IssueBackend is initialized by DefaultDeps() to a bdBackend.
	tracker := defaultIssueBackend()
	if tracker == nil {
		t.Fatal("defaultIssueBackend() returned nil on lazy init")
	}
	// Should be the same object as defaultDeps.IssueBackend.
	if tracker != defaultDeps.IssueBackend {
		t.Error("defaultIssueBackend() did not return defaultDeps.IssueBackend on lazy init")
	}
}
