package backends

import (
	"os"
	"path/filepath"
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

// --- resolveNotifyToken tests ---

func TestResolveNotifyToken_EnvVar(t *testing.T) {
	t.Setenv("LOOM_NOTIFY_TOKEN", "token-from-env")

	got := resolveNotifyToken()
	if got != "token-from-env" {
		t.Errorf("resolveNotifyToken() = %q, want %q", got, "token-from-env")
	}
}

func TestResolveNotifyToken_FileOnDisk(t *testing.T) {
	// Ensure LOOM_NOTIFY_TOKEN env var is not set.
	t.Setenv("LOOM_NOTIFY_TOKEN", "")

	// Set up a workspace config so GetBeadsDir() returns a known temp dir.
	ResetBeadsDirCache()
	beadsDir := t.TempDir()
	cfg := &LoomConfig{
		DefaultWorkspace: "test",
		Workspaces: map[string]WorkspaceConfig{
			"test": {Path: beadsDir},
		},
	}
	setupWorkspaceConfig(t, cfg)

	// Write the notify.token file.
	tokenContent := "token-from-file"
	if err := os.WriteFile(filepath.Join(beadsDir, "notify.token"), []byte(tokenContent), 0o600); err != nil {
		t.Fatalf("write notify.token: %v", err)
	}

	got := resolveNotifyToken()
	if got != tokenContent {
		t.Errorf("resolveNotifyToken() = %q, want %q", got, tokenContent)
	}
}

func TestResolveNotifyToken_BothFail(t *testing.T) {
	// No env var.
	t.Setenv("LOOM_NOTIFY_TOKEN", "")

	// Set up workspace config pointing to a temp dir without notify.token.
	ResetBeadsDirCache()
	beadsDir := t.TempDir()
	cfg := &LoomConfig{
		DefaultWorkspace: "test",
		Workspaces: map[string]WorkspaceConfig{
			"test": {Path: beadsDir},
		},
	}
	setupWorkspaceConfig(t, cfg)

	got := resolveNotifyToken()
	if got != "" {
		t.Errorf("resolveNotifyToken() = %q, want empty string", got)
	}
}
