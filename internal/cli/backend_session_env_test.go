package cli

import (
	"sort"
	"sync"
	"testing"
)

func TestSetActiveSessionEnv(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionEnv("/path/to/.beads", "20260321-153042-nova-abc-a3f9b2c1")

	beadsDir, sid := GetActiveSessionEnv()
	if beadsDir != "/path/to/.beads" {
		t.Errorf("beadsDir = %q, want %q", beadsDir, "/path/to/.beads")
	}
	if sid != "20260321-153042-nova-abc-a3f9b2c1" {
		t.Errorf("sid = %q, want %q", sid, "20260321-153042-nova-abc-a3f9b2c1")
	}
}

func TestClearActiveSessionEnv(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionEnv("/path/to/.beads", "some-session-id")
	ClearActiveSessionEnv()

	beadsDir, sid := GetActiveSessionEnv()
	if beadsDir != "" {
		t.Errorf("beadsDir = %q after clear, want empty", beadsDir)
	}
	if sid != "" {
		t.Errorf("sid = %q after clear, want empty", sid)
	}
}

func TestActiveSessionEnvVars_WhenSet(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionEnv("/home/user/.beads", "sess-123")

	vars := activeSessionEnvVars()
	if len(vars) != 2 {
		t.Fatalf("expected 2 vars, got %d: %v", len(vars), vars)
	}

	// Sort for deterministic comparison.
	sort.Strings(vars)
	want := []string{
		"LOOM_BEADS_DIR=/home/user/.beads",
		"LOOM_SESSION_ID=sess-123",
	}
	sort.Strings(want)
	for i, v := range want {
		if vars[i] != v {
			t.Errorf("vars[%d] = %q, want %q", i, vars[i], v)
		}
	}
}

func TestActiveSessionEnvVars_PartialSet(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	// Only session ID set, no beads dir
	SetActiveSessionEnv("", "sess-only")
	vars := activeSessionEnvVars()
	if len(vars) != 1 {
		t.Fatalf("expected 1 var, got %d: %v", len(vars), vars)
	}
	if vars[0] != "LOOM_SESSION_ID=sess-only" {
		t.Errorf("vars[0] = %q, want %q", vars[0], "LOOM_SESSION_ID=sess-only")
	}

	// Only beads dir set, no session ID
	SetActiveSessionEnv("/path/to/.beads", "")
	vars = activeSessionEnvVars()
	if len(vars) != 1 {
		t.Fatalf("expected 1 var, got %d: %v", len(vars), vars)
	}
	if vars[0] != "LOOM_BEADS_DIR=/path/to/.beads" {
		t.Errorf("vars[0] = %q, want %q", vars[0], "LOOM_BEADS_DIR=/path/to/.beads")
	}
}

func TestActiveSessionEnvVars_WhenEmpty(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	ClearActiveSessionEnv()

	vars := activeSessionEnvVars()
	if len(vars) != 0 {
		t.Errorf("expected empty slice, got %v", vars)
	}
}

func TestActiveSessionEnvVars_Concurrent(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // half writers, half readers

	// Writers
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if j%2 == 0 {
					SetActiveSessionEnv("/beads", "sid")
				} else {
					ClearActiveSessionEnv()
				}
			}
		}(i)
	}

	// Readers
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = activeSessionEnvVars()
				_, _ = GetActiveSessionEnv()
			}
		}()
	}

	wg.Wait()
}
