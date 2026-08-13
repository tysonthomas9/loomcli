package backends

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBuildBackendEnvPrependsLoomExecutableDirToPath(t *testing.T) {
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	env := buildBackendEnv("/worktree", "local-planner")
	got, ok := envValue(env, "PATH")
	if !ok {
		t.Fatalf("buildBackendEnv missing PATH in %v", env)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	wantPrefix := filepath.Dir(exe)
	parts := filepath.SplitList(got)
	if len(parts) == 0 || parts[0] != wantPrefix {
		t.Fatalf("PATH = %q, want first entry %q", got, wantPrefix)
	}
}

// --- Resume session ID tests ---

func TestResumeSessionID_SetGetClear(t *testing.T) {
	t.Cleanup(ClearResumeSessionID)

	// Initially empty.
	if got := GetResumeSessionID(); got != "" {
		t.Errorf("GetResumeSessionID() = %q before set, want empty", got)
	}

	SetResumeSessionID("sess-abc-123")
	if got := GetResumeSessionID(); got != "sess-abc-123" {
		t.Errorf("GetResumeSessionID() = %q, want %q", got, "sess-abc-123")
	}

	ClearResumeSessionID()
	if got := GetResumeSessionID(); got != "" {
		t.Errorf("GetResumeSessionID() = %q after clear, want empty", got)
	}
}

func TestConsumeResumeSessionID(t *testing.T) {
	t.Cleanup(ClearResumeSessionID)

	SetResumeSessionID("sess-consume-me")

	// Consume should return the value and clear it atomically.
	got := consumeResumeSessionID()
	if got != "sess-consume-me" {
		t.Errorf("consumeResumeSessionID() = %q, want %q", got, "sess-consume-me")
	}

	// After consume, Get should return empty.
	if got := GetResumeSessionID(); got != "" {
		t.Errorf("GetResumeSessionID() = %q after consume, want empty", got)
	}

	// Second consume should return empty.
	got = consumeResumeSessionID()
	if got != "" {
		t.Errorf("second consumeResumeSessionID() = %q, want empty", got)
	}
}

func TestConsumeResumeSessionID_WhenEmpty(t *testing.T) {
	t.Cleanup(ClearResumeSessionID)

	ClearResumeSessionID()
	got := consumeResumeSessionID()
	if got != "" {
		t.Errorf("consumeResumeSessionID() = %q on empty state, want empty", got)
	}
}

func TestLastCapturedSessionID_SetGetClear(t *testing.T) {
	t.Cleanup(ClearLastCapturedSessionID)

	// Initially empty.
	if got := GetLastCapturedSessionID(); got != "" {
		t.Errorf("GetLastCapturedSessionID() = %q before set, want empty", got)
	}

	SetLastCapturedSessionID("captured-xyz-789")
	if got := GetLastCapturedSessionID(); got != "captured-xyz-789" {
		t.Errorf("GetLastCapturedSessionID() = %q, want %q", got, "captured-xyz-789")
	}

	ClearLastCapturedSessionID()
	if got := GetLastCapturedSessionID(); got != "" {
		t.Errorf("GetLastCapturedSessionID() = %q after clear, want empty", got)
	}
}

func TestResumeSessionID_Concurrent(t *testing.T) {
	t.Cleanup(func() {
		ClearResumeSessionID()
		ClearLastCapturedSessionID()
	})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Writers/consumers for resumeSessionID
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetResumeSessionID("sess-concurrent")
				_ = consumeResumeSessionID()
				_ = GetResumeSessionID()
				ClearResumeSessionID()
			}
		}()
	}

	// Writers/readers for lastCapturedSessionID
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetLastCapturedSessionID("captured-concurrent")
				_ = GetLastCapturedSessionID()
				ClearLastCapturedSessionID()
			}
		}()
	}

	wg.Wait()
}
