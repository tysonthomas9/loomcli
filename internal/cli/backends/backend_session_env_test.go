package backends

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

func TestSetActiveSessionRuntimeEnv(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionRuntimeEnv("/path/to/runtime", "20260321-153042-nova-abc-a3f9b2c1")

	runtimeDir, sid := GetActiveSessionRuntimeEnv()
	if runtimeDir != "/path/to/runtime" {
		t.Errorf("runtimeDir = %q, want %q", runtimeDir, "/path/to/runtime")
	}
	if sid != "20260321-153042-nova-abc-a3f9b2c1" {
		t.Errorf("sid = %q, want %q", sid, "20260321-153042-nova-abc-a3f9b2c1")
	}
}

func TestClearActiveSessionEnv(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionRuntimeEnv("/path/to/runtime", "some-session-id")
	ClearActiveSessionEnv()

	runtimeDir, sid := GetActiveSessionRuntimeEnv()
	if runtimeDir != "" {
		t.Errorf("runtimeDir = %q after clear, want empty", runtimeDir)
	}
	if sid != "" {
		t.Errorf("sid = %q after clear, want empty", sid)
	}
}

func TestActiveSessionEnvVars_WhenSet(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionRuntimeEnv("/home/user/runtime", "sess-123")

	vars := activeSessionEnvVars()
	if len(vars) != 2 {
		t.Fatalf("expected 2 vars, got %d: %v", len(vars), vars)
	}

	// Sort for deterministic comparison.
	sort.Strings(vars)
	want := []string{
		"LOOM_WORKSPACE_RUNTIME_DIR=/home/user/runtime",
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

	// Only session ID set, no runtime dir.
	SetActiveSessionRuntimeEnv("", "sess-only")
	vars := activeSessionEnvVars()
	if len(vars) != 1 {
		t.Fatalf("expected 1 var, got %d: %v", len(vars), vars)
	}
	if vars[0] != "LOOM_SESSION_ID=sess-only" {
		t.Errorf("vars[0] = %q, want %q", vars[0], "LOOM_SESSION_ID=sess-only")
	}

	// Only runtime dir set, no session ID
	SetActiveSessionRuntimeEnv("/path/to/runtime", "")
	vars = activeSessionEnvVars()
	if len(vars) != 1 {
		t.Fatalf("expected 1 var, got %d: %v", len(vars), vars)
	}
	sort.Strings(vars)
	want := []string{
		"LOOM_WORKSPACE_RUNTIME_DIR=/path/to/runtime",
	}
	for i, v := range want {
		if vars[i] != v {
			t.Errorf("vars[%d] = %q, want %q", i, vars[i], v)
		}
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

func TestBuildBackendEnv_IncludesActiveSessionEnv(t *testing.T) {
	t.Cleanup(ClearActiveSessionEnv)

	SetActiveSessionRuntimeEnv("/runtime", "sess-123")

	env := buildBackendEnv("/worktree", "local-planner")
	for _, want := range []string{
		"LOOM_WORKTREE_PATH=/worktree",
		"LOOM_AGENT_NAME=local-planner",
		"LOOM_WORKSPACE_RUNTIME_DIR=/runtime",
		"LOOM_SESSION_ID=sess-123",
	} {
		if !envHas(env, want) {
			t.Fatalf("buildBackendEnv missing %q in %v", want, env)
		}
	}
}

func envHas(env []string, want string) bool {
	for _, got := range env {
		if got == want {
			return true
		}
	}
	return false
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
					SetActiveSessionRuntimeEnv("/runtime", "sid")
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
				_, _ = GetActiveSessionRuntimeEnv()
			}
		}()
	}

	wg.Wait()
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

	// Set up a workspace config so GetWorkspaceRuntimeDir() returns a known temp dir.
	ResetWorkspaceRuntimeDirCache()
	runtimeDir := t.TempDir()
	cfg := &LoomConfig{
		DefaultWorkspace: "test",
		Workspaces: map[string]WorkspaceConfig{
			"test": {Path: runtimeDir},
		},
	}
	setupWorkspaceConfig(t, cfg)

	// Write the notify.token file.
	tokenContent := "token-from-file"
	if err := os.WriteFile(filepath.Join(runtimeDir, "notify.token"), []byte(tokenContent), 0o600); err != nil {
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
	ResetWorkspaceRuntimeDirCache()
	runtimeDir := t.TempDir()
	cfg := &LoomConfig{
		DefaultWorkspace: "test",
		Workspaces: map[string]WorkspaceConfig{
			"test": {Path: runtimeDir},
		},
	}
	setupWorkspaceConfig(t, cfg)

	got := resolveNotifyToken()
	if got != "" {
		t.Errorf("resolveNotifyToken() = %q, want empty string", got)
	}
}
